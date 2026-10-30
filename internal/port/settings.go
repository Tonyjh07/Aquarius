package port

// 设置窗的提交与视图 DTO（D229）。
//
// 这些类型为什么在 port 而不在 uigui：设置窗的**语义规则**（哪个字段落到 config 哪个
// 键、取值范围、留空是什么意思）属于内核，不属于界面。规则要挪进 app 与装配根，而
// `app` 不得 import `adapter`、`uigui` 不得 import `app`（双向分层守卫），所以两边
// 都要认的那份数据形状只能落在中间层。port 是唯一同时被两侧依赖的包。
//
// 另一条约束是「界面适配器不能成为公共依赖」：`uigui` 只允许装配根引用。DTO 若留在
// 那里，规则实现方就得反过来依赖界面，拆包时整条设置逻辑都被钉在 uigui 上。
//
// uigui 侧保留同名类型别名（`type SettingsPatch = port.SettingsPatch`），所以既有的
// 约 90 处 `uigui.SettingsPatch{...}` 字面量不用改。

// SettingsPatch 设置窗保存提交（表单全量）。
//
// 数值键 0 = 不改（表单留空 = 保持快照原值的既有口径）；Notify/TTS 恒写（布尔无留空
// 语义）；SystemPrompt 恒写（清空 = 合法语义「内置默认」）；RawJSON 非空 = 高级页整
// 文件替换（保存前由写回方校验 JSON）。
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
	// BackupKeep 会话树保留的备份代数（D254）：0 = 不改（表单留空 = 保持现值）。
	BackupKeep   int
	SystemPrompt string
	// PromptModules/PromptPersona 人格模块树与有序引用（S7/D144，恒全量写：空 = 清空）。
	// 空名模块行被跳过；模块名校验（非空/不以 @ 开头/不重复）在写回侧拦截。
	PromptModules []PromptModuleView
	PromptPersona []string
	RawJSON       string // "" = 不改
	// Fallback 降级链（D176 口径三；恒全量写：空 = 清空）。链条目 {provider, model}，
	// 模型可为空（取该 provider 的缺省模型）；写回侧校验 provider 名存在。
	Fallback []FallbackLink
	// 语音（D179；恒全量写）：provider 空 = 该能力关闭；model 非空且 provider 空时
	// 原样保留（不生效但不丢用户输入）；写回侧校验引用的 provider 存在。
	ASRProvider string
	// ASRKind / TTSKind 协议位（空 = chat-audio）；ASRModelPath 是本地引擎的模型文件。
	ASRKind      string
	ASRModel     string
	ASRModelPath string
	ASRLang      string // auto | zh | en
	TTSProvider  string
	TTSKind      string
	TTSModel     string
	TTSVoice     string
	TTSDesign    string
	TTSStyle     string
	TTSTags      string
	// 语音的端点与密钥（D222）：BaseURL 空 = 借 ASRProvider/TTSProvider 的；Key 空 =
	// 保持原引用（只写不回显），`secret:` 照写引用、其余明文转进密钥库。
	ASRBaseURL string
	ASRKey     string
	TTSBaseURL string
	TTSKey     string

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

	// 采样参数（D204；恒全量写）：空串 = 该项不发送（删键），有值 = 照写（含 0）。
	// 与「数值 0 = 不改」的限额口径不同——这里 0 是有效取值，不能被当成「没填」。
	// SamplingSet 区分「设置窗保存（整段替换）」与「窄写回（别动采样）」——窄写回
	// （人格模板、provider 应用等）给的是零值，不该顺手把用户的采样参数抹平。
	SamplingSet         bool
	SamplingTemperature string
	SamplingTopP        string
	SamplingMaxTokens   string
	SamplingStop        string
}

// FallbackLink 降级链一项（D173/D176 口径三）：provider + 模型名。模型可为空
// （= 取该 provider 的缺省模型），也可手填未声明的名字——链是运行时路由，不要求
// 目标模型已写进 provider 的 models 声明（声明只影响能力位与窗口）。
type FallbackLink struct {
	Provider string
	Model    string
}

// WebProviderPatch 联网提供商一条的提交（D200/D222）。Key 空 = 保持原值（只写不回显）；
// `secret:` 前缀照写引用，否则明文转进密钥库（与 provider 页同口径）。
type WebProviderPatch struct {
	Kind    string
	From    string
	Key     string
	BaseURL string
	Country string
	Lang    string
	Count   int
}

// PromptModuleView 人格模块树单个节点的编辑视图（S7/D144）：Name 唯一（用户模块名不以
// `@` 开头，该前缀保留给内置模块）；Text = 叶正文（组亦可带，作组头）；Children = 子模块
// 引用名（有序，可再嵌套 → 多级细分）。数据面真源 = config `prompts.modules`。
type PromptModuleView struct {
	Name     string
	Text     string
	Children []string
}

// 设置项的取值范围（D229）。
//
// 这些数是**内核语义**，不是界面偏好：字号决定正文排版的可读区间，窗口尺寸决定浮窗能
// 贴在哪，搜索条数与抓取上限决定一次请求花多少钱，采样温度决定一次生成有多随机。
//
// 为什么收成一张表：同一个数原先写了两到三份——界面表单填一次、启动期配置校验一次、
// 抓取实现再兜一次——改一处忘一处就是「设置窗能存进去、重启却被拒」这类故障。现在
// 界面只负责照表提示（用户一点保存就看到错误，不必等落盘），装配根与启动校验照表权威
// 判定，两侧引用同一组常量。放在 port 而不是界面包里，是因为内核侧也要用，而反向依赖
// 界面既过不了分层守卫，也会把这张表钉死在界面包里。
const (
	// SettingsFontSizeMin/Max 正文字号（sp）。低于下限正文在 125% 缩放下挤成一团，
	// 高于上限一行放不下几个字。界面侧另据它做越界夹取（回落正文基准 15sp）。
	SettingsFontSizeMin = 10.0
	SettingsFontSizeMax = 28.0

	// SettingsScaleMin/Max 元素缩放倍率的硬区间。设置窗只提供 100%–200% 五个档，但
	// 手改过 config 的旧值可以落在更宽的范围里，夹取按这一对算（越界回落 1.0）。
	SettingsScaleMin = 0.75
	SettingsScaleMax = 2.5

	// SettingsWindowWidthMin/Max 与 SettingsWindowHeightMin/Max 主窗像素粗界：界面照它
	// 夹取用户填的尺寸，再叠加布局地板（240dp × DPI × 缩放）抬下限。
	SettingsWindowWidthMin  = 200
	SettingsWindowWidthMax  = 3840
	SettingsWindowHeightMin = 200
	SettingsWindowHeightMax = 2160

	// SettingsCompactMin/Max 自动压缩阈值（占上下文窗口的比例）。
	SettingsCompactMin = 0.0
	SettingsCompactMax = 1.0

	// SettingsBackupKeepMin/Max 会话树保留的备份代数（D254）。至少一代，否则等于没有
	// 兜底；上限 20 是「够用但不至于把磁盘塞满」的粗界。配置侧只在 `< 1` 时回落默认 3，
	// 手改 config 写更大的值仍按原值走——这里限的是界面入口。
	SettingsBackupKeepMin = 1
	SettingsBackupKeepMax = 20

	// SettingsSearchCountMin/Max 一次联网搜索取几条结果。
	SettingsSearchCountMin = 1
	SettingsSearchCountMax = 20
	// SettingsFetchCharsMin/Max 抓取正文上限（字符）。
	SettingsFetchCharsMin = 500
	SettingsFetchCharsMax = 20000
	// SettingsFetchTimeoutMin/Max 抓取超时（秒）。
	SettingsFetchTimeoutMin = 1
	SettingsFetchTimeoutMax = 600

	// SettingsTemperatureMin/Max 采样温度。上限 2 是「还能读」的边界；协议更严的限制
	// （Anthropic 只接 0–1）由启动校验按协议细判，不在这里一刀切。
	SettingsTemperatureMin = 0.0
	SettingsTemperatureMax = 2.0
	// SettingsTopPMin/Max 核采样。三协议口径一致。
	SettingsTopPMin = 0.0
	SettingsTopPMax = 1.0
)
