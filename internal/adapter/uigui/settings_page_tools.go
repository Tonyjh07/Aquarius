package uigui

// settings_page_tools.go 「工具与 MCP」页（D162 页面注册表；数据面与动作见
// settings_tools.go，S6/D143）：内置工具启停 + 插件管理。行数随快照变化，状态行
// 由异步动作结果回灌。本页只负责把它接进设置窗分页框架。

import (
	"gioui.org/layout"
	"gioui.org/widget"
	"gioui.org/widget/material"
)

// toolsPage 工具与 MCP 页模块。
type toolsPage struct{}

func (toolsPage) Key() string { return "工具与 MCP" }

func (toolsPage) Rows(f *settingsForm) []rowDesc {
	rows := []rowDesc{}
	if f.tools == nil {
		return append(rows, rowDesc{kind: rowToolsEmpty, text: "未接线（只读占位）"})
	}
	rows = append(rows, sec("内置工具（开关即时生效，无需重启）"))
	for i := range f.toolsSnap.Builtins {
		rows = append(rows, rowDesc{kind: rowBuiltin, field: i})
	}
	rows = append(rows, sec("MCP 与插件（来源 config / 插件目录）"))
	for i := range f.toolsSnap.Plugins {
		rows = append(rows, rowDesc{kind: rowPlugin, field: i})
	}
	if len(f.toolsSnap.Plugins) == 0 {
		rows = append(rows, rowDesc{kind: rowToolsEmpty, text: "没有已声明的插件（config mcpServers 或 插件目录）"})
	}
	if f.toolsStatus != "" {
		rows = append(rows, rowDesc{kind: rowToolsStatus})
	}
	return rows
}

func (toolsPage) Body(gtx layout.Context, th *material.Theme, f *settingsForm, rows []rowDesc, i int) (layout.Dimensions, bool) {
	r := rows[i]
	switch r.kind {
	case rowBuiltin:
		return f.builtinToolRow(gtx, th, f.toolsSnap.Builtins[r.field]), true
	case rowPlugin:
		return f.pluginToolRow(gtx, th, f.toolsSnap.Plugins[r.field]), true
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
	// S6/D143：动作分发（按钮只在工具页渲染；快照取上帧同步值）。
	for key, btn := range f.toolBtns {
		if btn.Clicked(gtx) {
			u.dispatchToolAction(f, key)
		}
	}
}

// FirstEditor 本页无编辑档（全是按钮/开关），不抢焦点。
func (toolsPage) FirstEditor(f *settingsForm) *widget.Editor { return nil }
