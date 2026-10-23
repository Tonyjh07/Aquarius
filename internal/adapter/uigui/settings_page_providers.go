package uigui

// settings_page_providers.go 「模型服务」页（D162 页面注册表；D176 口径一）：provider
// 管理——协议 / 端点 / 密钥池 / 模型能力表。这一页回答「怎么连」，与「模型」页的
// 「用哪个」分开：两件事各自有明确的问题，混在一页就是 N 家 × 10 行的一堵墙。
//
// 形态（D176 口径二）：每家一张折叠卡片，收起态一行摘要、展开才露出全部字段；同一
// 时刻只展开一张。展开区含三块：端点/协议等基本字段、密钥池（多把，值只写不回显）、
// 模型能力表（每行 name + 三能力勾选 + 上下文窗口 + 「探测能力」）。
//
// 数据面：密钥池与模型能力表的写回走 ProviderManager.Apply（全量）；「探测能力」走
// ProviderManager.Probe（与 `/model probe` 同一条写回逻辑，D174）。

import (
	"fmt"
	"image"
	"strconv"
	"strings"

	"gioui.org/font"
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
		if f.provOpen != i { // 同一时刻只展开一张（D176 口径二）
			continue
		}
		pf := f.provs[i]
		rows = append(rows,
			rowDesc{kind: rowProvName, prov: i},
			rowDesc{kind: rowProvKind, prov: i},
			rowDesc{kind: rowProvBase, prov: i},
			rowDesc{kind: rowProvTok, prov: i},
			rowDesc{kind: rowProvUnsup, prov: i},
			rowDesc{kind: rowProvHead, prov: i, text: "密钥池"},
		)
		for k := range pf.keyForms {
			rows = append(rows, rowDesc{kind: rowProvKey, prov: i, field: k})
		}
		rows = append(rows, rowDesc{kind: rowProvKeyAdd, prov: i},
			rowDesc{kind: rowProvModels, prov: i},
		)
		start, end := pf.modelPageBounds()
		for m := start; m < end; m++ {
			rows = append(rows, rowDesc{kind: rowProvModel, prov: i, field: m})
		}
		if pf.modelPageCount() > 1 {
			rows = append(rows, rowDesc{kind: rowProvModelPager, prov: i})
		}
		rows = append(rows, rowDesc{kind: rowProvModelAdd, prov: i}, rowDesc{kind: rowProvBtns, prov: i})
	}
	return append(rows, rowDesc{kind: rowProvAdd}, rowDesc{kind: rowProvApply})
}

// OnEnter 首次切到本页时自动拉一轮各家服务端模型清单（D181）：结果并入模型列表（新增行
// 默认未启用），随「应用 provider 变更」落盘。每窗只拉一次——来回切页不该反复打网络。
func (providersPage) OnEnter(u *UI, f *settingsForm) {
	if f.provFetched || f.readOnly || u.opts.ProviderMgr == nil {
		return
	}
	f.provFetched = true
	for _, pf := range f.provs {
		switch {
		case pf.fetchNote != "": // 已有结果（例如刚被「获取模型」拉过）：不重复请求
			continue
		case strings.TrimSpace(pf.baseURL.Text()) == "": // 没填端点的卡片问不出东西
			continue
		}
		u.runFetchModels(f, pf, true)
	}
}

func (providersPage) Body(gtx layout.Context, th *material.Theme, f *settingsForm, rows []rowDesc, i int) (layout.Dimensions, bool) {
	r := rows[i]
	switch r.kind {
	case rowProvSummary:
		return f.providerSummaryRow(gtx, th, r.prov), true
	case rowProvName:
		pf := f.provs[r.prov]
		return fieldRow(gtx, th, "名称", editorBox(th, &pf.name, "provider 唯一标识（primary/fallback 按名引用）")), true
	case rowProvKind:
		pf := f.provs[r.prov]
		return fieldRow(gtx, th, "协议", func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
				layout.Rigid(cycleBtn(th, &pf.kind)),
				layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
					return layout.Inset{Left: unit.Dp(10)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
						l := material.Label(th, paperNoteSp(th), kindHint(pf.kind.value()))
						l.Color = paperOf().text3
						return l.Layout(gtx)
					})
				}),
			)
		}), true
	case rowProvBase:
		pf := f.provs[r.prov]
		return fieldRow(gtx, th, "接口地址", editorBox(th, &pf.baseURL, "如 https://api.openai.com/v1")), true
	case rowProvModels:
		return f.providerModelsHead(gtx, th, r.prov), true
	case rowProvTok:
		pf := f.provs[r.prov]
		return fieldRow(gtx, th, "Tokenizer", editorBox(th, &pf.tokenizer, "本地 tokenizer.json 路径；空 = 估算")), true
	case rowProvUnsup:
		pf := f.provs[r.prov]
		return fieldRow(gtx, th, "不认参数", editorBox(th, &pf.unsupported, "逗号分隔；这些参数会被剔除，不发往服务端")), true
	case rowProvHead:
		return provHeadRow(gtx, th, r.text), true
	case rowProvKey:
		return f.providerKeyRow(gtx, th, r.prov, r.field), true
	case rowProvKeyAdd:
		pf := f.provs[r.prov]
		return paperButton(th, &pf.keyAdd, "＋ 添加密钥", paperBtnSecondary, true)(gtx), true
	case rowProvModel:
		return f.providerModelRow(gtx, th, r.prov, r.field), true
	case rowProvModelPager:
		return f.providerModelPager(gtx, th, r.prov), true
	case rowProvModelAdd:
		pf := f.provs[r.prov]
		return paperButton(th, &pf.modelAdd, "＋ 添加模型", paperBtnSecondary, true)(gtx), true
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
		pf.kind.advance(gtx)
		for pf.test.Clicked(gtx) {
			u.runProviderTest(f, pf)
		}
		for pf.modelsBtn.Clicked(gtx) {
			u.runFetchModels(f, pf, false)
		}
		for pf.modelPagePrev.Clicked(gtx) {
			if pf.modelPage > 0 {
				pf.modelPage--
			}
		}
		for pf.modelPageNext.Clicked(gtx) {
			if pf.modelPage < pf.modelPageCount()-1 {
				pf.modelPage++
			}
		}
		for pf.keyAdd.Clicked(gtx) {
			pf.keyForms = append(pf.keyForms, newProviderKeyForm(ProviderKeySnapshot{}))
		}
		for k, kf := range pf.keyForms {
			for kf.del.Clicked(gtx) {
				if len(pf.keyForms) > 1 { // 至少留一把（providers 不可为空，同口径）
					pf.keyForms = append(pf.keyForms[:k], pf.keyForms[k+1:]...)
				}
			}
		}
		for pf.modelAdd.Clicked(gtx) {
			pf.modelForms = append(pf.modelForms, newModelRow("", pf.kind.value(), true))
			pf.modelPage = pf.modelPageCount() - 1 // 新行在末页：跳过去，免得以为没加上
		}
		for m, mf := range pf.modelForms {
			for mf.probe.Clicked(gtx) {
				u.runProbeModel(f, pf, mf)
			}
			for mf.del.Clicked(gtx) {
				if len(pf.modelForms) > 1 { // 至少留一行（models 不可为空，启动校验同口径）
					pf.modelForms = append(pf.modelForms[:m], pf.modelForms[m+1:]...)
					pf.modelPage = min(pf.modelPage, pf.modelPageCount()-1) // 删到末页空 = 退一页
				}
			}
		}
		for pf.setPrimary.Clicked(gtx) {
			f.primaryIdx = i
		}
		for pf.del.Clicked(gtx) {
			f.removeProvider(i)
		}
	}
}

func (providersPage) FirstEditor(f *settingsForm) *widget.Editor {
	if f.provOpen < 0 || f.provOpen >= len(f.provs) {
		return nil // 全收起：无编辑档可聚焦（展开哪张才聚焦哪张）
	}
	return &f.provs[f.provOpen].name
}

// removeProvider 删除一个 provider（至少保留一条）+ 展开态/主下标跟随重算。
func (f *settingsForm) removeProvider(i int) {
	if i < 0 || i >= len(f.provs) || len(f.provs) <= 1 {
		return
	}
	f.provs = append(f.provs[:i], f.provs[i+1:]...)
	switch {
	case f.primaryIdx == i:
		f.primaryIdx = 0
	case f.primaryIdx > i:
		f.primaryIdx--
	}
	switch {
	case f.provOpen == i:
		f.provOpen = -1
	case f.provOpen > i:
		f.provOpen--
	}
}

// kindHint 协议档的一句话说明（帮助用户理解该选哪个）。
func kindHint(kind string) string {
	switch kind {
	case "openai-responses":
		return "OpenAI Responses API（/responses）"
	case "anthropic":
		return "Anthropic Messages API（/messages）"
	default:
		return "OpenAI 兼容 /chat/completions"
	}
}

// providerSummaryRow 折叠卡片收起态（S11-7②/D176 口径二）：整行可点（点击展开/收起），
// 左侧一个 caret（▸ 收起 / ▾ 展开），右侧一行摘要（名字 · 协议 · N 个模型 · M 把钥匙 · 是否主）。
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

// provHeadRow 展开区里的小标题行（密钥池 / 模型能力表）：比分区标题轻，作块内分隔。
func provHeadRow(gtx layout.Context, th *material.Theme, text string) layout.Dimensions {
	l := material.Label(th, paperBodySp(th), text)
	l.Font.Weight = font.Bold
	l.Color = paperOf().text2
	return l.Layout(gtx)
}

// providerKeyRow 密钥池单行（S11-7③/D173）：展示名 + 值（只写不回显，掩码）+ 删除。
// 值档恒空（D35）——占位提示说明语义：留空 = 保持、secret: 前缀 = 照写引用、否则明文入库。
func (f *settingsForm) providerKeyRow(gtx layout.Context, th *material.Theme, prov, idx int) layout.Dimensions {
	kf := f.provs[prov].keyForms[idx]
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
				layout.Flexed(1, editorBox(th, &kf.value, "留空 = 保持；填明文则转入本机密钥库；以 secret: 开头则照写引用")),
				layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
					return layout.Inset{Left: unit.Dp(8)}.Layout(gtx, editorBox(th, &kf.label, "展示名（空 = 第 N 把）"))
				}),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return layout.Inset{Left: unit.Dp(8)}.Layout(gtx,
						paperButton(th, &kf.del, "删除", paperBtnDanger, len(f.provs[prov].keyForms) > 1))
				}),
			)
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Top: unit.Dp(4)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				l := material.Label(th, paperMicroSp(th), keySlotNote(kf))
				l.Color = paperOf().text3
				return l.Layout(gtx)
			})
		}),
	)
}

// keySlotNote 单槽的读侧状态说明（不回显值）：已入库的引用只露 `secret:<名>`；
// 明文引用只标「明文」；空引用标「回落缺省引用」。
func keySlotNote(kf *providerKeyForm) string {
	switch {
	case kf.snap.Saved:
		return "当前引用：" + kf.snap.Ref + "（值在密钥库，不回显）"
	case kf.snap.Plain:
		return "当前为明文（重启会在日志中提示；重填一次即转入密钥库）"
	default:
		return "当前未存值（回落缺省引用）"
	}
}

// providerModelsHead 「模型列表」块头（D181）：左侧标题 + 一段「共 N 个 · 已启用 M 个」的
// 轻量事实，右侧「获取模型」按钮；下面一行是本卡片自己的拉取反馈（成功/失败都只落在这里，
// 不占全局状态行）。
func (f *settingsForm) providerModelsHead(gtx layout.Context, th *material.Theme, i int) layout.Dimensions {
	pf := f.provs[i]
	total, on := len(pf.modelForms), 0
	for _, mf := range pf.modelForms {
		if mf.enabled.Value {
			on++
		}
	}
	count := fmt.Sprintf("共 %d 个 · 已启用 %d 个", total, on)
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					// 标题用 Rigid：右侧按钮才拿得到自己的自然宽度（D181 修「按钮被挤扁」）。
					return provHeadRow(gtx, th, "模型列表")
				}),
				layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
					return layout.Inset{Left: unit.Dp(10)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
						l := material.Label(th, paperMicroSp(th), count)
						l.Color = paperOf().text3
						return l.Layout(gtx)
					})
				}),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return paperButton(th, &pf.modelsBtn, "获取模型", paperBtnSecondary, true)(gtx)
				}),
			)
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			if pf.fetchNote == "" {
				return layout.Dimensions{}
			}
			return layout.Inset{Top: unit.Dp(4)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				l := material.Label(th, paperMicroSp(th), pf.fetchNote)
				col := paperOf().text3
				if pf.fetchErr {
					col = paperOf().danger
				}
				l.Color = col
				return l.Layout(gtx)
			})
		}),
	)
}

// providerModelPager 模型列表分页条（D181）：只在一页装不下时出现。
func (f *settingsForm) providerModelPager(gtx layout.Context, th *material.Theme, i int) layout.Dimensions {
	pf := f.provs[i]
	pages := pf.modelPageCount()
	page := pf.modelPage + 1
	if page > pages {
		page = pages
	}
	hint := "第 " + strconv.Itoa(page) + "/" + strconv.Itoa(pages) + " 页"
	return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return paperButton(th, &pf.modelPagePrev, "‹ 上一页", paperBtnSecondary, pf.modelPage > 0)(gtx)
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Left: unit.Dp(10), Right: unit.Dp(10)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				l := material.Label(th, paperMicroSp(th), hint)
				l.Color = paperOf().text3
				return l.Layout(gtx)
			})
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return paperButton(th, &pf.modelPageNext, "下一页 ›", paperBtnSecondary, pf.modelPage < pages-1)(gtx)
		}),
	)
}

// modelPageCount 模型列表页数（至少 1 页）。
func (pf *providerForm) modelPageCount() int {
	if len(pf.modelForms) <= 0 {
		return 1
	}
	return (len(pf.modelForms) + provModelPageSize - 1) / provModelPageSize
}

// modelPageBounds 当前页的行下标区间（越界夹回；行计划与渲染共用一处口径）。
func (pf *providerForm) modelPageBounds() (start, end int) {
	pages := pf.modelPageCount()
	if pf.modelPage >= pages {
		pf.modelPage = pages - 1
	}
	if pf.modelPage < 0 {
		pf.modelPage = 0
	}
	start = pf.modelPage * provModelPageSize
	end = min(start+provModelPageSize, len(pf.modelForms))
	return start, end
}

// providerModelRow 模型列表单行（S11-7③/D173；D181 改版）：启用勾选 + 名字 + 上下文窗口 +
// 删除；下一行三个能力勾选 + 「探测能力」；再一行探测反馈（有才显示）。能力位的声明形态
// 不写注脚——勾选本身就是声明，解释留在文档里（用户不读那行小字，却要为它让出按钮宽度）。
func (f *settingsForm) providerModelRow(gtx layout.Context, th *material.Theme, prov, idx int) layout.Dimensions {
	mf := f.provs[prov].modelForms[idx]
	kids := []layout.FlexChild{
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return capCheck(th, &mf.enabled, "启用")(gtx)
				}),
				layout.Flexed(2, func(gtx layout.Context) layout.Dimensions {
					return layout.Inset{Left: unit.Dp(10)}.Layout(gtx, editorBox(th, &mf.name, "模型名（服务端标识）"))
				}),
				layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
					return layout.Inset{Left: unit.Dp(8)}.Layout(gtx, editorBox(th, &mf.contextWin, "窗口 tokens"))
				}),
				// 单价（D205，每百万 token）：与厂商报价同口径，留空 = 未声明、不折算费用。
				layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
					return layout.Inset{Left: unit.Dp(8)}.Layout(gtx, editorBox(th, &mf.costIn, "输入 $/M"))
				}),
				layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
					return layout.Inset{Left: unit.Dp(8)}.Layout(gtx, editorBox(th, &mf.costOut, "输出 $/M"))
				}),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return layout.Inset{Left: unit.Dp(8)}.Layout(gtx,
						paperButton(th, &mf.del, "删除", paperBtnDanger, len(f.provs[prov].modelForms) > 1))
				}),
			)
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Top: unit.Dp(6)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
					layout.Rigid(capCheck(th, &mf.capsTools, capLabels[0])),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return layout.Inset{Left: unit.Dp(10)}.Layout(gtx, capCheck(th, &mf.capsReason, capLabels[1]))
					}),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return layout.Inset{Left: unit.Dp(10)}.Layout(gtx, capCheck(th, &mf.capsVision, capLabels[2]))
					}),
					layout.Flexed(1, func(gtx layout.Context) layout.Dimensions { return layout.Dimensions{} }),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return paperButton(th, &mf.probe, "探测能力", paperBtnSecondary, true)(gtx)
					}),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						if !mf.probeBusy {
							return layout.Dimensions{}
						}
						return layout.Inset{Left: unit.Dp(8)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
							return probeSpinner(gtx, th)
						})
					}),
				)
			})
		}),
	}
	if mf.probeStatus != "" {
		kids = append(kids, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Top: unit.Dp(4)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				l := material.Label(th, paperMicroSp(th), mf.probeStatus)
				col := paperOf().text3
				if mf.probeErr {
					col = paperOf().danger
				}
				l.Color = col
				return l.Layout(gtx)
			})
		}))
	}
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx, kids...)
}

// capCheck 能力位勾选（点击即 touched = 把声明固化成显式；渲染时经 Bool 自身消费）。
func capCheck(th *material.Theme, b *widget.Bool, label string) layout.Widget {
	return paperCheckBox(th, b, label)
}

// probeSpinner 「探测能力」按钮右侧的在途转圈：直径 = 一个正文行高，颜色取次级文字。
// material.Loader 自己按帧时间推进并唤帧（op.InvalidateCmd），探测结束由 probeBusy
// 翻回 false 收掉——不必自己做 ticker，也不依赖别的动画在跑。
func probeSpinner(gtx layout.Context, th *material.Theme) layout.Dimensions {
	d := gtx.Dp(unit.Dp(16))
	gtx.Constraints.Min, gtx.Constraints.Max = image.Pt(d, d), image.Pt(d, d)
	l := material.Loader(th)
	l.Color = paperOf().text3
	return l.Layout(gtx)
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
					text, col = "变更全量写回 config，重启生效；「测试连通」「探测能力」发真实请求。", paperOf().text3
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
