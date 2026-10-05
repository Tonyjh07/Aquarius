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

// rawViewFrame winRaw 单帧：主题底铺满 + 标题行 + 只读编辑器（垂直滚动）。
func rawViewFrame(gtx layout.Context, th *material.Theme, u *UI, st *rawViewState) layout.Dimensions {
	st.sync(u)
	return layout.Stack{}.Layout(gtx,
		layout.Expanded(func(gtx layout.Context) layout.Dimensions {
			paint.FillShape(gtx.Ops, th.Bg,
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
						return material.Body2(th, title).Layout(gtx)
					}),
					layout.Rigid(layout.Spacer{Height: unit.Dp(8)}.Layout),
					layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
						return st.list.Layout(gtx, 1, func(gtx layout.Context, _ int) layout.Dimensions {
							return material.Editor(th, &st.ed, "").Layout(gtx)
						})
					}),
				)
			})
		}),
	)
}
