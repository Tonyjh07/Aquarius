package uigui

// settings_page_plugins.go 「插件」页（D347）：清单、开关与插件自己声明的参数。
//
// 这一页有两层，语义完全不同：
//
//   - **清单**由宿主给（PluginAdmin）：有哪些插件、开没开、状态如何。开关放哪不能由插件
//     决定——那样每个插件一张脸，用户找不到「关掉它」。
//   - **参数**由插件声明（PluginSettings / SettingsSectionProvider）：键、区间、留空语义、
//     保存口径全归插件。内核只把每一行渲染成控件、把值原样回传、把插件的报错显示出来。
//
// 参数分两种摆法：缺省内联在本页，与清单同屏；插件只要有**一段**声明为独立页，整个插件
// 的参数都搬到那一页（本页只留一个「设置」入口），点进去这一页只显示那一个插件。理由：
// 参数多、说明长的插件不该把清单挤到屏外，也不该在左栏给每个插件加一个导航项——左栏是
// 内核的页，不是插件的；而同一插件的参数被劈成两半，用户永远记不住哪个在哪儿。
//
// 因此没有全局「保存并应用」：底栏保存是全量内核 patch，与插件参数没有对应关系。
// 保存按**分区**走（每个分区底部一个按钮），只提交这一段的键——插件可以只认自己认识的
// 键，宿主也不必知道哪些键属于谁。
//
// 快照在开窗、启停后、保存成功后各取一次。控件按平铺行下标重建：插件改了行表（增删参数），
// 下次保存后自然对齐；旧控件被丢掉，不会拿上一版的行去猜新值。

import (
	"context"
	"strconv"
	"sync/atomic"

	"gioui.org/layout"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"

	"github.com/Tonyjh07/Aquarius/internal/i18n"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

// pluginsPage 插件页模块。
type pluginsPage struct{}

func (pluginsPage) Key() string { return "plugins" }

// plugRowRef 平铺行 → (分区下标, 分区内行下标)。
type plugRowRef struct {
	sec, row int
}

// plugItemCtl 清单里一个插件的两个按钮（开关 / 进入独立页）。
type plugItemCtl struct {
	toggle, open widget.Clickable
}

// pluginSettingsState 插件页的全部界面状态（本窗 goroutine 独占）。
//
// 控件与平铺行一一对应（同一下标），重建时整体替换：插件换了一套参数，旧的编辑器内容
// 已经无意义，留着反而会把上一版的值提交回去。
type pluginSettingsState struct {
	admin PluginAdmin      // nil = 没有清单入口（只渲染参数）
	wired bool             // PluginSettings 是否接线
	list  []PluginState    // 清单快照（每次 reload 现取）
	items []plugItemCtl    // 清单行按钮（与 list 同长）
	back  widget.Clickable // 独立页的「返回清单」
	focus string           // 非空 = 独立页模式，只看这个插件的分区

	snap    []port.PluginSettingsSection
	rows    []plugRowRef
	editors []widget.Editor
	checks  []widget.Bool
	selects []*selectField
	saves   []widget.Clickable

	status string
	err    bool
	// dirty 启停动作在旁路 goroutine 里完成；快照重建必须回到本窗 goroutine 做，
	// 所以这里只挂一个待重取的标记（atomic：写方在旁路，读方在帧内）。
	dirty atomic.Bool
}

// plugSectionsOf 本页要显示的分区下标。plugin 为空 = 清单模式（只有内联插件），
// 非空 = 独立页模式（这个插件的全部分区）。
//
// 一个插件只要**有一段**声明为独立页，它的其余分区也一起搬过去：同一插件的参数被劈成
// 一半内联、一半在别的页最糟——用户永远记不住哪个在哪儿（D350）。放置位置因此实际是
// 插件级属性，per-section 声明只是「这个插件需要一页」的表达方式。
func (f *settingsForm) plugSectionsOf(plugin string) []int {
	if plugin != "" {
		var out []int
		for si, sec := range f.plug.snap {
			if sec.Plugin == plugin {
				out = append(out, si)
			}
		}
		return out
	}
	paged := f.plugPagedPlugins()
	var out []int
	for si, sec := range f.plug.snap {
		if paged[sec.Plugin] {
			continue
		}
		out = append(out, si)
	}
	return out
}

// plugPagedPlugins 有独立页的插件集合（只要有一段声明为 page 就算）。
func (f *settingsForm) plugPagedPlugins() map[string]bool {
	var m map[string]bool
	for _, sec := range f.plug.snap {
		if sec.Placement == port.PluginPlacementPage {
			if m == nil {
				m = make(map[string]bool, 1)
			}
			m[sec.Plugin] = true
		}
	}
	return m
}

// plugHasPageSections 这个插件是否声明了独立页分区（决定清单行要不要给「设置」入口）。
func (f *settingsForm) plugHasPageSections(plugin string) bool {
	for _, sec := range f.plug.snap {
		if sec.Plugin == plugin && sec.Placement == port.PluginPlacementPage {
			return true
		}
	}
	return false
}

// reloadPluginSettings 现取快照并重建控件（开窗、启停、保存成功后各一次）。
func (f *settingsForm) reloadPluginSettings() {
	st := &f.plug
	st.snap, st.rows = nil, nil
	st.editors, st.checks, st.selects, st.saves = nil, nil, nil, nil
	if !st.wired {
		return
	}
	if st.admin != nil {
		st.list = st.admin.Snapshot()
		st.items = make([]plugItemCtl, len(st.list))
	}
	st.snap = f.plugins.Sections()
	// 焦点插件没了（被停用或改了声明）：退回清单，不然会停在一张空白的独立页上。
	if st.focus != "" && len(f.plugSectionsOf(st.focus)) == 0 {
		st.focus = ""
	}
	for si, sec := range st.snap {
		for ri, r := range sec.Rows {
			st.rows = append(st.rows, plugRowRef{sec: si, row: ri})
			st.editors = append(st.editors, newPlugEditor(r))
			st.checks = append(st.checks, widget.Bool{Value: r.Kind == port.PluginFieldSwitch && r.Value == "true"})
			sel := &selectField{displays: r.Values, values: r.Values}
			sel.setValue(r.Value)
			st.selects = append(st.selects, sel)
		}
		st.saves = append(st.saves, widget.Clickable{})
	}
}

// newPlugEditor 按控件种类造编辑器：单行档 Enter 不提交，多行档留给正文类参数。
func newPlugEditor(r port.PluginSettingRow) widget.Editor {
	var ed widget.Editor
	switch r.Kind {
	case port.PluginFieldText, port.PluginFieldNumber:
		ed.SingleLine = true
	}
	ed.SetText(r.Value)
	return ed
}

func (pluginsPage) Rows(f *settingsForm) []rowDesc {
	st := &f.plug
	if !st.wired && st.admin == nil {
		return []rowDesc{{kind: rowPlugNote}, {kind: rowPlugEmpty}}
	}
	out := []rowDesc{{kind: rowPlugNote}}
	// 独立页模式下清单让位：这一页的意义就是给这一组参数腾视野，列在头上只会把参数推下去。
	// 返回条是唯一的出口，措辞也才对得上「只看」。
	if st.admin != nil && st.focus == "" {
		out = append(out, rowDesc{kind: rowPlugListHead})
		if len(st.list) == 0 {
			out = append(out, rowDesc{kind: rowPlugListEmpty})
		}
		for i := range st.list {
			out = append(out, rowDesc{kind: rowPlugItem, field: i})
		}
	}
	if st.focus != "" {
		out = append(out, rowDesc{kind: rowPlugBack, text: st.focus})
	}
	for _, si := range f.plugSectionsOf(st.focus) {
		sec := st.snap[si]
		out = append(out, rowDesc{kind: rowPlugGroup, text: sec.Title, prov: si})
		if sec.Desc != "" {
			out = append(out, rowDesc{kind: rowPlugDesc, text: sec.Desc})
		}
		for i, ref := range st.rows {
			if ref.sec != si {
				continue
			}
			out = append(out, rowDesc{kind: rowPlugRow, field: i})
		}
		out = append(out, rowDesc{kind: rowPlugSave, prov: si})
	}
	// 空态只在「真的一无所有」时给：有清单就说明插件在，只是没声明参数。
	if st.focus == "" && st.wired && st.admin == nil && len(st.snap) == 0 {
		out = append(out, rowDesc{kind: rowPlugEmpty})
	}
	out = append(out, rowDesc{kind: rowPlugStatus})
	return out
}

func (pluginsPage) Body(gtx layout.Context, th *material.Theme, f *settingsForm, rows []rowDesc, i int) (layout.Dimensions, bool) {
	st := &f.plug
	r := rows[i]
	switch r.kind {
	case rowPlugNote:
		text := i18n.T("ui.page.plugins.note")
		if !st.wired && st.admin == nil {
			text = i18n.T("ui.page.plugins.unwired")
		}
		return pluginNote(gtx, th, text), true
	case rowPlugEmpty, rowPlugListEmpty:
		return pluginNote(gtx, th, i18n.T("ui.page.plugins.empty")), true
	case rowPlugListHead:
		return layout.Inset{Top: unit.Dp(rowSectionTopDp)}.Layout(gtx,
			paperSection(th, i18n.T("ui.page.plugins.list_head"))), true
	case rowPlugItem:
		if r.field < 0 || r.field >= len(st.list) {
			return layout.Dimensions{}, true
		}
		return f.pluginItemRow(gtx, th, r.field), true
	case rowPlugBack:
		return f.pluginBackRow(gtx, th, r.text), true
	case rowPlugGroup:
		if r.prov < 0 || r.prov >= len(st.snap) {
			return layout.Dimensions{}, true
		}
		sec := st.snap[r.prov]
		head := paperSection(th, sec.Plugin+" · "+r.text)
		return layout.Inset{Top: unit.Dp(rowSectionTopDp), Bottom: unit.Dp(rowSectionGapDp)}.Layout(gtx, head), true
	case rowPlugDesc:
		return pluginNote(gtx, th, r.text), true
	case rowPlugRow:
		if r.field < 0 || r.field >= len(st.rows) {
			return layout.Dimensions{}, true
		}
		return f.pluginRow(gtx, th, r.field), true
	case rowPlugSave:
		if r.prov < 0 || r.prov >= len(st.saves) {
			return layout.Dimensions{}, true
		}
		return f.pluginSaveRow(gtx, th, r.prov), true
	case rowPlugStatus:
		a := material.Body2(th, st.status)
		a.Color = paperOf().text3
		if st.err {
			a.Color = paperOf().danger
		}
		return layout.Inset{Top: unit.Dp(10)}.Layout(gtx, a.Layout), true
	}
	return layout.Dimensions{}, false
}

// pluginItemRow 清单里的一行：名字、状态、供给面摘要，右侧开关与（有独立页时）设置入口。
func (f *settingsForm) pluginItemRow(gtx layout.Context, th *material.Theme, i int) layout.Dimensions {
	st := &f.plug
	p := st.list[i]
	btn := &st.items[i]
	toggle := i18n.T("ui.common.disable")
	if !p.Enabled {
		toggle = i18n.T("ui.common.enable")
	}
	meta := pluginStateLabel(p.State)
	if len(p.Provides) > 0 {
		meta += " · " + joinComma(p.Provides)
	}
	pp := paperOf()
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
				layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
					return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							return material.Body1(th, p.Name).Layout(gtx)
						}),
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							l := material.Body2(th, meta)
							l.Color = pp.text3
							return l.Layout(gtx)
						}),
					)
				}),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return layout.Inset{Left: unit.Dp(6)}.Layout(gtx,
						paperButton(th, &btn.toggle, toggle, paperBtnSecondary, true))
				}),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					if !f.plugHasPageSections(p.Name) {
						return layout.Dimensions{}
					}
					return layout.Inset{Left: unit.Dp(6)}.Layout(gtx,
						paperButton(th, &btn.open, i18n.T("ui.page.plugins.open"), paperBtnSecondary, true))
				}),
			)
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			if p.Err == "" {
				return layout.Dimensions{}
			}
			l := material.Body2(th, p.Err)
			l.Color = pp.danger
			return l.Layout(gtx)
		}),
	)
}

// pluginBackRow 独立页顶部的返回条：一眼看出现在只属于哪个插件，以及怎么回去。
func (f *settingsForm) pluginBackRow(gtx layout.Context, th *material.Theme, plugin string) layout.Dimensions {
	return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return material.Body2(th, i18n.Tf("ui.page.plugins.focus", "name", plugin)).Layout(gtx)
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Left: unit.Dp(10)}.Layout(gtx,
				paperButton(th, &f.plug.back, i18n.T("ui.page.plugins.back"), paperBtnSecondary, true))
		}),
	)
}

// pluginStateLabel 注册表状态串 → 展示名（未知原样露出）。
func pluginStateLabel(state string) string {
	switch state {
	case "active":
		return i18n.T("ui.page.plugins.state.active")
	case "stopped":
		return i18n.T("ui.page.plugins.state.stopped")
	case "draining":
		return i18n.T("ui.page.plugins.state.draining")
	case "registered", "validated":
		return i18n.T("ui.page.plugins.state.pending")
	}
	return state
}

// pluginRow 一行参数：控件按插件声明的种类选，说明灰字挂在控件下方。
//
// 说明**不塞进输入框的 hint**：hint 在值非空时不显示，而参数一旦有值，正是用户回想
// 「这个数什么意思」的时候。开关自带标签，不再套 fieldRow（否则标签出现两遍）。
func (f *settingsForm) pluginRow(gtx layout.Context, th *material.Theme, i int) layout.Dimensions {
	st := &f.plug
	ref := st.rows[i]
	row := st.snap[ref.sec].Rows[ref.row]
	control := func(gtx layout.Context) layout.Dimensions { return layout.Dimensions{} }
	switch row.Kind {
	case port.PluginFieldSwitch:
		return withPlugHelp(gtx, th, row.Help, paperCheckBox(th, &st.checks[i], row.Label))
	case port.PluginFieldSelect:
		control = selectBtn(th, st.selects[i])
	case port.PluginFieldTextarea:
		control = editorBoxH(th, &st.editors[i], "", 96)
	case port.PluginFieldNumber:
		control = editorBox(th, &st.editors[i], "")
	default:
		control = editorBox(th, &st.editors[i], "")
	}
	return fieldRow(gtx, th, row.Label, func(gtx layout.Context) layout.Dimensions {
		return withPlugHelp(gtx, th, row.Help, control)
	})
}

// withPlugHelp 控件 + 下方灰字说明。说明为空时原样返回控件（不多占一行）。
func withPlugHelp(gtx layout.Context, th *material.Theme, help string, control layout.Widget) layout.Dimensions {
	if help == "" {
		return control(gtx)
	}
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Rigid(control),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Top: unit.Dp(4)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				l := material.Body2(th, help)
				l.Color = paperOf().text3
				return l.Layout(gtx)
			})
		}),
	)
}

// pluginSaveRow 分区保存按钮。
func (f *settingsForm) pluginSaveRow(gtx layout.Context, th *material.Theme, sec int) layout.Dimensions {
	btn := paperButton(th, &f.plug.saves[sec], i18n.T("ui.page.plugins.save"), paperBtnSecondary, true)
	return layout.Inset{Top: unit.Dp(6)}.Layout(gtx, btn)
}

// pluginNote 灰字说明行。
func pluginNote(gtx layout.Context, th *material.Theme, text string) layout.Dimensions {
	l := material.Body2(th, text)
	l.Color = paperOf().text3
	return layout.Inset{Top: unit.Dp(6), Bottom: unit.Dp(4)}.Layout(gtx, l.Layout)
}

// Events 页事件：清单开关、独立页进出、分区保存与下拉弹层。
func (pluginsPage) Events(gtx layout.Context, u *UI, f *settingsForm) {
	st := &f.plug
	// 启停完成后重取一次清单与分区表：停用的插件不该再贡献参数，启用的应立刻出现。
	if st.dirty.Swap(false) {
		f.reloadPluginSettings()
	}
	for i := range st.items {
		if i >= len(st.list) {
			break
		}
		btn := &st.items[i]
		for btn.toggle.Clicked(gtx) {
			f.togglePlugin(u, st.list[i])
		}
		for btn.open.Clicked(gtx) {
			st.focus = st.list[i].Name
			f.plug.status = ""
			if f.win != nil {
				f.win.Invalidate()
			}
		}
	}
	for st.back.Clicked(gtx) {
		st.focus = ""
		if f.win != nil {
			f.win.Invalidate()
		}
	}
	if !st.wired {
		return
	}
	for si := range st.saves {
		for st.saves[si].Clicked(gtx) {
			f.savePluginSection(u, si)
		}
	}
	for i, sel := range st.selects {
		if i >= len(st.rows) {
			break
		}
		label := st.snap[st.rows[i].sec].Rows[st.rows[i].row].Label
		f.openPicker(gtx, label, sel)
	}
}

// FirstEditor 插件页没有固定的首档：行表随插件变，开窗把焦点留给页面本身。
func (pluginsPage) FirstEditor(*settingsForm) *widget.Editor { return nil }

// togglePlugin 启停一个插件。动作异步（启用可能弹确认），完成后重新取清单与分区表——
// 停用后的插件不该再贡献参数，启用后的应该立刻出现。
func (f *settingsForm) togglePlugin(u *UI, p PluginState) {
	if f.plug.admin == nil {
		return
	}
	target := !p.Enabled
	u.toolsAsync(f, func() (string, error) {
		if err := f.plug.admin.SetEnabled(p.Name, target); err != nil {
			return "", err
		}
		f.plug.dirty.Store(true) // 重建留给本窗 goroutine（见 Events）
		return i18n.Tf("ui.page.plugins.toggled", "name", p.Name), nil
	})
}

// savePluginSection 收集本分区各行的当前值并交给插件。
//
// 只收集**本分区**的键：插件按 SectionID 认段，多给的键会让它以为用户动了别处。
func (f *settingsForm) savePluginSection(u *UI, si int) {
	st := &f.plug
	if u.opts.PluginSettings == nil || si < 0 || si >= len(st.snap) {
		return
	}
	sec := st.snap[si]
	vals := make(map[string]string, len(sec.Rows))
	for i, ref := range st.rows {
		if ref.sec != si {
			continue
		}
		row := sec.Rows[ref.row]
		switch row.Kind {
		case port.PluginFieldSwitch:
			vals[row.Key] = strconv.FormatBool(st.checks[i].Value)
		case port.PluginFieldSelect:
			vals[row.Key] = st.selects[i].value()
		default:
			vals[row.Key] = st.editors[i].Text()
		}
	}
	if err := u.opts.PluginSettings.Apply(context.Background(), sec.Plugin, sec.ID, vals); err != nil {
		st.status, st.err = err.Error(), true
	} else {
		st.status, st.err = i18n.T("ui.page.plugins.saved"), false
	}
	// 保存成功后以插件侧为准重取：插件可能对值做了归一（截断、夹取），界面必须显示
	// 真正生效的那个，而不是用户刚敲进去的那个。
	f.reloadPluginSettings()
	if f.win != nil {
		f.win.Invalidate()
	}
}

// joinComma 列表用逗号连起来（供给面摘要这类短清单，不值得进文案表）。
func joinComma(xs []string) string {
	out := ""
	for i, x := range xs {
		if i > 0 {
			out += ", "
		}
		out += x
	}
	return out
}
