package uigui

// settings_datadir.go 「删除数据目录」入口（常规页存储区末行）：整个数据根
//（`~/.aquarius` 或 `-data` 改指处）的删除请求。装配根实现 DataDirAdmin——只标记
// 待删，真正的删除在**进程退出前**最后一步（日志句柄等已关，Windows 下运行中删不掉
// 正被占用的文件）；确认后 UI 走既有菜单退出路径（EOF 收尾）。
//
// 确认是**输入确认词**而非二次点击：整目录删掉不可恢复，误点代价太大，要求手输
// 字面量才放行。确认词不进文案表——语言无关的固定串，文档与界面提示里原样引用。

import (
	"strings"

	"gioui.org/layout"
	"gioui.org/unit"
	"gioui.org/widget/material"

	"github.com/Tonyjh07/Aquarius/internal/i18n"
)

// wipeConfirmWord 输入确认词（区分大小写）：输入框内容与它完全一致才执行删除。
const wipeConfirmWord = "DELETE"

// DataDirAdmin 数据目录管理面（装配根实现；线程安全——次窗 goroutine 调用）。
type DataDirAdmin interface {
	// Path 数据根的磁盘位置（提示文案里原样展示）。
	Path() string
	// RequestWipe 标记「退出时删除整个数据目录」；返回即已生效，调用方随后走退出
	// 路径（应用按正常流程收尾，进程结束前删目录）。删除尽力而为：被占用的文件
	// 留在原处，不阻断退出。
	RequestWipe() error
}

// dataWipeRow 存储区末行的删除入口：初始态一行按钮 + 说明；点「删除」展开确认区
// （夹宽输入框单独一行 + 确认/取消与说明一行）。未接线（dataDir == nil）时按钮禁用、
// 说明照常展示。
//
// 输入框**不能**与按钮同排塞进 Rigid：editorBox 会把 Min.X 抬到 Max.X 吃满剩余行宽
// （settings_row_body.go 的铺满规则），按钮被挤出窗外（实机截图踩中）。展开态因此
// 拆两行：输入框按确认词宽度夹宽独占一行，按钮与说明并排其下。
func (f *settingsForm) dataWipeRow(gtx layout.Context, th *material.Theme) layout.Dimensions {
	hint := func(gtx layout.Context) layout.Dimensions {
		l := material.Body2(th, i18n.Tf("ui.page.general.data_wipe_hint",
			"word", wipeConfirmWord, "path", f.dataDirPath()))
		l.Color = paperOf().text3
		return l.Layout(gtx)
	}
	if !f.wipeArmed {
		return fieldRow(gtx, th, i18n.T("ui.page.general.data_wipe_label"), func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return paperButton(th, &f.wipeArm, i18n.T("ui.page.general.data_wipe_btn"),
						paperBtnDanger, f.dataDir != nil)(gtx)
				}),
				layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
					return layout.Inset{Left: unit.Dp(10)}.Layout(gtx, hint)
				}),
			)
		})
	}
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return fieldRow(gtx, th, i18n.T("ui.page.general.data_wipe_label"),
				func(gtx layout.Context) layout.Dimensions {
					// 夹宽：确认词只有几个字符，框太宽读起来像「这里该填很多字」；
					// editorBox 满宽铺底衬，宽度由这里的 Max.X 决定。
					gtx.Constraints.Max.X = min(gtx.Constraints.Max.X, gtx.Dp(wipeInputDp))
					return editorBox(th, &f.wipeInput,
						i18n.Tf("ui.page.general.data_wipe_input_hint", "word", wipeConfirmWord))(gtx)
				})
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Top: unit.Dp(8)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return layout.Inset{Right: unit.Dp(8)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
							return paperButton(th, &f.wipeGo, i18n.T("ui.settings.dlg.remove_confirm"),
								paperBtnDanger, true)(gtx)
						})
					}),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return layout.Inset{Right: unit.Dp(12)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
							return paperButton(th, &f.wipeCancel, i18n.T("ui.common.cancel"),
								paperBtnSecondary, true)(gtx)
						})
					}),
					layout.Flexed(1, hint),
				)
			})
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			if f.wipeStatus == "" {
				return layout.Dimensions{}
			}
			return layout.Inset{Top: unit.Dp(4)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return f.secretStatusRowOf(gtx, th, f.wipeStatus, f.wipeErr)
			})
		}),
	)
}

// wipeInputDp 确认词输入框的夹宽（dp）：只装得下确认词 + 提示，不跟行宽走。
const wipeInputDp = 220

// dataDirPath 展示用的数据根位置（未接线 = 空，提示里少一段路径）。
func (f *settingsForm) dataDirPath() string {
	if f.dataDir == nil {
		return ""
	}
	return f.dataDir.Path()
}

// dataWipeAction 删除入口的动作分派：arm = 展开确认区；cancel = 收起清空；
// confirm = 校验确认词并标记待删，成功返回 true（调用方触发退出）。动作与渲染分开
// = headless 测试直接驱动，不必伪造点击事件。
func (u *UI) dataWipeAction(f *settingsForm, action string) bool {
	switch action {
	case "arm":
		f.wipeArmed = true
		f.wipeErr, f.wipeStatus = false, ""
	case "cancel":
		f.wipeArmed = false
		f.wipeInput.SetText("")
		f.wipeErr, f.wipeStatus = false, ""
	case "confirm":
		if strings.TrimSpace(f.wipeInput.Text()) != wipeConfirmWord {
			f.wipeErr, f.wipeStatus = true, i18n.Tf("ui.page.general.data_wipe_mismatch",
				"word", wipeConfirmWord)
			return false
		}
		if err := f.dataDir.RequestWipe(); err != nil {
			f.wipeErr, f.wipeStatus = true, i18n.Tf("ui.page.general.data_wipe_failed",
				"err", err.Error())
			return false
		}
		f.wipeArmed = false
		f.wipeInput.SetText("")
		f.wipeErr, f.wipeStatus = false, i18n.T("ui.page.general.data_wipe_ok")
		return true
	}
	return false
}
