package uigui

// settings_page_prompt.go 「系统提示词」页（D162 页面注册表）：人格模块树 / 有序引用 /
// 内置只读模块 / 组合预览。编辑区本体是 promptEditor（prompt_editor.go）——与历史窗
// 遮罩「默认模板」页签**同一份实现**（D154）；本页只把它接进设置窗的分页框架。

import (
	"gioui.org/layout"
	"gioui.org/widget"
	"gioui.org/widget/material"
)

// promptPage 系统提示词页模块。
type promptPage struct{}

func (promptPage) Key() string { return "系统提示词" }

// Rows 由 promptEditor 给行表（S7/D144 + S7b/D145：独立成页；D154 与遮罩共用一份）。
func (promptPage) Rows(f *settingsForm) []rowDesc {
	return f.promptEditor.rows()
}

func (promptPage) Body(gtx layout.Context, th *material.Theme, f *settingsForm, rows []rowDesc, i int) (layout.Dimensions, bool) {
	return f.promptEditor.rowBody(gtx, th, rows, i)
}

func (promptPage) Events(gtx layout.Context, u *UI, f *settingsForm) {
	// S7/D144：人格模块树与引用编辑（新增/删除/上移/下移、内置模块「引用」）。
	// D148：模块库改成三列网格——新增走网格末尾那格，模块本身的增删改走右键弹窗。
	// D154：这一组动作收在 promptEditor 里（与遮罩「默认模板」页签共用）。
	// 模块网格手势与组合预览按**当前页**收窄（磁贴只在「系统提示词」页渲染；
	// 磁贴拖动会吃整帧指针，非当前页必须不接）。
	f.promptEditor.refEvents(gtx)
	if f.page == pagePrompt {
		if !f.modEdit.open {
			f.moduleGridEvents(gtx)
		}
		f.promptEditor.preview(u)
	}
}

func (promptPage) FirstEditor(f *settingsForm) *widget.Editor { return &f.sysPrompt }
