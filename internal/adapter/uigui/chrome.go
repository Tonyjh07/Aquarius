package uigui

import (
	"image"
	"image/color"

	"gioui.org/f32"
	"gioui.org/gesture"
	"gioui.org/io/event"
	"gioui.org/io/input"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/widget"
)

// drawMaximize 展开图标占位（灰、不可点）：四角括号——canvas 原几何（内缩 2、臂长 6、
// 臂厚 2，稿内 1:1 即 dp），只把配色换成占位灰。
func drawMaximize(gtx layout.Context, box image.Rectangle) {
	if box.Dx() <= 0 || box.Dy() <= 0 {
		return
	}
	in, arm, th := gtx.Dp(2), gtx.Dp(6), gtx.Dp(2)
	x0, y0 := box.Min.X, box.Min.Y
	x1, y1 := box.Max.X, box.Max.Y
	for _, r := range []image.Rectangle{
		image.Rect(x0+in, y0+in, x0+in+arm, y0+in+th), // 左上·横
		image.Rect(x0+in, y0+in, x0+in+th, y0+in+arm), // 左上·竖
		image.Rect(x1-in-arm, y0+in, x1-in, y0+in+th), // 右上·横
		image.Rect(x1-in-th, y0+in, x1-in, y0+in+arm), // 右上·竖
		image.Rect(x0+in, y1-in-th, x0+in+arm, y1-in), // 左下·横
		image.Rect(x0+in, y1-in-arm, x0+in+th, y1-in), // 左下·竖
		image.Rect(x1-in-arm, y1-in-th, x1-in, y1-in), // 右下·横
		image.Rect(x1-in-th, y1-in-arm, x1-in, y1-in), // 右下·竖
	} {
		paint.FillShape(gtx.Ops, iconDim, clip.Rect(r).Op())
	}
}

// drawMinimize 收起图标（D106）：四边中点向内的短杠（与外扩四角括号对偶——「收回」），
// 灰、臂长同 drawMaximize。
func drawMinimize(gtx layout.Context, box image.Rectangle) {
	if box.Dx() <= 0 || box.Dy() <= 0 {
		return
	}
	in, arm, th := gtx.Dp(2), gtx.Dp(6), gtx.Dp(2)
	x0, y0, x1, y1 := box.Min.X, box.Min.Y, box.Max.X, box.Max.Y
	cx, cy := (x0+x1)/2, (y0+y1)/2
	for _, r := range []image.Rectangle{
		image.Rect(cx-th/2, y0+in, cx+th/2, y0+in+arm), // 上·竖（向下指）
		image.Rect(cx-th/2, y1-in-arm, cx+th/2, y1-in), // 下·竖（向上指）
		image.Rect(x0+in, cy-th/2, x0+in+arm, cy+th/2), // 左·横（向右指）
		image.Rect(x1-in-arm, cy-th/2, x1-in, cy+th/2), // 右·横（向左指）
	} {
		paint.FillShape(gtx.Ops, iconDim, clip.Rect(r).Op())
	}
}

// cubicArc 以 c 为圆心、从 p 到 q 的 90° 圆弧（三次贝塞尔逼近，k = 0.5523）：
// 切向 = 半径向量 ±90°（方向由 p→q 相对 c 的旋转取号），控制点 = 出点沿切向前伸、
// 入点沿切向回退。占位图标折线用。
func cubicArc(path *clip.Path, c, p, q f32.Point) {
	const k = 0.5523
	r1 := f32.Point{X: p.X - c.X, Y: p.Y - c.Y}
	r2 := f32.Point{X: q.X - c.X, Y: q.Y - c.Y}
	t1 := f32.Point{X: -r1.Y, Y: r1.X}
	t2 := f32.Point{X: -r2.Y, Y: r2.X}
	if r1.X*r2.Y-r1.Y*r2.X < 0 {
		t1 = f32.Point{X: r1.Y, Y: -r1.X}
		t2 = f32.Point{X: r2.Y, Y: -r2.X}
	}
	path.CubeTo(
		f32.Point{X: p.X + k*t1.X, Y: p.Y + k*t1.Y},
		f32.Point{X: q.X - k*t2.X, Y: q.Y - k*t2.Y},
		q,
	)
}

// logoRight D72 logo 右键菜单手势状态：区域内右键按下（pointer.Event.Buttons 含
// ButtonSecondary）武装、同指针**原位**抬起（位置落 bounds 内）= 请求菜单；移出/
// Cancel = 放弃。Gio 的 Release 恒投给按下时记录的 handlers（不按位置重命中），故
// 「移出不弹」必须位置门控。gesture.Drag/Click 均跳过非主键按下（右键与其零冲突），
// 故需自挂 pointer.Filter。
type logoRight struct {
	armed  bool
	pid    pointer.ID
	bounds image.Rectangle // 注册区（logo 钮/球矩形；坐标空间 = event.Op 处的局部空间）
}

// add 注册右键手势并记录热区矩形（展开 = logo 钮 clip 矩形（输入栏局部空间）、
// 收起 = 球矩形（窗口空间）——与事件投递的 invTransform 局部空间一一对应）。
// 收起态注册整窗 clip、事件本只落球像素（D62 逐像素命中），热区仍按球矩形门控。
func (r *logoRight) add(ops *op.Ops, bounds image.Rectangle) {
	r.bounds = bounds
	event.Op(ops, r)
}

// update 消费手势事件：右键按下武装、同指针原位抬起返回 true（请求菜单）；位置在
// 热区外的抬起、Cancel、非右键按下均解除武装（左键照常零干扰——gesture.Drag 本就
// 跳过非主键）。
func (r *logoRight) update(q input.Source) bool {
	fire := false
	for {
		ev, ok := q.Event(pointer.Filter{
			Target: r,
			Kinds:  pointer.Press | pointer.Release | pointer.Cancel,
		})
		if !ok {
			break
		}
		e, ok := ev.(pointer.Event)
		if !ok {
			continue
		}
		switch e.Kind {
		case pointer.Press:
			r.armed = e.Buttons.Contain(pointer.ButtonSecondary)
			r.pid = e.PointerID
		case pointer.Release:
			if r.armed && e.PointerID == r.pid && e.Position.Round().In(r.bounds) {
				fire = true
			}
			r.armed = false
		case pointer.Cancel:
			r.armed = false
		}
	}
	return fire
}

// requestLogoMenu 请求弹出 logo 右键菜单（D72）：测试经 logoMenuHook 回执；生产投
// 托盘线程呈现（shell 线程已有独立消息泵，TrackPopupMenu 不嵌 Gio 泵）。
func (u *UI) requestLogoMenu() {
	if u.logoMenuHook != nil {
		u.logoMenuHook()
		return
	}
	postLogoMenu()
}

// updateLogo logo 圆钮手势（§15.1 把手含 logo）：拖动移窗；单击（位移小于
// dragClickSlackPx）= 收起回球。悬停 tips 不经手势事件（D85：事件态不可靠，改
// WindowFromPoint 直证命中，见 tips 块与 cursorHitsLogo）。收起态圆钮区不存在
// （事件归背景把手）——右键菜单例外，收起态照跑（球即 logo，D72）。
func (u *UI) updateLogo(gtx layout.Context) {
	// D72 右键菜单：原位抬起才请求（动画期不响应，D54；收起态照跑——球即 logo）。
	if !u.expandAn.active && u.logoRight.update(gtx.Source) {
		u.requestLogoMenu()
	}
	for {
		ev, ok := u.logoDrag.Update(gtx.Metric, gtx.Source, gesture.Both)
		if !ok {
			break
		}
		switch ev.Kind {
		case pointer.Press:
			u.beginDrag()
		case pointer.Drag:
			u.moveDrag()
		case pointer.Release, pointer.Cancel:
			was := u.dragging
			u.endDrag()
			if ev.Kind == pointer.Release && was && u.clickHeld() {
				u.toggleExpand() // 左键 logo = 互切（§15.1；D54 动画中反向续跑）
			}
		}
	}
}

// actionCircle 右侧主操作圆钮（D49/§15.2：⌀ = 行高，三态恒在同一位置、几何不随状态变）：
// idle = 发送（向上箭头）、生成中 = 停止（方块图标）、确认态 = 置灰（cl=nil 不可点）。
// 坐标 = 当前原点（调用方偏移到 send 位）；Gio Clickable 在原点登记面积（无居中），
// 走同一 updateClicks 路径。
func actionCircle(gtx layout.Context, cl *widget.Clickable, fill color.NRGBA, stop bool) layout.Dimensions {
	d := gtx.Dp(inputRowDp)
	draw := func(gtx layout.Context) layout.Dimensions {
		box := image.Rectangle{Max: image.Pt(d, d)}
		paint.FillShape(gtx.Ops, fill, clip.UniformRRect(box, d/2).Op(gtx.Ops))
		if stop {
			// 停止 = 圆角方块。
			s := d / 3
			r := image.Rectangle{
				Min: image.Pt((d-s)/2, (d-s)/2),
				Max: image.Pt((d+s)/2, (d+s)/2),
			}
			paint.FillShape(gtx.Ops, whiteText, clip.UniformRRect(r, gtx.Dp(2)).Op(gtx.Ops))
			return layout.Dimensions{Size: image.Pt(d, d)}
		}
		// 向上箭头 = 竖杆 + 人字头（白色描边）。
		cx, cy := float32(d)/2, float32(d)/2
		a := float32(d) * 0.18
		var p clip.Path
		p.Begin(gtx.Ops)
		p.MoveTo(f32.Pt(cx, cy+a)) // 竖杆
		p.LineTo(f32.Pt(cx, cy-a))
		p.MoveTo(f32.Pt(cx-a, cy)) // 人字头
		p.LineTo(f32.Pt(cx, cy-a))
		p.LineTo(f32.Pt(cx+a, cy))
		paint.FillShape(gtx.Ops, whiteText,
			clip.Stroke{Path: p.End(), Width: float32(gtx.Dp(3))}.Op())
		return layout.Dimensions{Size: image.Pt(d, d)}
	}
	if cl == nil {
		return draw(gtx)
	}
	return cl.Layout(gtx, draw)
}
