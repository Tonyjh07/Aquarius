package uigui

// relmap_drag_test.go 右图拖拽与物理：按住期间续帧、节点钉住后落定、近失手
// 释放、取消释放、吸附光标，以及开窗展开的物理收敛。

import (
	"math"
	"testing"
	"time"

	"gioui.org/f32"
	"gioui.org/io/input"
	"gioui.org/io/pointer"
	"github.com/Tonyjh07/Aquarius/internal/domain/conversation"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

// TestRelGraphTwoNodeDragKeepsAnimating 2026-10-18 实测修：**少节点**（会话根 + 人格两个
// 节点）下快速拖动后，树会僵在非平衡态，「再拖一次」才继续动。
//
// 根因两处：① 源档的 friction 既是步长又停判时钟，按住期间每帧衰减——两个节点时邻居
// 早就到位、逐帧位移判停立刻成立，拖动第十几帧就 settled；② tickLayout 只在「抓取偏移
// 未衰减完」时续帧，于是指针停住后不再有下一帧把树拉回平衡。松手只清 settled、不拨回
// 时钟，步长≈0 ⇒ 首帧即判停（画面僵住）；再拖一次走 warmUp 才复活。
//
// 断言：按住期间 tickLayout 恒续帧（不被 settled 掐断）、时钟保持初值；松手后计时重上、
// 且能在续帧预算内正常收敛。
func TestRelGraphTwoNodeDragKeepsAnimating(t *testing.T) {
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	mk := func(id, parent string, role conversation.Role, i int) port.GraphNode {
		return port.GraphNode{
			ID: conversation.MessageID(id), Parent: conversation.MessageID(parent),
			Role: role, Snippet: "s-" + id, EdgeKind: port.EdgeSeq,
			SiblingIdx: 0, SiblingCount: 1, CreatedAt: base.Add(time.Duration(i) * time.Second),
		}
	}
	g := port.TreeGraph{
		Anchor: "p",
		Nodes: []port.GraphNode{
			mk("r", "", conversation.RoleRoot, 0),
			mk("p", "r", conversation.RoleSystem, 1),
		},
	}
	g.Total = len(g.Nodes)
	texts := map[conversation.MessageID]string{"r": "root", "p": "persona"}
	u := newHistUI(fakeRelTree{g: g, texts: texts}, fakeLister(nil))
	st := newHistoryState(u)
	q := new(input.Router)
	relGFrame(q, u, st)
	relDriveQuiet(q, u, st, relSettleFrames*3)

	gr := st.graph
	li := gr.anchor
	sx, ys := gr.toScreen(gr.sol.Pos[li])
	at := f32.Pt(sx, ys)
	q.Queue(relPointer(pointer.Press, at, true))
	relGFrame(q, u, st)
	to := f32.Pt(at.X+120, at.Y+60)
	q.Queue(relPointer(pointer.Move, to, true))
	relGFrame(q, u, st)
	if gr.dragNode != li {
		t.Fatalf("未进入拖节点: dragNode=%d, want %d", gr.dragNode, li)
	}
	// 按住不动：连续步进几百帧，物理时钟不得被拖拽耗尽、续帧不得断。
	for i := 0; i < relSettleFrames/2; i++ {
		if !gr.tickLayout() {
			t.Fatalf("拖拽第 %d 帧起不再续帧（树会僵在半路）: settled=%v friction=%.4f",
				i, gr.lay.settled, gr.lay.friction)
		}
		if gr.lay.settled {
			t.Fatalf("拖拽第 %d 帧被判定 settled（时钟被拖拽耗尽）", i)
		}
	}
	if got := gr.lay.friction; got < relSrcFriction*0.99 {
		t.Fatalf("拖拽期间的时钟被消耗: friction=%.4f, want ≈%.2f", got, relSrcFriction)
	}

	q.Queue(relPointer(pointer.Release, to, false))
	relGFrame(q, u, st) // Release → releaseDrag（撤 pin、重上时钟）
	if gr.dragNode >= 0 {
		t.Fatalf("松手后仍固定着节点 %d", gr.dragNode)
	}
	if got := gr.lay.friction; got < relSrcFriction*0.99 {
		t.Fatalf("松手未重上时钟: friction=%.4f（下一帧会立刻判停）", got)
	}
	relDriveDone(t, q, u, st)
}

// TestRelGraphDragNodePinsAndSettles 拖节点：视场不平移（那是空白拖拽的事）、节点被临时
// 固定在指针下、周围被斥力推开；松手后锚撤销、节点回归自由并收敛回原位。
func TestRelGraphDragNodePinsAndSettles(t *testing.T) {
	u, st, q := relGraphFixture(t)
	g := st.graph
	sx, li := relAnchorScreen(t, g)
	ox, oy := g.offX, g.offY
	before := append([]relPoint(nil), g.sol.Pos...)

	q.Queue(relPointer(pointer.Press, sx, true))
	relGFrame(q, u, st)
	to := f32.Pt(sx.X+80, sx.Y+40)
	q.Queue(relPointer(pointer.Move, to, true)) // 按压中 Move → Router 转 Drag
	relGFrame(q, u, st)

	if g.dragNode != li {
		t.Fatalf("拖动中的节点 = %d, want %d（锚点）", g.dragNode, li)
	}
	if g.offX != ox || g.offY != oy {
		t.Fatalf("拖节点不应平移视场: off=(%v,%v), want (%v,%v)", g.offX, g.offY, ox, oy)
	}
	if px, py := g.toScreen(g.sol.Pos[li]); math.Abs(float64(px-to.X)) > 2 || math.Abs(float64(py-to.Y)) > 2 {
		t.Fatalf("节点未跟随指针: 屏幕 (%v,%v), 指针 %v", px, py, to)
	}
	// 拖拽期间逐帧松弛：按住若干帧，周围应被斥力推开（渐进推开，不是一步到位）。
	for i := 0; i < 12; i++ {
		relGFrame(q, u, st)
	}
	moved := false
	for i, p := range g.sol.Pos {
		if i != li && (math.Abs(p.X-before[i].X) > 0.5 || math.Abs(p.Y-before[i].Y) > 0.5) {
			moved = true
			break
		}
	}
	for i, p := range g.sol.Pos {
		t.Logf("node %d: %+v (Δ=%.2f)", i, p, math.Hypot(p.X-before[i].X, p.Y-before[i].Y))
	}
	if !moved {
		t.Fatal("拖开节点后周围毫无位移（斥力未展现）")
	}

	q.Queue(relPointer(pointer.Release, to, false))
	relGFrame(q, u, st)
	if g.dragNode >= 0 {
		t.Fatalf("松手后仍固定着节点 %d（临时固定应为非持久）", g.dragNode)
	}
	// 松手后物理自然收敛：跑到静止即停（R3：稳定态不烧资源）。
	for i := 0; i < relSettleFrames*4 && (g.dragNode >= 0 || (g.lay != nil && !g.lay.settled)); i++ {
		relGFrame(q, u, st)
	}
	if g.dragNode >= 0 || g.lay == nil || !g.lay.settled {
		t.Fatal("松手后未收敛即停（R3：稳定态不烧资源）")
	}
	// 位置本身不断言「回到拖前」：力导向是**路径相关**的——拖开过程把周遭推到了新的
	// 平衡态，松手后收敛即停，不保证逐点还原（要还原得数据变更/rebudget 触发重排）。
	// 这里断言的是契约本身：锚已撤销、布局有界、且此后不再自己动。
	settled := append([]relPoint(nil), g.sol.Pos...)
	relGFrame(q, u, st)
	relGFrame(q, u, st)
	for i, p := range g.sol.Pos {
		if math.Abs(p.X-settled[i].X) > 0.01 || math.Abs(p.Y-settled[i].Y) > 0.01 {
			t.Fatalf("已收敛却仍在漂移（节点 %d）: %+v → %+v", i, settled[i], p)
		}
	}
	select {
	case in := <-u.inCh:
		t.Fatalf("拖节点不应投命令（拖过 ≠ 点击）: %+v", in)
	default:
	}
}

// TestRelGraphDragNodeNearMiss 实测修（D121）：**按下时命中落空**（低 zoom 卡片塌缩、
// 指针差几像素）不得静默退化成平移——起拖那一刻用更宽的抓手半径补命中，命中即拖该节点。
func TestRelGraphDragNodeNearMiss(t *testing.T) {
	u, st, q := relGraphFixture(t)
	g := st.graph
	li := g.anchor
	// 复现现场：低 zoom ⇒ 标签隐藏、卡片塌缩成小徽标（点不准的典型形态）
	// zoom 取 relZoomLabelMin 下方一档即可——再低（D130 起 fit 可压到 0.1）抓手/命中
	// 外扩换算成像素后都趋近 0，「命中落空」与「补抓」都不剩可测的像素窗口。
	g.zoom = relZoomLabelMin - 0.01
	g.z = g.zoom * g.dpx
	g.focusAnchor() // 夹具是按 fit 档（size-driven 后可能压得很低）定的视场：改 zoom 后
	// 重对一次，否则锚点已漂出视口，「命中落空」就不再是差几像素而是整棵树在别处。
	xs, ys := g.toScreen(g.sol.Pos[li])
	bare := relBadgeBox(xs, ys, float32(g.nodeRadius(&st.model.nodes[g.vis.nodes[li]])*g.z))
	if g.nodeCardBox(g.vis.nodes[li], xs, ys) != bare {
		t.Fatal("低 zoom 下卡片应塌缩为裸形状（本测试的前提）")
	}
	// 落点：徽标右侧「命中外扩之外、抓手外扩之内」的一小段
	near := f32.Pt(float32(bare.Max.X)+float32(relHitSlop*g.z)+1, ys)
	if got := g.hitTest(near.X, near.Y); got >= 0 {
		t.Fatalf("前提不成立：落点 %v 本就命中 %d", near, got)
	}
	ox, oy := g.offX, g.offY
	q.Queue(relPointer(pointer.Press, near, true))
	relGFrame(q, u, st)
	to := f32.Pt(near.X+30, near.Y+10)
	q.Queue(relPointer(pointer.Move, to, true))
	relGFrame(q, u, st)
	if g.dragNode != li {
		t.Fatalf("补抓失败：dragNode=%d, want %d（且不得平移）", g.dragNode, li)
	}
	if g.offX != ox || g.offY != oy {
		t.Fatalf("补抓时不应平移视场: off=(%v,%v), want (%v,%v)", g.offX, g.offY, ox, oy)
	}
}

// TestRelGraphDragCancelReleasesPin 指针被取消（拖出窗口/失焦）：锚同样撤销，不得卡在拖动态。
func TestRelGraphDragCancelReleasesPin(t *testing.T) {
	u, st, q := relGraphFixture(t)
	g := st.graph
	sx, li := relAnchorScreen(t, g)
	q.Queue(relPointer(pointer.Press, sx, true))
	relGFrame(q, u, st)
	q.Queue(relPointer(pointer.Move, f32.Pt(sx.X+40, sx.Y), true))
	relGFrame(q, u, st)
	if g.dragNode != li {
		t.Fatalf("未进入拖节点: %d", g.dragNode)
	}
	q.Queue(pointer.Event{Kind: pointer.Cancel, Position: f32.Pt(sx.X+40, sx.Y), PointerID: 7, Source: pointer.Mouse})
	relGFrame(q, u, st)
	if g.dragNode >= 0 || g.pressed || g.dragging {
		t.Fatalf("取消后交互态未清: drag=%d pressed=%v dragging=%v", g.dragNode, g.pressed, g.dragging)
	}
}

// TestRelGraphDragSnapsToCursor 实测修（D121）：**偏心按下**起拖后，节点中心在 ~120ms
// 内平滑滑到光标正下方并锁死——「光标和节点的位置不一样」不得持续存在。同时验证起拖
// 第一帧不瞬跳（偏移保留，衰减起步）。
func TestRelGraphDragSnapsToCursor(t *testing.T) {
	u, st, q := relGraphFixture(t)
	g := st.graph
	li := g.anchor
	// 偏心按下：标签区右缘（仍在卡片内；锚点 w3 在 zoom1.0 必有标签）。
	// 改 zoom 后先把 Head 收回右基准位——宽树在 zoom1.0 下锚点会跑到视口外，而 clip 只
	// 放行视口内的指针事件（D125），视口外按下会被静默丢弃。
	g.zoom = 1.0
	g.z = g.zoom * g.dpx
	g.focusAnchor()
	xs, ys := g.toScreen(g.sol.Pos[li])
	box := g.nodeCardBox(g.vis.nodes[li], xs, ys)
	ecc := f32.Pt(float32(box.Max.X-4), ys)
	if got := g.hitTest(ecc.X, ecc.Y); got != li {
		t.Fatalf("偏心点 %v 未命中节点 %d（卡片几何变了？）", ecc, got)
	}
	q.Queue(relPointer(pointer.Press, ecc, true))
	relGFrame(q, u, st)
	to := f32.Pt(ecc.X+40, ecc.Y+15)
	q.Queue(relPointer(pointer.Move, to, true))
	relGFrame(q, u, st)
	if g.dragNode != li {
		t.Fatalf("未拖到节点: %d", g.dragNode)
	}
	// 起拖第一帧：偏移尚未衰减完 → 节点中心 ≠ 光标（不瞬跳，本测试的要点之一）
	if px, py := g.toScreen(g.sol.Pos[li]); math.Abs(float64(px-to.X)) < 1 && math.Abs(float64(py-to.Y)) < 1 {
		t.Fatalf("起拖首帧节点就贴住光标（偏移未保留，标签区起拖会瞬跳）: (%v,%v)", px, py)
	}
	// 衰减逐帧推进：指针停住也要继续滑向光标（tickLayout 续帧）
	for i := 0; i < 30 && (g.dragGrabX != 0 || g.dragGrabY != 0); i++ {
		relGFrame(q, u, st)
	}
	if g.dragGrabX != 0 || g.dragGrabY != 0 {
		t.Fatalf("抓取偏移未衰减完: (%v,%v)", g.dragGrabX, g.dragGrabY)
	}
	if px, py := g.toScreen(g.sol.Pos[li]); math.Abs(float64(px-to.X)) > 2 || math.Abs(float64(py-to.Y)) > 2 {
		t.Fatalf("节点中心未贴住光标: 屏幕 (%v,%v), 光标 %v", px, py, to)
	}
	q.Queue(relPointer(pointer.Release, to, false))
	relGFrame(q, u, st)
}

// TestRelGraphPhysicsUnfoldOnOpen D124：首帧数据从**折叠态**物理展开到静息布局
// （弹簧 + 斥力 + 阻尼，秒级），收敛即停（R3）。曾为 lerp 入场动画（D123），现改为
// 常驻物理——开窗的运动就是物理过程本身（不是独立时间线）。
func TestRelGraphPhysicsUnfoldOnOpen(t *testing.T) {
	u := newHistUI(fakeRelTree{g: relChainGraph(150)}, fakeLister(nil))
	st := newHistoryState(u) // sync → rebuild → 折叠播种
	g := st.graph
	q := new(input.Router)
	if g.lay == nil || g.lay.settled {
		t.Fatal("rebuild 后应处于物理展开态（而非一次全解静止）")
	}
	rest := g.lay.restPos()
	start := append([]relPoint(nil), g.sol.Pos...)
	shrunk := 0
	for i, p := range start {
		if math.Hypot(p.X-rest[i].X, p.Y-rest[i].Y) > 1 {
			shrunk++
		}
	}
	if shrunk == 0 {
		t.Fatal("起帧不应已是静息布局（没有可展开的初态）")
	}
	for i := 0; i < 20; i++ {
		relGFrame(q, u, st)
	}
	moved := false
	for i, p := range g.sol.Pos {
		if math.Hypot(p.X-start[i].X, p.Y-start[i].Y) > 0.5 {
			moved = true
			break
		}
	}
	if !moved {
		t.Fatal("展开 20 帧后仍在原位（没有在动）")
	}
	for i := 0; i < relSettleFrames*4 && g.lay != nil && !g.lay.settled; i++ {
		relGFrame(q, u, st)
	}
	if g.lay == nil || !g.lay.settled {
		t.Fatal("物理展开未收敛")
	}
	settled := append([]relPoint(nil), g.sol.Pos...)
	relGFrame(q, u, st)
	relGFrame(q, u, st)
	for i, p := range g.sol.Pos {
		if math.Hypot(p.X-settled[i].X, p.Y-settled[i].Y) > 0.01 {
			t.Fatalf("收敛后仍在漂移（节点 %d）", i)
		}
	}
}
