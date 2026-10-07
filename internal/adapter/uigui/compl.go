package uigui

import (
	"image"
	"image/color"
	"strconv"
	"strings"

	"gioui.org/io/event"
	"gioui.org/io/key"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"
	"gioui.org/widget/material"

	"github.com/Tonyjh07/Aquarius/internal/port"
)

// complSelBg 补全行高亮底色（tipBg 亮一档）。
var complSelBg = color.NRGBA{R: 0x36, G: 0x3B, B: 0x41, A: 0xFF}

// complMaxRows 补全浮层最多显示的命令行数（D103：超出部分折叠为提示行）。
const complMaxRows = 8

const (
	complRowDp       = 24  // 行高
	complMaxWidthDp  = 440 // 卡片最大宽（随胶囊宽收缩）
	complTailGapDp   = 8   // 名称与尾段（usage+描述）间距
	complAnchorGapDp = 4   // 卡片与胶囊上沿间距
)

// complItem 过滤后的补全行：条目 + 命中名（别名场景落笔用命中名，不回写主名——
// 输入 "/exit" 补全不改写成 "/quit"）。
type complItem struct {
	info port.CommandInfo
	name string
}

// complPhase 命令词法相判定（D103）：文本以 "/" 开头且尚无空格——出现空格即视为
// 参数相，浮层关闭（参数级补全不做）。
func complPhase(text string) bool {
	return strings.HasPrefix(text, "/") && !strings.ContainsAny(text, " \t")
}

// complFilter 前缀过滤（D103）：对条目任一名称做大小写不敏感前缀匹配、保持清单序；
// 命中名取第一个匹配的名称（主名优先）。
func complFilter(prefix string, all []port.CommandInfo) []complItem {
	p := strings.ToLower(prefix)
	var out []complItem
	for _, ci := range all {
		for _, n := range ci.Names {
			if strings.HasPrefix(strings.ToLower(n), p) {
				out = append(out, complItem{info: ci, name: n})
				break
			}
		}
	}
	return out
}

// complAccept 补全落笔（D103）：已输入与高亮匹配名完全一致 → 直接提交；否则补全为
// "/名 "（尾随空格落参数相，浮层随词法相关闭）。
func complAccept(text string, it complItem) (string, bool) {
	full := "/" + it.name
	if text == full {
		return text, true
	}
	return full + " ", false
}

// complUpdate 补全浮层消费者（D103）：每帧依编辑框文本重算词法相与过滤清单（命令清单
// 端口每帧拉取——动态命令可随插件启停变化）；开启期消费上一帧注册的 ↑↓/Esc 键过滤
// 与行点击。须先于 updateEditor（Enter 接管在其 SubmitEvent 分支、Esc 在编辑态让渡）。
func (u *UI) updateCompl(gtx layout.Context) {
	u.complOpenNow = false
	u.complEsc = false
	if u.m.confirm != nil { // 确认态主编辑器隐藏，浮层不参与
		u.complList = nil
		return
	}
	text := u.editor.Text()
	if text != u.complText {
		u.complText = text
		u.complDismissed = false
		u.complSel = 0
	}
	if u.complDismissed || !complPhase(text) || u.opts.Commands == nil {
		u.complList = nil
		return
	}
	u.complList = complFilter(text[1:], u.opts.Commands.Commands())
	if len(u.complList) == 0 {
		return
	}
	u.complOpenNow = true
	if u.complSel >= len(u.complList) {
		u.complSel = len(u.complList) - 1
	}
	// 键面（Focus 限编辑器——浮层随编辑框起落；编辑器对 ↑↓/Esc 无语义，无冲突面；
	// 过滤器由上一帧 drawCompl 登记，Gio 一帧陈旧与手势同口径）。
	for {
		ev, ok := gtx.Event(
			key.Filter{Focus: &u.editor, Name: key.NameUpArrow},
			key.Filter{Focus: &u.editor, Name: key.NameDownArrow},
			key.Filter{Focus: &u.editor, Name: key.NameEscape},
		)
		if !ok {
			break
		}
		ke, isKey := ev.(key.Event)
		if !isKey || ke.State != key.Press {
			continue
		}
		switch ke.Name {
		case key.NameUpArrow:
			u.complSel = (u.complSel - 1 + len(u.complList)) % len(u.complList)
		case key.NameDownArrow:
			u.complSel = (u.complSel + 1) % len(u.complList)
		case key.NameEscape:
			u.complDismissed = true
			u.complEsc = true // 编辑态 Esc 让渡：本轮 updateEditor 不取消编辑
			u.complOpenNow = false
		}
	}
	u.complClicks(gtx)
}

// complClicks 行点击消费（drawCompl 登记 Press/Release 于卡片矩形）：武装-原位抬起
// （D72 口径）→ 命中行补全落笔。坐标 = 窗口系（注册与行矩形同系）。
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
				for i, r := range u.complRows {
					if i < len(u.complList) && pt.In(r) && u.complPressPt.In(r) {
						u.complSel = i
						u.complEnter()
						break
					}
				}
			case pointer.Cancel:
				u.complArmed = false
			}
		}
	}
}

// complEnter Enter/点击落笔（D103）：高亮项与已输入完全一致 → 直接提交（submitEditor
// 自取编辑框文本）；否则补全为 "/名 "。
func (u *UI) complEnter() {
	if u.complSel < 0 || u.complSel >= len(u.complList) {
		return
	}
	if _, submit := complAccept(u.editor.Text(), u.complList[u.complSel]); submit {
		u.submitEditor()
		return
	}
	full, _ := complAccept("", u.complList[u.complSel])
	u.editor.SetText(full)
}

// drawCompl 补全浮层绘制（D103）：hoverCard 同款深色卡（chrome 形状——整窗 clip 登记、
// 不受 D79 淡化带作用），锚在胶囊上方左缘。行 = 命令名（品牌色）+ usage/描述（暗色、
// 按剩余宽二分截断）；键盘高亮与光标悬停（D85 直采）都着底色。绘制与命中统一用窗口系
// 坐标（绘制经 offset(-absY) 换到窗口系，热区注册同系）；动画期不画（D54 tips 同口径），
// 行矩形每帧重登记（fade pass 重渲同几何复登，与 bubbleRects 同构）。
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
	paint.Fill(gtx.Ops, tipBg)
	st.Pop()
	cur := u.cursorPos()
	y := card.Min.Y + padY
	for i := 0; i < n; i++ {
		it := u.complList[i]
		r := image.Rect(card.Min.X+padX, y, card.Max.X-padX, y+rowH)
		y += rowH + gap
		if i == u.complSel || u.cursorHitsRect(r, cur) {
			hs := clip.Rect(r).Push(gtx.Ops)
			paint.Fill(gtx.Ops, complSelBg)
			hs.Pop()
		}
		nameOp, nameDims := complMeasure(gtx, func(gtx layout.Context) layout.Dimensions {
			s := material.Body2(u.th, "/"+it.name)
			s.Color = brandColor
			s.TextSize = unit.Sp(13)
			return s.Layout(gtx)
		})
		tail := it.info.Usage
		if it.info.Desc != "" {
			if tail != "" {
				tail += " — "
			}
			tail += it.info.Desc
		}
		tail = u.complFitText(gtx, tail, r.Dx()-nameDims.Size.X-gtx.Dp(complTailGapDp))
		tailOp, tailDims := complMeasure(gtx, func(gtx layout.Context) layout.Dimensions {
			s := material.Body2(u.th, tail)
			s.Color = textMuted
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
		u.complRows = append(u.complRows, r)
	}
	if over > 0 {
		hr := image.Rect(card.Min.X+padX, y, card.Max.X-padX, y+rowH)
		hint := u.complFitText(gtx, "还有 "+strconv.Itoa(over)+" 条，继续输入过滤", hr.Dx())
		hintOp, hintDims := complMeasure(gtx, func(gtx layout.Context) layout.Dimensions {
			s := material.Body2(u.th, hint)
			s.Color = textDim
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
	u.record(card, gtx.Dp(10), tipBg, image.Rectangle{Max: u.frameSize})
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
