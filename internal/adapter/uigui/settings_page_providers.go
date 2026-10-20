package uigui

// settings_page_providers.go 「模型服务」页（D162 页面注册表；D176 口径一）：provider
// 管理——协议 / 端点 / 密钥池 / 模型能力表。这一页回答「怎么连」，与「模型」页的
// 「用哪个」分开：两件事各自有明确的问题，混在一页就是 N 家 × 10 行的一堵墙。
//
// 本步（S11-7①）先把整块 provider 编辑区从「模型」页搬过来，卡片仍是全展开的旧形态；
// 折叠卡片（D176 口径二）与密钥池/能力表编辑（S11-7③）随后两步补。控件事件与异步结果
// 回灌的语义与改前一致，只是从「模型」页的 Body/Events 收进本页。

import (
	"gioui.org/layout"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"
)

// providersPage 模型服务页模块。
type providersPage struct{}

func (providersPage) Key() string { return "模型服务" }

func (providersPage) Rows(f *settingsForm) []rowDesc {
	rows := []rowDesc{sec("模型服务（provider 列表）")}
	for i := range f.provs {
		rows = append(rows, rowDesc{kind: rowProvSummary, prov: i})
		if f.provOpen == i { // 同一时刻只展开一张（D176 口径二）
			for fld := 0; fld < provFldN; fld++ {
				rows = append(rows, rowDesc{kind: rowProvField, prov: i, field: fld})
			}
			rows = append(rows, rowDesc{kind: rowProvBtns, prov: i})
		}
	}
	return append(rows, rowDesc{kind: rowProvAdd}, rowDesc{kind: rowProvApply})
}

func (providersPage) Body(gtx layout.Context, th *material.Theme, f *settingsForm, rows []rowDesc, i int) (layout.Dimensions, bool) {
	r := rows[i]
	switch r.kind {
	case rowProvSummary:
		return f.providerSummaryRow(gtx, th, r.prov), true
	case rowProvField:
		pf := f.provs[r.prov]
		eds := []*widget.Editor{&pf.name, &pf.baseURL, &pf.apiKey, &pf.models, &pf.tokenizer, &pf.unsupported}
		meta := provFieldMeta[r.field]
		if r.field == provModelsFld {
			// 模型名行右侧挂「获取模型」（D153，模拟稿同款）：按钮与输入框同一行，
			// 结果不自动覆盖——候选以 chip 列在行下方，点了才替换。
			return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return fieldRow(gtx, th, meta.label, func(gtx layout.Context) layout.Dimensions {
						return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
							layout.Flexed(1, editorBox(th, eds[r.field], meta.hint)),
							layout.Rigid(func(gtx layout.Context) layout.Dimensions {
								return layout.Inset{Left: unit.Dp(8)}.Layout(gtx,
									paperButton(th, &pf.modelsBtn, "获取模型", paperBtnSecondary, true))
							}),
						)
					})
				}),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					if len(pf.cands) == 0 {
						return layout.Dimensions{}
					}
					return layout.Inset{Top: unit.Dp(6)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
						return modelChips(gtx, th, pf)
					})
				}),
			), true
		}
		return fieldRow(gtx, th, meta.label, editorBox(th, eds[r.field], meta.hint)), true
	case rowProvBtns:
		return f.providerButtonsRow(gtx, th, r.prov), true
	case rowProvAdd:
		return f.providerAddRow(gtx, th), true
	case rowProvApply:
		return f.providerApplyRow(gtx, th), true
	}
	return layout.Dimensions{}, false
}

func (providersPage) Events(gtx layout.Context, u *UI, f *settingsForm) {
	for f.provAdd.Clicked(gtx) {
		f.addProviderForm()
	}
	for f.provApply.Clicked(gtx) {
		u.applyProviderChanges(f)
	}
	for i, pf := range f.provs {
		for pf.toggle.Clicked(gtx) {
			if f.provOpen == i {
				f.provOpen = -1 // 再点一次收起
			} else {
				f.provOpen = i // 展开本张即收起其它（只展开一张）
			}
		}
		for pf.test.Clicked(gtx) {
			u.runProviderTest(f, pf)
		}
		for pf.modelsBtn.Clicked(gtx) {
			u.runFetchModels(f, pf)
		}
		for k := range pf.candClicks {
			if !pf.candClicks[k].Clicked(gtx) {
				continue
			}
			if k < len(pf.cands) {
				// 点 chip = **替换**模型名行（模拟稿「点选替换」）：不追加，免得越点越长。
				pf.models.SetText(pf.cands[k])
			}
		}
		for pf.setPrimary.Clicked(gtx) {
			f.primaryIdx = i
		}
		for pf.del.Clicked(gtx) {
			if len(f.provs) > 1 { // 至少保留一条（providers 不可为空，启动校验同口径）
				f.provs = append(f.provs[:i], f.provs[i+1:]...)
				switch {
				case f.primaryIdx == i:
					f.primaryIdx = 0
				case f.primaryIdx > i:
					f.primaryIdx--
				}
				// 展开态跟随删除重算：删的就是展开的那张 → 全收起；删在它之前 → 下标前移。
				switch {
				case f.provOpen == i:
					f.provOpen = -1
				case f.provOpen > i:
					f.provOpen--
				}
			}
		}
	}
}

func (providersPage) FirstEditor(f *settingsForm) *widget.Editor {
	if f.provOpen < 0 || f.provOpen >= len(f.provs) {
		return nil // 全收起：无编辑档可聚焦（展开哪张才聚焦哪张）
	}
	return &f.provs[f.provOpen].name
}

// providerSummaryRow 折叠卡片收起态（S11-7②/D176 口径二）：整行可点（点击展开/收起），
// 左侧一个 caret（▸ 收起 / ▾ 展开），右侧一行摘要（名字 · 协议 · N 个模型 · M 把钥匙 · 是否主）。
// 摘要行落进分组卡（rowInCard 为真），展开后的字段行与它在同一张卡里——收起态一眼看全有几家，
// 展开哪家看哪家。
func (f *settingsForm) providerSummaryRow(gtx layout.Context, th *material.Theme, i int) layout.Dimensions {
	pf := f.provs[i]
	caret := "▸"
	if f.provOpen == i {
		caret = "▾"
	}
	return pf.toggle.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		gtx.Constraints.Min.X = gtx.Constraints.Max.X // 整行可点
		return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				l := material.Label(th, paperBodySp(th), caret)
				l.Color = paperOf().text2
				return l.Layout(gtx)
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Inset{Left: unit.Dp(8)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					l := material.Label(th, paperBodySp(th), pf.providerSummary(f.primaryIdx == i))
					l.Color = paperOf().text
					return l.Layout(gtx)
				})
			}),
		)
	})
}

// providerButtonsRow 单个 provider 的动作按钮：连通测试 / 设为主 / 删除（D110②）。
func (f *settingsForm) providerButtonsRow(gtx layout.Context, th *material.Theme, i int) layout.Dimensions {
	pf := f.provs[i]
	btn := func(c *widget.Clickable, label string) layout.Widget {
		return paperButton(th, c, label, paperBtnSecondary, true)
	}
	primary := "设为主"
	if f.primaryIdx == i {
		primary = "★ 主"
	}
	// 主 provider 的「★ 主」用强调色文字标识当前主，不铺整块实心底——一行里两块
	// 实心按钮会互相抢视线；不可删的那条（providers 不可为空）呈禁用态。
	mark := paperBtnSecondary
	if f.primaryIdx == i {
		mark = paperBtnPrimary
	}
	return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
		layout.Rigid(btn(&pf.test, "测试连通")),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Left: unit.Dp(8)}.Layout(gtx, paperButton(th, &pf.setPrimary, primary, mark, true))
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Left: unit.Dp(8)}.Layout(gtx, paperButton(th, &pf.del, "删除", paperBtnDanger, len(f.provs) > 1))
		}),
	)
}

// providerAddRow 新增 provider 入口（编辑区尾部；Apply 校验名字等必填项）。
func (f *settingsForm) providerAddRow(gtx layout.Context, th *material.Theme) layout.Dimensions {
	return paperButton(th, &f.provAdd, "＋ 新增 provider", paperBtnSecondary, true)(gtx)
}

// providerApplyRow provider 区应用按钮 + 反馈文案（成功绿/错误红 = 转写区语义色）。
func (f *settingsForm) providerApplyRow(gtx layout.Context, th *material.Theme) layout.Dimensions {
	return layout.Flex{Alignment: layout.Baseline, Spacing: layout.SpaceStart}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return paperButton(th, &f.provApply, "应用 provider 变更", paperBtnPrimary, true)(gtx)
		}),
		layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Left: unit.Dp(12)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				text := f.provStatus
				col := paperOf().text2
				switch {
				case text == "":
					text, col = "变更全量写回 config，重启生效；「测试连通」发真实最小请求。", paperOf().text3
				case f.provErr:
					col = paperOf().danger
				}
				l := material.Label(th, paperNoteSp(th), text)
				l.Color = col
				return l.Layout(gtx)
			})
		}),
	)
}

// modelChips 候选模型 chip（D153）：点击 = 用该名字**替换**模型名行。每行最多 3 个、
// 左对齐等分（最多 3 行）——候选可达几十个，铺开会把卡撑成一面墙；名字过长按 24 字截断，
// 状态行里再给出总数。
func modelChips(gtx layout.Context, th *material.Theme, pf *providerForm) layout.Dimensions {
	return paperChipFlow(gtx, th, "点选替换模型名", pf.cands, pf.candClicks)
}
