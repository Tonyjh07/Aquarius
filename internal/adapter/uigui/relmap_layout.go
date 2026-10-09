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
// rebudget 触发，见第 7 步帧层）。**唯一例外**是拖拽节点的交互态（D121）：拖拽期间每帧
// 带「临时固定锚」增量松弛（relLayout.relax），松手锚消失 → 回归自由并收敛即停。

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
	// relSpringK 边弹簧刚度（子被拉向父的 x + 其分配角对应的静息偏移）。软弹簧 = 长边：
	// 力弱则斥力胜出，兄弟被推开、树铺得开；刚度大时所有子都贴到父的正下方，卡片必然压字。
	relSpringK = 0.012
	// relRestLen 静息边长的下限（dp）：无卡片占位（质点夹具）时的兄弟间距下限——「弹簧
	// 拉长」。有卡片时以卡片宽为准（见 seedSlots 的 pitch）。
	relRestLen = 90.0
	// relSlotGapDp 兄弟槽位之间的额外间隙（dp）：卡片并排时留条缝，免得描边/文字
	// 贴在一起读成一片。
	relSlotGapDp = 10.0
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
	// relFrameIter 单帧（拖拽/收敛）迭代上限（D121）：拖拽时每帧只走这些步，靠帧率推进收敛
	// ——单帧成本 = relFrameIter/relMaxIter 倍的一次全解（200 节点 ≈ 0.09ms）。
	relFrameIter = 32
	// relPushK / relPushMaxDp 卡片相叠时的水平接触推力（线性弹簧，刚度 k）：y 被层序
	// 投影钉死，卡片重叠只能靠 x 让开，故按**穿透深度**线性推开（接触力学；穿透过深
	// 时封顶，避免线性律爆掉）。0 半宽输入（性质测试夹具）时整段不触发。
	relPushK     = 0.5
	relPushMaxDp = 60.0
	// relCardPenetrationMax 允许的最深穿透（占卡宽比例，性质测试钉住）：接触推力把
	// 相叠卡片推到「贴着」附近，但密集分支点的卡片总宽本就大于可铺开宽度——零重叠在
	// A5 的取舍下不可达，故只钉「不深压」。
	relCardPenetrationMax = 0.25
)

// relGeoNode 布局输入：几何相关的最小投影（与 port 解耦——性质测试与基准可直接构造）。
// Parent = -1 表示结构根；Level 为到结构根的跳数（根 = 0）；Weight 仅参与斥力限幅与
// 绘制（不影响大小语义，A4：大小与 degree 解耦）。
//
// HalfW / HalfH = 该节点**卡片**的半宽/半高（dp；0 = 按质点处理）。斥力从卡片边缘起算
// （D121）：节点卡要装标签，宽度可观；按中心距排斥必然压字。这两个值**不随 zoom 变**
// ——否则「缩放」就会改变布局，违反 R3（平移/缩放不重排）。
type relGeoNode struct {
	Parent int
	Level  int
	Weight float64
	HalfW  float64
	HalfH  float64
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

// relPin 临时固定锚（D121，§5.2 增补）：把某节点钉在世界坐标 (X, Y)——**被钉者位移锁定**
// （不参与位移步与角度投影），但继续对邻居施斥力、被边弹簧拉扯。于是「拖开一个节点，
// 看谁被推开」成立。
//
// **零值 = 无固定**（Valid 显式声明，不用「Index = -1」当缺省：Go 零值 0 会把结构根
// 悄悄钉死——这类坑必须由构造方式堵死，而不是靠每个调用点自觉）。
//
// 这是 R3「懒重排」纪律的**交互态例外**：拖拽进行中每帧带锚松弛，松手后锚消失、
// 节点在拉力/斥力下自然收敛（R3 随即恢复：收敛即停）。
type relPin struct {
	Valid bool
	Index int
	Pos   relPoint
}

// relLayout 可增量松弛的求解态（D121）：派生量（孩子表/度数/BFS 序/子扇区）只算一次，
// 之后每次 relax 从**当前坐标**接着迭代。
//
// 为什么不能每次从结构初值重解：拖拽时那会让所有节点每帧都跳回原位，只有被拖的那个
// 在动——看起来像「全场瞬移」。故把求解态持有在帧层（relGraph.lay）。
type relLayout struct {
	nodes    []relGeoNode
	children [][]int
	deg      []int
	root     int
	halfW    []float64 // 卡片半宽（斥力从边缘起算，D121）
	halfH    []float64 // 卡片半高
	restOff  []float64 // 每节点的横向静息偏移（= relRestLen·sin(分配角)；根/非子 = 0）
	x, y     []float64
	prev, f  []float64
	iters    int
	settled  bool
}

// newRelLayout 结构校验 + 初值（层序投影 + 分配扇区中心角展开）。ok=false = 输入不合格。
func newRelLayout(nodes []relGeoNode) (*relLayout, bool) {
	n := len(nodes)
	if n == 0 {
		return &relLayout{}, true
	}
	assigned, _, ok := relAssign(nodes)
	if !ok {
		return nil, false
	}
	order, ok := relBFS(nodes)
	if !ok {
		return nil, false
	}
	l := &relLayout{
		nodes:    nodes,
		children: make([][]int, n),
		deg:      make([]int, n),
		root:     order[0],
		halfW:    make([]float64, n),
		halfH:    make([]float64, n),
		restOff:  make([]float64, n),
		x:        make([]float64, n),
		y:        make([]float64, n),
		prev:     make([]float64, n),
		f:        make([]float64, n),
	}
	for i, nd := range nodes {
		if nd.Parent >= 0 {
			l.children[nd.Parent] = append(l.children[nd.Parent], i)
			l.deg[nd.Parent]++
		}
		l.halfW[i], l.halfH[i] = nd.HalfW, nd.HalfH
	}
	// 初值：y 由层序投影直接解出（含确定性行内抖动）；x = 静息槽位（见 seedSlots）。
	for i := range nodes {
		l.y[i] = float64(nodes[i].Level)*relRowGap + relJitterOf(i)
	}
	l.seedSlots(assigned, order)
	copy(l.prev, l.x)
	l.settled = true
	return l, true
}

// 初值：y 由层序投影直接解出（含确定性行内抖动）；x = **静息槽位**（D121）。
//
// 槽位规则：同一父的 m 个孩子按分配角排序，等距铺在宽度 (m-1)·pitch 的槽上——
// pitch = max(卡片宽, relRestLen) + 间隙。于是「兄弟并排」是构造保证的，不靠斥力
// 挤出来；斥力/接触只负责**额外**拥挤（拖拽、远端挤压）。
// 扇区此后只提供**顺序与相对位置**（t ∈ [0,1]），不再夹角（角度硬投影已废除）。
func (l *relLayout) seedSlots(assigned []relSector, order []int) {
	for _, p := range order {
		kids := l.children[p]
		if len(kids) == 0 {
			continue
		}
		pitch := relRestLen
		for _, k := range kids { // 卡片更宽时以卡片为准（下限 = 静息长度）
			if w := 2*l.halfW[k] + relSlotGapDp; w > pitch {
				pitch = w
			}
		}
		lo := assigned[kids[0]].Lo
		hi := assigned[kids[len(kids)-1]].Hi
		span := float64(len(kids)-1) * pitch
		for _, k := range kids {
			mid := (assigned[k].Lo + assigned[k].Hi) / 2
			t := 0.5
			if hi > lo {
				t = (mid - lo) / (hi - lo)
			}
			l.restOff[k] = (t - 0.5) * span
			l.x[k] = l.x[p] + l.restOff[k]
		}
	}
}

// relax 从当前坐标迭代至收敛（或达 maxIter）。pin.Valid 时该节点坐标被锁死，
// 其余节点照常收敛——这就是拖拽「被拖者不动、周围被推开」的力学。
// 返回（迭代数, 是否已收敛）。
func (l *relLayout) relax(maxIter int, pin relPin) (int, bool) {
	n := len(l.nodes)
	if n == 0 {
		l.iters, l.settled = 0, true
		return 0, true
	}
	if pin.Valid && pin.Index >= n {
		pin.Valid = false // 越界锚视为无固定（防御：帧层本地索引与布局可能不同步）
	}
	pinned := pin.Valid
	// 投影 B（层序）：y 恒 = level·rowGap + 确定性抖动——被拖过 y 的节点松手即回到本层带，
	// 层序单调不变量不会因交互而**永久**破坏（交互态的例外只持续于拖拽期间）。
	for i, nd := range l.nodes {
		l.y[i] = float64(nd.Level)*relRowGap + relJitterOf(i)
	}
	if pinned {
		l.y[pin.Index] = relBoundFinite(pin.Pos.Y)
		l.x[pin.Index] = relBoundFinite(pin.Pos.X)
	}
	copy(l.prev, l.x)

	it, settled := 0, false
	for it = 0; it < maxIter; it++ {
		for i := range l.f {
			l.f[i] = 0
		}
		// 1. 斥力：全对互斥，系数 min(kRepel·(deg_i+1)·(deg_j+1), kMax)（R5 限幅）；
		//    只作用在 x —— y 由层序投影钉死（分层布局的常见退化，减少自由度、利于收敛）。
		//    软排斥仍按**中心距**（长程铺开）；卡片相叠时另加**接触推力**（D121）。
		for i := 0; i < n; i++ {
			for j := i + 1; j < n; j++ {
				dx := l.x[i] - l.x[j]
				dy := l.y[i] - l.y[j]
				d2 := dx*dx + dy*dy
				if d2 < 1e-4 { // 确定性微扰，避免重叠除零
					dx = 0.1 + float64(i-j)*0.01
					dy = 0.1
					d2 = dx*dx + dy*dy
				}
				if d2 > relCut*relCut { // 超出作用半径：不互斥
					continue
				}
				coef := relKElec * float64(l.deg[i]+1) * float64(l.deg[j]+1)
				if coef > relKRepelMax {
					coef = relKRepelMax
				}
				mag := coef / (d2 + relSoft)
				dir := math.Sqrt(d2)
				fx := mag * (dx / dir)
				// 卡片相叠（D121 斥力从卡片边缘起算）：按水平穿透深度线性推开。
				// 必须两侧都有实体尺寸才谈得上「穿透」——半宽/半高为 0（质点夹具）时
				// 间隙就是中心距，拿它判重叠会把「右上/左下」的对误判成相叠。
				hw := l.halfW[i] + l.halfW[j]
				hh := l.halfH[i] + l.halfH[j]
				if hw > 0 && hh > 0 {
					if pen := hw - math.Abs(dx); pen > 0 && math.Abs(dy)-hh < 0 {
						if pen > relPushMaxDp {
							pen = relPushMaxDp
						}
						fx += pen * relPushK * math.Copysign(1, dx)
					}
				}
				l.f[i] += fx
				l.f[j] -= fx
			}
		}
		// 2. 弹簧：子被拉向「父的 x + 分配角静息偏移」（长边 = D121）。软弹簧 = 力弱则斥力
		// 胜出，兄弟被推开、树铺得开；刚度大时所有子都贴到父的正下方，卡片必然压字。
		for i := range l.nodes {
			if p := l.nodes[i].Parent; p >= 0 {
				l.f[i] += relSpringK * (l.x[p] + l.restOff[i] - l.x[i])
			}
		}
		// 3. 位移（无惯性的梯度下降步；单步限幅防爆）。**只动 x**——y 由层序投影钉死。
		for i := range l.x {
			d := l.f[i] * relGain
			if d > relVMax {
				d = relVMax
			} else if d < -relVMax {
				d = -relVMax
			}
			l.x[i] += d
		}
		if !pinned || l.root != pin.Index {
			l.x[l.root] = 0
		}
		// 投影 A：**已废除**（D121）。原设计每轮把子角度硬夹进父扇区——那是硬约束，
		// 会把斥力原样顶回去（节点卡越宽，压得越死，拖开也推不动）。现改为：
		// x 方向纯受力（斥力 + 弹簧），扇区只作**初值**；方向性约束由层序投影（y）承担
		// ——子恒在父之下，即角度 < 180° 的硬上限（§5.1「180° 硬上限」）。
		if pinned {
			l.x[pin.Index] = relBoundFinite(pin.Pos.X)
		}
		// 5. 有界盒：无 NaN / 坐标爆炸（A4/R5 的可观测底线）。
		for i := range l.x {
			if math.IsNaN(l.x[i]) || math.Abs(l.x[i]) > relBound {
				l.x[i] = relBoundFinite(l.x[i])
			}
		}
		// 动能 = 本步真实位移（含投影位移）。
		kin := 0.0
		for i := range l.x {
			d := l.x[i] - l.prev[i]
			l.prev[i] = l.x[i]
			kin += d * d
		}
		if kin < relEps*float64(n) {
			settled = true
			it++
			break
		}
	}
	l.iters, l.settled = it, settled
	return it, settled
}

// solution 导出当前坐标快照。
func (l *relLayout) solution() *relSolution {
	pos := make([]relPoint, len(l.nodes))
	for i := range l.x {
		pos[i] = relPoint{X: l.x[i], Y: l.y[i]}
	}
	return &relSolution{Pos: pos, Iterations: l.iters, Settled: l.settled}
}

// relSolve 求解一棵树的布局（无固定锚）。ok=false 表示输入不合格（空/多根/环/父越界）。
// 空树返回空解（ok=true）。
func relSolve(nodes []relGeoNode) (*relSolution, bool) {
	sol, ok := relSolvePinned(nodes, relPin{})
	return sol, ok
}

// relSolvePinned 带固定锚**从结构初值**求解（冷启动用；零值 pin 时 == relSolve）。
// 拖拽途中的逐帧重排不走这里——走 relGraph 持有的 *relLayout 增量松弛。
func relSolvePinned(nodes []relGeoNode, pin relPin) (*relSolution, bool) {
	if len(nodes) == 0 {
		return &relSolution{}, true
	}
	l, ok := newRelLayout(nodes)
	if !ok {
		return nil, false
	}
	l.relax(relMaxIter, pin)
	return l.solution(), true
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
//
// **卡片下限（D121）**：节点卡要装标签，宽度可观；扇区若比卡片窄，投影 A（硬夹角）
// 会把斥力顶回去——卡片必然压字。故每份扇区至少宽到「相邻两张卡摆得开」的角距：
// share_i ≥ atan((半宽_i + 半宽_右邻) / 层距)。总量超出父扇区则**扩张**父扇区
// （上限 ±relMaxAngle：越界会因 tan 周期折叠破坏角度不变量）；再超出则按比例压缩
// ——认重叠，绝不越界。半宽全为 0（质点夹具）时本段恒不触发，扇区与规模分配逐位不变。
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
		// 份额：规模分配打底，卡片角距托底（D121）。
		shares := make([]float64, len(kids))
		need := 0.0
		for i, k := range kids {
			shares[i] = float64(size[k]) / total
			nb := nodes[k].HalfW
			if i+1 < len(kids) {
				nb += nodes[kids[i+1]].HalfW
			} else {
				nb += nodes[kids[i]].HalfW // 末位用左邻（同一公式，前后对称）
			}
			if c := math.Atan(nb / relRowGap); c > shares[i] {
				shares[i] = c
			}
			need += shares[i]
		}
		lo, hi := childSector[p].Lo, childSector[p].Hi
		span := hi - lo
		if need > span { // 卡片要得比父扇区宽：扩张（上限 ±relMaxAngle）
			lo, hi = -relMaxAngle, relMaxAngle
			span = hi - lo
		}
		if need > span { // 顶到上限仍不够：按比例压缩（认重叠，不越界）
			k := span / need
			for i := range shares {
				shares[i] *= k
			}
			need = span
		}
		cur := lo
		for i, k := range kids {
			share := shares[i]
			assigned[k] = relSector{Lo: cur, Hi: cur + share}
			cur += share
			mid := (assigned[k].Lo + assigned[k].Hi) / 2
			hw := relHw(size[k])
			if w := need; w > hw { // 子扇区至少装得下孩子已分配的份额
				hw = w / 2
			}
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
