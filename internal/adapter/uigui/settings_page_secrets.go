package uigui

// settings_page_secrets.go 「密钥库」页（D162 页面注册表；数据面与动作见
// settings_secrets.go，S9-6b/D161）：本机加密密钥库（`secret:<名>`）的可视管理面
// ——列出已有名字（只给尾 4 位掩码）、新增/覆盖、行内二次确认删除。
//
// 页序契约：插在「模型」之后（D161 落点④），页集由注册表次序决定，加页只动
// settings_page.go 的 settingsPages 与本文件。

import (
	"strings"

	"gioui.org/layout"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"

	"github.com/Tonyjh07/Aquarius/internal/i18n"
)

// secretsPage 密钥库页模块。
type secretsPage struct{}

func (secretsPage) Key() string { return "secrets" }

func (secretsPage) Rows(f *settingsForm) []rowDesc {
	rows := []rowDesc{sec(i18n.T("ui.page.secrets.section_entries"))}
	if f.secrets == nil {
		return append(rows, rowDesc{kind: rowSecretEmpty, text: i18n.T("ui.common.unwired")})
	}
	if len(f.secretsSnap.Entries) == 0 {
		rows = append(rows, rowDesc{kind: rowSecretEmpty, text: i18n.T("ui.page.secrets.empty")})
	}
	for _, e := range f.secretsSnap.Entries {
		rows = append(rows, rowDesc{kind: rowSecretEntry, text: e.Name})
	}
	rows = append(rows,
		sec(i18n.T("ui.page.secrets.section_add")),
		rowDesc{kind: rowSecretName},
		rowDesc{kind: rowSecretValue},
		rowDesc{kind: rowSecretAdd},
	)
	if f.secretsStatus != "" {
		// 存入/删除反馈排在末尾；无反馈时不占行。
		rows = append(rows, rowDesc{kind: rowSecretStatus})
	}
	return rows
}

func (secretsPage) Body(gtx layout.Context, th *material.Theme, f *settingsForm, rows []rowDesc, i int) (layout.Dimensions, bool) {
	r := rows[i]
	switch r.kind {
	case rowSecretEntry:
		return f.secretEntryRow(gtx, th, r.text), true
	case rowSecretEmpty:
		l := material.Body2(th, r.text)
		l.Color = paperOf().text3
		return l.Layout(gtx), true
	case rowSecretName:
		return fieldRow(gtx, th, i18n.T("ui.page.secrets.name_label"),
			editorBox(th, &f.secretName, i18n.T("ui.page.secrets.name_hint"))), true
	case rowSecretValue:
		return fieldRow(gtx, th, i18n.T("ui.page.secrets.value_label"),
			editorBox(th, &f.secretValue, i18n.T("ui.page.secrets.value_hint"))), true
	case rowSecretAdd:
		return f.secretAddRow(gtx, th), true
	case rowSecretStatus:
		return f.secretStatusRow(gtx, th), true
	}
	return layout.Dimensions{}, false
}

func (secretsPage) Events(gtx layout.Context, u *UI, f *settingsForm) {
	for f.secretAdd.Clicked(gtx) {
		u.secretsAction(f, "put", "")
	}
	// 按键名分派：`del:` = 请求删除（进二次确认）；`confirm:` = 真删；`cancel:` = 退出确认。
	// 按钮只在「密钥库」页渲染，未渲染的 Clicked 恒假，故不会误触别的页。
	for key, btn := range f.secretBtns {
		for btn.Clicked(gtx) {
			switch {
			case strings.HasPrefix(key, "del:"):
				f.secretConfirm = strings.TrimPrefix(key, "del:")
			case strings.HasPrefix(key, "cancel:"):
				f.secretConfirm = ""
			case strings.HasPrefix(key, "confirm:"):
				u.secretsAction(f, "delete", strings.TrimPrefix(key, "confirm:"))
			}
		}
	}
}

// FirstEditor 开窗焦点入键名档（本页唯一还需手输的编辑起点）。
func (secretsPage) FirstEditor(f *settingsForm) *widget.Editor { return &f.secretName }

// secretEntryRow 已存条目行：名字 + 掩码 + （删除 / 行内二次确认）。
//
// 删除走**行内二次确认**（D161 落点④）：点「删除」不等于删——该行就地换成
// 「确认删除 / 取消」，避免误点把 MCP 插件还在引用的密钥删掉。
func (f *settingsForm) secretEntryRow(gtx layout.Context, th *material.Theme, name string) layout.Dimensions {
	mask := ""
	for _, e := range f.secretsSnap.Entries {
		if e.Name == name {
			mask = e.Mask
		}
	}
	confirming := f.secretConfirm == name
	return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
		layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
			// 名字与掩码**竖排**（同插件行口径）：横排会被右侧「确认删除/取消」挤压，
			// 掩码在中途折行，读起来像数据坏了。
			return layout.Inset{Right: unit.Dp(10)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return material.Body1(th, name).Layout(gtx)
					}),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						l := material.Body2(th, mask)
						l.Color = paperOf().text3
						return l.Layout(gtx)
					}),
				)
			})
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			if !confirming {
				return f.secretActionButton(gtx, th, "del:"+name, i18n.T("ui.common.delete"), paperBtnDanger)
			}
			return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					l := material.Body2(th, i18n.T("ui.common.confirm_q"))
					l.Color = paperOf().danger
					return layout.Inset{Right: unit.Dp(8)}.Layout(gtx, l.Layout)
				}),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return layout.Inset{Right: unit.Dp(8)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
						return f.secretActionButton(gtx, th, "cancel:"+name, i18n.T("ui.common.cancel"), paperBtnSecondary)
					})
				}),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return f.secretActionButton(gtx, th, "confirm:"+name, i18n.T("ui.page.secrets.confirm_delete"), paperBtnDanger)
				}),
			)
		}),
	)
}

// secretAddRow 新增/覆盖入口（键名与值在上一行；值字段动作后恒清空）。
func (f *settingsForm) secretAddRow(gtx layout.Context, th *material.Theme) layout.Dimensions {
	return paperButton(th, &f.secretAdd, i18n.T("ui.page.secrets.add"), paperBtnPrimary, f.secrets != nil)(gtx)
}

// secretActionButton 密钥库页动作按钮（get-or-create 按 key 缓存；文案与配色按帧给定）。
func (f *settingsForm) secretActionButton(gtx layout.Context, th *material.Theme, key, label string, kind paperBtnKind) layout.Dimensions {
	btn := f.secretBtns[key]
	if btn == nil {
		btn = new(widget.Clickable)
		f.secretBtns[key] = btn
	}
	return paperButton(th, btn, label, kind, true)(gtx)
}
