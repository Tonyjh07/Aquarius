package uigui

// settings_page_adv.go 「高级」页（D162 页面注册表）：config.json 原文整文件编辑。
// 保存前 JSON 校验由装配根负责（解析失败原文件不动，Q10）。

import (
	"gioui.org/layout"
	"gioui.org/widget"
	"gioui.org/widget/material"
)

// advPage 高级页模块。
type advPage struct{}

func (advPage) Key() string { return "高级" }

func (advPage) Rows(f *settingsForm) []rowDesc {
	return []rowDesc{sec("config.json 原文"), {kind: rowRaw}}
}

// Body 认领 rowRaw（改前它落进 rowBody 的 default 兜底；D162 起必须有页显式认领）。
func (advPage) Body(gtx layout.Context, th *material.Theme, f *settingsForm, rows []rowDesc, i int) (layout.Dimensions, bool) {
	if rows[i].kind != rowRaw {
		return layout.Dimensions{}, false
	}
	return editorBoxH(th, &f.rawCfg, "整文件编辑；保存前 JSON 校验（解析失败原文件不动，Q10）", 220)(gtx), true
}

func (advPage) Events(gtx layout.Context, u *UI, f *settingsForm) {}

func (advPage) FirstEditor(f *settingsForm) *widget.Editor { return &f.rawCfg }
