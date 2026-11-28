package pluginapi

import "context"

// uiext.go UI 扩展面（plugin-guide.md §6）：插件交声明，不交像素。
//
// 本文件不出现任何 GUI 类型，也没有回调式绘制：插件产出一棵语义组件树，界面层各渲染各的
// （图形界面按 Gio 渲染，文本界面降级成对齐文本）。同一份数据两个消费者，将来换界面技术栈
// 插件不用改。
//
// 插件不推送界面：内核来取当前帧（Render），用户操作时把动作交回来（OnAction），插件按
// 自己的状态算出新树返回。状态权威始终在插件侧，内核只持当前帧。

// NodeKind 组件树节点种类。词表冻结：新增一种要两个渲染器各配一段降级行为。
// 兼容承诺：改取值会破坏两个界面渲染器与所有贡献面板的插件。
type NodeKind string

const (
	NodeText     NodeKind = "text"     // Text 是纯文本
	NodeMarkdown NodeKind = "markdown" // Text 走内核的 markdown 渲染管线
	NodeTable    NodeKind = "table"    // Table 给列与行；文本界面降级为对齐文本
	NodeKV       NodeKind = "kv"       // Items 按「名」「值」成对排列
	NodeList     NodeKind = "list"     // Items 逐行；文本界面降级为编号列表
	NodeForm     NodeKind = "form"     // Fields 逐字段；文本界面降级为逐行问答
	NodeButtons  NodeKind = "buttons"  // Actions 横向按钮；文本界面降级为数字选择
)

// Node 组件树的一个节点。各字段按 Kind 取用，其余留零值。
//
// Kind 为零值 = **纯容器**：自身不产生任何像素（也不产生任何文本行），只按顺序渲染
// Children。要「把几块并成一棵」就用它，不要借 list 之类的种类来充当容器——那会让
// 渲染器多出一行空列表。
//
// Children 与任何 Kind 都可以同时出现：渲染器先画该 Kind 自身，再依次画子节点。这条
// 让插件能把「一张表 + 它的按钮」放进同一个节点下，而不必为「组合」发明新种类。
// 兼容承诺：改字段会破坏两个界面渲染器与所有贡献面板的插件。
type Node struct {
	Kind     NodeKind
	Text     string
	Table    *TableSpec
	Items    []string
	Fields   []FormField
	Actions  []Action
	Children []Node
}

// TableSpec 表格：列名 + 行。单元格是纯文本，不走 markdown。
// 兼容承诺：改字段会破坏面板插件与表格渲染。
type TableSpec struct {
	Columns []string
	Rows    [][]string
}

// FieldKind 表单字段种类。词表冻结。
// 兼容承诺：改取值会破坏表单渲染与插件声明的字段。
type FieldKind string

const (
	FieldText     FieldKind = "text"     // 单行文本
	FieldTextarea FieldKind = "textarea" // 多行文本
	FieldSwitch   FieldKind = "switch"   // 开关；Value 用 "true" / "false"
	FieldSelect   FieldKind = "select"   // 下拉；Values 是可选值
	FieldNumber   FieldKind = "number"   // 数字；Value 是十进制字符串
)

// FormField 一个表单字段：标签 + 当前值。提交时字段值按 Key 收进 ViewAction.Values。
// 兼容承诺：改字段会破坏表单渲染与面板插件。
type FormField struct {
	Key   string
	Label string
	Help  string
	Kind  FieldKind
	Value string
	// Values select 的可选值；其他类型留空。
	Values []string
}

// Action 一个可点的动作：ID 由插件定义，Label 是用户面文字（插件文案，不进内核文案表）。
// 兼容承诺：改字段会破坏按钮渲染与面板插件。
type Action struct {
	ID    string
	Label string
}

// ViewSpec 一个面板视图的声明：ID 与标题。标题是插件文案。
//
// Window 声明这一页要多大（dp）。零值 = 宿主缺省，宿主会夹到合理区间；文本界面没有
// OS 窗口，忽略这些值。窗口是**宿主的**：插件只说尺寸，不拿到窗口句柄，也不见任何 GUI
// 类型（§6 总原则）。
// 兼容承诺：改字段会破坏面板注册与菜单展示。
type ViewSpec struct {
	ID     string
	Title  string
	Window WindowSpec
}

// WindowSpec 面板窗口的尺寸请求（dp）。
//
// 只是请求：宿主把宽高夹进 [320,4096]、最小尺寸不超过声明尺寸，并且只在有 OS 窗口的
// 前端生效。没有「置顶 / 无边框 / 位置 / 父子窗」这些字段——窗口形态由宿主统一，插件
// 一旦能定制形态，用户就会遇到每个插件一张脸的窗口。
// 兼容承诺：改字段会破坏申请窗口的插件与宿主窗管理器。
type WindowSpec struct {
	// Width/Height 初始尺寸；0 = 宿主缺省。
	Width, Height int
	// MinWidth/MinHeight 最小尺寸；0 = 宿主缺省。
	MinWidth, MinHeight int
}

// ViewAction 用户的一次操作：点了哪个按钮（ActionID），或提交了哪些字段（Values）。
// 表单提交与按钮点击可能同时发生——ActionID 为空表示只是提交字段。
// 兼容承诺：改字段会破坏面板插件与界面的回调。
type ViewAction struct {
	ViewID   string
	ActionID string
	Values   map[string]string
}

// ViewProvider 面板贡献（可选接口，内核按 type assertion 探测）。
// 兼容承诺：改方法集或签名会破坏所有贡献面板的插件。
type ViewProvider interface {
	// Views 声明这个插件有哪些面板。声明在手，内核据此建菜单与命令。
	Views() []ViewSpec
	// Render 取当前帧。内核每次要画都来问一次，所以实现要便宜（读自己的内存状态，
	// 别在这做网络请求）。
	Render(ctx context.Context, viewID string) (Node, error)
	// OnAction 处理一次用户操作，返回操作后的新帧。act.Values 里是本次提交的字段值；
	// 未提交字段不在表里，不要当成「被清空」。
	OnAction(ctx context.Context, act ViewAction) (Node, error)
}

// SettingsSectionProvider 设置页贡献（可选接口，内核按 type assertion 探测）。
// 语义规则全在插件侧：哪个值落哪个配置键、留空什么意思，都由插件决定；内核只管
// 表单渲染与提交框。
// 兼容承诺：改方法集或签名会破坏所有贡献设置页的插件。
type SettingsSectionProvider interface {
	// Sections 声明设置分区。Value 是当前生效值，内核原样展示。
	Sections() []SettingsSection
	// ApplySettings 收到用户对自己那一段的改动。只包含本分区提交的键。
	ApplySettings(ctx context.Context, patch SettingsPatch) error
}

// SettingsPlacement 设置分区放在哪里。
//
// 缺省（""）与 SettingsInline 等价：内联在「插件」页，与插件清单同一屏。
// SettingsPage 表示这一段参数多、需要大段说明或需要独占视野——宿主会把它放进只属于
// 这个插件的一页里，入口仍在插件清单上。
//
// 为什么不按行数自动判定：多少行算「多」是插件自己的语义（有些行一个数字就够，有些
// 一行背后要读三段说明），宿主数不出这件事。宿主只给缺省，插件有权覆盖。
// 兼容承诺：改两个取值词会破坏声明设置分区的插件与设置窗路由。
type SettingsPlacement string

const (
	// SettingsInline 内联在插件页（缺省）。
	SettingsInline SettingsPlacement = ""
	// SettingsPage 独占一页，从插件清单进入。
	SettingsPage SettingsPlacement = "page"
)

// SettingsSection 一个设置分区：标题、说明与若干行。
// 兼容承诺：改字段会破坏设置窗渲染与插件声明。
type SettingsSection struct {
	ID    string
	Title string
	Desc  string
	Rows  []SettingsRow
	// Placement 这一段放在哪（缺省内联）。只影响呈现位置，不影响提交语义。
	Placement SettingsPlacement
}

// SettingsRow 一行设置：标签 + 当前值 + 字段种类。
// Key 是插件自己的键，内核只按它回传，不认识它的含义。
// 兼容承诺：改字段会破坏设置窗渲染与插件声明。
type SettingsRow struct {
	Key   string
	Label string
	Help  string
	Kind  FieldKind
	Value string
	// Values select 的可选值；其他类型留空。
	Values []string
}

// SettingsPatch 一次设置提交：哪个分区、哪些键改成了什么。
// 兼容承诺：改字段会破坏设置窗的提交路径与插件实现。
type SettingsPatch struct {
	SectionID string
	Values    map[string]string
}
