package app

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
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
	// Confirmer 逐次确认（/rm 二次确认等）；nil 时破坏性命令报"未配置确认器"。
	Confirmer port.Confirmer
	// Jobs 后台任务管理（/jobs，DESIGN §7.3，M3）；nil 时 /jobs 报"未配置任务管理器"。
	Jobs port.JobManager
	// OpenMemory 用系统编辑器打开记忆文档（D24，装配根实现）；返回实际打开路径。
	// nil 时 /memory 报"未配置编辑器"。
	OpenMemory func(name string) (string, error)
}

// Session 当前会话 + 命令处理（DESIGN §7.3；M0 起启用 /new /list /title /compact /
// /permission /quit，M1 启用树交互 /goto /edit /branch /rm，M3 启用 /jobs，
// /model /plugin 留待其里程碑）。
// 命令的文本输出经返回值交给装配根渲染，轮次过程输出走 Presenter——app 不直接接触 IO。
type Session struct {
	store        port.ConversationStore
	agent        *Agent
	ids          port.IDGen
	clock        port.Clock
	systemPrompt string
	level        perm.Level
	sandboxPath  string
	persistLevel func(perm.Level) error
	confirmer    port.Confirmer
	jobs         port.JobManager
	openMemory   func(name string) (string, error)
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
		confirmer:    d.Confirmer,
		jobs:         d.Jobs,
		openMemory:   d.OpenMemory,
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
		if runErr != nil {
			// 两个错误都保留：Turn 错误为主，保存失败附注。
			return "", fmt.Errorf("session: %w（且保存失败: %v）", runErr, err)
		}
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

// execCommand 命令分发（DESIGN §7.3；树交互命令随 M1 启用）。
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

	case "goto":
		if len(cmd.Args) != 1 {
			return "", errors.New("用法: /goto <id>（id 可用 /branch 查看，支持唯一前缀）")
		}
		id, err := s.resolveNode(cmd.Args[0])
		if err != nil {
			return "", err
		}
		if err := s.cur.Checkout(id); err != nil {
			return "", fmt.Errorf("session: 移动 Head: %w", err)
		}
		if err := s.persist(ctx); err != nil {
			return "", err
		}
		return fmt.Sprintf("Head → %s", id), nil

	case "edit":
		target, mode, text, err := parseEditArgs(cmd.Args)
		if err != nil {
			return "", err
		}
		id, err := s.resolveNode(target)
		if err != nil {
			return "", err
		}
		m, err := s.cur.Revise(id, []conversation.Part{{Kind: conversation.PartText, Text: text}}, mode)
		if err != nil {
			return "", fmt.Errorf("session: 修订: %w", err)
		}
		if err := s.persist(ctx); err != nil {
			return "", err
		}
		note := "旧分支保留"
		if mode == conversation.Carry {
			note = "后续历史已转移"
		}
		return fmt.Sprintf("已修订 %s → %s（%s，%s；Head → %s）", id, m.ID, mode, note, s.cur.Head), nil

	case "branch":
		if len(cmd.Args) > 1 {
			return "", errors.New("用法: /branch [id]（缺省取当前 Head）")
		}
		id := s.cur.Head
		if len(cmd.Args) == 1 {
			var err error
			if id, err = s.resolveNode(cmd.Args[0]); err != nil {
				return "", err
			}
		}
		self, ok := s.cur.Find(id)
		if !ok {
			return "", fmt.Errorf("session: %w", conversation.ErrNotFound)
		}
		// 展示自身 + 同级分叉（新旧版本对比）+ 下级（逐层下钻可发现深层 id）；
		// Root 的"同级"按 D15 即其孩子，改标"顶层消息"且不重复列出下级。
		isRoot := self.Role == conversation.RoleRoot
		var b strings.Builder
		fmt.Fprintf(&b, "当前: %s\n", branchLine(self, s.cur.Head, s.cur.RevisedFrom))
		sibsLabel := "同级分叉"
		if isRoot {
			sibsLabel = "顶层消息"
		}
		sibs := s.cur.Branches(id)
		if len(sibs) == 0 {
			fmt.Fprintf(&b, "%s: 无", sibsLabel)
		} else {
			fmt.Fprintf(&b, "%s（%d 条，不含自身）:", sibsLabel, len(sibs))
			for _, m := range sibs {
				b.WriteString("\n  " + branchLine(m, s.cur.Head, s.cur.RevisedFrom))
			}
		}
		if !isRoot {
			kids := s.cur.Children[id]
			if len(kids) == 0 {
				b.WriteString("\n下级: 无")
			} else {
				fmt.Fprintf(&b, "\n下级（%d 条）:", len(kids))
				for _, kid := range kids {
					if m, ok := s.cur.Find(kid); ok {
						b.WriteString("\n  " + branchLine(m, s.cur.Head, s.cur.RevisedFrom))
					}
				}
			}
		}
		return b.String(), nil

	case "rm":
		if len(cmd.Args) != 1 {
			return "", errors.New("用法: /rm <id>（剪掉该节点及整棵子树，二次确认）")
		}
		id, err := s.resolveNode(cmd.Args[0])
		if err != nil {
			return "", err
		}
		m, _ := s.cur.Find(id)
		if m.Role == conversation.RoleRoot {
			return "", errors.New("session: root 即会话，不可删除（D19）") // 先于确认拒绝，不消费确认应答
		}
		if s.confirmer == nil {
			return "", errors.New("session: 未配置确认器（SessionDeps.Confirmer），/rm 需要二次确认")
		}
		size := subtreeSize(s.cur, id)
		prompt := fmt.Sprintf("确认删除 %s 及其子树（至少 %d 条节点）？此操作不可恢复", id, size)
		ok, err := s.confirmer.Confirm(ctx, prompt)
		if err != nil {
			return "", fmt.Errorf("session: 确认删除: %w", err)
		}
		if !ok {
			return fmt.Sprintf("已取消删除 %s", id), nil
		}
		before := len(s.cur.Nodes)
		if err := s.cur.Prune(id); err != nil {
			return "", fmt.Errorf("session: 删除: %w", err)
		}
		removed := before - len(s.cur.Nodes) // 实际删除数：Prune 可能连带删除子树外的失联节点（D18）
		if err := s.persist(ctx); err != nil {
			// 删除绝不"半生效"：落盘失败即回滚到最近一次成功保存的状态。
			if c, lerr := s.store.Load(ctx, s.cur.ID); lerr == nil {
				s.cur = c
				return "", fmt.Errorf("session: 落盘失败，删除已回滚（会话树未改动）: %w", err)
			}
			return "", fmt.Errorf("session: 落盘失败且无法回滚，删除仅存在于内存（下次成功保存会写盘，请谨慎继续）: %w", err)
		}
		return fmt.Sprintf("已删除 %s 子树（%d 条节点；Head → %s）", id, removed, s.cur.Head), nil

	case "exit":
		return "", ErrQuit

	case "compact":
		node, absorbed, err := s.agent.Compact(ctx, s.cur)
		if errors.Is(err, ErrNothingToCompact) {
			return "无需压缩：摘要之上没有新的历史", nil
		}
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return "已取消压缩（会话未改动）", nil
		}
		if err != nil {
			return "", err
		}
		if err := s.store.Save(ctx, s.cur); err != nil {
			return "", fmt.Errorf("session: 保存会话: %w", err)
		}
		return fmt.Sprintf("已压缩 %d 条历史 → 1 条摘要（in=%d out=%d tokens）",
			absorbed, node.Usage.InputTokens, node.Usage.OutputTokens), nil

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
		return fmt.Sprintf("权限等级已切换为 %s（已写回 config；工具链路即时生效）", level), nil

	case "memory":
		if s.openMemory == nil {
			return "", errors.New("session: 未配置记忆编辑器（SessionDeps.OpenMemory）")
		}
		if len(cmd.Args) > 1 {
			return "", errors.New("用法: /memory [会话id前缀]（缺省打开全局记忆文件）")
		}
		name := port.GlobalMemoryDoc
		if len(cmd.Args) == 1 {
			id, err := s.resolveConversation(ctx, cmd.Args[0])
			if err != nil {
				return "", err
			}
			name = port.SessionMemoryDoc(id)
		}
		path, err := s.openMemory(name)
		if err != nil {
			return "", fmt.Errorf("session: 打开记忆文件: %w", err)
		}
		return fmt.Sprintf("已用系统编辑器打开 %s（保存后下次读取即生效）", path), nil

	case "usage":
		rep, err := s.agent.UsageReport(ctx, s.cur)
		if err != nil {
			return "", fmt.Errorf("session: 估算用量: %w", err)
		}
		var b strings.Builder
		pct := 100 * float64(rep.Estimate) / float64(rep.MaxCtx)
		tpct := 100 * float64(rep.CompactAt) / float64(rep.MaxCtx)
		mode := fmt.Sprintf("估算（服务端 usage 校准 ×%.2f，D26③）", rep.Ratio)
		if rep.Exact {
			mode = "精确（适配器 TokenCounter，D26②）"
		}
		fmt.Fprintf(&b, "当前上下文:≈%d / %d tokens（%.1f%%，自动压缩阈值 %d = %.0f%%）\n",
			rep.Estimate, rep.MaxCtx, pct, rep.CompactAt, tpct)
		fmt.Fprintf(&b, "计数方式: %s\n", mode)
		fmt.Fprintf(&b, "上轮实测: in=%d out=%d tokens（服务端返回）\n", rep.LastIn, rep.LastOut)
		fmt.Fprintf(&b, "本会话累计(Path): in=%d out=%d tokens（%d 次生成）",
			rep.SumIn, rep.SumOut, rep.Gen)
		return b.String(), nil

	case "jobs":
		return s.jobsReport(ctx, cmd.Args)

	case "quit":
		return "", ErrQuit

	case "help":
		return strings.Join([]string{
			"/new [标题]             新建会话",
			"/list                   列出会话",
			"/title [文本]           查看/改写会话标题",
			"/goto <id>              Head 移到任意节点（分支导航；id 支持唯一前缀）",
			"/edit <id> [--keep] <文本>  Revise：缺省 Fresh 开新分支；--keep 边转移保留后续历史",
			"/branch [id]            展示同级分叉与下级（Root 显示顶层消息；缺省当前 Head）",
			"/rm <id>                删除节点及整棵子树（二次确认）",
			"/compact                触发上下文压缩（生成 system 摘要节点，D21）",
			"/permission [等级]      查看/切换权限等级（read-only/strict/permissive/full-access）",
			"/memory [会话id]        用系统编辑器打开记忆文件（缺省全局 memories.md，D24）",
			"/usage                  查看 token 用量（上下文占用/上轮实测/会话累计，三级计数链 D26）",
			"/jobs [list|logs <id> [行数]|kill <id>]  后台任务管理（job_start 启动的任务，DESIGN §7.3）",
			"/quit, /exit            退出",
		}, "\n"), nil

	case "model", "plugin":
		return "", fmt.Errorf("命令 /%s 尚未启用（里程碑 M3/M4，见 DESIGN §12）", cmd.Name)

	default:
		return "", fmt.Errorf("未知命令 /%s（/help 查看可用命令）", cmd.Name)
	}
}

// persist 落盘当前会话。
func (s *Session) persist(ctx context.Context) error {
	if err := s.store.Save(ctx, s.cur); err != nil {
		return fmt.Errorf("session: 保存会话: %w", err)
	}
	return nil
}

// resolveNode 把用户给出的节点标识解析为树中节点：先精确匹配，再按唯一前缀匹配
// （Root 节点 ID = 会话 ID，/goto <会话ID> 即回根）；前缀命中多个时报歧义。
func (s *Session) resolveNode(arg string) (conversation.MessageID, error) {
	arg = strings.TrimSpace(arg)
	if arg == "" {
		return "", errors.New("缺少节点 id")
	}
	if _, ok := s.cur.Find(conversation.MessageID(arg)); ok {
		return conversation.MessageID(arg), nil
	}
	var hits []conversation.MessageID
	for id := range s.cur.Nodes {
		if strings.HasPrefix(string(id), arg) {
			hits = append(hits, id)
		}
	}
	switch len(hits) {
	case 0:
		return "", fmt.Errorf("会话树中没有节点 %q（/branch 可查看同级 id）", arg)
	case 1:
		return hits[0], nil
	default:
		sort.Slice(hits, func(i, j int) bool { return hits[i] < hits[j] })
		ids := make([]string, len(hits))
		for i, h := range hits {
			ids[i] = string(h)
		}
		return "", fmt.Errorf("节点标识 %q 有歧义（命中 %d 个: %s），请加长前缀", arg, len(hits), strings.Join(ids, ", "))
	}
}

// resolveConversation 按会话 ID（精确或唯一前缀）解析会话（/memory [会话id] 用）；
// 前缀命中多个时报歧义（会话列表按时间降序，命中序确定）。
func (s *Session) resolveConversation(ctx context.Context, arg string) (conversation.ID, error) {
	arg = strings.TrimSpace(arg)
	if arg == "" {
		return s.cur.ID, nil
	}
	list, err := s.store.List(ctx)
	if err != nil {
		return "", fmt.Errorf("session: 列出会话: %w", err)
	}
	var hits []conversation.ID
	for _, sm := range list {
		if string(sm.ID) == arg {
			return sm.ID, nil
		}
		if strings.HasPrefix(string(sm.ID), arg) {
			hits = append(hits, sm.ID)
		}
	}
	switch len(hits) {
	case 0:
		return "", fmt.Errorf("没有会话 %q（/list 查看可用会话）", arg)
	case 1:
		return hits[0], nil
	default:
		ids := make([]string, len(hits))
		for i, h := range hits {
			ids[i] = string(h)
		}
		return "", fmt.Errorf("会话标识 %q 有歧义（命中 %d 个: %s），请加长前缀", arg, len(hits), strings.Join(ids, ", "))
	}
}

// jobsReport /jobs 命令（DESIGN §7.3）：list（缺省）/ logs <id> [行数] / kill <id>。
// 任务由模型经 job_* 工具启动，状态与日志同源（port.JobManager）。
func (s *Session) jobsReport(ctx context.Context, args []string) (string, error) {
	if s.jobs == nil {
		return "", errors.New("session: 未配置任务管理器（SessionDeps.Jobs），/jobs 不可用")
	}
	sub := "list"
	if len(args) > 0 {
		sub, args = args[0], args[1:]
	}
	switch sub {
	case "list":
		if len(args) != 0 {
			return "", errors.New("用法: /jobs [list|logs <id> [行数]|kill <id>]")
		}
		jobs, err := s.jobs.List(ctx)
		if err != nil {
			return "", fmt.Errorf("session: 列出任务: %w", err)
		}
		if len(jobs) == 0 {
			return "（暂无后台任务；模型可用 job_start 启动）", nil
		}
		var b strings.Builder
		for _, j := range jobs {
			fmt.Fprintf(&b, "%s  %-8s pid=%-7d %s  %s\n",
				j.ID, j.Status, j.PID, j.StartedAt.Format("15:04:05"), jobCommand(j.Spec))
		}
		return strings.TrimRight(b.String(), "\n"), nil

	case "logs":
		if len(args) < 1 || len(args) > 2 {
			return "", errors.New("用法: /jobs logs <id> [行数]（缺省最后 50 行）")
		}
		id, err := s.resolveJob(ctx, args[0])
		if err != nil {
			return "", err
		}
		tail := defaultJobLogsTail
		if len(args) == 2 {
			n, cerr := strconv.Atoi(args[1])
			if cerr != nil || n <= 0 {
				return "", errors.New("/jobs logs 行数须为正整数")
			}
			tail = n
		}
		text, err := s.jobs.Logs(ctx, id, tail)
		if err != nil {
			return "", fmt.Errorf("session: 读取 %s 日志: %w", id, err)
		}
		if strings.TrimSpace(text) == "" {
			return fmt.Sprintf("任务 %s 日志为空", id), nil
		}
		return fmt.Sprintf("任务 %s 日志（末尾 %d 行）:\n%s", id, tail, text), nil

	case "kill":
		if len(args) != 1 {
			return "", errors.New("用法: /jobs kill <id>")
		}
		id, err := s.resolveJob(ctx, args[0])
		if err != nil {
			return "", err
		}
		if err := s.jobs.Kill(ctx, id); err != nil {
			return "", fmt.Errorf("session: 终止任务: %w", err)
		}
		return fmt.Sprintf("已终止 %s", id), nil

	default:
		return "", errors.New("用法: /jobs [list|logs <id> [行数]|kill <id>]")
	}
}

// defaultJobLogsTail /jobs logs 缺省行数（与 job_logs 工具一致）。
const defaultJobLogsTail = 50

// resolveJob 解析任务标识（精确优先 + 唯一前缀，port 共用实现）。
func (s *Session) resolveJob(ctx context.Context, arg string) (port.JobID, error) {
	list, err := s.jobs.List(ctx)
	if err != nil {
		return "", fmt.Errorf("session: 列出任务: %w", err)
	}
	id, err := port.ResolveJobID(list, arg)
	if err != nil {
		return "", fmt.Errorf("session: %w（/jobs 查看可用 ID）", err)
	}
	return id, nil
}

// jobCommand JobSpec 的可读命令行（/jobs 列表展示用）。
func jobCommand(spec port.JobSpec) string {
	parts := make([]string, 0, 1+len(spec.Args))
	parts = append(parts, spec.Command)
	return strings.Join(append(parts, spec.Args...), " ")
}

// parseEditArgs 解析 /edit <id> [--keep] <文本>：--keep 只在紧跟 id 的位置识别
// （用法与文档一致），修订文本里的字面 "--keep" 原样保留，返回（目标, 模式, 文本）。
func parseEditArgs(args []string) (string, conversation.KeepMode, string, error) {
	usage := errors.New("用法: /edit <id> [--keep] <文本>（缺省 Fresh 开新分支；--keep 紧跟 id 转移后续历史）")
	if len(args) > 0 && args[0] == "--keep" {
		return "", conversation.Fresh, "", usage // flag 在 id 前：按用法拒绝
	}
	mode := conversation.Fresh
	rest := args
	if len(args) > 1 && args[1] == "--keep" {
		mode = conversation.Carry
		rest = append([]string{args[0]}, args[2:]...)
	}
	if len(rest) < 2 {
		return "", mode, "", usage
	}
	text := strings.TrimSpace(strings.Join(rest[1:], " "))
	if text == "" {
		return "", mode, "", errors.New("用法: /edit <id> [--keep] <文本>（修订文本不可为空）")
	}
	return rest[0], mode, text, nil
}

// subtreeSize 统计 id 及其整棵子树的节点数（/rm 确认提示用；树无环，遍历必终止）。
func subtreeSize(c *conversation.Conversation, id conversation.MessageID) int {
	n := 0
	var walk func(conversation.MessageID)
	walk = func(x conversation.MessageID) {
		n++
		for _, ch := range c.Children[x] {
			walk(ch)
		}
	}
	walk(id)
	return n
}

// branchLine 渲染一行分支摘要：<id> [角色] 内容预览 [← Head] [（修订自 <id>）]。
func branchLine(m conversation.Message, head conversation.MessageID, revisedFrom map[conversation.MessageID]conversation.MessageID) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s [%s] %s", m.ID, m.Role, nodeSummary(m))
	if m.ID == head {
		b.WriteString(" ← Head")
	}
	if old, ok := revisedFrom[m.ID]; ok {
		fmt.Fprintf(&b, "（修订自 %s）", old)
	}
	return b.String()
}

// nodeSummary 节点内容的单行预览（分支列表/回放树用）。
// 内容与工具输出均为不可信数据，只渲染不执行（DESIGN §9）。
func nodeSummary(m conversation.Message) string {
	const maxRunes = 40
	var parts []string
	for _, p := range m.Content {
		if s := strings.TrimSpace(p.Text); s != "" {
			parts = append(parts, s)
		}
	}
	s := strings.Join(parts, " ")
	if s == "" && len(m.ToolCalls) > 0 {
		names := make([]string, len(m.ToolCalls))
		for i, c := range m.ToolCalls {
			names[i] = c.Name
		}
		s = "（调用 " + strings.Join(names, ", ") + "）"
	}
	if s == "" && m.ToolResult != nil {
		if m.ToolResult.OK {
			s = strings.TrimSpace(m.ToolResult.Output)
		} else {
			s = "错误: " + m.ToolResult.Err
		}
	}
	if s == "" {
		return "（空）"
	}
	s = strings.Join(strings.Fields(s), " ") // 压成单行
	if r := []rune(s); len(r) > maxRunes {
		return string(r[:maxRunes]) + "…"
	}
	return s
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
