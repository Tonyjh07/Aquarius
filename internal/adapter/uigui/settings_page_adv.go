package uigui

// settings_page_adv.go 「高级」页（D162 页面注册表）：config.json 原文整文件编辑。
// 保存前 JSON 校验由装配根负责（解析失败原文件不动）。

import (
	"gioui.org/layout"
	"gioui.org/widget"
	"gioui.org/widget/material"

	"github.com/Tonyjh07/Aquarius/internal/i18n"
)

// advPage 高级页模块。
type advPage struct{}

func (advPage) Key() string { return "adv" }

func (advPage) Rows(f *settingsForm) []rowDesc {
	return []rowDesc{sec(i18n.T("ui.page.adv.section_raw")), {kind: rowRaw}}
}

// Body 认领 rowRaw（改前它落进 rowBody 的 default 兜底；D162 起必须有页显式认领）。
func (advPage) Body(gtx layout.Context, th *material.Theme, f *settingsForm, rows []rowDesc, i int) (layout.Dimensions, bool) {
	if rows[i].kind != rowRaw {
		return layout.Dimensions{}, false
	}
	return editorBoxFill(th, &f.rawCfg, i18n.T("ui.page.adv.raw_hint"))(gtx), true
}

// FillRow 整文件编辑区是这一页唯一的可变高度行：它吸走内容区剩余高度。整份 config
// 越短越好读——写死高度会让窗口拉高后底下留一大片空白。
func (advPage) FillRow(rows []rowDesc) int {
	for i, r := range rows {
		if r.kind == rowRaw {
			return i
		}
	}
	return -1
}

func (advPage) Events(gtx layout.Context, u *UI, f *settingsForm) {}

func (advPage) FirstEditor(f *settingsForm) *widget.Editor { return &f.rawCfg }
