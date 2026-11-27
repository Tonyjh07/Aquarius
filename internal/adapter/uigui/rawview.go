package uigui

// rawview.go 查看原文窗（D99，§15.7）：气泡右键「查看原文」的只读次窗——消毒后
// 原始 markdown（block.text）进 ReadOnly 编辑器（不可改、可选中复制）+ 垂直滚动。
// 内容经 u.rawView 原子槽注入（分发处 Store → 帧内 Load），窗单实例、重开 = 聚焦
// + 原地刷新；常规装饰窗形态，不接主窗任何机制。

import (
	"image"
	"io"
	"strings"
	"time"

	"gioui.org/io/clipboard"
	"gioui.org/layout"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"

	"github.com/Tonyjh07/Aquarius/internal/i18n"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

// copiedFlash 「已复制」在按钮上停留的时长：够看清，又不会让人以为按钮变了个功能。
const copiedFlash = 2 * time.Second

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

	// copyBtn 标题行左上角的「复制 Markdown」（D353）。与主窗气泡右键的「复制」
	// 语义分开：那个给的是渲染后的可选文本，这里给原始 markdown 源码。
	copyBtn widget.Clickable
	// copiedAt 上次复制的时间；窗内 2 秒显示「已复制」，靠一次定时重绘回落。
	copiedAt time.Time
	// inval 本窗重绘请求（runSecondary 注入 w.Invalidate；nil = 无窗环境，不排定时器）。
	inval func()
	// writeClip 帧内写剪贴板。nil = 走 gio（剪贴板写入只能在帧内）；测试注入以断言
	// 交出去的**内容**，那是这一项唯一会错的地方。
	writeClip func(gtx layout.Context, text string)
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

// copyMarkdown 把当前原文写进剪贴板（D353）。
//
// 交出去的是 raw 的 text 本身，不是编辑器里的可见文本：两者在这一窗里恰好同源，但
// 编辑器以后若加了换行/软折行处理，复制仍应给源码——markdown 粘贴出去要能重新渲染。
func (st *rawViewState) copyMarkdown(gtx layout.Context) {
	c := st.applied
	if c == nil || strings.TrimSpace(c.text) == "" {
		return
	}
	if st.writeClip != nil {
		st.writeClip(gtx, c.text)
	} else {
		gtx.Execute(clipboard.WriteCmd{
			Type: "application/text",
			Data: io.NopCloser(strings.NewReader(c.text)),
		})
	}
	st.copiedAt = gtx.Now
	if st.inval != nil {
		// 2 秒后请一次重绘，把按钮文案换回来（次窗是事件驱动，没有帧就没有时间流逝）。
		time.AfterFunc(copiedFlash, st.inval)
	}
}

// copyLabel 按钮当前文案：刚复制过 2 秒内显示「已复制」。
func (st *rawViewState) copyLabel(gtx layout.Context) string {
	if !st.copiedAt.IsZero() && gtx.Now.Sub(st.copiedAt) < copiedFlash {
		return i18n.T("ui.rawview.copied")
	}
	return i18n.T("ui.rawview.copy_md")
}

// showTextView 把一份长文本铺进查看原文窗（§7.1）。内容由内核经 TextViewEvent
// 投来——已经落过盘的那一份，所以这里只负责呈现，不做格式化。
func (u *UI) showTextView(ev port.TextViewEvent) {
	raw := rawContent{title: ev.Title, text: ev.Text}
	u.rawView.Store(&raw)
	u.wins.openWin(winRaw)
	u.wins.invalidateKind(winRaw)
}

// rawViewFrame winRaw 单帧：窗体面铺满 + hairline 分隔的标题行 + 下沉面只读块。
//
// 只读块必须有边界：整窗一个白色也就没有「这块是内容、那块是留白」的区分，长文读起来
// 像窗子没加载完。故内容走 sunken 面 + hairline，与设置页的输入框同一套规则。
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
						hasText := st.applied != nil && strings.TrimSpace(st.applied.text) != ""
						if st.copyBtn.Clicked(gtx) {
							st.copyMarkdown(gtx)
						}
						return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
							// 左上角：复制整份原文（markdown 源码）。与气泡右键的复制区分开，
							// 按钮文字直说「Markdown」，不靠用户记得两条路径的差别。
							layout.Rigid(paperButton(th, &st.copyBtn, st.copyLabel(gtx),
								paperBtnSecondary, hasText)),
							layout.Rigid(layout.Spacer{Width: unit.Dp(12)}.Layout),
							layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
								title := i18n.T("ui.chat.none_selected")
								if c := st.applied; c != nil {
									title = c.title
								}
								l := material.Label(th, paperBodySp(th), title)
								l.Color = pp.text2
								l.MaxLines, l.Truncator = 1, "…"
								return l.Layout(gtx)
							}),
						)
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
