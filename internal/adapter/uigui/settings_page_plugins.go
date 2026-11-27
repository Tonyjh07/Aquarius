package uigui

// settings_page_plugins.go 「插件」页（D347）：插件自己声明的参数。
//
// 这一页与其余页有一个本质区别——**字段不由内核定义**。行表来自当前启用的插件
//（SettingsSectionProvider），键的含义、取值范围、留空语义都归插件所有；内核只把每一行
// 渲染成控件、把值原样回传、把插件的报错显示出来。
//
// 因此没有全局「保存并应用」：底栏的保存是全量内核 patch，与插件参数没有对应关系。
// 保存按**分区**走（每个分区底部一个按钮），只提交这一段的键——插件可以只认自己认识的
// 键，宿主也不必知道哪些键属于谁。
//
// 快照在开窗、保存成功后各取一次。控件按平铺行下标重建：插件改了行表（增删参数），
// 下次保存后自然对齐；旧控件被丢掉，不会拿上一版的行去猜新值。

import (
	"context"
	"strconv"

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

// pluginSettingsState 插件页的全部界面状态（本窗 goroutine 独占）。
//
// 控件与平铺行一一对应（同一下标），重建时整体替换：插件换了一套参数，旧的编辑器内容
// 已经无意义，留着反而会把上一版的值提交回去。
type pluginSettingsState struct {
	wired bool // Options.PluginSettings 是否接线

	snap    []port.PluginSettingsSection
	rows    []plugRowRef
	editors []widget.Editor
	checks  []widget.Bool
	selects []*selectField
	saves   []widget.Clickable

	status string
	err    bool
}

// reload 现取快照并重建控件（开窗、保存成功后各一次）。
func (f *settingsForm) reloadPluginSettings() {
	st := &f.plug
	st.snap, st.rows = nil, nil
	st.editors, st.checks, st.selects, st.saves = nil, nil, nil, nil
	if !st.wired {
		return
	}
	st.snap = f.plugins.Sections()
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
	if !st.wired {
		return []rowDesc{{kind: rowPlugNote}}
	}
	if len(st.snap) == 0 {
		return []rowDesc{{kind: rowPlugEmpty}}
	}
	var out []rowDesc
	out = append(out, rowDesc{kind: rowPlugNote})
	for si, sec := range st.snap {
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
	out = append(out, rowDesc{kind: rowPlugStatus})
	return out
}

func (pluginsPage) Body(gtx layout.Context, th *material.Theme, f *settingsForm, rows []rowDesc, i int) (layout.Dimensions, bool) {
	st := &f.plug
	r := rows[i]
	switch r.kind {
	case rowPlugNote:
		text := i18n.T("ui.page.plugins.note")
		if !st.wired {
			text = i18n.T("ui.page.plugins.unwired")
		}
		return pluginNote(gtx, th, text), true
	case rowPlugEmpty:
		return pluginNote(gtx, th, i18n.T("ui.page.plugins.empty")), true
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

// Events 页事件：分区保存与下拉弹层（弹层机制与其余页共用同一套模态）。
func (pluginsPage) Events(gtx layout.Context, u *UI, f *settingsForm) {
	st := &f.plug
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
