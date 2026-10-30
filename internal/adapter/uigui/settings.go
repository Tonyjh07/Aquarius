package uigui

// 设置窗（§15.7/D60 核心档 + §15.4/D61 主题档）：跨层契约类型（快照 / 补丁 / 管理者接口）、
// 枚举档与页常量。表单状态与两段构造见 settings_form.go，provider 编辑组见
// settings_provider_form.go，行计划与渲染见 settings_rows.go，保存流见 settings_actions.go。
// 并发口径（§15.7）：表单状态仅次窗事件循环 goroutine 读写、不跨窗共享；持久化经
// ApplySettings 单一写回调（装配根实现），内核命令经 inCh 排队到主循环与键入同路径串行执行。

import (
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
	// CostIn / CostOut 单价，**每百万 token**（D205，与厂商报价同口径）；0 = 未声明，
	// 未声明就不折算费用（宁可没有数，也不给一个猜的）。
	CostIn  float64
	CostOut float64
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
	// CostIn / CostOut 单价（每百万 token，D205）；0 = 不声明（界面留空即此值）。
	CostIn  float64
	CostOut float64
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

// FallbackLink 降级链一项（D173/D176 口径三）。定义在 port：写回规则要挪进 app 与
// 装配根，两边都得认这份形状，而 uigui 不能成为公共依赖。
type FallbackLink = port.FallbackLink

// ProviderChoice 「模型」页降级链编辑用的 provider 选项：名字 + 它声明的模型名。
// 与 ProviderManager 的编辑面分开——那边编辑 provider 本体（怎么连），这里只挑路由目标。
//
// BaseURL / KeyRef（D222）：「联网」与「语音」页的「填入」按钮用它把某个服务的端点与
// 密钥引用抄进独立配置。KeyRef 只会是 `secret:<名>` 或空（明文永不出编辑区）。
type ProviderChoice struct {
	Name    string
	Models  []string
	BaseURL string
	KeyRef  string
}

// WebProviderView 联网提供商一条的编辑视图（D200/D222）：kind 决定怎么连，其余字段按
// kind 取用（与 cmd 侧 webProvider 同构）。端点与密钥可以自己填，也可以借某个
// model.providers 条目——From 只是「借」的那条，不再决定端点与密钥的唯一来源。
// 密钥**永不回显**：只给形态标记（Saved = config 里是 `secret:<名>` 引用、Plain = 明文、
// Ref = 仅 Saved 时的引用名）。
type WebProviderView struct {
	Kind     string // 搜索侧 deepseek|brave；抓取侧 deepseek|local
	From     string // 借哪个 model.providers（deepseek 缺省 = "deepseek"）
	BaseURL  string // 端点（空 = 借 From 的；都没填 = 报因）
	Country  string // 仅 brave
	Lang     string // 仅 brave
	Count    int    // 仅搜索：这一家的缺省条数（0 = 用 web.search.count）
	KeySaved bool
	KeyPlain bool
	KeyRef   string
}

// WebProviderPatch 联网提供商一条的提交（D200/D222）。Key 空 = 保持原值（只写不回显）；
// `secret:` 前缀照写引用，否则明文转进密钥库（与 provider 页同口径）。定义在 port，
// 理由同 FallbackLink。
type WebProviderPatch = port.WebProviderPatch

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

// PromptModuleView 人格模块树单个节点的编辑视图（S7/D144）。定义在 port，理由同
// FallbackLink（数据面真源 = config `prompts.modules`）。
type PromptModuleView = port.PromptModuleView

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
	// BackupKeep 会话树保留的备份代数（D254；0 = 缺省 3，与 config 键缺失同口径）。
	BackupKeep    int
	SystemPrompt  string              // 默认人格（= 内置模块 @default 正文；空 = 内置默认）
	PromptModules []PromptModuleView  // 人格模块树（config prompts.modules，按名排序）
	PromptPersona []string            // 人格有序引用（config prompts.persona）
	Builtins      []BuiltinModuleView // 内置只读模块（展示 + 引用，S7/D144）
	MCPServers    []string            // 只读：声明名清单（sorted）
	Plugins       []string            // 只读：声明名清单（sorted）
	RawJSON       string              // config.json 原文（高级页编辑起点）

	// 语音（D179）：ASR/TTS 各自引用 model.providers 里的一个 name；空 = 该能力未配置。
	// 只借 base_url 与密钥池，不继承 kind/能力位、不进降级链。
	ASRProvider string
	// ASRKind 识别协议：chat-audio | whisper | openai | funasr（空 = chat-audio）。
	ASRKind  string
	ASRModel string
	// ASRModelPath 本地识别模型文件路径（whisper 用；别的协议不看）。
	ASRModelPath string
	ASRLang      string // 录音语种提示：auto | zh | en
	TTSProvider  string
	// TTSKind 合成协议：chat-audio | vits | gptsovits（空 = chat-audio）。
	TTSKind   string
	TTSModel  string
	TTSVoice  string // 预置音色名；-voiceclone 模型填样本文件绝对路径
	TTSDesign string // -voicedesign 模型的音色描述
	TTSStyle  string // 自然语言风格指令
	TTSTags   string // 风格标签（拼成 "(标签)" 前缀）
	// 语音的端点与密钥（D222）：可以自己填，也可以借 ASRProvider/TTSProvider 指向的
	// 条目。密钥永不进快照（只给形态标记，D35 同口径）。
	ASRBaseURL  string
	ASRKeySaved bool
	ASRKeyPlain bool
	ASRKeyRef   string
	TTSBaseURL  string
	TTSKeySaved bool
	TTSKeyPlain bool
	TTSKeyRef   string

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

	// 采样参数（D204）：整链缺省。**空串 = 不发送**（交服务端默认），"0" = 真的发 0。
	// 用字符串而不是 float 承载，是为了让「留空」与「写了 0」在表单里可区分。
	SamplingTemperature string
	SamplingTopP        string
	SamplingMaxTokens   string
	SamplingStop        string // JSON 数组文本，如 ["\n"]（空 = 不发送）
}

// SettingsPatch 设置窗保存提交（表单全量；模型服务走 ProviderManager.Apply，D110②）。
//
// 定义在 port 而不是本包（D229）：本包只负责**采集表单并渲染**，字段落到 config 的
// 哪个键、取值范围、留空什么意思属于内核规则，要由 app 与装配根实现；而分层守卫禁止
// app import adapter、也禁止 uigui import app，两边共用的数据形状只能落在 port。留成
// 别名是为了既有调用点（约 90 处字面量）不用改。
type SettingsPatch = port.SettingsPatch

// 枚举档（展示值 + 键值；内核 /permission /effort 的校验为单一事实源，此处只负责渲染）。
var (
	// permLevels 权限档（§5/D22，与 domain/perm.Level 对齐）。
	permLevels = []string{"read-only", "strict", "permissive", "full-access"}
	// effortLevels 推理档位（D34）：首项 = 未设（键值 "" = 清除态）。
	effortLevels = []string{"未设", "minimal", "low", "medium", "high"}
	effortValues = []string{"", "minimal", "low", "medium", "high"}
	// themeLevels 主题档展示与键值（§15.4/D61：system|light|dark）。
	themeLevels = []string{"跟随系统", "浅色", "深色"}
	themeValues = []string{"system", "light", "dark"}
	// zoomLevels 元素缩放档（D90/§15.8）：展示百分比，键值 = 倍率字符串（selectField
	// 键值为 string 的既有口径，表单侧 ParseFloat 换算；%g 往返一致）。
	zoomLevels = []string{"100%", "125%", "150%", "175%", "200%"}
	zoomValues = []string{"1", "1.25", "1.5", "1.75", "2"}
	// langLevels 录音语种提示（D179）：只喂给识别，不改变界面语言。
	langLevels = []string{"自动识别", "中文", "英文"}
	langValues = []string{"auto", "zh", "en"}
	// asrKindLevels / ttsKindLevels 协议档：展示名是人话，键值是 config 里的 kind 原文。
	// 首项是缺省（chat-audio）。
	asrKindLevels = []string{"对话接口", "本地 whisper", "OpenAI 转写", "FunASR 原生"}
	asrKindValues = []string{"chat-audio", "whisper", "openai", "funasr"}
	ttsKindLevels = []string{"对话接口", "VITS", "GPT-SoVITS"}
	ttsKindValues = []string{"chat-audio", "vits", "gptsovits"}
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
	pageGeneral    = iota // 常规：Profile 区 / 欢迎引导 / 权限等级 / 外观与呼出 / 输出
	pageModel             // 模型：当前模型名 / 原生思考与推理档位 / 降级链（D176 口径一）
	pageProviders         // 模型服务：provider 编辑区（协议 / 端点 / 密钥池 / 模型能力表；D176）
	pageSecrets           // 密钥库：已存名字 + 尾 4 位掩码 / 新增覆盖 / 行内二次确认删除（S9-6b/D161）
	pageChat              // 对话：限额
	pagePrompt            // 系统提示词：人格模块树 / 有序引用 / 内置只读模块 / 组合预览（S7b/D145 独立成页）
	pageTools             // 工具与 MCP：内置工具启停 + MCP 与插件管理（S6/D143 起升级为可交互管理面）
	pageSkills            // 技能：已装清单 + 四种导入入口 + 更新/卸载 + 额外目录（D202/D203）
	pageCharacters        // 角色：角色卡清单 + 头像 + 导入/删除/开始对话（§5.17）
	pageWeb               // 联网：搜索与抓取提供商、本地抓取参数（D200）
	pageAudio             // 语音：识别 / 合成服务、音色与风格、试听（D179）
	pageAdv               // 高级：config.json 原文
	pageAbout             // 关于：图标 / 名字与一句话 / 构建版本 / 运行环境 / 项目主页 / 版权与许可
	// pageConstCount 页常量个数（编译期锚；见上方说明）。
	pageConstCount
)
