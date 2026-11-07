package uigui

// settings_page_delegate.go 「外部代理」页（D270 / v0.6.0 的 A1）。
// 数据面与动作见 settings_delegate.go。
//
// 页面三段：代理清单（每条可展开成字段级编辑）、新增条目、页级「重新检测」。
// 展开是**行内**的：展开区的每一行也是独立 row，不用另开弹窗——这样它能跟普通字段行
// 共用标签列与输入框样式，滚动也归本页的 List 管。

import (
	"strings"

	"gioui.org/layout"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"

	"github.com/Tonyjh07/Aquarius/internal/i18n"
)

// delegatePage 外部代理页模块。
type delegatePage struct{}

func (delegatePage) Key() string { return "delegate" }

func (delegatePage) Rows(f *settingsForm) []rowDesc {
	rows := []rowDesc{}
	if f.delegates == nil {
		return append(rows, rowDesc{kind: rowDlgEmpty, text: i18n.T("ui.common.unwired")})
	}
	rows = append(rows, sec(i18n.T("ui.page.delegate.section_agents")), rowDesc{kind: rowDlgNote})
	if len(f.delegateSnap.Agents) == 0 {
		text := i18n.T("ui.page.delegate.empty")
		if f.delegateSnap.Note != "" {
			text = f.delegateSnap.Note
		}
		rows = append(rows, rowDesc{kind: rowDlgEmpty, text: text})
	}
	for i := range f.delegateSnap.Agents {
		rows = append(rows, rowDesc{kind: rowDlgItem, field: i})
		if f.delegateOpen != f.delegateSnap.Agents[i].Name {
			continue
		}
		rows = append(rows,
			rowDesc{kind: rowDlgCmd, field: i},
			rowDesc{kind: rowDlgArgs, field: i},
			rowDesc{kind: rowDlgEnv, field: i},
			rowDesc{kind: rowDlgWorkdir, field: i},
			rowDesc{kind: rowDlgRoots, field: i},
			rowDesc{kind: rowDlgSave, field: i},
			rowDesc{kind: rowDlgProbe, field: i},
		)
	}
	rows = append(rows,
		sec(i18n.T("ui.page.delegate.section_add")),
		rowDesc{kind: rowDlgAdd},
		rowDesc{kind: rowDlgRefresh},
	)
	if f.delegateStat != "" {
		rows = append(rows, rowDesc{kind: rowDlgStatus})
	}
	return rows
}

func (delegatePage) Body(gtx layout.Context, th *material.Theme, f *settingsForm, rows []rowDesc, i int) (layout.Dimensions, bool) {
	r := rows[i]
	if r.field < 0 || r.field >= len(f.delegateSnap.Agents) {
		if r.kind >= rowDlgCmd && r.kind <= rowDlgProbe {
			return layout.Dimensions{}, true // 展开区在快照变短后仍可能多排一帧
		}
	}
	switch r.kind {
	case rowDlgItem:
		return f.delegateAgentRow(gtx, th, f.delegateSnap.Agents[r.field]), true
	case rowDlgCmd:
		st := f.delegateSnap.Agents[r.field]
		df := f.delegateFormFor(st.Name)
		return f.delegateFieldRow(gtx, th, i18n.T("ui.settings.dlg.exec_label"),
			&df.cmd, i18n.T("ui.settings.dlg.exec_hint"), 0), true
	case rowDlgArgs:
		st := f.delegateSnap.Agents[r.field]
		df := f.delegateFormFor(st.Name)
		return f.delegateFieldRow(gtx, th, i18n.T("ui.settings.dlg.args_label"),
			&df.args, i18n.T("ui.settings.dlg.args_hint"), delegateMultilineH), true
	case rowDlgEnv:
		st := f.delegateSnap.Agents[r.field]
		df := f.delegateFormFor(st.Name)
		return f.delegateFieldRow(gtx, th, i18n.T("ui.settings.dlg.env_label"),
			&df.env, delegateEnvHint(f.delegateSnap.Secrets), delegateMultilineH), true
	case rowDlgWorkdir:
		st := f.delegateSnap.Agents[r.field]
		df := f.delegateFormFor(st.Name)
		return f.delegateFieldRow(gtx, th, i18n.T("ui.settings.dlg.workdir_label"),
			&df.workdir, i18n.T("ui.settings.dlg.workdir_hint"), 0), true
	case rowDlgRoots:
		st := f.delegateSnap.Agents[r.field]
		df := f.delegateFormFor(st.Name)
		return f.delegateFieldRow(gtx, th, i18n.T("ui.settings.dlg.roots_label"),
			&df.roots, i18n.T("ui.settings.dlg.roots_hint"), delegateMultilineH), true
	case rowDlgSave:
		return f.delegateSaveRow(gtx, th, f.delegateSnap.Agents[r.field].Name), true
	case rowDlgProbe:
		return f.delegateProbeRow(gtx, th, f.delegateSnap.Agents[r.field].Name), true
	case rowDlgAdd:
		return f.delegateAddRow(gtx, th), true
	case rowDlgEmpty:
		l := material.Body2(th, r.text)
		l.Color = paperOf().text3
		return l.Layout(gtx), true
	case rowDlgNote:
		l := material.Body2(th, i18n.T("ui.page.delegate.note"))
		l.Color = paperOf().text3
		return l.Layout(gtx), true
	case rowDlgRefresh:
		return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				btn := f.delegateBtns["refresh"]
				if btn == nil {
					btn = new(widget.Clickable)
					f.delegateBtns["refresh"] = btn
				}
				return paperButton(th, btn, i18n.T("ui.page.delegate.refresh"), paperBtnSecondary, true)(gtx)
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Inset{Left: unit.Dp(10)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					l := material.Label(th, paperMicroSp(th), i18n.T("ui.page.delegate.refresh_hint"))
					l.Color = paperOf().text3
					return l.Layout(gtx)
				})
			}),
		), true
	case rowDlgStatus:
		l := material.Body2(th, f.delegateStat)
		if f.delegateErr {
			l.Color = paperOf().danger
		} else {
			l.Color = paperOf().text3
		}
		return l.Layout(gtx), true
	}
	return layout.Dimensions{}, false
}

// delegateMultilineH 多行档的定高（dp）：三行参数/环境/根集够看，再多走编辑器内滚动。
const delegateMultilineH = 84

// delegateAddRow 新增条目：名字 + 命令行 + 「添加」。
//
// 名字与命令行做成两个单行档而不是一行「命令行」：新增时最常错的是重名，把它单独摆出来
// 才不会跟参数混在一行里。
func (f *settingsForm) delegateAddRow(gtx layout.Context, th *material.Theme) layout.Dimensions {
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
				layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
					return fieldRow(gtx, th, i18n.T("ui.settings.dlg.name_label"),
						editorBox(th, &f.delegateAddN, i18n.T("ui.settings.dlg.name_hint")))
				}),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return layout.Inset{Left: unit.Dp(10)}.Layout(gtx,
						paperButton(th, &f.delegateAddB, i18n.T("ui.settings.dlg.add"), paperBtnPrimary, true))
				}),
			)
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Top: unit.Dp(6)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return fieldRow(gtx, th, i18n.T("ui.settings.dlg.cmd_label"),
					editorBox(th, &f.delegateAddC, i18n.T("ui.settings.dlg.cmd_hint")))
			})
		}),
	)
}

// delegateEnvHint 环境变量档的占位：把已存密钥名摆出来，用户不必去密钥页抄。
func delegateEnvHint(secrets []string) string {
	if len(secrets) == 0 {
		return i18n.T("ui.settings.dlg.env_hint")
	}
	return i18n.Tf("ui.settings.dlg.env_hint_keys", "keys", joinLimit(secrets, 6))
}

// joinLimit 前 n 项用「、」连起来，多出来的收成「等 N 个」（提示行不该被一串名字撑爆）。
func joinLimit(items []string, n int) string {
	if len(items) <= n {
		return strings.Join(items, i18n.T("ui.common.list_sep"))
	}
	return strings.Join(items[:n], i18n.T("ui.common.list_sep")) + i18n.Tf("ui.settings.dlg.more_items", "n", len(items)-n)
}

func (delegatePage) Events(gtx layout.Context, u *UI, f *settingsForm) {
	if f.delegates == nil {
		return
	}
	if btn := f.delegateBtns["refresh"]; btn != nil {
		for btn.Clicked(gtx) {
			f.syncDelegateSnapshot()
			// 已展开的那条跟着刷新草稿（其余条目收起时不预填，避免白覆盖未保存的输入）。
			for _, a := range f.delegateSnap.Agents {
				if a.Name == f.delegateOpen {
					f.loadDelegateForm(a)
				}
			}
			f.delegateStat, f.delegateErr = i18n.T("ui.page.delegate.rescanned"), false
		}
	}
	for f.delegateAddB.Clicked(gtx) {
		u.dispatchDelegateAction(f, "add:")
	}
	for key, btn := range f.delegateBtns {
		if key == "refresh" {
			continue
		}
		if btn.Clicked(gtx) {
			u.dispatchDelegateAction(f, key)
		}
	}
}

// FirstEditor 开窗焦点：展开的那条给命令档；都没展开就给新增的名字档（这一页的入口）。
func (delegatePage) FirstEditor(f *settingsForm) *widget.Editor {
	if f.delegateOpen != "" {
		if df := f.delegateForms[f.delegateOpen]; df != nil {
			return &df.cmd
		}
	}
	return &f.delegateAddN
}
