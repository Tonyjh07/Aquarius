package uigui

// settings_page_general.go 「常规」页（D162 页面注册表）：Profile 区 / 欢迎引导 /
// 权限等级 / 外观与呼出 / 输出。本页是**字段页**——行与控件一一对应，动作只在帧首
// 消费（保存走全局底栏，D142 口径不变）。

import (
	"strings"

	"gioui.org/layout"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"
)

// generalPage 常规页模块。
type generalPage struct{}

func (generalPage) Key() string { return "常规" }

func (generalPage) Rows(f *settingsForm) []rowDesc {
	return []rowDesc{
		sec("Profile"),
		{kind: rowProfCurrent}, {kind: rowProfName}, {kind: rowProfBtns},
		{kind: rowWelcome},
		sec("权限"), {kind: rowPerm},
		sec("外观与呼出"),
		{kind: rowHotkey}, {kind: rowTheme}, {kind: rowScale},
		{kind: rowFontSize}, {kind: rowWinSize},
		sec("输出"), {kind: rowNotify}, {kind: rowTTS},
	}
}

func (generalPage) Body(gtx layout.Context, th *material.Theme, f *settingsForm, rows []rowDesc, i int) (layout.Dimensions, bool) {
	switch rows[i].kind {
	case rowProfCurrent:
		return fieldRow(gtx, th, "当前", func(gtx layout.Context) layout.Dimensions {
			txt := f.profiles.Current
			if len(f.profiles.Items) > 0 {
				txt += "（可用：" + strings.Join(f.profiles.Items, ", ") + "）"
			}
			l := material.Body1(th, txt)
			l.Color = paperOf().text3
			return l.Layout(gtx)
		}), true
	case rowProfName:
		return fieldRow(gtx, th, "Profile 名", editorBox(th, &f.profName, "新建 / 复制 / 切换的目标名")), true
	case rowProfBtns:
		return f.profileButtonsRow(gtx, th), true
	case rowWelcome:
		return f.welcomeRow(gtx, th), true
	case rowPerm:
		return fieldRow(gtx, th, "权限等级", cycleBtn(th, &f.perm)), true
	case rowHotkey:
		return fieldRow(gtx, th, "全局快捷键", editorBox(th, &f.hotkey, "如 Alt+A（空 = 默认）")), true
	case rowTheme:
		return fieldRow(gtx, th, "主题", cycleBtn(th, &f.theme)), true
	case rowScale:
		return fieldRow(gtx, th, "元素缩放", cycleBtn(th, &f.scale)), true
	case rowFontSize:
		return fieldRow(gtx, th, "字号", editorBox(th, &f.fontSize, "正文字号 10–28（最终 = 字号 × 缩放）")), true
	case rowWinSize:
		return fieldRow(gtx, th, "窗口尺寸", sizeRow(gtx, th, &f.winW, &f.winH)), true
	case rowNotify:
		return paperCheckBox(th, &f.notify, "桌面通知（回答完成时）")(gtx), true
	case rowTTS:
		return paperCheckBox(th, &f.tts, "语音播报（已解析配置，尚未启用）")(gtx), true
	}
	return layout.Dimensions{}, false
}

func (generalPage) Events(gtx layout.Context, u *UI, f *settingsForm) {
	for f.profCreate.Clicked(gtx) {
		u.profileAction(f, "create")
	}
	for f.profCopy.Clicked(gtx) {
		u.profileAction(f, "copy")
	}
	for f.profSwitch.Clicked(gtx) {
		u.profileAction(f, "switch")
	}
	for f.welcomeOpen.Clicked(gtx) {
		u.OpenWelcome() // D141：入口在设置窗（两个菜单都不带此项）
	}
	f.perm.advance(gtx)
	f.theme.advance(gtx)
	f.scale.advance(gtx)
}

func (generalPage) FirstEditor(f *settingsForm) *widget.Editor { return &f.profName }

// profileButtonsRow profile 区三动作按钮（D110③）：新建 / 复制当前 / 切换（重启生效）。
func (f *settingsForm) profileButtonsRow(gtx layout.Context, th *material.Theme) layout.Dimensions {
	btn := func(c *widget.Clickable, label string) layout.Widget {
		return paperButton(th, c, label, paperBtnSecondary, true)
	}
	return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
		layout.Rigid(btn(&f.profCreate, "新建")),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Left: unit.Dp(8)}.Layout(gtx, btn(&f.profCopy, "复制当前"))
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Left: unit.Dp(8)}.Layout(gtx, btn(&f.profSwitch, "切换（重启生效）"))
		}),
	)
}

// welcomeRow 欢迎 / 首次运行引导入口（D141）：设置窗是**唯一入口**——托盘与球右键菜单都
// 不带此项；另有「首跑 / `ui.welcomed` 缺失」时自动打开一次（装配根接线）。按钮开次窗，
// 单实例防重开由注册表保证（§15.7）。
func (f *settingsForm) welcomeRow(gtx layout.Context, th *material.Theme) layout.Dimensions {
	return fieldRow(gtx, th, "欢迎引导", func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return paperButton(th, &f.welcomeOpen, "打开", paperBtnSecondary, true)(gtx)
			}),
			layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
				return layout.Inset{Left: unit.Dp(10)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					l := material.Body2(th, "模型接入向导 + 快捷键 / 右键菜单 / 常用命令")
					l.Color = paperOf().text3
					return l.Layout(gtx)
				})
			}),
		)
	})
}
