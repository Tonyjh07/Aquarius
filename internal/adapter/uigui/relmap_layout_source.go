package uigui

// 会话树关系图 · **生产物理档：源档**（RSM v1.2「关系星图设计语言」附录 B 的力参数 +
// 源实现的步进算法，逐行移植；D128 转正）。
//
// 与生产档 D127 的关系：D127 的力积分档**保留**为对照回退档（`AQUARIUS_REL_PHYS=d127`
// 启动时切换，运行期不变），默认走本文件的源档。两档共用同一套静息布局、播种、拖拽
// pin、视场适配与绘制；只差「每帧怎么把坐标推一步」。
//
// ———————————————————————— 为什么是这两样，而不是只搬参数 ————————————————————————
//
// 源实现的运动时长**不是参数的函数**，而是结构算出来的：它的 `friction` 同时扮演三个角色——
//
//	① 步长缩放器：三个力通道的位移统统乘 friction；
//	② 衰减时钟：每步 friction ×= 0.992；
//	③ 停止判据：friction 跌破 0.01 即结束。
//
// 于是步数 = ⌈log(0.01/0.2)/log(0.992)⌉ = **373**，与节点数、边数、是否真的收敛**完全无关**
// （RSM 档案实测：373 步 × 16ms 心跳 = 5968ms，浏览器实测 5949ms）。只把 900/200/.1/.2
// 塞进 D127 的力积分档，量到的仍然是 D127 的时长语义（规模相关的涌现时长），答不了
// 「时长到底是参数还是结构决定的」。这正是本档转正的理由：**时长由一条显式日程决定，
// 而不是由「有多少东西要排」隐式漂移**。
//
// ———————————————————————— 移植对照（源实现 → 本档）————————————————————————
//
//	源实现（forceHelper.ts `step()`）                     本档
//	弹簧：位置式，距离偏差 d = dist − edgeLength           同（relSrcEdgeLength = 200dp）
//	      两端按 w = n2.w/(n1.w+n2.w) 分摊位移、方向为连线方向
//	引力：p += (画布矩形中心 − p)·gravity·friction        同，固定点取**树锚**（静息盒心）
//	斥力：(rep_i+rep_j)/d²，**累加进 pp**                  同（刻意保留 pp 双角色混用）
//	速度：p += (p − pp)·friction，然后 pp = p              同
//	阻尼时钟：friction ×= 0.992；< 0.01 判停               同（停判据 = 阻尼代理量）
//	拖拽起始 warmUp：friction 初值 ×0.8（步数 373 → 346）  同（检测 pin 上升沿）
//	卡片接触推力                                          无（源实现没有卡片占位概念）
//	初始位置 = 画布内随机（NaN 回落）                      用本仓的折叠播种（可复现）
//	静止时无锚定                                          仍走 snapToAnchor（纯平移）
//
// 偏离项的取舍理由：
//   - **引力目标取树锚**：源实现取坐标系包围盒中心（与数据无关的固定点，「锚点居中」是其
//     度数竞争的结果——A8 的反面教材）。本档同样是「一个与节点无关的固定点」，但布局核
//     不该知道视口（port/Gio 解耦），故取树锚；构图差别有、时长语义无。
//   - **播种不用随机**：随机初始是源实现「终局不可复现」的根源（其分析报告 §5：四次加载
//     锚点 x = 675/674/239/288）。本仓把「同输入可复现」写成性质测试，故沿用折叠播种。
//     要复现那种不可复现性得另做实验，不该由生产档承担。
//   - **snapToAnchor 保留**：纯平移，不改任何相对位置，对本档要量的形状/时长/残差无影响；
//     保留它让两档的视场适配（fit / 右基准跟随）同一口径。
//
// ———————————————————————— 本档当前**不**保证的东西（D128 待补，勿当契约用）————————————————————————
//
//	- **层序**（R4「子恒在父之右」）：源档没有结构约束，x 单调只是构造期望。实测深链/宽树
//	  上有 3~46 处子落在父的左边。fitView 的「过去部分 = Head 左侧整段」按构造仍成立
//	  （Head 是最右列），但「左即过去」不再严格。
//	- **松手采纳槽位**（A12）：源档的弹簧拉向「距父 200dp」，不看静息槽位 ⇒ 拖完松手会
//	  弹回源档平衡态，用户摆的形状留不住（D126 的 adoptSlot 在本档下不生效）。
//	- **卡片不深压**（R6）：没有卡片接触推力，分叉树上卡片面积重叠实测 0.5~0.97。
//	- **判据是残差**（A17）：判停看 friction 阈值而非布局残差。实测判停那一刻形状位移
//	  只有 0.02~0.4dp，离平衡态却还差 880~8740dp（chain24 残差 = 静息跨度的 43%）。
//	- **有界盒**：`relBound`（20,000dp）小于合法深链跨度，见该常量处的说明。

import (
	"fmt"
	"math"
	"os"
	"strings"
)

// 源实现的力参数档（RSM v1.2 附录 B.1，逐值照抄源配置；这些是「参数」）与阻尼时钟
// （源实现硬编码，非配置项；这些是「结构」，时长由它们算出）：
//
//	repulsion: 900  → relSrcRepulsion     斥力强度（(rep_i+rep_j)/d²；源实现按节点值线性
//	                                      映射，全部节点等值时回落为均值 ⇒ 每节点恒 900）
//	edgeLength: 200 → relSrcEdgeLength    边静息**距离**（源实现是距离，不是位移偏移）
//	gravity: .1     → relSrcGravity       引力强度（拉向一个与节点无关的固定点）
//	friction: .2    → relSrcFriction      步长缩放器 + 衰减时钟初值
//
//	时钟：friction 每步 × relSrcFrictionDecay，跌破 relSrcFrictionFloor 即判停
//	      ⇒ 步数 = ⌈log(0.01/0.2)/log(0.992)⌉ = 373（与规模无关 ⇒ 60fps 下 6.2s）
const (
	relSrcRepulsion     = 900.0
	relSrcEdgeLength    = 200.0
	relSrcGravity       = 0.1
	relSrcFriction      = 0.2
	relSrcFrictionFloor = 0.01
	// relSrcWarmUp 再激励系数（拖拽起始把 friction 初值乘 0.8 ⇒ 步数随之缩短 7%）。
	relSrcWarmUp = 0.8

	// relSrcScheduleSteps **动画时长上限**（帧；60fps 下 900 帧 = 15s）。这是 D128 起
	// 时长**配置化**的那一格：源实现把衰减率硬编码成 ×0.992，于是时长被钉死在 373 帧
	// （≈6.2s）——一个与「要排多少东西」无关的任意常数（违 A15）。本档把「时长」变成
	// 配置：衰减率由它反推（relSrcDecayRate），于是 `log(θ/f₀)/log(γ) = 步数` 恒等于它。
	//
	// 取 900（15s）而非源实现的 373：用户 2026-10-12 实测参照程序在极端情况下松弛可达
	// **数十秒**（原档案记的 ~6s 是 51 节点那一档，不是上限）；且源实现的衰减率意味着
	// **总位移预算** = Σfriction = f₀(1−γᴺ)/(1−γ) ≈ 24.75（dp 量级），大链根本没走完
	// 就被时钟掐停（实测 chain24 判停时离平衡态还差 3248dp = 静息跨度的 43%）。把
	// γ 调成 900 步对应的值（×0.99667）⇒ 总预算 ≈ 57.3，长链才有余量走到位。
	// **上限语义**：还叠加逐帧位移早停（relSrcSettleEps），到点或到位都停、取先到者 ⇒
	// 简单树不会傻等 15s。
	relSrcScheduleSteps = 900
	// relSrcSettleEps 早停阈值（dp/帧，最大逐帧位移）：低于此值在屏幕上不可见
	// （zoom 0.75 下 <0.25px）。这是**残差判据**（A17）——源实现的停判据只看 friction
	// 阈值（求解器内部状态代理量）。
	relSrcSettleEps = 0.3
	// relSrcConeK 结构锥形通道强度（见 stepSource ⑦）：子水平距不足时按 `k × 缺口 ×
	// friction` 推。k=2.0 是实测甜点：六档夹具层序违反全 0、单帧最大位移仅比无通道基线
	// 多 ~25dp（不甩）；k 再大更硬（k=4 时 chain60 单帧 300dp）、k 再小守不住
	// （k=0.3 时 star30/rand200/chain60 残留 10/6/16 处违反）。
	relSrcConeK = 2.0
)

// relSrcDecayRate 时钟衰减率 = (θ/f₀)^(1/步数)：由 relSrcScheduleSteps 反推，使
// 「friction 跌破 θ」的步数恰好等于配置时长。var 而非 const 是为了 spike 能扫档
// （生产只读）。
var relSrcDecayRate = math.Pow(relSrcFrictionFloor/relSrcFriction, 1.0/relSrcScheduleSteps)

// relPhysProfile 物理档位。生产默认 = 源档（relProfSource）。
type relPhysProfile int

const (
	// relProfSource 源档：RSM v1.2 力参数 + 源实现步进（生产默认）。
	relProfSource relPhysProfile = iota
	// relProfD127 D127 力积分档（relPhysSpringX/Y + 树锚 + 卡片接触，判据 = 动能残差）：
	// 保留作对照回退档，等源档的待补项（层序 / 采纳槽位 / 卡片深压 / 残差判据）落地。
	relProfD127
)

// relPhysCur 当前物理档（启动读一次：`AQUARIUS_REL_PHYS=d127` 回退对照档；不设 = 源档）。
var relPhysCur = relPhysProfileEnv()

func relPhysProfileEnv() relPhysProfile {
	switch strings.ToLower(os.Getenv("AQUARIUS_REL_PHYS")) {
	case "d127", "force", "kernel":
		return relProfD127
	}
	return relProfSource
}

func init() {
	if relPhysCur == relProfD127 {
		fmt.Println("[relmap] 物理档 = D127 力积分对照档（AQUARIUS_REL_PHYS=d127）——生产默认是源档（RSM v1.2 力参数 + 源实现步进）")
	}
}

// relContactPush 卡片接触推力（R6「卡片不深压」，源实现没有这个通道，D127 有）：
// 按**卡片轴隙**算——水平/竖直分别扣掉两卡半宽/半高得「到目标槽隙还差多少」；两轴都
// 还没分开才算接触，沿**最小穿透轴**推（不是中心连线方向：斜推会把卡推歪、增出力臂
// 抖动）。等大反向 ⇒ 动量守恒。亲属对（祖先-后代）跳过：它们的间距由弹簧与结构锥形
// 通道定义，误判成相叠会把深链横向顶开。
//
// 位置式（直接改坐标）且被 friction 缩放 ⇒ 连续、不钳位、不会甩；每步推力上限
// = relPhysPushMaxDp × relPhysPushK × friction ≈ 40×0.4×0.2 = 3.2dp，折叠播种态下全卡
// 叠在根上也不会爆。
func relContactPush(l *relLayout, i, j int, fr float64) {
	if relInSubtree(l.tin, l.tout, i, j) || relInSubtree(l.tin, l.tout, j, i) {
		return
	}
	px := relSlotGapDp + l.halfW[i] + l.halfW[j] - math.Abs(l.x[i]-l.x[j])
	py := relSlotGapDp + l.halfH[i] + l.halfH[j] - math.Abs(l.y[i]-l.y[j])
	if px <= 0 || py <= 0 {
		return // 至少一轴分开了：不接触
	}
	pen := math.Min(px, py)
	if pen > relPhysPushMaxDp {
		pen = relPhysPushMaxDp
	}
	f := pen * relPhysPushK * fr
	if px < py { // 沿最小穿透轴分离（同轴叠合时给确定性微偏，保证反向）
		dx := l.x[i] - l.x[j]
		if dx == 0 {
			dx = 0.1 + float64(i-j)*0.01
		}
		s := math.Copysign(f, dx)
		l.x[i] += s
		l.x[j] -= s
	} else {
		dy := l.y[i] - l.y[j]
		if dy == 0 {
			dy = 0.1 + float64(i-j)*0.01
		}
		s := math.Copysign(f, dy)
		l.y[i] += s
		l.y[j] -= s
	}
}

// 结构与 D127 的半隐式欧拉**完全不同**，这正是它被选中的原因：
//   - 没有速度量、没有力、没有积分——三个通道都直接改坐标；
//   - 弹簧与引力当帧写 p，斥力却**累加进 pp**（名义上的「上一帧位置」），随后
//     p += (p − pp)·friction 把斥力混进一个「假速度」里打折作用（源实现的 pp 双角色，
//     刻意保留：它让「弹簧与斥力走在不同时钟上」这一实现事实可被观察）；
//   - 停判据是 friction 跌破阈值（**求解器内部状态代理量**，A17），不是布局残差。
//
// pin.Valid = 源实现的 `fixed`：被拖节点不参与任何通道的位移更新（仍被邻居弹簧拉动、
// 仍对邻居施斥力），位置由帧层每帧写回。拖拽起始走 warmUp 再激励。
func (l *relLayout) stepSource(pin relPin) {
	n := len(l.nodes)
	if n == 0 {
		l.settled = true
		return
	}
	if pin.Valid && (pin.Index < 0 || pin.Index >= n) {
		pin.Valid = false // 越界锚视为无固定（与 D127 档同口径）
	}
	if pin.Valid && !l.wasPinned {
		l.friction = relSrcFriction * relSrcWarmUp // 拖拽起始：再激励（步数 373 → 346）
	}
	l.wasPinned = pin.Valid
	fr := l.friction

	// ① 弹簧（位置式）：把「子—父」距离拉到 edgeLength，按权重分摊位移，等大反向。
	for i := range l.nodes {
		p := l.nodes[i].Parent
		if p < 0 {
			continue
		}
		dx, dy := l.x[i]-l.x[p], l.y[i]-l.y[p]
		d := math.Hypot(dx, dy)
		if d < 1e-9 { // 重合：给确定性微偏（源实现此处是随机方向——本仓要求可复现）
			dx = 0.1 + float64(i-p)*0.01
			dy = 0
			d = math.Abs(dx)
		}
		dd := d - relSrcEdgeLength // 距离偏差（正 = 太远）
		w := l.nodes[i].Weight / (l.nodes[p].Weight + l.nodes[i].Weight)
		if math.IsNaN(w) { // 源实现同款守卫（两端权重皆 0）
			w = 0
		}
		ux, uy := dx/d, dy/d
		if !(pin.Valid && i == pin.Index) { // 子：向父收 (1−w)·dd
			l.x[i] -= ux * (1 - w) * dd * fr
			l.y[i] -= uy * (1 - w) * dd * fr
		}
		if !(pin.Valid && p == pin.Index) { // 父：向子收 w·dd
			l.x[p] += ux * w * dd * fr
			l.y[p] += uy * w * dd * fr
		}
	}

	// ② 引力：**均匀树锚**（只平移、零内部应力）。源实现是「每个节点朝固定点按距离比例
	//    移动」= 收缩，会制造内部应力把整棵树压成球——实测 chain60 被压到 109dp 列距、
	//    chain200 压到 40dp（卡片全叠），时间轴横树「越右越新」直接读不出来。源档的
	//    shape 通道（弹簧 + 结构锥形 + 卡片接触）已经负责形状，这里只保留它的**另一半
	//    职责**：把整树兜在锚点上不漂移。做法与 D126「不锚定节点，锚定树」同源：算整树
	//    相对锚点的位移，每个节点同量平移 ⇒ 内部零应力、整树不漂。
	cx, cy := l.boxCenterOf(l.x, l.y)
	gdx := (l.anchorX - cx) * relSrcGravity * fr
	gdy := (l.anchorY - cy) * relSrcGravity * fr
	for i := range l.nodes {
		l.x[i] += gdx
		l.y[i] += gdy
	}

	// ③ 斥力：全对，1/d²。**累加进 pp**（源实现的 pp 双角色：既是速度基准又是斥力累加器）。
	//    同一遍配对里顺带做**卡片接触推力**（R6，源实现没有卡片占位这回事）——沿**最小
	//    穿透轴**把相叠的卡推开，力度 = 穿透 × K，同样被 friction 缩放（位置式 ⇒
	//    连续、不钳位 ⇒ 不会甩）。排除祖先-后代对（亲属间距由弹簧/锥形通道定义，否则
	//    链会被横向顶开）。融进本遍是为了不额外多跑一次 O(n²)。
	for i := 0; i < n; i++ {
		if pin.Valid && i == pin.Index {
			continue
		}
		for j := i + 1; j < n; j++ {
			if pin.Valid && j == pin.Index {
				continue
			}
			dx, dy := l.x[j]-l.x[i], l.y[j]-l.y[i]
			d := math.Hypot(dx, dy)
			if d < 1e-9 { // 重合：确定性微偏（源实现是随机方向）
				dx = 0.1 + float64(j-i)*0.01
				dy = 0
				d = math.Abs(dx)
			}
			f := relSrcRepulsion / (d * d) // (rep_i+rep_j)/d²，rep 均匀 ⇒ 2×900/d²
			l.ppx[i] += dx * f             // 朝 j 累加（经速度通道作用后表现为互相排斥）
			l.ppy[i] += dy * f
			l.ppx[j] -= dx * f
			l.ppy[j] -= dy * f

			if l.halfW[i]+l.halfW[j] > 0 || l.halfH[i]+l.halfH[j] > 0 {
				relContactPush(l, i, j, fr) // R6：卡片接触（见 relContactPush）
			}
		}
	}

	// ④ 速度通道：p += (p − pp)·friction，然后 pp = p。pp 里混着本帧的斥力的量，
	//    所以「速度」的真实语义是（弹簧+引力的即时位移 − 斥力的累积量）。
	move := 0.0 // 本步最大位移（早停判据用；单位 dp/帧）
	for i := range l.nodes {
		if pin.Valid && i == pin.Index {
			continue
		}
		nx := l.x[i] + (l.x[i]-l.ppx[i])*fr
		ny := l.y[i] + (l.y[i]-l.ppy[i])*fr
		if d := math.Hypot(nx-l.x[i], ny-l.y[i]); d > move {
			move = d
		}
		l.x[i], l.y[i] = nx, ny
		l.ppx[i], l.ppy[i] = l.x[i], l.y[i]
	}

	// ⑦ 结构锥形通道（D128，树形保持 = R4「子恒在父之右」）：对每条边，子相对父的
	//    水平距不足 minGap 时，按**缺口比例**把子往右推 `relSrcConeK × 缺口 × friction`。
	//
	//    为什么是「通道」而不是「投影」：投影是钳位（clamp），一钳就是硬跳变——实测
	//    硬投影在 chain24/chain60 上单帧甩 1392/3574dp（不轻柔）；限速投影在链上沿链
	//    累加、900 步追不平（chain60 残留 27 处）。锥形通道**连续且被 friction 缩放**：
	//    缺口越大推得越多、缺口越小推得越轻 ⇒ 永不超调、不会甩，物理仍可临时把子推到
	//    父左边（**给自由**），锥形通道按比例把它「渐进拉回」。k 扫描（relmap 布局
	//    spike，900 步日程）：k=0.3 时 star30/rand200/chain60 有残留（10/6/16 处），
	//    k=2.0 时**六档夹具全部 0 违反**且单帧最大位移只比基线多 ~25dp。
	//
	//    minGap = 两卡半宽和（父卡右缘到子卡左缘不重叠）⇒ 树形与「卡片不横压」同源；
	//    锥形通道只管水平方向，y（兄弟/版本方向）完全自由。
	for _, i := range l.order {
		p := l.nodes[i].Parent
		if p < 0 {
			continue
		}
		minGap := l.halfW[i] + l.halfW[p]
		if gap := l.x[i] - l.x[p]; gap < minGap {
			l.x[i] += (minGap - gap) * relSrcConeK * fr
		}
	}

	// 有界盒 + NaN 兜底（1/d² 在近重合时会爆；源实现无此护栏，靠随机方向侥幸躲开）。
	// 注意 relBound 的取值问题见常量处说明——本档下深链更容易撞上它。
	for i := range l.x {
		if math.IsNaN(l.x[i]) || math.Abs(l.x[i]) > l.bound {
			l.x[i] = relBoundTo(l.x[i], l.bound)
		}
		if math.IsNaN(l.y[i]) || math.Abs(l.y[i]) > l.bound {
			l.y[i] = relBoundTo(l.y[i], l.bound)
		}
		l.ppx[i], l.ppy[i] = l.x[i], l.y[i]
	}
	if pin.Valid {
		l.x[pin.Index] = relBoundTo(pin.Pos.X, l.bound)
		l.y[pin.Index] = relBoundTo(pin.Pos.Y, l.bound)
		l.ppx[pin.Index], l.ppy[pin.Index] = l.x[pin.Index], l.y[pin.Index]
	}

	// ⑤ 时钟与判停（D128）：衰减率由配置时长反推（relSrcDecayRate）⇒ 「到点」= 时长
	//    上限（15s）；再叠加**逐帧位移**早停（残差判据 A17）⇒ 「到位」就停。
	//    两者取先到者：简单树早停（不必等满 15s），长链用满时长（源档 373 步掐停时
	//    chain24 还差 3248dp）。
	l.iters++
	l.friction = fr * relSrcDecayRate
	l.settled = l.friction < relSrcFrictionFloor || move < relSrcSettleEps
	if l.settled && !pin.Valid {
		l.snapToAnchor()
	}
}
