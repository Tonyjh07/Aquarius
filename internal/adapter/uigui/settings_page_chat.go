package uigui

// settings_page_chat.go 「对话」页（D162 页面注册表）：限额五项。S7b/D145 起人格编辑器
// 已拆到「系统提示词」页，本页只留限额。

import (
	"gioui.org/layout"
	"gioui.org/widget"
	"gioui.org/widget/material"

	"github.com/Tonyjh07/Aquarius/internal/i18n"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

// chatPage 对话页模块。
type chatPage struct{}

func (chatPage) Key() string { return "chat" }

func (chatPage) Rows(f *settingsForm) []rowDesc {
	return []rowDesc{
		sec(i18n.T("ui.page.chat.section_limits")),
		{kind: rowMaxTurns}, {kind: rowMaxCtx}, {kind: rowCompact},
		{kind: rowToolChars}, {kind: rowToolTo},
	}
}

func (chatPage) Body(gtx layout.Context, th *material.Theme, f *settingsForm, rows []rowDesc, i int) (layout.Dimensions, bool) {
	switch rows[i].kind {
	case rowMaxTurns:
		return fieldRow(gtx, th, i18n.T("ui.page.chat.max_turns"),
			editorBox(th, &f.maxTurns, i18n.T("ui.page.chat.max_turns_hint"))), true
	case rowMaxCtx:
		return fieldRow(gtx, th, i18n.T("ui.page.chat.max_ctx"),
			editorBox(th, &f.maxCtx, i18n.T("ui.page.chat.max_ctx_hint"))), true
	case rowCompact:
		return fieldRow(gtx, th, i18n.T("ui.page.chat.compact"), editorBox(th, &f.compact,
			i18n.Tf("ui.page.chat.compact_hint",
				"min", port.SettingsCompactMin, "max", port.SettingsCompactMax))), true
	case rowToolChars:
		return fieldRow(gtx, th, i18n.T("ui.page.chat.tool_chars"),
			editorBox(th, &f.toolChars, i18n.T("ui.page.chat.tool_chars_hint"))), true
	case rowToolTo:
		return fieldRow(gtx, th, i18n.T("ui.page.chat.tool_timeout"),
			editorBox(th, &f.toolTo, i18n.T("ui.page.chat.tool_timeout_hint"))), true
	}
	return layout.Dimensions{}, false
}

// Events 本页无独立动作（数值档的取值在保存时统一读；无按钮）。
func (chatPage) Events(gtx layout.Context, u *UI, f *settingsForm) {}

func (chatPage) FirstEditor(f *settingsForm) *widget.Editor { return &f.maxTurns }
