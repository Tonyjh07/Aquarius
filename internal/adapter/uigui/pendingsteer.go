package uigui

// pendingsteer.go 转写区里的引导气泡（D272）：用户已经说出口、等下一次请求取走的话。
//
// 为什么它不待在待发条里：排队是「等服务完再说」，引导是「马上就到模型眼前」——两者
// 的时间感差一整轮，挤在同一条带上分不清。引导直接铺成末端的用户气泡（与真节点同形），
// 旁边挂两枚控件：↓ 让它退回队列（气泡消失、条目回到待发条），× 丢弃。
//
// 这两枚控件是**自绘热区**，不用气泡内的布局（气泡内容是量期录好的宏，两遍布局会让
// Clickable 注册两次）。
//
// 气泡本体比真节点淡一档：转写区里同时存在「已经进树的话」与「还没被取走的话」，形状
// 一样、含义不同，靠透明度分开；控件不跟着淡——它得始终看得清、点得着。

import (
	"image"
	"image/color"

	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/widget/material"

	"github.com/Tonyjh07/Aquarius/internal/port"
)

// drawActIcon 画一枚圆形图标按钮（字居中）。待发条与引导气泡共用同一个形状——
// 同一种动作在两处出现时长得一样，用户不用重新认。
func (u *UI) drawActIcon(gtx layout.Context, r image.Rectangle, glyph string, fg, bgc color.NRGBA) {
	if r.Empty() {
		return
	}
	paint.FillShape(gtx.Ops, bgc, clip.UniformRRect(r, r.Dx()/2).Op(gtx.Ops))
	l := material.Caption(u.th, glyph)
	l.Color = fg
	lw, lh := measure(gtx, l)
	o := op.Offset(image.Pt(r.Min.X+(r.Dx()-lw)/2, r.Min.Y+(r.Dy()-lh)/2)).Push(gtx.Ops)
	l.Layout(gtx)
	o.Pop()
}

const (
	// steerActDp 引导气泡控件直径（dp）。
	steerActDp = 20
	// steerActGapDp 两枚控件之间的缝隙（dp）。
	steerActGapDp = 4
	// steerActInsetDp 气泡外侧留白（dp）：控件与气泡之间也用它。
	steerActInsetDp = 6
	// steerBubbleAlpha 待响应引导气泡的不透明度：它还不是会话节点，与已入树的气泡
	// 要一眼可分（0.7 仍看得清字，又明显淡于真气泡）。
	steerBubbleAlpha = 0.7
)

// steerHot 一条引导气泡在本帧占的控件热区（窗口坐标）。
type steerHot struct {
	id   string
	down image.Rectangle
	drop image.Rectangle
}

// steerActions 在一条引导气泡旁画「↓ 回队列 / × 丢弃」。
//
// 位置优先放气泡左侧（用户气泡右对齐，左边通常空着）；窄窗里气泡占满整行时退到气泡
// 内部左上角——控件不能因为窗口窄就消失，那正是用户够不到的时候。
//
// 两枚圆钮**必须各自 record**（viewport = 转写视口）：Windows 上真正上屏的是全帧合成
// （fadeFrame），它只写已登记形状覆盖的像素——画出来不等于留得下，漏了这一步控件会
// 在合成阶段整颗被丢掉（气泡本体没这毛病，是因为 paintRow 登过记）。
func (u *UI) steerActions(gtx layout.Context, id string, bg, viewport image.Rectangle) {
	sz := gtx.Dp(steerActDp)
	gap := gtx.Dp(steerActGapDp)
	inset := gtx.Dp(steerActInsetDp)
	margin := gtx.Dp(sideMarginDp)

	x := bg.Min.X - inset - sz // ×（贴气泡）
	dropRect := image.Rect(x, bg.Min.Y+(bg.Dy()-sz)/2, x+sz, bg.Min.Y+(bg.Dy()-sz)/2+sz)
	x -= sz + gap
	downRect := image.Rect(x, dropRect.Min.Y, x+sz, dropRect.Max.Y)

	inside := false
	if downRect.Min.X < margin {
		// 退到气泡内部左上角（压在选择区之上，但它是按钮，点它不会被当选文本）。
		inside = true
		x0 := bg.Min.X + inset
		downRect = image.Rect(x0, bg.Min.Y+inset, x0+sz, bg.Min.Y+inset+sz)
		dropRect = image.Rect(downRect.Max.X+gap, downRect.Min.Y, downRect.Max.X+gap+sz, downRect.Max.Y)
	}

	ctl := u.steerCtlFor(id)
	// 先取点击再画（Clickable.Layout 会把点击结果丢掉，见 clickRect）。
	hitDown := clickRect(gtx, &ctl.down, downRect)
	hitDrop := clickRect(gtx, &ctl.drop, dropRect)

	// 气泡外：静息底 + 主色字；气泡内：半透明白底 + 白字（压得住品牌色气泡）。
	fg, bgc := tok().textMuted, tok().pillBg
	if inside {
		// 压在品牌色气泡上：白字 + 比气泡更深的同色底（只靠白字会糊成一片）。
		fg, bgc = tok().whiteText, tok().brandColor
	}
	radius := sz / 2
	u.drawActIcon(gtx, downRect, "↓", fg, bgc)
	u.drawActIcon(gtx, dropRect, "×", fg, bgc)
	// 合成阶段只认登记过的形状（D62）：不登记 = 上屏时看不见，热区却还在——更难查。
	u.record(downRect, radius, bgc, viewport)
	u.record(dropRect, radius, bgc, viewport)

	u.steerHot = append(u.steerHot, steerHot{id: id, down: downRect, drop: dropRect})
	switch {
	case hitDown:
		u.setPendingDelivery(id, port.DeliveryQueue)
	case hitDrop:
		u.dropPending(id)
	}
}
