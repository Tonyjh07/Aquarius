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
)

// modelPage 模型页模块。
type modelPage struct{}

func (modelPage) Key() string { return "模型" }

func (modelPage) Rows(f *settingsForm) []rowDesc {
	rows := []rowDesc{
		sec("当前模型"), {kind: rowModelName},
		sec("思考与推理"), {kind: rowThink}, {kind: rowEffort},
		sec("采样参数（留空 = 交服务端默认）"),
		{kind: rowSamplingTemp}, {kind: rowSamplingTopP},
		{kind: rowSamplingMax}, {kind: rowSamplingStop},
		{kind: rowSamplingNote},
		sec("降级链"),
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
		return fieldRow(gtx, th, "当前模型名", editorBox(th, &f.model, "model.name；改动保存后经 /model 热切换")), true
	case rowThink:
		return paperCheckBox(th, &f.think, "原生思考（/think 总开关，关时忽略推理档位）")(gtx), true
	case rowEffort:
		return fieldRow(gtx, th, "推理档位", cycleBtn(th, &f.effort)), true
	case rowSamplingTemp:
		return fieldRow(gtx, th, "温度", editorBox(th, &f.samplingTemp, "0–2；越低越稳，越高越发散。留空 = 服务端默认，写 0 = 每次都取最可能的词")), true
	case rowSamplingTopP:
		return fieldRow(gtx, th, "top_p", editorBox(th, &f.samplingTopP, "0–1；只从概率累计到该比例的候选里挑。留空 = 服务端默认")), true
	case rowSamplingMax:
		return fieldRow(gtx, th, "输出上限", editorBox(th, &f.samplingMax, "tokens；单轮最多生成多少。留空 = 不发送（仍受本轮预算约束）")), true
	case rowSamplingStop:
		return fieldRow(gtx, th, "停止串", editorBox(th, &f.samplingStop, "JSON 数组，如 [\"\\n\", \"END\"]；生成到这些串就停。留空 = 不发送")), true
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
		for ff.prov.Clicked(gtx) {
			f.cycleChainProvider(i) // 循环选择器：点击进一个 provider
		}
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
	f.effort.advance(gtx)
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

// cycleChainProvider 把第 i 项的 provider 选择前进一档（循环选择器：material v0.10 无
// 下拉框，与推理档位同款）。选项为空时 no-op（无 provider 可切）。
func (f *settingsForm) cycleChainProvider(i int) {
	if i < 0 || i >= len(f.chain) || len(f.chainChoices) == 0 {
		return
	}
	ff := f.chain[i]
	ff.provIdx = (ff.provIdx + 1) % len(f.chainChoices)
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
	return paperNote(th,
		"留空 = 这个字段不发，由服务端取自家默认；写 0 = 明确要 0（温度 0 就是每次取最可能的词）。"+
			"这里填的是整条链的缺省；只想改某个模型，就在配置文件里给那个模型加 sampling。")(gtx)
}

// chainItemRow 降级链一项：提供者（循环选择器）/ 模型（单行档）/ 上移 / 下移 / 删除。
// 提供者走「循环选择器」而非下拉（material v0.10 无下拉框，与推理档位同款）；模型名
// 允许手填未声明的名字——链是运行时路由。
func (f *settingsForm) chainItemRow(gtx layout.Context, th *material.Theme, i int) layout.Dimensions {
	ff := f.chain[i]
	provLabel := ff.chainProvider(f.chainChoices)
	if provLabel == "" {
		provLabel = "（无 provider）"
	}
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
				layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
					return fieldRow(gtx, th, "提供者", paperButton(th, &ff.prov, provLabel, paperBtnSecondary, true))
				}),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return layout.Inset{Left: unit.Dp(8)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
						return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
							layout.Rigid(paperButton(th, &ff.up, "↑", paperBtnSecondary, true)),
							layout.Rigid(func(gtx layout.Context) layout.Dimensions {
								return layout.Inset{Left: unit.Dp(6)}.Layout(gtx, paperButton(th, &ff.down, "↓", paperBtnSecondary, true))
							}),
							layout.Rigid(func(gtx layout.Context) layout.Dimensions {
								return layout.Inset{Left: unit.Dp(6)}.Layout(gtx, paperButton(th, &ff.del, "删除", paperBtnDanger, true))
							}),
						)
					})
				}),
			)
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Top: unit.Dp(6)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return fieldRow(gtx, th, "模型", editorBox(th, &ff.model, "空 = 取该提供者的缺省模型；可填未声明的名字"))
			})
		}),
	)
}

// chainAddRow 「＋ 添加降级项」入口（链尾；项目为空时仍是唯一入口）。
func (f *settingsForm) chainAddRow(gtx layout.Context, th *material.Theme) layout.Dimensions {
	return paperButton(th, &f.chainAdd, "＋ 添加降级项", paperBtnSecondary, true)(gtx)
}
