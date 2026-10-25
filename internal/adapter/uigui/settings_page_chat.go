package uigui

// settings_page_chat.go 「对话」页（D162 页面注册表）：限额五项。S7b/D145 起人格编辑器
// 已拆到「系统提示词」页，本页只留限额。

import (
	"fmt"

	"gioui.org/layout"
	"gioui.org/widget"
	"gioui.org/widget/material"

	"github.com/Tonyjh07/Aquarius/internal/port"
)

// chatPage 对话页模块。
type chatPage struct{}

func (chatPage) Key() string { return "对话" }

func (chatPage) Rows(f *settingsForm) []rowDesc {
	return []rowDesc{
		sec("限额"),
		{kind: rowMaxTurns}, {kind: rowMaxCtx}, {kind: rowCompact},
		{kind: rowToolChars}, {kind: rowToolTo},
	}
}

func (chatPage) Body(gtx layout.Context, th *material.Theme, f *settingsForm, rows []rowDesc, i int) (layout.Dimensions, bool) {
	switch rows[i].kind {
	case rowMaxTurns:
		return fieldRow(gtx, th, "最大轮数", editorBox(th, &f.maxTurns, "单次提问的工具调用轮数上限（空 = 保持）")), true
	case rowMaxCtx:
		return fieldRow(gtx, th, "上下文上限", editorBox(th, &f.maxCtx, "tokens；自动压缩与截断的预算分母（空 = 保持）")), true
	case rowCompact:
		return fieldRow(gtx, th, "压缩阈值", editorBox(th, &f.compact, fmt.Sprintf("%g–%g；占用超阈值触发自动压缩（空 = 保持）",
			port.SettingsCompactMin, port.SettingsCompactMax))), true
	case rowToolChars:
		return fieldRow(gtx, th, "工具输出上限", editorBox(th, &f.toolChars, "字符；超出截断（空 = 保持）")), true
	case rowToolTo:
		return fieldRow(gtx, th, "工具超时", editorBox(th, &f.toolTo, "秒；缺省单次调用超时（空 = 保持）")), true
	}
	return layout.Dimensions{}, false
}

// Events 本页无独立动作（数值档的取值在保存时统一读；无按钮）。
func (chatPage) Events(gtx layout.Context, u *UI, f *settingsForm) {}

func (chatPage) FirstEditor(f *settingsForm) *widget.Editor { return &f.maxTurns }
