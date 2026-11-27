// Package pluginapi 是 Aquarius Tier-1 插件的对外稳定契约（plugin-design.md，D327）。
//
// 本包独立 go.mod、零依赖（只许标准库），按「只增不改」的版本纪律演进：不改已有
// 字段的语义与类型；新字段只加在结构体末尾并注释零值语义；新方法只加新的独立接口；
// 签名冻结，破坏性变更开 v2 并存。插件编译到本包上，永不编译到内核类型上。
//
// 状态（v1.0.0 的 P3）：Manifest、稳定值类型层（value.go）与接口面（plugin / host /
// llm / tool / command / memory / ingest / modality / secrets / uiext）已落盘；
// plugingo 转换器与装载器随后，契约在本版收尾时完整冻结。规格见 docs/plugin-design.md。
package pluginapi

// APIVersion 当前契约的 API 版本：宿主启动时断言 Manifest.API == APIVersion，
// 不匹配拒载并报因（plugin-design.md §7 校验条）。版本随破坏性变更递增（届时开 v2 包）。
const APIVersion = 1

// Manifest 插件清单：编译进插件的自我声明，宿主启动时逐项校验。
// 字段即对外承诺——新增字段只加末尾并注释零值语义；改语义开 v2。
type Manifest struct {
	// Name 插件名：全局唯一，与内置工具、MCP server 端点撞名即拒载。
	Name string
	// API 本插件编译所用的契约版本，必须等于 APIVersion。
	API int
	// Capabilities 能力自述（network 等）：Tier-1 无进程隔离，capabilities 只作
	// 对外展示与审计归组（/plugin 可见），不触发授权弹窗（D327 展示语义）。
	// 空 = 不宣称任何能力。
	Capabilities []string
	// Provides 供给面自述（"llm" / "tool" / "memory" / "ingest" / "output" /
	// "transcribe" / "synthesize" / "secrets" / "command" / "view" / "settings"）：宿主据此做 provides
	// 自检（声明 llm 必须实现 LanguageModel）。"memory" 是独占的：同一时刻至多
	// 一个插件启用，两个同时启用由宿主拒绝（D337）。空 = 不供给任何端口。
	Provides []string
	// Locales 插件自带的 UI 文案语种（"zh" / "en"）：插件文案不进内核 i18n 表
	//（plugin-design.md §6 纪律 2）。宿主不替插件选文案，只是经 Host.Locale() 告知
	// 当前语言，插件自己按本字段挑；没带宿主那种语言就用自己的缺省。
	// 空 = 无 UI 文案（不贡献设置页与面板）。
	Locales []string
}
