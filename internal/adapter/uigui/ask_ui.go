package uigui

import (
	"image"
	"image/color"
	"math"
	"strconv"
	"strings"
	"unicode/utf8"

	"gioui.org/f32"
	"gioui.org/io/event"
	"gioui.org/io/key"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"

	"github.com/Tonyjh07/Aquarius/internal/port"
)

// 提问面板呈现与交互（D184，§15.2）：顶部进度段（可点跳题）+ 最小化、中部滚动区
// （题干 / 选项行 / 「自己输入」）、底部「忽略 | 上一步 | 下一步·提交」。
// 面板高随内容增长并夹到窗高 70%，超出部分内部滚动。

// askPanel 面板主体（胶囊内，约束 = 胶囊尺寸）。
func (u *UI) askPanel(gtx layout.Context) layout.Dimensions {
	a := u.m.ask
	if a == nil {
		return layout.Dimensions{}
	}
	// 焦点落点（与遮罩同一套：FocusFilter 使其可获焦 + event.Op 登记命中面），否则
	// FocusCmd 无处落地——面板会拿不到任何键盘事件。最小化时同样保留（Esc 仍可中断）。
	gtx.Event(key.FocusFilter{Target: &u.askTag})
	event.Op(gtx.Ops, &u.askTag)
	if a.mini {
		return u.askMiniRow(gtx)
	}
	// 面板内边距只给上下：左右由 pillContent 的 inputPadDp 统一（避免双层缩进）。
	pad := layout.Inset{Top: unit.Dp(askPadDp), Bottom: unit.Dp(askPadDp)}
	return pad.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
			layout.Rigid(u.askHead),
			layout.Rigid(layout.Spacer{Height: unit.Dp(askGapDp)}.Layout),
			layout.Flexed(1, u.askBody),
			layout.Rigid(layout.Spacer{Height: unit.Dp(askGapDp)}.Layout),
			layout.Rigid(u.askFoot),
		)
	})
}

// askMiniRow 最小化形态：一行摘要（题号 + 页签 + 已答数），整行点击展开。
func (u *UI) askMiniRow(gtx layout.Context) layout.Dimensions {
	a := u.m.ask
	h := gtx.Dp(inputRowDp)
	return u.askMiniBtn.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		gtx.Constraints.Min.Y = h
		pad := layout.Inset{Left: unit.Dp(inputPadDp), Right: unit.Dp(inputPadDp)}
		return pad.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					l := material.Body2(u.th, u.askSummary(a))
					l.TextSize = unit.Sp(14)
					l.Color = textMuted
					l.MaxLines = 1
					return l.Layout(gtx)
				}),
				layout.Flexed(1, layout.Spacer{}.Layout),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					d := gtx.Dp(inputIconDp)
					box := image.Rectangle{Max: image.Pt(d, d)}
					drawMaximize(gtx, box)
					return layout.Dimensions{Size: box.Size()}
				}),
			)
		})
	})
}

// askSummary 最小化行文本：`提问 1/3 · <页签> · 已答 N`。
func (u *UI) askSummary(a *pendingAsk) string {
	if a == nil || len(a.req.Questions) == 0 {
		return "提问"
	}
	q := a.req.Questions[a.cur]
	done := 0
	for i := range a.req.Questions {
		if len(a.sel[i]) > 0 || a.free[i] != "" {
			done++
		}
	}
	return "提问 " + strconv.Itoa(a.cur+1) + "/" + strconv.Itoa(len(a.req.Questions)) +
		" · " + sanitizeControl(q.Header) + " · 已答 " + strconv.Itoa(done)
}

// askHead 顶部：`1/3 个问题` + 进度段（点 = 跳题）+ 最小化。
func (u *UI) askHead(gtx layout.Context) layout.Dimensions {
	a := u.m.ask
	gtx.Constraints.Min.Y = gtx.Dp(askHeadDp)
	kids := []layout.FlexChild{
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			l := material.Caption(u.th, strconv.Itoa(a.cur+1)+"/"+strconv.Itoa(len(a.req.Questions))+" 个问题")
			l.Color = textMuted
			return l.Layout(gtx)
		}),
		layout.Rigid(layout.Spacer{Width: unit.Dp(10)}.Layout),
	}
	kids = append(kids, u.askTabSegments(gtx)...)
	kids = append(kids,
		layout.Flexed(1, layout.Spacer{}.Layout),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return u.askMiniBtn.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				d := gtx.Dp(inputIconDp)
				box := image.Rectangle{Max: image.Pt(d, d)}
				drawMinimize(gtx, box)
				return layout.Dimensions{Size: box.Size()}
			})
		}),
	)
	return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx, kids...)
}

// askTabSegments 每题一段进度条（当前题实色加宽；已答 = 半透明品牌色；未答 = 灰）。
func (u *UI) askTabSegments(gtx layout.Context) []layout.FlexChild {
	a := u.m.ask
	kids := make([]layout.FlexChild, 0, len(a.req.Questions)*2)
	for i := range a.req.Questions {
		i := i
		if i > 0 {
			kids = append(kids, layout.Rigid(layout.Spacer{Width: unit.Dp(6)}.Layout))
		}
		kids = append(kids, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return u.askTabClicks[i].Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				w, h := gtx.Dp(18), gtx.Dp(4)
				if i == a.cur {
					w = gtx.Dp(26)
				}
				box := image.Rectangle{Max: image.Pt(w, h)}
				fill := textDim
				switch {
				case i == a.cur:
					fill = brandColor
				case len(a.sel[i]) > 0 || a.free[i] != "":
					fill = brandColor
					fill.A = 0x99
				}
				paint.FillShape(gtx.Ops, fill, clip.UniformRRect(box, h/2).Op(gtx.Ops))
				// 命中面放大到 22dp 高（进度条本身 4dp 点不中）。
				return layout.Dimensions{Size: image.Pt(w, gtx.Dp(22))}
			})
		}))
	}
	return kids
}

// askBody 中部滚动区：题干 + 选项行 + 「自己输入」行。
func (u *UI) askBody(gtx layout.Context) layout.Dimensions {
	a := u.m.ask
	q := a.req.Questions[a.cur]
	n := 1 + len(q.Options) + 1
	return u.askList.Layout(gtx, n, func(gtx layout.Context, i int) layout.Dimensions {
		switch {
		case i == 0:
			return u.askQuestionRow(gtx, q)
		case i <= len(q.Options):
			return u.askOptionRow(gtx, i-1, q.Options[i-1])
		default:
			return u.askFreeRow(gtx)
		}
	})
}

// askQuestionRow 题干行（列表第 0 行）：多行正文，宽度由列表给定。
func (u *UI) askQuestionRow(gtx layout.Context, q port.AskQuestion) layout.Dimensions {
	l := material.Body2(u.th, sanitizeControl(q.Question))
	l.TextSize = unit.Sp(15)
	l.Color = u.th.Palette.Fg
	return l.Layout(gtx)
}

// askOptionRow 选项行：[○/☑] label（+ 说明小字）。键盘游标（↑↓）落在这行时整行淡底。
func (u *UI) askOptionRow(gtx layout.Context, i int, o port.AskOption) layout.Dimensions {
	a := u.m.ask
	q := a.req.Questions[a.cur]
	on := a.askSelected(i)
	return u.askOptClicks[i].Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		rec := op.Record(gtx.Ops)
		dims := layout.Inset{Top: unit.Dp(3), Bottom: unit.Dp(3)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Axis: layout.Horizontal}.Layout(gtx,
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					d := gtx.Dp(16)
					box := image.Rectangle{Max: image.Pt(d, d)}
					// 垂直对齐首行文字：图标顶到行中偏上。
					off := op.Offset(image.Pt(0, gtx.Dp(2))).Push(gtx.Ops)
					drawAskCheck(gtx, box, q.Multiple, on)
					off.Pop()
					return layout.Dimensions{Size: image.Pt(d, gtx.Dp(askOptRowDp))}
				}),
				layout.Rigid(layout.Spacer{Width: unit.Dp(10)}.Layout),
				layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
					return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							l := material.Body2(u.th, sanitizeControl(o.Label))
							l.TextSize = unit.Sp(14)
							if on {
								l.Color = brandColor
							} else {
								l.Color = u.th.Palette.Fg
							}
							l.MaxLines = 1
							return l.Layout(gtx)
						}),
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							if strings.TrimSpace(o.Description) == "" {
								return layout.Dimensions{}
							}
							l := material.Caption(u.th, sanitizeControl(o.Description))
							l.Color = textMuted
							l.MaxLines = 1
							return l.Layout(gtx)
						}),
					)
				}),
			)
		})
		call := rec.Stop()
		if a.cursor == i {
			// 游标行淡底：内容已录进 call，先垫底再回放（顺序 = 底在下），整行宽。
			box := image.Rectangle{Max: dims.Size}.Inset(-gtx.Dp(4))
			paint.FillShape(gtx.Ops, askRowBg(), clip.UniformRRect(box, gtx.Dp(6)).Op(gtx.Ops))
		}
		call.Add(gtx.Ops)
		return dims
	})
}

// askRowBg 游标行淡底：品牌色压到 10% 不透明度（深浅两版都读得出来，不必另设令牌）。
func askRowBg() color.NRGBA {
	c := brandColor
	c.A = 0x1A
	return c
}

// askFreeRow 「自己输入」行：未展开 = 一行提示（点开就地变输入框）；展开 = 编辑器。
func (u *UI) askFreeRow(gtx layout.Context) layout.Dimensions {
	a := u.m.ask
	if a.open[a.cur] {
		return layout.Inset{Top: unit.Dp(4)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			gtx.Constraints.Min.Y = gtx.Dp(askFreeRowDp)
			ed := material.Editor(u.th, &u.askEd, "自己输入答案…")
			ed.TextSize = unit.Sp(14)
			if u.inFadePass && u.askCaret {
				u.drawEditorSelection(gtx, &u.askEd)
			}
			dims := ed.Layout(gtx)
			if u.inFadePass && u.askCaret {
				u.drawCaret(gtx, dims)
			}
			return dims
		})
	}
	on := a.free[a.cur] != ""
	return u.askFreeClick.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		gtx.Constraints.Min.Y = gtx.Dp(askFreeRowDp)
		label := "自己输入…"
		if on {
			label = "自己输入：" + a.free[a.cur]
		}
		l := material.Body2(u.th, sanitizeControl(label))
		l.TextSize = unit.Sp(14)
		l.Color = textMuted
		if on {
			l.Color = brandColor
		}
		l.MaxLines = 1
		return l.Layout(gtx)
	})
}

// askFoot 底部：左「忽略」，右「上一步」/「下一步 · 提交」。
func (u *UI) askFoot(gtx layout.Context) layout.Dimensions {
	a := u.m.ask
	gtx.Constraints.Min.Y = gtx.Dp(askFootDp)
	last := a.cur >= len(a.req.Questions)-1
	kids := []layout.FlexChild{
		layout.Rigid(u.askTextBtn(gtx, &u.askIgnore, "忽略", false)),
		layout.Flexed(1, layout.Spacer{}.Layout),
	}
	if a.cur > 0 {
		kids = append(kids,
			layout.Rigid(u.askTextBtn(gtx, &u.askPrev, "上一步", false)),
			layout.Rigid(layout.Spacer{Width: unit.Dp(8)}.Layout),
		)
	}
	next := "下一步"
	if last {
		next = "提交"
	}
	kids = append(kids, layout.Rigid(u.askTextBtn(gtx, &u.askNextBtn, next, true)))
	return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx, kids...)
}

// askTextBtn 面板文字按钮（primary = 品牌色实底白字；否则灰字）。
func (u *UI) askTextBtn(gtx layout.Context, cl *widget.Clickable, label string, primary bool) func(gtx layout.Context) layout.Dimensions {
	return func(gtx layout.Context) layout.Dimensions {
		return cl.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			padX, padY := gtx.Dp(14), gtx.Dp(7)
			l := material.Body2(u.th, label)
			l.TextSize = unit.Sp(13)
			if primary {
				l.Color = whiteText
			} else {
				l.Color = textMuted
			}
			rec := op.Record(gtx.Ops)
			dims := l.Layout(gtx)
			call := rec.Stop()
			w, h := dims.Size.X+2*padX, dims.Size.Y+2*padY
			box := image.Rectangle{Max: image.Pt(w, h)}
			if primary {
				paint.FillShape(gtx.Ops, brandColor, clip.UniformRRect(box, h/2).Op(gtx.Ops))
			}
			off := op.Offset(image.Pt(padX, padY)).Push(gtx.Ops)
			call.Add(gtx.Ops)
			off.Pop()
			return layout.Dimensions{Size: box.Size()}
		})
	}
}

// drawAskCheck 单选圆点 / 多选方框（选中 = 品牌色实心 + 白点/白勾；未选 = 灰描边）。
// 描边用「外圈实心 + 内圈底色挖空」画，避免 Path 描边的圆角换算。
func drawAskCheck(gtx layout.Context, box image.Rectangle, multiple, on bool) {
	r := box.Dx() / 2
	if multiple {
		r = box.Dx() / 4
	}
	if !on {
		paint.FillShape(gtx.Ops, textDim, clip.UniformRRect(box, r).Op(gtx.Ops))
		inner := box.Inset(gtx.Dp(1))
		if inner.Empty() {
			return
		}
		paint.FillShape(gtx.Ops, pillBg, clip.UniformRRect(inner, max(0, r-gtx.Dp(1))).Op(gtx.Ops))
		return
	}
	paint.FillShape(gtx.Ops, brandColor, clip.UniformRRect(box, r).Op(gtx.Ops))
	if !multiple {
		d := box.Dx() / 4
		dot := image.Rect(box.Min.X+d, box.Min.Y+d, box.Max.X-d, box.Max.Y-d)
		paint.FillShape(gtx.Ops, whiteText, clip.UniformRRect(dot, d).Op(gtx.Ops))
		return
	}
	cx := float32(box.Min.X) + float32(box.Dx())/2
	cy := float32(box.Min.Y) + float32(box.Dy())/2
	a := float32(box.Dx()) * 0.22
	var p clip.Path
	p.Begin(gtx.Ops)
	p.MoveTo(f32.Pt(cx-a, cy))
	p.LineTo(f32.Pt(cx-a/3, cy+a*0.7))
	p.LineTo(f32.Pt(cx+a, cy-a*0.7))
	paint.FillShape(gtx.Ops, whiteText, clip.Stroke{Path: p.End(), Width: float32(gtx.Dp(2))}.Op())
}

// askPanelPx 面板高（px）：内容自然高，夹在 [最小, 窗高 × 70%]。
func (u *UI) askPanelPx(gtx layout.Context) int {
	a := u.m.ask
	if a == nil || a.mini {
		return gtx.Dp(inputRowDp)
	}
	dpPerPx := float64(gtx.Metric.PxPerDp)
	if dpPerPx <= 0 {
		dpPerPx = 1 // 零值度量（离屏/测试）兜底，防除零得 Inf
	}
	winW := float64(gtx.Constraints.Max.X) / dpPerPx
	winH := float64(gtx.Constraints.Max.Y) / dpPerPx
	natural := u.askNaturalDp(winW)
	if limit := winH * askPanelFrac; natural > limit {
		natural = limit
	}
	if natural < askPanelMinDp {
		natural = askPanelMinDp
	}
	if max := winH - 24; natural > max { // 极矮窗兜底（不让面板吃掉整窗）
		natural = max
	}
	if natural < inputRowDp {
		natural = inputRowDp
	}
	return gtx.Dp(unit.Dp(float32(natural)))
}

// askNaturalDp 面板自然高估算（dp）：题量与选项数决定；超出窗高由 askPanelPx 夹取后
// 内部滚动。文字按字宽估算行数（中文比 ASCII 宽，取两档权重）。
func (u *UI) askNaturalDp(winW float64) float64 {
	a := u.m.ask
	if a == nil || a.cur >= len(a.req.Questions) {
		return float64(askPanelMinDp)
	}
	q := a.req.Questions[a.cur]
	// 胶囊宽 = 窗宽 − 左右边距 − 两枚圆钮 − 两段间距（inputRowRects 同源）；再扣
	// pillContent 的左右内边距，并留滚动条余量。
	inner := winW - float64(2*(sideMarginDp+inputRowDp+inputGapDp+inputPadDp)+12)
	if inner < 80 {
		inner = 80
	}
	h := float64(2*askPadDp + askHeadDp + askFootDp + 2*askGapDp)
	h += float64(askTextLines(q.Question, inner)) * 21
	for _, o := range q.Options {
		h += askOptRowDp + 6 // 行高 + 上下各 3dp 内边距
		if strings.TrimSpace(o.Description) != "" {
			h += askOptDescDp
		}
	}
	h += askFreeRowDp
	return h
}

// askTextLines 估算文本在给定宽度内占几行（ASCII 8.5dp/字、其余 15dp/字）。
func askTextLines(s string, width float64) int {
	if strings.TrimSpace(s) == "" {
		return 1
	}
	total := 0.0
	for _, r := range s {
		if r < utf8.RuneSelf {
			total += 8.5
		} else {
			total += 15
		}
	}
	lines := int(math.Ceil(total / width))
	if lines < 1 {
		lines = 1
	}
	return lines
}

// updateAsk 面板按键与焦点（D184）：面板持键盘时 1..9 选项、↑↓ 移动、Enter 确认并进
// 下一题、Ctrl+Enter 下一步/提交、Alt+← 上一步、Esc 中断本轮。
//
// 焦点纪律：面板打开时焦点给 askTag（主编辑器不渲染、也不能吃键）；点开「自己输入」
// 后焦点给 askEd，此时字符键归编辑器（面板的数字/方向键过滤器自然让位）。
func (u *UI) updateAsk(gtx layout.Context) {
	a := u.m.ask
	if a == nil {
		return
	}
	if u.askFocus {
		u.askFocus = false
		gtx.Execute(key.FocusCmd{Tag: &u.askTag})
	}
	if u.askFreeFocus {
		u.askFreeFocus = false
		gtx.Execute(key.FocusCmd{Tag: &u.askEd})
	}
	if !u.inFadePass {
		u.askCaret = gtx.Focused(&u.askEd)
	}
	// 「自己输入」编辑器：Enter（单行）= 确认并进下一题。
	for {
		evt, ok := u.askEd.Update(gtx)
		if !ok {
			break
		}
		if _, isSubmit := evt.(widget.SubmitEvent); isSubmit {
			u.m.askNext()
			return
		}
	}
	if u.inFadePass {
		return
	}
	for {
		// 三组过滤：无修饰单键（数字/方向/Enter/Tab）、Alt 组合（Alt+←）、Ctrl 组合
		// （Ctrl+Enter）——Gio 的默认过滤只收「无额外修饰」的键，带修饰的另立一条。
		ev, ok := gtx.Event(
			key.Filter{Focus: &u.askTag},
			key.Filter{Focus: &u.askTag, Required: key.ModAlt},
			key.Filter{Focus: &u.askTag, Required: key.ModCtrl},
		)
		if !ok {
			break
		}
		ke, isKey := ev.(key.Event)
		if !isKey || ke.State != key.Press {
			continue
		}
		u.askKey(ke)
		if u.m.ask == nil {
			return // 面板已收场（Esc / 提交）
		}
	}
}

// askKey 面板单键语义。
func (u *UI) askKey(ke key.Event) {
	a := u.m.ask
	if a == nil {
		return
	}
	q := a.req.Questions[a.cur]
	switch {
	case ke.Name == key.NameEscape:
		// Esc = 中断本轮（与停止生成同路径）；Ask 侧据 ctx 取消收场并撤面板。
		u.interruptNow()
	case ke.Name == key.NameEnter || ke.Name == key.NameReturn:
		// 游标停在「自己输入」行 = 点开输入框；否则确认并进下一题（最后一题 = 提交）。
		if a.cursor == len(q.Options) {
			u.m.askOpenFree()
			return
		}
		u.m.askNext()
	case ke.Name == key.NameUpArrow:
		u.askMove(-1)
	case ke.Name == key.NameDownArrow:
		u.askMove(+1)
	case ke.Name == key.NameLeftArrow && ke.Modifiers.Contain(key.ModAlt):
		u.m.askGoto(a.cur - 1)
	case ke.Name == key.NameTab:
		u.m.askGoto(a.cur + 1)
	default:
		n, err := strconv.Atoi(string(ke.Name))
		if err != nil || n < 1 || n > len(q.Options) {
			return
		}
		u.m.askToggle(n - 1)
		a.cursor = n - 1
	}
}

// askMove 键盘游标移动（选项行 + 「自己输入」行）。
func (u *UI) askMove(d int) {
	a := u.m.ask
	if a == nil {
		return
	}
	q := a.req.Questions[a.cur]
	last := len(q.Options) // 「自己输入」行下标
	cur := a.cursor
	if cur < 0 {
		if d > 0 {
			cur = 0
		} else {
			cur = last
		}
	} else {
		cur += d
	}
	if cur < 0 {
		cur = 0
	}
	if cur > last {
		cur = last
	}
	a.cursor = cur
	if cur == last {
		// 游标落在「自己输入」= 展开输入框（与鼠标点开同语义）。
		u.m.askOpenFree()
		return
	}
	// 离开「自己输入」行：焦点交还面板（否则字符键还往自由输入框里跑）。
	u.askFocus = true
}

// updateAskClicks 面板点击：最小化、进度段、选项行、自由输入行、底部按钮。
func (u *UI) updateAskClicks(gtx layout.Context) {
	a := u.m.ask
	if a == nil {
		return
	}
	if u.askMiniBtn.Clicked(gtx) {
		a.mini = !a.mini
		if !a.mini {
			u.askFocus = true
		}
		return
	}
	if a.mini {
		return
	}
	for i := range a.req.Questions {
		if u.askTabClicks[i].Clicked(gtx) {
			u.m.askGoto(i)
			return
		}
	}
	for i := range a.req.Questions[a.cur].Options {
		if u.askOptClicks[i].Clicked(gtx) {
			u.m.askToggle(i)
			a.cursor = i
			u.askFocus = true // 点选项 = 焦点回面板（自由输入框让位）
			return
		}
	}
	if u.askFreeClick.Clicked(gtx) {
		u.m.askOpenFree()
		return
	}
	if u.askIgnore.Clicked(gtx) {
		u.m.askDismiss()
		return
	}
	if u.askPrev.Clicked(gtx) {
		u.m.askGoto(a.cur - 1)
		return
	}
	if u.askNextBtn.Clicked(gtx) {
		u.m.askNext()
	}
}
