package uigui

// 树编辑模式的拖动改挂（§5.6.3）测试：
//   - 落点预演：最近的一个合法目标、根与自身后代不当目标、自身后代留一句解释；
//   - 松手：有目标 → 投 /reparent（结构交内核）；无目标 → 只改位置（不投命令）；
//   - 拖起：旧父边淡出（120ms 时间线）；
//   - 编辑模式关闭时一切照旧（拖节点 = 摆位置，不投命令）——既有的 D121 行为不被拖下水。

import (
	"testing"

	"gioui.org/f32"
	"gioui.org/io/input"
	"gioui.org/io/pointer"

	"github.com/Tonyjh07/Aquarius/internal/domain/conversation"
)

// relEditFixture 编辑模式夹具（fakeRelTree.editOn = true）：物理先收敛到静止，
// 否则卡片位置每帧都在动，落点断言无从谈起。
func relEditFixture(t *testing.T) (*UI, *historyState, *input.Router, *fakeRelTree) {
	t.Helper()
	texts := make(map[conversation.MessageID]string)
	for _, n := range relFixtureGraph().Nodes {
		texts[n.ID] = relFixtureBody(n.ID)
	}
	tree := &fakeRelTree{g: relFixtureGraph(), texts: texts, editOn: true}
	u := newHistUI(tree, fakeLister(nil))
	st := newHistoryState(u)
	q := new(input.Router)
	relGFrame(q, u, st)
	relDriveQuiet(q, u, st, relSettleFrames*3)
	return u, st, q, tree
}

// relNodeScreen 某节点当前的屏幕坐标（按消息 ID 找本地索引）。
func relNodeScreen(t *testing.T, g *relGraph, id string) f32.Point {
	t.Helper()
	li := g.localOf(conversation.MessageID(id))
	if li < 0 {
		t.Fatalf("节点 %s 不在可见集", id)
	}
	x, y := g.toScreen(g.sol.Pos[li])
	return f32.Pt(x, y)
}

// TestRelDropCandidateRules 落点预演的合法性：最近的一个、非根、非自身后代；
// 压在自己后代上时只留一句解释、绝不当目标。
func TestRelDropCandidateRules(t *testing.T) {
	_, st, _, _ := relEditFixture(t)
	g := st.graph

	// 拖 u2（叶子）到 u1b 的卡片位置：u1b 是合法目标。
	g.dragNode = g.localOf("u2")
	if g.dragNode < 0 {
		t.Fatal("u2 不在可见集")
	}
	// 把被拖节点挪到目标卡片上（拖拽期间它就是被 pin 到那儿的），再看预演结果。
	moveTo := func(drag, onto string) {
		dl, ol := g.localOf(conversation.MessageID(drag)), g.localOf(conversation.MessageID(onto))
		if dl < 0 || ol < 0 {
			t.Fatalf("节点不在可见集: %s / %s", drag, onto)
		}
		g.dragNode = dl
		g.sol.Pos[dl] = g.sol.Pos[ol]
	}
	moveTo("u2", "u1b")
	if got, blocked := g.dropCandidate(); got != g.localOf("u1b") || blocked >= 0 {
		t.Fatalf("拖到 u1b 上：目标 = %d, 拦下 = %d，want %d/-1", got, blocked, g.localOf("u1b"))
	}
	// 拖到结构根上：根不能当父 → 无目标、也不算「被拦下的后代」。
	moveTo("u2", "r")
	if got, blocked := g.dropCandidate(); got >= 0 || blocked >= 0 {
		t.Fatalf("拖到根上：目标 = %d, 拦下 = %d，want -1/-1", got, blocked)
	}
	// 拖到自己的后代上：不做目标，但记下「被拦下」。
	moveTo("u1", "a1")
	if got, blocked := g.dropCandidate(); got >= 0 || blocked != g.localOf("a1") {
		t.Fatalf("拖到自己后代上：目标 = %d, 拦下 = %d，want -1/%d",
			got, blocked, g.localOf("a1"))
	}
	// 远处（离所有卡片都超过 48dp）：无目标、也无解释。
	dl := g.localOf("u2")
	g.dragNode = dl
	g.sol.Pos[dl] = relPoint{X: -1000, Y: -1000}
	if got, blocked := g.dropCandidate(); got >= 0 || blocked >= 0 {
		t.Fatalf("拖到空处：目标 = %d, 拦下 = %d，want -1/-1", got, blocked)
	}
}

// TestRelDragReparentsOnRelease 编辑态拖到合法目标上松手 → 投 `/reparent <被拖> <目标>`，
// 预演状态清干净、目标环高亮亮起。
func TestRelDragReparentsOnRelease(t *testing.T) {
	u, st, q, _ := relEditFixture(t)
	g := st.graph
	drainCh(u)

	from := relNodeScreen(t, g, "u2")
	q.Queue(relPointer(pointer.Press, from, true))
	relGFrame(q, u, st)
	// 朝 u1b 拖：目标位置每帧都可能被弹簧推开，故每帧按**当前**位置纠一次指针。
	var to f32.Point
	for i := 0; i < 40; i++ {
		to = relNodeScreen(t, g, "u1b")
		q.Queue(relPointer(pointer.Move, to, true))
		relGFrame(q, u, st)
		if g.dragNode >= 0 && g.dropTarget == g.localOf("u1b") {
			break
		}
	}
	if g.dragNode != g.localOf("u2") {
		t.Fatalf("拖动中的节点 = %d, want %d", g.dragNode, g.localOf("u2"))
	}
	if g.dropTarget != g.localOf("u1b") {
		t.Fatalf("落点预演 = %d, want %d（u1b）", g.dropTarget, g.localOf("u1b"))
	}
	// 旧父边淡出：时间线已布防、目标为 0（测试帧跑得比 120ms 快得多，只能断言「在跑」）。
	if g.dragFade.to != 0 || !(g.dragFade.active() || g.dragFade.v == 0) {
		t.Fatalf("旧父边未开始淡出：alpha = %v, to = %v, running = %v",
			g.dragFade.v, g.dragFade.to, g.dragFade.active())
	}

	q.Queue(relPointer(pointer.Release, to, false))
	relGFrame(q, u, st)

	select {
	case in := <-u.inCh:
		if in.Command == nil || in.Command.Name != "reparent" {
			t.Fatalf("投出 %+v, want /reparent", in.Command)
		}
		if len(in.Command.Args) != 2 || in.Command.Args[0] != "u2" || in.Command.Args[1] != "u1b" {
			t.Fatalf("投出参数 %v, want [u2 u1b]", in.Command.Args)
		}
	default:
		t.Fatal("松手后未投 /reparent")
	}
	if g.dragNode >= 0 || g.dropTarget >= 0 {
		t.Fatalf("松手后预演状态未清: drag=%d drop=%d", g.dragNode, g.dropTarget)
	}
	if g.flash.id != "u1b" {
		t.Fatalf("改挂回执目标 = %q, want u1b", g.flash.id)
	}
	if !g.dropFlashActive(st.graph.flash.start) {
		t.Fatal("回执高亮应立即有效")
	}
}

// TestRelDragNoTargetKeepsParent 没靠近任何节点 = 只改位置（§5.6.3）：不投命令、槽位固化。
func TestRelDragNoTargetKeepsParent(t *testing.T) {
	u, st, q, _ := relEditFixture(t)
	g := st.graph
	drainCh(u)

	from := relNodeScreen(t, g, "u2")
	q.Queue(relPointer(pointer.Press, from, true))
	relGFrame(q, u, st)
	// 朝视口左下角拖（远离所有卡片）。
	to := f32.Pt(20, 470)
	q.Queue(relPointer(pointer.Move, to, true))
	relGFrame(q, u, st)
	if g.dragNode != g.localOf("u2") {
		t.Fatalf("拖动中的节点 = %d, want %d", g.dragNode, g.localOf("u2"))
	}
	if g.dropTarget >= 0 {
		t.Fatalf("空处不该有落点，得到 %d", g.dropTarget)
	}
	q.Queue(relPointer(pointer.Release, to, false))
	relGFrame(q, u, st)

	select {
	case in := <-u.inCh:
		t.Fatalf("无目标松手不应投命令: %+v", in)
	default:
	}
	if _, ok := g.slots[conversation.MessageID("u2")]; !ok {
		t.Fatal("无目标松手应固化槽位（既有行为：拖完留在放手处）")
	}
	if g.dragFade.v != 1 {
		t.Fatalf("松手后旧父边应恢复实线，alpha = %v", g.dragFade.v)
	}
}

// TestRelDragOnDescendantExplains 压在自己的后代上松手：一句解释、不投命令。
func TestRelDragOnDescendantExplains(t *testing.T) {
	u, st, q, _ := relEditFixture(t)
	g := st.graph
	drainCh(u)

	from := relNodeScreen(t, g, "u1")
	q.Queue(relPointer(pointer.Press, from, true))
	relGFrame(q, u, st)
	var to f32.Point
	for i := 0; i < 40; i++ {
		to = relNodeScreen(t, g, "a1")
		q.Queue(relPointer(pointer.Move, to, true))
		relGFrame(q, u, st)
		if g.dragNode >= 0 {
			break
		}
	}
	if g.dragNode != g.localOf("u1") {
		t.Fatalf("拖动中的节点 = %d, want %d", g.dragNode, g.localOf("u1"))
	}
	q.Queue(relPointer(pointer.Release, to, false))
	relGFrame(q, u, st)

	select {
	case in := <-u.inCh:
		t.Fatalf("压在自己后代上不应投命令: %+v", in)
	default:
	}
	if st.toast.text == "" {
		t.Fatal("压在自己后代上应给一句解释（轻提示）")
	}
}

// TestRelDragOutsideEditModeJustMoves 没开编辑模式：拖到别人身上也只是摆位置（D121 行为不变）。
func TestRelDragOutsideEditModeJustMoves(t *testing.T) {
	texts := make(map[conversation.MessageID]string)
	for _, n := range relFixtureGraph().Nodes {
		texts[n.ID] = relFixtureBody(n.ID)
	}
	u := newHistUI(fakeRelTree{g: relFixtureGraph(), texts: texts}, fakeLister(nil))
	st := newHistoryState(u)
	q := new(input.Router)
	relGFrame(q, u, st)
	relDriveQuiet(q, u, st, relSettleFrames*3)
	g := st.graph
	if g.editOn {
		t.Fatal("前提：非编辑态")
	}
	drainCh(u)

	from := relNodeScreen(t, g, "u2")
	q.Queue(relPointer(pointer.Press, from, true))
	relGFrame(q, u, st)
	var to f32.Point
	for i := 0; i < 20; i++ {
		to = relNodeScreen(t, g, "u1b")
		q.Queue(relPointer(pointer.Move, to, true))
		relGFrame(q, u, st)
	}
	q.Queue(relPointer(pointer.Release, to, false))
	relGFrame(q, u, st)

	select {
	case in := <-u.inCh:
		t.Fatalf("非编辑态不应投命令: %+v", in)
	default:
	}
	if g.dragFade.v != 1 {
		t.Fatalf("非编辑态不该有旧父边淡出，alpha = %v", g.dragFade.v)
	}
}

// TestPinDetachedCutsParentSpring 编辑态拖起 = 与结构父断开（§5.6.3）：同样的拖起动作，
// 连着时父被弹簧拽向被拖者，断开后只剩斥力（父几乎不动）；撤锚（松手）后连接自动恢复。
//
// 2026-10-31 实测报障：边已淡出，物理还拉着——手上是一根看不见的皮筋。
func TestPinDetachedCutsParentSpring(t *testing.T) {
	geo := []relGeoNode{
		{Parent: -1, Level: 0, Weight: 1},
		{Parent: 0, Level: 1, Weight: 1},
	}
	const dragged = 400.0 // 被拖者被拽到父右侧 400dp（远超 edgeLength = 100dp）
	run := func(pin relPin, steps int) float64 {
		l, ok := newRelLayout(geo)
		if !ok {
			t.Fatal("newRelLayout 拒绝两节点链")
		}
		l.setPositions([]relPoint{{X: 0, Y: 0}, {X: dragged, Y: 0}})
		l.wake()
		for i := 0; i < steps; i++ {
			l.step(pin)
		}
		return l.x[1] - l.x[0]
	}

	attached := relPin{Valid: true, Index: 1, Pos: relPoint{X: dragged, Y: 0}}
	if d := run(attached, 120); d > 200 {
		t.Fatalf("连着时父子距离 = %.1f, want ≈ edgeLength（弹簧该把父拽过去）", d)
	}
	cut := attached
	cut.Detached = true
	if d := run(cut, 120); d < dragged*0.95 {
		t.Fatalf("断开后父子距离 = %.1f, want ≈ %.1f（父不该再被拽向被拖者）", d, dragged)
	}
	if d := run(relPin{}, 400); d > 200 {
		t.Fatalf("撤锚后父子距离 = %.1f, want ≈ edgeLength（松手后连接应恢复）", d)
	}
}
