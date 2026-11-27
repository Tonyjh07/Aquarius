package port

import "context"

// pluginsettings.go 插件设置贡献在内核侧的中性形态（pluginapi.SettingsSectionProvider 的
// 转换目标）。理由与 uiview.go 相同：两个稳定级互不引用，界面层只认这里；插件换组件词表
// 不该牵动内核重编。
//
// 语义边界：**哪个键落到哪、留空什么意思、取值区间，全在插件侧**。内核只做三件事——
// 把行表渲染成控件、把用户改过的值原样回传、把插件报的错误显示出来。这条边界让宿主不必
// 认识任何具体插件的参数，也让插件改自己的参数时不用改内核。

// 设置行的控件种类。取值与 pluginapi.FieldKind 同名（改名要两边一起改，由 plugingo 的
// 转换测试兜住）。
const (
	PluginFieldText     = "text"     // 单行文本
	PluginFieldTextarea = "textarea" // 多行文本
	PluginFieldSwitch   = "switch"   // 开关；值用 "true" / "false"
	PluginFieldSelect   = "select"   // 下拉；Values 是可选值
	PluginFieldNumber   = "number"   // 数字；值是十进制字符串
)

// PluginSettingRow 一行设置：键、标签、说明、控件种类与当前生效值。
//
// Key 由插件定义，内核只按它回传，不认识它的含义。Value 永远是**当前生效值**（不是
// 默认值、不是待保存值）——界面拿它当初始值，保存成功后重新取一次快照。
type PluginSettingRow struct {
	Key    string
	Label  string
	Help   string
	Kind   string
	Value  string
	Values []string // 仅 select 用
}

// PluginSettingsSection 一个插件贡献的设置分区。
//
// Plugin 是插件名（回传时用来定位实例），ID 是插件自己的分区标识（只在那个插件里有意义）。
// Desc 是分区说明，可为空。
type PluginSettingsSection struct {
	Plugin string
	ID     string
	Title  string
	Desc   string
	Rows   []PluginSettingRow
}

// PluginSettings 插件设置面（装配根聚合，设置窗消费）。
//
// 与 PluginViews 同一条约定：每次调用都现问插件，不缓存声明——停用/换实例后旧声明就是
// 错的。Apply 只提交**本分区**被用户改过的键。
type PluginSettings interface {
	Sections() []PluginSettingsSection
	Apply(ctx context.Context, plugin, section string, values map[string]string) error
}
