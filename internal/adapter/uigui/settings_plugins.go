package uigui

// settings_plugins.go 插件清单与启停的数据面（插件页上半部：开关与入口）。
//
// 与 ToolsAdmin 分开的理由：那边管的是 MCP server（外部进程、拨号、信任指纹、能力授权），
// 这边管的是编译进二进制的 Tier-1 插件（注册表状态机）。两类东西的失败模式与动作集都不一样，
// 摊在同一页上用户分不清「关掉的是工具还是插件」——D347 的插件页接手第一类。
//
// 并发规则同 §15.7：快照在开窗与动作完成后现取（插件启停会改供给面，缓存就是错的）；
// 启停可能弹确认（记忆接管、能力授权），一律异步，结果回灌状态行。

// PluginAdmin 插件清单与启停（装配根实现；线程安全——设置窗 goroutine 调用）。
type PluginAdmin interface {
	// Snapshot 全部已编译进来的插件（含停用态与校验失败者）。
	Snapshot() []PluginState
	// SetEnabled 启停一个插件。启用含信任门与能力授权流，可能弹主窗确认。
	SetEnabled(name string, on bool) error
}

// PluginState 一个插件在界面上的样子。
type PluginState struct {
	Name string
	// State 注册表状态串（active / stopped / draining …）。**原样**给到界面，
	// 展示名由 pluginStateLabel 换，不在数据面翻译。
	State    string
	Enabled  bool
	Provides []string // 供给面清单（memory / tool / command / view / settings …）
	Err      string   // 最近一次失败原因；空 = 没有记录
}
