package uigui

// pendingbar.go 待发条（D272）：输入胶囊上方那几行——「还有几句没说出去，各自什么时候走」。
//
// 形态刻意做得薄：每行一条，左侧档位徽标（排队/引导）、中间一句正文、右侧三个小动作
// （拉回输入框 / 撤销 / 改档）。条数封顶 3 行，多出来的折成一行「还有 N 条」——输入栏
// 上方是转写区的地盘，待发是附注，不该把它挤没。
//
// 三态互转（输入框 / 待发送 / 等插入的引导）在这里各有一个入口：编辑器里的字按发送键
// 进清单（排队）、按插话键进清单（引导）；退回编辑器点「拉回」；两档之间点「改档」。
// 键位可配（ui.keys），见 input.go 的 sendKeys。

import (
	"fmt"
	"image"
	"strings"

	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/widget"
	"gioui.org/widget/material"

	"github.com/Tonyjh07/Aquarius/internal/port"
)

const (
	// pendingRowDp 待发条单行高（dp）：比状态行稍高，够放一个小徽标和一行正文。
	pendingRowDp = 22
	// pendingGapDp 待发条与输入胶囊之间的空隙（dp）。
	pendingGapDp = 6
	// pendingMaxRows 最多铺几行（多出来的折成一行计数）。
	pendingMaxRows = 3
)

// pendingBandPx 待发条占高（px）；没有待发 = 0（布局不变，与今天完全一致）。
func (u *UI) pendingBandPx(gtx layout.Context) int {
	n := len(u.pendingQueued())
	if n == 0 {
		return 0
	}
	rows := n
	extra := 0
	if rows > pendingMaxRows {
		rows = pendingMaxRows
		extra = 1 // 「还有 N 条」那一行
	}
	return (rows+extra)*gtx.Dp(pendingRowDp) + gtx.Dp(pendingGapDp)
}

// pendingBand 画待发条（y 原点 = 本带左上）。
//
// 透明度跟转写区的揭示进度（msgP）走：转写区**展开时**待发条随之上浮出现、**收起收完**
// 才彻底隐去（D54 两阶段动画：展开 = barP→1 再 msgP→1，收起 = msgP→0 再 barP→0）。
// 待发条在转写区下沿，跟着 msgP 才是「从下至上展开、从上至下收起」的自然时机；高度不
// 乘 msgP——留出位置只做淡入淡出，免得半行被裁、也免得转写区在动画里被顶来顶去。
func (u *UI) pendingBand(gtx layout.Context, w, absY int) {
	items := u.pendingQueued()
	if len(items) == 0 {
		return
	}
	_, msgP := u.expandProgress()
	if msgP < 0.01 {
		return // 收起动画收尾：已经看不见了，下一帧走 layoutCollapsed
	}
	if msgP < 1 {
		defer paint.PushOpacity(gtx.Ops, float32(msgP)).Pop()
	}
	u.prunePendingCtl(items)
	u.pendHot = u.pendHot[:0] // 热区随帧复位（消费者读本帧登记）
	rowH := gtx.Dp(pendingRowDp)
	margin := gtx.Dp(sideMarginDp)
	inner := w - 2*margin
	if inner < 40 {
		return // 极窄窗：宁可先不画，也不画成一团
	}
	draw := items
	hidden := 0
	if len(draw) > pendingMaxRows {
		hidden = len(draw) - pendingMaxRows
		draw = draw[:pendingMaxRows]
	}
	for i, it := range draw {
		u.pendingRow(gtx, margin, i*rowH, inner, rowH, it, absY)
	}
	if hidden > 0 {
		y := len(draw) * rowH
		off := op.Offset(image.Pt(margin, y)).Push(gtx.Ops)
		l := material.Caption(u.th, fmt.Sprintf("还有 %d 条待发（改档或撤销在待发条上）", hidden))
		l.Color = tok().textMuted
		l.Layout(gtx)
		off.Pop()
	}
}

// pendingRow 一行待发：徽标 + 正文（超宽截断）+ 三个动作。
//
// 三个动作从左到右 = 转档（排队 ↔ 引导）、拉回输入框、撤销。改档单列一个按钮而不是
// 藏在徽标上：徽标看着是标签，没人会去点它（实测如此），而「这句话什么时候到模型眼前」
// 恰恰是待发条上最该一眼找到的开关。
func (u *UI) pendingRow(gtx layout.Context, x, y, w, rowH int, it port.PendingInput, absY int) {
	ctl := u.ctlFor(it.ID)
	rowRect := image.Rect(x, y, x+w, y+rowH-2)
	paint.FillShape(gtx.Ops, tok().pillBg, clip.UniformRRect(rowRect, (rowH-2)/2).Op(gtx.Ops))
	u.record(rowRect.Add(image.Pt(0, absY)), (rowH-2)/2, tok().pillBg, image.Rectangle{Max: u.frameSize})

	// 档位徽标：引导用品牌色（它会更早到模型眼前），排队用静息灰。
	badge, bg := "排队", tok().textMuted
	toggle := "转引导"
	if it.Delivery == port.DeliverySteer {
		badge, bg = "引导", tok().brandColor
		toggle = "转排队"
	}
	bl := material.Caption(u.th, badge)
	bl.Color = tok().whiteText
	badgeW, badgeH := measure(gtx, bl)
	padX := rowH / 4
	badgeRect := image.Rect(x+6, y+(rowH-2-badgeH)/2-1, x+6+badgeW+2*padX, y+(rowH-2+badgeH)/2+1)
	paint.FillShape(gtx.Ops, bg, clip.UniformRRect(badgeRect, 4).Op(gtx.Ops))
	st := clip.UniformRRect(badgeRect, 4).Push(gtx.Ops)
	off := op.Offset(image.Pt(badgeRect.Min.X+padX, (badgeRect.Min.Y+badgeRect.Max.Y-badgeH)/2)).Push(gtx.Ops)
	bl.Layout(gtx)
	off.Pop()
	st.Pop()

	// 右侧三个动作先量宽，正文吃剩下的。
	actions := []string{toggle, "拉回", "×"}
	widths := make([]int, len(actions))
	total := 0
	for i, t := range actions {
		l := material.Caption(u.th, t)
		lw, _ := measure(gtx, l)
		widths[i] = lw + 12 // 多留点缝，三个动作挨着也能点准
		total += widths[i]
	}
	textX := badgeRect.Max.X + 8
	avail := (x + w) - 6 - total - textX
	if avail < 24 {
		avail = 24
	}
	lbl := material.Caption(u.th, clampToWidth(gtx, u.th, it.Text, avail))
	lbl.Color = tok().fg
	lh := measureHeight(gtx, lbl)
	to := op.Offset(image.Pt(textX, y+(rowH-2-lh)/2)).Push(gtx.Ops)
	lbl.Layout(gtx)
	to.Pop()

	// 动作区：先算出每个热区，再统一画字——绘制顺序与命中顺序解耦。
	rects := make([]image.Rectangle, len(actions))
	ax := x + w - 6
	for i := len(actions) - 1; i >= 0; i-- {
		ax -= widths[i]
		rects[i] = image.Rect(ax, y, ax+widths[i], y+rowH-2)
		l := material.Caption(u.th, actions[i])
		l.Color = tok().textMuted
		if i == 0 {
			l.Color = tok().brandColor // 转档是这一行的主按钮
		}
		lh := measureHeight(gtx, l)
		o := op.Offset(image.Pt(ax+(widths[i]-measureWidth(gtx, l))/2, y+(rowH-2-lh)/2)).Push(gtx.Ops)
		l.Layout(gtx)
		o.Pop()
	}
	// 三个热区都注册（不能命中一个就 return——否则下一帧另外两个的热区会缺一块）。
	hitToggle := clickRect(gtx, &ctl.toggle, rects[0])
	hitRecall := clickRect(gtx, &ctl.recall, rects[1])
	hitDrop := clickRect(gtx, &ctl.drop, rects[2])
	u.pendHot = append(u.pendHot, pendHot{
		id:     it.ID,
		toggle: rects[0].Add(image.Pt(0, absY)),
		recall: rects[1].Add(image.Pt(0, absY)),
		drop:   rects[2].Add(image.Pt(0, absY)),
	})
	switch {
	case hitToggle:
		u.togglePendingDelivery(it.ID)
	case hitRecall:
		u.recallPending(it.ID)
	case hitDrop:
		u.dropPending(it.ID)
	}
}

// clickRect 在指定矩形上铺一个 Clickable（热区随几何走；返回本帧是否被点中）。
// Gio 的 Clickable 按当前变换原点注册指针区，所以先位移到矩形左上、再按尺寸布局。
//
// 顺序有讲究：**先取点击、再 Layout**。Gio v0.10 的 Clickable.Layout 自己会 update
// 一遍并把点击结果丢掉（它只给「渲染一个按钮」用），Layout 完再 Clicked 永远是 false
// ——这条曾让整条待发条点不动。
func clickRect(gtx layout.Context, c *widget.Clickable, hit image.Rectangle) bool {
	if hit.Empty() {
		return false
	}
	clicked := c.Clicked(gtx)
	off := op.Offset(hit.Min).Push(gtx.Ops)
	st := clip.Rect(image.Rectangle{Max: hit.Size()}).Push(gtx.Ops)
	c.Layout(gtx, func(layout.Context) layout.Dimensions { return layout.Dimensions{Size: hit.Size()} })
	st.Pop()
	off.Pop()
	return clicked
}

// measure 量一个 label 的尺寸（不改动 gtx 的可见输出——记一笔就停）。
func measure(gtx layout.Context, l material.LabelStyle) (int, int) {
	m := op.Record(gtx.Ops)
	d := l.Layout(gtx)
	m.Stop()
	return d.Size.X, d.Size.Y
}

// measureWidth 只量宽（动作按钮在热区里居中用）。
func measureWidth(gtx layout.Context, l material.LabelStyle) int {
	w, _ := measure(gtx, l)
	return w
}

// measureHeight 只量高（行内垂直居中用）。
func measureHeight(gtx layout.Context, l material.LabelStyle) int {
	_, h := measure(gtx, l)
	return h
}

// clampToWidth 把一行正文裁到可用宽度内（末尾省略号）：待发条是附注，一行读不完就
// 读不完——真要看全文，把它拉回输入框。
func clampToWidth(gtx layout.Context, th *material.Theme, s string, maxW int) string {
	s = strings.ReplaceAll(strings.TrimSpace(s), "\n", " ")
	l := material.Caption(th, s)
	w, _ := measure(gtx, l)
	if w <= maxW {
		return s
	}
	r := []rune(s)
	for len(r) > 1 {
		// 按比例估一个「大约能放下」的长度，再逐轮收缩；夹住上界（估算可能给出
		// ≥ 当前长度）与下界 1（估算可能在极小宽度/超大字号下给出 0 甚至负数——
		// 那会让切片倒着走，直接崩）。
		n := len(r) * maxW / w
		if n >= len(r) {
			n = len(r) - 1
		}
		if n < 1 {
			n = 1
		}
		r = r[:n]
		l = material.Caption(th, string(r)+"…")
		w, _ = measure(gtx, l)
		if w <= maxW {
			return string(r) + "…"
		}
	}
	return "…"
}
