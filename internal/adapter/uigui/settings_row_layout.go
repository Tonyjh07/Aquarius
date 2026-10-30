package uigui

// 行布局计算：卡片分组、行内缩进与 hairline、按行画卡的取整规则。

import (
	"image"

	"gioui.org/layout"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"
	"gioui.org/widget/material"
)

// fillRows 撑满页的行布局（见 fillPage）：fill 之前的行按自然高度，fill 那一行吸走
// 剩余高度。
//
// 与 material.List 的差别只在「谁决定高度」：列表给每行的上限是无穷大，行只能按内容
// 自己定高；这里整页排进 Flex，撑满行的约束上限就是「窗口减去上面那些行」，于是
// 「还剩多少就用多少」成为可表达的意图。代价是没有虚拟化与滚动——走这条路的页只有
// 一个可变高度块、行数是个位数，不需要虚拟化；版面仍用同一份 rowLayout，卡片与留白
// 才和别的页一致（两处排版分家就会出现卡粗细不一）。
func (f *settingsForm) fillRows(gtx layout.Context, th *material.Theme, rows []rowDesc, fill int) layout.Dimensions {
	children := make([]layout.FlexChild, 0, len(rows))
	for i := range rows {
		w := func(gtx layout.Context) layout.Dimensions {
			return rowLayout(gtx, rows, i, f.rowBodyFn(th, rows, i))
		}
		if i == fill {
			children = append(children, layout.Flexed(1, w))
			continue
		}
		children = append(children, layout.Rigid(w))
	}
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx, children...)
}

// rowLayout 单行版面（卡片分组 + 留白），行内容由 body 给（D154 起抽成自由函数：
// 设置页与遮罩「默认模板」页签的行表不同，版面必须同一份，否则两处卡片粗细不一样）。
func rowLayout(gtx layout.Context, rows []rowDesc, i int, body layout.Widget) layout.Dimensions {
	r := rows[i]
	g := rowCardGroup(r)
	if g < 0 {
		// 分区标题按「间距有亲疏」单独给不对称留白（见 rowInset）；其余非卡行（说明段、
		// 网格、整组按钮行）上下对称——它们不领起一个组，贴谁都不对。
		top, bottom := rowInset(rows, i)
		return layout.Inset{
			Left: unit.Dp(rowInsetDp), Right: unit.Dp(rowInsetDp),
			Top: unit.Dp(top), Bottom: unit.Dp(bottom),
		}.Layout(gtx, body)
	}
	first := i == 0 || rowCardGroup(rows[i-1]) != g
	last := i == len(rows)-1 || rowCardGroup(rows[i+1]) != g
	// 组首/组尾与相邻标题之间留整份 rowInsetDp；**组内上下不留**——卡内行距由
	// Stacked 的内边距给。留了的话相邻行的卡之间会插进一段白，竖边逐行断开，
	// 整张卡看上去被切成几截（实测：放大后左右各缺一块，像没画完）。
	padT, padB := cardPad(first, last)
	return layout.Inset{
		Left: unit.Dp(rowInsetDp), Right: unit.Dp(rowInsetDp),
		Top: unit.Dp(padT), Bottom: unit.Dp(padB),
	}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		pp := paperOf()
		rad := gtx.Dp(unit.Dp(pp.rCard))
		bd := gtx.Dp(1)
		return layout.Stack{}.Layout(gtx,
			// 卡底 + 外沿：Expanded 拿到 Stacked 子项的最大尺寸，即「本行高度」。
			layout.Expanded(func(gtx layout.Context) layout.Dimensions {
				// 卡宽取**可用宽**而不是内容宽：layout.Stack 会把 Stacked 子项的
				// Min 归零，只报自身尺寸的内容（复选框、单个按钮）会让卡缩成一条
				// ——同一页出现两种卡宽，读起来像排版塌了。
				w, h := gtx.Constraints.Min.X, gtx.Constraints.Min.Y
				if m := gtx.Constraints.Max.X; m > w {
					w = m
				}
				// 上下圆角只在组界生效：中间行直角，与相邻行拼成一张连续的卡。
				topR, botR := 0, 0
				if first {
					topR = rad
				}
				if last {
					botR = rad
				}
				// **整圈画环**：先铺描边色的圆角矩形，再内缩 bd 铺面色。逐条边画直线
				// 会在四个角留缺口（实测：框线断成四截，读起来像渲染坏了）。
				outer := clip.RRect{Rect: image.Rect(0, 0, w, h), NE: topR, NW: topR, SE: botR, SW: botR}
				paint.FillShape(gtx.Ops, pp.hairline, outer.Op(gtx.Ops))
				ir := func(v int) int {
					if v <= bd {
						return 0
					}
					return v - bd
				}
				inner := clip.RRect{
					Rect: image.Rect(bd, bd, w-bd, h-bd),
					NE:   ir(topR), NW: ir(topR), SE: ir(botR), SW: ir(botR),
				}
				paint.FillShape(gtx.Ops, pp.card, inner.Op(gtx.Ops))
				// 组内：把「不该出现的那条横边」用面色抹掉，再补一条让开内边距的分隔线。
				// **抹边必须让开左右 bd**：抹整宽会把两条竖边一起凿掉，卡在每行交界
				// 处断成一段（实测：放大后左右各缺一块，整张卡看着像没画完）。
				// 抹边只碰本行自己的边界像素（相邻行各有各的边界行），不会伤到邻行。
				erase := func(rr image.Rectangle) {
					paint.FillShape(gtx.Ops, pp.card, clip.Rect(rr).Op())
				}
				if !first {
					erase(image.Rect(bd, 0, w-bd, bd))
					paint.FillShape(gtx.Ops, pp.hairline,
						clip.Rect(image.Rect(gtx.Dp(14), 0, w-gtx.Dp(14), bd)).Op())
				}
				if !last {
					erase(image.Rect(bd, h-bd, w-bd, h))
				}
				return layout.Dimensions{Size: image.Pt(w, h)}
			}),
			layout.Stacked(func(gtx layout.Context) layout.Dimensions {
				// 卡内边距：上下 14 与左右一致，字段才不会贴着卡的描边。
				return layout.Inset{
					Top: unit.Dp(cardInnerDp), Bottom: unit.Dp(cardInnerDp),
					Left: unit.Dp(cardInnerDp), Right: unit.Dp(cardInnerDp),
				}.Layout(gtx, body)
			}),
		)
	})
}

// cardPad 卡在组首/组尾的上下外边距（dp）：只在组界留，组内为 0。
//
// 组内留边距 = 相邻卡的竖边之间插进一段白，整张卡被切成几截；卡内行距由内容的
// 内边距承担，不靠行间空隙。
func cardPad(first, last bool) (top, bottom int) {
	if first {
		top = rowInsetDp
	}
	if last {
		bottom = rowInsetDp
	}
	return top, bottom
}

// rowInset 非卡行（不落卡）的上下留白（dp）。
//
// 只有分区标题（rowSection）用不对称留白——间距要有亲疏（D209）：标题贴住**它自己
// 那一组**，所以上留白（与上一组之间）远大于下留白（与自己那组之间）。改前上下各
// rowInsetDp，叠上卡片组界后上下完全对称，读者分不清标题属于上面还是下面那组。
//
// 特例：紧跟在页标题后的首个分区标题上面没有「上一组」，按对称留白处理，否则每页
// 开头都会空出一截。其余非卡行（说明段、网格、整组按钮行）不领起一个组，恒对称。
func rowInset(rows []rowDesc, i int) (top, bottom int) {
	if rows[i].kind != rowSection {
		return rowInsetDp, rowInsetDp
	}
	top = rowSectionTopDp
	if i == 0 || rows[i-1].kind == rowTitle {
		top = rowInsetDp
	}
	return top, rowSectionGapDp
}

// rowInCard 该行是否落进分组卡（表单行）。
//
// 口径：**可编辑/可操作的表单行**打成一张卡（iOS 分组表读法）——分隔线在卡内、
// 圆角只在组首组尾；标题、分区标题、说明段、整块内容（代码块/网格/预览）不落卡。
// 「人格引用」整组落卡：它们是同一份有序列表的成员，逐行浮着会被误读成互不相关的项。
//
// **归属按「属于谁」判，不按「是不是按钮」判**：provider 卡片里的「＋ 添加密钥」
// 「＋ 添加模型」与底部动作行（测试连通 / 设为主 / 删除）都是**这一张卡自己的**动作，
// 必须落卡——否则卡在这里收边、按钮浮在卡外，同一个 provider 被切成两张卡（实测：
// 密钥池一张、模型列表一张、按钮行谁也不属于）。页级动作（＋ 新增 provider、应用变更）
// 才不落卡。
func rowInCard(k rowKind) bool {
	switch k {
	case rowTitle, rowSection, rowPromptNote, rowPromptGrid, rowToolsEmpty, rowRaw,
		rowPromptPreview, rowProfBtns, rowProvAdd, rowProvApply,
		rowPromptRefAdd, rowSecretEmpty, rowSecretAdd, rowSecretStatus,
		rowChainNote, rowChainAdd, rowWebSearchAdd, rowWebFetchAdd, rowWebNote,
		rowSamplingNote,
		rowSkillEmpty, rowSkillNote, rowSkillRescan, rowSkillWarn, rowSkillStatus,
		rowCharEmpty, rowCharWarn, rowCharNote, rowCharStatus,
		rowAboutHero, rowAboutNote, rowAboutStatus:
		return false
	}
	return true
}

// rowCardGroup 行所属的卡组（同组 = 同一张卡；-1 = 不落卡）。
//
// 一般表单行同属卡组 0（连续的字段行拼成一张卡）。**provider 卡片按 provider 分卡组**：
// 每家一张卡，展开区里那些「属于这一家」的按钮行（＋ 添加密钥 / ＋ 添加模型 / 测试连通 ·
// 设为主 · 删除）与字段行同组，卡片才不会在按钮前收边；同时相邻两家不会因为都是落卡行
// 而拼进同一张卡（收起的摘要行必须属于它自己那一张）。
func rowCardGroup(r rowDesc) int {
	if !rowInCard(r.kind) {
		return -1
	}
	if provCardRow(r.kind) {
		return 1 + r.prov
	}
	return 0
}

// provCardRow 该行是否属于某一家 provider 的折叠卡片。
func provCardRow(k rowKind) bool {
	switch k {
	case rowProvSummary, rowProvName, rowProvKind, rowProvBase, rowProvModels,
		rowProvTok, rowProvUnsup, rowProvHead, rowProvKey, rowProvKeyAdd,
		rowProvModel, rowProvModelPager, rowProvModelAdd, rowProvBtns:
		return true
	}
	return false
}
