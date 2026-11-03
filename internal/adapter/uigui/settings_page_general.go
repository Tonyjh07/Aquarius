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

	"github.com/Tonyjh07/Aquarius/internal/i18n"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

// generalPage 常规页模块。
type generalPage struct{}

func (generalPage) Key() string { return "general" }

func (generalPage) Rows(f *settingsForm) []rowDesc {
	return []rowDesc{
		sec(i18n.T("ui.page.general.section.profile")),
		{kind: rowProfCurrent}, {kind: rowProfName}, {kind: rowProfBtns},
		{kind: rowWelcome},
		sec(i18n.T("ui.page.general.section.perm")), {kind: rowPerm}, {kind: rowUnattended},
		sec(i18n.T("ui.page.general.section.appearance")),
		{kind: rowLang}, {kind: rowPlaceholder}, {kind: rowHotkey}, {kind: rowTheme}, {kind: rowScale},
		{kind: rowFontSize}, {kind: rowWinSize},
		sec(i18n.T("ui.page.general.section.output")), {kind: rowNotify}, {kind: rowTTS},
		sec(i18n.T("ui.page.general.section.storage")), {kind: rowBackupKeep},
	}
}

func (generalPage) Body(gtx layout.Context, th *material.Theme, f *settingsForm, rows []rowDesc, i int) (layout.Dimensions, bool) {
	switch rows[i].kind {
	case rowProfCurrent:
		return fieldRow(gtx, th, i18n.T("ui.page.general.prof_current_label"), func(gtx layout.Context) layout.Dimensions {
			txt := f.profiles.Current
			if len(f.profiles.Items) > 0 {
				txt += i18n.Tf("ui.page.general.prof_current_avail", "names", strings.Join(f.profiles.Items, ", "))
			}
			l := material.Body1(th, txt)
			l.Color = paperOf().text3
			return l.Layout(gtx)
		}), true
	case rowProfName:
		return fieldRow(gtx, th, i18n.T("ui.page.general.prof_name_label"),
			editorBox(th, &f.profName, i18n.T("ui.page.general.prof_name_hint"))), true
	case rowProfBtns:
		return f.profileButtonsRow(gtx, th), true
	case rowWelcome:
		return f.welcomeRow(gtx, th), true
	case rowPerm:
		return fieldRow(gtx, th, i18n.T("ui.page.general.perm_label"), selectBtn(th, &f.perm)), true
	case rowUnattended:
		// D185：越界即拒——说明必须写在开关旁，否则「无人值守」会被读成「免确认」。
		//
		// 开关那侧**按上限夹宽**，不能两个都 Rigid：两段都要折行的文字并排时，前一个
		// Rigid 会拿走它折行后占满的整幅宽度，后一个只剩几个像素。中文字短，剩下那点还
		// 勉强能读（三行）；en 文案长 1.4 倍，前一格吃满后提示被压成一列一个字（实测
		// 849×700 窗口下就是一列竖排）。夹到 60% 后，左格在该上限内按自然宽排，右格拿
		// 剩下的全部——中文版看起来和从前一样，英文版两段都还能成句。
		return fieldRow(gtx, th, i18n.T("ui.page.general.unattended_label"), func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					if max := gtx.Constraints.Max.X * 3 / 5; gtx.Constraints.Max.X > max {
						gtx.Constraints.Max.X = max
					}
					return paperCheckBox(th, &f.unattend, i18n.T("ui.page.general.unattended_check"))(gtx)
				}),
				layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
					return layout.Inset{Left: unit.Dp(10)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
						l := material.Body2(th, i18n.T("ui.page.general.unattended_hint"))
						l.Color = paperOf().text3
						return l.Layout(gtx)
					})
				}),
			)
		}), true
	case rowLang:
		// D275/§7.8：语言在启动时定一次，进程内不换（文案已散进各视图与测量）。这行
		// 因此只写 config，旁边必须写明「重启后生效」——否则用户点完发现没变，会以为
		// 控件坏了，接着去翻高级页手改。
		return fieldRow(gtx, th, i18n.T("ui.page.general.lang_label"), func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
				layout.Rigid(selectBtn(th, &f.uiLang)),
				layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
					return layout.Inset{Left: unit.Dp(10)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
						l := material.Body2(th, i18n.T("ui.page.general.lang_hint"))
						l.Color = paperOf().text3
						return l.Layout(gtx)
					})
				}),
			)
		}), true
	case rowPlaceholder:
		// D279：自定义灰字，**不随界面语言变**（用户自己写的内容不翻译），因此这里没有
		// 「重启后生效」——它每帧现取，保存完下一帧就是新值。
		//
		// 说明**不放进输入框的占位**：填了值之后占位就不显示了，而那正是用户最需要知道
		// 「怎么清掉」的时候。所以另起一行灰字挂在框下面（框里留空串），行高跟着长一点。
		return fieldRow(gtx, th, i18n.T("ui.page.general.placeholder_label"), func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					gtx.Constraints.Min.X = gtx.Constraints.Max.X // 底衬与点击区都要铺满行宽
					return editorBox(th, &f.placeholder, "")(gtx)
				}),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return layout.Inset{Top: unit.Dp(4)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
						l := material.Body2(th, i18n.Tf("ui.page.general.placeholder_hint",
							"max", port.SettingsPlaceholderMax))
						l.Color = paperOf().text3
						return l.Layout(gtx)
					})
				}),
			)
		}), true
	case rowHotkey:
		return fieldRow(gtx, th, i18n.T("ui.page.general.hotkey_label"),
			editorBox(th, &f.hotkey, i18n.T("ui.page.general.hotkey_hint"))), true
	case rowTheme:
		return fieldRow(gtx, th, i18n.T("ui.page.general.theme_label"), selectBtn(th, &f.theme)), true
	case rowScale:
		return fieldRow(gtx, th, i18n.T("ui.page.general.scale_label"), selectBtn(th, &f.scale)), true
	case rowFontSize:
		return fieldRow(gtx, th, i18n.T("ui.page.general.font_size_label"),
			editorBox(th, &f.fontSize, i18n.Tf("ui.page.general.font_size_hint",
				"min", port.SettingsFontSizeMin, "max", port.SettingsFontSizeMax))), true
	case rowWinSize:
		return fieldRow(gtx, th, i18n.T("ui.page.general.win_size_label"), sizeRow(gtx, th, &f.winW, &f.winH)), true
	case rowNotify:
		return paperCheckBox(th, &f.notify, i18n.T("ui.page.general.notify_check"))(gtx), true
	case rowTTS:
		return paperCheckBox(th, &f.tts, i18n.T("ui.page.general.tts_check"))(gtx), true
	case rowBackupKeep:
		return fieldRow(gtx, th, i18n.T("ui.page.general.backup_keep_label"),
			editorBox(th, &f.backupKeep, i18n.Tf("ui.page.general.backup_keep_hint",
				"min", port.SettingsBackupKeepMin, "max", port.SettingsBackupKeepMax))), true
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
	f.openPicker(gtx, i18n.T("ui.page.general.perm_label"), &f.perm)
	f.openPicker(gtx, i18n.T("ui.page.general.lang_label"), &f.uiLang)
	f.openPicker(gtx, i18n.T("ui.page.general.theme_label"), &f.theme)
	f.openPicker(gtx, i18n.T("ui.page.general.scale_label"), &f.scale)
}

func (generalPage) FirstEditor(f *settingsForm) *widget.Editor { return &f.profName }

// profileButtonsRow profile 区三动作按钮（D110③）：新建 / 复制当前 / 切换（重启生效）。
func (f *settingsForm) profileButtonsRow(gtx layout.Context, th *material.Theme) layout.Dimensions {
	btn := func(c *widget.Clickable, label string) layout.Widget {
		return paperButton(th, c, label, paperBtnSecondary, true)
	}
	return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
		layout.Rigid(btn(&f.profCreate, i18n.T("ui.page.general.prof_create"))),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Left: unit.Dp(8)}.Layout(gtx, btn(&f.profCopy, i18n.T("ui.page.general.prof_copy")))
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Left: unit.Dp(8)}.Layout(gtx, btn(&f.profSwitch, i18n.T("ui.page.general.prof_switch")))
		}),
	)
}

// welcomeRow 欢迎 / 首次运行引导入口（D141）：设置窗是**唯一入口**——托盘与球右键菜单都
// 不带此项；另有「首跑 / `ui.welcomed` 缺失」时自动打开一次（装配根接线）。按钮开次窗，
// 单实例防重开由注册表保证（§15.7）。
func (f *settingsForm) welcomeRow(gtx layout.Context, th *material.Theme) layout.Dimensions {
	return fieldRow(gtx, th, i18n.T("ui.page.general.welcome_label"), func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return paperButton(th, &f.welcomeOpen, i18n.T("ui.page.general.welcome_open"), paperBtnSecondary, true)(gtx)
			}),
			layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
				return layout.Inset{Left: unit.Dp(10)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					l := material.Body2(th, i18n.T("ui.page.general.welcome_hint"))
					l.Color = paperOf().text3
					return l.Layout(gtx)
				})
			}),
		)
	})
}
