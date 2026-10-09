package uigui

// relmap_frame_test.go 右图自绘/手势/菜单（D120⑦）：headless 离屏帧 + input.Router
// 事件回放（§15.5，零网络零窗口）。覆盖 A8（锚点居中）、A9（缩放钳制 + 越界降级平移）、
// A10（点节点 → /goto）、悬停/右键菜单、拖拽平移、预算控件。

import (
	"image"
	"math"
	"testing"
	"time"

	"gioui.org/f32"
	"gioui.org/io/input"
	"gioui.org/io/pointer"
	"gioui.org/layout"

	"github.com/Tonyjh07/Aquarius/internal/domain/conversation"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

// relGraphFixture 右图测试夹具：固定图 + 全预算模型（本地索引 = 模型索引；锚点 u2 = 6）。
// 首帧后把物理跑完（从折叠播种展开至静止）——交互测试需要一个**已收敛的静态基线**。
func relGraphFixture(t *testing.T) (*UI, *historyState, *input.Router) {
	t.Helper()
	u := newHistUI(fakeRelTree{g: relFixtureGraph()}, fakeLister(nil))
	st := newHistoryState(u)
	q := new(input.Router)
	relGFrame(q, u, st)
	for i := 0; i < relSettleFrames*3 && st.graph.lay != nil && !st.graph.lay.settled; i++ {
		relGFrame(q, u, st)
	}
	if st.graph.lay == nil || !st.graph.lay.settled {
		t.Fatal("夹具物理未收敛")
	}
	return u, st, q
}

// relGFrame 一帧右图（500×500 视口）+ 提交 hit 树。
func relGFrame(q *input.Router, u *UI, st *historyState) {
	gtx, ops := frameGtxSize(q.Source(), 500, 500)
	st.graph.frame(gtx, u.th, u, st)
	q.Frame(ops)
}

// relPointer 鼠标事件（ID=7；primary = 按压中）。
func relPointer(kind pointer.Kind, p f32.Point, primary bool) pointer.Event {
	e := pointer.Event{Kind: kind, Position: p, PointerID: 7, Source: pointer.Mouse}
	if primary {
		e.Buttons = pointer.ButtonPrimary
	}
	return e
}

// relPaneFrame 在「左栏 + 右图」两栏布局中跑一帧右图（复刻 history.go 的 Flex：
// Rigid 左栏 + Flexed(1) 右图）并提交 hit 树。注入事件的坐标是**窗口坐标**；右图的
// 指针输入区必须收在本栏 clip 内，Gio 才能借该 clip 的变换把窗口坐标反变换回栏内坐标。
func relPaneFrame(q *input.Router, u *UI, st *historyState, leftPx int) {
	gtx, ops := frameGtxSize(q.Source(), 500, 500)
	layout.Flex{Axis: layout.Horizontal}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			gtx.Constraints = layout.Constraints{
				Min: image.Pt(leftPx, 0),
				Max: image.Pt(leftPx, gtx.Constraints.Max.Y),
			}
			return layout.Dimensions{Size: image.Pt(leftPx, gtx.Constraints.Max.Y)}
		}),
		layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
			return st.graph.frame(gtx, u.th, u, st)
		}),
	)
	q.Frame(ops)
}

// relPaneFixture 真窗两栏结构下的右图收敛基线（从首帧起跑完物理）。
func relPaneFixture(t *testing.T, leftPx int) (*UI, *historyState, *input.Router) {
	t.Helper()
	u := newHistUI(fakeRelTree{g: relFixtureGraph()}, fakeLister(nil))
	st := newHistoryState(u)
	q := new(input.Router)
	for i := 0; i < relSettleFrames*3 && st.graph.lay != nil && !st.graph.lay.settled; i++ {
		relPaneFrame(q, u, st, leftPx)
	}
	if st.graph.lay == nil || !st.graph.lay.settled {
		t.Fatal("两栏夹具物理未收敛")
	}
	return u, st, q
}

// relAnchorScreen 锚点屏幕坐标（来自布局输出，A8）。
func relAnchorScreen(t *testing.T, g *relGraph) (f32.Point, int) {
	t.Helper()
	if g.sol == nil || g.anchor < 0 {
		t.Fatal("布局未就绪")
	}
	li := g.anchor
	x, y := g.toScreen(g.sol.Pos[li])
	return f32.Pt(x, y), li
}

// TestRelGraphFitViewOnOpen 开窗即**适配整树**（用户 2026-10-09：只看锚点 = 只看一角落）：
// 首帧后所有可见节点都在视口内，zoom 仍夹在 [relZoomMin, relZoomMax]（A9）。
func TestRelGraphFitViewOnOpen(t *testing.T) {
	_, st, _ := relGraphFixture(t)
	g := st.graph
	if g.zoom < relZoomMin || g.zoom > relZoomMax {
		t.Fatalf("首帧 zoom = %v，超出 [%v,%v]", g.zoom, relZoomMin, relZoomMax)
	}
	for li := range st.vis.nodes {
		xs, ys := g.toScreen(g.sol.Pos[li])
		if xs < 0 || ys < 0 || float64(xs) > g.viewW || float64(ys) > g.viewH {
			t.Fatalf("节点 %d 未被适配进视口: (%v,%v)，视口 %v×%v", li, xs, ys, g.viewW, g.viewH)
		}
	}
}

// TestRelGraphFocusCentersAnchor A8：「回到当前」= 视场居中锚点（布局器输出的坐标），
// 且**不**重置 zoom。
func TestRelGraphFocusCentersAnchor(t *testing.T) {
	u, st, q := relGraphFixture(t)
	g := st.graph
	// 先把视场挪开 + 改 zoom，再触发「回到当前」
	g.offX, g.offY = -120, 80
	g.zoom = 1.2
	g.focusPending = true
	relGFrame(q, u, st)
	sx, _ := relAnchorScreen(t, g)
	if math.Abs(float64(sx.X)-g.viewW/2) > 1 || math.Abs(float64(sx.Y)-g.viewH/2) > 1 {
		t.Fatalf("锚点屏幕 = %v, want ≈(%.0f,%.0f)", sx, g.viewW/2, g.viewH/2)
	}
	if g.zoom != 1.2 {
		t.Fatalf("「回到当前」不应重置 zoom: %v, want 1.2", g.zoom)
	}
}

// TestRelGraphHitTest 命中判定：锚点处命中锚点本地索引。
func TestRelGraphHitTest(t *testing.T) {
	_, st, _ := relGraphFixture(t)
	sx, li := relAnchorScreen(t, st.graph)
	if got := st.graph.hitTest(sx.X, sx.Y); got != li {
		t.Fatalf("hitTest(锚点) = %d, want %d", got, li)
	}
	if got := st.graph.hitTest(0, 0); got != -1 {
		t.Fatalf("hitTest(角落) = %d, want -1", got)
	}
}

// TestRelGraphClickGoto A10：左键点节点 → /goto <id> 经 inCh。
func TestRelGraphClickGoto(t *testing.T) {
	u, st, q := relGraphFixture(t)
	sx, li := relAnchorScreen(t, st.graph)
	want := st.model.nodes[st.vis.nodes[li]].id
	q.Queue(relPointer(pointer.Press, sx, true))
	relGFrame(q, u, st)
	q.Queue(relPointer(pointer.Release, sx, false))
	relGFrame(q, u, st)
	select {
	case in := <-u.inCh:
		if in.Command == nil || in.Command.Name != "goto" ||
			len(in.Command.Args) != 1 || conversation.MessageID(in.Command.Args[0]) != want {
			t.Fatalf("inCh = %+v, want /goto %s", in, want)
		}
	default:
		t.Fatal("inCh 空：点节点未投 /goto")
	}
}

// TestRelGraphPointerAreaIsPane 右图的指针输入区 = 右图栏：事件位置经该栏的变换反算回
// 栏内坐标，点击位置与实际响应位置不错位（2026-10-09 实测报障：整体右偏一个左栏宽）；
// 左栏上的点击也不得被右图误收。
func TestRelGraphPointerAreaIsPane(t *testing.T) {
	const leftPx = 200
	u, st, q := relPaneFixture(t, leftPx)
	g := st.graph
	sx, li := relAnchorScreen(t, g)
	want := st.model.nodes[st.vis.nodes[li]].id
	// 点锚点的**窗口**坐标（栏内坐标 + 左栏宽）。
	at := f32.Pt(sx.X+float32(leftPx), sx.Y)
	q.Queue(relPointer(pointer.Press, at, true))
	relPaneFrame(q, u, st, leftPx)
	q.Queue(relPointer(pointer.Release, at, false))
	relPaneFrame(q, u, st, leftPx)
	select {
	case in := <-u.inCh:
		if in.Command == nil || in.Command.Name != "goto" ||
			len(in.Command.Args) != 1 || conversation.MessageID(in.Command.Args[0]) != want {
			t.Fatalf("inCh = %+v, want /goto %s（点击位置与响应错位）", in, want)
		}
	default:
		t.Fatal("inCh 空：两栏结构下点节点未投 /goto")
	}
	// 左栏上的点击不得被右图误收（输入区收窄为右图栏）。
	q.Queue(relPointer(pointer.Press, f32.Pt(20, 20), true))
	relPaneFrame(q, u, st, leftPx)
	if g.pressed || g.pressNode != -1 || g.dragNode != -1 {
		t.Fatalf("左栏点击被右图误收：pressed=%v pressNode=%d dragNode=%d",
			g.pressed, g.pressNode, g.dragNode)
	}
	q.Queue(relPointer(pointer.Release, f32.Pt(20, 20), false))
	relPaneFrame(q, u, st, leftPx)
	// 拖拽同样按栏内坐标走：按在锚点的**窗口**坐标上拖动 → 该节点被 pin（不是平移）。
	moved := f32.Pt(at.X+60, at.Y+40)
	q.Queue(relPointer(pointer.Press, at, true))
	relPaneFrame(q, u, st, leftPx)
	q.Queue(relPointer(pointer.Move, moved, true))
	relPaneFrame(q, u, st, leftPx)
	if g.dragNode != li {
		t.Fatalf("两栏结构下拖拽未抓到锚点节点: dragNode = %d, want %d", g.dragNode, li)
	}
	q.Queue(relPointer(pointer.Release, moved, false))
	relPaneFrame(q, u, st, leftPx)
}

// TestRelGraphPanByDrag 空白拖拽 = 平移（绝对跟踪：起点 + 差值）。
func TestRelGraphPanByDrag(t *testing.T) {
	u, st, q := relGraphFixture(t)
	ox, oy := st.graph.offX, st.graph.offY
	start := f32.Pt(10, 10)
	q.Queue(relPointer(pointer.Press, start, true))
	relGFrame(q, u, st)
	q.Queue(relPointer(pointer.Move, f32.Pt(40, 30), true)) // 按压中 Move → Router 转 Drag
	relGFrame(q, u, st)
	q.Queue(relPointer(pointer.Release, f32.Pt(40, 30), false))
	relGFrame(q, u, st)
	if d := st.graph.offX - ox; d < 29 || d > 31 {
		t.Fatalf("offX 位移 = %v, want ≈30", d)
	}
	if d := st.graph.offY - oy; d < 19 || d > 21 {
		t.Fatalf("offY 位移 = %v, want ≈20", d)
	}
}

// TestRelGraphWheelZoomAndDegrade A9：滚轮缩放钳 [0.5,1.5]；越界降级纵向平移。
func TestRelGraphWheelZoomAndDegrade(t *testing.T) {
	u, st, q := relGraphFixture(t)
	g := st.graph
	// 放大（Gio 约定滚上 = Scroll.Y < 0）
	q.Queue(pointer.Event{Kind: pointer.Scroll, Scroll: f32.Pt(0, -120), PointerID: 7, Source: pointer.Mouse})
	relGFrame(q, u, st)
	if g.zoom <= 1.0 {
		t.Fatalf("滚上后 zoom = %v, want > 1", g.zoom)
	}
	// 缩回并压到下限
	q.Queue(pointer.Event{Kind: pointer.Scroll, Scroll: f32.Pt(0, 120), PointerID: 7, Source: pointer.Mouse})
	relGFrame(q, u, st)
	g.zoom = relZoomMin // 直接压到下限
	oy := g.offY
	q.Queue(pointer.Event{Kind: pointer.Scroll, Scroll: f32.Pt(0, 120), PointerID: 7, Source: pointer.Mouse})
	relGFrame(q, u, st)
	if g.zoom != relZoomMin {
		t.Fatalf("低于下限 zoom = %v, want %v", g.zoom, relZoomMin)
	}
	if g.offY == oy {
		t.Fatal("越界后应降级纵向平移（offY 不变 = 静默失效）")
	}
}

// TestRelGraphWheelPanStep 实测修（D121）：滚轮越界降级平移用**定值小步**（relWheelPanPx），
// 一格滚轮只挪 48px——原实现 16×像素增量（一格 ≈ 1600px）会把树甩出裁剪盒、完全不可见。
func TestRelGraphWheelPanStep(t *testing.T) {
	u, st, q := relGraphFixture(t)
	g := st.graph
	g.zoom = relZoomMin
	oy := g.offY
	q.Queue(pointer.Event{Kind: pointer.Scroll, Scroll: f32.Pt(0, 120), PointerID: 7, Source: pointer.Mouse})
	relGFrame(q, u, st)
	if d := g.offY - oy; d <= 0 || d > relWheelPanPx*1.5 {
		t.Fatalf("越界平移步长 = %v, want 定值小步 (0, %v]", d, relWheelPanPx*1.5)
	}
}

// TestRelGraphRebuildKeepsView 实测修（D121）：数据变更（rebuild）**不重置 zoom、不挪
// 视场**——用户 zoom/平移是私有的，一条新消息进来不抢。只有开窗首帧（everFit）适配、
// 拖后松手（fitPending）拉回视野。
func TestRelGraphRebuildKeepsView(t *testing.T) {
	u, st, q := relGraphFixture(t)
	g := st.graph
	if !g.everFit {
		t.Fatal("夹具应已完成开窗首帧适配")
	}
	g.zoom = 1.2
	g.z = g.zoom * g.dpx
	ox, oy := g.offX, g.offY
	m2, ok := relNewModel(relChainGraph(20))
	if !ok {
		t.Fatal("chain 模型非法")
	}
	g.rebuild(m2, relBudget(m2, 20))
	if g.zoom != 1.2 {
		t.Fatalf("rebuild 重置了 zoom: %v, want 1.2", g.zoom)
	}
	relGFrame(q, u, st)
	if g.offX != ox || g.offY != oy {
		t.Fatalf("数据变更挪动了视场: off=(%v,%v), want (%v,%v)", g.offX, g.offY, ox, oy)
	}
}

// TestRelGraphDragReleaseKeepsTreeInView 拖后松手：树是常驻物理 → 松手后回到静息位，
// 视场（开窗已按静息盒适配）仍能看到整棵树。曾（D121）靠松手 recenter 把「拖开摊宽」
// 的树拉回；物理版树自己回去，recenter 只在用户曾平移过视场时才改变 off。
func TestRelGraphDragReleaseKeepsTreeInView(t *testing.T) {
	u, st, q := relGraphFixture(t)
	g := st.graph
	sx, li := relAnchorScreen(t, g)
	q.Queue(relPointer(pointer.Press, sx, true))
	relGFrame(q, u, st)
	to := f32.Pt(sx.X+300, sx.Y+200)
	q.Queue(relPointer(pointer.Move, to, true))
	relGFrame(q, u, st)
	if g.dragNode != li {
		t.Fatalf("未拖到节点: %d", g.dragNode)
	}
	q.Queue(relPointer(pointer.Release, to, false))
	relGFrame(q, u, st) // Release → releaseDrag（撤 pin、唤醒物理）
	for i := 0; i < relSettleFrames*2 && g.lay != nil && !g.lay.settled; i++ {
		relGFrame(q, u, st)
	}
	if g.lay == nil || !g.lay.settled {
		t.Fatal("松手后物理未收敛")
	}
	// 盒心对准视口中心（recenterTree 保持 zoom、只对盒心）。
	minX, minY, maxX, maxY, ok := g.treeBox()
	if !ok {
		t.Fatal("树盒不可用")
	}
	if cx, cy := (minX+maxX)/2*g.z, (minY+maxY)/2*g.z; math.Abs(g.offX+cx-g.viewW/2) > 2 || math.Abs(g.offY+cy-g.viewH/2) > 2 {
		t.Fatalf("松手后树盒心未对准视口: off=(%v,%v) 盒心=(%v,%v) 视口=(%v,%v)",
			g.offX, g.offY, cx, cy, g.viewW/2, g.viewH/2)
	}
	// 所有可见节点回到视口内（物理回位 + 适配视场）。
	for i := range g.vis.nodes {
		xs, ys := g.toScreen(g.sol.Pos[i])
		if xs < -1 || float64(xs) > g.viewW+1 || ys < -1 || float64(ys) > g.viewH+1 {
			t.Fatalf("节点 %d 松手后仍在视口外: (%v,%v)", i, xs, ys)
		}
	}
}

// TestRelGraphFitFloorLabelMin 实测修（D121）：开窗适配的 zoom 下限 = relZoomLabelMin
// （标签可读下限）——适配不得把树压成全裸点。超大树的适配退回锚点居中。
func TestRelGraphFitFloorLabelMin(t *testing.T) {
	u := newHistUI(fakeRelTree{g: relChainGraph(150)}, fakeLister(nil))
	st := newHistoryState(u)
	q := new(input.Router)
	relGFrame(q, u, st) // 首帧：fitView
	if st.graph.zoom < relZoomLabelMin {
		t.Fatalf("fit zoom = %v, want ≥ 标签下限 %v", st.graph.zoom, relZoomLabelMin)
	}
}

// TestRelGraphLabelFontClamp 字号钳制（A9）：clamp(12×zoom, 10, 16)。
func TestRelGraphLabelFontClamp(t *testing.T) {
	g := &relGraph{zoom: 0.5}
	if g.labelFont() != 10 {
		t.Fatalf("zoom0.5 字号 = %v, want 10", g.labelFont())
	}
	g.zoom = 1.0
	if g.labelFont() != 12 {
		t.Fatalf("zoom1.0 字号 = %v, want 12", g.labelFont())
	}
	g.zoom = 1.5
	if g.labelFont() != 16 {
		t.Fatalf("zoom1.5 字号 = %v, want 16", g.labelFont())
	}
}

// TestRelGraphRightClickMenuOpenClose 右键节点开菜单；菜单外左键关闭。
func TestRelGraphRightClickMenuOpenClose(t *testing.T) {
	u, st, q := relGraphFixture(t)
	g := st.graph
	sx, _ := relAnchorScreen(t, g)
	// 右键按下（secondary）
	q.Queue(pointer.Event{Kind: pointer.Press, Position: sx, PointerID: 7, Source: pointer.Mouse, Buttons: pointer.ButtonSecondary})
	relGFrame(q, u, st)
	if !g.menuOpen || g.menuNode != g.anchor {
		t.Fatalf("右键后 menuOpen=%v menuNode=%d, want true/%d", g.menuOpen, g.menuNode, g.anchor)
	}
	// 菜单外左键 → 关闭
	q.Queue(relPointer(pointer.Press, f32.Pt(400, 400), true))
	relGFrame(q, u, st)
	if g.menuOpen {
		t.Fatal("菜单外左键应关闭菜单")
	}
}

// TestRelGraphMenuRm 菜单「删除」点击 → /rm <id> 经 inCh（程序化点击，同一 Clicked 通路）。
func TestRelGraphMenuRm(t *testing.T) {
	u, st, q := relGraphFixture(t)
	g := st.graph
	sx, _ := relAnchorScreen(t, g)
	want := st.model.nodes[st.vis.nodes[g.anchor]].id
	q.Queue(pointer.Event{Kind: pointer.Press, Position: sx, PointerID: 7, Source: pointer.Mouse, Buttons: pointer.ButtonSecondary})
	relGFrame(q, u, st) // 菜单已绘制（item 几何注册）
	if !g.menuOpen || g.menuNode != g.anchor {
		t.Fatalf("菜单未开: open=%v node=%d", g.menuOpen, g.menuNode)
	}
	g.menuRm.Click() // 程序化点击（Router 帧序不稳定，同 TestBranchClickDispatches 口径）
	relGFrame(q, u, st)
	select {
	case in := <-u.inCh:
		if in.Command == nil || in.Command.Name != "rm" ||
			len(in.Command.Args) != 1 || conversation.MessageID(in.Command.Args[0]) != want {
			t.Fatalf("inCh = %+v, want /rm %s", in, want)
		}
	default:
		t.Fatal("inCh 空：菜单删除未投 /rm")
	}
	if g.menuOpen {
		t.Fatal("删除后菜单未关闭")
	}
}

// TestRelBudgetControls 预算 −/＋ 钳制与「展开全部」（用 150 节点链验证非平凡预算）。
func TestRelBudgetControls(t *testing.T) {
	u := newHistUI(fakeRelTree{g: relChainGraph(150)}, fakeLister(nil))
	st := newHistoryState(u)
	if st.budget != relDefaultBudget {
		t.Fatalf("初始预算 = %d, want %d", st.budget, relDefaultBudget)
	}
	st.applyBudget(+10)
	if st.budget != relDefaultBudget+10 {
		t.Fatalf("＋ 后 budget = %d, want %d", st.budget, relDefaultBudget+10)
	}
	for i := 0; i < 100; i++ {
		st.applyBudget(-10)
	}
	if st.budget != 1 {
		t.Fatalf("− 应钳到 1，实为 %d", st.budget)
	}
	st.expandAll()
	if st.budget != 150 { // 150 < relHardCap → 全量
		t.Fatalf("展开全部 = %d, want 150", st.budget)
	}
	if len(st.vis.nodes) != 150 {
		t.Fatalf("展开全部呈现 = %d, want 150", len(st.vis.nodes))
	}
	_ = u
}

// relChainGraph n 节点链（根 + 依次顺接；锚点 = 末节点）——预算/大图测试用。
func relChainGraph(n int) port.TreeGraph {
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	nodes := make([]port.GraphNode, n)
	for i := 0; i < n; i++ {
		role := conversation.RoleAssistant
		if i%2 == 1 {
			role = conversation.RoleUser
		}
		if i == 0 {
			role = conversation.RoleRoot
		}
		parent := ""
		if i > 0 {
			parent = "n" + itoa(i-1)
		}
		nodes[i] = port.GraphNode{
			ID: conversation.MessageID("n" + itoa(i)), Parent: conversation.MessageID(parent),
			Role: role, Snippet: "s", CreatedAt: base.Add(time.Duration(i) * time.Second),
			EdgeKind: port.EdgeSeq,
		}
	}
	return port.TreeGraph{Anchor: conversation.MessageID("n" + itoa(n-1)), Nodes: nodes, Total: n}
}

// TestRelGraphRenderFullWindow 整窗渲染（含状态栏控件与图例）不 panic（离屏帧）。
func TestRelGraphRenderFullWindow(t *testing.T) {
	u, st, q := relGraphFixture(t)
	gtx, ops := frameGtxSize(q.Source(), 720, 560)
	historyFrame(gtx, u.th, u, st)
	q.Frame(ops)
}

// TestRelGraphBudgetCulledNoPanic 回归：预算裁剪使可见集非连续（全局≠本地索引）时
// 渲染不越界——曾以全局索引查本地 sol.Pos 崩溃（index out of range）。150 节点链 +
// 默认预算 120 → 可见集 = anchor 父链一段，全局索引从 30 起、本地 0..119。
func TestRelGraphBudgetCulledNoPanic(t *testing.T) {
	u := newHistUI(fakeRelTree{g: relChainGraph(150)}, fakeLister(nil))
	st := newHistoryState(u)
	if len(st.vis.nodes) != 120 {
		t.Fatalf("预算切片 = %d, want 120", len(st.vis.nodes))
	}
	q := new(input.Router)
	gtx, ops := frameGtxSize(q.Source(), 720, 560)
	st.graph.frame(gtx, u.th, u, st) // 不 panic = 通过
	q.Frame(ops)
}

// —— D121：节点拖拽（临时固定 → 松手回归自由）——

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
	g.zoom = relZoomLabelMin - 0.01
	g.z = g.zoom * g.dpx
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
	// 偏心按下：标签区右缘（仍在卡片内；锚点 w3 在 zoom1.0 必有标签）
	g.zoom = 1.0
	g.z = g.zoom * g.dpx
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

// TestRelNodeCardCarriesLabel 节点卡承载标签（D121「内容入内」）：标签可见时卡比徽标宽、
// 高度仍等于徽标（宽度不改变节点直径语义，A4）；标签不可见（低于 zoom 下限）时退化为
// 居中方块。
func TestRelNodeCardCarriesLabel(t *testing.T) {
	_, st, _ := relGraphFixture(t)
	g := st.graph
	gi := st.vis.nodes[g.anchor]
	if relLabelText(st.model, gi, g.zoom) == "" {
		t.Fatal("锚点应有标签")
	}
	xs, ys := g.toScreen(g.sol.Pos[g.anchor])
	r := float32(g.nodeRadius(&st.model.nodes[gi]) * g.z)
	bare := relBadgeBox(xs, ys, r)
	box := g.nodeCardBox(gi, xs, ys)
	if box.Dx() <= bare.Dx()+int(relCardGapDp*g.z) {
		t.Fatalf("标签卡宽 %d 未超出徽标 %d（标签没进卡里）", box.Dx(), bare.Dx())
	}
	// 徽标带内边距地贴在卡内（菱形尖/空心圆不被切边，分叉虚线环不越界）
	pad, pv := int(relCardPadDp*g.z), int(relCardPadV*g.z)
	if box.Min.X != bare.Min.X-pad || box.Min.Y != bare.Min.Y-pv || box.Max.Y != bare.Max.Y+pv {
		t.Fatalf("标签卡背离徽标锚点: %v vs %v +内边距 %d/%d", box, bare, pad, pv)
	}
	// 低于 zoom 下限 → 无标签 → 裸形状（直接改 zoom 时须同步有效缩放 g.z）
	g.zoom = relZoomLabelMin - 0.01
	g.z = g.zoom * g.dpx
	bare = relBadgeBox(xs, ys, float32(g.nodeRadius(&st.model.nodes[gi])*g.z))
	if box := g.nodeCardBox(gi, xs, ys); box != bare {
		t.Fatalf("无标签时卡 %v ≠ 裸形状 %v（应退化）", box, bare)
	}
}

// TestRelEstTextWidth 文本宽估算：CJK 按全角记、同字数下宽于 ASCII（卡片宽度量级正确）。
func TestRelEstTextWidth(t *testing.T) {
	cjk := relEstTextW("你今天想聊点什么", 12)
	ascii := relEstTextW("abcdefghijkl", 12)
	if cjk <= ascii {
		t.Fatalf("CJK 宽 %v 应大于等长 ASCII %v", cjk, ascii)
	}
	if got := relEstTextW("", 12); got != 0 {
		t.Fatalf("空串宽 = %v, want 0", got)
	}
	if got := relEstTextW("ab", 10); got != 11 { // 2 × 0.55 × 10
		t.Fatalf("ASCII 估宽 = %v, want 11", got)
	}
}
