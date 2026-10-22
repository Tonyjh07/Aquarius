package uigui

// relmap_draw.go 右图绘制：边、节点卡片、标签、徽标与字形、菱形/三角、虚线外环，
// 以及虚线/圆弧的采样。所有尺寸都乘 g.z（含 zoom 与 DPI 换算）。

import (
	"image"
	"image/color"
	"math"

	"gioui.org/f32"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"
	"gioui.org/widget/material"

	"github.com/Tonyjh07/Aquarius/internal/port"
)

// draw 一帧：边（source 色 + 线型）→ 节点（形状/填充/描边）→ 标签（预算）。
// 视口 clip（D121 实测修）：剔除盒外扩 20%，那些节点照画不误——没有 clip 时
// 负坐标节点会盖到左栏会话列表上。
func (g *relGraph) draw(gtx layout.Context, th *material.Theme, u *UI, st *historyState) {
	defer clip.Rect(image.Rectangle{Max: image.Pt(int(g.viewW), int(g.viewH))}).Push(gtx.Ops).Pop()
	vp := image.Rect(
		-int(g.viewW*relCullPad), -int(g.viewH*relCullPad),
		int(g.viewW*(1+relCullPad)), int(g.viewH*(1+relCullPad)))
	// 全局 → 本地索引映射：relVisible.edges 存全局索引，sol.Pos 按本地（见 §6.1 两套索引）。
	local := make(map[int]int, len(g.vis.nodes))
	for li, gi := range g.vis.nodes {
		local[gi] = li
	}
	for _, e := range g.vis.edges {
		from, to := e[0], e[1] // 全局索引
		fli, lok1 := local[from]
		tli, lok2 := local[to]
		if !lok1 || !lok2 {
			continue // 端点不在可见集（预算裁剪），跳过
		}
		fx, fy := g.toScreen(g.sol.Pos[fli])
		tx, ty := g.toScreen(g.sol.Pos[tli])
		if !relInBox(vp, fx, fy) && !relInBox(vp, tx, ty) {
			continue
		}
		fill := paperOf().depth[relPaletteIndex(g.model.nodes[from].level)] // 边色 = source 填充色
		// 主干（A11）改由**边描边 + 本色加粗**表达：先铺一条窗底色的粗线当描边（把主干
		// 从背景与相邻卡片里抬出来），再画本色细线。节点上不再套白圈。
		path := g.model.nodes[from].inPath && g.model.nodes[to].inPath
		if path {
			g.strokeLineW(gtx, fx, fy, tx, ty, paperOf().win, relEdgeW*relEdgeHalo*float32(g.z))
		}
		g.drawEdge(gtx, fx, fy, tx, ty, fill,
			g.model.nodes[to].edgeKind == port.EdgeRevise, path)
	}
	for li, gi := range g.vis.nodes {
		xs, ys := g.toScreen(g.sol.Pos[li])
		if !relInBox(vp, xs, ys) {
			continue
		}
		g.drawNodeCard(gtx, th, gi, xs, ys)
	}
}

// relCardTint 节点卡底色 = 节点层色向**窗底色**靠拢（纯函数）。卡片因此「跟着节点走」：
// 浅色主题下是同色系淡彩，深色主题下自动变深（同一个公式，两版都对）。
func relCardTint(fill, bg color.NRGBA) color.NRGBA {
	mix := func(f, b uint8) uint8 {
		return uint8(float64(f)*relCardTintF + float64(b)*(1-relCardTintF))
	}
	return color.NRGBA{R: mix(fill.R, bg.R), G: mix(fill.G, bg.G), B: mix(fill.B, bg.B), A: 0xFF}
}

// drawNodeCard 节点卡（D121「内容入内」；D126「多行占满卡片」）：标签可见时 = 卡片底 +
// 形状徽标（Role 四形 × Level 色，A2/§3.1 不变）+ 卡内标签（按盒宽换行、最多
// relLabelLines 行，超出走省略号）；标签不可见（A5：低 zoom / 低 weight）时退化为
// 「裸形状」——标签仍是预算，卡不是。
func (g *relGraph) drawNodeCard(gtx layout.Context, th *material.Theme, gi int, xs, ys float32) {
	m := g.model.nodes[gi]
	r := float32(g.nodeRadius(&m) * g.z)
	txt := relLabelText(g.model, gi, g.zoom)
	if txt == "" {
		g.drawNode(gtx, th, g.model, gi, xs, ys, r)
		return
	}
	box := g.nodeCardBox(gi, xs, ys)
	fill := paperOf().depth[relPaletteIndex(m.level)]
	paint.FillShape(gtx.Ops, relCardTint(fill, paperOf().win),
		clip.UniformRRect(box, int(relCardRoundP*r)).Op(gtx.Ops))
	g.drawNode(gtx, th, g.model, gi, xs, ys, r)
	// 标签：徽标右侧、卡片内（多行块相对徽标垂直居中）。
	badge := relBadgeBox(xs, ys, r)
	lw, lh, lines := g.labelBlock(txt)
	lx := badge.Max.X + int(relCardGapDp*g.z)
	ly := int(ys) - int(lh)/2
	off := op.Offset(image.Pt(lx, ly)).Push(gtx.Ops)
	// **精确约束**（D126 实测修）：layout.Center 按 max(Constraints.Min, 子件尺寸)
	// 定位——Min 给 (0,0) 时等于不平移，文字贴在盒子左上角（数字/文本「不居中」的
	// 根因）。Min = 盒子尺寸 → Center 才真的把子件摆到盒子中央。
	gtx.Constraints = layout.Exact(image.Pt(int(lw), int(lh)))
	l := material.Label(th, unit.Sp(g.labelFont()), txt)
	l.Color = paperOf().text
	l.MaxLines = lines // 多行占满卡片；超出行数走省略号
	l.LineHeight = unit.Sp(g.labelFont() * relLabelLineLead)
	l.Layout(gtx)
	off.Pop()
}

// labelFont 标签字号 = clamp(12×zoom, 10, 16)sp（A9 排版硬约束）。
func (g *relGraph) labelFont() float32 {
	sp := relLabelBase * g.zoom
	if sp < relLabelMinSp {
		sp = relLabelMinSp
	}
	if sp > relLabelMaxSp {
		sp = relLabelMaxSp
	}
	return float32(sp)
}

// drawEdge 边：source 色；版本链 = 虚线段（§2.3）；末端箭头 = 对话推进方向（A3）。
// path = 该边在主干上（`InPath` 两端）时本色线加粗一档（A11，与描边叠加）。
func (g *relGraph) drawEdge(gtx layout.Context, ax, ay, bx, by float32, fill color.NRGBA, revise, path bool) {
	dx, dy := bx-ax, by-ay
	dist := float32(math.Hypot(float64(dx), float64(dy)))
	if dist < 1 {
		return
	}
	ux, uy := dx/dist, dy/dist
	w := relEdgeW * float32(g.z)
	if path {
		w *= relEdgePathW
	}
	if revise {
		step := float32(relDashOn+relDashOff) * float32(g.z)
		n := int(dist / step)
		for k := 0; k <= n; k++ {
			s := float32(k) * step
			e := s + float32(relDashOn)*float32(g.z)
			if e > dist {
				e = dist
			}
			g.strokeLineW(gtx, ax+ux*s, ay+uy*s, ax+ux*e, ay+uy*e, fill, w)
		}
	} else {
		g.strokeLineW(gtx, ax, ay, bx, by, fill, w)
	}
	g.arrowHead(gtx, ax, ay, bx, by, fill)
}

// strokeLine 直线段（细描边，默认边宽）。
func (g *relGraph) strokeLine(gtx layout.Context, ax, ay, bx, by float32, c color.NRGBA) {
	g.strokeLineW(gtx, ax, ay, bx, by, c, relEdgeW*float32(g.z))
}

// strokeLineW 直线段（指定线宽）：主干描边 = 先用窗底色铺一条更粗的线，再压本色细线。
func (g *relGraph) strokeLineW(gtx layout.Context, ax, ay, bx, by float32, c color.NRGBA, w float32) {
	var p clip.Path
	p.Begin(gtx.Ops)
	p.MoveTo(f32.Pt(ax, ay))
	p.LineTo(f32.Pt(bx, by))
	paint.FillShape(gtx.Ops, c, clip.Stroke{Path: p.End(), Width: w}.Op())
}

// arrowHead 子端小三角（父 → 子方向）。
func (g *relGraph) arrowHead(gtx layout.Context, ax, ay, bx, by float32, fill color.NRGBA) {
	dx, dy := bx-ax, by-ay
	dist := float32(math.Hypot(float64(dx), float64(dy)))
	if dist < 1 {
		return
	}
	ux, uy := dx/dist, dy/dist
	px, py := -uy, ux                         // 垂直方向
	s := float32(relArrowSize) * float32(g.z) // 有效缩放（含 DPI；曾误用 g.zoom）
	tip := f32.Pt(bx, by)
	base := f32.Pt(bx-ux*s, by-uy*s)
	var p clip.Path
	p.Begin(gtx.Ops)
	p.MoveTo(tip)
	p.LineTo(f32.Pt(base.X+px*s*0.5, base.Y+py*s*0.5))
	p.LineTo(f32.Pt(base.X-px*s*0.5, base.Y-py*s*0.5))
	p.Close()
	paint.FillShape(gtx.Ops, fill, clip.Outline{Path: p.End()}.Op())
}

// drawNode 节点徽标：填充（Level mod 4 深度色带）+ 形状（Role）+ 版本下标 +
// 分叉虚线外环（沿形状）。形状画在 2r×2r 的盒里，圆/方/菱形贴边、空心圆改用**描边**
// （此前「填实心 + 内填窗底」在节点卡上会露出底色错位的洞）。主干（A11）不在节点上。
func (g *relGraph) drawNode(gtx layout.Context, th *material.Theme, m *relModel, gi int, xs, ys, r float32) {
	fill := paperOf().depth[relPaletteIndex(m.nodes[gi].level)]
	shape := relShapeOf(m.nodes[gi].role)
	box := image.Rect(int(xs-r), int(ys-r), int(xs+r), int(ys+r))
	switch shape {
	case relShapeSquare:
		paint.FillShape(gtx.Ops, fill, clip.UniformRRect(box, int(r*0.28)).Op(gtx.Ops))
	case relShapeDiamond:
		g.diamond(gtx, xs, ys, r, fill)
	case relShapeTriangle: // 注入节点（D166）：正三角，与圆/方/菱/环都不混
		g.triangle(gtx, xs, ys, r, fill)
	case relShapeRing: // 空心圆（结构根）：描边而非挖洞
		ring := clip.UniformRRect(box, int(r)).Path(gtx.Ops)
		paint.FillShape(gtx.Ops, fill,
			clip.Stroke{Path: ring, Width: float32(relRingW*1.6) * float32(g.z)}.Op())
	default: // circle
		paint.FillShape(gtx.Ops, fill, clip.UniformRRect(box, int(r)).Op(gtx.Ops))
	}
	// 版本下标写进**图形内部**（D121）：菱形中心宽度为 0、空心圆无底，都放不下字——
	// 那两种形状不落字（分叉位点几乎都是 user/assistant，够用）。
	if shape != relShapeDiamond && shape != relShapeRing {
		if glyph := relVersionGlyph(m, gi); glyph != "" {
			g.drawGlyph(gtx, th, xs, ys, r, glyph)
		}
	}
	// 主干（A11）由**边**表达（描边 + 本色加粗，见 draw）：白圈会把形状切出白口、
	// 且在卡片里读作「贴纸边框」。分叉位点的虚线外环保留（另一条通道），且
	// **沿本节点自身形状**走（D121：圆节点套方虚线框读作两套语言）。
	if m.nodes[gi].sibCount > 1 {
		g.dashedOutline(gtx, xs, ys, r+float32(relRingW)*float32(g.z), shape)
	}
}

// drawGlyph 版本下标：白字居中于形状内（字号随半径缩放，小节点也不糊成一坨）。
// **精确约束 + Center**（D126 实测修）：Gio 的 Center 按 max(Constraints.Min, 子件尺寸)
// 定位——Min 给 (0,0) 时等于不平移，数字贴在形状左上角（用户报障「数字居中问题」）；
// Min = 盒子尺寸才真的居中。反过来少了 Center 则退化为左顶对齐，一样不居中。
func (g *relGraph) drawGlyph(gtx layout.Context, th *material.Theme, xs, ys, r float32, txt string) {
	badge := relBadgeBox(xs, ys, r*0.82)
	sp := math.Max(8, float64(r)*0.95)
	off := op.Offset(badge.Min).Push(gtx.Ops)
	gtx.Constraints = layout.Exact(badge.Size())
	layout.Center.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		l := material.Label(th, unit.Sp(float32(sp)), txt)
		l.Color = paperOf().accentFg
		l.MaxLines = 1
		return l.Layout(gtx)
	})
	off.Pop()
}

// diamond 菱形（system：人格 / 压缩摘要）。半对角取 0.78r：满格菱形的尖会顶出卡片
// 内边距，看起来像被切了一刀。
func (g *relGraph) diamond(gtx layout.Context, xs, ys, r float32, fill color.NRGBA) {
	d := r * 0.78
	var p clip.Path
	p.Begin(gtx.Ops)
	p.MoveTo(f32.Pt(xs, ys-d))
	p.LineTo(f32.Pt(xs+d, ys))
	p.LineTo(f32.Pt(xs, ys+d))
	p.LineTo(f32.Pt(xs-d, ys))
	p.Close()
	paint.FillShape(gtx.Ops, fill, clip.Outline{Path: p.End()}.Op())
}

// triangle 正三角（inject：注入上下文，D166）。外接半径 0.95r——顶点朝上，底边
// 只向下压 0.475r，整体仍在 2r 盒内（与菱形同口径，尖不顶出卡片内边距）。
func (g *relGraph) triangle(gtx layout.Context, xs, ys, r float32, fill color.NRGBA) {
	d := r * 0.95
	var p clip.Path
	p.Begin(gtx.Ops)
	p.MoveTo(f32.Pt(xs, ys-d))
	p.LineTo(f32.Pt(xs+relTriCos*d, ys+relTriSin*d))
	p.LineTo(f32.Pt(xs-relTriCos*d, ys+relTriSin*d))
	p.Close()
	paint.FillShape(gtx.Ops, fill, clip.Outline{Path: p.End()}.Op())
}

// 正三角的两个方向常量（cos30°/sin30°）：填充与虚线轮廓共用同一组顶点。
const (
	relTriCos = float32(0.8660254)
	relTriSin = float32(0.5)
)

// dashedOutline 分叉位点的虚线外环，**沿节点自身形状**走（D121）：圆走圆、方走圆角
// 方、菱形走菱——圆节点套方虚线框会读成两套语言。
func (g *relGraph) dashedOutline(gtx layout.Context, xs, ys, r float32, shape relShape) {
	step := (relDashOn + relDashOff) * float64(g.z)
	stroke := relEdgeW * float32(g.z)
	pts := shapeOutline(shape, xs, ys, r, step)
	for i := 0; i+1 < len(pts); i += 2 {
		g.strokeLineW(gtx, pts[i].X, pts[i].Y, pts[i+1].X, pts[i+1].Y, paperOf().text3, stroke)
	}
}

// shapeOutline 形状轮廓的折线采样（步长 step；虚段 = 一步、间隔 = 一步，故成对取点）。
func shapeOutline(shape relShape, xs, ys, r float32, step float64) []f32.Point {
	var pts []f32.Point
	switch shape {
	case relShapeDiamond:
		v := [4]f32.Point{
			{X: xs, Y: ys - r}, {X: xs + r, Y: ys}, {X: xs, Y: ys + r}, {X: xs - r, Y: ys},
		}
		for i := 0; i < 4; i++ {
			pts = relSampleSeg(pts, v[i], v[(i+1)%4], step)
		}
	case relShapeTriangle:
		d := r * 0.95 // 与填充同值
		v := [3]f32.Point{
			{X: xs, Y: ys - d},
			{X: xs + relTriCos*d, Y: ys + relTriSin*d},
			{X: xs - relTriCos*d, Y: ys + relTriSin*d},
		}
		for i := 0; i < 3; i++ {
			pts = relSampleSeg(pts, v[i], v[(i+1)%3], step)
		}
	case relShapeSquare:
		cr := r * 0.28 // 与填充的圆角半径同值
		cen := [4]f32.Point{
			{X: xs + r - cr, Y: ys - r + cr}, // 右上
			{X: xs + r - cr, Y: ys + r - cr}, // 右下
			{X: xs - r + cr, Y: ys + r - cr}, // 左下
			{X: xs - r + cr, Y: ys - r + cr}, // 左上
		}
		for i := 0; i < 4; i++ {
			a0 := -math.Pi/2 + float64(i)*math.Pi/2
			a1 := a0 + math.Pi/2
			pts = relSampleArc(pts, cen[i], cr, a0, a1, step)
			// 直边必须贴 **±r**（= 填充圆角方的边所在），端点是「本角弧终点 → 下一角弧
			// 起点」。D126 实测修：旧版把直边画在 r−cr（弧心列）上，比填充内缩一个角
			// 半径（≈6.8px）——左右（上下）虚线画进徽标里，读作「虚线框嵌在节点中」。
			j := (i + 1) % 4
			b0 := -math.Pi/2 + float64(j)*math.Pi/2
			pts = relSampleSeg(pts, relArcPoint(cen[i], cr, a1), relArcPoint(cen[j], cr, b0), step)
		}
	default: // circle / ring
		n := relOutlineSegs // 固定段数：步长过小时不至于采样出上万点
		per := 2 * math.Pi / float64(n)
		for i := 0; i < n; i++ {
			a := float64(i) * per
			p0 := f32.Pt(xs+r*float32(math.Cos(a)), ys+r*float32(math.Sin(a)))
			a1 := a + per/2
			p1 := f32.Pt(xs+r*float32(math.Cos(a1)), ys+r*float32(math.Sin(a1)))
			pts = append(pts, p0, p1)
		}
	}
	return pts
}

// relArcPoint 弧上一点的屏幕坐标（弧心 + 角）。
func relArcPoint(c f32.Point, r float32, a float64) f32.Point {
	return f32.Pt(c.X+r*float32(math.Cos(a)), c.Y+r*float32(math.Sin(a)))
}

// relSampleSeg 直线段按步长采样（虚段/间隔交替成对取点）。
func relSampleSeg(pts []f32.Point, a, b f32.Point, step float64) []f32.Point {
	d := math.Hypot(float64(b.X-a.X), float64(b.Y-a.Y))
	n := int(d / step)
	if n < 1 {
		n = 1
	}
	for i := 0; i < n; i++ {
		t0, t1 := float64(2*i)/float64(2*n), float64(2*i+1)/float64(2*n)
		p0 := f32.Pt(a.X+float32(t0)*(b.X-a.X), a.Y+float32(t0)*(b.Y-a.Y))
		p1 := f32.Pt(a.X+float32(t1)*(b.X-a.X), a.Y+float32(t1)*(b.Y-a.Y))
		pts = append(pts, p0, p1)
	}
	return pts
}

// relSampleArc 圆弧按步长采样（用于圆角方块的四个角）。
func relSampleArc(pts []f32.Point, c f32.Point, r float32, from, to, step float64) []f32.Point {
	sweep := math.Abs(to - from)
	if sweep <= 0 {
		return pts
	}
	n := int(sweep * float64(r) / step)
	if n < 1 {
		n = 1
	}
	for i := 0; i < n; i++ {
		t0 := from + sweep*float64(2*i)/float64(2*n)
		t1 := from + sweep*float64(2*i+1)/float64(2*n)
		pts = append(pts,
			f32.Pt(c.X+r*float32(math.Cos(t0)), c.Y+r*float32(math.Sin(t0))),
			f32.Pt(c.X+r*float32(math.Cos(t1)), c.Y+r*float32(math.Sin(t1))))
	}
	return pts
}
