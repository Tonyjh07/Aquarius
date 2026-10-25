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
	SystemPrompt     string
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
