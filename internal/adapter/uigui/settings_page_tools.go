package uigui

// settings_page_tools.go 「工具与 MCP」页（D162 页面注册表；数据面与动作见
// settings_tools.go，S6/D143）：内置工具启停 + 每行放行档位（D280/D282）+ 插件管理 +
// 外部代理整来源的放行档位。行数随快照变化，状态行由动作结果回灌。

import (
	"strings"

	"gioui.org/layout"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"

	"github.com/Tonyjh07/Aquarius/internal/i18n"
)

// toolsPage 工具与 MCP 页模块。
type toolsPage struct{}

func (toolsPage) Key() string { return "tools" }

func (toolsPage) Rows(f *settingsForm) []rowDesc {
	rows := []rowDesc{}
	if f.tools == nil {
		return append(rows, rowDesc{kind: rowToolsEmpty, text: i18n.T("ui.common.unwired")})
	}
	rows = append(rows, sec(i18n.T("ui.page.tools.section_builtin")), rowDesc{kind: rowToolsNote})
	for i := range f.toolsSnap.Builtins {
		rows = append(rows, rowDesc{kind: rowBuiltin, field: i})
		if f.polConfirm == f.toolsSnap.Builtins[i].Name {
			rows = append(rows, rowDesc{kind: rowToolsConfirm, field: i})
		}
	}
	rows = append(rows, sec(i18n.T("ui.page.tools.section_mcp")))
	for i := range f.toolsSnap.Plugins {
		rows = append(rows, rowDesc{kind: rowPlugin, field: i})
	}
	if len(f.toolsSnap.Plugins) == 0 {
		rows = append(rows, rowDesc{kind: rowToolsEmpty, text: i18n.T("ui.page.tools.no_plugins")})
	}
	// 目录级放宽（D282）：确认框里点「以后都允许」写进来的条目，只在某个目录内生效，
	// 不落在任何一行工具档位里——单独列出来，撤销出口也在这里。
	if len(f.toolsSnap.DirRules) > 0 {
		rows = append(rows, sec(i18n.T("ui.page.tools.section_dir")))
		for i := range f.toolsSnap.DirRules {
			rows = append(rows, rowDesc{kind: rowToolsDir, field: i})
		}
	}
	// 外部代理没有稳定的工具名（ACP 的 fs/terminal 回调），只能按整个来源设一条。
	rows = append(rows, sec(i18n.T("ui.page.tools.section_policy")),
		rowDesc{kind: rowToolsAgent}, rowDesc{kind: rowToolsHard})
	if f.toolsStatus != "" {
		rows = append(rows, rowDesc{kind: rowToolsStatus})
	}
	return rows
}

func (toolsPage) Body(gtx layout.Context, th *material.Theme, f *settingsForm, rows []rowDesc, i int) (layout.Dimensions, bool) {
	r := rows[i]
	switch r.kind {
	case rowBuiltin:
		if r.field < 0 || r.field >= len(f.toolsSnap.Builtins) {
			return layout.Dimensions{}, true
		}
		return f.builtinToolRow(gtx, th, f.toolsSnap.Builtins[r.field]), true
	case rowToolsConfirm:
		if r.field < 0 || r.field >= len(f.toolsSnap.Builtins) {
			return layout.Dimensions{}, true
		}
		return f.policyConfirmRow(gtx, th, f.toolsSnap.Builtins[r.field].Name), true
	case rowToolsAgent:
		return f.agentPolicyRow(gtx, th), true
	case rowToolsDir:
		if r.field < 0 || r.field >= len(f.toolsSnap.DirRules) {
			return layout.Dimensions{}, true
		}
		return f.dirRuleRow(gtx, th, f.toolsSnap.DirRules[r.field]), true
	case rowPlugin:
		if r.field < 0 || r.field >= len(f.toolsSnap.Plugins) {
			return layout.Dimensions{}, true
		}
		return f.pluginToolRow(gtx, th, f.toolsSnap.Plugins[r.field]), true
	case rowToolsNote:
		return f.policyNoteRow(gtx, th), true
	case rowToolsHard:
		l := material.Body2(th, i18n.T("ui.page.policy.hard_note"))
		l.Color = paperOf().text3
		return l.Layout(gtx), true
	case rowToolsEmpty:
		l := material.Body2(th, r.text)
		l.Color = paperOf().text3
		return l.Layout(gtx), true
	case rowToolsStatus:
		l := material.Body2(th, f.toolsStatus)
		if f.toolsErr {
			l.Color = paperOf().danger
		} else {
			l.Color = paperOf().text3
		}
		return l.Layout(gtx), true
	}
	return layout.Dimensions{}, false
}

func (toolsPage) Events(gtx layout.Context, u *UI, f *settingsForm) {
	if f.tools == nil {
		return
	}
	// S6/D143：动作分发（按钮只在工具页渲染；快照取上帧同步值）。
	for key, btn := range f.toolBtns {
		if !btn.Clicked(gtx) {
			continue
		}
		if strings.HasPrefix(key, "pol:") {
			f.dispatchPolicyConfirm(key)
			continue
		}
		u.dispatchToolAction(f, key)
	}
	// 行内档位分段（D282）：点中即落值；执行类选「允许」先立确认态。
	for _, b := range f.toolsSnap.Builtins {
		if idx := f.polSegFor(b.Name).clicked(gtx); idx >= 0 {
			values, _ := policySegments(b.Policy)
			if idx < len(values) {
				f.chooseToolPolicy(b.Name, values[idx])
			}
		}
	}
	if idx := f.polSegFor("").clicked(gtx); idx >= 0 {
		values, _ := policySegments(f.toolsSnap.AgentPolicy)
		if idx < len(values) {
			f.applyAgentPolicy(values[idx])
		}
	}
}

// FirstEditor 本页无编辑档（全是按钮/开关），不抢焦点。
func (toolsPage) FirstEditor(f *settingsForm) *widget.Editor { return nil }

// policyNoteRow 内置工具区顶部的说明：默认跟随哪个档、执行类为什么要确认。
func (f *settingsForm) policyNoteRow(gtx layout.Context, th *material.Theme) layout.Dimensions {
	l := material.Body2(th, i18n.Tf("ui.page.policy.note", "level", f.toolsSnap.Level))
	l.Color = paperOf().text3
	return l.Layout(gtx)
}

// dirRuleRow 一条目录级放宽（D282）：[工具名 · 目录][撤销]。
//
// 这一行的存在本身就是提示——用户是在弹框里点的「以后都允许」，隔几天不会记得点过，
// 目录要写全（不缩写、不换成「该项目」之类），否则他认不出这是哪个目录。
func (f *settingsForm) dirRuleRow(gtx layout.Context, th *material.Theme, rule DirRule) layout.Dimensions {
	return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
		layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Right: unit.Dp(10)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						l := material.Body1(th, i18n.Tf("ui.page.tools.dir_rule", "tool", rule.Label))
						return l.Layout(gtx)
					}),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						l := material.Body2(th, rule.Dir)
						l.Color = tok().textDim
						return l.Layout(gtx)
					}),
				)
			})
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return f.toolActionButton(gtx, th, "d:"+rule.Tool+":"+rule.Dir,
				i18n.T("ui.page.tools.dir_revoke"))
		}),
	)
}

// agentPolicyRow 外部代理整来源一行：名字固定是「全部请求」。
func (f *settingsForm) agentPolicyRow(gtx layout.Context, th *material.Theme) layout.Dimensions {
	values, labels := policySegments(f.toolsSnap.AgentPolicy)
	return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
		layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
			l := material.Body1(th, i18n.T("ui.page.policy.agent_label"))
			return l.Layout(gtx)
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return f.polSegFor("").layout(gtx, th, labels, policyIndex(values, f.toolsSnap.AgentPolicy))
		}),
	)
}

// chooseToolPolicy 用户点了某一档：执行类选「允许」先立确认态，其余直接写。
func (f *settingsForm) chooseToolPolicy(name, mode string) {
	if mode == "allow" && f.polExec(name) {
		f.polConfirm = name
		return
	}
	if f.polConfirm == name {
		f.polConfirm = ""
	}
	f.applyToolPolicy(name, mode)
}

// dispatchPolicyConfirm 执行类确认/取消（key = "pol:yes" / "pol:no"）。
func (f *settingsForm) dispatchPolicyConfirm(key string) {
	name := f.polConfirm
	if name == "" {
		return
	}
	if key == "pol:yes" {
		f.polConfirm = ""
		f.applyToolPolicy(name, "allow")
		return
	}
	f.polConfirm = ""
}

// policyConfirmRow 执行类自动放行的行内确认：一句话说清代价 + 确认/取消。
//
// 文字与按钮分两行：窄窗（620dp）下「一句话 + 两个按钮」挤在一行会把句子折成三行，
// 按钮被顶到中间，读起来像两段错位的内容。
func (f *settingsForm) policyConfirmRow(gtx layout.Context, th *material.Theme, tool string) layout.Dimensions {
	pp := paperOf()
	yes, no := f.polConfirmButtons()
	return layout.Inset{Top: unit.Dp(4), Bottom: unit.Dp(8)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				l := material.Body2(th, i18n.Tf("ui.page.policy.exec_confirm", "tool", tool))
				l.Color = pp.danger
				return l.Layout(gtx)
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Inset{Top: unit.Dp(6)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
						layout.Flexed(1, layout.Spacer{}.Layout),
						layout.Rigid(paperButton(th, yes,
							i18n.T("ui.page.policy.confirm_ok"), paperBtnPrimary, true)),
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							return layout.Inset{Left: unit.Dp(8)}.Layout(gtx,
								paperButton(th, no,
									i18n.T("ui.page.policy.confirm_cancel"), paperBtnSecondary, true))
						}),
					)
				})
			}),
		)
	})
}
