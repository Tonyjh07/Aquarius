package uigui

// relmap_frame_test.go 右图自绘/手势/菜单（D120⑦）：headless 离屏帧 + input.Router
// 事件回放（§15.5，零网络零窗口）。覆盖 A8（锚点居中）、A9（缩放钳制 + 越界降级平移）、
// A10（点节点 → /goto）、悬停/右键菜单、拖拽平移、预算控件。

import (
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

// TestRelGraphFocusCentersAnchor A8：首帧视场居中锚点（布局器输出的坐标）。
func TestRelGraphFocusCentersAnchor(t *testing.T) {
	u, st, _ := relGraphFixture(t)
	g := st.graph
	sx, _ := relAnchorScreen(t, g)
	if sx.X < 249 || sx.X > 251 || sx.Y < 249 || sx.Y > 251 {
		t.Fatalf("锚点屏幕 = %v, want ≈(250,250)", sx)
	}
	_ = u
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
