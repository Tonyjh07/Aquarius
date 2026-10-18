package uigui

// rawview.go 查看原文窗（D99，§15.7）：气泡右键「查看原文」的只读次窗——消毒后
// 原始 markdown（block.text）进 ReadOnly 编辑器（不可改、可选中复制）+ 垂直滚动。
// 内容经 u.rawView 原子槽注入（分发处 Store → 帧内 Load），窗单实例、重开 = 聚焦
// + 原地刷新；常规装饰窗形态，不接主窗任何机制。

import (
	"image"

	"gioui.org/layout"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"
)

// rawContent 查看原文窗的一次内容快照（D99）：title = 窗内标题行（含节点 ID 前缀），
// text = 原始文本（block.text；thinking/chip 步扩展后为思考原文/工具 JSON）。
type rawContent struct {
	title string
	text  string
}

// rawViewState winRaw 次窗的自持帧状态（runSecondary 创建、仅该窗 goroutine 读写）。
type rawViewState struct {
	ed      widget.Editor // ReadOnly：可选中复制、不可改
	list    widget.List
	applied *rawContent // 本窗已呈现的内容快照（指针比较，变化才 SetText）
}

// newRawViewState 构造 winRaw 帧状态。
func newRawViewState() *rawViewState {
	return &rawViewState{
		list: widget.List{List: layout.List{Axis: layout.Vertical}},
	}
}

// sync 把原子槽内容同步进编辑器（内容变化才重置；跨 goroutine 只经原子快照）。
func (st *rawViewState) sync(u *UI) {
	c := u.rawView.Load()
	if c == nil || c == st.applied {
		return
	}
	st.applied = c
	st.ed.ReadOnly = true
	st.ed.SetText(c.text)
}

// rawViewFrame winRaw 单帧：窗体面铺满 + hairline 分隔的标题行 + 下沉面只读块。
//
// 只读块必须有边界：整窗一个白色也就没有「这块是内容、那块是留白」的区分，长文读起来
// 像窗子没加载完。故内容走 sunken 面 + hairline，与设置页的输入框同一口径。
func rawViewFrame(gtx layout.Context, th *material.Theme, u *UI, st *rawViewState) layout.Dimensions {
	st.sync(u)
	pp := paperOf()
	return layout.Stack{}.Layout(gtx,
		layout.Expanded(func(gtx layout.Context) layout.Dimensions {
			paint.FillShape(gtx.Ops, pp.win,
				clip.Rect(image.Rectangle{Max: gtx.Constraints.Max}).Op())
			return layout.Dimensions{}
		}),
		layout.Stacked(func(gtx layout.Context) layout.Dimensions {
			return layout.UniformInset(unit.Dp(16)).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						title := "（未选择）"
						if c := st.applied; c != nil {
							title = c.title
						}
						l := material.Label(th, paperBodySp(th), title)
						l.Color = pp.text2
						l.MaxLines, l.Truncator = 1, "…"
						return l.Layout(gtx)
					}),
					layout.Rigid(layout.Spacer{Height: unit.Dp(10)}.Layout),
					layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
						// 内容块面：先铺面（Expanded 拿实际尺寸），再把编辑器排上去。
						return layout.Stack{}.Layout(gtx,
							layout.Expanded(func(gtx layout.Context) layout.Dimensions {
								// 面要**撑满**可用宽高：按 Min 铺会缩成正文文字的宽度，
								// 右边留出一大片白——看上去像窗子只画了一半。
								sz := gtx.Constraints.Max
								paperSurface(gtx, pp, image.Rectangle{Max: sz},
									gtx.Dp(unit.Dp(pp.rCard)))
								return layout.Dimensions{Size: sz}
							}),
							layout.Stacked(func(gtx layout.Context) layout.Dimensions {
								// 编辑器自身不撑宽（material.Editor 只报文字宽度），
								// 这里补一次最小宽度约束，内容才会贴着块的左内边距排版。
								gtx.Constraints.Min.X = gtx.Constraints.Max.X
								return layout.UniformInset(unit.Dp(12)).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
									return st.list.Layout(gtx, 1, func(gtx layout.Context, _ int) layout.Dimensions {
										ed := material.Editor(th, &st.ed, "")
										ed.Color = pp.text
										return ed.Layout(gtx)
									})
								})
							}),
						)
					}),
				)
			})
		}),
	)
}
