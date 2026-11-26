package port

import "context"

// uiview.go 插件面板在内核侧的中性形态（pluginapi/uiext.go 的防腐转换目标）。
//
// 为什么不是直接引用 pluginapi.Node：两个稳定级互不引用（doc.go），界面层是内核的一部分，
// 不能因为插件的组件词表变化就重编。plugingo 做一次平铺转换，Gio 与文本两个渲染器只认这里。
//
// 词表与 pluginapi 一一对应、取值同名（"text" / "markdown" / …）。改名要两边一起改，
// 由 adapter 层的转换测试兜住。

// UIKind 组件种类（取值与 pluginapi.NodeKind 同名）。
type UIKind string

const (
	UIText     UIKind = "text"     // Text 是纯文本
	UIMarkdown UIKind = "markdown" // Text 走 markdown 渲染管线
	UITable    UIKind = "table"    // Columns + Rows
	UIKV       UIKind = "kv"       // Items 按「名」「值」成对
	UIList     UIKind = "list"     // Items 逐行
	UIForm     UIKind = "form"     // Fields 逐字段
	UIButtons  UIKind = "buttons"  // Actions 横向排列
)

// UIViewInfo 插件声明的一个面板（列表用）。
type UIViewInfo struct {
	Plugin string // 插件名（打开时回传）
	ID     string // 视图 ID（插件自己的命名空间）
	Title  string // 标题（插件文案，不进内核 i18n）
}

// UINode 组件树的一个节点。各字段按 Kind 取用，其余留零值。
type UINode struct {
	Kind     UIKind
	Text     string
	Columns  []string
	Rows     [][]string
	Items    []string
	Fields   []UIFormField
	Actions  []UIAction
	Children []UINode
}

// UIFormField 一个表单字段。Kind 取值同 pluginapi.FieldKind：text / textarea / switch /
// select / number；switch 的 Value 是 "true" / "false"。
type UIFormField struct {
	Key    string
	Label  string
	Help   string
	Kind   string
	Value  string
	Values []string // select 的可选值
}

// UIAction 一个可点的动作（ID 与标签都由插件给）。
type UIAction struct {
	ID    string
	Label string
}

// UIViewAction 用户的一次操作：点了按钮（ActionID）或提交了字段（Values），可能两者同时。
// ActionID 为空表示只是提交字段；Values 里没有的字段**不是被清空**，是没提交。
type UIViewAction struct {
	Plugin   string
	ViewID   string
	ActionID string
	Values   map[string]string
}

// PluginViews 面板供给面：列出所有在用的面板、取当前帧、交回动作。
//
// 拉模式（plugin-design.md §6）：内核要画就来取一次，插件不推送。Render 的实现要便宜——
// 渲染器可能每帧问一次，但**只有动作后与打开时**才会真的调到插件，见 uigui 的帧状态。
type PluginViews interface {
	// Views 当前可打开的面板（只含已启用的插件）。顺序稳定：插件登记序 + 视图声明序。
	Views() []UIViewInfo
	// Render 取当前帧。插件停用、视图不存在都返回 error（调用方原样展示）。
	Render(ctx context.Context, plugin, viewID string) (UINode, error)
	// Act 交回一次操作，返回操作后的新帧。
	Act(ctx context.Context, act UIViewAction) (UINode, error)
}
