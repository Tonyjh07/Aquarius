package uigui

import (
	"image"
	"image/color"
	"strconv"
	"unicode/utf8"

	"gioui.org/io/event"
	"gioui.org/io/key"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"
	"gioui.org/widget/material"
)

// complSelBg 补全行高亮底色（tipBg 亮一档）。
var complSelBg = color.NRGBA{R: 0x36, G: 0x3B, B: 0x41, A: 0xFF}

// complMaxRows 补全浮层最多显示的行数（D103：超出部分折叠为提示行）。
const complMaxRows = 8

const (
	complRowDp       = 24  // 行高
	complMaxWidthDp  = 440 // 卡片最大宽（随胶囊宽收缩）
	complTailGapDp   = 8   // 名称与尾段（usage+描述）间距
	complAnchorGapDp = 4   // 卡片与胶囊上沿间距
)

// complRowRect 一行的命中矩形 + 它在 complList 里的下标（提示行不登记，下标才需要
// 单独记：否则行序与清单序会错位）。
type complRowRect struct {
	idx  int
	rect image.Rectangle
}

// complUpdate 补全浮层消费者（D103/D250）：每帧依编辑框文本重算目标与候选（命令清单
// 端口每帧拉取——动态命令可随插件启停变化）；开启期消费上一帧登记的 Tab/Esc 键过滤、
// 行点击，并在轮换期沿用锁定的候选表。须先于 updateEditor（Esc 在编辑态让渡）。
func (u *UI) updateCompl(gtx layout.Context) {
	u.complOpenNow = false
	u.complEsc = false
	if u.m.confirm != nil || u.m.ask != nil { // 确认态/提问态主编辑器不自由输入，浮层不参与
		u.complList = nil
		u.cycle.reset()
		return
	}
	text := u.editor.Text()
	if text != u.complText { // 用户改了字：搁置解除，轮换作废
		u.complText = text
		u.complDismissed = false
		u.complSel = 0
		u.cycle.reset()
	}
	if u.complDismissed || !complPhase(text) || u.opts.Commands == nil {
		u.complList = nil
		u.cycle.reset() // 已不是命令行 / 已搁置：轮换作废，下次 Tab 重新起一轮
		return
	}
	if u.cycle.on {
		// 轮换期沿用锁定的候选表：每按一次 Tab 文本就变，重算会把候选缩成一项，
		// 轮换便退化成「一直填同一项」。
		u.complList = u.cycle.items
	} else {
		all := u.opts.Commands.Commands()
		t := complTargetOf(text, all)
		switch {
		case t.cmdPhase:
			u.complList = complFilter(t.prefix, all)
		case !t.known:
			u.complList = nil
		default:
			u.complList = complArgItems(t.ci, t.args, t.tok)
		}
	}
	if len(u.complList) == 0 {
		return
	}
	u.complOpenNow = true
	if u.complSel >= len(u.complList) {
		u.complSel = len(u.complList) - 1
	}
	// 键面（Focus 限编辑器——浮层随编辑框起落；编辑器不认 Tab，↑↓ 归历史，故只剩这两个）。
	// 过滤器由上一帧 drawCompl 登记，Gio 一帧陈旧与手势同口径。
	for {
		ev, ok := gtx.Event(
			key.Filter{Focus: &u.editor, Name: key.NameTab},
			key.Filter{Focus: &u.editor, Name: key.NameEscape},
		)
		if !ok {
			break
		}
		e, isKey := ev.(key.Event)
		if !isKey || e.State != key.Press {
			continue
		}
		switch e.Name {
		case key.NameTab:
			u.complTab()
		case key.NameEscape:
			u.complDismissed = true
			u.complEsc = true // 编辑态 Esc 让渡：本轮 updateEditor 不取消编辑
			u.complOpenNow = false
			u.cycle.reset()
		}
	}
	u.complClicks(gtx)
}

// complTab Tab 轮换落笔（D250）：没在轮换就先锁定候选、填第一项；在轮换则前移到下一项
// 并替换同一段 token。只轮可填的行（insert 为空的是提示行）。
func (u *UI) complTab() {
	text := u.editor.Text()
	if !u.cycle.on {
		start, _ := complToken(text)
		sel, ok := u.cycle.begin(u.complList, start)
		if !ok {
			return
		}
		u.complSel = sel
		u.complInsertAt(u.cycle.start, u.complList[sel].insert)
		return
	}
	sel, ok := u.cycle.advance()
	if !ok {
		return
	}
	u.complSel = sel
	u.complInsertAt(u.cycle.start, u.complList[sel].insert)
}

// complInsertAt 把末尾 token（从字节位置 start 到行尾）换成 ins，光标留在行尾。
//
// 换算到 rune 坐标是必须的：SetCaret 收的是 rune 下标，而切分按字节算（中文参数名
// 在前面时两套下标对不上，光标会偏到半个字里）。
func (u *UI) complInsertAt(start int, ins string) {
	text := u.editor.Text()
	if start < 0 || start > len(text) {
		return
	}
	head := text[:start]
	next := head + ins
	u.editor.SetText(next)
	u.editor.SetCaret(utf8.RuneCountInString(next), utf8.RuneCountInString(next))
	// 与下一帧的文本比对对齐：这不是「用户改了字」，轮换要继续。
	u.complText = next
}

// complClicks 行点击消费（drawCompl 登记 Press/Release 于各行矩形）：武装-原位抬起
// （D72 口径）→ 命中行落笔一次，等价于按一下 Tab（D250：点击不再等于提交，Enter 才是
// 提交）。提示行没有登记矩形，自然点不到。
func (u *UI) complClicks(gtx layout.Context) {
	for {
		ev, ok := gtx.Event(pointer.Filter{
			Target: &u.complTag,
			Kinds:  pointer.Press | pointer.Release | pointer.Cancel,
		})
		if !ok {
			return
		}
		switch pe := ev.(type) {
		case pointer.Event:
			switch pe.Kind {
			case pointer.Press:
				u.complPressPt = pe.Position.Round()
				u.complArmed = true
			case pointer.Release:
				if !u.complArmed {
					continue
				}
				u.complArmed = false
				pt := pe.Position.Round()
				for _, rr := range u.complRows {
					if pt.In(rr.rect) && u.complPressPt.In(rr.rect) {
						u.complClick(rr.idx)
						break
					}
				}
			case pointer.Cancel:
				u.complArmed = false
			}
		}
	}
}

// complClick 点某一行：插进编辑框（不是提交）。第一次点击先锁定候选表，之后点别的行
// 仍替换同一段 token。
func (u *UI) complClick(i int) {
	if i < 0 || i >= len(u.complList) {
		return
	}
	it := u.complList[i]
	if it.insert == "" {
		return // 提示行：没有可填内容
	}
	if !u.cycle.on {
		start, _ := complToken(u.editor.Text())
		if _, ok := u.cycle.begin(u.complList, start); !ok {
			return
		}
	}
	u.cycle.seek(i)
	u.complSel = i
	u.complInsertAt(u.cycle.start, it.insert)
}

// drawCompl 补全浮层绘制（D103/D250）：hoverCard 同款深色卡（chrome 形状——整窗 clip
// 登记、不受 D79 淡化带作用），锚在胶囊上方左缘。行 = 左半（命令名带斜杠 / 参数名或
// 可选值，品牌色）+ 右半说明（暗色、按剩余宽二分截断）；键盘高亮与光标悬停（D85 直采）
// 都着底色。提示行（insert 为空）整行暗色、不登记命中矩形——看得见、点不动。
//
// 绘制与命中统一用窗口系坐标（绘制经 offset(-absY) 换到窗口系，热区注册同系）；动画期
// 不画（D54 tips 同口径），行矩形每帧重登记（fade pass 重渲同几何复登，与 bubbleRects
// 同构）。
func (u *UI) drawCompl(gtx layout.Context, absY int, pill image.Rectangle) {
	u.complRows = u.complRows[:0]
	if !u.complOpenNow || u.expandAn.active {
		return
	}
	padX, padY := gtx.Dp(8), gtx.Dp(6)
	rowH, gap := gtx.Dp(complRowDp), gtx.Dp(2)
	n := min(len(u.complList), complMaxRows)
	over := len(u.complList) - n
	cardW := min(pill.Dx(), gtx.Dp(complMaxWidthDp))
	cardH := 2*padY + n*rowH + (n-1)*gap
	if over > 0 {
		cardH += rowH + gap
	}
	// 卡片矩形（窗口系）：胶囊上方 complAnchorGapDp。
	bottom := pill.Min.Y + absY - gtx.Dp(complAnchorGapDp)
	card := image.Rect(pill.Min.X, bottom-cardH, pill.Min.X+cardW, bottom)

	// 换到窗口系绘制（输入栏局部 T=translate(0,absY) → offset(-absY) = 恒等）；
	// 热区注册在同一变换内 → pointer.Position 即窗口系，与 complRows 一致。
	wt := op.Offset(image.Pt(0, -absY)).Push(gtx.Ops)
	st := clip.UniformRRect(card, gtx.Dp(10)).Push(gtx.Ops)
	paint.Fill(gtx.Ops, tok().tipBg)
	st.Pop()
	cur := u.cursorPos()
	y := card.Min.Y + padY
	for i := 0; i < n; i++ {
		it := u.complList[i]
		fillable := it.insert != ""
		r := image.Rect(card.Min.X+padX, y, card.Max.X-padX, y+rowH)
		y += rowH + gap
		if fillable && (i == u.complSel || u.cursorHitsRect(r, cur)) {
			hs := clip.Rect(r).Push(gtx.Ops)
			paint.Fill(gtx.Ops, complSelBg)
			hs.Pop()
		}
		labelColor := tok().brandColor
		if !fillable {
			labelColor = tok().textDim
		}
		nameOp, nameDims := complMeasure(gtx, func(gtx layout.Context) layout.Dimensions {
			s := material.Body2(u.th, it.label)
			s.Color = labelColor
			s.TextSize = unit.Sp(13)
			return s.Layout(gtx)
		})
		tail := u.complFitText(gtx, it.tail, r.Dx()-nameDims.Size.X-gtx.Dp(complTailGapDp))
		tailOp, tailDims := complMeasure(gtx, func(gtx layout.Context) layout.Dimensions {
			s := material.Body2(u.th, tail)
			s.Color = tok().textMuted
			if !fillable {
				s.Color = tok().textDim
			}
			s.TextSize = unit.Sp(13)
			return s.Layout(gtx)
		})
		tr := op.Offset(image.Pt(r.Min.X, r.Min.Y+(rowH-nameDims.Size.Y)/2)).Push(gtx.Ops)
		nameOp.Add(gtx.Ops)
		tr.Pop()
		tr = op.Offset(image.Pt(r.Min.X+nameDims.Size.X+gtx.Dp(complTailGapDp),
			r.Min.Y+(rowH-tailDims.Size.Y)/2)).Push(gtx.Ops)
		tailOp.Add(gtx.Ops)
		tr.Pop()
		if fillable {
			u.complRows = append(u.complRows, complRowRect{idx: i, rect: r})
		}
	}
	if over > 0 {
		hr := image.Rect(card.Min.X+padX, y, card.Max.X-padX, y+rowH)
		hint := u.complFitText(gtx, "还有 "+strconv.Itoa(over)+" 条，继续输入过滤", hr.Dx())
		hintOp, hintDims := complMeasure(gtx, func(gtx layout.Context) layout.Dimensions {
			s := material.Body2(u.th, hint)
			s.Color = tok().textDim
			s.TextSize = unit.Sp(12)
			return s.Layout(gtx)
		})
		tr := op.Offset(image.Pt(hr.Min.X, hr.Min.Y+(rowH-hintDims.Size.Y)/2)).Push(gtx.Ops)
		hintOp.Add(gtx.Ops)
		tr.Pop()
	}
	cl := clip.Rect(card).Push(gtx.Ops)
	event.Op(gtx.Ops, &u.complTag)
	cl.Pop()
	wt.Pop()
	// chrome 形状登记（u.record 用窗口系矩形；整窗 clip → 不受 D79 淡化带作用）。
	u.record(card, gtx.Dp(10), tok().tipBg, image.Rectangle{Max: u.frameSize})
}

// complFitText 尾段按剩余宽截断（D103）：二分最大 rune 前缀，超宽以 … 收尾。
func (u *UI) complFitText(gtx layout.Context, s string, budget int) string {
	if s == "" || budget <= 0 {
		return ""
	}
	if w := u.complTextWidth(gtx, s); w <= budget {
		return s
	}
	const ell = "…"
	budget -= u.complTextWidth(gtx, ell)
	runes := []rune(s)
	lo, hi := 0, len(runes)
	for lo < hi {
		mid := (lo + hi + 1) / 2
		if u.complTextWidth(gtx, string(runes[:mid])) <= budget {
			lo = mid
		} else {
			hi = mid - 1
		}
	}
	if lo == 0 {
		return ell
	}
	return string(runes[:lo]) + ell
}

// complMeasure 量测文本（不落笔）：返回可复放的宏与自然尺寸（约束放开防换行）。
func complMeasure(gtx layout.Context, fn func(gtx layout.Context) layout.Dimensions) (op.CallOp, layout.Dimensions) {
	gtx.Constraints = layout.Constraints{Max: image.Pt(1<<20, 1<<20)}
	m := op.Record(gtx.Ops)
	dims := fn(gtx)
	return m.Stop(), dims
}

// complTextWidth 文本自然宽（px）。
func (u *UI) complTextWidth(gtx layout.Context, s string) int {
	_, dims := complMeasure(gtx, func(gtx layout.Context) layout.Dimensions {
		l := material.Body2(u.th, s)
		l.TextSize = unit.Sp(13)
		return l.Layout(gtx)
	})
	return dims.Size.X
}

// complRowsText 行矩形的可读快照（测试用：只为断言「提示行没有命中矩形」）。
func (u *UI) complRowsText() []string {
	out := make([]string, len(u.complRows))
	for i, r := range u.complRows {
		out[i] = u.complList[r.idx].label
	}
	return out
}
