package app

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"

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
	// Env 运行环境块（D37）：附加在 SystemPrompt/默认提示之后一并快照进 persona。
	Env RuntimeEnv
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
	// Ingestors 多模态输入摄取器（M3 管线；/attach 等触发命令面留 §14，D27/D33）；
	// nil 时 Raw 输入报"没有可用的摄取器"。
	Ingestors []port.Ingestor
	// UI 轮次外的过程事件（摄取 Note 等 NoticeEvent）；nil 时跳过呈现。
	UI port.Presenter
	// OpenMemory 用系统编辑器打开记忆文档（D24，装配根实现）；返回实际打开路径。
	// nil 时 /memory 报"未配置编辑器"。
	OpenMemory func(name string) (string, error)
	// Plugins 插件管理面（/plugin，DESIGN §7.3 / M4）；nil 时 /plugin 报"未配置"。
	Plugins PluginAdmin
	// ListModels 可用模型列表（LLM.Models，/model 无参展示，D32）；nil 时报"未配置模型服务"。
	ListModels func(ctx context.Context) ([]port.ModelInfo, error)
	// PersistModel 把切换后的模型写回 config（D32，同 /permission 模式）；
	// nil 时 /model 有参报"未配置持久化"。
	PersistModel func(name string) error
	// ProviderName 当前生效 provider 名（D110②：/model 无参展示；装配根注入
	// 静态值——provider 运行态不热切，切换 = 重启生效）；nil 时省略该行。
	ProviderName func() string
	// PersistThink 把原生思考开关写回 config（D34，同 /permission 模式）；
	// nil 时 /think 有参报"未配置持久化"。
	PersistThink func(on bool) error
	// PersistEffort 把推理档位写回 config（D34；"" = 清除）；nil 时 /effort 有参报错。
	PersistEffort func(level string) error
}

// CommandHandler 动态命令处理器（DESIGN §6.1 扩展点 #4 / §7.3 MCP prompts）：
// 装配根随插件启停注册 `/mcp:<server>:<prompt>` 等动态命令；静态命令优先命中。
type CommandHandler func(ctx context.Context, args []string) (string, error)

// Session 当前会话 + 命令处理（DESIGN §7.3；M0 起启用 /new /list /title /compact /
// /permission /quit，M1 启用树交互 /goto /edit /branch /rm，M3 启用 /jobs，
// M4 启用 /plugin 与动态 /mcp: 命令、/model，思考控制 /think /effort 随 D34 启用）。
// 命令的文本输出经返回值交给装配根渲染，轮次过程输出走 Presenter——app 不直接接触 IO。
type Session struct {
	store         port.ConversationStore
	agent         *Agent
	ids           port.IDGen
	clock         port.Clock
	systemPrompt  string
	env           RuntimeEnv
	level         perm.Level
	sandboxPath   string
	persistLevel  func(perm.Level) error
	confirmer     port.Confirmer
	jobs          port.JobManager
	ingestors     []port.Ingestor
	ui            port.Presenter
	openMemory    func(name string) (string, error)
	plugins       PluginAdmin
	listModels    func(ctx context.Context) ([]port.ModelInfo, error)
	persistModel  func(name string) error
	providerName  func() string
	persistThink  func(on bool) error
	persistEffort func(level string) error
	cur           *conversation.Conversation
	// tree 会话树只读快照（D80/§7.5，前置 A）：树变更后由 publishTree 整体重建并原子
	// 发布，UI 事件循环 goroutine 经 Branches 无锁读（见 tree.go）。
	tree atomic.Pointer[treeSnapshot]
	// facts 会话展示事实快照（D82/§7.6，前置 B）：与 tree 同组发布点原子发布，UI 经
	// Facts 无锁读（见 facts.go）；重算经签名门控，命令类输入不空转。
	facts atomic.Pointer[factsSnapshot]
	// resumed 本次启动是否恢复了既有会话（NewSession 走 Load 分支）；
	// ReplayHistory 仅在恢复时回放（新建会话无历史可回放，D40/§7.4）。
	resumed bool

	// dynMu 保护 dynamic/dynamicMeta：REPL 主循环读、插件启停回调（可能来自崩溃重启的
	// watch goroutine）写，须加锁（§10 并发语义）。
	dynMu       sync.Mutex
	dynamic     map[string]CommandHandler
	dynamicMeta map[string]port.CommandInfo // 清单元数据（D103：名称→Names/Desc，键与 dynamic 对齐）
}

// SetDynamicCommands 替换动态命令表（装配根随插件启停刷新；锁内整体换）。
// meta 携带清单元数据（D103/S2b-1：补全浮层与 /help 展示用；缺项按名生成无描述条目）。
func (s *Session) SetDynamicCommands(cmds map[string]CommandHandler, meta map[string]port.CommandInfo) {
	s.dynMu.Lock()
	defer s.dynMu.Unlock()
	s.dynamic = cmds
	s.dynamicMeta = meta
}

// dynamicCommand 查动态命令（未命中返回 nil）。
func (s *Session) dynamicCommand(name string) CommandHandler {
	s.dynMu.Lock()
	defer s.dynMu.Unlock()
	return s.dynamic[name]
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
		store:         d.Store,
		agent:         d.Agent,
		ids:           d.IDs,
		clock:         d.Clock,
		systemPrompt:  strings.TrimSpace(d.SystemPrompt),
		env:           d.Env,
		level:         level,
		sandboxPath:   d.SandboxPath,
		persistLevel:  d.PersistLevel,
		confirmer:     d.Confirmer,
		jobs:          d.Jobs,
		ingestors:     d.Ingestors,
		ui:            d.UI,
		openMemory:    d.OpenMemory,
		plugins:       d.Plugins,
		listModels:    d.ListModels,
		persistModel:  d.PersistModel,
		providerName:  d.ProviderName,
		persistThink:  d.PersistThink,
		persistEffort: d.PersistEffort,
		dynamic:       map[string]CommandHandler{},
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
		s.resumed = true
		s.publishTree()     // D80/§7.5：构造完成即发布树只读快照（UI 早于首帧即可读）
		s.publishFacts(ctx) // D82/§7.6：事实快照同组发布（恢复会话首帧 tooltip 即可用）
		return s, nil
	}
	s.cur, err = s.newConversation(defaultTitle)
	if err != nil {
		return nil, err
	}
	if err := d.Store.Save(ctx, s.cur); err != nil {
		return nil, fmt.Errorf("session: 初始化会话: %w", err)
	}
	s.publishTree()     // D80/§7.5：构造完成即发布树只读快照
	s.publishFacts(ctx) // D82/§7.6：事实快照同组发布
	return s, nil
}

// newConversation 建会话：Root + persona 首节点（system 角色，D20），Head 落在 persona。
func (s *Session) newConversation(title string) (*conversation.Conversation, error) {
	c := conversation.New(s.ids.ConversationID(), title)
	prompt := s.systemPrompt
	if prompt == "" {
		prompt = defaultSystem
	}
	prompt = systemWithEnv(prompt, s.env) // D37：环境块随 config 快照进 persona
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

// ReplayHistory 启动历史回放（D40/§7.4）：把当前分支"模型可见"的历史
// （水位 → Head；无摘要则 persona 之后 → Head）经 Presenter 逐节点呈现，
// 并先发 NoticeEvent 说明恢复了哪个会话。新建会话无历史，直接返回 0。
// 仅启动恢复时回放一次；/switch 切会话走 replayHistory（D75）。
// 返回回放的节点数（不含提示）。
func (s *Session) ReplayHistory(ctx context.Context) (int, error) {
	if !s.resumed {
		return 0, nil
	}
	return s.replayHistory(ctx, "已恢复")
}

// replayHistory 回放当前会话的可见历史（D40/§7.4 口径：水位 → Head，persona 不回放），
// 先发 NoticeEvent 说明会话身份与条数；verb 是提示动词——启动「已恢复」、切换「已切换到」
// （D75）。无 UI 或仅 Root/persona 时直接返回 0。
func (s *Session) replayHistory(ctx context.Context, verb string) (int, error) {
	if s.ui == nil {
		return 0, nil
	}
	path := s.cur.Path()
	personaIdx, watermarkIdx := waterline(path)
	start := 1 // 跳过 Root
	if personaIdx >= 0 {
		start = personaIdx + 1 // 跳过 persona（配置快照，非对话内容）
	}
	if watermarkIdx >= 0 {
		start = watermarkIdx // 有压缩摘要：从摘要节点起（含），与 assemblePath 同口径
	}
	if start >= len(path) {
		return 0, nil // 仅 Root/persona：没有可回放的历史
	}
	n := len(path) - start
	notice := fmt.Sprintf("%s会话 %s (%s)，回放 %d 条历史", verb, s.cur.Title, s.cur.ID, n)
	if err := s.ui.Emit(ctx, port.NoticeEvent{Text: notice}); err != nil {
		return 0, fmt.Errorf("session: 回放提示: %w", err)
	}
	for i := start; i < len(path); i++ {
		if err := s.ui.Emit(ctx, port.HistoryEvent{Message: path[i]}); err != nil {
			return i - start, fmt.Errorf("session: 回放历史: %w", err)
		}
	}
	return n, nil
}

// emitClear 发清屏事件（D75）：/switch 与 /new 前置——UI 丢弃已呈现内容，
// 后续 HistoryEvent 从空白铺开。无 UI 时静默。
func (s *Session) emitClear(ctx context.Context) error {
	if s.ui == nil {
		return nil
	}
	if err := s.ui.Emit(ctx, port.ClearEvent{}); err != nil {
		return fmt.Errorf("session: 清屏: %w", err)
	}
	return nil
}

// Handle 处理一次用户输入：命令走 execCommand；Raw 走多模态摄取管线（M3）；
// 普通文本入树 → Turn → 落盘。返回值是命令的文本输出（空 = 无）；Turn 过程输出已由 Presenter 呈现。
func (s *Session) Handle(ctx context.Context, in port.UserInput) (string, error) {
	// D80/§7.5：树变更只发生在 Handle 内（命令面与 Turn），返回前重发一次只读快照——
	// UI 侧每帧无锁读，本 defer 是唯一的变更后发布点。D82/§7.6：事实快照同点重发
	// （签名门控：无变更的命令输入不重算）。
	defer s.publishTree()
	defer s.publishFacts(ctx)
	if in.Command != nil {
		return s.execCommand(ctx, *in.Command)
	}
	if in.Raw != nil {
		return s.ingestAndRun(ctx, *in.Raw)
	}
	text := strings.TrimSpace(in.Text)
	if text == "" {
		return "", nil
	}
	s.maybeSetTitle(text)
	if _, err := s.cur.Append(conversation.RoleUser, []conversation.Part{{Kind: conversation.PartText, Text: text}}); err != nil {
		return "", fmt.Errorf("session: 追加消息: %w", err)
	}
	return "", s.runTurn(ctx)
}

// runTurn 执行一轮 Turn 并落盘：成败都保存（error/cancelled 终态的节点同样持久化）。
func (s *Session) runTurn(ctx context.Context) error {
	runErr := s.agent.Run(ctx, s.cur)
	if err := s.store.Save(ctx, s.cur); err != nil {
		if runErr != nil {
			// 两个错误都保留：Turn 错误为主，保存失败附注。
			return fmt.Errorf("session: %w（且保存失败: %v）", runErr, err)
		}
		return fmt.Errorf("session: 保存会话: %w", err)
	}
	return runErr
}

// ingestAndRun 多模态输入管线（M3，D27：/attach 等触发命令面留 §14，D27/D33）：
// RawInput → Ingestor 分派 → Part 入树（user）→ Turn → 落盘；摄取 Note 经 NoticeEvent 提示。
func (s *Session) ingestAndRun(ctx context.Context, raw port.RawInput) (string, error) {
	parts, note, err := s.ingest(ctx, raw)
	if err != nil {
		return "", fmt.Errorf("session: 摄取输入: %w", err)
	}
	if len(parts) == 0 {
		return "", errors.New("session: 摄取未产出内容")
	}
	s.maybeSetTitle(ingestTitle(parts))
	if _, err := s.cur.Append(conversation.RoleUser, parts); err != nil {
		return "", fmt.Errorf("session: 追加消息: %w", err)
	}
	if note != "" && s.ui != nil {
		_ = s.ui.Emit(ctx, port.NoticeEvent{Text: note})
	}
	return "", s.runTurn(ctx)
}

// ingest 分派 RawInput：Kind=text 直通（DESIGN §7.2 文本摄取），
// 其余按注册序找首个 Accepts 的 Ingestor（file/clipboard 由装配注入，mic 见 D27）。
func (s *Session) ingest(ctx context.Context, raw port.RawInput) ([]conversation.Part, string, error) {
	if raw.Kind == "text" {
		text := strings.TrimSpace(raw.Text)
		if text == "" {
			return nil, "", errors.New("session: 文本输入为空")
		}
		return []conversation.Part{{Kind: conversation.PartText, Text: text}}, "", nil
	}
	for _, in := range s.ingestors {
		if in.Accepts(raw) {
			rep, err := in.Ingest(ctx, raw)
			if err != nil {
				return nil, "", err
			}
			return rep.Parts, rep.Note, nil
		}
	}
	return nil, "", fmt.Errorf("session: 没有可用的摄取器处理 %q 输入（M3 装配 file/clipboard；语音输入见 D27）", raw.Kind)
}

// ingestTitle 摄取消息的标题摘要：文本分片优先，其次附件名。
func ingestTitle(parts []conversation.Part) string {
	for _, p := range parts {
		if p.Kind == conversation.PartText && strings.TrimSpace(p.Text) != "" {
			return strings.TrimSpace(p.Text)
		}
		if p.Ref != nil && p.Ref.Name != "" {
			return p.Ref.Name
		}
	}
	return ""
}

// maybeSetTitle 首条消息后把默认标题改写为消息摘要（空文本不改写，
// 防 ingestTitle 无文本分片时把默认标题抹成空串）。
func (s *Session) maybeSetTitle(text string) {
	if text == "" {
		return
	}
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
		return s.execNew(ctx, cmd)
	case "list":
		return s.execList(ctx, cmd)
	case "switch":
		return s.execSwitch(ctx, cmd)
	case "title":
		return s.execTitle(ctx, cmd)
	case "goto":
		return s.execGoto(ctx, cmd)
	case "edit":
		return s.execEdit(ctx, cmd)
	case "regen":
		return s.execRegen(ctx, cmd)
	case "branch":
		return s.execBranch(ctx, cmd)
	case "rm":
		return s.execRm(ctx, cmd)
	case "exit":
		return "", ErrQuit
	case "compact":
		return s.execCompact(ctx, cmd)
	case "permission":
		return s.execPermission(ctx, cmd)
	case "memory":
		return s.execMemory(ctx, cmd)
	case "usage":
		return s.execUsage(ctx, cmd)
	case "jobs":
		return s.jobsReport(ctx, cmd.Args)
	case "quit":
		return "", ErrQuit
	case "help":
		static, dyn := s.commandsSplit() // D103：help 与补全浮层同源（命令清单表渲染）
		return renderHelp(static, dyn), nil
	case "plugin":
		return s.execPlugin(ctx, cmd.Args)
	case "model":
		return s.execModel(ctx, cmd.Args)
	case "think":
		return s.execThink(ctx, cmd.Args)
	case "effort":
		return s.execEffort(ctx, cmd.Args)
	default:
		// 动态命令（§6.1 扩展点 #4）：静态命令未命中时查询
		//（装配根随插件启停注册 /mcp:<server>:<prompt> 等）。
		if h := s.dynamicCommand(cmd.Name); h != nil {
			return h(ctx, cmd.Args)
		}
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
