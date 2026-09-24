package app

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/Tonyjh07/Aquarius/internal/domain/conversation"
	"github.com/Tonyjh07/Aquarius/internal/domain/perm"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

// ErrQuit /quit 命令的退出请求；装配根据此收尾（app 不依赖具体 UI）。
var ErrQuit = errors.New("session: quit")

// defaultTitle 新会话默认标题（首条消息后改写为消息摘要）。
const defaultTitle = "新会话"

// titleRunes 标题摘要的最大长度。
const titleRunes = 24

// SessionDeps 会话依赖（全部端口，测试注入替身）。
type SessionDeps struct {
	Store port.ConversationStore
	Agent *Agent
	IDs   port.IDGen
	Clock port.Clock // persona 节点 CreatedAt
	// SystemPrompt 人格提示：空 = 内置默认；新建会话时快照进 persona 首节点（D20）。
	SystemPrompt string
	// Level 权限等级（D22）；空 = perm.DefaultLevel。
	Level perm.Level
	// SandboxPath 特权目录（<dataDir>/sandbox），仅用于 /permission 展示。
	SandboxPath string
	// PersistLevel 把等级切换写回 config（D22）；nil 时 /permission 切换报错。
	PersistLevel func(perm.Level) error
}

// Session 当前会话 + 命令处理（DESIGN §7.3；M0 启用 /new /list /quit /help，
// 其余命令给出里程碑提示）。命令的文本输出经返回值交给装配根渲染，
// 轮次过程输出走 Presenter——app 不直接接触 IO。
type Session struct {
	store        port.ConversationStore
	agent        *Agent
	ids          port.IDGen
	clock        port.Clock
	systemPrompt string
	level        perm.Level
	sandboxPath  string
	persistLevel func(perm.Level) error
	cur          *conversation.Conversation
}

// NewSession 恢复最近更新的会话；没有则新建并落盘（会话树跨进程持久化）。
func NewSession(ctx context.Context, d SessionDeps) (*Session, error) {
	switch {
	case d.Store == nil:
		return nil, errors.New("session: store 依赖为空")
	case d.Agent == nil:
		return nil, errors.New("session: agent 依赖为空")
	case d.IDs == nil:
		return nil, errors.New("session: IDGen 依赖为空")
	case d.Clock == nil:
		return nil, errors.New("session: Clock 依赖为空")
	}
	level := d.Level
	if level == "" {
		level = perm.DefaultLevel
	}
	s := &Session{
		store:        d.Store,
		agent:        d.Agent,
		ids:          d.IDs,
		clock:        d.Clock,
		systemPrompt: strings.TrimSpace(d.SystemPrompt),
		level:        level,
		sandboxPath:  d.SandboxPath,
		persistLevel: d.PersistLevel,
	}

	list, err := d.Store.List(ctx)
	if err != nil {
		return nil, fmt.Errorf("session: 列出会话: %w", err)
	}
	if len(list) > 0 { // List 按 UpdatedAt 降序，首条即最近
		c, err := d.Store.Load(ctx, list[0].ID)
		if err != nil {
			return nil, fmt.Errorf("session: 恢复会话 %s: %w", list[0].ID, err)
		}
		s.cur = c
		return s, nil
	}
	s.cur, err = s.newConversation(defaultTitle)
	if err != nil {
		return nil, err
	}
	if err := d.Store.Save(ctx, s.cur); err != nil {
		return nil, fmt.Errorf("session: 初始化会话: %w", err)
	}
	return s, nil
}

// newConversation 建会话：Root + persona 首节点（system 角色，D20），Head 落在 persona。
func (s *Session) newConversation(title string) (*conversation.Conversation, error) {
	c := conversation.New(s.ids.ConversationID(), title)
	prompt := s.systemPrompt
	if prompt == "" {
		prompt = defaultSystem
	}
	persona := conversation.Message{
		ID:        s.ids.MessageID(),
		Parent:    conversation.MessageID(c.ID),
		Role:      conversation.RoleSystem,
		Content:   []conversation.Part{{Kind: conversation.PartText, Text: prompt}},
		CreatedAt: s.clock.Now(),
	}
	if err := c.AppendCommitted(persona); err != nil {
		return nil, fmt.Errorf("session: 写入 persona 首节点: %w", err)
	}
	return c, nil
}

// Current 当前会话（供只读展示；一切修改仍走 Session）。
func (s *Session) Current() *conversation.Conversation { return s.cur }

// Handle 处理一次用户输入：命令走 execCommand；普通文本入树 → Turn → 落盘。
// 返回值是命令的文本输出（空 = 无）；Turn 过程输出已由 Presenter 呈现。
func (s *Session) Handle(ctx context.Context, in port.UserInput) (string, error) {
	if in.Command != nil {
		return s.execCommand(ctx, *in.Command)
	}
	if in.Raw != nil {
		return "", errors.New("session: 附件/语音输入尚未启用（里程碑 M3）")
	}
	text := strings.TrimSpace(in.Text)
	if text == "" {
		return "", nil
	}
	s.maybeSetTitle(text)
	if _, err := s.cur.Append(conversation.RoleUser, []conversation.Part{{Kind: conversation.PartText, Text: text}}); err != nil {
		return "", fmt.Errorf("session: 追加消息: %w", err)
	}
	runErr := s.agent.Run(ctx, s.cur)
	// Turn 成败都落盘：error/cancelled 终态的节点同样要持久化。
	if err := s.store.Save(ctx, s.cur); err != nil {
		return "", fmt.Errorf("session: 保存会话: %w", err)
	}
	return "", runErr
}

// maybeSetTitle 首条消息后把默认标题改写为消息摘要。
func (s *Session) maybeSetTitle(text string) {
	if s.cur.Title != "" && s.cur.Title != defaultTitle {
		return
	}
	r := []rune(text)
	if len(r) > titleRunes {
		s.cur.Title = string(r[:titleRunes]) + "…"
		return
	}
	s.cur.Title = string(r)
}

// execCommand 命令分发（DESIGN §7.3 的 M0 子集）。
func (s *Session) execCommand(ctx context.Context, cmd port.Command) (string, error) {
	switch cmd.Name {
	case "new":
		title := strings.TrimSpace(strings.Join(cmd.Args, " "))
		if title == "" {
			title = defaultTitle
		}
		c, err := s.newConversation(title)
		if err != nil {
			return "", err
		}
		if err := s.store.Save(ctx, c); err != nil {
			return "", fmt.Errorf("session: 新建会话: %w", err)
		}
		s.cur = c
		return fmt.Sprintf("已新建会话 %s", c.ID), nil

	case "list":
		sums, err := s.store.List(ctx)
		if err != nil {
			return "", fmt.Errorf("session: 列出会话: %w", err)
		}
		if len(sums) == 0 {
			return "（暂无会话）", nil
		}
		var b strings.Builder
		for _, sm := range sums {
			mark := " "
			if sm.ID == s.cur.ID {
				mark = "*"
			}
			fmt.Fprintf(&b, "%s %s  %d条  %s\n", mark, sm.ID, sm.MessageN, sm.Title)
		}
		return strings.TrimRight(b.String(), "\n"), nil

	case "title":
		if len(cmd.Args) == 0 {
			return fmt.Sprintf("当前标题: %s", s.cur.Title), nil
		}
		title := strings.TrimSpace(strings.Join(cmd.Args, " "))
		s.cur.Title = title
		if err := s.store.Save(ctx, s.cur); err != nil {
			return "", fmt.Errorf("session: 保存标题: %w", err)
		}
		return fmt.Sprintf("标题已改为: %s", title), nil

	case "exit":
		return "", ErrQuit

	case "permission":
		if len(cmd.Args) == 0 {
			return s.permissionReport(), nil
		}
		if len(cmd.Args) > 1 {
			return "", errors.New("用法: /permission [read-only|strict|permissive|full-access]")
		}
		level, err := perm.Parse(cmd.Args[0])
		if err != nil {
			return "", err
		}
		if level == s.level {
			return fmt.Sprintf("权限等级已是 %s", level), nil
		}
		if s.persistLevel == nil {
			return "", errors.New("session: 未配置权限持久化，无法切换（D22 要求写回 config）")
		}
		if err := s.persistLevel(level); err != nil {
			return "", fmt.Errorf("session: 写回 config 失败: %w", err)
		}
		s.level = level
		return fmt.Sprintf("权限等级已切换为 %s（已写回 config，立即生效）", level), nil

	case "quit":
		return "", ErrQuit

	case "help":
		return strings.Join([]string{
			"/new [标题]             新建会话",
			"/list                   列出会话",
			"/title [文本]           查看/改写会话标题",
			"/permission [等级]      查看/切换权限等级（read-only/strict/permissive/full-access）",
			"/quit, /exit            退出",
		}, "\n"), nil

	case "goto", "edit", "branch", "rm", "memory", "model", "jobs", "plugin":
		return "", fmt.Errorf("命令 /%s 尚未启用（里程碑 M1+）", cmd.Name)

	default:
		return "", fmt.Errorf("未知命令 /%s（/help 查看可用命令）", cmd.Name)
	}
}

// permissionReport /permission 无参输出：当前等级 + 全档免确认矩阵（由 perm 判定推导，单一事实源）+ 特权目录。
func (s *Session) permissionReport() string {
	var b strings.Builder
	fmt.Fprintf(&b, "当前权限等级: %s（默认 strict）\n", s.level)
	fmt.Fprintf(&b, "特权目录: %s\n", s.sandboxPath)
	b.WriteString("免确认矩阵（矩阵格外一律逐次确认，D22）:\n")
	for _, l := range perm.Levels {
		mark := "  "
		if l == s.level {
			mark = "* "
		}
		sandbox, other, free := l.Matrix()
		tools := "工具 Confirm 逐次"
		if free {
			tools = "工具全免确认"
		}
		fmt.Fprintf(&b, "%s%-12s 特权 %s | 其他 %-2s | %s\n", mark, l, sandbox, other, tools)
	}
	return strings.TrimRight(b.String(), "\n")
}
