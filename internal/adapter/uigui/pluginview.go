package uigui

// pluginview.go 插件面板次窗（D346，plugin-design.md §6）。
//
// 拉模式落地：插件不推送界面，本窗只在**三处**去问插件要当前帧——打开时、每次动作之后、
// 目标换了之后。其余帧都画缓存里的那一棵，所以拖动窗口、滚动、输入表单文字都不会碰到插件。
//
// 表单值随按钮一起交回：契约允许一次 ViewAction 同时带 ActionID 与 Values（点了按钮，
// 同时提交可见字段），所以按钮点击时把整棵树里所有 form 字段的当前值一并收集。反过来，
// 只提交字段、不点按钮的路径 v1 不开——插件要多一个「提交」按钮，就用 Actions 里的一条，
// 语义清楚得多。

import (
	"context"
	"errors"
	"image"

	"gioui.org/layout"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"

	"github.com/Tonyjh07/Aquarius/internal/i18n"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

// viewTarget 本窗当前要显示的东西（原子槽内容：分发处 Store → 帧内 Load）。
type viewTarget struct {
	plugin string
	viewID string
	title  string // 视图标题（插件文案）；窗标题固定，标题行显示它
}

// viewFieldState 一个表单字段的控件状态（按字段 Key 索引，切视图时整体丢弃）。
type viewFieldState struct {
	ed widget.Editor
	sw widget.Bool
	// applied 控件上已经呈现的值：插件给的新值与它不同才重置控件，否则会把用户正在
	// 输入的文字按帧刷回去。
	applied string
	// sel 下拉的当前下标（切换式，没有浮层）；cycle 是切换它的按钮。
	sel   int
	cycle widget.Clickable
}

// pluginViewState 面板次窗的自持帧状态（runSecondary 创建、仅该窗 goroutine 读写）。
type pluginViewState struct {
	u      *UI // 动作要回源（Act），构造时绑
	target *viewTarget
	node   port.UINode
	err    error

	list   widget.List
	fields map[string]*viewFieldState
	clicks map[string]*widget.Clickable
	inval  func()
}

// newPluginViewState 构造面板窗帧状态；inval 用来在动作完成后请求重绘。
func newPluginViewState(u *UI, inval func()) *pluginViewState {
	return &pluginViewState{
		u:      u,
		list:   widget.List{List: layout.List{Axis: layout.Vertical}},
		fields: make(map[string]*viewFieldState, 8),
		clicks: make(map[string]*widget.Clickable, 8),
		inval:  inval,
	}
}

// showPluginView 打开（或原地换成）一个插件面板。窗单实例：再开另一个就是换内容，
// 与查看原文窗同一套语义。
func (u *UI) showPluginView(ev port.OpenViewEvent) {
	title := ev.ViewID
	if u.opts.Views != nil {
		for _, v := range u.opts.Views.Views() {
			if v.Plugin == ev.Plugin && v.ID == ev.ViewID {
				title = v.Title
				break
			}
		}
	}
	u.viewTarget.Store(&viewTarget{plugin: ev.Plugin, viewID: ev.ViewID, title: title})
	u.wins.openWin(winView)
	u.wins.invalidateKind(winView)
}

// closePluginView 关掉某个插件的面板窗。只有窗里看的正是它才关：用户在同时看别的插件的
// 面板时停用了 A，不该顺手把 B 的窗也关掉。
func (u *UI) closePluginView(plugin string) {
	t := u.viewTarget.Load()
	if t == nil || t.plugin != plugin {
		return
	}
	u.viewTarget.Store(nil)
	u.wins.closeWin(winView)
}

// sync 目标变化时取一次帧（帧内只经原子快照，跨线程不共享可变态）。
func (st *pluginViewState) sync(u *UI) {
	t := u.viewTarget.Load()
	if t == st.target {
		return
	}
	st.target = t
	// 换了视图就把字段与按钮状态全丢掉：Key 相同但语义不同是插件自己的自由，跨视图
	// 复用控件只会把上一条记忆的输入带进下一条。
	st.fields = make(map[string]*viewFieldState, 8)
	st.clicks = make(map[string]*widget.Clickable, 8)
	st.list = widget.List{List: layout.List{Axis: layout.Vertical}}
	st.node, st.err = port.UINode{}, nil
	if t == nil {
		st.err = errors.New(i18n.T("ui.win.ph.view"))
		return
	}
	if u.opts.Views == nil {
		st.err = errors.New(i18n.T("ui.win.ph.view"))
		return
	}
	n, err := u.opts.Views.Render(context.Background(), t.plugin, t.viewID)
	st.node, st.err = n, err
}

// act 交回一次操作并换上新帧。同步调用：契约要求 Render/Act 便宜（读插件自己的内存），
// 异步只会把「点了没反应」变成「点了过一会儿才有反应」。
func (st *pluginViewState) act(u *UI, actionID string) {
	t := st.target
	if t == nil || u.opts.Views == nil {
		return
	}
	n, err := u.opts.Views.Act(context.Background(), port.UIViewAction{
		Plugin:   t.plugin,
		ViewID:   t.viewID,
		ActionID: actionID,
		Values:   st.values(),
	})
	if err != nil {
		st.err = err
		return
	}
	st.node, st.err = n, nil
	if st.inval != nil {
		st.inval()
	}
}

// values 当前可见表单字段的值（插件没声明的字段不在表里，见 port.UIViewAction 注释）。
func (st *pluginViewState) values() map[string]string {
	var out map[string]string
	collectValues(st.node, st.fields, &out)
	return out
}

func collectValues(n port.UINode, fields map[string]*viewFieldState, out *map[string]string) {
	if n.Kind == port.UIForm {
		for _, f := range n.Fields {
			fs := fields[f.Key]
			if fs == nil {
				continue
			}
			if *out == nil {
				*out = make(map[string]string, len(n.Fields))
			}
			switch f.Kind {
			case "switch":
				if fs.sw.Value {
					(*out)[f.Key] = "true"
				} else {
					(*out)[f.Key] = "false"
				}
			case "select":
				if fs.sel >= 0 && fs.sel < len(f.Values) {
					(*out)[f.Key] = f.Values[fs.sel]
				}
			default:
				(*out)[f.Key] = fs.ed.Text()
			}
		}
	}
	for _, c := range n.Children {
		collectValues(c, fields, out)
	}
}

// field 取（或建）一个字段的控件状态，并把插件当前值同步进去。
func (st *pluginViewState) field(f port.UIFormField) *viewFieldState {
	fs := st.fields[f.Key]
	if fs == nil {
		fs = &viewFieldState{}
		fs.apply(f, true)
		st.fields[f.Key] = fs
		return fs
	}
	fs.apply(f, false)
	return fs
}

// apply 把插件给的值同步进控件。force = 首次创建（无条件写）。
func (fs *viewFieldState) apply(f port.UIFormField, force bool) {
	if !force && f.Value == fs.applied {
		return
	}
	fs.applied = f.Value
	switch f.Kind {
	case "switch":
		fs.sw.Value = f.Value == "true"
	case "select":
		fs.sel = 0
		for i, v := range f.Values {
			if v == f.Value {
				fs.sel = i
				break
			}
		}
	default:
		fs.ed.SetText(f.Value)
	}
}

// click 取（或建）一个按钮的点击件。
func (st *pluginViewState) click(id string) *widget.Clickable {
	c := st.clicks[id]
	if c == nil {
		c = &widget.Clickable{}
		st.clicks[id] = c
	}
	return c
}

// viewFrame 面板窗单帧：窗体面铺满 + 标题行 + 可滚动的组件树。
func viewFrame(gtx layout.Context, th *material.Theme, u *UI, st *pluginViewState) layout.Dimensions {
	st.sync(u)
	pp := paperOf()
	return layout.Stack{}.Layout(gtx,
		layout.Expanded(func(gtx layout.Context) layout.Dimensions {
			paint.FillShape(gtx.Ops, pp.win,
				clip.Rect(image.Rectangle{Max: gtx.Constraints.Max}).Op())
			return layout.Dimensions{}
		}),
		layout.Stacked(func(gtx layout.Context) layout.Dimensions {
			return layout.UniformInset(unit.Dp(16)).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return st.header(gtx, th)
					}),
					layout.Rigid(layout.Spacer{Height: unit.Dp(10)}.Layout),
					layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
						return st.body(gtx, th)
					}),
				)
			})
		}),
	)
}

// header 标题行：视图标题 + 所属插件（面板可能来自哪个插件，用户得看得见）。
func (st *pluginViewState) header(gtx layout.Context, th *material.Theme) layout.Dimensions {
	title := i18n.T("ui.win.title.view")
	sub := ""
	if t := st.target; t != nil {
		title = t.title
		sub = t.plugin
	}
	return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			l := material.Label(th, paperBodySp(th), title)
			l.Color = paperOf().text2
			l.MaxLines, l.Truncator = 1, "…"
			return l.Layout(gtx)
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			if sub == "" {
				return layout.Dimensions{}
			}
			l := material.Label(th, unit.Sp(12), sub)
			l.Color = paperOf().text2
			return layout.Inset{Left: unit.Dp(8)}.Layout(gtx, l.Layout)
		}),
	)
}

// body 内容区：错误 / 空态 / 组件树。
func (st *pluginViewState) body(gtx layout.Context, th *material.Theme) layout.Dimensions {
	if st.err != nil {
		l := material.Body2(th, i18n.Tf("ui.view.failed", "err", st.err.Error()))
		l.Color = paperOf().danger
		return l.Layout(gtx)
	}
	return st.list.Layout(gtx, 1, func(gtx layout.Context, _ int) layout.Dimensions {
		return layoutNode(gtx, th, st.node, st)
	})
}

// layoutNode 渲染一个节点及其子节点：先画该种类自身，再竖排子节点（契约：Children 与任何
// Kind 都可共存，Kind 为零值是纯容器）。
func layoutNode(gtx layout.Context, th *material.Theme, n port.UINode, st *pluginViewState) layout.Dimensions {
	if len(n.Children) == 0 {
		return nodeSelf(gtx, th, n, st)
	}
	kids := make([]layout.FlexChild, 0, len(n.Children)+1)
	kids = append(kids, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
		return nodeSelf(gtx, th, n, st)
	}))
	for _, c := range n.Children {
		child := c
		kids = append(kids, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layoutNode(gtx, th, child, st)
		}))
	}
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx, kids...)
}

// nodeSelf 画一个节点自身（不含子节点）。
func nodeSelf(gtx layout.Context, th *material.Theme, n port.UINode, st *pluginViewState) layout.Dimensions {
	pp := paperOf()
	switch n.Kind {
	case port.UIText:
		if n.Text == "" {
			return layout.Dimensions{}
		}
		l := material.Body1(th, n.Text)
		l.Color = pp.text
		return l.Layout(gtx)
	case port.UIMarkdown:
		// 走主窗同一条 markdown 渲染管线（§6「markdown 走内核管线」）。没有现成的
		// 独立渲染器可复用（那份绑在主窗 model 上），所以这里退成纯文本：宁可少样式，
		// 不要为了复用把主窗 model 拖进次窗。
		l := material.Body1(th, n.Text)
		l.Color = pp.text
		return l.Layout(gtx)
	case port.UITable:
		return tableNode(gtx, th, n)
	case port.UIKV:
		return kvNode(gtx, th, n)
	case port.UIList:
		return listNode(gtx, th, n)
	case port.UIForm:
		return formNode(gtx, th, n, st)
	case port.UIButtons:
		return buttonsNode(gtx, th, n, st)
	}
	// 零值 Kind（容器）什么都不画；未知种类则把文本显示出来（旧前端遇上新词表不至于
	// 一片空白）。子节点由 layoutNode 统一处理，这里不再重复。
	if n.Text == "" {
		return layout.Dimensions{}
	}
	l := material.Body1(th, n.Text)
	l.Color = pp.text
	return l.Layout(gtx)
}

// tableLayout 表格：每列等分（不按字符宽对齐——这里是比例字体，字符宽对齐会歪）。
func tableNode(gtx layout.Context, th *material.Theme, n port.UINode) layout.Dimensions {
	pp := paperOf()
	cols := len(n.Columns)
	for _, r := range n.Rows {
		if len(r) > cols {
			cols = len(r)
		}
	}
	if cols == 0 {
		return layout.Dimensions{}
	}
	row := func(cells []string, header bool) layout.Widget {
		return func(gtx layout.Context) layout.Dimensions {
			kids := make([]layout.FlexChild, 0, cols)
			for i := 0; i < cols; i++ {
				cell := ""
				if i < len(cells) {
					cell = cells[i]
				}
				kids = append(kids, layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
					style := material.Body2(th, cell)
					style.Color = pp.text
					if header {
						style.Color = pp.text2
					}
					style.MaxLines, style.Truncator = 1, "…"
					return layout.Inset{Top: unit.Dp(2), Bottom: unit.Dp(2)}.Layout(gtx, style.Layout)
				}))
			}
			return layout.Flex{Axis: layout.Horizontal}.Layout(gtx, kids...)
		}
	}
	var kids []layout.FlexChild
	if len(n.Columns) > 0 {
		kids = append(kids, layout.Rigid(row(n.Columns, true)))
	}
	for _, r := range n.Rows {
		cells := r
		kids = append(kids, layout.Rigid(row(cells, false)))
	}
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx, kids...)
}

// kvLayout 名值对：Items 成对，奇数个时最后一个只显示名字。
func kvNode(gtx layout.Context, th *material.Theme, n port.UINode) layout.Dimensions {
	pp := paperOf()
	var kids []layout.FlexChild
	for i := 0; i < len(n.Items); i += 2 {
		name := n.Items[i]
		val := ""
		if i+1 < len(n.Items) {
			val = n.Items[i+1]
		}
		kids = append(kids, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Baseline}.Layout(gtx,
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					l := material.Body2(th, name)
					l.Color = pp.text2
					return l.Layout(gtx)
				}),
				layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
					l := material.Body1(th, val)
					l.Color = pp.text
					return layout.Inset{Left: unit.Dp(10)}.Layout(gtx, l.Layout)
				}),
			)
		}))
	}
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx, kids...)
}

// listLayout 逐行。
func listNode(gtx layout.Context, th *material.Theme, n port.UINode) layout.Dimensions {
	pp := paperOf()
	kids := make([]layout.FlexChild, 0, len(n.Items))
	for _, it := range n.Items {
		item := it
		kids = append(kids, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			l := material.Body1(th, "· "+item)
			l.Color = pp.text
			return layout.Inset{Top: unit.Dp(1), Bottom: unit.Dp(1)}.Layout(gtx, l.Layout)
		}))
	}
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx, kids...)
}

// formLayout 表单：每个字段一行，控件绑在窗状态上（用户输入只在下面这些控件里）。
func formNode(gtx layout.Context, th *material.Theme, n port.UINode, st *pluginViewState) layout.Dimensions {
	pp := paperOf()
	var kids []layout.FlexChild
	for _, f := range n.Fields {
		field := f
		fs := st.field(field)
		kids = append(kids, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					l := material.Body2(th, field.Label)
					l.Color = pp.text2
					return layout.Inset{Bottom: unit.Dp(3)}.Layout(gtx, l.Layout)
				}),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return fieldCtl(gtx, th, field, fs)
				}),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					if field.Help == "" {
						return layout.Dimensions{}
					}
					l := material.Body2(th, field.Help)
					l.Color = pp.text2
					return layout.Inset{Top: unit.Dp(3)}.Layout(gtx, l.Layout)
				}),
			)
		}))
	}
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx, kids...)
}

// fieldCtl 一个字段的控件。select 用「点一下切下一个」而不是下拉浮层：浮层要自己管
// 定位、点外关闭、键盘导览，为面板的一个字段引入这些不值；值少时循环点击反而更快。
func fieldCtl(gtx layout.Context, th *material.Theme, f port.UIFormField, fs *viewFieldState) layout.Dimensions {
	switch f.Kind {
	case "switch":
		return material.Switch(th, &fs.sw, "").Layout(gtx)
	case "select":
		cur := ""
		if fs.sel >= 0 && fs.sel < len(f.Values) {
			cur = f.Values[fs.sel]
		}
		if fs.cycle.Clicked(gtx) && len(f.Values) > 0 {
			fs.sel = (fs.sel + 1) % len(f.Values)
			return layout.Dimensions{}
		}
		return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return material.Button(th, &fs.cycle, cur).Layout(gtx)
			}),
		)
	case "textarea":
		return editorBoxH(th, &fs.ed, f.Label, 120)(gtx)
	default:
		return editorBox(th, &fs.ed, f.Label)(gtx)
	}
}

// buttonsLayout 按钮行：点了就把当前表单值一起交回去。
func buttonsNode(gtx layout.Context, th *material.Theme, n port.UINode, st *pluginViewState) layout.Dimensions {
	kids := make([]layout.FlexChild, 0, len(n.Actions))
	for _, a := range n.Actions {
		act := a
		c := st.click("act:" + act.ID)
		kids = append(kids, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			btn := material.Button(th, c, act.Label)
			dims := layout.Inset{Right: unit.Dp(6), Top: unit.Dp(4)}.Layout(gtx, btn.Layout)
			if c.Clicked(gtx) {
				st.act(st.u, act.ID)
			}
			return dims
		}))
	}
	return layout.Flex{Axis: layout.Horizontal}.Layout(gtx, kids...)
}
