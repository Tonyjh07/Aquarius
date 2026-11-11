package uigui

// 确认态里的档位列表（D282 修订 D86 的「升一档」）。
//
// 位置与形状：锚在胶囊上沿之上、右缘对齐胶囊（🔑 在动作区最右，列表从它长出来），
// 深色实色卡（与补全浮层同一套：chrome 形状整窗登记、每帧重登记热区）。四项恒列
// （升与降同一处），当前档打「当前」标；卡底一句说明这是**全局**改档，不是「这一次」。
//
// 选完不做任何安全判定：投 /permission（永久改档，与托盘/设置同一条路径）后把选中的
// 档报给 runner，由它按新档重判本次（D282：判定只该有一个地方算）。

import (
	"image"

	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"
	"gioui.org/widget/material"

	"github.com/Tonyjh07/Aquarius/internal/i18n"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

// 档位列表几何（dp）：行高要容下 13sp 档名与「当前」标，卡宽够放最长档名
// （full-access）加两个光学校正的边距。
const (
	levelCardDp  unit.Dp = 220
	levelRowDp   unit.Dp = 30
	levelFootDp  unit.Dp = 30 // 底部说明留两行高（中英都放得下，超出按 MaxLines 截）
	levelPadDp   unit.Dp = 6
	levelGapDp   unit.Dp = 2
	levelFootGap unit.Dp = 4
)

// levelPickOpen 档位列表是否展开：随确认态存在，随应答/换问句收起。
func (u *UI) levelPickable() bool {
	return u.m.confirm != nil && u.m.confirm.req.Origin == port.OriginTool
}

// levelPickRects 档位列表的几何（输入栏局部坐标）：卡 + 四行。抽出来是为了可测——
// 卡片是浮层、不可见时没有任何控件能反推它的位置，几何错（出窗、行叠行）只能靠
// 断言拿住。
//
// 局部坐标可以是负的（卡越过输入行上沿、长在转写区之上，与 hoverCard 同口径），
// minTop 给出「窗顶在局部坐标里的位置」（= -absY），越界时整体下移。
func levelPickRects(gtx layout.Context, pill image.Rectangle, minTop int) (image.Rectangle, []image.Rectangle) {
	n := len(permLevels)
	padY, rowH, gap, footH := gtx.Dp(levelPadDp), gtx.Dp(levelRowDp), gtx.Dp(levelGapDp), gtx.Dp(levelFootDp)
	cardW := gtx.Dp(levelCardDp)
	if maxW := pill.Dx(); cardW > maxW {
		cardW = maxW
	}
	cardH := 2*padY + n*rowH + (n-1)*gap + gtx.Dp(levelFootGap) + footH
	bottom := pill.Min.Y - gtx.Dp(complAnchorGapDp)
	card := image.Rect(pill.Max.X-cardW, bottom-cardH, pill.Max.X, bottom)
	if card.Min.Y < minTop { // 卡顶越过窗顶：整体下移（宁可压住胶囊也不出窗）
		card = card.Add(image.Pt(0, minTop-card.Min.Y))
	}
	padX := gtx.Dp(levelPadDp)
	rows := make([]image.Rectangle, n)
	y := card.Min.Y + padY
	for i := range rows {
		rows[i] = image.Rect(card.Min.X+padX, y, card.Max.X-padX, y+rowH)
		y += rowH + gap
	}
	return card, rows
}

// drawLevelPick 画档位列表（输入栏局部坐标：负 y = 溢出到转写区之上）。
//
// 行矩形每帧重登记到 u.levelRows（窗口系）：高亮与点击都按它算，与补全浮层同构
// （D85：光标直采比 Hover 事件态可靠——光标停在原处时没有 Move 事件）。
func (u *UI) drawLevelPick(gtx layout.Context, absY int, pill image.Rectangle) {
	u.levelRows = u.levelRows[:0]
	if !u.levelPickOpen || !u.levelPickable() {
		return
	}
	cur := u.currentPermLevel()
	card, rows := levelPickRects(gtx, pill, -absY)
	st := clip.UniformRRect(card, gtx.Dp(10)).Push(gtx.Ops)
	paint.Fill(gtx.Ops, tok().tipBg)
	st.Pop()
	u.record(card.Add(image.Pt(0, absY)), gtx.Dp(10), tok().tipBg, image.Rectangle{Max: u.frameSize})

	for i, r := range rows {
		win := r.Add(image.Pt(0, absY))
		u.levelRows = append(u.levelRows, win)
		u.levelRow(gtx, i, r, win, cur)
	}
	padX := gtx.Dp(levelPadDp)
	fo := op.Offset(image.Pt(card.Min.X+padX, rows[len(rows)-1].Max.Y+gtx.Dp(levelFootGap))).Push(gtx.Ops)
	l := material.Caption(u.th, i18n.T("ui.confirm.level_note"))
	l.Color = tok().textDim
	l.MaxLines = 2 // 与 levelFootDp 的两行高配套：更窄的窗里截断，不越出卡片
	gtx2 := gtx
	gtx2.Constraints = layout.Constraints{Max: image.Pt(card.Dx()-2*padX, gtx.Dp(levelFootDp))}
	l.Layout(gtx2)
	fo.Pop()
}

// levelRow 一行档位（整行可点）；r = 局部坐标（排版），win = 窗口系（命中）。
// 当前档整行着底色并在右端标「当前」——底色不写字（品牌色在白面上做文字只有
// 2.5:1，够不着 AA），标记权交给文字。
func (u *UI) levelRow(gtx layout.Context, i int, r, win image.Rectangle, cur string) {
	lv := permLevels[i]
	current := cur != "" && lv == cur
	cl := &u.levelBtns[i]
	gtx2 := gtx
	gtx2.Constraints = layout.Exact(r.Size())
	off := op.Offset(r.Min).Push(gtx.Ops)
	cl.Layout(gtx2, func(gtx layout.Context) layout.Dimensions {
		if current || u.cursorHitsRect(win, u.cursorPos()) {
			st := clip.UniformRRect(image.Rectangle{Max: r.Size()}, gtx.Dp(6)).Push(gtx.Ops)
			paint.Fill(gtx.Ops, complSelBg)
			st.Pop()
		}
		return layout.Inset{Left: unit.Dp(6), Right: unit.Dp(6)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					gtx.Constraints.Min.Y = 0
					l := material.Body2(u.th, lv)
					l.TextSize = unit.Sp(13)
					l.Color = tok().whiteText
					return l.Layout(gtx)
				}),
				layout.Flexed(1, layout.Spacer{}.Layout),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					if !current {
						return layout.Dimensions{}
					}
					gtx.Constraints.Min.Y = 0
					l := material.Caption(u.th, i18n.T("ui.confirm.level_current"))
					l.Color = tok().textDim
					return l.Layout(gtx)
				}),
			)
		})
	})
	off.Pop()
}

// currentPermLevel 当前权限档（快照未就绪 = 空串，列表里一项都不标「当前」）。
func (u *UI) currentPermLevel() string {
	if u.opts.Status == nil {
		return ""
	}
	return u.opts.Status().Level
}

// pickLevel 选了一档（D282）：先投 /permission（永久改档，托盘/设置同一条路径），
// 再把选中的档报给 runner——放不放行由它按新档重判，界面不做安全判定。
func (u *UI) pickLevel(lv string) {
	u.levelPickOpen = false
	u.m.submitCommand(commandLine(uiCmdPermission, lv))
	u.m.replyConfirm(port.ConfirmAnswer{Level: lv})
}
