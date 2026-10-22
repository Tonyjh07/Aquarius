package uigui

// relmap_view.go 右图视场：缩放与平移、视图跟随、整树适配、聚焦域、盒与填充率、
// 装盒所需的缩放推导。全部只读帧态、只改 zoom/off（D126–D140 的视场口径）。

import (
	"math"
)

// toScreen 世界（dp） → 屏幕（px）：screen = world × 有效缩放 + 视口平移。
func (g *relGraph) toScreen(p relPoint) (float32, float32) {
	return float32(p.X*g.z + g.offX), float32(p.Y*g.z + g.offY)
}

// zoomAt 以视口内点 (cx,cy) 为不动点缩放（屏幕世界点固定）。
func (g *relGraph) zoomAt(cx, cy, factor float64) {
	old := g.zoom
	z := old * factor
	if z < relZoomMin {
		z = relZoomMin
	}
	if z > relZoomMax {
		z = relZoomMax
	}
	g.z = z * g.dpx // 入口处重算：直接改 zoom 的路径（测试）也自洽
	if z == old {
		return
	}
	wx := (cx - g.offX) / old / g.dpx
	wy := (cy - g.offY) / old / g.dpx
	g.zoom = z
	g.offX = cx - wx*g.z
	g.offY = cy - wy*g.z
}

// wheel 滚轮（口径同主窗转写区：d<0 = 滚上，d>0 = 滚下）：滚上放大、滚下缩小；
// 钳制边界后**降级为纵向平移**（A9，不静默失效）。越界平移用**定值步长**
// （relWheelPanPx，D121 实测修）——滚轮原始增量是像素累计（一格 ≈ ±100），
// 按增量乘 16 会把视场甩出裁剪盒，树「完全不可见」。
func (g *relGraph) wheel(d int) {
	g.fitFollow = false // 用户自己动了视场 ⇒ 取消跟随（不跟用户抢）
	cx, cy := g.viewW/2, g.viewH/2
	if d < 0 { // 滚上 = 放大
		if g.zoom < relZoomMax {
			g.zoomAt(cx, cy, relZoomStep)
		} else {
			g.offY -= relWheelPanPx // 越界：同一手势改为纵向平移（向上看更早内容）
		}
	} else { // 滚下 = 缩小
		if g.zoom > relZoomMin {
			g.zoomAt(cx, cy, 1/relZoomStep)
		} else {
			g.offY += relWheelPanPx // 越界：向下看更晚内容
		}
	}
}

// focusAnchor 视场**右基准**对准锚点（D127 时间轴横树，A8）：Head 落到视宽
// relAnchorDatumX 处（「现在」在右），纵向仍居中。D140 后只剩**兜底**用途（视场盒取不到、
// 取景域为空）——显式取景走 fitAll/focusScope（含 zoom 适配）。只挪视场，不动 zoom。
// 基准 = **当前位形**的锚点位：点它的时候树已经静止，用户要看的正是那个节点此刻在哪
// （物理位形与静息布局有接触推力造成的残差，按静息位对准会偏）。
func (g *relGraph) focusAnchor() {
	if g.sol == nil || g.anchor < 0 || g.anchor >= len(g.sol.Pos) {
		return
	}
	p := g.sol.Pos[g.anchor]
	g.offX = relAnchorDatumX*g.viewW - p.X*g.z
	g.offY = g.viewH/2 - p.Y*g.z
}

// followSpanStart 开视图跟随（D140）：树还在动时每帧把**当前盒**装进视口（followSpanStep），
// 收敛即停。
//
// 与「一次性对准静息位」的区别（D126 实测修，此处沿用结论）：深链/宽树要几秒到十几秒才
// 落定，期间盒子比静息盒大一大截（实测宽树 1.53×、链 3.06×），按静息盒定的视场会把节点
// 甩出视口（200 节点链实测偏 1900px，「拖节点」点不到人）。
func (g *relGraph) followSpanStart() {
	g.fitFollow = true
	g.followSpanStep(true)
}

// relFollowAlpha 视口跟随的每帧逼近率（0 = 硬锁、1 = 瞬间到位）。0.10 ⇒ 一帧走 10%，
// 约 10 帧（167ms）消掉 63% 的偏差、20 帧 86%——「看得见的缓慢」，又不至于落后太多。
// （曾试过「对称滞后钳」限幅，实测在大位移时反而把视口一帧拽 528dp，等于硬锁换了个
// 阈值——已删，改用 followSpanStep 里的可见性安全网。）
const relFollowAlpha = 0.10

// relFollowSnapPx 跟随的「精确落位」阈值（px）：两轴都逼到这条线以内就直接落到目标位。
const relFollowSnapPx = 2.0

// relZoomEps 跟随的 zoom 收敛阈值（zoom 单位）：落定后的「放大填留白」是几何逼近，不设
// 阈值会永远差一点点 ⇒ 续帧停不下来（R3 不许空转）。
const relZoomEps = 0.004

// followSpanStep 向目标视场**有界滞后地**逼近一步（D140）。返回是否仍需续帧。
//
// 三件事，分工明确：
//
//   - **zoom 收口**：按**当前盒**算 zoomForBox——展开期**立即缩**（盒子连续地在长，缩也是
//     连续的，V1「盒内可见」不破：这是「舒展到一半就出视口」的正面解法）；**落定后**允许
//     迟缓放大一次，把多余留白填掉（用户 2026-10-15：「舒展后下方空白小一点」）。
//     「只缩不放」的旧口径（D2）按此改订：展开期只缩（不回弹），落定后补一次填满。
//   - **平移目标**：横向 P1（Head 落右基准位，装得下才保得住）+ 纵向 P2（**盒心**居中）。
//     纵向原按 Head 居中——Head 不在盒的纵向中点时整盒被顶向一侧（实测上留白 −461px、
//     下留白 197px：顶端被裁、下方大片空白，正是用户截图那一幕）。
//   - **安全网**：软逼近之后才做最小幅度修正（Head 卡在场，A12）。是**可见性**约束，
//     不是审美约束，只在必要时动作。
//
// **D140 修的一处判据错**：原实现把「距目标余量」写成了「安全网位移量」，且用 `||` 判定，
// 于是几乎每帧都走进「精确落位」分支 ⇒ 跟随退化成硬锁（D128 想要的「缓慢跟随、看得见动」
// 从未真正生效），画面上整树的位移被相机完全抵消——用户 2026-10-15 报的「末端节点完全
// 不会动」正是这条。现在判据是**距目标余量**，且两轴都进阈值才落位。
func (g *relGraph) followSpanStep(hard bool) bool {
	if g.sol == nil {
		return false
	}
	b, ok := g.boxNow()
	if !ok {
		return false
	}
	moved := false
	// ① zoom 收口。上限：常规 relZoomMax，但**不许把显式取景已放大的 zoom 拉回来**——
	// 用户按「聚焦当前节点」顶到 2.5× 时，跟随只该在盒子装不下时缩小它（D3 的取景上限
	// 是用户意图，跟随只是兜底）。
	cap := relZoomMax
	if g.zoom > cap {
		cap = g.zoom
	}
	if need := g.zoomForBox(b, cap); need < g.zoom {
		g.zoom, g.z = need, need*g.dpx // 展开期：立即缩
		moved = true
	} else if g.lay != nil && g.lay.settled && need > g.zoom+relZoomEps {
		g.zoom += (need - g.zoom) * relFollowAlpha // 落定后：迟缓放大填留白
		g.z = g.zoom * g.dpx
		moved = true
	}
	// ② 平移（目标已钳进可行域，见 spanTarget）
	tgt := g.spanTarget(b)
	tx, ty := tgt.X, tgt.Y
	remX, remY := math.Abs(tgt.X-g.offX), math.Abs(tgt.Y-g.offY)
	if hard {
		remX, remY = 0, 0
	} else if remX < relFollowSnapPx && remY < relFollowSnapPx {
		remX, remY = 0, 0 // 精确落位：几何逼近会停在阈值外几 px
	} else {
		tx = g.offX + (tgt.X-g.offX)*relFollowAlpha
		ty = g.offY + (tgt.Y-g.offY)*relFollowAlpha
	}
	g.offX, g.offY = tx, ty
	// ③ 安全网
	if g.clampAnchorVisible() {
		moved = true
	}
	return moved || remX >= relFollowSnapPx || remY >= relFollowSnapPx
}

// spanTarget 跟随的平移目标（D140）：横向 P1「Head 落右基准位」（时间轴的「现在」在右，
// A8 的偏好形式）、纵向 P2「**盒心**居中」。最后把目标本身钳进可行域——「盒在视口内」。
//
// 钳的必须是**目标**而不是结果：钳结果会和下一帧的目标打架（P1 每帧都想把盒顶出去、安全网
// 每帧再拉回来）⇒ 跟随永远收敛不了、每帧空转重绘。
// 装不下（盒比视口大）时跳过钳制：那种档位只承诺 Head 在场（V2）。
func (g *relGraph) spanTarget(b viewBox) relPoint {
	x := g.viewW/2 - (b.MinX+b.MaxX)/2*g.z
	if g.anchor >= 0 && g.anchor < len(g.sol.Pos) {
		x = relAnchorDatumX*g.viewW - g.sol.Pos[g.anchor].X*g.z
	}
	y := g.viewH/2 - (b.MinY+b.MaxY)/2*g.z
	// 容差 0.5px：zoom = min(视宽/盒宽, …) 时盒宽×z 与视宽是同一个浮点乘除的往返，
	// 精确相等会因末位误差误判成「装不下」而跳过钳制（Head 贴边几个像素）。
	if b.w()*g.z <= g.viewW+0.5 {
		if sx := b.MinX*g.z + x; sx < 0 {
			x -= sx
		} else if sx := b.MaxX*g.z + x; sx > g.viewW {
			x -= sx - g.viewW
		}
	}
	if b.h()*g.z <= g.viewH+0.5 {
		if sy := b.MinY*g.z + y; sy < 0 {
			y -= sy
		} else if sy := b.MaxY*g.z + y; sy > g.viewH {
			y -= sy - g.viewH
		}
	}
	return relPoint{X: x, Y: y}
}

// clampAnchorVisible 把 Head 卡拉回视口内（最小幅度）；返回是否发生了修正。软逼近与
// 精确落位之后都要过这一关——「Head 在场」（A12）比「Head 压在基准位」优先。
func (g *relGraph) clampAnchorVisible() bool {
	if g.sol == nil || g.anchor < 0 || g.anchor >= len(g.sol.Pos) {
		return false
	}
	p := g.sol.Pos[g.anchor]
	hw, hh := relCardHalf(g.geo[g.anchor])
	dx, dy := 0.0, 0.0
	if hw > 0 {
		m := hw*g.z + 8
		if sx := p.X*g.z + g.offX; sx < m {
			dx = m - sx
		} else if sx > g.viewW-m {
			dx = g.viewW - m - sx
		}
	}
	if hh > 0 {
		m := hh*g.z + 8
		if sy := p.Y*g.z + g.offY; sy < m {
			dy = m - sy
		} else if sy > g.viewH-m {
			dy = g.viewH - m - sy
		}
	}
	g.offX += dx
	g.offY += dy
	return dx != 0 || dy != 0
}

// fitView 开窗首帧：整棵可见树**适配视口**（A13，D127 横树版，D140 改口径）。
//
//   - **zoom 基准取静息盒**（restBox）：首帧树还在 0.6 折叠播种态，按当前盒会算出一个
//     过大的 zoom，而跟随期「展开只缩不放」救不回来。展开期的实际适配由 followSpanStep
//     每帧按**当前盒**收口（这是 D140 的正解：旧版全程按静息盒，同时落定盒比静息盒宽
//     一大截 ⇒ 舒展完就出视口）。
//   - **构图取当前位形**：横向 P1（Head 落右基准位）、纵向 P2（盒心居中），由
//     followSpanStep 硬落位。装得下时两者都精确成立；装不下（宽树/长会话）时 P1 由可行域
//     钳制让步，Head 仍在场（A12）。
//   - **zoom 上限** = relZoomMax（滚轮档同值；显式取景动作另有 relFocusZoomMax）。
//   - 数据变更**不**重设 zoom/视口（见 frame）；显式取景走 viewPending（D140）。
func (g *relGraph) fitView() {
	b, ok := g.restBox()
	if !ok {
		g.focusAnchor()
		return
	}
	g.zoom = g.zoomForBox(b, relZoomMax)
	g.z = g.zoom * g.dpx
	if g.anchor >= 0 && g.anchor < len(g.sol.Pos) {
		g.followSpanStart() // 入场展开期间跟随（首帧硬落位，之后有界滞后）
		return
	}
	// 无锚点（理论不可达）：盒心居中，不开跟随
	g.centerBox(b)
}

// recenterTree 拖后松手的视场善后。**默认什么都不做**（用户 2026-10-12：「不管视角被
// 移动到哪，拖动任意节点时都会回到这个样子」；且「这样子同时是初始的样子 ⇒ 初始视角并
// 没有适配这个树」）。
//
// 原实现无条件重定中（装得下就盒心对准、装不下就 Head 基准），与开窗 fit 走同一条路径
// ⇒ 松手后看到的构图恒等于开窗构图，用户怎么移视角都会被拽回去。而源档拖拽并不把树摊宽
// （D129 起不写回静息槽位，物理自己收敛回平衡态），D121 当年「拖开摊宽的树会丢」的顾虑
// 已不成立。故松手该做的只剩兜底：**画面上还有节点就一个像素都不动**——用户把视角留在
// 过去看历史是他的选择，A7 状态栏承担切片诚实性；只有把视角移到了空白处（一棵节点都不
// 可见）才给一张装得下树的视图，否则画面全空、看着像挂了。
func (g *relGraph) recenterTree() {
	if g.sol == nil || g.lay == nil {
		return
	}
	if g.anyNodeVisible() {
		return
	}
	// D140：兜底盒取**当前位形**（用户此刻看到的树就是这一片），不是静息盒。
	b, ok := g.boxNow()
	if !ok {
		return
	}
	if b.w()*g.z <= g.viewW && b.h()*g.z <= g.viewH {
		g.centerBox(b) // 装得下：盒心对准视口中心
		return
	}
	// 装不下（长会话）：视图跟随（与 fitView 的回退口径一致）——至少当前消息可见。
	g.followSpanStart()
}

// anyNodeVisible 视口内是否还有可见节点（用于松手兜底判断）。
func (g *relGraph) anyNodeVisible() bool {
	if g.sol == nil {
		return false
	}
	for li := range g.sol.Pos {
		if li >= len(g.geo) {
			continue
		}
		hw, hh := relCardHalf(g.geo[li])
		xs, ys := g.sol.Pos[li].X*g.z+g.offX, g.sol.Pos[li].Y*g.z+g.offY
		if xs+hw*g.z >= 0 && xs-hw*g.z <= g.viewW && ys+hh*g.z >= 0 && ys-hh*g.z <= g.viewH {
			return true
		}
	}
	return false
}

// ——— D128：三种聚焦域（状态栏三按钮）———
//
// 同一个「把某片内容装进视口」的动作，三种取景范围（互不重叠，覆盖三个常见意图）：
//
//	focusScopeAll     整树：视口容纳**整棵可见树**（按钮文案「装下整树」；另有「回到初始态」
//	                  负责重排重放，与取景分开）
//	focusScopeLine    历史线：视口容纳 **root → Head 的祖先链**（这一轮对话的主干）
//	focusScopeNode    当前节点：视口容纳 **Head + 其父 + 其子**（一跳邻域；按钮文案写
//	                  「聚焦当前节点」，域比文案宽是刻意的——父是理解当前节点的前提，子是
//	                  「接下来会是什么」的答案）
//
// 共同口径（D140 改订）：内容按**当前物理位形**取盒（用户此刻看到的树就是这一片），
// **zoom 跟着一起适配**（上限 relFocusZoomMax，D3）——只挪视场不动 zoom 时，放大读一跳
// 邻域与「一屏看整树」都做不到。装不下（长链触底）时沿用视图跟随兜底（Head 优先在场）。
// 用户手动 zoom 仍是私有的：跟随一旦收敛就停，且 wheel/pan 立即取消跟随。
const (
	focusScopeAll = iota
	focusScopeLine
	focusScopeNode
)

// viewAction 显式视场动作（D140）：状态栏按下后**不在分发处直接算**——视场矩阵
// （viewW/viewH/dpx）与 g.z 只在帧首刷新，故置待办、帧首统一执行（与 fitPending/everFit
// 同一条路）。actions 由 dispatch / drainNodeAction 置位，frame 消费。
type viewAction uint8

const (
	vaNone      viewAction = iota
	vaFitAll               // 装下整树（仅状态栏按钮；2026-10-19 撤掉图侧菜单项）
	vaFocusLine            // 聚焦当前历史
	vaFocusNode            // 聚焦当前节点
)

// focusScope 对某片内容取盒并适配视口（D140：**zoom 一起适配**）。indices = 参与取景的
// 本地索引（空 = 理论不可达，退化为整树）。
func (g *relGraph) focusScope(scope int) {
	if g.sol == nil || g.lay == nil {
		return
	}
	idx := g.scopeIndices(scope)
	if len(idx) == 0 {
		g.focusAnchor()
		return
	}
	b, ok := g.boxOf(idx)
	if !ok {
		g.focusAnchor()
		return
	}
	g.zoom = g.zoomForBox(b, relFocusZoomMax)
	g.z = g.zoom * g.dpx
	if scope == focusScopeAll {
		// 整树：与「装下整树」同口径（P1 横向基准 + P2 盒心）；树还在展开就继续跟随
		g.followSpanStep(true)
		g.fitFollow = !g.lay.settled
		return
	}
	if b.w()*g.z > g.viewW+1 || b.h()*g.z > g.viewH+1 {
		g.followSpanStart() // 域装不下（长链触底）：Head 优先在场（A12）
		return
	}
	// 小域（历史线/当前节点）取**域盒心**居中：域偏在树一角时，P1 的右基准位会把域顶出视口。
	g.centerBox(b)
	g.fitFollow = false // 显式取景 = 用户选定的视场（与 wheel/pan 取消跟随同向）
}

// fitAll 「装下整树」（D140；原「回到当前」）：把**当前盒**一次到位装进视口——zoom 与
// 平移一起适配，不重排、不动物理、不动数据。树还在展开就继续跟随，落定即停。
//
// 与旧「回到当前」的区别：旧版只挪视场（zoom 不动）且把 Head 钉在右基准位，于是「树明明
// 装不下却什么都不改」。现在这条是用户视角的**显式自救**动作（滚轮缩小之外的出口）。
func (g *relGraph) fitAll() {
	if g.sol == nil {
		return
	}
	b, ok := g.boxNow()
	if !ok {
		return
	}
	g.zoom = g.zoomForBox(b, relFocusZoomMax)
	g.z = g.zoom * g.dpx
	g.followSpanStep(true)
	g.fitFollow = g.lay != nil && !g.lay.settled
}

// scopeIndices 取景范围的本地索引集。
func (g *relGraph) scopeIndices(scope int) []int {
	n := len(g.vis.nodes)
	switch scope {
	case focusScopeLine:
		// root → Head 的祖先链（不含预算裁掉的节点：本地索引连续，链在可见集内连续）
		var chain []int
		for i := g.anchor; i >= 0; i = g.geo[i].Parent {
			chain = append(chain, i)
			if len(chain) >= n {
				break
			}
		}
		return chain
	case focusScopeNode:
		if g.anchor < 0 || g.anchor >= n {
			return nil
		}
		idx := []int{g.anchor}
		if p := g.geo[g.anchor].Parent; p >= 0 && p < n {
			idx = append(idx, p) // 父：理解当前节点的来路
		}
		for _, c := range g.lay.children[g.anchor] { // 子：接下来会发生什么
			idx = append(idx, c)
		}
		return idx
	default: // focusScopeAll：整棵可见树
		idx := make([]int, n)
		for i := range idx {
			idx[i] = i
		}
		return idx
	}
}

// centerBox 盒心对准视口中心（不重置 zoom）——小域取景（历史线/当前节点）与拖后兜底用。
func (g *relGraph) centerBox(b viewBox) {
	g.offX = g.viewW/2 - (b.MinX+b.MaxX)/2*g.z
	g.offY = g.viewH/2 - (b.MinY+b.MaxY)/2*g.z
}

// relayoutFromSeed 回到初始态：重新折叠播种（速度清零、时钟重上）⇒ 整树**重新排列**
// 一次（首帧即涌现的观感重放），并立刻把视场适配到能容下整树（剩余的排布由随后
// 15s 的物理展开完成，followSpanStep 负责跟随）。
func (g *relGraph) relayoutFromSeed() {
	if g.lay == nil {
		return
	}
	g.lay.seedCollapsed()
	g.sol = g.lay.solution()
	g.relaxLeft = relSettleFrames
	g.slots = nil // 手动摆放的槽位一并作废：回到初始态 = 忘掉用户的摆放
	g.fitPending = true
	g.everFit = false // 让下一帧的 fitView 按新播种态重新适配视口
}

// ——— D140：视场盒（静息盒只用于首帧 zoom，其余口径一律取当前位形）———

// viewBox 世界盒（dp；含卡片占位与 relFitMargin）。
type viewBox struct {
	MinX, MinY, MaxX, MaxY float64
}

func (b viewBox) w() float64 { return b.MaxX - b.MinX }

func (b viewBox) h() float64 { return b.MaxY - b.MinY }

// boxAccum 盒累加器：卡片占位取 relCardHalf（与布局/绘制同源；否则「按 A 排、按 B 装」）。
type boxAccum struct {
	MinX, MinY, MaxX, MaxY float64
	n                      int
}

func (a *boxAccum) add(p relPoint, hw, hh float64) {
	if a.n == 0 {
		a.MinX, a.MinY, a.MaxX, a.MaxY = p.X-hw, p.Y-hh, p.X+hw, p.Y+hh
	} else {
		a.MinX, a.MinY = math.Min(a.MinX, p.X-hw), math.Min(a.MinY, p.Y-hh)
		a.MaxX, a.MaxY = math.Max(a.MaxX, p.X+hw), math.Max(a.MaxY, p.Y+hh)
	}
	a.n++
}

// box 收口成视场盒（外扩 relFitMargin：留点余量免得贴边被裁）。
func (a *boxAccum) box() (viewBox, bool) {
	if a.n == 0 {
		return viewBox{}, false
	}
	padX := (a.MaxX - a.MinX) * relFitMargin
	padY := (a.MaxY - a.MinY) * relFitMargin
	return viewBox{a.MinX - padX, a.MinY - padY, a.MaxX + padX, a.MaxY + padY}, true
}

// boxNow 当前**物理位形**的盒——D140 的视场基准（静息盒只用来定首帧 zoom）。
func (g *relGraph) boxNow() (viewBox, bool) {
	if g.sol == nil {
		return viewBox{}, false
	}
	var a boxAccum
	for li := range g.vis.nodes {
		if li >= len(g.sol.Pos) || li >= len(g.geo) {
			continue
		}
		hw, hh := relCardHalf(g.geo[li])
		a.add(g.sol.Pos[li], hw, hh)
	}
	return a.box()
}

// boxOf 给定**本地索引集**的盒（三个聚焦域用）。
func (g *relGraph) boxOf(idx []int) (viewBox, bool) {
	if g.sol == nil {
		return viewBox{}, false
	}
	var a boxAccum
	for _, li := range idx {
		if li < 0 || li >= len(g.sol.Pos) || li >= len(g.geo) {
			continue
		}
		hw, hh := relCardHalf(g.geo[li])
		a.add(g.sol.Pos[li], hw, hh)
	}
	return a.box()
}

// restBox 静息布局的外接盒（**只用于开窗首帧的 zoom 基准**：首帧树还在 0.6 折叠播种态，
// 按当前盒会算出过大的 zoom，而展开期只缩不放救不回来）。
func (g *relGraph) restBox() (viewBox, bool) {
	if g.lay == nil || len(g.vis.nodes) == 0 || len(g.lay.restX) == 0 {
		return viewBox{}, false
	}
	rest := g.lay.restPos()
	var a boxAccum
	for li := range g.vis.nodes {
		hw, hh := relCardHalf(g.geo[li]) // 世界坐标下的卡片占位（不随 zoom 变）
		a.add(rest[li], hw, hh)
	}
	return a.box()
}

// zoomForBox 把盒装进视口的 zoom（D140 口径）：整盒两维都装得下；上限 maxZoom（滚轮档 =
// relZoomMax、显式取景档 = relFocusZoomMax）、下限 relZoomMin（极端长会话装不下时由
// followSpanStep 的 Head 安全网兜住，A12）。
//
// **D7-b 去掉的那条**：旧口径还要求「Head 左侧的过去部分装进右基准位以左」（按静息盒算），
// 于是宽树永远在右边留 28% 空、且整个视场被这个语义按住不动。现在右基准位降级为**平移
// 偏好**（spanTarget 的 P1）：装得下才落在那儿，装不下就让步——两维尽量贴满。
func (g *relGraph) zoomForBox(b viewBox, maxZoom float64) float64 {
	w, h := b.w(), b.h()
	if w <= 0 || h <= 0 {
		return g.zoom
	}
	zf := math.Min(g.viewW/w, g.viewH/h) / g.dpx
	if zf < relZoomMin { // 防呆下限：树大得离谱（/vac 病态输入）也不至于除出 0
		zf = relZoomMin
	}
	if zf > maxZoom {
		zf = maxZoom
	}
	return zf
}
