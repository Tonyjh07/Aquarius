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
)

// secretsPage 密钥库页模块。
type secretsPage struct{}

func (secretsPage) Key() string { return "密钥库" }

func (secretsPage) Rows(f *settingsForm) []rowDesc {
	rows := []rowDesc{sec("已存密钥（值不可回显，只给尾 4 位）")}
	if f.secrets == nil {
		return append(rows, rowDesc{kind: rowSecretEmpty, text: "未接线（只读占位）"})
	}
	if len(f.secretsSnap.Entries) == 0 {
		rows = append(rows, rowDesc{kind: rowSecretEmpty, text: "密钥库还是空的——下面填名字与值即可存入"})
	}
	for _, e := range f.secretsSnap.Entries {
		rows = append(rows, rowDesc{kind: rowSecretEntry, text: e.Name})
	}
	rows = append(rows,
		sec("从旧处收进来"),
		rowDesc{kind: rowSecretNote},
		rowDesc{kind: rowSecretImport},
	)
	rows = append(rows,
		sec("新增 / 覆盖"),
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
	case rowSecretNote:
		l := material.Body2(th, "把 config 明文里的密钥，或只在环境变量里的引用值，收进本机密钥库。")
		l.Color = paperOf().text3
		return l.Layout(gtx), true
	case rowSecretImport:
		return f.secretImportRow(gtx, th), true
	case rowSecretName:
		return fieldRow(gtx, th, "密钥名", editorBox(th, &f.secretName, "如 AQUARIUS_OPENAI_API_KEY；引用写作 secret:<名>")), true
	case rowSecretValue:
		return fieldRow(gtx, th, "密钥值", editorBox(th, &f.secretValue, "存入后不回显；留空 = 不写")), true
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
	for f.secretImport.Clicked(gtx) {
		u.secretsAction(f, "import", "")
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
				return f.secretActionButton(gtx, th, "del:"+name, "删除", paperBtnDanger)
			}
			return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					l := material.Body2(th, "确认？")
					l.Color = paperOf().danger
					return layout.Inset{Right: unit.Dp(8)}.Layout(gtx, l.Layout)
				}),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return layout.Inset{Right: unit.Dp(8)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
						return f.secretActionButton(gtx, th, "cancel:"+name, "取消", paperBtnSecondary)
					})
				}),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return f.secretActionButton(gtx, th, "confirm:"+name, "确认删除", paperBtnDanger)
				}),
			)
		}),
	)
}

// secretAddRow 新增/覆盖入口（键名与值在上一行；值字段动作后恒清空）。
func (f *settingsForm) secretAddRow(gtx layout.Context, th *material.Theme) layout.Dimensions {
	return paperButton(th, &f.secretAdd, "存入密钥库", paperBtnPrimary, f.secrets != nil)(gtx)
}

// secretImportRow 一键导入入口：把仍在 config 明文里的密钥，以及只存在于同名环境变量
// 里的引用值收进本机密钥库（config 里明文改写成 `secret:<名>` 引用）。未接线时禁用。
//
// 反馈**同行**摆在按钮右侧（口径同 provider 应用行）：导入结果若单起一行落在新增区之后，
// 用户点完按钮在视口里看不到任何反馈。
func (f *settingsForm) secretImportRow(gtx layout.Context, th *material.Theme) layout.Dimensions {
	return layout.Flex{Alignment: layout.Baseline, Spacing: layout.SpaceStart}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return paperButton(th, &f.secretImport, "导入旧密钥", paperBtnSecondary, f.secrets != nil)(gtx)
		}),
		layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
			if f.importStatus == "" {
				return layout.Dimensions{}
			}
			return layout.Inset{Left: unit.Dp(12)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return f.secretStatusRowOf(gtx, th, f.importStatus, f.importErr)
			})
		}),
	)
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
