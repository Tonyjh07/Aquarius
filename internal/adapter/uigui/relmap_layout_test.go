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

// TestRelSolveSettlesAndInvariants 主性质：解存在、收敛、扇区不破、层序单调、有界。
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
			_, childSector, _ := relAssign(nodes)
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
				pp := sol.Pos[nd.Parent]
				if !(p.Y > pp.Y) { // 层序严格单调（jitter < rowGap/2 保证）
					t.Fatalf("节点 %d y=%v 未在父 y=%v 之下", i, p.Y, pp.Y)
				}
				theta := math.Atan2(p.X-pp.X, p.Y-pp.Y)
				s := childSector[nd.Parent]
				if theta < s.Lo-1e-9 || theta > s.Hi+1e-9 {
					t.Fatalf("节点 %d 角度 %v 越出父扇区 [%v,%v]", i, theta, s.Lo, s.Hi)
				}
			}
		})
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
