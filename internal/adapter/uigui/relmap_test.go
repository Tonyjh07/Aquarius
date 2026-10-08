package uigui

// 关系图派生纯函数测试（D120⑤，relation-map-design §6.2/§6.3）：逐条钉住 A1a/A1b/A2/
// A3/A4/A5/A6/A7/A11 + 结构合法性守卫 + 派生→布局的联调。

import (
	"testing"
	"time"

	"github.com/Tonyjh07/Aquarius/internal/domain/conversation"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

// relFixtureGraph 显式构造一棵带分叉的会话树快照（用于派生断言）：
//
//	r(root) → p(system) → u1(user) ─┬─ a1(assistant) → u2(user, 锚点)
//	                    └ u1b(user) └─ a2(assistant, 版本链边)
//
// inPath = {r,p,u1,a1,u2}；u1b/a2 为分叉位点（各 sib 1/2）。
func relFixtureGraph() port.TreeGraph {
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	at := func(i int) time.Time { return base.Add(time.Duration(i) * time.Second) }
	mk := func(id, parent string, role conversation.Role, sibIdx, sibCount int, ek port.EdgeKind) port.GraphNode {
		return port.GraphNode{
			ID: conversation.MessageID(id), Parent: conversation.MessageID(parent),
			Role: role, Snippet: "s-" + id, EdgeKind: ek,
			SiblingIdx: sibIdx, SiblingCount: sibCount,
		}
	}
	nodes := []port.GraphNode{
		mk("r", "", conversation.RoleRoot, 0, 1, ""),
		mk("p", "r", conversation.RoleSystem, 0, 1, port.EdgeSeq),
		mk("u1", "p", conversation.RoleUser, 0, 2, port.EdgeSeq),
		mk("u1b", "p", conversation.RoleUser, 1, 2, port.EdgeSeq),
		mk("a1", "u1", conversation.RoleAssistant, 0, 2, port.EdgeSeq),
		mk("a2", "u1", conversation.RoleAssistant, 1, 2, port.EdgeRevise),
		mk("u2", "a1", conversation.RoleUser, 0, 1, port.EdgeSeq),
	}
	for i := range nodes {
		nodes[i].CreatedAt = at(i)
	}
	return port.TreeGraph{Anchor: "u2", Nodes: nodes, Total: len(nodes)}
}

func relMustModel(t *testing.T, g port.TreeGraph) *relModel {
	t.Helper()
	m, ok := relNewModel(g)
	if !ok {
		t.Fatal("relNewModel 拒绝合法快照")
	}
	return m
}

func relIdx(t *testing.T, m *relModel, id string) int {
	t.Helper()
	i, ok := m.index[conversation.MessageID(id)]
	if !ok {
		t.Fatalf("节点 %s 不在模型", id)
	}
	return i
}

// TestRelA1a_SingleRoot 恰有一个 level=0 节点（结构根唯一，A1a）。
func TestRelA1a_SingleRoot(t *testing.T) {
	m := relMustModel(t, relFixtureGraph())
	zeros := 0
	for i := range m.nodes {
		if m.nodes[i].level == 0 {
			zeros++
			if m.index[conversation.MessageID("r")] != i {
				t.Fatalf("level=0 的不是结构根")
			}
		}
	}
	if zeros != 1 {
		t.Fatalf("level=0 节点数 = %d, want 1", zeros)
	}
}

// TestRelA1b_AnchorExempt 预算切片 maxCount=1 → 仅锚点（豁免预算）。
func TestRelA1b_AnchorExempt(t *testing.T) {
	m := relMustModel(t, relFixtureGraph())
	vis := relBudget(m, 1)
	if len(vis.nodes) != 1 || vis.nodes[0] != m.anchor {
		t.Fatalf("maxCount=1 → %v, want [%d]（锚点）", vis.nodes, m.anchor)
	}
	if len(vis.edges) != 0 {
		t.Fatalf("单节点不应有边: %v", vis.edges)
	}
}

// TestRelA11_InPath IsPath 恰为一条根→锚点路径，长度 = level(anchor)+1。
func TestRelA11_InPath(t *testing.T) {
	m := relMustModel(t, relFixtureGraph())
	var path []int
	for i := m.anchor; i >= 0; i = m.nodes[i].parent {
		path = append(path, i)
	}
	if got, want := len(path), m.nodes[m.anchor].level+1; got != want {
		t.Fatalf("路径长度 = %d, want %d", got, want)
	}
	inPath := 0
	for i := range m.nodes {
		if m.nodes[i].inPath {
			inPath++
		}
	}
	if inPath != len(path) {
		t.Fatalf("InPath 数 = %d, want %d", inPath, len(path))
	}
	wantSet := map[conversation.MessageID]bool{"r": true, "p": true, "u1": true, "a1": true, "u2": true}
	for i := range m.nodes {
		if m.nodes[i].inPath != wantSet[m.nodes[i].id] {
			t.Fatalf("节点 %s inPath=%v, want %v", m.nodes[i].id, m.nodes[i].inPath, wantSet[m.nodes[i].id])
		}
	}
}

// TestRelA2_PaletteProjection 颜色是 Level mod 4 的纯投影。
func TestRelA2_PaletteProjection(t *testing.T) {
	for lvl := 0; lvl < 20; lvl++ {
		if got, want := relPaletteIndex(lvl), lvl%4; got != want {
			t.Fatalf("relPaletteIndex(%d) = %d, want %d", lvl, got, want)
		}
	}
}

// TestRelA3_EdgeDirection 每条断言满足 level(from) < level(to)，且 from 是 to 的父。
func TestRelA3_EdgeDirection(t *testing.T) {
	m := relMustModel(t, relFixtureGraph())
	vis := relBudget(m, len(m.nodes))
	for _, e := range vis.edges {
		from, to := e[0], e[1]
		if m.nodes[to].parent != from {
			t.Fatalf("断言 %d→%d 非父子", from, to)
		}
		if !(m.nodes[from].level < m.nodes[to].level) {
			t.Fatalf("断言 %d→%d 层序不严格", from, to)
		}
	}
}

// TestRelA4_WeightIndependentOfDegree 把孩子数改成 5，weight 不变。
func TestRelA4_WeightIndependentOfDegree(t *testing.T) {
	g := relFixtureGraph()
	m1 := relMustModel(t, g)
	// 目标：a2（w1 分叉位点）——给它加 5 个孩子，weight 必须不变。
	target := relIdx(t, m1, "a2")
	wBefore := m1.nodes[target].weight

	g2 := relFixtureGraph()
	base := g2.Nodes[len(g2.Nodes)-1].CreatedAt
	for k := 0; k < 5; k++ {
		g2.Nodes = append(g2.Nodes, port.GraphNode{
			ID: conversation.MessageID("a2kid" + itoa(k)), Parent: "a2",
			Role: conversation.RoleUser, Snippet: "x", CreatedAt: base.Add(time.Duration(k+1) * time.Second),
			EdgeKind: port.EdgeSeq, SiblingCount: 5,
		})
	}
	g2.Total = len(g2.Nodes)
	m2 := relMustModel(t, g2)
	wAfter := m2.nodes[relIdx(t, m2, "a2")].weight
	if wBefore != wAfter {
		t.Fatalf("加孩子后 weight 变化: %v → %v（A4 违例）", wBefore, wAfter)
	}
}

// TestRelA5_LabelMonotone 缩放单调：zoom 越小可见标签数不增；低于下限全隐。
func TestRelA5_LabelMonotone(t *testing.T) {
	weights := []float64{relW0, relW1, relW2, relW3}
	prev := len(weights) + 1
	for _, z := range []float64{1.5, 1.3, 1.1, 0.95, 0.85, 0.8, 0.75, 0.7, 0.5} {
		n := 0
		for _, w := range weights {
			if relLabelVisible(w, z) {
				n++
			}
		}
		if n > prev {
			t.Fatalf("zoom=%v 可见数 %d > 上个 zoom 的 %d（A5 单调违例）", z, n, prev)
		}
		prev = n
	}
	if relLabelVisible(relW3, 0.5) {
		t.Fatal("低于 zoom 下限应全隐（A5）")
	}
}

// TestRelA6_BudgetConsistent 预算后无悬空边：每条边两端都可见，且节点数 = 实际绘制数。
func TestRelA6_BudgetConsistent(t *testing.T) {
	m := relMustModel(t, relFixtureGraph())
	for _, cap := range []int{1, 2, 3, 4, 5, 7} {
		vis := relBudget(m, cap)
		visSet := make(map[int]bool, len(vis.nodes))
		for _, i := range vis.nodes {
			visSet[i] = true
		}
		for _, e := range vis.edges {
			if !visSet[e[0]] || !visSet[e[1]] {
				t.Fatalf("cap=%d 悬空边 %v", cap, e)
			}
		}
	}
}

// TestRelA7_TotalVsPresented 总量（Total）与已呈现量（len(nodes)）不同源、可同时取得。
func TestRelA7_TotalVsPresented(t *testing.T) {
	m := relMustModel(t, relFixtureGraph())
	vis := relBudget(m, 4)
	if m.total != 7 {
		t.Fatalf("total = %d, want 7", m.total)
	}
	if len(vis.nodes) != 4 {
		t.Fatalf("呈现 = %d, want 4", len(vis.nodes))
	}
}

// TestRelDeriveToLayout 派生 → 预算 → 布局联调：收敛、锚点局部下标有效、层序单调。
func TestRelDeriveToLayout(t *testing.T) {
	m := relMustModel(t, relFixtureGraph())
	vis := relBudget(m, len(m.nodes))
	nodes, anchor := relGeo(m, vis)
	if anchor < 0 {
		t.Fatal("锚点局部下标缺失")
	}
	sol, ok := relSolve(nodes)
	if !ok || !sol.Settled {
		t.Fatalf("联调布局未收敛: ok=%v sol=%+v", ok, sol)
	}
	if nodes[anchor].Level != m.nodes[m.anchor].level {
		t.Fatal("锚点层号不一致")
	}
}

// TestRelModelGuards 结构非法守卫：空 / 无根 / 多根 / 父缺失 / 自环。
func TestRelModelGuards(t *testing.T) {
	if _, ok := relNewModel(port.TreeGraph{}); ok {
		t.Fatal("空图应被拒绝")
	}
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	g := func(nodes ...port.GraphNode) port.TreeGraph {
		for i := range nodes {
			nodes[i].CreatedAt = base.Add(time.Duration(i) * time.Second)
		}
		return port.TreeGraph{Nodes: nodes, Total: len(nodes), Anchor: nodes[len(nodes)-1].ID}
	}
	bad := map[string]port.TreeGraph{
		"no-root":     g(port.GraphNode{ID: "a", Parent: "b", Role: conversation.RoleUser}, port.GraphNode{ID: "b", Parent: "a", Role: conversation.RoleUser}),
		"multi-root":  g(port.GraphNode{ID: "a", Role: conversation.RoleRoot}, port.GraphNode{ID: "b", Role: conversation.RoleRoot}),
		"missing-par": g(port.GraphNode{ID: "a", Role: conversation.RoleRoot}, port.GraphNode{ID: "b", Parent: "zzz", Role: conversation.RoleUser}),
		"self-parent": g(port.GraphNode{ID: "a", Role: conversation.RoleRoot}, port.GraphNode{ID: "b", Parent: "b", Role: conversation.RoleUser}),
	}
	for name, gg := range bad {
		if _, ok := relNewModel(gg); ok {
			t.Fatalf("%s 应被拒绝", name)
		}
	}
}

// TestRelShapeAndStatement 形状查表与陈述自足（含低 zoom 前缀）。
func TestRelShapeAndStatement(t *testing.T) {
	if relShapeOf(conversation.RoleRoot) != relShapeRing ||
		relShapeOf(conversation.RoleUser) != relShapeSquare ||
		relShapeOf(conversation.RoleSystem) != relShapeDiamond ||
		relShapeOf(conversation.RoleAssistant) != relShapeCircle {
		t.Fatal("形状查表错误")
	}
	m := relMustModel(t, relFixtureGraph())
	for i := range m.nodes {
		if relStatementOf(m, i, "测试会话") == "" {
			t.Fatalf("节点 %s 陈述为空", m.nodes[i].id)
		}
	}
	// 低 zoom：可见标签补 Role 前缀。
	txt := relLabelText(m, relIdx(t, m, "u2"), 0.9)
	if txt == "" || txt[:len("你：")] != "你：" {
		t.Fatalf("低 zoom 标签缺 Role 前缀: %q", txt)
	}
}
