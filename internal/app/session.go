package app

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Tonyjh07/Aquarius/internal/domain/conversation"
	"github.com/Tonyjh07/Aquarius/internal/domain/perm"
	"github.com/Tonyjh07/Aquarius/internal/i18n"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

// ErrQuit /quit 命令的退出请求；装配根据此收尾（app 不依赖具体 UI）。
var ErrQuit = errors.New("session: quit")

// defaultTitle 新会话默认标题（首条消息后改写为消息摘要）。
//
// 函数不是常量：标题会落进会话库、被「是否还是默认标题」的比较用上，而标题的文字要跟着
// 界面语言走。旧数据里的「新会话」不会因此被改写——比较的是同一语言下的同一个字符串。
func defaultTitle() string { return i18n.T("session.default_title") }

// isDefaultTitle 判断标题是否还是「没命名」状态（空串也算）。
//
// 不能只跟当前语言的默认标题比：会话是中文下建的，切到英文再打开，库里那串文字与当前
// 表里的不是同一串。两种默认标题都认，才不会把没命名的会话当成用户取的名字而拒绝改写。
func isDefaultTitle(title string) bool {
	if title == "" {
		return true
	}
	for _, lang := range [...]i18n.Lang{i18n.LangZh, i18n.LangEn} {
		if title == i18n.Table(lang)["session.default_title"] {
			return true
		}
	}
	return false
}

// titleRunes 标题摘要的最大长度。
const titleRunes = 24

// quoteMarks 模型标题外层常见的包裹引号：直角引号与弯引号各两对。按码点构造是因为这些字符
// 不是文案，只是要被剪掉的字符——直接写成字符会让「中文只许出现在 i18n 表」的锁报出来。
var quoteMarks = string([]rune{0x300c, 0x300d, 0x300e, 0x300f, 0x201c, 0x201d, 0x2018, 0x2019})

// truncateTitle 按 titleRunes 截断（超长补省略号）。摘要与模型标题共用一份规则：
// 两处口径不同的话，「还是自动标题吗」的比较会在长标题上永远不相等。
func truncateTitle(text string) string {
	r := []rune(strings.TrimSpace(text))
	if len(r) > titleRunes {
		return string(r[:titleRunes]) + "…"
	}
	return string(r)
}

// autoTitleFrom 从树上第一条用户消息推出的自动标题；没有用户消息返回空串。
//
// 这是「什么算自动标题」的唯一判据（D309）：/title 手动改过的标题与这个结果不相等，
// 于是不会被模型标题覆盖；而首条消息之后 maybeSetTitle 写下的摘录与这个结果恒等。
func autoTitleFrom(c *conversation.Conversation) string {
	if c == nil {
		return ""
	}
	for _, m := range c.Path() {
		if m.Role == conversation.RoleUser {
			return truncateTitle(ingestTitle(m.Content))
		}
	}
	return ""
}

// titleEditable 标题还能被自动改写（D309）：仍是默认标题，或仍是首条消息的摘录。
func titleEditable(c *conversation.Conversation) bool {
	if c == nil {
		return false
	}
	return isDefaultTitle(c.Title) || c.Title == autoTitleFrom(c)
}

// firstUserTurn 路径上只有一条用户消息（会话的第一轮）。
//
// 不用「路径长度 == 2」判定：persona 是树里的 system 首节点（D20），用户第一条消息
// 进来时路径已经是 Root + persona + user 三条。
func firstUserTurn(c *conversation.Conversation) bool {
	n := 0
	for _, m := range c.Path() {
		if m.Role == conversation.RoleUser {
			n++
		}
	}
	return n == 1
}

// urlInLastUserMessage urls 里是否有地址出现在本轮（路径上最后一条用户消息）里（D310）。
//
// 服务端读链接只在**最后一条用户消息**里找地址、且只认前 10 个，别处的地址发出去也
// 读不到。所以在装配层先把这一层筛掉：不满足就别切形态，省下「少了工具还没读到网页」
// 的那一轮。
func urlInLastUserMessage(c *conversation.Conversation, urls []string) bool {
	if c == nil || len(urls) == 0 {
		return false
	}
	var text strings.Builder
	for _, m := range c.Path() {
		if m.Role != conversation.RoleUser {
			continue
		}
		text.Reset()
		for _, p := range m.Content {
			if p.Kind == conversation.PartText {
				text.WriteString(p.Text)
			}
		}
	}
	body := text.String() // 循环结束时留的是最后一条用户消息
	if body == "" {
		return false
	}
	for _, u := range urls {
		if u != "" && strings.Contains(body, u) {
			return true
		}
	}
	return false
}

// normalizeModelTitle 把服务端给的标题收拾成可落盘的一行：去空白与包裹引号、压成单行、
// 按 titleRunes 截断。空 = 不作为标题（保留原来的摘录）。
func normalizeModelTitle(raw string) string {
	t := strings.TrimSpace(raw)
	if t == "" {
		return ""
	}
	t = strings.ReplaceAll(t, "\r", " ")
	t = strings.ReplaceAll(t, "\n", " ")
	t = strings.Trim(t, " \t\"'`"+quoteMarks)
	if t == "" {
		return ""
	}
	return truncateTitle(t)
}

// applyModelTitle 把服务端生成的标题写进当前会话（D309）：只在标题仍可自动改写时生效，
// `/title` 手动命名过的永不覆盖。写盘由调用方随本轮的 Save 一并完成。
func (s *Session) applyModelTitle(raw string) {
	t := normalizeModelTitle(raw)
	if t == "" || !titleEditable(s.cur) {
		return
	}
	s.cur.Title = t
}

// SessionDeps 会话依赖（全部端口，测试注入替身）。
type SessionDeps struct {
	Store port.ConversationStore
	Agent *Agent
	IDs   port.IDGen
	Clock port.Clock // persona 节点 CreatedAt
	// Persona 人格组合器（D144/S7）：新建会话时展开为最终 system 文本快照进 persona 首节点
	// （@env.date 取展开时刻 ⇒ 创建当天）。nil = 空规格默认人格。
	Persona *PersonaComposer
	// Level 权限档读侧（D22/D331）：装配根持有活值，改档后无需回写 Session 副本——
	// 单一份事实源也免掉「界面 goroutine 写、主循环读」的竞争。nil = perm.DefaultLevel。
	Level func() perm.Level
	// SandboxPath 特权目录（<dataDir>/sandbox），仅用于 /permission 展示。
	SandboxPath string
	// Perms 权限面写回（/permission 与 /unattended，D22/D185）；nil 时有参切换报错。
	Perms PermissionSettings
	// Confirmer 逐次确认（/rm 二次确认等）；nil 时破坏性命令报"未配置确认器"。
	Confirmer port.Confirmer
	// Jobs 后台任务管理（/jobs，§7.3，M3）；nil 时 /jobs 报"未配置任务管理器"。
	Jobs port.JobManager
	// Delegate 外部代理转交（/delegate，§5.18/D244）；nil 时 /delegate 报"未配置外部代理"。
	// 命令面只列清单与叫停——派活是模型经 delegate 工具做的事（与 job_* 同一分工）。
	Delegate port.Delegate
	// Watches 观察器（/watch，D190）；nil 时 /watch 报"未配置观察器"。
	Watches port.WatchManager
	// Skills 技能清单（/skill，D201）；nil 时 /skill 报"未配置技能目录"。
	Skills port.SkillCatalog
	// SkillAdmin 技能安装面（/skill install|remove|update，D202）；nil 时子命令报未接入。
	// 与 Skills 分开：读侧是只读端口，安装面是用户面动作（不进 port）。
	SkillAdmin SkillAdmin
	// WatchCancel 撤销某会话的后台任务观察者（D167/D262）：**删除会话与关停**时调用；
	// 切换与新建不调用——任务的生命周期跟任务走，不跟焦点走（D261）。nil 时跳过。
	WatchCancel WatcherCanceller
	// Log 运行日志（D276，上下文固定 session）：异地送达遇到已删除的会话时说明丢弃。
	// nil = 丢弃。
	Log port.Log
	// Ingestors 多模态输入摄取器（M3 管线；/attach 等触发命令面留 §14，D27/D33）；
	// nil 时 Raw 输入报"没有可用的摄取器"。
	Ingestors []port.Ingestor
	// UI 轮次外的过程事件（摄取 Note 等 NoticeEvent）；nil 时跳过呈现。
	UI port.Presenter
	// Wake 空闲唤醒（D264）：带送达回执的注入（InjectAndWait）入队后自己敲一下，
	// 否则用户一直不输入就永远等不到排空。装配根接到 UI 的空闲唤醒通道；nil = 不敲。
	Wake func() // Memories 用系统编辑器打开记忆文档（D24，装配根实现）；返回实际打开路径。
	// nil 时 /memory 报"未配置编辑器"。
	Memories MemoryOpener
	// Plugins 插件管理面（/plugin，§7.3 / M4）；nil 时 /plugin 报"未配置"。
	Plugins PluginAdmin
	// Models 模型服务读侧（/model 无参展示，D32）；nil 时报"未配置模型服务"。
	Models ModelCatalog
	// PersistModel 把切换后的模型写回 config（D32，同 /permission 模式）；
	// nil 时 /model 有参报"未配置持久化"。
	ModelSwitch ModelSwitcher
	// Prober 能力探测（D174：/model probe）。model 空 = 当前模型；装配根实现
	// （造一次性客户端 → 真请求 → 写回 config → 返回逐项结论）。nil 时该子命令报错。
	Prober ModelProbe
	// Thinking 思考面写回（/think 与 /effort，D34）；nil 时两条命令的有参形态报错。
	Thinking ThinkingSettings
	// Exporter 会话导出面（/export，§5.16）；nil 时 /export 报「本构建未接入导出面」。
	Exporter port.SessionExporter
	// Importer 会话导入面（/import，§5.16）；nil 时 /import 报未接入。
	Importer port.SessionImporter
	// ExportsDir 导出缺省落点（`--path` 不给时用；装配根给 `<profile>/exports`）。
	// 空 = 必须显式 --path，否则报未配置。
	ExportsDir string
	// ImportSourcesDir 导入溯源 sidecar 目录（D321；装配根给 `<profile>/import-sources`）。
	// 空 = 不做重导去重（deepseek 一档每次都当新导入）。
	ImportSourcesDir string
	// TrashDir 会话回收站目录（`<profile>/trash`，D311）。空 = 未接入回收站：
	// /rmconv 退回硬删（store.Remove），/trash 报未接入。
	TrashDir string
	// AuditPath 审计日志路径（`/audit`）。空 = 未接入审计日志，命令报错。
	// 与 Plugins 分开传：插件面读的是同一份文件的插件那几行，审计面读全部 kind。
	AuditPath string
	// Version 构建版本（`/diag` 写进包头）。app 不 import 构建信息，由装配根给。
	Version string
	// Doctor 体检面（/doctor，§7.14/D306）：七项检查在装配根，这里只拿结论。
	// nil 时 /doctor 报未接入。
	Doctor HealthProbe
	// Characters 角色卡目录（/character，§5.17）；nil 时 /character 报未接入。
	Characters port.CharacterCatalog
	// CharacterAdmin 角色卡管理面（/character import|remove，§5.17）；nil 时子命令报未接入。
	// 与 Characters 分开：读侧是只读端口，导入/删除是用户面动作（不进 port）。
	CharacterAdmin CharacterAdmin
	// CharacterOpts 角色卡投影开关（§5.17）；由 config `characters` 段填充。零值 = 三开关全关，
	// 装配根负责把缺省（常驻世界书默认开）解析成显式值后再传进来。
	CharacterOpts CharacterOptions
	// Files 文档交给服务端解析的上传口（D315，DeepSeek Files API）；nil = 未接入，
	// 文档只走本地解析。装配根在 `files.purpose` 非空时构造（purpose/ttl 一并传）。
	Files port.FileUploader
	// FilesPurpose 上传用途档（`text_recognition` / `rich_layout_recognition`；D315）。
	// 空 = 不上传。空串配合 Files nil：两道闸都要开才走服务端。
	FilesPurpose string
	// FilesTTL 服务端保留时长（`files.ttl_seconds`）；0 = 不过期。
	FilesTTL time.Duration
	// Blobs 附件库读侧：上传时按 doc 分片的引用取原文。nil = 不能上传（跳过服务端解析）。
	Blobs port.AttachmentStore
}

// CommandHandler 动态命令处理器（§6.1 扩展点 #4 / §7.3 MCP prompts）：
// 装配根随插件启停注册 `/mcp:<server>:<prompt>` 等动态命令；静态命令优先命中。
type CommandHandler func(ctx context.Context, args []string) (string, error)

// Session 当前会话 + 命令处理（§7.3；M0 起启用 /new /list /title /compact /
// /permission /quit，M1 启用树交互 /goto /edit /branch /rm，M3 启用 /jobs，
// M4 启用 /plugin 与动态 /mcp: 命令、/model，思考控制 /think /effort 随 D34 启用）。
// 命令的文本输出经返回值交给装配根渲染，轮次过程输出走 Presenter——app 不直接接触 IO。
type Session struct {
	store            port.ConversationStore
	agent            *Agent
	ids              port.IDGen
	clock            port.Clock
	persona          *PersonaComposer
	levelFn          func() perm.Level
	sandboxPath      string
	perms            PermissionSettings
	confirmer        port.Confirmer
	jobs             jobAdmin
	delegates        delegateAdmin
	watches          port.WatchManager
	skills           port.SkillCatalog
	skillAdmin       SkillAdmin
	watchCancel      WatcherCanceller
	log              port.Log
	ingestors        []port.Ingestor
	ui               port.Presenter
	wake             func()
	memories         MemoryOpener
	plugins          PluginAdmin
	models           ModelCatalog
	modelSwitch      ModelSwitcher
	prober           ModelProbe
	thinking         ThinkingSettings
	exporter         port.SessionExporter
	importer         port.SessionImporter
	exportsDir       string
	importSourcesDir string
	trashDir         string
	auditPath        string
	version          string
	characters       port.CharacterCatalog
	charAdmin        CharacterAdmin
	charOpts         CharacterOptions
	// files 服务端文件解析上传口（D315）；nil = 未接入。purpose/ttl 来自 config `files` 段。
	files        port.FileUploader
	filesPurpose string
	filesTTL     time.Duration
	blobs        port.AttachmentStore
	// doctor 体检面（§7.14/D306）；nil 时 /doctor 报未接入。
	doctor HealthProbe
	cur    *conversation.Conversation
	// treeEdit 树编辑会话（D258）：非 nil 时改父只改内存、退出时统一写入磁盘；
	// nil = 不在模式里。状态只在这里存一份，UI 经 TreeEditActive 读。
	treeEdit *treeEditSession
	// tree 会话树只读快照（D80/§7.5，前置 A）：树变更后由 publishTree 整体重建并原子
	// 发布，UI 事件循环 goroutine 经 Branches 无锁读（见 tree.go）。
	tree atomic.Pointer[treeSnapshot]
	// treeRev 快照的内容发布号：内容真变了才递增（发布不等于变更，见 publishTree）。
	// 只在会话 goroutine 读写，UI 读到的是快照里那份随快照发布的副本。
	treeRev uint64
	// facts 会话展示事实快照（D82/§7.6，前置 B）：与 tree 同组发布点原子发布，UI 经
	// Facts 无锁读（见 facts.go）；重算经签名门控，命令类输入不空转。
	facts atomic.Pointer[factsSnapshot]
	// sessions 会话列表只读快照（D120⑥，S3 左栏）：与 tree/facts 同组发布点原子发布，
	// UI 经 List 无锁读（见 sessionlist.go）。
	sessions atomic.Pointer[sessionSnapshot]
	// resumed 本次启动是否恢复了既有会话（NewSession 走 Load 分支）；
	// ReplayHistory 仅在恢复时回放（新建会话无历史可回放，D40/§7.4）。
	resumed bool
	// injectCh 注入队列（D150）：工具/MCP/轮询器等外部生产者经 Inject 非阻塞入队，
	// 实现由会话 goroutine 在排空点（Handle 入口 / 轮末 / 驱动式命令）独占执行——
	// 树变更始终单线程（§7.5），外部 goroutine 只碰这个 channel。
	injectCh chan pendingInjection
	// pendingQ 待发清单（D272）：会话级、内存态、有序、有上限。前端在**自己的事件循环
	// 里**直接读写待发清单——主循环此刻卡在 Handle 里，走输入通道等于「等本轮结束才看得到」，
	// 那正是这条端口要修的毛病。切会话即丢（见 dropPending）。字段名不叫 pending：
	// 注入队列的入口方法已经叫 pending（session_inject.go）。
	pendingQ *pendingList
	// owedTurn 轮末实现的触发型注入欠着一轮（D189）：轮内到达的完成报告只能写入、
	// 不能打断当前轮（② 轮末不驱动），但这一轮不能就这么算了——记一笔，等随后的
	// 空闲唤醒补上。不记的话唤醒点看到的队列已经空了，会径直返回。
	// 只在会话 goroutine 读写。
	owedTurn bool
	// lastStop 最近一轮是否被停止键中断（装配根据此决定「拿引导重新请求」，D274）。
	// 不用 paused 位：那个位在真实输入前一直挂着，分不清「刚才停的」与「历史遗留」。
	lastStop atomic.Bool

	// dynMu 保护 dynamic/dynamicMeta：REPL 主循环读、插件启停回调（可能来自崩溃重启的
	// watch goroutine）写，须加锁（§10 并发语义）。
	dynMu       sync.Mutex
	dynamic     map[string]CommandHandler
	dynamicMeta map[string]port.CommandInfo // 清单元数据（D103：名称→Names/Desc，键与 dynamic 对齐）

	// hideMu 保护 hidden：会话内暂时退出的内置命令（D337：记忆被插件接管时 /memory 退出，
	// 交还后回来）。与动态命令分开是因为它改的是静态表——表本身不可变，过滤在查询处做。
	hideMu sync.Mutex
	hidden map[string]bool
}

// SetHiddenCommands 替换隐藏的内置命令集（装配根随记忆接管/交还刷新；整体换）。
// 隐藏 = 与不存在等同：分派按未知命令、清单与补全浮层都不出列。
func (s *Session) SetHiddenCommands(names []string) {
	set := make(map[string]bool, len(names))
	for _, n := range names {
		set[n] = true
	}
	s.hideMu.Lock()
	defer s.hideMu.Unlock()
	s.hidden = set
}

// isHiddenCommand 该内置命令当前是否隐藏。
func (s *Session) isHiddenCommand(name string) bool {
	s.hideMu.Lock()
	defer s.hideMu.Unlock()
	return s.hidden[name]
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

// NewSession 恢复最近更新的会话；没有则新建并写入磁盘（会话树跨进程持久化）。
func NewSession(ctx context.Context, d SessionDeps) (*Session, error) {
	switch {
	case d.Store == nil:
		return nil, errors.New("session: " + i18n.T("session.deps.store_missing"))
	case d.Agent == nil:
		return nil, errors.New("session: " + i18n.T("session.deps.agent_missing"))
	case d.IDs == nil:
		return nil, errors.New("session: " + i18n.T("session.deps.idgen_missing"))
	case d.Clock == nil:
		return nil, errors.New("session: " + i18n.T("session.deps.clock_missing"))
	}
	if d.Level == nil { // 未注入读侧：按缺省档（测试与最小装配）
		d.Level = func() perm.Level { return perm.DefaultLevel }
	}
	persona := d.Persona
	if persona == nil { // nil = 空规格默认人格（内置 @default + @env）
		persona = NewPersonaComposer(PromptSpec{}, RuntimeEnv{}, nil)
	}
	s := &Session{
		store:            d.Store,
		agent:            d.Agent,
		ids:              d.IDs,
		clock:            d.Clock,
		persona:          persona,
		levelFn:          d.Level,
		sandboxPath:      d.SandboxPath,
		perms:            d.Perms,
		confirmer:        d.Confirmer,
		jobs:             d.Jobs,
		delegates:        d.Delegate,
		watches:          d.Watches,
		skills:           d.Skills,
		skillAdmin:       d.SkillAdmin,
		watchCancel:      d.WatchCancel,
		log:              d.Log,
		ingestors:        d.Ingestors,
		ui:               d.UI,
		wake:             d.Wake,
		memories:         d.Memories,
		plugins:          d.Plugins,
		models:           d.Models,
		modelSwitch:      d.ModelSwitch,
		prober:           d.Prober,
		thinking:         d.Thinking,
		exporter:         d.Exporter,
		importer:         d.Importer,
		exportsDir:       d.ExportsDir,
		importSourcesDir: d.ImportSourcesDir,
		trashDir:         d.TrashDir,
		auditPath:        d.AuditPath,
		version:          d.Version,
		characters:       d.Characters,
		charAdmin:        d.CharacterAdmin,
		charOpts:         d.CharacterOpts,
		files:            d.Files,
		filesPurpose:     d.FilesPurpose,
		filesTTL:         d.FilesTTL,
		blobs:            d.Blobs,
		doctor:           d.Doctor,
		dynamic:          map[string]CommandHandler{},
		injectCh:         make(chan pendingInjection, injectQueueCap),
		pendingQ:         newPendingList(),
	}
	// 待发清单归会话持有，Agent 只借用待发清单做两个取用边界（§7.1.2 轮内 / 轮末）。
	d.Agent.setPending(s.pendingQ)

	list, err := d.Store.List(ctx)
	if err != nil {
		return nil, wrapSession("err.list_conversations", err)
	}
	if len(list) > 0 { // List 按 UpdatedAt 降序，首条即最近
		c, err := d.Store.Load(ctx, list[0].ID)
		if err != nil {
			return nil, fmt.Errorf("session: %s: %w",
				i18n.Tf("session.err.restore", "id", list[0].ID), err)
		}
		s.cur = c
		s.resumed = true
		s.publishTree()     // D80/§7.5：构造完成即发布树只读快照（UI 早于首帧即可读）
		s.publishFacts(ctx) // D82/§7.6：事实快照同组发布（恢复会话首帧 tooltip 即可用）
		s.publishSessions(ctx)
		return s, nil
	}
	s.cur, err = s.newConversation(defaultTitle())
	if err != nil {
		return nil, err
	}
	if err := d.Store.Save(ctx, s.cur); err != nil {
		return nil, wrapSession("session.err.init", err)
	}
	s.publishTree()     // D80/§7.5：构造完成即发布树只读快照
	s.publishFacts(ctx) // D82/§7.6：事实快照同组发布
	s.publishSessions(ctx)
	return s, nil
}

// newConversation 建会话：Root + persona 首节点（system 角色，D20），Head 落在 persona。
func (s *Session) newConversation(title string) (*conversation.Conversation, error) {
	c := conversation.New(s.ids.ConversationID(), title)
	prompt, _ := s.persona.Compose() // D144/S7：人格模块树展开（@env.date = 会话创建当天）
	if strings.TrimSpace(prompt) == "" {
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
		return nil, wrapSession("session.err.persona", err)
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
	return s.replayHistory(ctx, "replay.verb.restored")
}

// replayHistory 回放当前会话的可见历史（D40/§7.4 规则：水位 → Head，persona 不回放），
// 先发 NoticeEvent 说明会话身份与条数；verb 是提示动词——启动「已恢复」、切换「已切换到」
// （D75）。无 UI 或仅 Root/persona 时直接返回 0。
func (s *Session) replayHistory(ctx context.Context, verbKey string) (int, error) {
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
		start = watermarkIdx // 有压缩摘要：从摘要节点起（含），与 assemblePath 同一套规则
	}
	if start >= len(path) {
		return 0, nil // 仅 Root/persona：没有可回放的历史
	}
	n := len(path) - start
	notice := i18n.Tf("session.replay.notice",
		"verb", i18n.T(verbKey), "title", s.cur.Title, "id", s.cur.ID, "n", n)
	if err := s.ui.Emit(ctx, port.NoticeEvent{Text: notice}); err != nil {
		return 0, wrapSession("err.replay_notice", err)
	}
	for i := start; i < len(path); i++ {
		if err := s.ui.Emit(ctx, port.HistoryEvent{Message: path[i]}); err != nil {
			return i - start, wrapSession("err.replay_history", err)
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
		return wrapSession("err.clear_screen", err)
	}
	return nil
}

// Handle 处理一次用户输入：命令走 execCommand；Raw 走多模态摄取管线（M3）；
// 普通文本入树 → Turn → 写入磁盘。返回值是命令的文本输出（空 = 无）；Turn 过程输出已由 Presenter 呈现。
func (s *Session) Handle(ctx context.Context, in port.UserInput) (string, error) {
	s.lastStop.Store(false)
	// D80/§7.5：树变更只发生在 Handle 内（命令面与 Turn），返回前重发一次只读快照——
	// UI 侧每帧无锁读，本 defer 是唯一的变更后发布点。D82/§7.6：事实快照同点重发
	// （签名门控：无变更的命令输入不重算）。
	defer s.publishTree()
	defer s.publishFacts(ctx)
	defer s.publishSessions(ctx)
	// D167 排空点③：空闲唤醒不是用户输入——先做驱动式排空（触发型注入自己跑一轮），
	// 不追加 user 节点。必须排在下面的普通排空之前，否则报告会先被实现成未触发。
	if in.Wake {
		_, wantTurn, err := s.drainInjections(ctx, drainOnWake)
		if err != nil {
			return "", err
		}
		if wantTurn || s.owedTurn {
			s.owedTurn = false
			return "", s.runTurn(ctx)
		}
		return "", nil
	}
	// 真实用户输入到来：这一轮必然带上已落树的注入，欠着的那一轮就由这次输入兑现了。
	s.owedTurn = false
	// D150 排空点①：用户输入优先，先把外部生产者攒下的注入实现（只写入、不起轮，
	// 触发留给本轮——用户马上就发话，不必为注入单起一轮）。
	if _, _, err := s.drainInjections(ctx, drainBeforeInput); err != nil {
		return "", err
	}
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
	// 真实用户输入到来 = 停手结束（D272）：停止键只停当前轮与自动续跑，用户再开口就恢复。
	s.pendingQ.setPaused(false)
	s.maybeSetTitle(text)
	s.agent.noteDateChange(ctx, s.cur) // 跨日期时先补一条日期说明（D292）
	if _, err := s.cur.Append(conversation.RoleUser, []conversation.Part{{Kind: conversation.PartText, Text: text}}); err != nil {
		return "", wrapSession("session.err.append", err)
	}
	return "", s.runTurn(ctx)
}

// runTurn 执行一轮 Turn 并写入磁盘：成败都保存（error/cancelled 终态的节点同样持久化）。
//
// D150 排空点②：本 Turn 提交后，把轮内（工具等）攒下的注入实现——**只写入、不打断当前轮**
// （Trigger 记 false）；轮内注入是补上下文，何时起响应交给用户的下一条输入或空闲唤醒。
//
// 轮末取一条（D272 边界②）：本段提交之后按 FIFO 取一条待发（不分档位——引导错过窗口就只是
// 「一句话」，按下一轮发出去是对的），接着起下一轮，**不回到输入循环**。这让「刚按发送、
// 正好这轮结束了」不会卡在清单里发霉，也让排队消息真的连跑。
func (s *Session) runTurn(ctx context.Context) error {
	for {
		runErr := s.agent.Run(ctx, s.cur)
		// 会话标题（D309）：首轮回答顺带给出的标题，在这一轮的写盘之前落到会话元数据上。
		// 拿不到（空串）就走原来的截断标题——那不是故障，不报错。
		s.applyModelTitle(s.agent.TakeTitle())
		if err := s.store.Save(ctx, s.cur); err != nil {
			if runErr != nil {
				// 两个错误都保留：Turn 错误为主，保存失败附注。
				return fmt.Errorf("session: %w%s", runErr,
					i18n.Tf("session.save.note", "save", err))
			}
			return wrapSession("err.save_conversation", err)
		}
		// 停止键：本轮 ctx 被取消 → 清单原样保留但置 paused（轮末不再自动续跑）。停止是
		// 「停手」，不是「这些都不要了」；一停就接着跑队列里那条，会让人以为停了其实还在跑。
		if ctx.Err() != nil {
			s.pendingQ.setPaused(true)
			s.lastStop.Store(true)
			return runErr
		}
		if runErr != nil {
			return runErr
		}
		// 轮内注入的实现：只写入、Trigger 记 false；发 HistoryEvent 让转写区补上这条记录
		// （轮末不清屏，与 /goto 的回放规则区分）。
		if _, _, err := s.drainInjections(ctx, drainAfterTurn); err != nil {
			return err
		}
		next, ok := s.pendingQ.takeFirst()
		if !ok {
			return nil
		}
		if _, err := s.agent.appendUserNode(ctx, s.cur, next.Text); err != nil {
			return err
		}
	}
}

// ingestAndRun 多模态输入管线（M3，D27：/attach 等触发命令面留 §14，D27/D33）：
// RawInput → Ingestor 分派 → Part 入树（user）→ Turn → 写入磁盘；摄取 Note 经 NoticeEvent 提示。
func (s *Session) ingestAndRun(ctx context.Context, raw port.RawInput) (string, error) {
	parts, note, err := s.ingest(ctx, raw)
	if err != nil {
		return "", wrapSession("session.err.ingest", err)
	}
	if len(parts) == 0 {
		return "", errors.New("session: " + i18n.T("session.ingest.empty"))
	}
	// 服务端文件解析（D315）：门控未开 = 原样返回，请求路径一字节不变。
	if n := s.uploadDocs(ctx, parts); n != "" {
		if note != "" {
			note += i18n.T("ingest.join") + n
		} else {
			note = n
		}
	}
	s.maybeSetTitle(ingestTitle(parts))
	s.agent.noteDateChange(ctx, s.cur) // 跨日期时先补一条日期说明（D292）
	if _, err := s.cur.Append(conversation.RoleUser, parts); err != nil {
		return "", wrapSession("session.err.append", err)
	}
	if note != "" && s.ui != nil {
		_ = s.ui.Emit(ctx, port.NoticeEvent{Text: note})
	}
	return "", s.runTurn(ctx)
}

// ingest 分派 RawInput：Kind=text 直通（§7.2 文本摄取），
// 其余按注册序找首个 Accepts 的 Ingestor（file/clipboard 由装配注入，mic 见 D27）。
func (s *Session) ingest(ctx context.Context, raw port.RawInput) ([]conversation.Part, string, error) {
	if raw.Kind == "text" {
		text := strings.TrimSpace(raw.Text)
		if text == "" {
			return nil, "", errors.New("session: " + i18n.T("session.ingest.no_input"))
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
	return nil, "", fmt.Errorf("session: %s",
		i18n.Tf("session.ingest.no_ingestor", "kind", fmt.Sprintf("%q", raw.Kind)))
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
	if !isDefaultTitle(s.cur.Title) {
		return
	}
	s.cur.Title = truncateTitle(text)
}

// cancelWatchersFor 撤销某会话的后台任务观察者（D262）：只在**删除会话**与关停时调用。
// 切换不撤销——任务的生命周期跟任务走，报告按归属异地送达，不随焦点丢弃（D261）。
func (s *Session) cancelWatchersFor(id conversation.ID) {
	if s.watchCancel != nil {
		s.watchCancel.CancelSession(id)
	}
}

// levelNow 当前权限档（读装配根持有的活值，D331）。
func (s *Session) levelNow() perm.Level {
	if s.levelFn == nil {
		return perm.DefaultLevel
	}
	if lv := s.levelFn(); lv != "" {
		return lv
	}
	return perm.DefaultLevel
}

// execCommand 命令分发（§7.3）：静态注册表 → 动态命令 → 未知。
func (s *Session) execCommand(ctx context.Context, cmd port.Command) (string, error) {
	// 命令也是插件的一个入口（plugin-design.md §5「会话身份」）：动态命令（插件 CommandProvider、
	// MCP prompt）要按会话取状态，缺了会话身份它们只会悄悄退回全局。已经带了会话的 ctx 不动——
	// 上游显式注入的会话比「当前会话」更权威（转交、后台任务都这么进来）。
	if _, ok := port.SessionIDFrom(ctx); !ok && s.cur != nil {
		ctx = port.WithSessionID(ctx, s.cur.ID)
	}
	if e, ok := commandIndex[cmd.Name]; ok && !s.isHiddenCommand(cmd.Name) {
		return e.Run(s, ctx, cmd)
	}
	// 动态命令（§6.1 扩展点 #4）：静态命令未命中时查询
	//（装配根随插件启停注册 /mcp:<server>:<prompt> 等）。
	if h := s.dynamicCommand(cmd.Name); h != nil {
		return h(ctx, cmd.Args)
	}
	return "", errors.New(i18n.Tf("session.unknown_command", "name", cmd.Name))
}

// persist 写入磁盘当前会话。
func (s *Session) persist(ctx context.Context) error {
	if err := s.store.Save(ctx, s.cur); err != nil {
		return wrapSession("err.save_conversation", err)
	}
	// 树编辑会话期间照常写入磁盘（新消息、轮次不能只活在内存里），但要记下来：
	// 放弃编辑时得把这次写盘也撤回，否则盘上会留着一份「被放弃的中间态」。
	if s.treeEdit != nil {
		s.treeEdit.saved = true
	}
	return nil
}

// TakeSteer 取走第一条引导（按插入序）。停止键中断一次请求之后，装配根用这条引导立刻重新
// 请求——「停下来听我说这句」里，这句本来就已经在嘴边了，不该再让用户重打一遍。
func (s *Session) Stopped() bool { return s.lastStop.Load() }

func (s *Session) TakeSteer() (string, bool) {
	if s.pendingQ == nil {
		return "", false
	}
	it, ok := s.pendingQ.takeOneSteer()
	if !ok {
		return "", false
	}
	return it.Text, true
}
