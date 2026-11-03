package uigui

// settings_page_model.go 「模型」页（D162 页面注册表；D176 口径一）：**用哪个**——当前
// 模型名 / 原生思考与推理档位 / 有序降级链。provider 本体（怎么连：协议 / 端点 / 密钥池 /
// 模型能力表）搬到「模型服务」页（settings_page_providers.go），这一页只做路由决策。
//
// 降级链是**列表**不是文本框（D176 口径三）：每项一行 provider / model，可上移/下移/删除，
// 尾部「＋ 添加」。链改动随「保存并应用」全量写回 config（重启生效，与 provider 变更同口径）。

import (
	"gioui.org/layout"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"

	"github.com/Tonyjh07/Aquarius/internal/i18n"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

// modelPage 模型页模块。
type modelPage struct{}

func (modelPage) Key() string { return "model" }

func (modelPage) Rows(f *settingsForm) []rowDesc {
	rows := []rowDesc{
		sec(i18n.T("ui.page.model.section.current")), {kind: rowModelName},
		sec(i18n.T("ui.page.model.section.thinking")), {kind: rowThink}, {kind: rowEffort},
		sec(i18n.T("ui.page.model.section.sampling")),
		{kind: rowSamplingTemp}, {kind: rowSamplingTopP},
		{kind: rowSamplingMax}, {kind: rowSamplingStop},
		{kind: rowSamplingNote},
		sec(i18n.T("ui.page.model.section.chain")),
	}
	if f.snap.FallbackNote != "" {
		rows = append(rows, rowDesc{kind: rowChainNote, text: f.snap.FallbackNote})
	}
	for i := range f.chain {
		rows = append(rows, rowDesc{kind: rowChainItem, field: i})
	}
	return append(rows, rowDesc{kind: rowChainAdd})
}

func (modelPage) Body(gtx layout.Context, th *material.Theme, f *settingsForm, rows []rowDesc, i int) (layout.Dimensions, bool) {
	r := rows[i]
	switch r.kind {
	case rowModelName: // S4d：当前模型名（D110② 起在 UI 上失位，本次补回）
		return fieldRow(gtx, th, i18n.T("ui.page.model.model_name_label"),
			editorBox(th, &f.model, i18n.T("ui.page.model.model_name_hint"))), true
	case rowThink:
		return paperCheckBox(th, &f.think, i18n.T("ui.page.model.think_check"))(gtx), true
	case rowEffort:
		return fieldRow(gtx, th, i18n.T("ui.page.model.effort_label"), selectBtn(th, &f.effort)), true
	case rowSamplingTemp:
		return fieldRow(gtx, th, i18n.T("ui.page.model.temp_label"),
			editorBox(th, &f.samplingTemp, i18n.Tf("ui.page.model.temp_hint",
				"min", port.SettingsTemperatureMin, "max", port.SettingsTemperatureMax))), true
	case rowSamplingTopP:
		return fieldRow(gtx, th, "top_p",
			editorBox(th, &f.samplingTopP, i18n.Tf("ui.page.model.topp_hint",
				"min", port.SettingsTopPMin, "max", port.SettingsTopPMax))), true
	case rowSamplingMax:
		return fieldRow(gtx, th, i18n.T("ui.page.model.max_label"),
			editorBox(th, &f.samplingMax, i18n.T("ui.page.model.max_hint"))), true
	case rowSamplingStop:
		return fieldRow(gtx, th, i18n.T("ui.page.model.stop_label"),
			editorBox(th, &f.samplingStop, i18n.T("ui.page.model.stop_hint"))), true
	case rowSamplingNote:
		return samplingNoteRow(gtx, th), true
	case rowChainNote:
		return chainNoteRow(gtx, th, r.text), true
	case rowChainItem:
		return f.chainItemRow(gtx, th, r.field), true
	case rowChainAdd:
		return f.chainAddRow(gtx, th), true
	}
	return layout.Dimensions{}, false
}

func (modelPage) Events(gtx layout.Context, u *UI, f *settingsForm) {
	for f.chainAdd.Clicked(gtx) {
		f.addFallbackForm()
	}
	for i, ff := range f.chain {
		f.openPicker(gtx, i18n.T("ui.page.model.prov_picker"), &ff.prov) // 选项即 chainChoices
		for ff.up.Clicked(gtx) {
			f.moveFallback(i, -1)
		}
		for ff.down.Clicked(gtx) {
			f.moveFallback(i, 1)
		}
		for ff.del.Clicked(gtx) {
			f.removeFallback(i)
		}
	}
	f.openPicker(gtx, i18n.T("ui.page.model.effort_label"), &f.effort)
}

func (modelPage) FirstEditor(f *settingsForm) *widget.Editor { return &f.model }

// moveFallback 上移/下移降级链一项（dir = -1 / +1；越界 no-op）。
func (f *settingsForm) moveFallback(i, dir int) {
	j := i + dir
	if i < 0 || i >= len(f.chain) || j < 0 || j >= len(f.chain) {
		return
	}
	f.chain[i], f.chain[j] = f.chain[j], f.chain[i]
}

// removeFallback 删除降级链一项（越界 no-op）。
func (f *settingsForm) removeFallback(i int) {
	if i < 0 || i >= len(f.chain) {
		return
	}
	f.chain = append(f.chain[:i], f.chain[i+1:]...)
}

// chainNoteRow 链窄于当前模型能力时的一行警示（cmd 侧按 caps 现算；空 = 不窄，行不出现）。
func chainNoteRow(gtx layout.Context, th *material.Theme, text string) layout.Dimensions {
	l := material.Label(th, paperNoteSp(th), text)
	l.Color = paperOf().danger
	return l.Layout(gtx)
}

// samplingNoteRow 采样参数的说明段：讲清「留空」与「写 0」的区别——这是这一组字段
// 最容易误解的地方（留空 = 不发这个字段，由服务端取默认；写 0 = 明确要 0）。
func samplingNoteRow(gtx layout.Context, th *material.Theme) layout.Dimensions {
	return paperNote(th, i18n.T("ui.page.model.sampling_note"))(gtx)
}

// chainItemRow 降级链一项：提供者（枚举选择器）/ 模型（单行档）/ 上移 / 下移 / 删除。
// 模型名允许手填未声明的名字——链是运行时路由。
func (f *settingsForm) chainItemRow(gtx layout.Context, th *material.Theme, i int) layout.Dimensions {
	ff := f.chain[i]
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
				layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
					return fieldRow(gtx, th, i18n.T("ui.page.model.prov_label"), selectBtn(th, &ff.prov))
				}),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return layout.Inset{Left: unit.Dp(8)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
						return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
							layout.Rigid(paperButton(th, &ff.up, "↑", paperBtnSecondary, true)),
							layout.Rigid(func(gtx layout.Context) layout.Dimensions {
								return layout.Inset{Left: unit.Dp(6)}.Layout(gtx, paperButton(th, &ff.down, "↓", paperBtnSecondary, true))
							}),
							layout.Rigid(func(gtx layout.Context) layout.Dimensions {
								return layout.Inset{Left: unit.Dp(6)}.Layout(gtx, paperButton(th, &ff.del, i18n.T("ui.common.delete"), paperBtnDanger, true))
							}),
						)
					})
				}),
			)
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Top: unit.Dp(6)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return fieldRow(gtx, th, i18n.T("ui.page.model.model_label"),
					editorBox(th, &ff.model, i18n.T("ui.page.model.model_hint")))
			})
		}),
	)
}

// chainAddRow 「＋ 添加降级项」入口（链尾；项目为空时仍是唯一入口）。
func (f *settingsForm) chainAddRow(gtx layout.Context, th *material.Theme) layout.Dimensions {
	return paperButton(th, &f.chainAdd, i18n.T("ui.page.model.chain_add"), paperBtnSecondary, true)(gtx)
}
