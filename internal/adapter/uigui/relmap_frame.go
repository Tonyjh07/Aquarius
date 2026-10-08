package uigui

// relmap_frame.go 右图自绘 + 手势 + 右键菜单（D120⑦，relation-map-design §5.2/§5.4/§6.3）。
//
// 绘制符号全部由派生纯函数（relmap.go）喂入：fill = depthMap[Level mod 4]、shape = Role
// 四形、直径 = weight 查表（A4：与 degree 无关）、描边 = 主干亮环 ⊕ 分叉虚线环、边色 =
// source 填充色、边型 = EdgeKind（顺接实线 / 版本链虚线）、标签 = 权重预算 + zoom 字号。
//
// 手势（§5.2 动词表）：拖拽空白 = pan；滚轮 = zoom（夹 [0.5,1.5]，**越界降级纵向平移**
// A9）；悬停 = 陈述 tooltip（inspect）；左键节点 = /goto <id>（A10）；右键节点 = 菜单
// （复制 ID / 删除 / 回到当前）。
//
// 性能纪律（§5.4）：视口外扩裁剪、懒重排（平移/缩放/悬停不重排——重排只由数据变更 /
// rebudget 触发，R3）。

import (
	"image"
	"image/color"
	"io"
	"math"
	"strings"

	"gioui.org/f32"
	"gioui.org/gesture"
	"gioui.org/io/clipboard"
	"gioui.org/io/event"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"

	"github.com/Tonyjh07/Aquarius/internal/port"
)

// 右图档位（偶然实现，§7 可调；zoom 边界即 A9 的钳制区间）。
const (
	relZoomMin    = 0.5
	relZoomMax    = 1.5
	relZoomStep   = 1.1  // 每格滚轮缩放倍率
	relNodeBaseDp = 26.0 // w=1.00 的直径基准（dp）；直径 = weight × base（A4）
	relRingW      = 2.5  // 主干亮环描边宽
	relEdgeW      = 1.5  // 边线宽
	relLabelBase  = 12.0 // 标签基准字号（sp）× zoom，钳 [10,16]（A9）
	relLabelMinSp = 10.0
	relLabelMaxSp = 16.0
	relCullPad    = 0.2 // 视口外扩 20% 裁剪
	relDashOn     = 6.0 // 版本链虚线段长（dp）
	relDashOff    = 5.0 // 版本链虚线间隔（dp）
	relHitSlop    = 6.0 // 命中判定外扩（dp）
	relDragSlop   = 4.0 // 按下到拖动的位移阈值（dp；未越过 = 点击）
	relWheelMax   = 1e6 // 滚轮范围上限（放开；主窗 D71 钉点口径不适用本窗）
	relArrowSize  = 6.0 // 箭头半长（dp × zoom）
)

// relGraph 右图帧态（historyState 持有；仅历史窗 goroutine 读写）。
type relGraph struct {
	model  *relModel
	vis    relVisible
	geo    []relGeoNode
	anchor int
	sol    *relSolution

	zoom         float64 // 视场倍率 [relZoomMin, relZoomMax]（A9）
	offX, offY   float64 // 视口平移（screen = world×zoom + off）
	viewW, viewH float64 // 视口尺寸（dp，帧首刷新）
	focusPending bool    // 下帧居中锚点（开窗/回到当前/重排，A8）

	tag    int            // 原始指针 tag（悬停/点击/右键/平移）
	scroll gesture.Scroll // 滚轮（范围放开的纵向滚动 → wheel()）

	hover int // 悬停节点（本地索引；-1 无）

	// 主键按下：未越过拖拽 slop = 点击（释放同点 → /goto，A10）；越过 = 平移。
	pressed bool
	// 平移基准（绝对跟踪，主窗拖动铁律 2 同款：按下记起点，拖动按差值重算——不累计增量）。
	pressNode                  int
	dragging                   bool
	panStartOffX, panStartOffY float64
	panStartX, panStartY       float32

	menuOpen bool // 右键菜单（节点上下文）
	menuNode int  // 菜单所属节点（本地索引）
	menuX    float32
	menuY    float32
	menuCopy widget.Clickable
	menuRm   widget.Clickable
	menuHome widget.Clickable
}

// newRelGraph 构造右图帧态（默认 zoom = 1.0，重排后首帧居中锚点）。
func newRelGraph() *relGraph {
	return &relGraph{zoom: 1.0, hover: -1, pressNode: -1, tag: 1}
}

// rebuild 数据变更 / rebudget 时重排（R3）：派生几何 → 求解 → 重置交互态并居中锚点。
func (g *relGraph) rebuild(m *relModel, vis relVisible) {
	g.model = m
	g.vis = vis
	g.geo, g.anchor = relGeo(m, vis)
	if g.anchor < 0 && len(g.geo) > 0 {
		g.anchor = 0
	}
	if sol, ok := relSolve(g.geo); ok {
		g.sol = sol
	} else {
		g.sol = &relSolution{Pos: make([]relPoint, len(g.geo))}
	}
	g.hover = -1
	g.pressed = false
	g.pressNode = -1
	g.menuOpen = false
	g.focusPending = true // A8：开窗/重排后视场居中锚点
}

// nodeRadius 节点屏幕半径（dp → 屏幕）：weight × 基准 ÷ 2 × zoom（A4 与 degree 无关）。
func (g *relGraph) nodeRadius(m *relNode) float64 {
	return m.weight * relNodeBaseDp / 2
}

// toScreen 世界 → 屏幕。
func (g *relGraph) toScreen(p relPoint) (float32, float32) {
	return float32(p.X*g.zoom + g.offX), float32(p.Y*g.zoom + g.offY)
}

// zoomAt 以视口内点 (cx,cy) 为不动点缩放（屏幕世界点固定）。
func (g *relGraph) zoomAt(cx, cy, factor float64) {
	old := g.zoom
	z := old * factor
	if z < relZoomMin {
		z = relZoomMin
	}
	if z > relZoomMax {
		z = relZoomMax
	}
	if z == old {
		return
	}
	wx := (cx - g.offX) / old
	wy := (cy - g.offY) / old
	g.zoom = z
	g.offX = cx - wx*z
	g.offY = cy - wy*z
}

// wheel 滚轮（口径同主窗转写区：d<0 = 滚上，d>0 = 滚下）：滚上放大、滚下缩小；
// 钳制边界后**降级为纵向平移**（A9，不静默失效）。
func (g *relGraph) wheel(d int) {
	cx, cy := g.viewW/2, g.viewH/2
	if d < 0 { // 滚上 = 放大
		if g.zoom < relZoomMax {
			g.zoomAt(cx, cy, relZoomStep)
		} else {
			g.offY += 16 * float64(d) // 越界：同一手势改为纵向平移（向上看更早内容）
		}
	} else { // 滚下 = 缩小
		if g.zoom > relZoomMin {
			g.zoomAt(cx, cy, 1/relZoomStep)
		} else {
			g.offY += 16 * float64(d) // 越界：向下看更晚内容
		}
	}
}

// focusAnchor 视场居中锚点（A8：布局器输出的锚点坐标，不窥探内部结构）。
func (g *relGraph) focusAnchor() {
	if g.sol == nil || g.anchor < 0 || g.anchor >= len(g.sol.Pos) {
		return
	}
	p := g.sol.Pos[g.anchor]
	g.offX = g.viewW/2 - p.X*g.zoom
	g.offY = g.viewH/2 - p.Y*g.zoom
}

// hitTest 屏幕点 → 命中的可见节点（本地索引；-1 无）。按绘制半径 + 外扩判定。
func (g *relGraph) hitTest(sx, sy float32) int {
	if g.sol == nil {
		return -1
	}
	for li, gi := range g.vis.nodes {
		xs, ys := g.toScreen(g.sol.Pos[li])
		r := float64(g.nodeRadius(&g.model.nodes[gi]) * g.zoom)
		dx, dy := float64(xs-sx), float64(ys-sy)
		if dx*dx+dy*dy <= (r+relHitSlop)*(r+relHitSlop) {
			return li
		}
	}
	return -1
}

// frame 右图单帧：手势消费 → 居中锚点 → 绘制 → tooltip → 菜单。
func (g *relGraph) frame(gtx layout.Context, th *material.Theme, u *UI, st *historyState) layout.Dimensions {
	g.viewW, g.viewH = float64(gtx.Constraints.Max.X), float64(gtx.Constraints.Max.Y)
	if g.focusPending {
		g.focusPending = false
		g.focusAnchor()
	}
	g.update(gtx, u, st)
	if g.sol == nil {
		return layout.Center.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			l := material.Body2(th, "关系图未就绪")
			l.Color = textMuted
			return l.Layout(gtx)
		})
	}
	g.draw(gtx, th, u, st)
	g.drawTooltip(gtx, th, st)
	g.drawMenu(gtx, th, u, st)
	return layout.Dimensions{Size: gtx.Constraints.Max}
}

// update 手势状态机：平移 / 缩放（gesture.Scroll）/ 悬停 / 点击 / 右键菜单。
func (g *relGraph) update(gtx layout.Context, u *UI, st *historyState) {
	// 滚轮：范围放开（主窗转写区的 D71 钉点口径不适用本窗，§10.2）。
	if d := g.scroll.Update(gtx.Metric, gtx.Source, gtx.Now, gesture.Vertical,
		pointer.ScrollRange{}, pointer.ScrollRange{Min: -relWheelMax, Max: relWheelMax}); d != 0 {
		g.wheel(d)
	}
	for {
		ev, ok := gtx.Event(pointer.Filter{
			Target: &g.tag,
			Kinds: pointer.Press | pointer.Release | pointer.Move | pointer.Drag |
				pointer.Enter | pointer.Leave | pointer.Cancel,
		})
		if !ok {
			break
		}
		e := ev.(pointer.Event)
		switch e.Kind {
		case pointer.Move, pointer.Enter:
			if !g.menuOpen {
				g.hover = g.hitTest(e.Position.X, e.Position.Y)
			}
		case pointer.Leave, pointer.Cancel:
			g.hover = -1
			if e.Kind == pointer.Cancel {
				g.pressed = false
				g.pressNode = -1
				g.dragging = false
			}
		case pointer.Scroll:
			g.wheel(int(e.Scroll.Y))
		case pointer.Press:
			if e.Buttons.Contain(pointer.ButtonSecondary) {
				if h := g.hitTest(e.Position.X, e.Position.Y); h >= 0 {
					g.menuOpen = true
					g.menuNode = h
					g.menuX, g.menuY = e.Position.X, e.Position.Y
				}
				continue
			}
			if e.Buttons.Contain(pointer.ButtonPrimary) {
				if g.menuOpen {
					// 点菜单项（矩形内）→ 交给 Clickable 处理；菜单外 → 关闭。
					if e.Position.X >= g.menuX && e.Position.X <= g.menuX+float32(gtx.Dp(120)) &&
						e.Position.Y >= g.menuY && e.Position.Y <= g.menuY+float32(gtx.Dp(26)*3) {
						continue
					}
					g.menuOpen = false
					continue
				}
				g.pressed = true
				g.pressNode = g.hitTest(e.Position.X, e.Position.Y)
				g.panStartOffX, g.panStartOffY = g.offX, g.offY
				g.panStartX, g.panStartY = e.Position.X, e.Position.Y
				g.dragging = false
			}
		case pointer.Drag:
			if g.pressed {
				dx := float64(e.Position.X - g.panStartX)
				dy := float64(e.Position.Y - g.panStartY)
				if !g.dragging && dx*dx+dy*dy > relDragSlop*relDragSlop {
					g.dragging = true
				}
				if g.dragging {
					g.offX = g.panStartOffX + dx
					g.offY = g.panStartOffY + dy
				}
			}
		case pointer.Release:
			if g.pressed {
				g.pressed = false
				if !g.dragging && g.pressNode >= 0 &&
					g.pressNode == g.hitTest(e.Position.X, e.Position.Y) {
					g.gotoNode(u, st, g.pressNode)
				}
				g.pressNode = -1
				g.dragging = false
			}
		}
	}
	event.Op(gtx.Ops, &g.tag)
	g.scroll.Add(gtx.Ops)
}

// gotoNode 点节点 → 投 /goto <id>（A10；与键入同路径串行执行，缓冲满丢弃）。
func (g *relGraph) gotoNode(u *UI, st *historyState, li int) {
	if g.model == nil || li < 0 || li >= len(g.vis.nodes) {
		return
	}
	id := g.model.nodes[g.vis.nodes[li]].id
	select {
	case u.inCh <- port.UserInput{Command: &port.Command{Name: "goto", Args: []string{string(id)}}}:
	default:
	}
}

// draw 一帧：边（source 色 + 线型）→ 节点（形状/填充/描边）→ 标签（预算）。
func (g *relGraph) draw(gtx layout.Context, th *material.Theme, u *UI, st *historyState) {
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
		fill := depthMap[relPaletteIndex(g.model.nodes[from].level)] // 边色 = source 填充色
		g.drawEdge(gtx, fx, fy, tx, ty, fill, g.model.nodes[to].edgeKind == port.EdgeRevise)
	}
	for li, gi := range g.vis.nodes {
		xs, ys := g.toScreen(g.sol.Pos[li])
		if !relInBox(vp, xs, ys) {
			continue
		}
		m := g.model.nodes[gi]
		r := float32(g.nodeRadius(&m) * g.zoom)
		g.drawNode(gtx, th, g.model, gi, xs, ys, r)
		if txt := relLabelText(g.model, gi, g.zoom); txt != "" {
			sp := g.labelFont()
			l := material.Label(th, unit.Sp(sp), txt)
			l.Color = th.Fg
			off := op.Offset(image.Pt(int(xs)+int(r)+4, int(ys)-int(r))).Push(gtx.Ops)
			l.Layout(gtx)
			off.Pop()
		}
	}
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
func (g *relGraph) drawEdge(gtx layout.Context, ax, ay, bx, by float32, fill color.NRGBA, revise bool) {
	dx, dy := bx-ax, by-ay
	dist := float32(math.Hypot(float64(dx), float64(dy)))
	if dist < 1 {
		return
	}
	ux, uy := dx/dist, dy/dist
	if revise {
		step := float32(relDashOn+relDashOff) * float32(g.zoom)
		n := int(dist / step)
		for k := 0; k <= n; k++ {
			s := float32(k) * step
			e := s + float32(relDashOn)*float32(g.zoom)
			if e > dist {
				e = dist
			}
			g.strokeLine(gtx, ax+ux*s, ay+uy*s, ax+ux*e, ay+uy*e, fill)
		}
	} else {
		g.strokeLine(gtx, ax, ay, bx, by, fill)
	}
	g.arrowHead(gtx, ax, ay, bx, by, fill)
}

// strokeLine 直线段（细描边）。
func (g *relGraph) strokeLine(gtx layout.Context, ax, ay, bx, by float32, c color.NRGBA) {
	var p clip.Path
	p.Begin(gtx.Ops)
	p.MoveTo(f32.Pt(ax, ay))
	p.LineTo(f32.Pt(bx, by))
	paint.FillShape(gtx.Ops, c,
		clip.Stroke{Path: p.End(), Width: relEdgeW * float32(g.zoom)}.Op())
}

// arrowHead 子端小三角（父 → 子方向）。
func (g *relGraph) arrowHead(gtx layout.Context, ax, ay, bx, by float32, fill color.NRGBA) {
	dx, dy := bx-ax, by-ay
	dist := float32(math.Hypot(float64(dx), float64(dy)))
	if dist < 1 {
		return
	}
	ux, uy := dx/dist, dy/dist
	px, py := -uy, ux // 垂直方向
	s := float32(relArrowSize) * float32(g.zoom)
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

// drawNode 节点：填充（Level mod 4 深度色带）+ 形状（Role）+ 描边（主干亮环 ⊕ 分叉虚线环）。
func (g *relGraph) drawNode(gtx layout.Context, th *material.Theme, m *relModel, gi int, xs, ys, r float32) {
	fill := depthMap[relPaletteIndex(m.nodes[gi].level)]
	box := image.Rect(int(xs-r), int(ys-r), int(xs+r), int(ys+r))
	switch relShapeOf(m.nodes[gi].role) {
	case relShapeSquare:
		paint.FillShape(gtx.Ops, fill, clip.UniformRRect(box, int(r*0.3)).Op(gtx.Ops))
	case relShapeDiamond:
		g.diamond(gtx, xs, ys, r, fill)
	case relShapeRing: // 空心圆（结构根）
		paint.FillShape(gtx.Ops, fill, clip.UniformRRect(box, int(r)).Op(gtx.Ops))
		inner := r * 0.55
		ib := image.Rect(int(xs-inner), int(ys-inner), int(xs+inner), int(ys+inner))
		paint.FillShape(gtx.Ops, windowBg, clip.UniformRRect(ib, int(inner)).Op(gtx.Ops))
	default: // circle
		paint.FillShape(gtx.Ops, fill, clip.UniformRRect(box, int(r)).Op(gtx.Ops))
	}
	// 主干亮环（A11 一眼可辨）
	if m.nodes[gi].inPath {
		ring := clip.UniformRRect(box, int(r)).Path(gtx.Ops)
		paint.FillShape(gtx.Ops, whiteText,
			clip.Stroke{Path: ring, Width: relRingW * float32(g.zoom)}.Op())
	}
	// 分叉位点 = 细虚线外环（不改大小，A4 的描边通道）
	if m.nodes[gi].sibCount > 1 {
		g.dashedRing(gtx, xs, ys, r+float32(relRingW)*float32(g.zoom))
	}
}

// diamond 菱形（system：人格 / 压缩摘要）。
func (g *relGraph) diamond(gtx layout.Context, xs, ys, r float32, fill color.NRGBA) {
	var p clip.Path
	p.Begin(gtx.Ops)
	p.MoveTo(f32.Pt(xs, ys-r))
	p.LineTo(f32.Pt(xs+r*0.75, ys))
	p.LineTo(f32.Pt(xs, ys+r))
	p.LineTo(f32.Pt(xs-r*0.75, ys))
	p.Close()
	paint.FillShape(gtx.Ops, fill, clip.Outline{Path: p.End()}.Op())
}

// dashedRing 分叉位点虚线外环（沿圆周等分短弧）。
func (g *relGraph) dashedRing(gtx layout.Context, xs, ys, r float32) {
	const segs = 24
	for k := 0; k < segs; k += 2 {
		a0 := float32(k) / segs * 2 * math.Pi
		a1 := float32(k+1) / segs * 2 * math.Pi
		x0 := xs + r*float32(math.Cos(float64(a0)))
		y0 := ys + r*float32(math.Sin(float64(a0)))
		x1 := xs + r*float32(math.Cos(float64(a1)))
		y1 := ys + r*float32(math.Sin(float64(a1)))
		g.strokeLine(gtx, x0, y0, x1, y1, textMuted)
	}
}

// drawTooltip 悬停陈述（inspect，§3.4：一句话自足陈述）。
func (g *relGraph) drawTooltip(gtx layout.Context, th *material.Theme, st *historyState) {
	if g.menuOpen || g.hover < 0 || g.sol == nil || g.model == nil || g.hover >= len(g.sol.Pos) {
		return
	}
	xs, ys := g.toScreen(g.sol.Pos[g.hover])
	text := relStatementOf(g.model, g.vis.nodes[g.hover], st.title())
	if text == "" {
		return
	}
	l := material.Label(th, unit.Sp(11), truncRunes(text, 48))
	l.Color = whiteText
	x, y := int(xs)+10, int(ys)+10
	if float64(x+260) > g.viewW {
		x = int(xs) - 270
	}
	if float64(y+44) > g.viewH {
		y = int(ys) - 44
	}
	off := op.Offset(image.Pt(x, y)).Push(gtx.Ops)
	layout.Stack{}.Layout(gtx,
		layout.Expanded(func(gtx layout.Context) layout.Dimensions {
			paint.FillShape(gtx.Ops, tipBg, clip.RRect{Rect: image.Rectangle{Max: gtx.Constraints.Max},
				SE: 6, SW: 6, NE: 6, NW: 6}.Op(gtx.Ops))
			return layout.Dimensions{}
		}),
		layout.Stacked(func(gtx layout.Context) layout.Dimensions {
			return layout.UniformInset(unit.Dp(6)).Layout(gtx, l.Layout)
		}),
	)
	off.Pop()
}

// drawMenu 右键菜单：复制 ID / 删除 / 回到当前。点击判定须**先于绘制**（Clickable 的
// Layout 也会消费程序化点击 requestClicks，先判后绘才可靠）；命中即就地关闭。
func (g *relGraph) drawMenu(gtx layout.Context, th *material.Theme, u *UI, st *historyState) {
	if !g.menuOpen {
		return
	}
	doCopy := g.menuCopy.Clicked(gtx)
	doRm := g.menuRm.Clicked(gtx)
	doHome := g.menuHome.Clicked(gtx)

	x, y := int(g.menuX), int(g.menuY)
	off := op.Offset(image.Pt(x, y)).Push(gtx.Ops)
	layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return g.menuItem(gtx, th, &g.menuCopy, "复制 ID")
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return g.menuItem(gtx, th, &g.menuRm, "删除（/rm）")
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return g.menuItem(gtx, th, &g.menuHome, "回到当前")
		}),
	)
	off.Pop()

	id := ""
	if g.menuNode >= 0 && g.menuNode < len(g.vis.nodes) && g.model != nil {
		id = string(g.model.nodes[g.vis.nodes[g.menuNode]].id)
	}
	switch {
	case doCopy:
		if id != "" {
			gtx.Execute(clipboard.WriteCmd{Type: "application/text",
				Data: io.NopCloser(strings.NewReader(id))})
		}
		g.menuOpen = false
	case doRm:
		if id != "" {
			select {
			case u.inCh <- port.UserInput{Command: &port.Command{Name: "rm", Args: []string{id}}}:
			default:
			}
		}
		g.menuOpen = false
	case doHome:
		g.focusPending = true
		g.menuOpen = false
	}
}

// menuItem 菜单行（clickable + 文案，悬停底色）。
func (g *relGraph) menuItem(gtx layout.Context, th *material.Theme, c *widget.Clickable, text string) layout.Dimensions {
	return c.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return layout.Stack{}.Layout(gtx,
			layout.Expanded(func(gtx layout.Context) layout.Dimensions {
				bg := pillBg
				if c.Hovered() {
					bg = cardSystem
				}
				paint.FillShape(gtx.Ops, bg, clip.RRect{Rect: image.Rectangle{Max: gtx.Constraints.Max},
					SE: 4, SW: 4, NE: 4, NW: 4}.Op(gtx.Ops))
				return layout.Dimensions{Size: image.Pt(gtx.Dp(120), gtx.Dp(26))}
			}),
			layout.Stacked(func(gtx layout.Context) layout.Dimensions {
				return layout.UniformInset(unit.Dp(4)).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					l := material.Body2(th, text)
					l.Color = th.Fg
					return l.Layout(gtx)
				})
			}),
		)
	})
}

// relInBox 屏幕点是否在视口盒内（float32 版）。
func relInBox(box image.Rectangle, x, y float32) bool {
	return x >= float32(box.Min.X) && x <= float32(box.Max.X) &&
		y >= float32(box.Min.Y) && y <= float32(box.Max.Y)
}
