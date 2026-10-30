package uigui

// settings_page_characters.go 「角色」页（§5.17；数据面与头像加载见 settings_characters.go）。
//
// 页面内容三块：
//  1. 已导入清单（头像 / 显示名 / 来源与条目数）——「开始对话」开新会话，「删除」行内二次确认；
//  2. 导入（卡文件来源输入档 + 「选择文件…」+「导入」）；
//  3. 动作反馈与读目录失败提示。
//
// 动作全部经 `/character` 命令通道（回执进主窗转写区），本页不直连管理面；卡片只读导入，
// 不编辑、不导出。

import (
	"strings"

	"gioui.org/layout"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"

	"github.com/Tonyjh07/Aquarius/internal/adapter/uigui/platform"
)

// charactersPage 角色页模块。
type charactersPage struct{}

func (charactersPage) Key() string { return "角色" }

func (charactersPage) Rows(f *settingsForm) []rowDesc {
	rows := []rowDesc{sec("已导入角色卡")}
	if !f.charWired {
		return append(rows, rowDesc{kind: rowCharEmpty, text: "未接线（只读占位）"})
	}
	if f.charactersSnap.Err != "" {
		rows = append(rows, rowDesc{kind: rowCharWarn, text: "读取角色卡目录失败：" + f.charactersSnap.Err})
	}
	if len(f.charactersSnap.Items) == 0 {
		rows = append(rows, rowDesc{kind: rowCharEmpty,
			text: "还没有角色卡——下面选一个卡文件（.png / .json / .charx）导入"})
	}
	for i := range f.charactersSnap.Items {
		rows = append(rows, rowDesc{kind: rowCharItem, field: i})
	}
	rows = append(rows,
		sec("导入角色卡"),
		rowDesc{kind: rowCharNote},
		rowDesc{kind: rowCharSource},
		rowDesc{kind: rowCharPick},
	)
	if f.charStatus != "" {
		rows = append(rows, rowDesc{kind: rowCharStatus})
	}
	return rows
}

func (charactersPage) Body(gtx layout.Context, th *material.Theme, f *settingsForm, rows []rowDesc, i int) (layout.Dimensions, bool) {
	r := rows[i]
	switch r.kind {
	case rowCharItem:
		if r.field < 0 || r.field >= len(f.charactersSnap.Items) {
			return layout.Dimensions{}, true
		}
		return f.charItemRow(gtx, th, f.charactersSnap.Items[r.field]), true
	case rowCharEmpty:
		l := material.Body2(th, r.text)
		l.Color = paperOf().text3
		return l.Layout(gtx), true
	case rowCharWarn:
		l := material.Body2(th, r.text)
		l.Color = paperOf().danger
		return l.Layout(gtx), true
	case rowCharNote:
		l := material.Body2(th, "支持 TavernCard 角色卡：PNG 图卡、card.json 与 .charx 卡包。"+
			"导入的是副本，只读不改原卡；同名不覆盖——先删除再导入。")
		l.Color = paperOf().text3
		return l.Layout(gtx), true
	case rowCharSource:
		return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
			layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
				return fieldRow(gtx, th, "卡文件", editorBox(th, &f.charSource,
					"角色卡文件绝对路径（.png / .json / .charx）"))
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Inset{Left: unit.Dp(10)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					return paperButton(th, &f.charImport, "导入", paperBtnPrimary, true)(gtx)
				})
			}),
		), true
	case rowCharPick:
		return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return paperButton(th, &f.charPick, "选择文件…", paperBtnSecondary, true)(gtx)
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Inset{Left: unit.Dp(10)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					l := material.Label(th, paperMicroSp(th), "选中路径只填进上面的来源框，再点「导入」才落盘")
					l.Color = paperOf().text3
					return l.Layout(gtx)
				})
			}),
		), true
	case rowCharStatus:
		l := material.Body2(th, f.charStatus)
		if f.charErr {
			l.Color = paperOf().danger
		} else {
			l.Color = paperOf().text3
		}
		return l.Layout(gtx), true
	}
	return layout.Dimensions{}, false
}

func (charactersPage) Events(gtx layout.Context, u *UI, f *settingsForm) {
	for f.charImport.Clicked(gtx) {
		path := strings.TrimSpace(f.charSource.Text())
		if path == "" {
			f.charStatus, f.charErr = "请先填卡文件路径（或用「选择文件…」）", true
			continue
		}
		u.sendCharacterCmd("import", path)
		f.charSource.SetText("")
		f.charStatus, f.charErr = "已提交导入："+path+"（结果见主窗对话）", false
	}
	for f.charPick.Clicked(gtx) {
		u.plat.RequestPathPick(platform.PickCharacterCard)
	}
	// 按键分派：`start:` / `rm:` / `cancel:` / `confirm:`。
	for key, btn := range f.charBtns {
		for btn.Clicked(gtx) {
			switch {
			case strings.HasPrefix(key, "start:"):
				slug := strings.TrimPrefix(key, "start:")
				u.sendCharacterCmd("start", slug)
				f.charStatus, f.charErr = "已开始对话："+slug, false
			case strings.HasPrefix(key, "rm:"):
				f.charConfirm = strings.TrimPrefix(key, "rm:")
			case strings.HasPrefix(key, "cancel:"):
				f.charConfirm = ""
			case strings.HasPrefix(key, "confirm:"):
				slug := strings.TrimPrefix(key, "confirm:")
				f.charConfirm = ""
				u.sendCharacterCmd("remove", slug)
				f.charStatus, f.charErr = "已删除角色卡："+slug, false
			}
		}
	}
}

// FirstEditor 开窗焦点入来源档（导入是本页的主要动作）；未接线则不强抢焦点。
func (charactersPage) FirstEditor(f *settingsForm) *widget.Editor {
	if !f.charWired {
		return nil
	}
	return &f.charSource
}

// charItemRow 已导入角色卡一行：头像 + 显示名 + 来源/条目 meta + 右侧动作（开始对话 / 删除）。
func (f *settingsForm) charItemRow(gtx layout.Context, th *material.Theme, st CharacterState) layout.Dimensions {
	return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Right: unit.Dp(12)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return f.charAvatar(gtx, th, st)
			})
		}),
		layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Right: unit.Dp(10)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return material.Body1(th, st.Display).Layout(gtx)
					}),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						l := material.Label(th, paperMicroSp(th), st.metaLine())
						l.Color = paperOf().text3
						l.MaxLines = 1
						return l.Layout(gtx)
					}),
				)
			})
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			if f.charConfirm == st.Slug {
				return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						l := material.Body2(th, "确认？")
						l.Color = paperOf().danger
						return layout.Inset{Right: unit.Dp(8)}.Layout(gtx, l.Layout)
					}),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return layout.Inset{Right: unit.Dp(8)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
							return f.charActionButton(gtx, th, "cancel:"+st.Slug, "取消", paperBtnSecondary)
						})
					}),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return f.charActionButton(gtx, th, "confirm:"+st.Slug, "确认删除", paperBtnDanger)
					}),
				)
			}
			return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return layout.Inset{Right: unit.Dp(6)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
						return f.charActionButton(gtx, th, "start:"+st.Slug, "开始对话", paperBtnSecondary)
					})
				}),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return f.charActionButton(gtx, th, "rm:"+st.Slug, "删除", paperBtnDanger)
				}),
			)
		}),
	)
}

// charActionButton 角色页动作按钮（get-or-create 按 key 缓存）。
func (f *settingsForm) charActionButton(gtx layout.Context, th *material.Theme, key, label string, kind paperBtnKind) layout.Dimensions {
	btn := f.charBtns[key]
	if btn == nil {
		btn = new(widget.Clickable)
		f.charBtns[key] = btn
	}
	return paperButton(th, btn, label, kind, true)(gtx)
}
