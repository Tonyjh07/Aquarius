package uigui

// 会话树关系图——布局内核（D124 重构，取代 D120④/D121 的「一次性求解 + 静止」）。
//
// 本文件是 Gio / port 无关的**物理仿真核**：输入 = 极小几何投影（父索引 / 层号 / 权重 /
// 卡片半宽），输出 = 每节点坐标 + 是否静止。port.TreeGraph → 本输入的映射在 relmap.go。
//
// 模型（§2）：节点 = 质点（质量 1），**常驻物理**——
//
//	① 边弹簧：子被拉向「父当前位置 + 静息偏移 (restOff, relRowGap)」，父子等大反向
//	   施加（牛顿第三定律）→ 子树随父拖动、动量沿树传导（拖任一节点整树响应）；
//	② 静息锚：每节点被拉向确定性静息位 (restX, restY) → 平衡态 ≈ 静息布局、快速局部收敛；
//	   根另加根锚（拉向原点）防整树漂移；
//	③ 斥力：全对（排除祖先-后代对，DFS 序区间 O(1) 判定）2D，软化 + 作用半径截止 +
//	   卡片相叠接触推力；
//	④ 阻尼：速度每帧乘 relPhysDamp → 系统自然收敛；
//	⑤ 拖拽 pin：被拖节点硬钉在指针位（速度清零），其余节点只经弹簧/斥力被带动。
//
// 积分 = 半隐式欧拉（每帧 relPhysSub 子步：高自由度节点的显式刚度会越稳定边界，
// 子步把 ω·dt 压回稳区）：v += F·dt; v *= 阻尼; 限幅; pos += v·dt。收敛（动能 < 阈值）即
// 静止，帧层据此停帧（R3）；任何交互（拖拽 / rebuild）都注入能量 → 自动唤醒——树
// 「全程都能动」。
//
// 静息布局（rest layout，§3）是确定性参考：restPos(子) = restPos(父) + (restOff, relRowGap)。
// 它是物理的平衡态（无斥力时精确），也供视场适配（fit/recenter）取树盒——当前物理位形
// 会动，不适合当视场基准。
//
// 可测性：力导向坐标不确定 ⇒ 断言**不变量**：静息树形 / 层序（静息态）/ 有界盒 /
// 同输入可复现 / 动能降至静止。

import "math"

// 物理仿真档位（偶然实现，spike 调定；见 relmap_anim_bench_test.go）。
const (
	// relHardCap 呈现节点数上限（§5.4.3）：超过则 UI 侧优先收窄 maxDepth。
	relHardCap = 240

	// relPhysDt 每帧固定步长（帧率相关，见设计 §8）。60fps 基准。
	relPhysDt = 1.0
	// relPhysRepel 斥力系数基准（见下方「物理刚度/阻尼/播种档位」const 块）。
	// relPhysSoft 斥力软化（dp²）：近重合时力有限——折叠播种不会爆炸。
	relPhysSoft = 6.0e2
	// relPhysCut 斥力作用半径（dp）：超出即不互斥——砍长程慢漂移模态、加速收敛。
	relPhysCut = 420.0
	// relPhysPushK / relPhysPushMaxDp 卡片相叠的接触推力（沿重叠方向，兜底）：斥力主导
	// 分离，相叠时再补一把短程推力，防深压。
	relPhysPushK     = 0.4
	relPhysPushMaxDp = 40.0

	relRowGap    = 72.0 // 层序：每层竖直间距（dp）
	relJitter    = 12.0 // 确定性抖动幅度基准（播种用）
	relKRepelMax = 6.0e3
	// relRestLen 静息边长的下限（dp）：无卡片占位（质点夹具）时的兄弟间距下限。
	relRestLen = 90.0
	// relSlotGapDp 兄弟槽位之间的额外间隙（dp）：卡片并排时留条缝。
	relSlotGapDp = 10.0
	relBound     = 20000.0
	relSectorMin = 0.05 // 子扇区最小半宽（弧度）
	relSectorMax = 1.20 // 子扇区最大半宽（≈69°）
	relMaxAngle  = 1.35 // 任意扇区允许的最大 |角度|（< π/2）
	relRootHalf  = 1.25 // 根的子扇区半宽（≈72°）
	relHwBase    = 0.30 // 扇区半宽随子树规模增长系数
	// relCardPenetrationMax 允许的最深穿透（占卡宽比例，性质测试钉住）：密集分支点总量
	// 本就大于可铺开宽度，零重叠在 A5 取舍下不可达，故只钉「不深压」。
	relCardPenetrationMax = 0.25
)

// 物理刚度/阻尼/播种档位（偶然实现，spike 调定）：
//   - 边弹簧 0.25 + 静息锚 0.15 + 阻尼 0.90 → 播完 ~1–3s 落定（100–180 帧），拖后 ~1s；
//   - 子步 6：高自由度节点的显式刚度（父子等大反向）会越过稳定边界，子步把 ω·dt 压回稳区；
//   - 斥力 150：既保证交互推开，又使平衡态 ≈ 静息布局（偏差 ≤ ~30dp，fit 余量可覆盖）。
const (
	relPhysSpring = 0.25  // 边弹簧刚度：力 = k × 偏移
	relPhysRoot   = 0.25  // 根锚刚度：力 = k × (静息原点 − 根位置)
	relPhysHome   = 0.15  // 静息锚刚度：力 = k × (静息位 − 位置)（钉住平衡态 ≈ 静息布局）
	relPhysRepel  = 150.0 // 斥力系数基准：力 = coef/(d²+soft)，coef = 基准 × (deg+1)²，限幅 relKRepelMax
	relPhysDamp   = 0.90  // 每帧速度阻尼
	relPhysSub    = 6     // 每帧子步数（显式刚度稳定化）
	relPhysVMax   = 30.0  // 速度限幅（dp/帧）
	relPhysEps    = 0.02  // 静止阈值：Σv² < 阈值 × n
	relSeedShrink = 0.60  // 折叠播种：静息布局向根点收缩的比例
)

// relGeoNode 布局输入：几何相关的最小投影（与 port 解耦）。Parent = -1 = 结构根；
// Level 为到结构根跳数；Weight 参与斥力限幅与绘制。HalfW/HalfH = 卡片半宽/半高（dp；
// 0 = 质点）。斥力从卡片边缘起算（D121）。这些值不随 zoom 变（否则缩放会改布局，违反 R3）。
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

// relPin 临时固定锚（D124，§5）：把某节点硬钉在世界坐标 (X, Y)——被钉者不参与积分，
// 但继续对邻居施斥力、被边弹簧拉扯。零值 = 无固定（Valid 显式声明，避免把结构根
// 悄悄钉死）。这是 R3 的交互态例外：拖拽中每帧带锚，松手锚消失、物理自然收敛。
type relPin struct {
	Valid bool
	Index int
	Pos   relPoint
}

// relLayout 常驻物理仿真态：派生量（孩子表 / 度数 / BFS 序 / 静息布局）只算一次，
// 之后每帧从**当前坐标 + 速度**接着积分（拖拽/展开都不跳回初值）。
type relLayout struct {
	nodes     []relGeoNode
	children  [][]int
	deg       []int
	root      int
	order     []int // BFS 序（父先于子）
	halfW     []float64
	halfH     []float64
	restOff   []float64 // 每节点的横向静息偏移（相对父；根 = 0）
	restX     []float64 // 静息布局坐标（确定性树形）
	restY     []float64
	tin, tout []int     // DFS 序区间（判祖先-后代：j 在 i 子树内 ⇔ tin_i ≤ tin_j ≤ tout_i）
	x, y      []float64 // 当前位置
	vx, vy    []float64 // 速度（物理积分）
	fx, fy    []float64 // 力缓存
	iters     int
	settled   bool
}

// newRelLayout 结构校验 + 静息布局计算。初值 = 静息布局（settled）；需要物理展开 /
// 增量保留由帧层调用 seedCollapsed / setPositions。ok=false = 输入不合格。
func newRelLayout(nodes []relGeoNode) (*relLayout, bool) {
	n := len(nodes)
	if n == 0 {
		return &relLayout{settled: true}, true
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
		order:    order,
		halfW:    make([]float64, n),
		halfH:    make([]float64, n),
		restOff:  make([]float64, n),
		restX:    make([]float64, n),
		restY:    make([]float64, n),
		x:        make([]float64, n),
		y:        make([]float64, n),
		vx:       make([]float64, n),
		vy:       make([]float64, n),
		fx:       make([]float64, n),
		fy:       make([]float64, n),
	}
	for i, nd := range nodes {
		if nd.Parent >= 0 {
			l.children[nd.Parent] = append(l.children[nd.Parent], i)
			l.deg[nd.Parent]++
		}
		l.halfW[i], l.halfH[i] = nd.HalfW, nd.HalfH
	}
	l.initRest(assigned, order)
	l.initTour()
	copy(l.x, l.restX)
	copy(l.y, l.restY)
	l.settled = true
	return l, true
}

// initTour 计算 DFS 序区间（tin/tout），用于 O(1) 判祖先-后代（斥力排除亲属，
// 使链/主干不被纵向斥力拉长，同族间距只由弹簧定义）。
func (l *relLayout) initTour() {
	n := len(l.nodes)
	l.tin, l.tout = make([]int, n), make([]int, n)
	timer := 0
	var dfs func(i int)
	dfs = func(i int) {
		l.tin[i] = timer
		timer++
		for _, c := range l.children[i] {
			dfs(c)
		}
		l.tout[i] = timer - 1
	}
	dfs(l.root)
}

// initRest 计算静息布局：restOff（兄弟槽位）+ restX/restY（递归：
// 子 = 父 + (restOff, relRowGap)，根 = 原点）。BFS 序保证父先于子。
func (l *relLayout) initRest(assigned []relSector, order []int) {
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
		}
	}
	l.restX[l.root], l.restY[l.root] = 0, 0
	for _, i := range order {
		if i == l.root {
			continue
		}
		p := l.nodes[i].Parent
		l.restX[i] = l.restX[p] + l.restOff[i]
		l.restY[i] = l.restY[p] + relRowGap
	}
}

// restPos 静息布局坐标快照（与输入同序）。
func (l *relLayout) restPos() []relPoint {
	pos := make([]relPoint, len(l.restX))
	for i := range pos {
		pos[i] = relPoint{X: l.restX[i], Y: l.restY[i]}
	}
	return pos
}

// seedCollapsed 折叠播种（开窗 / 全新图）：初值 = 静息布局向根点收缩 relSeedShrink
// + 确定性小抖动，速度清零。物理据此从「缩成一小团」展开成树（展开本身即物理）。
func (l *relLayout) seedCollapsed() {
	n := len(l.nodes)
	if n == 0 {
		l.settled = true
		return
	}
	ox, oy := l.restX[l.root], l.restY[l.root]
	for i := range l.nodes {
		jx := (relNoise(i, 1)*2 - 1) * relJitter * 0.25
		jy := (relNoise(i, 2)*2 - 1) * relJitter * 0.25
		l.x[i] = ox + (l.restX[i]-ox)*relSeedShrink + jx
		l.y[i] = oy + (l.restY[i]-oy)*relSeedShrink + jy
		l.vx[i], l.vy[i] = 0, 0
	}
	l.x[l.root], l.y[l.root] = ox, oy
	l.iters, l.settled = 0, false
}

// setPositions 用给定坐标重设当前位置（增量保留：数据变更时按节点 ID 复用旧位置），
// 速度清零、进入运动态。pos 可短于节点数（缺者为静息位）。
func (l *relLayout) setPositions(pos []relPoint) {
	for i := range l.x {
		if i < len(pos) {
			l.x[i], l.y[i] = pos[i].X, pos[i].Y
		} else {
			l.x[i], l.y[i] = l.restX[i], l.restY[i]
		}
		l.vx[i], l.vy[i] = 0, 0
	}
	l.iters, l.settled = 0, false
}

// step 一个物理步进（半隐式欧拉，内部按 relPhysSub 子步——高自由度节点的显式刚度会越过
// 稳定边界，子步把 ω·dt 压回稳区）。pin.Valid 时该节点被硬钉（速度清零），其余节点只经
// 弹簧/斥力被带动——拖一个节点，整棵树按物理规律动起来。
func (l *relLayout) step(pin relPin) {
	n := len(l.nodes)
	if n == 0 {
		l.settled = true
		return
	}
	if pin.Valid && (pin.Index < 0 || pin.Index >= n) {
		pin.Valid = false // 越界锚视为无固定（防御：帧层本地索引与布局可能不同步）
	}
	sub := relPhysSub
	if sub < 1 {
		sub = 1
	}
	dt := relPhysDt / float64(sub)
	damp := math.Pow(relPhysDamp, 1/float64(sub))
	for s := 0; s < sub; s++ {
		l.accumulate(dt, damp, pin)
	}

	// 有界盒 + 动能（被钉者不计）。
	kin := 0.0
	for i := range l.x {
		if math.IsNaN(l.x[i]) || math.Abs(l.x[i]) > relBound {
			l.x[i], l.vx[i] = relBoundFinite(l.x[i]), 0
		}
		if math.IsNaN(l.y[i]) || math.Abs(l.y[i]) > relBound {
			l.y[i], l.vy[i] = relBoundFinite(l.y[i]), 0
		}
		if pin.Valid && i == pin.Index {
			continue
		}
		kin += l.vx[i]*l.vx[i] + l.vy[i]*l.vy[i]
	}
	l.iters++
	l.settled = kin < relPhysEps*float64(n)
}

// accumulate 一个子步：累计力 → 半隐式欧拉积分（步长 dt、阻尼 damp）→ pin 硬钉。
func (l *relLayout) accumulate(dt, damp float64, pin relPin) {
	n := len(l.nodes)
	for i := range l.fx {
		l.fx[i], l.fy[i] = 0, 0
	}

	// ① 边弹簧（子被拉向「父当前位置 + 静息偏移」→ 子树随父拖动；父子等大反向施加，
	//    动量沿树传导 = 整树响应）+ 根锚（根被拉向静息原点，防整树漂移）+ 可选静息锚
	//    （每节点拉向静息位；spike 定其有无/强弱）。
	for i := range l.nodes {
		if relPhysHome > 0 {
			l.fx[i] += relPhysHome * (l.restX[i] - l.x[i])
			l.fy[i] += relPhysHome * (l.restY[i] - l.y[i])
		}
		if p := l.nodes[i].Parent; p >= 0 {
			fx := relPhysSpring * (l.x[p] + l.restOff[i] - l.x[i])
			fy := relPhysSpring * (l.y[p] + relRowGap - l.y[i])
			l.fx[i] += fx
			l.fy[i] += fy
			l.fx[p] -= fx // 反作用：父同样被拉（牛顿第三定律 → 动量沿树传导）
			l.fy[p] -= fy
		} else {
			l.fx[i] += relPhysRoot * (l.restX[i] - l.x[i])
			l.fy[i] += relPhysRoot * (l.restY[i] - l.y[i])
		}
	}

	// ② 斥力：全对（排除祖先-后代），2D，软化 + 截止 + 卡片相叠接触推力。
	for i := 0; i < n; i++ {
		for j := i + 1; j < n; j++ {
			if relInSubtree(l.tin, l.tout, i, j) || relInSubtree(l.tin, l.tout, j, i) {
				continue // 祖先-后代（含父子）不互斥：亲属间距由弹簧定义，否则链被拉长
			}
			dx := l.x[i] - l.x[j]
			dy := l.y[i] - l.y[j]
			d2 := dx*dx + dy*dy
			if d2 < 1e-6 { // 确定性微扰，避免重叠除零
				dx = 0.1 + float64(i-j)*0.01
				dy = 0.1
				d2 = dx*dx + dy*dy
			}
			if d2 > relPhysCut*relPhysCut { // 超出作用半径：不互斥
				continue
			}
			d := math.Sqrt(d2)
			ux, uy := dx/d, dy/d
			coef := relPhysRepel * float64(l.deg[i]+1) * float64(l.deg[j]+1)
			if coef > relKRepelMax {
				coef = relKRepelMax
			}
			mag := coef / (d2 + relPhysSoft)
			fx, fy := mag*ux, mag*uy
			// 卡片相叠（D121）：按最小穿透深度沿重叠方向补一把短程推力。
			hw := l.halfW[i] + l.halfW[j]
			hh := l.halfH[i] + l.halfH[j]
			if hw > 0 && hh > 0 {
				if pen := math.Min(hw-math.Abs(dx), hh-math.Abs(dy)); pen > 0 {
					if pen > relPhysPushMaxDp {
						pen = relPhysPushMaxDp
					}
					fx += pen * relPhysPushK * ux
					fy += pen * relPhysPushK * uy
				}
			}
			l.fx[i] += fx
			l.fy[i] += fy
			l.fx[j] -= fx
			l.fy[j] -= fy
		}
	}

	// ③ 积分：v += F·dt; v *= 阻尼; 限幅; pos += v·dt。
	for i := range l.x {
		vx := (l.vx[i] + l.fx[i]*dt) * damp
		vy := (l.vy[i] + l.fy[i]*dt) * damp
		if sp2 := vx*vx + vy*vy; sp2 > relPhysVMax*relPhysVMax {
			s := relPhysVMax / math.Sqrt(sp2)
			vx *= s
			vy *= s
		}
		l.vx[i], l.vy[i] = vx, vy
		l.x[i] += vx * dt
		l.y[i] += vy * dt
	}

	// ④ pin：硬钉 + 清速（被拖者跟手，其余靠物理传导）。
	if pin.Valid {
		l.x[pin.Index] = relBoundFinite(pin.Pos.X)
		l.y[pin.Index] = relBoundFinite(pin.Pos.Y)
		l.vx[pin.Index], l.vy[pin.Index] = 0, 0
	}
}

// solution 导出当前坐标快照。
func (l *relLayout) solution() *relSolution {
	pos := make([]relPoint, len(l.x))
	for i := range l.x {
		pos[i] = relPoint{X: l.x[i], Y: l.y[i]}
	}
	return &relSolution{Pos: pos, Iterations: l.iters, Settled: l.settled}
}

// relSolve 求解一棵树的**静息布局**（确定性、无物理过程；点合并/夹具/视场基准用）。
// ok=false 表示输入不合格（空/多根/环/父越界）。空树返回空解（ok=true）。
func relSolve(nodes []relGeoNode) (*relSolution, bool) {
	if len(nodes) == 0 {
		return &relSolution{}, true
	}
	l, ok := newRelLayout(nodes)
	if !ok {
		return nil, false
	}
	return l.solution(), true
}

// relBFS 自根宽度优先，返回索引序（根首）；ok=false = 结构不合法。
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
// 卡片下限（D121）：每份扇区至少宽到「相邻两张卡摆得开」的角距；超出父扇区则扩张
// （上限 ±relMaxAngle），再超出按比例压缩（认重叠，不越界）。半宽全 0 时本段不触发。
//
// 本函数在 D124 后**只用于静息布局的兄弟排序/间距**（角度硬投影已废除）。
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

// relHw 子扇区半宽：随子树规模增长、钳在 [min,max]。
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

// relClampSector 把扇区夹进 [-relMaxAngle, relMaxAngle]。
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

// relInSubtree 节点 desc 是否在 anc 的子树内（含自身）——DFS 序区间 O(1) 判定。
func relInSubtree(tin, tout []int, anc, desc int) bool {
	return tin[anc] <= tin[desc] && tin[desc] <= tout[anc]
}

// relNoise 确定性伪随机 ∈ [0,1)（同 i/salt 恒同值 → 播种可复现）。
func relNoise(i int, salt float64) float64 {
	f := math.Sin(float64(i)*12.9898+salt*78.233) * 43758.5453
	return f - math.Floor(f)
}
