package uigui

// toast.go 轻提示（toast）组件：一小段文案浮在窗口底部，约 2 秒后自动消失。
//
// 定位：**可复用的窗内组件**，不是全局单例。每个窗各持一个 toastState 并在自己帧尾
// 渲染——历史窗与主窗可能同时开着（各窗独立 goroutine），若做成全局单例，在历史窗触发
// 的复制会连主窗一起闪；而这次交互只发生在历史窗。故「在哪个窗触发，就在哪个窗呈现」。
//
// 可见性靠 Gio 的定时唤帧（op.InvalidateCmd{At}）自动擦除：不做逐帧轮询，也不依赖下一
// 次用户输入——到期那一帧起渲染为无。

import (
	"image"
	"image/color"
	"time"

	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/widget/material"
)

// toastLifetimeDp 轻提示停留时长。
const toastLifetime = 2 * time.Second

// toastPadXdp / toastPadYdp 轻提示内衬；toastRaisedp 距窗口底边的抬升；toastRadiusDp 圆角。
const (
	toastPadXdp   = 18
	toastPadYdp   = 9
	toastRaisedp  = 18
	toastRadiusDp = 10
	toastShadowDp = 8
	toastShadowDy = 3
	toastShadowPk = 0x30
	// toastWidthFac 文案区宽度上限（视口占比）：超长文案在卡内折行，不铺满整窗。
	toastWidthFac = 0.6
)

// toastState 单个窗的轻提示帧态（零值 = 无提示）。
type toastState struct {
	text  string
	until time.Time
}

// show 显示一条提示（覆盖上一条），自 now 起停留 toastLifetime。
func (ts *toastState) show(text string, now time.Time) {
	if text == "" {
		return
	}
	ts.text = text
	ts.until = now.Add(toastLifetime)
}

// active 当前是否有提示在屏（按帧时间判定；到期即视为无）。
func (ts *toastState) active(now time.Time) bool {
	return ts.text != "" && now.Before(ts.until)
}

// toastFrame 轻提示单帧：底部居中浮层。无提示 = 零尺寸空操作。
// bg = 浮层底色（各窗按自有配色传入：次窗传 paperOf().tipBg，主窗传包级 tipBg），
// 文字恒白（深底上的高对比，与既有悬浮 tips 同口径）。
func toastFrame(gtx layout.Context, th *material.Theme, ts *toastState, bg color.NRGBA) layout.Dimensions {
	if !ts.active(gtx.Now) {
		return layout.Dimensions{}
	}
	// 到期补一帧：本帧之后自动擦除，不依赖下一次用户输入。
	gtx.Execute(op.InvalidateCmd{At: ts.until})

	l := material.Label(th, paperNoteSp(th), ts.text)
	l.Color = whiteText
	maxW := int(float64(gtx.Constraints.Max.X) * toastWidthFac)
	if maxW < gtx.Dp(toastPadXdp)*2 {
		maxW = gtx.Constraints.Max.X
	}
	// 量文字尺寸（限制宽度 → 超长文案在卡内折行）。
	macro := op.Record(gtx.Ops)
	m := gtx
	m.Constraints = layout.Constraints{Max: image.Pt(maxW, gtx.Constraints.Max.Y)}
	dims := l.Layout(m)
	call := macro.Stop()

	padX, padY := gtx.Dp(toastPadXdp), gtx.Dp(toastPadYdp)
	w, h := dims.Size.X+2*padX, dims.Size.Y+2*padY
	if w > gtx.Constraints.Max.X {
		w = gtx.Constraints.Max.X
	}
	rad := gtx.Dp(toastRadiusDp)
	x := (gtx.Constraints.Max.X - w) / 2
	if x < 0 {
		x = 0
	}
	y := gtx.Constraints.Max.Y - gtx.Dp(toastRaisedp) - h
	if y < 0 {
		y = 0
	}
	box := image.Rectangle{Min: image.Pt(x, y), Max: image.Pt(x+w, y+h)}

	// 浮层：先阴影再底板（同遮罩卡片口径），底板自带圆角、不借窗底色。
	paperShadow(gtx, box, rad, gtx.Dp(toastShadowDp), toastShadowDy, toastShadowPk)
	paint.FillShape(gtx.Ops, bg, clip.UniformRRect(box, rad).Op(gtx.Ops))

	// 文字：按量出的尺寸居中定尺（不借 layout.Center——Stacked 会清 Min，导致贴左上）。
	inner := image.Rect(padX, padY, padX+dims.Size.X, padY+dims.Size.Y)
	gtx.Constraints = layout.Exact(inner.Size())
	off := op.Offset(box.Min.Add(inner.Min)).Push(gtx.Ops)
	call.Add(gtx.Ops)
	off.Pop()
	return layout.Dimensions{Size: box.Size()}
}
