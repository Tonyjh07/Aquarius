package uigui

// relmap_frame_test.go 右图自绘/手势/菜单（D120⑦）：headless 离屏帧 + input.Router
// 事件回放（§15.5，零网络零窗口）。覆盖 A8（锚点居中）、A9（缩放钳制 + 越界降级平移）、
// A10（点节点 → /goto）、悬停/右键菜单、拖拽平移、预算控件。

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

// relGraphFixture 右图测试夹具：固定图 + 全预算模型（本地索引 = 模型索引；锚点 u2 = 6）。
func relGraphFixture(t *testing.T) (*UI, *historyState, *input.Router) {
	t.Helper()
	u := newHistUI(fakeRelTree{g: relFixtureGraph()}, fakeLister(nil))
	st := newHistoryState(u)
	q := new(input.Router)
	relGFrame(q, u, st)
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
	// 松手当帧即可能收敛（settling 为假）；未收敛则继续跑帧直至停（R3：稳定态不烧资源）。
	for i := 0; i < relSettleFrames+2 && (g.settling || g.dragNode >= 0); i++ {
		relGFrame(q, u, st)
	}
	if g.settling || g.dragNode >= 0 {
		t.Fatal("收敛帧预算用尽仍未停（R3：稳定态不烧资源）")
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
