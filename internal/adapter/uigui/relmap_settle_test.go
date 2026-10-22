package uigui

// relmap_settle_test.go 右图收敛与视场平衡：盒/填充率度量辅助 + 落定后盒仍在
// 视口内、上下留白均衡、跟随收口不回弹、标签字号夹取。

import (
	"fmt"
	"math"
	"testing"
	"time"

	"gioui.org/io/input"
	"github.com/Tonyjh07/Aquarius/internal/domain/conversation"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

// relWideGraph 宽树夹具：root → system → fan 个 user 分叉，每分叉再挂 depth 级链。
// 源档按「距父 100dp」拉、不看静息槽位 ⇒ 落定盒比静息盒**纵向**大一大截（宽树的病态档）。
func relWideGraph(fan, depth int) port.TreeGraph {
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	n := 0
	at := func() time.Time { n++; return base.Add(time.Duration(n) * time.Second) }
	var nodes []port.GraphNode
	mk := func(id, parent string, role conversation.Role, sibIdx, sibCount int) {
		nodes = append(nodes, port.GraphNode{
			ID: conversation.MessageID(id), Parent: conversation.MessageID(parent),
			Role: role, Snippet: "s-" + id, EdgeKind: port.EdgeSeq,
			SiblingIdx: sibIdx, SiblingCount: sibCount, CreatedAt: at(),
		})
	}
	mk("r", "", conversation.RoleRoot, 0, 1)
	mk("p", "r", conversation.RoleSystem, 0, 1)
	last := "p"
	for i := 0; i < fan; i++ {
		id := fmt.Sprintf("b%d", i)
		mk(id, "p", conversation.RoleUser, i, fan)
		cur := id
		for d := 1; d < depth; d++ {
			nid := fmt.Sprintf("%s_%d", id, d)
			role := conversation.RoleAssistant
			if d%2 == 1 {
				role = conversation.RoleUser
			}
			mk(nid, cur, role, 0, 1)
			cur = nid
		}
		last = cur
	}
	return port.TreeGraph{Anchor: conversation.MessageID(last), Nodes: nodes, Total: len(nodes)}
}

// relBoxOf 给定本地索引集在当前物理位形下的卡片外接盒（含 relFitMargin）。测试侧**独立
// 复算**：被测口径就是「可见卡片都在视口内」，若测试与实现借同一个函数，算错法相同就测不出错。
func relBoxOf(g *relGraph, idx []int) (minX, minY, maxX, maxY float64) {
	minX, minY = math.Inf(1), math.Inf(1)
	maxX, maxY = math.Inf(-1), math.Inf(-1)
	for _, li := range idx {
		hw, hh := relCardHalf(g.geo[li])
		p := g.sol.Pos[li]
		minX, maxX = math.Min(minX, p.X-hw), math.Max(maxX, p.X+hw)
		minY, maxY = math.Min(minY, p.Y-hh), math.Max(maxY, p.Y+hh)
	}
	padX := (maxX - minX) * relFitMargin
	padY := (maxY - minY) * relFitMargin
	return minX - padX, minY - padY, maxX + padX, maxY + padY
}

// relCurBox 全可见树的当前盒。
func relCurBox(g *relGraph) (minX, minY, maxX, maxY float64) {
	idx := make([]int, len(g.vis.nodes))
	for i := range idx {
		idx[i] = i
	}
	return relBoxOf(g, idx)
}

// relCardOutOfView 该可见节点的卡片盒是否越出视口（±1px 容差）。
func relCardOutOfView(g *relGraph, li int) bool {
	if g.sol == nil || li < 0 || li >= len(g.sol.Pos) {
		return true
	}
	xs, ys := g.toScreen(g.sol.Pos[li])
	hw, hh := relCardHalf(g.geo[li])
	x, y := float64(xs), float64(ys)
	return x-hw*g.z < -1 || x+hw*g.z > g.viewW+1 || y-hh*g.z < -1 || y+hh*g.z > g.viewH+1
}

// relViewGaps 盒相对视口的上/下留白（px；负 = 越界）。
func relViewGaps(g *relGraph) (top, bottom float64) {
	_, minY, _, maxY := relCurBox(g)
	return minY*g.z + g.offY, g.viewH - (maxY*g.z + g.offY)
}

// relFill 给定盒在视口里的贴满度（两维取大；1 = 恰有一维贴满视口）。
func relFill(g *relGraph, minX, minY, maxX, maxY float64) float64 {
	return math.Max((maxX-minX)*g.z/g.viewW, (maxY-minY)*g.z/g.viewH)
}

// relFillOf 给定索引集域盒的贴满度。
func relFillOf(g *relGraph, idx []int) float64 {
	minX, minY, maxX, maxY := relBoxOf(g, idx)
	return relFill(g, minX, minY, maxX, maxY)
}

// relBoxFill 全可见树盒的贴满度。
func relBoxFill(g *relGraph) float64 {
	minX, minY, maxX, maxY := relCurBox(g)
	return relFill(g, minX, minY, maxX, maxY)
}

// relDriveDone 驱动到「物理静止 **且** 视口跟随收敛」，收敛不了直接失败（跟随不许空转）。
func relDriveDone(t *testing.T, q *input.Router, u *UI, st *historyState) {
	t.Helper()
	relDriveQuiet(q, u, st, relSettleFrames*4)
	if st.graph.lay == nil || !st.graph.lay.settled {
		t.Fatal("物理未收敛")
	}
	if st.graph.fitFollow {
		t.Fatal("视口跟随未收敛（空转重绘）")
	}
}

// TestRelGraphSettledBoxStaysInView V1a/V2（D140 主复现）：舒展落定后，可见节点的卡片必须
// **全在视口内**；只有 zoom 触底（relZoomMin，超长会话）才退化为「Head 在场」（V2）。
// 修前：wide12x3 38 节点出 27、上越界 461px（用户截图那一幕）。
func TestRelGraphSettledBoxStaysInView(t *testing.T) {
	cases := []struct {
		name string
		g    port.TreeGraph
	}{
		{"fixture7", relFixtureGraph()},
		{"wide12x3", relWideGraph(12, 3)},
		{"wide24x1", relWideGraph(24, 1)},
		{"chain120", relChainGraph(120)}, // zoom 触底档：只承诺 V2
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			u := newHistUI(fakeRelTree{g: c.g}, fakeLister(nil))
			st := newHistoryState(u)
			q := new(input.Router)
			relGFrame(q, u, st)
			relDriveDone(t, q, u, st)
			g := st.graph
			floor := g.zoom <= relZoomMin+1e-9
			for li := range st.vis.nodes {
				if !relCardOutOfView(g, li) || floor {
					continue
				}
				xs, ys := g.toScreen(g.sol.Pos[li])
				t.Fatalf("落定后节点 %d 出视口 (%v,%v)，视口 %v×%v（zoom %.3f）",
					li, xs, ys, g.viewW, g.viewH, g.zoom)
			}
			if li := g.anchor; li >= 0 && li < len(st.vis.nodes) { // V2：Head 恒在场
				if relCardOutOfView(g, li) {
					xs, ys := g.toScreen(g.sol.Pos[li])
					t.Fatalf("落定后 Head 出视口 (%v,%v)，视口 %v×%v", xs, ys, g.viewW, g.viewH)
				}
			}
		})
	}
}

// TestRelGraphSettledViewBalanced V1b/V1c（D140；用户「舒展后下方空白小一点」）：盒装得下
// 时上下留白必须**对称**（P2 盒心居中；修前 fixture7 是 225/78、wide12x3 是 −461/197），
// 且未触上下限时至少一维贴满（zoom 按当前盒收口，不是按静息盒）。
func TestRelGraphSettledViewBalanced(t *testing.T) {
	cases := []struct {
		name string
		g    port.TreeGraph
	}{
		{"fixture7", relFixtureGraph()},
		{"wide12x3", relWideGraph(12, 3)},
		{"wide24x1", relWideGraph(24, 1)},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			u := newHistUI(fakeRelTree{g: c.g}, fakeLister(nil))
			st := newHistoryState(u)
			q := new(input.Router)
			relGFrame(q, u, st)
			relDriveDone(t, q, u, st)
			g := st.graph
			_, minY, _, maxY := relCurBox(g)
			if (maxY-minY)*g.z > g.viewH {
				return // 纵向装不下（触底档）：留白无从谈起
			}
			top, bot := relViewGaps(g)
			if top < -1 || bot < -1 {
				t.Fatalf("纵向越界：上留白 %.0f 下留白 %.0f（视口高 %.0f）", top, bot, g.viewH)
			}
			if math.Abs(top-bot) > 8 {
				t.Fatalf("上下留白不对称：上 %.0f 下 %.0f（P2 应盒心居中）", top, bot)
			}
			if g.zoom > relZoomMin+1e-9 && g.zoom < relZoomMax-1e-9 {
				if f := relBoxFill(g); f < 0.99 {
					t.Fatalf("两维都没贴满（贴满度 %.2f）：zoom 没按当前盒收口", f)
				}
			}
		})
	}
}

// TestRelGraphFollowZoomNoBounce D2 改订（D140）：展开期（物理未静止）zoom **单调不增**
// ——盒子只长 ⇒ 只缩，不会有「放大—缩回」的来回摆动（抖动会让人眼盯不住树）。落定后允许
// 放大一次把留白填掉（V1c），故判据只在 settled 之前生效。
func TestRelGraphFollowZoomNoBounce(t *testing.T) {
	u := newHistUI(fakeRelTree{g: relWideGraph(12, 3)}, fakeLister(nil))
	st := newHistoryState(u)
	q := new(input.Router)
	relGFrame(q, u, st)
	g := st.graph
	prev := g.zoom
	expanding := false
	for i := 0; i < relSettleFrames*4; i++ {
		relGFrame(q, u, st)
		if g.lay == nil || g.lay.settled {
			break // 落定帧起的「放大填留白」不算回弹
		}
		expanding = true
		if g.zoom > prev+1e-9 {
			t.Fatalf("展开期 zoom 回弹（第 %d 帧）：%.4f → %.4f", i, prev, g.zoom)
		}
		prev = g.zoom
	}
	if !expanding {
		t.Fatal("夹具没经历展开期（测试失效）")
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
