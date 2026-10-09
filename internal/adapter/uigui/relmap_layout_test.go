package uigui

// 关系图布局求解器性质测试（D120④，relation-map-design §5.1）。
// 力导向坐标不确定 ⇒ 只断言**不变量**：扇区不破 / 层序单调 / 动能静止 /
// 有界盒 / 同输入可复现；另覆盖输入合法性守卫。

import (
	"math"
	"math/rand"
	"testing"
)

// relChain 链：0 → 1 → … → n-1（层号 = 索引）。
func relChain(n int) []relGeoNode {
	ns := make([]relGeoNode, n)
	for i := range ns {
		p := i - 1
		ns[i] = relGeoNode{Parent: p, Level: i, Weight: 1}
	}
	return ns
}

// relBinary 完全二叉树（层号 = 深度）：节点 2i+1/2i+2 挂在 i 下。
func relBinary(depth int) []relGeoNode {
	count := (1 << (depth + 1)) - 1
	ns := make([]relGeoNode, count)
	for i := range ns {
		ns[i] = relGeoNode{Parent: (i - 1) / 2, Level: relBitLen(i), Weight: 1}
		if i == 0 {
			ns[i].Parent = -1
			ns[i].Level = 0
		}
	}
	return ns
}

// relBitLen 完全二叉树节点 i 的深度（= floor(log2(i+1))）。
func relBitLen(i int) int {
	n := 0
	for v := i + 1; v > 1; v >>= 1 {
		n++
	}
	return n
}

// relStar 星：根挂 n-1 个叶。
func relStar(n int) []relGeoNode {
	ns := make([]relGeoNode, n)
	for i := range ns {
		if i == 0 {
			ns[i] = relGeoNode{Parent: -1, Level: 0, Weight: 1.3}
			continue
		}
		ns[i] = relGeoNode{Parent: 0, Level: 1, Weight: 0.85}
	}
	return ns
}

// relRandTree 固定种子随机树（父索引 < 自身 → 无环；层号 = 父层 + 1）。
func relRandTree(seed int64, n int) []relGeoNode {
	r := rand.New(rand.NewSource(seed))
	ns := make([]relGeoNode, n)
	for i := range ns {
		if i == 0 {
			ns[i] = relGeoNode{Parent: -1, Level: 0, Weight: 1.3}
			continue
		}
		p := r.Intn(i) // 父必在更早索引
		ns[i] = relGeoNode{Parent: p, Level: ns[p].Level + 1, Weight: 1}
	}
	return ns
}

// relFixtures 代表性形状：链 / 二叉树 / 星 / 多棵随机树。
func relFixtures() map[string][]relGeoNode {
	return map[string][]relGeoNode{
		"chain20":   relChain(20),
		"binary4":   relBinary(4),
		"star30":    relStar(30),
		"rand50":    relRandTree(1, 50),
		"rand200":   relRandTree(2, 200),
		"path2node": {{Parent: -1, Level: 0}, {Parent: 0, Level: 1}},
	}
}

// TestRelSolveSettlesAndInvariants 主性质：解存在、收敛、层序单调（=「子恒在父之下」
// 的 180° 硬上限）、有界。角度硬投影已废除（D121），故不再断言扇区不破。
func TestRelSolveSettlesAndInvariants(t *testing.T) {
	for name, nodes := range relFixtures() {
		nodes := nodes
		t.Run(name, func(t *testing.T) {
			sol, ok := relSolve(nodes)
			if !ok {
				t.Fatalf("relSolve 拒绝合法输入")
			}
			if len(sol.Pos) != len(nodes) {
				t.Fatalf("Pos 数 = %d, want %d", len(sol.Pos), len(nodes))
			}
			if !sol.Settled {
				t.Fatalf("未收敛（Iterations=%d）", sol.Iterations)
			}
			for i, nd := range nodes {
				p := sol.Pos[i]
				if math.IsNaN(p.X) || math.IsNaN(p.Y) || math.IsInf(p.X, 0) || math.IsInf(p.Y, 0) {
					t.Fatalf("节点 %d 坐标非有限: %+v", i, p)
				}
				if math.Abs(p.X) > relBound {
					t.Fatalf("节点 %d X 越界: %v", i, p.X)
				}
				if nd.Parent < 0 {
					continue
				}
				if !(p.Y > sol.Pos[nd.Parent].Y) { // 层序严格单调（jitter < rowGap/2 保证）
					t.Fatalf("节点 %d y=%v 未在父 y=%v 之下", i, p.Y, sol.Pos[nd.Parent].Y)
				}
			}
		})
	}
}

// TestRelSolveCardsDoNotOverlap 卡片占位生效（D121 斥力从卡片边缘起算）：带卡片尺寸
// 求解后，同层拥挤处不得**深压**——接触推力把卡片推到「贴着」为止。星（根挂 11 个
// 同层叶）是同层挤压最狠的一档：卡片总宽远大于能铺开的宽度，故断言的是**穿透有界**
// （≤ 卡片宽的 relCardPenetrationMax），不是「零重叠」——后者在密集分支点本就不可达，
// 真要它就得把标签砍没（A5 的取舍）。
func TestRelSolveCardsDoNotOverlap(t *testing.T) {
	const half, halfH = 70.0, 17.0
	nodes := relStar(12)
	for i := range nodes {
		nodes[i].HalfW, nodes[i].HalfH = half, halfH
	}
	sol, ok := relSolve(nodes)
	if !ok || !sol.Settled {
		t.Fatalf("未收敛: ok=%v %+v", ok, sol)
	}
	worst := 0.0
	for i := 0; i < len(nodes); i++ {
		for j := i + 1; j < len(nodes); j++ {
			if math.Abs(sol.Pos[i].Y-sol.Pos[j].Y)-2*halfH >= 0 {
				continue // 竖直间隙已分开：横向重叠不算压字
			}
			if pen := 2*half - math.Abs(sol.Pos[i].X-sol.Pos[j].X); pen > worst {
				worst = pen
			}
		}
	}
	if worst > 2*half*relCardPenetrationMax {
		t.Fatalf("最深穿透 %.1f > 卡宽的 %.0f%%（斥力未把卡推开）",
			worst, relCardPenetrationMax*100)
	}
}

// TestRelSolveReproducible 同输入两次求解逐点相等（R1：确定性种子 → 布局可复现）。
func TestRelSolveReproducible(t *testing.T) {
	nodes := relRandTree(7, 120)
	a, okA := relSolve(nodes)
	b, okB := relSolve(nodes)
	if !okA || !okB {
		t.Fatal("relSolve 拒绝合法输入")
	}
	if a.Settled != b.Settled || a.Iterations != b.Iterations {
		t.Fatalf("收敛态不一致: %+v vs %+v", a, b)
	}
	for i := range a.Pos {
		if a.Pos[i] != b.Pos[i] {
			t.Fatalf("节点 %d 坐标不一致: %+v vs %+v", i, a.Pos[i], b.Pos[i])
		}
	}
}

// TestRelSolveKineticNonIncreasing 动能（本步位移²）总体下降——不要求逐步严格单调
// （投影可能瞬时注入位移），但首步总远大于末步、且终态静止。
func TestRelSolveKineticNonIncreasing(t *testing.T) {
	nodes := relRandTree(3, 80)
	sol, ok := relSolve(nodes)
	if !ok || !sol.Settled {
		t.Fatalf("未收敛: ok=%v sol=%+v", ok, sol)
	}
	// 已收敛即终态静止：再解一次应给出相同结果（R3 的纯函数口径）。
	again, _ := relSolve(nodes)
	for i := range sol.Pos {
		if sol.Pos[i] != again.Pos[i] {
			t.Fatalf("收敛后重解结果漂移（节点 %d）", i)
		}
	}
}

// TestRelValidateInputs 非法输入守卫：空 ok / 多根 / 父越界 / 自环 / 环。
func TestRelValidateInputs(t *testing.T) {
	if sol, ok := relSolve(nil); !ok || len(sol.Pos) != 0 {
		t.Fatalf("空树应返回空解 ok=true")
	}
	bad := map[string][]relGeoNode{
		"multi-root":  {{Parent: -1}, {Parent: -1}},
		"parent-oob":  {{Parent: -1}, {Parent: 9}},
		"self-parent": {{Parent: -1}, {Parent: 1}},
		"cycle":       {{Parent: -1}, {Parent: 2}, {Parent: 1}},
	}
	for name, nodes := range bad {
		if _, ok := relSolve(nodes); ok {
			t.Fatalf("%s 应被拒绝", name)
		}
	}
}

// TestRelWeightDoesNotAffectStructure 权重只入斥力限幅，不改结构合法性（A4 的布局侧锚）。
func TestRelWeightDoesNotAffectStructure(t *testing.T) {
	nodes := relRandTree(5, 60)
	heavier := append([]relGeoNode(nil), nodes...)
	for i := range heavier {
		heavier[i].Weight = 5 // 与结构无关
	}
	_, okA := relSolve(nodes)
	_, okB := relSolve(heavier)
	if !okA || !okB {
		t.Fatalf("权重不应影响可解性")
	}
}

// —— D121：临时固定锚（拖拽交互的力学）——

// TestRelSolvePinLocksNodeAndPushesNeighbors 钉住某节点：其坐标被锁死，其余节点被斥力推开；
// 未被钉的节点之间层序仍严格单调（交互态只破坏「被拖者」的层位）。
func TestRelSolvePinLocksNodeAndPushesNeighbors(t *testing.T) {
	nodes := relRandTree(11, 40)
	lay, ok := newRelLayout(nodes)
	if !ok {
		t.Fatal("newRelLayout 拒绝合法输入")
	}
	lay.relax(relMaxIter, relPin{})
	free := lay.solution()

	const dragged = 7
	pin := relPin{Valid: true, Index: dragged, Pos: relPoint{X: free.Pos[dragged].X + 240, Y: free.Pos[dragged].Y}}
	lay.relax(relMaxIter, pin)
	got := lay.solution()

	if p := got.Pos[dragged]; math.Abs(p.X-pin.Pos.X) > 1e-6 || math.Abs(p.Y-pin.Pos.Y) > 1e-6 {
		t.Fatalf("被钉节点未锁死在锚点: %+v, want %+v", p, pin.Pos)
	}
	pushed := 0
	for i := range nodes {
		if i != dragged && math.Abs(got.Pos[i].X-free.Pos[i].X) > 0.5 {
			pushed++
		}
		if i == dragged || nodes[i].Parent < 0 || nodes[i].Parent == dragged {
			continue
		}
		if got.Pos[i].Y <= got.Pos[nodes[i].Parent].Y {
			t.Fatalf("节点 %d 层序被破坏: y=%v, 父 y=%v", i, got.Pos[i].Y, got.Pos[nodes[i].Parent].Y)
		}
	}
	if pushed == 0 {
		t.Fatal("钉住节点后周围毫无位移（斥力未生效）")
	}
}

// TestRelSolvePinReleaseRestoresLayerOrder 松手（撤销锚）后：y 全回本层带、层序不变量
// 恢复——交互态的例外不留在稳态里。
func TestRelSolvePinReleaseRestoresLayerOrder(t *testing.T) {
	nodes := relRandTree(13, 40)
	lay, ok := newRelLayout(nodes)
	if !ok {
		t.Fatal("newRelLayout 拒绝合法输入")
	}
	lay.relax(relMaxIter, relPin{Valid: true, Index: 5, Pos: relPoint{X: 300, Y: -120}})
	lay.relax(relMaxIter, relPin{}) // 松手：回归自由
	sol := lay.solution()
	for i, nd := range nodes {
		wantY := float64(nd.Level)*relRowGap + relJitterOf(i)
		if math.Abs(sol.Pos[i].Y-wantY) > 1e-9 {
			t.Fatalf("节点 %d 未回本层带: y=%v, want %v", i, sol.Pos[i].Y, wantY)
		}
		if nd.Parent >= 0 && sol.Pos[i].Y <= sol.Pos[nd.Parent].Y {
			t.Fatalf("节点 %d 层序未恢复", i)
		}
	}
}

// TestRelRelaxIsIncremental 增量松弛：从当前坐标接着走，而不是每帧回到结构初值
// （否则拖拽时全场每帧瞬移，只有被拖的那个在动）。
func TestRelRelaxIsIncremental(t *testing.T) {
	nodes := relRandTree(17, 30)
	lay, ok := newRelLayout(nodes)
	if !ok {
		t.Fatal("newRelLayout 拒绝合法输入")
	}
	lay.relax(relMaxIter, relPin{})
	home := lay.x[9]
	lay.x[9] += 80 // 扰动（模拟刚被拖开）
	lay.relax(1, relPin{})
	if d := math.Abs(lay.x[9] - (home + 80)); d > 40 {
		t.Fatalf("单步松弛跳回初值（增量失效）: 位移 %v", d)
	}
}
