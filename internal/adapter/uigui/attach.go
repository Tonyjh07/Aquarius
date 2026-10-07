package uigui

import (
	"image"
	"image/color"
	"path/filepath"

	"gioui.org/f32"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"
	"gioui.org/widget/material"
)

// attachSlot 附件槽实装（D104⑤）：20dp 命中 + 回形针图标（canvas 1:1，D49 灰槽启用）。
func (u *UI) attachSlot(gtx layout.Context) layout.Dimensions {
	d := gtx.Dp(inputIconDp)
	return u.attachBtn.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		box := image.Rectangle{Max: image.Pt(d, d)}
		drawPaperclip(gtx, box)
		return layout.Dimensions{Size: box.Size()}
	})
}

// attachChip 暂存附件 chip（D104①）：回形针 + 文件名（按剩余宽截断）+ ×，深底圆角条；
// 整 chip 点击 = 取消暂存（芯片无第二动作，命中面最大化）。
func (u *UI) attachChip(gtx layout.Context) layout.Dimensions {
	const (
		attachChipDp    = 32  // 芯片高（胶囊 48 内垂直居中）
		attachNameMaxDp = 200 // 文件名显示预算（超宽截断）
	)
	return u.attachClear.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		h := gtx.Dp(attachChipDp)
		padX, iconD, gap := gtx.Dp(10), gtx.Dp(12), gtx.Dp(6)
		budget := gtx.Dp(attachNameMaxDp)
		if max := gtx.Constraints.Max.X - 2*padX - iconD - 3*gap - gtx.Dp(14); budget > max {
			budget = max // 胶囊窄时让位（编辑器至少留一行宽）
		}
		name := u.complFitText(gtx, filepath.Base(u.m.stagedFile), budget)
		nameOp, nameDims := complMeasure(gtx, func(gtx layout.Context) layout.Dimensions {
			s := material.Body2(u.th, name)
			s.TextSize = unit.Sp(13)
			return s.Layout(gtx)
		})
		w := 2*padX + iconD + gap + nameDims.Size.X + gap + gtx.Dp(14)
		box := image.Rectangle{Max: image.Pt(w, h)}
		paint.FillShape(gtx.Ops, tipBg, clip.UniformRRect(box, h/2).Op(gtx.Ops))
		// 回形针（品牌色小图）。
		off := op.Offset(image.Pt(padX, (h-iconD)/2)).Push(gtx.Ops)
		drawPaperclip(gtx, image.Rectangle{Max: image.Pt(iconD, iconD)})
		off.Pop()
		// 文件名。
		tr := op.Offset(image.Pt(padX+iconD+gap, (h-nameDims.Size.Y)/2)).Push(gtx.Ops)
		nameOp.Add(gtx.Ops)
		tr.Pop()
		// ×（两杆，白字色）。
		cx, cy := float32(w-padX-gtx.Dp(7)), float32(h)/2
		a := float32(gtx.Dp(5))
		var p clip.Path
		p.Begin(gtx.Ops)
		p.MoveTo(f32.Pt(cx-a, cy-a))
		p.LineTo(f32.Pt(cx+a, cy+a))
		p.MoveTo(f32.Pt(cx+a, cy-a))
		p.LineTo(f32.Pt(cx-a, cy+a))
		paint.FillShape(gtx.Ops, whiteText, clip.Stroke{Path: p.End(), Width: float32(gtx.Dp(2))}.Op())
		return layout.Dimensions{Size: box.Size()}
	})
}

// attachSlotRect 附件槽窗口系矩形（D85 tooltip 直采；布局拓扑 [pad16][附件槽20]…，
// 内容按终位整盒排版 → 锚定 pillEnd 左缘）。
func attachSlotRect(pillEnd image.Rectangle, padX, rowH, iconPx int) image.Rectangle {
	y := pillEnd.Min.Y + (rowH-iconPx)/2
	x := pillEnd.Min.X + padX
	return image.Rect(x, y, x+iconPx, y+iconPx)
}

// drawPaperclip 附件图标占位（灰、不可点）：回形针折线 = 三段直线 + 三段半圆（24 视图的
// 中心线 (2.005,1.39)–(21.44,22) 等比 s=0.8 缩进 20dp 槽居中，描边 2dp 按稿）。
func drawPaperclip(gtx layout.Context, box image.Rectangle) {
	if box.Dx() <= 0 || box.Dy() <= 0 {
		return
	}
	const s = 0.8
	const bw, bh = 15.548, 16.488 // 缩后中心线尺寸
	ox := float32(box.Min.X) + (float32(box.Dx())-bw)/2 - 2.005*s
	oy := float32(box.Min.Y) + (float32(box.Dy())-bh)/2 - 1.39*s
	at := func(x, y float32) f32.Point { return f32.Point{X: ox + x*s, Y: oy + y*s} }

	var p clip.Path
	p.Begin(gtx.Ops)
	p.MoveTo(at(21.44, 11.05)) // 外端（右侧中）
	p.LineTo(at(12.25, 20.24)) // 沿外线下行到底
	c1 := at(8.005, 16)        // 大半圆 r=6（底 → 左）
	m1 := at(3.762, 20.243)
	cubicArc(&p, c1, at(12.25, 20.24), m1)
	cubicArc(&p, c1, m1, at(3.76, 11.75))
	p.LineTo(at(12.95, 2.56)) // 上行到顶
	c2 := at(15.78, 5.39)     // 中半圆 r=4（顶 → 右）
	m2 := at(18.61, 2.56)
	cubicArc(&p, c2, at(12.95, 2.56), m2)
	cubicArc(&p, c2, m2, at(18.61, 8.22))
	p.LineTo(at(9.41, 17.41)) // 内线斜下
	c3 := at(7.995, 15.995)   // 小半圆 r=2（内底 → 内左）
	m3 := at(6.58, 17.41)
	cubicArc(&p, c3, at(9.41, 17.41), m3)
	cubicArc(&p, c3, m3, at(6.58, 14.58))
	p.LineTo(at(15.07, 6.1)) // 内端（收尾）
	paint.FillShape(gtx.Ops, iconDim, clip.Stroke{Path: p.End(), Width: float32(gtx.Dp(2))}.Op())
}

// record 登记可见元素（D44–D48、D62）：保存真实轮廓 + 可见裁剪区 + 底色，全帧合成的
// vis 覆盖度（fadeFrame）由此推导。零半径、裁剪后为空的元素不登记；带内元素**必须登记**
// （D62：带渐变只作用于登记形状的像素——取代旧「带整行单绘」机制）。
func (u *UI) record(abs image.Rectangle, radius int, fill color.NRGBA, clipRect image.Rectangle) {
	if radius <= 0 || abs.Empty() {
		return
	}
	vis := abs.Intersect(clipRect)
	if vis.Empty() {
		return
	}
	u.shapes = append(u.shapes, drawShape{outline: abs, clip: clipRect, radius: radius, fill: fill})
}

// featherWidth 元素边缘渐隐带的宽（px，D47/D48 响应式 + D70 全元素统一）：带宽恒取
// **输入胶囊的带宽** round(Dp(inputRowDp)×featherRatio)——同屏各元素边缘剖面一致（按
// 元素自身短边算会让气泡/chip/胶囊各得不同带宽）；仍夹到 [Dp(featherMinDp),
// Dp(featherMaxDp)] 且不超过该元素短边 1/3（极小元素防被整带吃掉）。Dp 换算保证
// DPI/缩放跟随、不写死 px。纯逻辑，可测。
func featherWidth(m unit.Metric, w, h int) int {
	short := w
	if h < short {
		short = h
	}
	f := int(float64(m.Dp(inputRowDp))*featherRatio + 0.5)
	if lo := m.Dp(featherMinDp); f < lo {
		f = lo
	}
	if hi := m.Dp(featherMaxDp); f > hi {
		f = hi
	}
	if lim := short / 3; f > lim {
		f = lim
	}
	return f
}
