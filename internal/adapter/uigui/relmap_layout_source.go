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
	relSrcFrictionDecay = 0.992
	relSrcFrictionFloor = 0.01
	// relSrcWarmUp 再激励系数（拖拽起始把 friction 初值乘 0.8 ⇒ 步数 373 → 346）。
	relSrcWarmUp = 0.8
)

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

// stepSource 源实现的步进（forceHelper.ts `step()` 的逐行移植，见文件头对照表）。
//
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

	// ② 引力：拉向树锚（源实现 = 画布矩形中心，见文件头偏离项）。
	for i := range l.nodes {
		if pin.Valid && i == pin.Index {
			continue
		}
		l.x[i] += (l.anchorX - l.x[i]) * relSrcGravity * fr
		l.y[i] += (l.anchorY - l.y[i]) * relSrcGravity * fr
	}

	// ③ 斥力：全对，1/d²。**累加进 pp**（源实现的 pp 双角色：既是速度基准又是斥力累加器）。
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
		}
	}

	// ④ 速度通道：p += (p − pp)·friction，然后 pp = p。pp 里混着本帧的斥力量，
	//    所以「速度」的真实语义是（弹簧+引力的即时位移 − 斥力的累积量）。
	for i := range l.nodes {
		if pin.Valid && i == pin.Index {
			continue
		}
		l.x[i] += (l.x[i] - l.ppx[i]) * fr
		l.y[i] += (l.y[i] - l.ppy[i]) * fr
		l.ppx[i], l.ppy[i] = l.x[i], l.y[i]
	}

	// 有界盒 + NaN 兜底（1/d² 在近重合时会爆；源实现无此护栏，靠随机方向侥幸躲开）。
	// 注意 relBound 的取值问题见常量处说明——本档下深链更容易撞上它。
	for i := range l.x {
		if math.IsNaN(l.x[i]) || math.Abs(l.x[i]) > relBound {
			l.x[i] = relBoundFinite(l.x[i])
		}
		if math.IsNaN(l.y[i]) || math.Abs(l.y[i]) > relBound {
			l.y[i] = relBoundFinite(l.y[i])
		}
		l.ppx[i], l.ppy[i] = l.x[i], l.y[i]
	}
	if pin.Valid {
		l.x[pin.Index] = relBoundFinite(pin.Pos.X)
		l.y[pin.Index] = relBoundFinite(pin.Pos.Y)
		l.ppx[pin.Index], l.ppy[pin.Index] = l.x[pin.Index], l.y[pin.Index]
	}

	// ⑤ 阻尼时钟：每步自乘衰减，跌破阈值即判停（**代理量判据**，见文件头待补项）。
	l.iters++
	l.friction = fr * relSrcFrictionDecay
	l.settled = l.friction < relSrcFrictionFloor
	if l.settled && !pin.Valid {
		l.snapToAnchor()
	}
}
