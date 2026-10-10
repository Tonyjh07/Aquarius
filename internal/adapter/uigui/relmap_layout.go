package uigui

// 会话树关系图——布局内核（D124 重构，D126 改订；取代 D120④/D121 的「一次性求解 + 静止」）。
//
// 本文件是 Gio / port 无关的**物理仿真核**：输入 = 极小几何投影（父索引 / 层号 / 权重 /
// 卡片半宽），输出 = 每节点坐标 + 是否静止。port.TreeGraph → 本输入的映射在 relmap.go。
//
// 模型（§2）：节点 = 质点（质量 1），**常驻物理**——
//
//	① 边弹簧：子被拉向「父当前位置 + 静息偏移 (restOff, relRowGap)」，父子等大反向
//	   施加（牛顿第三定律）→ 子树随父拖动、动量沿树传导（拖任一节点整树响应）；
//	② 斥力：全对（排除祖先-后代对，DFS 序区间 O(1) 判定）2D，软化 + 作用半径截止 +
//	   卡片相叠接触推力；
//	③ 阻尼：速度每帧乘 relPhysDamp → 系统自然收敛；
//	④ 拖拽 pin：被拖节点硬钉在指针位（速度清零），其余节点只经弹簧/斥力被带动；
//	⑤ 树锚：整树的包围盒中心被拉向锚点（静息盒心）——**外力均匀施加在每个节点上，
//	   只平移、不产生内部应力**，于是单个节点完全自由（可被拖到任何位置并留在那里），
//	   整树却不漂移、始终停在原处（D126「不锚定节点，锚定树」）。
//
// 积分 = 半隐式欧拉（每帧 relPhysSub 子步：高自由度节点的显式刚度会越稳定边界，
// 子步把 ω·dt 压回稳区）：v += F·dt; v *= 阻尼; 限幅; pos += v·dt。收敛（动能 < 阈值）即
// 静止，帧层据此停帧（R3）；任何交互（拖拽 / rebuild）都注入能量 → 自动唤醒——树
// 「全程都能动」。
//
// 静息布局（rest layout，§3）是确定性参考：restPos(子) = restPos(父) + (restOff, relRowGap)。
// 节点锚废除后（D126）它不再是逐点平衡态，只提供**播种初值 + 树锚点 + 视场基准**
// （fit/recenter 取树盒——当前物理位形会动，不适合当视场基准）。
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
	// relPhysPushK / relPhysPushMaxDp 卡片接触推力（D126 重写）：**轴隙**（水平/竖直
	// 分别扣掉两卡半宽/半高）小于 relSlotGapDp 时，沿最小穿透轴推，力 = 间隙差 × K，
	// 单面上限 relPhysPushMaxDp × K。静息布局的兄弟槽隙恰等于 relSlotGapDp ⇒ 静息态
	// 受力为零 = **真正的平衡态**——不必靠逐节点静息锚维持（D126 废除节点锚的原因：
	// 长程斥力下平衡态比静息布局宽 30%~56%，按静息盒适配的视场装不下整树）。
	relPhysPushK     = 0.4
	relPhysPushMaxDp = 40.0

	relRowGap = 72.0 // 层序：每层竖直间距（dp）
	relJitter = 12.0 // 确定性抖动幅度基准（播种用）
	// relRestLen 静息边长的下限（dp）：无卡片占位（质点夹具）时的兄弟间距下限。
	relRestLen     = 90.0
	relRowAdoptMin = 0.75 // 手动摆放层距钳制（× relRowGap）：保「子恒在父之下」+ 父子卡片不叠
	relRowAdoptMax = 2.0  // 上限：防把子树推到两层外去挤别人的行
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

// 物理刚度/阻尼/播种档位（偶然实现，spike 调定；D126 起废除逐节点静息锚/根锚）：
//   - 边弹簧拆双轴：横向 1.0、纵向 2.0（刚 ⇒ 树贴身保持静息形状、平衡 ≈ 静息布局）。
//     0.012 软弹簧实测失败（势能面太平卡局部极小 + 接触力顶离槽位 100+dp，见
//     relPhysSpringX 注释）；用户 2026-10-09 先要「约 10s 收敛看看效果」、实测后
//     反馈「提高阻尼，现在太晃了」——软弹簧 + 轻阻尼的组合已被否掉；
//   - 阻尼按轴 0.5/0.5（配 relPhysFMax 力限幅挡 VMax 钳位极限环）：~0.5s 内一两下
//     轻摆收敛（fixture 实测 ~130 帧/2.2s）；
//   - 子步 12：最高频模态是纵向呼吸（ω=√(2×(deg+1))，deg≤240 ⇒ ω≤22，ω·dt≈1.8 < 2），
//     12 子步留足余量，成本 ~0.25ms/帧可忽略；
//   - 分离力只来自卡片接触（relPhysPushK）：静息布局即平衡态，视场按它适配才装得下整树。
const (
	relPhysSpringX = 1.0 // 横向弹簧刚度（刚 ⇒ 树贴身保持静息形状）。0.012 软弹簧实测失败：
	//                      (1) 势能面太平，拖开松手后树卡在局部极小（rand40 根节点离静息
	//                      103dp 却判静止）；(2) 接触推力能把节点顶离静息槽 100+dp，视场基准
	//                      （静息盒）与平衡态脱节。刚弹簧 + relPhysFMax 限幅 + 阻尼后：
	//                      平衡 ≈ 静息布局（D126「静息布局即平衡态」的真契约）。
	relPhysSpringY = 2.0 // 纵向弹簧刚度（刚 ⇒ 层序结构快速归位）。深链的「手风琴模式」
	//                      （整链均匀伸缩 = 零净力模态）松弛率 λ = ky×(1−dampY)/n ∝ ky：
	//                      ky=0.5 时 chain150 末段慢爬 2500+ 帧，ky=2.0 快 4 倍（~600 帧，
	//                      落在 relSettleFrames 内）。过冲由 relPhysFMax 限幅兜住，不炸。
	relPhysTree  = 0.03 // 树锚刚度：整树盒心被拉向锚点（均匀外力 ⇒ 只平移、不变形）
	relPhysDampX = 0.5  // 横向每帧速度阻尼（ζ≈0.25，一两下轻摆收敛 ~0.5s）。0.982 档实测
	//                   「太晃」（ζ≈0.08，来回摆十几下才停，用户 2026-10-09 反馈「提高
	//                   阻尼，现在太晃了」）。
	relPhysDampY = 0.5 // 纵向每帧速度阻尼：纵向模态频率高（ω=√2≈1.4），ζ≈0.18，轻回弹
	//                   收敛快；太重会「慢爬」（弱弹簧力被阻尼吃掉，chain150 末段 2000+ 帧）。
	//                   配 relPhysFMax 力限幅（挡 VMax 钳位极限环）后恰到好处。x/y 弹簧与
	//                   树锚本来就按轴解耦，拆轴干净。
	// relPhysSub 每帧子步数（显式刚度稳定化）：**必须压住最高频模态**——星形枢纽的
	// 「呼吸」模态 ω = √(k×(deg+1))，半隐式欧拉稳定边界 ω·dt < 2。本档最高频是纵向：
	// ω = √(0.5×240) ≈ 11，dt = 1/12 ⇒ ω·dt ≈ 0.9，稳。不足会撞出「速度被限幅钳住、
	// 阻尼失效」的极限环：位置看着不动、settled 永远为假、节点在帧内高频抖（k=6 时代
	// 实测 star30 钳在 maxv=30 抖 3000 帧不收敛）。
	relPhysSub  = 12
	relPhysVMax = 30.0 // 速度限幅（dp/帧）
	relPhysFMax = 12.0 // 弹簧/树锚力限幅（dp/帧²，每轴）。防「VMax 限幅打败阻尼」的
	//                     极限环：巨幅振荡时弹簧加速度每子步把速度推回 30 钳位，阻尼永远
	//                     追不上（chain150 实测 2140dp 超调 3000 帧不收敛）。力 ≤ 12 ⇒
	//                     v_steady = F/(1−damp) = 24 < 30，阻尼能真正生效。平衡态不受
	//                     影响（近平衡时力远小于限幅：kx=1 时 12dp 内不触发）。
	relPhysEps    = 0.002 // 静止阈值：Σv² < 阈值 × n（软物理下收紧：减少「慢爬」残差）
	relSeedShrink = 0.60  // 折叠播种：静息布局向盒心收缩的比例（盒心 = 树锚，见 seedCollapsed）
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

// relLayout 常驻物理仿真态：派生量（孩子表 / BFS 序 / 静息布局）只算一次，
// 之后每帧从**当前坐标 + 速度**接着积分（拖拽/展开都不跳回初值）。
type relLayout struct {
	nodes     []relGeoNode
	children  [][]int
	root      int
	order     []int // BFS 序（父先于子）
	halfW     []float64
	halfH     []float64
	restOff   []float64 // 每节点的横向静息偏移（相对父；根 = 0）
	restGap   []float64 // 每节点的纵向静息间距（相对父；默认 relRowGap，手动摆放后可变）
	restX     []float64 // 静息布局坐标（确定性树形）
	restY     []float64
	tin, tout []int     // DFS 序区间（判祖先-后代：j 在 i 子树内 ⇔ tin_i ≤ tin_j ≤ tout_i）
	x, y      []float64 // 当前位置
	vx, vy    []float64 // 速度（物理积分）
	fx, fy    []float64 // 力缓存
	// anchorX/anchorY 树锚点（世界 dp） = 静息布局的包围盒中心：整树被拉向这里。
	// D126：节点锚废除后，单个节点只受弹簧/接触推力（完全自由），整树的位置由它兜住。
	anchorX, anchorY float64
	iters            int
	settled          bool
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
		root:     order[0],
		order:    order,
		halfW:    make([]float64, n),
		halfH:    make([]float64, n),
		restOff:  make([]float64, n),
		restGap:  make([]float64, n),
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
		}
		l.halfW[i], l.halfH[i] = nd.HalfW, nd.HalfH
	}
	l.initRest(assigned, order)
	l.initTour()
	l.anchorX, l.anchorY = l.restBoxCenter()
	copy(l.x, l.restX)
	copy(l.y, l.restY)
	l.settled = true
	return l, true
}

// initTour 计算 DFS 序区间（tin/tout），用于 O(1) 判祖先-后代（接触推力排除亲属，
// 使链/主干不被横推，同族间距只由弹簧定义）。
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
		span := float64(len(kids)-1) * pitch
		offs := make([]float64, len(kids))
		lo := assigned[kids[0]].Lo
		hi := assigned[kids[len(kids)-1]].Hi
		for i, k := range kids {
			// 兄弟站位：先按扇区角度中点的比例取（子树规模加权 ⇒ 大树分得宽扇区），再按
			// 顺序右推保最小槽距 pitch（D126 实测修：等规模兄弟原来被压到 0.35×pitch，
			// 卡片在静息布局里就相叠，接触推力只能把平衡态推离静息布局 ~20dp，视场基准
			// 与物理平衡态于是不是同一个形状）。
			mid := (assigned[k].Lo + assigned[k].Hi) / 2
			t := 0.5
			if hi > lo {
				t = (mid - lo) / (hi - lo)
			}
			offs[i] = (t - 0.5) * span
		}
		for i := 1; i < len(offs); i++ { // dodge：每人至少离前人一个 pitch（保序）
			if offs[i] < offs[i-1]+pitch {
				offs[i] = offs[i-1] + pitch
			}
		}
		if n := len(offs); n > 1 { // 回中：整体平移，不改相对间距（保形）
			c := (offs[0] + offs[n-1]) / 2
			for i := range offs {
				offs[i] -= c
			}
		}
		for i, k := range kids {
			l.restOff[k] = offs[i]
			l.restGap[k] = relRowGap
		}
	}
	l.restX[l.root], l.restY[l.root] = 0, 0
	for _, i := range order {
		if i == l.root {
			continue
		}
		p := l.nodes[i].Parent
		l.restX[i] = l.restX[p] + l.restOff[i]
		l.restY[i] = l.restY[p] + l.restGap[i]
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

// boxCenterOf 给定位形的包围盒中心（世界 dp；含卡片半宽/半高）。整棵树的平铺位置
// 一律用**盒心**度量——它就是 fitView/recenterTree 对准视口中心的那个点（两份口径
// 必须一致，否则「树锚」与「视场」说的不是同一个地方）。
func (l *relLayout) boxCenterOf(x, y []float64) (float64, float64) {
	minX, minY := math.Inf(1), math.Inf(1)
	maxX, maxY := math.Inf(-1), math.Inf(-1)
	for i := range x {
		minX, maxX = math.Min(minX, x[i]-l.halfW[i]), math.Max(maxX, x[i]+l.halfW[i])
		minY, maxY = math.Min(minY, y[i]-l.halfH[i]), math.Max(maxY, y[i]+l.halfH[i])
	}
	return (minX + maxX) / 2, (minY + maxY) / 2
}

// restBoxCenter 静息布局的包围盒中心 = 树锚点。
func (l *relLayout) restBoxCenter() (float64, float64) {
	return l.boxCenterOf(l.restX, l.restY)
}

// seedCollapsed 折叠播种（开窗 / 全新图）：初值 = 静息布局向**盒心**收缩 relSeedShrink
// + 确定性小抖动，速度清零。物理据此从「缩成一小团」展开成树（展开本身即物理）。
// **向盒心而不是向根点收缩**（D126 实测修）：向根点收缩时链/深树的盒心起点离树锚
// （= 静息盒心）几千 dp，展开被树锚猛拽 → 整体平移过冲 + VMax 限幅极限环（chain150
// 纵向超调 4268dp、3000 帧不收敛）。向盒心收缩 ⇒ 播种盒心 = 锚点，展开是纯扩张。
func (l *relLayout) seedCollapsed() {
	n := len(l.nodes)
	if n == 0 {
		l.settled = true
		return
	}
	cx, cy := l.restBoxCenter()
	for i := range l.nodes {
		jx := (relNoise(i, 1)*2 - 1) * relJitter * 0.25
		jy := (relNoise(i, 2)*2 - 1) * relJitter * 0.25
		l.x[i] = cx + (l.restX[i]-cx)*relSeedShrink + jx
		l.y[i] = cy + (l.restY[i]-cy)*relSeedShrink + jy
		l.vx[i], l.vy[i] = 0, 0
	}
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
	dampX := math.Pow(relPhysDampX, 1/float64(sub))
	dampY := math.Pow(relPhysDampY, 1/float64(sub))
	for s := 0; s < sub; s++ {
		l.accumulate(dt, dampX, dampY, pin)
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
	if l.settled && !pin.Valid {
		l.snapToAnchor()
	}
}

// snapToAnchor 静止时把整树平移到树锚点（软锚的收敛残差 ≤ relPhysEps 级，视觉无感；
// 但「树停在哪」是硬契约——D126：静止态整树盒心精确等于锚点，不偏几个 dp）。纯平移
// 不改任何相对位置 ⇒ 弹簧/接触力的内部状态不受影响，下一帧仍静止。**钉住期间不平**
// （pin 每子步会把被拖节点拉回指针位，平移它只会制造亚像素闪跳）。
func (l *relLayout) snapToAnchor() {
	cx, cy := l.boxCenterOf(l.x, l.y)
	dx, dy := l.anchorX-cx, l.anchorY-cy
	if dx == 0 && dy == 0 {
		return
	}
	for i := range l.x {
		l.x[i] += dx
		l.y[i] += dy
	}
}

// refreshRestFrom 从节点 i 起重算其子树的静息坐标（父的静息位不变），并重算树锚点。
func (l *relLayout) refreshRestFrom(i int) {
	if i < 0 || i >= len(l.nodes) {
		return
	}
	stack := []int{i}
	for len(stack) > 0 {
		k := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if p := l.nodes[k].Parent; p >= 0 {
			l.restX[k] = l.restX[p] + l.restOff[k]
			l.restY[k] = l.restY[p] + l.restGap[k]
		}
		stack = append(stack, l.children[k]...)
	}
	l.anchorX, l.anchorY = l.restBoxCenter()
}

// adoptSlot 把节点 i 的**当前位置**固化成静息槽位（D126「不锚定节点 ⇒ 自由移动」）：
// 弹簧的零内力平衡态本就是静息布局，光去掉节点锚只会让松手后弹回旧形状（用户实测
// 「拖完还是回到一个固定的形状」）——所以松手时把新位置**写回**静息槽位，新形状成为
// 平衡态。横向自由；纵向钳在本层带内（relRowAdoptMin/Max × relRowGap：保「子恒在父
// 之下」且父子卡片不叠）。结构根没有父：整棵静息布局随它平移（= 拖着整树走）。
func (l *relLayout) adoptSlot(i int) {
	n := len(l.nodes)
	if i < 0 || i >= n {
		return
	}
	if p := l.nodes[i].Parent; p < 0 {
		dx, dy := l.x[i]-l.restX[i], l.y[i]-l.restY[i]
		if dx == 0 && dy == 0 {
			return
		}
		for k := range l.nodes {
			l.restX[k] += dx
			l.restY[k] += dy
		}
	} else {
		l.restOff[i] = l.x[i] - l.x[p]
		gap := l.y[i] - l.y[p]
		if lo := relRowGap * relRowAdoptMin; gap < lo {
			gap = lo
		}
		if hi := relRowGap * relRowAdoptMax; gap > hi {
			gap = hi
		}
		l.restGap[i] = gap
		l.refreshRestFrom(i)
		return
	}
	l.anchorX, l.anchorY = l.restBoxCenter()
}

// setSlot 帧层按消息 ID 恢复的手动槽位（rebuild 后用户的排列不被自动布局冲掉）。
// root = 该节点是结构根：OffX/OffY 是整树平移量。
func (l *relLayout) setSlot(i int, s relSlot) {
	n := len(l.nodes)
	if i < 0 || i >= n {
		return
	}
	if s.Root || l.nodes[i].Parent < 0 {
		if s.OffX != 0 || s.OffY != 0 {
			for k := range l.nodes {
				l.restX[k] += s.OffX
				l.restY[k] += s.OffY
			}
			l.anchorX, l.anchorY = l.restBoxCenter()
		}
		return
	}
	l.restOff[i] = s.OffX
	gap := s.Gap
	if lo := relRowGap * relRowAdoptMin; gap < lo {
		gap = lo
	}
	if hi := relRowGap * relRowAdoptMax; gap > hi {
		gap = hi
	}
	l.restGap[i] = gap
	l.refreshRestFrom(i)
}

// accumulate 一个子步：累计力 → 半隐式欧拉积分（步长 dt、阻尼按轴 dampX/dampY）→ pin 硬钉。
func (l *relLayout) accumulate(dt, dampX, dampY float64, pin relPin) {
	n := len(l.nodes)
	for i := range l.fx {
		l.fx[i], l.fy[i] = 0, 0
	}

	// ① 边弹簧（子被拉向「父当前位置 + 静息偏移」→ 子树随父拖动；父子等大反向施加，
	//    动量沿树传导 = 整树响应）。**没有逐节点静息锚**（D126：节点自由移动）——
	//    整树位置由 ⑤ 树锚兜住。
	for i := range l.nodes {
		if p := l.nodes[i].Parent; p >= 0 {
			fx := relPhysSpringX * (l.x[p] + l.restOff[i] - l.x[i])
			fy := relPhysSpringY * (l.y[p] + l.restGap[i] - l.y[i])
			// 力限幅（D126）：防「VMax 限幅打败阻尼」极限环——巨幅振荡时若弹簧加速度
			// 能把速度推回 30 钳位，阻尼永远追不上。限幅后阻尼真正生效（见 relPhysFMax）。
			if fx > relPhysFMax {
				fx = relPhysFMax
			} else if fx < -relPhysFMax {
				fx = -relPhysFMax
			}
			if fy > relPhysFMax {
				fy = relPhysFMax
			} else if fy < -relPhysFMax {
				fy = -relPhysFMax
			}
			l.fx[i] += fx
			l.fy[i] += fy
			l.fx[p] -= fx // 反作用：父同样被拉（牛顿第三定律 → 动量沿树传导）
			l.fy[p] -= fy
		}
	}

	// ② 卡片接触分离：全对（排除祖先-后代）。**按卡片轴隙**算——水平/竖直分别扣掉
	//    两卡半宽/半高得轴隙；两轴都小于目标槽隙 relSlotGapDp 才算接触，沿**最小
	//    穿透轴**推（不是中心连线方向：斜推会把卡片推歪、增出力臂抖动）。等大反向
	//    ⇒ 动量守恒；限幅防深压（密集分支点零重叠不可达，只钉「不深压」）。
	for i := 0; i < n; i++ {
		for j := i + 1; j < n; j++ {
			if relInSubtree(l.tin, l.tout, i, j) || relInSubtree(l.tin, l.tout, j, i) {
				continue // 祖先-后代（含父子）不互斥：亲属间距由弹簧定义，否则链被拉长
			}
			dx := l.x[i] - l.x[j]
			dy := l.y[i] - l.y[j]
			// 轴隙 = 目标槽隙 − 两卡在该轴上的「超过量」；≤0 = 该轴已经分得够开。
			px := relSlotGapDp + l.halfW[i] + l.halfW[j] - math.Abs(dx)
			py := relSlotGapDp + l.halfH[i] + l.halfH[j] - math.Abs(dy)
			if px <= 0 || py <= 0 {
				continue // 至少一轴分开了：不接触
			}
			pen := math.Min(px, py)
			if pen > relPhysPushMaxDp {
				pen = relPhysPushMaxDp // 力限幅：折叠播种时全卡叠在根上，不限幅会炸
			}
			f := pen * relPhysPushK
			if px < py { // 沿最小穿透轴分离（同轴叠合时给确定性微偏，保证反向）
				if dx == 0 {
					dx = 0.1 + float64(i-j)*0.01
				}
				l.fx[i] += math.Copysign(f, dx)
				l.fx[j] -= math.Copysign(f, dx)
			} else {
				if dy == 0 {
					dy = 0.1 + float64(i-j)*0.01
				}
				l.fy[i] += math.Copysign(f, dy)
				l.fy[j] -= math.Copysign(f, dy)
			}
		}
	}

	// ③ 树锚（D126「不锚定节点，锚定树」）：把**整棵树的盒心**拉向锚点。外力在每个
	//    节点上是同一个向量（均匀）⇒ 只产生平移、不引入任何内部应力——单个节点因此
	//    完全自由（可以被拖到任何位置并留在那里），整树却不漂移、始终停在原处
	//    （锚点 = 静息盒心 = 视场适配对准的那个点）。
	cx, cy := l.boxCenterOf(l.x, l.y)
	ax := relPhysTree * (l.anchorX - cx)
	ay := relPhysTree * (l.anchorY - cy)
	if ax > relPhysFMax {
		ax = relPhysFMax
	} else if ax < -relPhysFMax {
		ax = -relPhysFMax
	}
	if ay > relPhysFMax {
		ay = relPhysFMax
	} else if ay < -relPhysFMax {
		ay = -relPhysFMax
	}
	for i := range l.nodes {
		l.fx[i] += ax
		l.fy[i] += ay
	}

	// ④ 积分：v += F·dt; v *= 阻尼（按轴）; 限幅; pos += v·dt。
	for i := range l.x {
		vx := (l.vx[i] + l.fx[i]*dt) * dampX
		vy := (l.vy[i] + l.fy[i]*dt) * dampY
		if sp2 := vx*vx + vy*vy; sp2 > relPhysVMax*relPhysVMax {
			s := relPhysVMax / math.Sqrt(sp2)
			vx *= s
			vy *= s
		}
		l.vx[i], l.vy[i] = vx, vy
		l.x[i] += vx * dt
		l.y[i] += vy * dt
	}

	// ⑤ pin：硬钉 + 清速（被拖者跟手，其余靠物理传导）。
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
