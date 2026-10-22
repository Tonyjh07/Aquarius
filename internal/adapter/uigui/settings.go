package uigui

// 设置窗（§15.7/D60 核心档 + §15.4/D61 主题档）：表单状态、保存流与帧渲染。
// 并发口径（§15.7）：表单状态仅次窗事件循环 goroutine 读写、不跨窗共享；持久化经
// ApplySettings 单一写回调（装配根实现），内核命令经 inCh 排队到主循环与键入同
// 路径串行执行（壳内不旁路），主题/快捷键热生效经消息与原子槽（同跨窗模型）。

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	"gioui.org/app"
	"gioui.org/layout"
	"gioui.org/widget"

	"github.com/Tonyjh07/Aquarius/internal/port"
)

// ProfilesSnapshot profile 区快照（D110③）：当前生效 profile + 可用清单。
type ProfilesSnapshot struct {
	Current string   // 本次启动生效的 profile 名（--profile 或指针决定）
	Items   []string // profiles/ 下可用清单（含 config.json 的目录）
}

// ProfilesManager profile 管理面（D110③/修订④，装配根实现）：设置窗 profile 区
// 数据面与三动作。切换/新建/复制均重启生效（Q1：profile 进程级，运行态不热切）；
// 复制语义 = 仅复制配置（新 profile 以当前 profile 的 config.json 为底，不带会话
// /记忆/附件数据）。
type ProfilesManager interface {
	Snapshot() ProfilesSnapshot
	Create(name string) error // 模板 config 新建
	Copy(name string) error   // 以当前 profile 配置为底新建（仅配置）
	Switch(name string) error // 原子写根指针（重启生效）
}

// ProviderKeySnapshot provider 密钥池单槽快照（S11-7③/D173）：Label = 展示名；
// Saved = 引用是 `secret:<名>`（值在库里，Ref 给名字供展示与「沿用键名」）；
// Plain = 引用是明文（**只标形态、永不含值**，D35：密钥只写不回显）。
type ProviderKeySnapshot struct {
	Label string // 展示名（空 = 「第 N 把」）
	Saved bool   // true = 引用形如 secret:<名>（已入库）
	Plain bool   // true = 引用是明文（不回显值，只标形态）
	Ref   string // 仅 Saved：`secret:<名>`（只含名、永不含值）
}

// ProviderModelSnapshot provider 模型条目快照（S11-7③/D173）：能力位取**生效值**
// （缺省时按协议缺省），CapsExplicit 区分「缺省」与「显式声明」——只有用户真动过
// 能力位才把它固化成显式声明。
type ProviderModelSnapshot struct {
	Name          string
	Caps          []string // 生效能力名（port.CapTools/CapReasoning/CapVision 的子集）
	CapsExplicit  bool     // false = 键缺失（按协议缺省）
	ContextWindow int      // 该模型上下文窗口 tokens（0 = 不声明，跟随全局）
	Enabled       bool     // 是否启用（D180：键缺失 = true；不启用 = 保留声明但不参与选型）
}

// ProviderSnapshot provider 编辑区单条快照（D110②/③）：模型条目与密钥池都以结构化
// 列表呈现（S11-7③）；APIKey 只写不回显（D35），不进快照。Kind 供折叠卡片摘要。
type ProviderSnapshot struct {
	Name        string
	Kind        string // 协议（空 = openai-chat，读侧归一展示）
	BaseURL     string
	Models      []ProviderModelSnapshot
	Tokenizer   string
	Unsupported string // 逗号分隔
	Keys        []ProviderKeySnapshot
	Primary     bool // 是否当前 primary
}

// ProviderKeyPatch provider 密钥池单槽提交（S11-7③）：Value 空 = 保持不变（含沿用键名，
// D161 落点③——原引用名从落盘 config 按槽位下标读取）；Value 以 `secret:` 开头 = 照写
// 引用（不进库）；否则明文写库后 config 只落引用。Label 空 = 不留展示名。
type ProviderKeyPatch struct {
	Value string
	Label string
}

// ProviderModelPatch provider 模型条目提交（S11-7③）：Caps nil = 保持原声明（键缺失
// 与显式都原样保留）；非 nil（含空数组）= 以本次为准（显式声明）。ContextWindow 0 = 不声明。
// Disabled = true 时落盘为显式 `enabled: false`（D180）；缺省（false）= 启用、不落键。
// 用「禁用」而不是「启用」命名，是为了让零值等于默认行为：忘了填的提交不会把模型全关掉。
type ProviderModelPatch struct {
	Name          string
	Caps          *[]string
	ContextWindow int
	Disabled      bool
}

// ProviderPatch provider 编辑区单条提交（Apply 全量替换口径）。Orig = 编辑前的
// 原名（cmd 侧按它保留原条目的无关键；空 = 新增条目）。
type ProviderPatch struct {
	Orig        string
	Name        string
	Kind        string // 协议（S11-7③ 起进编辑面）；空 = openai-chat
	BaseURL     string
	Keys        []ProviderKeyPatch
	Models      []ProviderModelPatch
	Tokenizer   string
	Unsupported string // 逗号分隔
}

// ProbeOutcome 一次能力探测的结果快照（S11-7③/D174）：设置窗「探测能力」按钮经
// ProviderManager.Probe 走与 `/model probe` 同一条写回逻辑，只把结论带回界面展示。
type ProbeOutcome struct {
	Provider string
	Model    string
	Results  []port.ProbeResult
	// Written 是否真的写回了 config（无明确结论且声明没变 = false）。
	Written bool
	// Pending 该模型还没写进配置（编辑区新加、尚未应用）：结论只回填到勾选，
	// 等「应用 provider 变更」一并落盘（D181）。此时 Written 恒 false。
	Pending bool
}

// FallbackLink 降级链一项（D173/D176 口径三）：provider + 模型名。模型可为空
// （= 取该 provider 的缺省模型），也可手填未声明的名字——链是运行时路由，不要求
// 目标模型已写进 provider 的 models 声明（声明只影响能力位与窗口）。
type FallbackLink struct {
	Provider string
	Model    string
}

// ProviderChoice 「模型」页降级链编辑用的 provider 选项：名字 + 它声明的模型名。
// 与 ProviderManager 的编辑面分开——那边编辑 provider 本体（怎么连），这里只挑路由目标。
type ProviderChoice struct {
	Name   string
	Models []string
}

// WebProviderView 联网提供商一条的编辑视图（D200）：kind 决定怎么连，其余字段按 kind
// 取用（与 cmd 侧 webProvider 同构）。密钥**永不回显**：只给形态标记（Saved = config 里
// 是 `secret:<名>` 引用、Plain = 明文、Ref = 仅 Saved 时的引用名）。
type WebProviderView struct {
	Kind     string // 搜索侧 deepseek|brave；抓取侧 deepseek|local
	From     string // 复用哪个 model.providers（deepseek 缺省 = "deepseek"）
	BaseURL  string // 端点覆盖（空 = 官方端点）
	Country  string // 仅 brave
	Lang     string // 仅 brave
	Count    int    // 仅搜索：这一家的缺省条数（0 = 用 web.search.count）
	KeySaved bool
	KeyPlain bool
	KeyRef   string
}

// WebProviderPatch 联网提供商一条的提交（D200）。Key 空 = 保持原值（只写不回显）；
// `secret:` 前缀照写引用，否则按明文落盘（启动期会警告，与 provider 密钥同口径）。
type WebProviderPatch struct {
	Kind    string
	From    string
	Key     string
	BaseURL string
	Country string
	Lang    string
	Count   int
}

// ProviderManager provider 列表管理面（D110②/③，装配根实现）：设置窗 provider
// 编辑区数据面 + 全量应用 + 连通性测试。provider 变更重启生效（base_url/api_key/
// tokenizer 构造期固化，port.LLM 不可热换）；Test = 真实最小请求（/models，短超时）。
type ProviderManager interface {
	Snapshot() []ProviderSnapshot
	// Apply 全量替换 model.providers（primary 须命中列表；删除 = 缺席；cmd 侧
	// 校验与 fallback 悬挂引用清理，泛键写回保留各条目无关键）。
	Apply(primary string, providers []ProviderPatch) error
	// Test 连通性测试（真实最小请求；调用方 goroutine 负责，不进帧循环）。
	Test(p ProviderPatch) error
	// Models 拉取该 provider 的模型列表（GET /models，短超时；调用方 goroutine 负责）。
	// 只读：不改配置、不设主。返回顺序按服务端响应（去重）。
	Models(p ProviderPatch) ([]string, error)
	// Probe 对某模型发起三项能力探测并写回判定明确的结果（S11-7③/D174）：与
	// `/model probe` 走**同一条**写回逻辑（装配根 probeModel），只是把结论带回界面。
	// 调用方 goroutine 负责（三次真请求，可能数十秒，不进帧循环）。
	Probe(model string, p ProviderPatch) (ProbeOutcome, error)
}

// PromptModuleView 人格模块树单个节点的编辑视图（S7/D144）：Name 唯一（用户模块名不以
// `@` 开头，该前缀保留给内置模块）；Text = 叶正文（组亦可带，作组头）；Children = 子模块
// 引用名（有序，可再嵌套 → 多级细分）。数据面真源 = config `prompts.modules`。
type PromptModuleView struct {
	Name     string
	Text     string
	Children []string
}

// BuiltinModuleView 内置只读模块展示项（S7/D144，`@default`/`@env` 及其子模块）：
// 设置窗列出并可一键加入人格引用；不可改名/删除。
type BuiltinModuleView struct {
	Name  string // 引用名（含 @ 前缀）
	Label string // 中文说明
}

// SettingsSnapshot 设置窗核心档快照（开窗现取；同 Status 口径线程安全回调）。
// Permission/Model/Think/Effort 由装配根取运行态（等级活槽与 Agent 访问器），
// 其余取 config 文件；密钥永不进快照（D35 只写不回显）。provider 编辑数据面走
// ProviderManager（D110②），不在本快照。MCPServers/Plugins 只读展示（编辑走
// 高级页 raw JSON，Q10）；RawJSON = config.json 原文（高级页，解析失败不阻断开窗）。
type SettingsSnapshot struct {
	Permission string // 规范值 read-only|strict|permissive|full-access（perm.Level）
	// Unattended 无人值守开（D185）：运行态现取，与 Permission 同口径。
	Unattended   bool
	Model        string
	Think        bool
	Effort       string  // "" = 未设（D34 清除态）
	Hotkey       string  // "" = 默认 Alt+A
	Theme        string  // "" = system
	Scale        float64 // D90：元素缩放倍率；0 = 缺省 1.0（表单归一）
	FontSize     float64 // D90：正文字号 sp；0 = 缺省 15（表单归一）
	WindowWidth  int     // D90：主窗像素宽；0 = 缺省 608×460dp
	WindowHeight int

	// 全量分组页（S4/D110③）：输出 / 限制 / 提示词 / MCP 与插件 / 高级。
	Notify           bool
	TTS              bool
	MaxTurns         int
	MaxContextTokens int
	CompactThreshold float64
	ToolOutputChars  int
	ToolTimeoutSec   int
	SystemPrompt     string              // 默认人格（= 内置模块 @default 正文；空 = 内置默认）
	PromptModules    []PromptModuleView  // 人格模块树（config prompts.modules，按名排序）
	PromptPersona    []string            // 人格有序引用（config prompts.persona）
	Builtins         []BuiltinModuleView // 内置只读模块（展示 + 引用，S7/D144）
	MCPServers       []string            // 只读：声明名清单（sorted）
	Plugins          []string            // 只读：声明名清单（sorted）
	RawJSON          string              // config.json 原文（高级页编辑起点）

	// 语音（D179）：ASR/TTS 各自引用 model.providers 里的一个 name；空 = 该能力未配置。
	// 只借 base_url 与密钥池，不继承 kind/能力位、不进降级链。
	ASRProvider string
	ASRModel    string
	ASRLang     string // 录音语种提示：auto | zh | en
	TTSProvider string
	TTSModel    string
	TTSVoice    string // 预置音色名；-voiceclone 模型填样本文件绝对路径
	TTSDesign   string // -voicedesign 模型的音色描述
	TTSStyle    string // 自然语言风格指令
	TTSTags     string // 风格标签（拼成 "(标签)" 前缀）

	// 联网（D200）：搜索与抓取各自一组**有序**提供商（顺序即优先级），加本地抓取的
	// 网络与安全参数。提供商本体只给「怎么连」的展示值，密钥永不进快照（只给
	// KeySaved / KeyPlain 这两个形态标记，D35 同口径）。
	WebSearchProviders []WebProviderView
	WebSearchCount     int // 0 = 缺省 8
	WebFetchProviders  []WebProviderView
	WebFetchMaxChars   int    // 0 = 缺省 16000
	WebFetchProxy      string // 空 = 读 HTTP(S)_PROXY
	WebFetchUserAgent  string // 空 = 浏览器形态缺省
	WebFetchRobots     bool   // 遵守 robots.txt（键缺失 = 遵守，读侧归一为 true）
	WebFetchPrivate    bool   // 允许回环/私网目标（调试用）
	WebFetchTimeoutSec int    // 0 = 缺省 30

	// Fallback 降级链（D173/D176 口径三）。模型 "" = 取该 provider 的缺省模型。
	// 链条目可指向声明之外的模型名——它是运行时路由，不要求目标先在 provider 里声明。
	Fallback []FallbackLink
	// FallbackNote 链窄于当前模型能力时的警示文案（cmd 侧按 caps 现算；空 = 不窄）。
	// 与其余快照同口径：开窗取一次（降级链改动要重启才生效，运行态本就不同步）。
	FallbackNote string
}

// SettingsPatch 保存提交（表单全量；模型服务走 ProviderManager.Apply，D110②）。
// 数值键 0 = 不改（表单留空 = 保持快照原值的既有口径）；Notify/TTS 恒写（布尔无
// 留空语义）；SystemPrompt 恒写（清空 = 合法语义「内置默认」）；RawJSON 非空 =
// 高级页整文件替换（保存前 cmd 侧 JSON 校验）。
type SettingsPatch struct {
	Permission string
	// Unattended 无人值守（D185）：恒写（布尔无留空语义）；与 Permission 一起走内核命令。
	Unattended   bool
	Model        string
	Think        bool
	Effort       string
	Hotkey       string
	Theme        string
	Scale        float64 // D90：元素缩放倍率（cycle 档，恒非零）
	FontSize     float64 // D90：正文字号 sp；0 = 不改
	WindowWidth  int     // D90：主窗像素宽；0 = 不改（缺省）
	WindowHeight int

	Notify           bool
	TTS              bool
	MaxTurns         int
	MaxContextTokens int
	CompactThreshold float64
	ToolOutputChars  int
	ToolTimeoutSec   int
	SystemPrompt     string
	// PromptModules/PromptPersona 人格模块树与有序引用（S7/D144，恒全量写：空 = 清空）。
	// 空名模块行被跳过；模块名校验（非空/不以 @ 开头/不重复）在 cmd 侧拦截。
	PromptModules []PromptModuleView
	PromptPersona []string
	RawJSON       string // "" = 不改
	// Fallback 降级链（D176 口径三；恒全量写：空 = 清空）。链条目 {provider, model}，
	// 模型可为空（取该 provider 的缺省模型）；cmd 侧校验 provider 名存在。
	Fallback []FallbackLink
	// 语音（D179；恒全量写）：provider 空 = 该能力关闭；model 非空且 provider 空时
	// 原样保留（不生效但不丢用户输入）；cmd 侧校验引用的 provider 存在。
	ASRProvider string
	ASRModel    string
	ASRLang     string // auto | zh | en
	TTSProvider string
	TTSModel    string
	TTSVoice    string
	TTSDesign   string
	TTSStyle    string
	TTSTags     string

	// 联网（D200）：两组提供商列表恒全量写（nil = 这条写路径不管联网，空数组 = 清空）。
	// 数值 0 = 不改（留空保持快照原值）；布尔恒写（robots 的「键缺失 = 遵守」在写侧
	// 归一为显式 true/false）。
	WebSearchProviders []WebProviderPatch
	WebSearchCount     int
	WebFetchProviders  []WebProviderPatch
	WebFetchMaxChars   int
	WebFetchProxy      string
	WebFetchUserAgent  string
	WebFetchRobots     bool
	WebFetchPrivate    bool
	WebFetchTimeoutSec int
}

// 枚举档（展示值 + 键值；内核 /permission /effort 的校验为单一事实源，此处仅渲染循环）。
var (
	// permLevels 权限档（§5/D22，与 domain/perm.Level 对齐）。
	permLevels = []string{"read-only", "strict", "permissive", "full-access"}
	// effortLevels 推理档位（D34）：首项 = 未设（键值 "" = 清除态）。
	effortLevels = []string{"未设", "minimal", "low", "medium", "high"}
	effortValues = []string{"", "minimal", "low", "medium", "high"}
	// themeLevels 主题档展示与键值（§15.4/D61：system|light|dark）。
	themeLevels = []string{"跟随系统", "浅色", "深色"}
	themeValues = []string{"system", "light", "dark"}
	// zoomLevels 元素缩放档（D90/§15.8）：展示百分比，键值 = 倍率字符串（cycleField
	// 键值为 string 的既有口径，表单侧 ParseFloat 换算；%g 往返一致）。
	zoomLevels = []string{"100%", "125%", "150%", "175%", "200%"}
	zoomValues = []string{"1", "1.25", "1.5", "1.75", "2"}
	// langLevels 录音语种提示（D179）：只喂给识别，不改变界面语言。
	langLevels = []string{"自动识别", "中文", "英文"}
	langValues = []string{"auto", "zh", "en"}
	// kindLevels 协议档（S11-7③/D173）：展示名短（chat/responses/anthropic），键值为
	// config 里的 kind 原文；空 = openai-chat（读侧归一）。
	kindLevels = []string{"chat", "responses", "anthropic"}
	kindValues = []string{"openai-chat", "openai-responses", "anthropic"}
	// capLevels 模型能力位三勾选的展示名与键名（S11-7③）：顺序与 port.CapNames 一致。
	capLabels = []string{"工具", "思考", "图片"}
	capNames  = []string{port.CapTools, port.CapReasoning, port.CapVision}
)

// 设置窗页（S4d/D142）：内容分页呈现（左侧 140dp 导航栏 + 右页内容 + 全局底栏）。
// 页序即导航序 = settingsPages 注册序（D162）；页名集与页序为契约（测试锁定）。
// 这里的常量只是**可读的页索引**——加页时必须在 settingsPages 追加一页，并在此加对应常量；
// pageConstCount 与注册表长度的等式由测试锁住，两处漏一处就响亮失败。
const (
	pageGeneral   = iota // 常规：Profile 区 / 欢迎引导 / 权限等级 / 外观与呼出 / 输出
	pageModel            // 模型：当前模型名 / 原生思考与推理档位 / 降级链（D176 口径一）
	pageProviders        // 模型服务：provider 编辑区（协议 / 端点 / 密钥池 / 模型能力表；D176）
	pageSecrets          // 密钥库：已存名字 + 尾 4 位掩码 / 新增覆盖 / 行内二次确认删除（S9-6b/D161）
	pageChat             // 对话：限额
	pagePrompt           // 系统提示词：人格模块树 / 有序引用 / 内置只读模块 / 组合预览（S7b/D145 独立成页）
	pageTools            // 工具与 MCP：内置工具启停 + MCP 与插件管理（S6/D143 起升级为可交互管理面）
	pageWeb              // 联网：搜索与抓取提供商、本地抓取参数（D200）
	pageAudio            // 语音：识别 / 合成服务、音色与风格、试听（D179）
	pageAdv              // 高级：config.json 原文
	// pageConstCount 页常量个数（编译期锚；见上方说明）。
	pageConstCount
)

// cycleField 点击循环枚举档（material v0.10 无下拉框）：按钮显示当前项，点击进一档。
type cycleField struct {
	click    widget.Clickable
	displays []string
	values   []string
	idx      int
}

// setValue 按键值定位；未命中保持当前（快照异常值不致越界）。
func (c *cycleField) setValue(v string) {
	for i, x := range c.values {
		if x == v {
			c.idx = i
			return
		}
	}
}

// value 当前键值（空表返回 ""）。
func (c *cycleField) value() string {
	if len(c.values) == 0 {
		return ""
	}
	return c.values[c.idx]
}

// advance 消费点击进一档（帧渲染前调用——Clickable.Clicked 自带 Update）。
func (c *cycleField) advance(gtx layout.Context) {
	if len(c.values) == 0 {
		return
	}
	for c.click.Clicked(gtx) {
		c.idx = (c.idx + 1) % len(c.values)
	}
}

// settingsForm 设置窗表单状态（次窗事件循环 goroutine 独占；控件即状态，不跨窗
// 共享）。S4d/D142 起内容分页：page = 当前页、pages = 每页各自的 material.List
// （滚动位置按页保留）、navBtns = 导航栏点击件；控件事先常驻表单对象，**切页只是换
// 渲染**——跨页编辑天然保留。行模型为动态行计划（pageRows）：行数随 provider 数量
// 变化（D110② 编辑区）。
type settingsForm struct {
	// 分页（S4d/D142；D162 起按注册表长度构造——页数不再是编译期常量）。
	page    int                // 当前页（pageGeneral..pageAdv）
	pages   []widget.List      // 每页一个滚动列表（构造处定轴向）
	navBtns []widget.Clickable // 左侧导航栏项（按页缓存）

	// 工具与 MCP 页（S6/D143）。
	tools       ToolsAdmin                   // nil = 只读占位（未接线）
	toolsSnap   ToolsSnapshot                // 每帧原子快照（本窗 goroutine 独占）
	toolsRev    int64                        // 上帧 Revision（变了才重建行/按钮）
	toolBtns    map[string]*widget.Clickable // 动作按钮（get-or-create 按 key）
	toolRes     chan toolActionResult        // 异步动作结果（回灌状态行）
	toolWait    int                          // 在途异步动作数（UI goroutine 增减）
	toolsStatus string                       // 反馈文案（状态行）
	toolsErr    bool                         // 反馈是否为错误

	// 密钥库页（S9-6b/D161）：数据面见 settings_secrets.go。动作**同步**（本地小文件 IO），
	// 故没有工具页那套结果通道；`secretBtns` 按键名分派（动作按钮 + 行内二次确认）。
	secrets       SecretsAdmin                 // nil = 只读占位（未接线）
	secretsSnap   SecretsSnapshot              // 每帧原子快照（Revision 变了才重建）
	secretsRev    int64                        // 上帧 Revision
	secretBtns    map[string]*widget.Clickable // 动作按钮（get-or-create 按 key）
	secretAdd     widget.Clickable             // 「＋ 存入」按钮
	secretImport  widget.Clickable             // 「导入旧密钥」按钮
	secretName    widget.Editor                // 新增/覆盖的键名（单行）
	secretValue   widget.Editor                // 新增/覆盖的值（单行；动作后恒清空）
	secretsStatus string                       // 反馈文案（状态行；不含值）
	secretsErr    bool                         // 反馈是否为错误
	secretConfirm string                       // 待删目标名（"" = 无；行内二次确认）
	// importStatus/importErr 一键导入的反馈（与上面那条分开：两条反馈各贴自己的按钮）。
	importStatus string
	importErr    bool

	// 核心档控件（模型名/思考/档位/外观；provider 编辑走 provs）。
	model    widget.Editor
	hotkey   widget.Editor
	fontSize widget.Editor // D90：正文字号 sp（数值，空 = 不改）
	winW     widget.Editor // D90：主窗像素宽（数值，空 = 不改）
	winH     widget.Editor // D90：主窗像素高
	profName widget.Editor // D110③：profile 名（新建/复制/切换共用输入档）
	think    widget.Bool
	unattend widget.Bool // D185：无人值守开关（常规页权限区）
	perm     cycleField
	effort   cycleField
	theme    cycleField
	scale    cycleField // D90：元素缩放（cycle 百分比档）
	save     widget.Clickable
	// D141：欢迎 / 首次运行引导入口（设置窗是唯一入口——托盘与球右键菜单都不带此项）。
	welcomeOpen widget.Clickable

	// 全量分组页控件（S4/D110③）：输出 / 限制 / 提示词 / 高级 raw JSON。
	notify    widget.Bool
	tts       widget.Bool
	maxTurns  widget.Editor
	maxCtx    widget.Editor
	compact   widget.Editor
	toolChars widget.Editor
	toolTo    widget.Editor
	rawCfg    widget.Editor // 多行：config.json 原文（Q10）

	// 人格模块树（S7/D144，S7b/D145 独立成页）：编辑区实现收在 promptEditor 里，
	// 与历史窗遮罩的「默认模板」页签共用（D154）——字段仍按原名提升，设置页侧
	// 代码不必改称。
	promptEditor

	// provider 编辑区（D110②）：每条一组控件 + 测试/设主/删除；provApply 全量应用。
	// S11-7② 起按卡片折叠（D176 口径二）：每张卡片一行摘要，同一时刻只展开一张
	// （provOpen = 展开的下标；-1 = 全收起）。
	provs       []*providerForm
	primaryIdx  int // 当前 primary（provs 下标；应用时取该条名字）
	provOpen    int // 展开的卡片下标（-1 = 全收起；界面状态，不进 config）
	provAdd     widget.Clickable
	provApply   widget.Clickable
	provStatus  string // provider 区反馈（"" = 默认提示）
	provErr     bool
	provFetched bool             // 本窗会话已自动拉过一轮服务端清单（D181：一窗一次）
	testResults chan testOutcome // 连通性测试异步结果（测试 goroutine 写、帧循环排空）
	win         *app.Window      // 结果回灌的 Invalidate（headless = nil）

	// 降级链（D176 口径三）：链是列表不是文本框——每项 provider / model，可上移/下移/删除/
	// 添加。chain/snapChain 以「磁盘快照」为基线（保存 diff + 未改动判定），链改动随
	// 「保存并应用」写回（重启生效，与 provider 变更同口径）。
	chain     []*fallbackForm
	snapChain []FallbackLink
	chainAdd  widget.Clickable
	// chainChoices 每项的 provider 下拉选项（快照现取：provider 列表可能被「模型服务」页改过，
	// 但那一改要重启才生效，故以开窗快照为准，不每帧重取）。
	chainChoices []ProviderChoice

	// 语音页（D179）：识别/合成各引用一个 provider（循环档，选项同 chainChoices），
	// 音色与风格是自由文本；试听在后台合成一句，结果经 voiceRes 回灌行内状态。
	asrProv     cycleField
	asrModel    widget.Editor
	asrLang     cycleField
	ttsProv     cycleField
	ttsModel    widget.Editor
	ttsVoice    widget.Editor
	ttsDesign   widget.Editor
	ttsStyle    widget.Editor
	ttsTags     widget.Editor
	voiceTry    widget.Clickable
	voiceRes    chan error
	voiceBusy   bool   // 试听合成在途
	voiceStatus string // 试听反馈（成功/失败）
	voiceErr    bool

	// 联网页（D200）：搜索与抓取各自一组有序提供商（顺序即优先级），加本地抓取的
	// 网络与安全参数。密钥只写不回显（空 = 保持），与 provider 密钥池同口径。
	webSearch    []*webProviderForm
	webFetch     []*webProviderForm
	webSearchAdd widget.Clickable
	webFetchAdd  widget.Clickable
	webCount     widget.Editor // 缺省条数（空 = 不改）
	webMaxChars  widget.Editor // 正文上限（空 = 不改）
	webProxy     widget.Editor
	webUA        widget.Editor
	webTimeout   widget.Editor
	webRobots    widget.Bool // 遵守 robots.txt
	webPrivate   widget.Bool // 允许私网/回环目标（调试）

	// profile 区（D110③）。
	profCreate widget.Clickable
	profCopy   widget.Clickable
	profSwitch widget.Clickable

	snap     SettingsSnapshot // 开窗快照（保存 diff 基准 + 变更提示）
	profiles ProfilesSnapshot // profile 区数据面（动作后刷新，D110③）
	status   string           // 保存反馈（本 goroutine 独占；"" = 默认提示）
	err      bool             // 反馈为错误态（红字）
	readOnly bool             // 无写回调：保存仅报只读占位
	focused  bool             // 首帧焦点入首档（其余点击自聚焦，editor.go FocusCmd）
}

// providerForm 单个 provider 的编辑控件组（D110②；本窗 goroutine 独占）。S11-7② 起
// 折叠成一张卡片（D176 口径二）：收起态只露一行摘要（`rowProvSummary`），展开才露出
// 下面的字段行。S11-7③ 起展开区含**密钥池**（多把，`keyForms`）与**模型能力表**
// （每行 name + 三个能力勾选 + 上下文窗口 + 「探测能力」按钮，`modelForms`）。
// 同一时刻只展开一张（展开多张又回到「一堵墙」）。
type providerForm struct {
	orig        string // 快照原名（Apply 保留无关键；"" = 新增）
	name        widget.Editor
	kind        cycleField // 协议（openai-chat / openai-responses / anthropic）
	baseURL     widget.Editor
	tokenizer   widget.Editor
	unsupported widget.Editor // 逗号分隔
	test        widget.Clickable
	modelsBtn   widget.Clickable // 「获取模型」（D153）：拉 /models 并列候选
	del         widget.Clickable
	setPrimary  widget.Clickable
	// 折叠卡片（S11-7②/D176 口径二）：toggle = 摘要行点击件；expanded 由表单级
	// provOpen 决定。
	toggle widget.Clickable
	// 密钥池（S11-7③/D173）：每把一组（label + 值只写不回显 + 删除），尾部「＋ 添加」。
	keyForms []*providerKeyForm
	keyAdd   widget.Clickable
	// 模型列表（S11-7③/D173；D181 起一张表装全部模型）：每行一组（启用 + 名字 + 三能力
	// 勾选 + 上下文窗口 + 探测 + 删除），尾部「＋ 添加模型」；行数不设上限，界面按
	// provModelPageSize 分页（modelPage = 当前页，越界由 Rows 夹回）。
	modelForms []*providerModelForm
	modelAdd   widget.Clickable
	modelPage  int
	// 分页按钮（D181）：行数超过一页时才画（provModelPageSize）。
	modelPagePrev widget.Clickable
	modelPageNext widget.Clickable
	// 服务端清单一行的反馈（自动拉取 / 「获取模型」共用）：成功报新增了几个、
	// 失败报原因；空 = 还没拉过。只挂在本卡片里，不占全局状态行。
	fetchNote string
	fetchErr  bool
}

// providerKeyForm 密钥池单槽编辑控件组（S11-7③）。
type providerKeyForm struct {
	snap  ProviderKeySnapshot // 快照（读侧形态：已入库 / 明文 / 空；值不回显）
	label widget.Editor       // 展示名（空 = 「第 N 把」）
	value widget.Editor       // 值（只写不回显；空 = 保持；secret: 前缀 = 照写引用；否则明文入库）
	del   widget.Clickable
}

// providerModelForm 模型能力表单行编辑控件组（S11-7③）：启用勾选 + 能力位三勾选 +
// 上下文窗口 + 探测按钮。base* 记录填表基线（快照生效值），只有用户动过勾选才把能力位
// 固化成显式声明（键缺失 = 协议缺省；不动就保持「缺省」这个形态）。
type providerModelForm struct {
	enabled     widget.Bool // D180：是否启用（勾选态；不启用 = 保留声明但不参与选型）
	name        widget.Editor
	capsTools   widget.Bool
	capsReason  widget.Bool
	capsVision  widget.Bool
	explicit    bool // 快照里该条目本来就是显式声明（用于摘要/回灌判定，不改变写回路径）
	baseTools   bool // 填表基线（判定「用户是否动过勾选」）
	baseReason  bool
	baseVision  bool
	contextWin  widget.Editor // 上下文窗口 tokens（空/0 = 不声明，跟随全局）
	probe       widget.Clickable
	del         widget.Clickable
	probeBusy   bool   // 探测在途（按钮右侧转圈；结果回灌即收）
	probeStatus string // 探测反馈（成功绿/错误红，行内）
	probeErr    bool
}

// touched 用户是否动过能力勾选（与填表基线比对）。
func (mf *providerModelForm) touched() bool {
	return mf.capsTools.Value != mf.baseTools ||
		mf.capsReason.Value != mf.baseReason ||
		mf.capsVision.Value != mf.baseVision
}

// providerSummary 折叠卡片收起态的一行摘要：名字 · 协议 · N 个模型 · M 把钥匙 · 是否主。
// 名字与协议取自**当前编辑值**（改动过程中也如实反映），钥匙数取编辑区行数。
func (pf *providerForm) providerSummary(primary bool) string {
	name := strings.TrimSpace(pf.name.Text())
	if name == "" {
		name = "（未命名）"
	}
	switch strings.TrimSpace(pf.kind.value()) {
	case "openai-responses":
		name += " · responses"
	case "anthropic":
		name += " · anthropic"
	default:
		name += " · chat"
	}
	keys := len(pf.keyForms)
	if keys < 1 {
		keys = 1 // 0 条声明也按缺省引用算一把（读侧同口径）
	}
	s := fmt.Sprintf("%s · %d 个模型 · %d 把钥匙", name, len(pf.modelForms), keys)
	if primary {
		s += " · ★ 主"
	}
	return s
}

// fallbackForm 降级链一项的编辑控件组（D176 口径三；本窗 goroutine 独占）。
// 提供者与模型都得能改：provider 走「循环选择器」（material v0.10 无下拉框，与档位同款），
// 模型是单行编辑档（可填未声明的名字——链是运行时路由）。
type fallbackForm struct {
	provIdx  int           // 在 f.chainChoices 里的下标（-1 = 未命中，保留旧名）
	provName string        // 当前 provider 名（provIdx 未命中时的兜底）
	model    widget.Editor // 模型名；空 = 取该 provider 的缺省模型
	prov     widget.Clickable
	up       widget.Clickable
	down     widget.Clickable
	del      widget.Clickable
}

// chainProvider 该项当前 provider 名（下拉命中名字，否则用保留的原始名）。
func (ff *fallbackForm) chainProvider(choices []ProviderChoice) string {
	if ff.provIdx >= 0 && ff.provIdx < len(choices) {
		return choices[ff.provIdx].Name
	}
	return ff.provName
}

// testOutcome 异步探针结果（testResults 单消费者 = 帧循环）：连通测试 / 「获取模型」/
// 「探测能力」共用一条通道，靠 kind 区分（同一 provider 的多次动作不该交错成一条状态行）。
type testOutcome struct {
	kind    int // probePing / probeModels / probeCaps
	name    string
	model   string // kind == probeCaps：被探测的模型名
	err     error
	ms      int64        // 耗时（毫秒；只报成功，失败不报时延）
	models  []string     // kind == probeModels：候选（已去重保序）
	auto    bool         // kind == probeModels：切到本页时的自动拉取（结果只与本卡片有关）
	outcome ProbeOutcome // kind == probeCaps：探测结论
}

const (
	probePing   = iota // 连通测试
	probeModels        // 获取模型列表
	probeCaps          // 探测能力（D174/S11-7③）
)

// newSettingsFormState 表单控件与固定档的构造骨架（快照无关：列表轴向 / 枚举档 /
// 单行档 / 结果通道）。与快照填表分开 = 开窗路径与测试共用同一构造口径（S4c：
// 列表轴向属构造不变量——轴向错配会让 material.List 按视口**宽**迭代，只画首行）。
func newSettingsFormState() *settingsForm {
	f := &settingsForm{
		provOpen:    -1, // S11-7②：开窗全收起（第一张也不自动展开——展开是用户的动作）
		perm:        cycleField{displays: permLevels, values: permLevels},
		effort:      cycleField{displays: effortLevels, values: effortValues},
		theme:       cycleField{displays: themeLevels, values: themeValues},
		scale:       cycleField{displays: zoomLevels, values: zoomValues},
		asrLang:     cycleField{displays: langLevels, values: langValues},
		testResults: make(chan testOutcome, 8),
		toolRes:     make(chan toolActionResult, 16),
		voiceRes:    make(chan error, 2),
		toolBtns:    map[string]*widget.Clickable{},
		secretBtns:  map[string]*widget.Clickable{},
		// 密钥库快照「上次 Revision」起点 −1：**首帧必采纳**。若起点取 0，库
		// Revision 恰为 0（空库、或本进程 provider 写路径直接落库未经过本管理面）
		// 时会被当「没变」而跳过，窗口打开即显示空列表。
		secretsRev: -1,
	}
	f.promptEditor.init()
	// S4c 防线按页复制：行列表轴向恒纵向（material.List 未设轴向 = Horizontal，
	// 按视口**宽**迭代 → 只画首行、其余空白窗底）。页数按注册表长度给（D162）。
	f.pages = make([]widget.List, settingsPageCount)
	f.navBtns = make([]widget.Clickable, settingsPageCount)
	for i := range f.pages {
		f.pages[i].Axis = layout.Vertical
	}
	for _, ed := range []*widget.Editor{&f.model, &f.hotkey, &f.fontSize, &f.winW, &f.winH, &f.profName} {
		ed.SingleLine = true // 表单单行档（Enter 无提交语义，保存走按钮）
	}
	// 密钥库页（S9-6b）：键名与值都是单行档；值档掩码显示（肩上可视读，落盘/日志仍不回显）。
	f.secretName.SingleLine = true
	f.secretValue.SingleLine = true
	f.secretValue.Mask = '•'
	return f
}

// newSettingsForm 开窗现取快照填表：读回调缺失 = 空表单，写回调缺失 = 只读占位。
func newSettingsForm(u *UI) *settingsForm {
	f := newSettingsFormState()
	f.readOnly = u.opts.ApplySettings == nil
	if u.opts.Profiles != nil {
		f.profiles = u.opts.Profiles.Snapshot() // D110③：profile 区数据面
	}
	if u.opts.ProviderMgr != nil {
		snap := u.opts.ProviderMgr.Snapshot()
		f.loadProviders(snap)    // D110②：provider 编辑区
		f.loadChainChoices(snap) // D176 口径三：降级链的 provider 选项
	}
	f.tools = u.opts.Tools     // S6/D143：工具与 MCP 管理面（nil = 只读占位）
	f.secrets = u.opts.Secrets // S9-6b：密钥库管理面（nil = 只读占位）
	f.syncSecretsSnapshot()    // 开窗即取一次（帧首同款；动作后再取）
	if u.opts.Settings == nil {
		return f
	}
	s := u.opts.Settings()
	if s.Permission == "" {
		s.Permission = "strict" // 快照未就绪（Agent 晚于 UI 构造）对齐默认档 perm.DefaultLevel，防误发切换
	}
	if s.Scale == 0 {
		s.Scale = 1.0 // D90：config 未配 = 缺省（表单显示 100%）
	}
	if s.FontSize == 0 {
		s.FontSize = fontSpBase
	}
	for _, ed := range []*widget.Editor{&f.maxTurns, &f.maxCtx, &f.compact, &f.toolChars, &f.toolTo,
		&f.asrModel, &f.ttsModel, &f.ttsVoice, &f.ttsDesign, &f.ttsStyle, &f.ttsTags,
		&f.webCount, &f.webMaxChars, &f.webProxy, &f.webUA, &f.webTimeout} {
		ed.SingleLine = true
	}
	// f.sysPrompt / f.rawCfg 保持多行（表单仅有的多行档）。
	f.snap = s
	f.model.SetText(s.Model)
	// api_key 只写不回显（D35）：字段恒空，留空 = 保持不变。
	f.hotkey.SetText(s.Hotkey)
	f.think.Value = s.Think
	f.unattend.Value = s.Unattended
	f.perm.setValue(s.Permission)
	f.effort.setValue(s.Effort)
	f.theme.setValue(s.Theme) // "" 未命中 → 首项 = 跟随系统
	f.scale.setValue(fmt.Sprintf("%g", s.Scale))
	f.fontSize.SetText(fmt.Sprintf("%g", s.FontSize))
	if s.WindowWidth > 0 {
		f.winW.SetText(strconv.Itoa(s.WindowWidth))
	}
	if s.WindowHeight > 0 {
		f.winH.SetText(strconv.Itoa(s.WindowHeight))
	}
	// S4/D110③ 全量分组页填表。
	f.notify.Value = s.Notify
	f.tts.Value = s.TTS
	if s.MaxTurns > 0 {
		f.maxTurns.SetText(strconv.Itoa(s.MaxTurns))
	}
	if s.MaxContextTokens > 0 {
		f.maxCtx.SetText(strconv.Itoa(s.MaxContextTokens))
	}
	if s.CompactThreshold > 0 {
		f.compact.SetText(fmt.Sprintf("%g", s.CompactThreshold))
	}
	if s.ToolOutputChars > 0 {
		f.toolChars.SetText(strconv.Itoa(s.ToolOutputChars))
	}
	if s.ToolTimeoutSec > 0 {
		f.toolTo.SetText(strconv.Itoa(s.ToolTimeoutSec))
	}
	f.sysPrompt.SetText(s.SystemPrompt)
	f.rawCfg.SetText(s.RawJSON)
	f.builtins = s.Builtins
	// 语音页填表（D179）：provider 选项来自本窗的 provider 快照；未配置 = 首项。
	f.setAudioProviderChoices(s.ASRProvider, &f.asrProv)
	f.setAudioProviderChoices(s.TTSProvider, &f.ttsProv)
	f.asrLang.setValue(s.ASRLang)
	f.asrModel.SetText(s.ASRModel)
	f.ttsModel.SetText(s.TTSModel)
	f.ttsVoice.SetText(s.TTSVoice)
	f.ttsDesign.SetText(s.TTSDesign)
	f.ttsStyle.SetText(s.TTSStyle)
	f.ttsTags.SetText(s.TTSTags)
	f.loadPrompts(s.PromptModules, s.PromptPersona)
	f.loadFallback(s.Fallback, f.chainChoices) // D176 口径三：降级链
	f.loadWeb(s)                               // D200：联网提供商与本地抓取参数
	return f
}

// splitRefList 逗号/分号/空白分隔的引用名列表（去空、去重且保序）。
func splitRefList(s string) []string {
	fields := strings.FieldsFunc(s, func(r rune) bool {
		switch r {
		case ',', '，', ';', '；', '\n', '\r', '\t', ' ':
			return true
		}
		return false
	})
	out := make([]string, 0, len(fields))
	seen := map[string]bool{}
	for _, name := range fields {
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		out = append(out, name)
	}
	return out
}

// addProviderForm 追加一个空白 provider 编辑组（帧循环与测试共用；Apply 校验必填项）。
// S11-7②：新条目直接展开（刚建好就等着填，收起态反而要多点一次）。
func (f *settingsForm) addProviderForm() {
	f.provs = append(f.provs, newProviderForm())
	f.provOpen = len(f.provs) - 1 // 展开新条目（同时收起其它）
}

// newProviderForm 空白 provider 编辑组（字段单行档 + 默认一个模型行与一把钥匙）。
func newProviderForm() *providerForm {
	pf := &providerForm{
		kind: cycleField{displays: kindLevels, values: kindValues},
	}
	for _, ed := range []*widget.Editor{&pf.name, &pf.baseURL, &pf.tokenizer, &pf.unsupported} {
		ed.SingleLine = true
	}
	pf.keyForms = append(pf.keyForms, newProviderKeyForm(ProviderKeySnapshot{}))
	pf.modelForms = append(pf.modelForms, newModelRow("", "", true))
	return pf
}

// newProviderKeyForm 由快照造一把钥匙的编辑控件组（值恒空——只写不回显，D35）。
func newProviderKeyForm(k ProviderKeySnapshot) *providerKeyForm {
	kf := &providerKeyForm{snap: k}
	kf.label.SingleLine = true
	kf.value.SingleLine = true
	kf.value.Mask = '•'
	kf.label.SetText(k.Label)
	return kf
}

// defaultCapsFor 某协议的缺省能力位（与装配根按协议给的缺省同口径，DESIGN §8）：
// 只用于**新建模型行**时把勾选初值画成「缺省生效的样子」——这些行尚未写进 config，
// 拿不到生效值。键缺失的语义（写回时 Caps 仍为 nil）不受影响。
func defaultCapsFor(kind string) []string {
	switch kind {
	case "openai-responses":
		return []string{port.CapTools, port.CapReasoning, port.CapVision}
	case "anthropic":
		return []string{port.CapTools, port.CapVision}
	default: // openai-chat
		return []string{port.CapTools, port.CapReasoning}
	}
}

// newProviderModelForm 由快照造一个模型行的编辑控件组；勾选初值 = 快照生效能力
// （缺省时按协议缺省给全），并记下基线用于判定用户是否动过（动了才固化显式声明）。
func newProviderModelForm(m ProviderModelSnapshot) *providerModelForm {
	mf := &providerModelForm{explicit: m.CapsExplicit}
	mf.enabled.Value = m.Enabled
	mf.name.SingleLine = true
	mf.contextWin.SingleLine = true
	mf.name.SetText(m.Name)
	for _, c := range m.Caps {
		switch c {
		case port.CapTools:
			mf.capsTools.Value = true
		case port.CapReasoning:
			mf.capsReason.Value = true
		case port.CapVision:
			mf.capsVision.Value = true
		}
	}
	mf.baseTools, mf.baseReason, mf.baseVision = mf.capsTools.Value, mf.capsReason.Value, mf.capsVision.Value
	if m.ContextWindow > 0 {
		mf.contextWin.SetText(strconv.Itoa(m.ContextWindow))
	}
	return mf
}

// newModelRow 新建一个模型行（「＋ 添加模型」/「获取模型」/候选 chip 三处共用）：按该
// provider 当前协议把缺省能力位画进勾选初值（未声明 → 写回时仍为键缺失）。enabled 区分
// 来路（D180）：用户主动加的行默认启用；自动拉回的清单默认不启用，由用户挑。
func newModelRow(name, kind string, enabled bool) *providerModelForm {
	return newProviderModelForm(ProviderModelSnapshot{Name: name, Caps: defaultCapsFor(kind), Enabled: enabled})
}

// loadProviders 以快照重建 provider 编辑区（开窗填表；快照空 = 无条目，留新增入口）。
// S11-7②：重建后全收起（展开态不跨重建保留——它是界面状态，重建即回到稳定落点）。
func (f *settingsForm) loadProviders(snap []ProviderSnapshot) {
	f.provs = make([]*providerForm, 0, len(snap))
	f.primaryIdx = 0
	f.provOpen = -1
	for i, p := range snap {
		pf := newProviderForm()
		pf.orig = p.Name
		pf.name.SetText(p.Name)
		pf.kind.setValue(p.Kind)
		pf.baseURL.SetText(p.BaseURL)
		pf.tokenizer.SetText(p.Tokenizer)
		pf.unsupported.SetText(p.Unsupported)
		// 密钥池与模型能力表按快照重建（值恒空、能力勾选取生效值、记下基线）。
		pf.keyForms = make([]*providerKeyForm, 0, len(p.Keys))
		for _, k := range p.Keys {
			pf.keyForms = append(pf.keyForms, newProviderKeyForm(k))
		}
		if len(pf.keyForms) == 0 {
			pf.keyForms = append(pf.keyForms, newProviderKeyForm(ProviderKeySnapshot{}))
		}
		pf.modelForms = make([]*providerModelForm, 0, len(p.Models))
		for _, m := range p.Models {
			pf.modelForms = append(pf.modelForms, newProviderModelForm(m))
		}
		if len(pf.modelForms) == 0 {
			pf.modelForms = append(pf.modelForms, newModelRow("", p.Kind, true))
		}
		if p.Primary {
			f.primaryIdx = i
		}
		f.provs = append(f.provs, pf)
	}
}

// loadChainChoices 降级链的 provider 选项（名字 + 启用的模型名）：取自 provider 编辑区
// 快照（同一份事实），故「模型服务」页改动后重开窗即同步；本窗内改 provider 列表不
// 影响链选项（那类改动要重启才生效，运行态本就不同步）。未启用的模型不进候选（D180）。
func (f *settingsForm) loadChainChoices(snap []ProviderSnapshot) {
	f.chainChoices = make([]ProviderChoice, 0, len(snap))
	for _, p := range snap {
		names := make([]string, 0, len(p.Models))
		for _, m := range p.Models {
			if !m.Enabled {
				continue
			}
			names = append(names, m.Name)
		}
		f.chainChoices = append(f.chainChoices, ProviderChoice{Name: p.Name, Models: names})
	}
}

// loadFallback 以快照重建降级链（开窗填表；空链 = 只留「添加」入口）。
func (f *settingsForm) loadFallback(chain []FallbackLink, choices []ProviderChoice) {
	f.snapChain = append([]FallbackLink(nil), chain...)
	f.chain = make([]*fallbackForm, 0, len(chain))
	for _, l := range chain {
		f.chain = append(f.chain, newFallbackForm(l, choices))
	}
}

// setAudioProviderChoices 语音页 provider 循环档选项：首项「未配置」，其后为 provider
// 快照里的名字；当前值不在快照内时补一项，避免开窗即把它悄悄改成「未配置」。
func (f *settingsForm) setAudioProviderChoices(cur string, c *cycleField) {
	displays := []string{"未配置"}
	values := []string{""}
	for _, p := range f.chainChoices {
		if p.Name == "" || slices.Contains(values, p.Name) {
			continue
		}
		displays = append(displays, p.Name)
		values = append(values, p.Name)
	}
	if cur != "" && !slices.Contains(values, cur) {
		displays = append(displays, cur)
		values = append(values, cur)
	}
	c.displays, c.values = displays, values
	c.setValue(cur) // "" 命中首项 = 未配置
}

// addFallbackForm 追加一项空白降级链（帧循环与测试共用；provider 缺省取首个选项）。
func (f *settingsForm) addFallbackForm() {
	f.chain = append(f.chain, newFallbackForm(FallbackLink{}, f.chainChoices))
}

// newFallbackForm 由一项链配置造编辑控件组；provIdx 按名命中选项（未命中 = -1，保留名）。
func newFallbackForm(l FallbackLink, choices []ProviderChoice) *fallbackForm {
	provIdx := -1
	for i, c := range choices {
		if c.Name == l.Provider {
			provIdx = i
			break
		}
	}
	if provIdx < 0 && len(choices) > 0 && strings.TrimSpace(l.Provider) == "" {
		provIdx = 0 // 新增项缺省指向首个 provider
	}
	ff := &fallbackForm{provIdx: provIdx, provName: strings.TrimSpace(l.Provider)}
	ff.model.SingleLine = true
	if s := strings.TrimSpace(l.Model); s != "" {
		ff.model.SetText(s)
	}
	return ff
}
