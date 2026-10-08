package uigui

// 会话树关系图——布局求解器（D112② / D120④，relation-map-design §5.1）。
//
// 设计语言只规定布局的**性格**（布局是可替换轴，§8）：**斥力主导 + 角度约束 + 层序投影**。
// 本文件是 Gio / port 无关的纯几何核：
//   - 输入 = 极小几何投影（父索引 / 层号 / 权重），便于性质测试与基准构造；
//   - 输出 = 每节点坐标 + 是否收敛。
// port.TreeGraph → 本输入的映射（派生 Level/Depth/InPath/扇区/文案）在 relmap.go（第 5 步）。
//
// 可测性口径（§5.1）：力导向坐标不确定 ⇒ **断言不变量而非坐标**：
//
//	扇区不破（子角度 ∈ 父分配扇区）· 层序单调（y 严格递增）· 动能降至静止 ·
//	有界盒（无 NaN/坐标爆炸）· 同输入可复现（两次运行逐点相等）。
//
// 与主窗纪律一致（§10.2/R3）：收敛即停；平移/缩放**不**触发重排（重排只由数据变更 /
// rebudget 触发，见第 7 步帧层）。

import "math"

// 求解器档位（阶段 4「spike 前不定档」的落点，§5.4.4）。实测（Ryzen 7 8700F，见
// relmap_layout_bench_test.go）：50 节点 ≈ 0.03ms、200 ≈ 1.1ms、500 ≈ 178ms。
// 500 的代价来自 O(n²)·迭代，交互不可接受 ⇒ hardCap 收在 240（≈2ms 一次性重排）。
// 这几条是**偶然实现**（§7 剥离清单），不是设计语言的一部分——随基准数据调整。
const (
	relMaxIter = 400 // 迭代上限（500 节点实测 345 收敛；兜底）
	// relHardCap 呈现节点数上限：超过则 UI 侧优先收窄 maxDepth（砍远端整层，§5.4.3），
	// 不逐个截断——保证「主干完整 + 远端按层消失」的语义（A6）。由 spike 定档。
	relHardCap = 240
	relEps     = 0.25  // 动能静止阈值（Σ 本步位移²）/ 节点数（→ 平均每节点约 0.5dp 位移）
	relRowGap  = 72.0  // 层序投影：每层竖直间距（dp）
	relJitter  = 12.0  // 行内竖直抖动上限（< rowGap/2 → 层带不重叠 → 层序严格单调）
	relKElec   = 8.0e3 // 斥力系数基准
	// relKRepelMax 斥力系数上限（R5 限幅：高度数不得把邻居推出视野）。
	relKRepelMax = 6.0e3
	relSpringK   = 0.04  // 边弹簧刚度（子被拉向父的 x）
	relGain      = 0.08  // 每步位移增益（梯度下降步长；无惯性 → 稳定收敛）
	relSoft      = 64.0  // 斥力软化（dp²；避免近重合奇异力 → 定步振荡）
	relCut       = 420.0 // 斥力作用半径（dp）：超出即不互斥——砍长程慢漂移模态、加速收敛
	relVMax      = 24.0  // 单步位移上限（防爆）
	relBound     = 20000.0
	relSectorMin = 0.05 // 子扇区最小半宽（弧度）
	relSectorMax = 1.20 // 子扇区最大半宽（≈69°）
	// relMaxAngle 任意扇区允许的最大 |角度|（< π/2）：投影 A 用 tan/atan2 互逆，
	// 必须保证扇区落在正下方半平面内，否则 tan 周期折叠 → 角度不变量被破坏。
	relMaxAngle = 1.35 // ≈77°
	relRootHalf = 1.25 // 根的子扇区半宽（≈72°）
	relHwBase   = 0.30 // 扇区半宽随子树规模增长系数
)

// relGeoNode 布局输入：几何相关的最小投影（与 port 解耦——性质测试与基准可直接构造）。
// Parent = -1 表示结构根；Level 为到结构根的跳数（根 = 0）；Weight 仅参与斥力限幅与
// 绘制（不影响大小语义，A4：大小与 degree 解耦）。
type relGeoNode struct {
	Parent int
	Level  int
	Weight float64
}

// relPoint 布局坐标（dp；y 向下 = 对话推进方向，顶部最早/根，底部为最新/Head）。
type relPoint struct{ X, Y float64 }

// relSector 角度扇区 [Lo, Hi]（弧度，0 = 正下方，正 = 右）。
type relSector struct{ Lo, Hi float64 }

// relSolution 布局结果：Pos 与输入同序；Settled = 动能已降至阈值内。
type relSolution struct {
	Pos        []relPoint
	Iterations int
	Settled    bool
}

// relSolve 求解一棵树的布局。ok=false 表示输入不合格（空/多根/环/父越界）。
// 空树返回空解（ok=true）。
func relSolve(nodes []relGeoNode) (*relSolution, bool) {
	n := len(nodes)
	if n == 0 {
		return &relSolution{}, true
	}
	assigned, childSector, ok := relAssign(nodes)
	if !ok {
		return nil, false
	}
	order, _ := relBFS(nodes)
	root := order[0]

	children := make([][]int, n)
	deg := make([]int, n)
	for i, nd := range nodes {
		if nd.Parent >= 0 {
			children[nd.Parent] = append(children[nd.Parent], i)
			deg[nd.Parent]++
		}
	}

	// 初始几何：y 由层序投影直接解出（含确定性行内抖动）；x 由分配扇区中心角展开。
	y := make([]float64, n)
	x := make([]float64, n)
	for i := range nodes {
		y[i] = float64(nodes[i].Level)*relRowGap + relJitterOf(i)
	}
	for _, p := range order {
		for _, k := range children[p] {
			theta := relClamp((assigned[k].Lo+assigned[k].Hi)/2, childSector[p])
			x[k] = x[p] + (y[k]-y[p])*math.Tan(theta)
		}
	}

	prev := make([]float64, n)
	copy(prev, x)
	f := make([]float64, n)

	it := 0
	settled := false
	for it = 0; it < relMaxIter; it++ {
		for i := range f {
			f[i] = 0
		}
		// 1. 斥力：全对互斥，系数 min(kRepel·(deg_i+1)·(deg_j+1), kMax)（R5 限幅）；
		//    只作用在 x —— y 由层序投影主导（分层布局的常见退化，减少自由度、利于收敛）。
		for i := 0; i < n; i++ {
			for j := i + 1; j < n; j++ {
				dx := x[i] - x[j]
				dy := y[i] - y[j]
				d2 := dx*dx + dy*dy
				if d2 < 1e-4 { // 确定性微扰，避免重叠除零
					dx = 0.1 + float64(i-j)*0.01
					dy = 0.1
					d2 = dx*dx + dy*dy
				}
				if d2 > relCut*relCut { // 超出作用半径：不互斥
					continue
				}
				coef := relKElec * float64(deg[i]+1) * float64(deg[j]+1)
				if coef > relKRepelMax {
					coef = relKRepelMax
				}
				mag := coef / (d2 + relSoft)
				f[i] += mag * (dx / math.Sqrt(d2))
				f[j] -= mag * (dx / math.Sqrt(d2))
			}
		}
		// 2. 弹簧：子被拉向父的 x（父自身由其父牵；根钉在 x=0）。
		for i := range nodes {
			if p := nodes[i].Parent; p >= 0 {
				f[i] += relSpringK * (x[p] - x[i])
			}
		}
		// 3. 位移（无惯性的梯度下降步；单步限幅防爆）。
		for i := range x {
			d := f[i] * relGain
			if d > relVMax {
				d = relVMax
			} else if d < -relVMax {
				d = -relVMax
			}
			x[i] += d
		}
		x[root] = 0
		// 4. 投影 A（角度扇区）：子角度夹进父分配扇区。
		for _, p := range order {
			for _, k := range children[p] {
				dy := y[k] - y[p]
				theta := relClamp(math.Atan2(x[k]-x[p], dy), childSector[p])
				x[k] = x[p] + dy*math.Tan(theta)
			}
		}
		// 5. 有界盒：无 NaN / 坐标爆炸（A4/R5 的可观测底线）。
		for i := range x {
			if math.IsNaN(x[i]) || math.Abs(x[i]) > relBound {
				x[i] = relBoundFinite(x[i])
			}
		}
		// 动能 = 本步真实位移（含投影位移）。
		kin := 0.0
		for i := range x {
			d := x[i] - prev[i]
			prev[i] = x[i]
			kin += d * d
		}
		if kin < relEps*float64(n) {
			settled = true
			it++
			break
		}
	}

	pos := make([]relPoint, n)
	for i := range x {
		pos[i] = relPoint{X: x[i], Y: y[i]}
	}
	return &relSolution{Pos: pos, Iterations: it, Settled: settled}, true
}

// relBFS 自根宽度优先，返回索引序（根首）；ok=false = 结构不合法（空/多根/环/父越界）。
func relBFS(nodes []relGeoNode) ([]int, bool) {
	n := len(nodes)
	if n == 0 {
		return nil, false
	}
	children := make([][]int, n)
	root := -1
	roots := 0
	for i, nd := range nodes {
		if nd.Parent < 0 {
			roots++
			root = i
			continue
		}
		if nd.Parent >= n || nd.Parent == i {
			return nil, false
		}
		children[nd.Parent] = append(children[nd.Parent], i)
	}
	if roots != 1 {
		return nil, false
	}
	order := make([]int, 0, n)
	queue := []int{root}
	for len(queue) > 0 {
		i := queue[0]
		queue = queue[1:]
		order = append(order, i)
		queue = append(queue, children[i]...)
	}
	if len(order) != n { // 存在未达节点 = 环
		return nil, false
	}
	return order, true
}

// relSubtree 每节点子树规模（含自身）；输入须合法。
func relSubtree(nodes []relGeoNode) []int {
	n := len(nodes)
	size := make([]int, n)
	order, ok := relBFS(nodes)
	if !ok {
		return size
	}
	for i := len(order) - 1; i >= 0; i-- { // 逆 BFS：子先于父聚合
		idx := order[i]
		size[idx]++
		if p := nodes[idx].Parent; p >= 0 {
			size[p] += size[idx]
		}
	}
	return size
}

// relAssign 分配角度扇区（确定性、一次）：
//   - assigned[i] = 节点 i 作为孩子所占的角度区间（其 angle 初值取中点）；
//   - childSector[i] = 节点 i 分配给**其孩子**的扇区（宽度 ∝ clamp(子树规模)）。
//
// 不变量：assigned[child] ⊆ childSector[parent]（子扇区由父扇区等比例切分）。
func relAssign(nodes []relGeoNode) (assigned, childSector []relSector, ok bool) {
	order, ok := relBFS(nodes)
	if !ok {
		return nil, nil, false
	}
	n := len(nodes)
	size := relSubtree(nodes)
	assigned = make([]relSector, n)
	childSector = make([]relSector, n)
	children := make([][]int, n)
	for i, nd := range nodes {
		if nd.Parent >= 0 {
			children[nd.Parent] = append(children[nd.Parent], i)
		}
	}
	root := order[0]
	childSector[root] = relClampSector(relSector{-relRootHalf, relRootHalf})
	assigned[root] = relSector{0, 0}
	for _, p := range order {
		kids := children[p]
		if len(kids) == 0 {
			continue
		}
		total := 0.0
		for _, k := range kids {
			total += float64(size[k])
		}
		lo, hi := childSector[p].Lo, childSector[p].Hi
		cur := lo
		for _, k := range kids {
			share := (hi - lo) * float64(size[k]) / total
			assigned[k] = relSector{Lo: cur, Hi: cur + share}
			cur += share
			mid := (assigned[k].Lo + assigned[k].Hi) / 2
			hw := relHw(size[k])
			childSector[k] = relClampSector(relSector{Lo: mid - hw, Hi: mid + hw})
		}
	}
	return assigned, childSector, true
}

// relHw 子扇区半宽：随子树规模增长、钳在 [min,max]（R5：宽度按子树规模分配但受限）。
func relHw(subtreeSize int) float64 {
	hw := relHwBase * math.Sqrt(float64(subtreeSize-1))
	if hw < relSectorMin {
		hw = relSectorMin
	}
	if hw > relSectorMax {
		hw = relSectorMax
	}
	return hw
}

// relClamp 把角度夹进扇区。
func relClamp(theta float64, s relSector) float64 {
	if theta < s.Lo {
		return s.Lo
	}
	if theta > s.Hi {
		return s.Hi
	}
	return theta
}

// relClampSector 把扇区夹进正下方半平面的可表示范围 [-relMaxAngle, relMaxAngle]：
// 投影 A 依赖 tan/atan2 在 (-π/2, π/2) 上互逆，越界会因 tan 周期折叠破坏角度不变量。
func relClampSector(s relSector) relSector {
	if s.Lo < -relMaxAngle {
		s.Lo = -relMaxAngle
	}
	if s.Hi > relMaxAngle {
		s.Hi = relMaxAngle
	}
	if s.Hi < s.Lo {
		s.Hi = s.Lo
	}
	return s
}

// relBoundFinite 有界盒落点：NaN → 0；越界 → 夹到 ±relBound。
func relBoundFinite(x float64) float64 {
	if math.IsNaN(x) {
		return 0
	}
	if x > relBound {
		return relBound
	}
	if x < -relBound {
		return -relBound
	}
	return x
}

// relJitterOf 确定性的行内竖直抖动 ∈ [-relJitter, relJitter]（同索引恒同值 → 布局可复现）。
func relJitterOf(i int) float64 {
	f := math.Sin(float64(i)*12.9898+1.0) * 43758.5453
	f -= math.Floor(f)
	return (f*2 - 1) * relJitter
}
