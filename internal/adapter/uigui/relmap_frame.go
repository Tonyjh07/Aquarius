package uigui

// relmap_frame.go 右图自绘 + 手势 + 右键菜单（D120⑦，relation-map-design §5.2/§5.4/§6.3）。
//
// 绘制符号全部由派生纯函数（relmap.go）喂入：fill = depthMap[Level mod 4]、shape = Role
// 四形、直径 = weight 查表（A4：与 degree 无关）、描边 = 主干亮环 ⊕ 分叉虚线环、边色 =
// source 填充色、边型 = EdgeKind（顺接实线 / 版本链虚线）、标签 = 权重预算 + zoom 字号。
//
// 手势（§5.2 动词表）：拖拽空白 = pan；滚轮 = zoom（夹 [0.5,1.5]，**越界降级纵向平移**
// A9）；悬停 = 陈述 tooltip（inspect）；左键节点 = /goto <id>（A10）；右键节点 = 菜单
// （复制 ID / 删除 / 回到当前）。
//
// 性能纪律（§5.4）：视口外扩裁剪、懒重排（平移/缩放/悬停不重排——重排只由数据变更 /
// rebudget 触发，R3）。

import (
	"image"
	"image/color"
	"io"
	"math"
	"strings"

	"gioui.org/f32"
	"gioui.org/gesture"
	"gioui.org/io/clipboard"
	"gioui.org/io/event"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"

	"github.com/Tonyjh07/Aquarius/internal/port"
)

// 右图档位（偶然实现，§7 可调；zoom 边界即 A9 的钳制区间）。
const (
	relZoomMin    = 0.5
	relZoomMax    = 1.5
	relZoomStep   = 1.1  // 每格滚轮缩放倍率
	relNodeBaseDp = 34.0 // w=1.00 的直径基准（dp）；直径 = weight × base（A4）
	relRingW      = 2.5  // 分叉虚线外环描边宽
	relEdgeW      = 1.5  // 边线宽
	relLabelBase  = 12.0 // 标签基准字号（sp）× zoom，钳 [10,16]（A9）
	relLabelMinSp = 10.0
	relLabelMaxSp = 16.0
	relCullPad    = 0.2 // 视口外扩 20% 裁剪
	relDashOn     = 6.0 // 虚线段长（dp）
	relDashOff    = 5.0 // 虚线间隔（dp）
	relHitSlop    = 6.0 // 命中判定外扩（dp）
	// relGrabSlop 抓手判定的外扩（dp，起拖时用，比点击判定宽）：低 zoom 下卡片塌缩成
	// 小徽标，按下时命中极易差几像素落空——静默退化成平移是最糟的失败方式。
	relGrabSlop = 14.0
	relDragSlop = 4.0 // 按下到拖动的位移阈值（dp；未越过 = 点击）
	// relGrabDecay 抓取偏移每帧衰减率（D121 实测修：光标与节点不许错位）。起拖第一帧
	// 节点还停在按下时的相对位置（零跳变），随后中心**逐帧滑向光标正下方**并锁死——
	// 若直接清零，按在标签上起拖会瞬移 100~185px（标签区离节点中心很远）。
	relGrabDecay = 0.5 // ≈7 帧（120ms）归零
	relWheelMax  = 1e6 // 滚轮范围上限（放开；主窗 D71 钉点口径不适用本窗）
	// relWheelPanPx 滚轮越界降级平移的**定值步长**（px，D121 实测修）：滚轮原始增量是
	// 像素累计（Windows 一格 ≈ ±100），按增量乘 16 会把视场甩出裁剪盒（≈1600px/格）——
	// 「某些缩放下树完全不可见」的元凶之一。定值 48px/格 = 一眼可见、一格一格走。
	relWheelPanPx = 48.0
	relArrowSize  = 6.0 // 箭头半长（dp × 有效缩放）
	// 节点卡（D121「内容入内」）：标签可见时节点 = [形状徽标 | 标签] 卡片，标签画在
	// 卡片内部（此前在节点右侧外面飘着）。徽标仍是 Role 四形 + Level 色（A2/§3.1 不变），
	// 卡片只承载文字。下列均为卡片内边距/圆角档位（偶然实现，§7）。
	relCardGapDp  = 7.0  // 徽标与标签间距
	relCardPadDp  = 9.0  // 卡片左右内边距（左内边距让分叉虚线外环不越出卡片边界）
	relCardPadV   = 7.0  // 卡片上下内边距（徽标不贴卡片上下沿——菱形/空心圆的尖会切边）
	relCardRoundP = 0.35 // 卡片圆角 = 该系数 × 节点半径（随 weight 缩放）
	// relCardTintF 卡片底色 = 层色向窗底色靠拢的比例（其余取窗底）。卡片因此跟着节点走：
	// 浅色版同色系淡彩、深色版自动加深——一个公式两版都对（§15.4 加色不改代码）。
	relCardTintF = 0.16
	// relEdgeHalo / relEdgePathW 主干的两个杠杆（× 边宽）：窗底色粗线垫在本色细线之下
	// 把主干从背景与相邻卡片里抬出来，本色线再加粗一档——只描边看不出「这条线更主要」。
	relEdgeHalo  = 3.4
	relEdgePathW = 1.9
	// relOutlineSegs 圆/空心圆虚线环的固定段数：步长随 zoom 变，按步长采样在放大时
	// 会多到上万段——按段数采样才是稳的。
	relOutlineSegs = 24
	// relLabelMaxDp 标签区宽上限（dp × 有效缩放）：摘要可到 40 字程（CJK 40 字 ≈ 480dp），
	// 不封顶则一张卡横跨半个视口、邻卡互相吞字。超出走 MaxLines=1 的省略号。
	relLabelMaxDp = 96.0
	// relSettleFrames 拖拽/收敛的续帧硬顶（帧）：正常几帧内收敛，触顶即停——
	// 防病态输入下无限松弛（交互态也守 R3 的「稳定态不烧资源」）。
	relSettleFrames = 240
	// relEntryFrames 入场动画总帧数（§5.3）：开窗首帧数据的布局动画时长。180 帧 ≈
	// 3 秒（60fps）——用户口径「几秒都可以」。分帧展开（根点 → 收敛布局，ease-out），
	// 不做独立时间线：帧计数走既有帧循环（InvalidateCmd 续帧），播完即停（R3）。
	relEntryFrames = 180
	// relAnimLeadFrac 入场动画的**级联**占比：前 leadFrac 帧按层序错峰起滑（浅层先动），
	// 后 (1-leadFrac) 帧全部滑到收敛位。树的「长出来」是逐层展开，不是整图同时平移。
	relAnimLeadFrac = 0.5
)

// relEstTextW 文本宽估算（dp）：CJK/全角记 1.0em、其余记 0.55em × 字号。
//
// 卡片宽度属 L4 偶然实现（§7）：只需「够宽到不挤、又不至于溢出」的量级，估宽误差
// 表现为标签提前出省略号或卡片略宽——不引入额外测量 pass（每帧每节点的文本测量不划算）。
func relEstTextW(txt string, sp float64) float64 {
	w := 0.0
	for _, r := range txt {
		switch {
		case r >= 0x1100: // CJK / 全角（含标点）
			w++
		case r == ' ':
			w += 0.33
		default:
			w += 0.55
		}
	}
	return w * sp
}

// relGraph 右图帧态（historyState 持有；仅历史窗 goroutine 读写）。
type relGraph struct {
	model  *relModel
	vis    relVisible
	geo    []relGeoNode
	anchor int
	sol    *relSolution
	lay    *relLayout // 增量求解态（D121：拖拽逐帧松弛必须从当前坐标接着走）

	zoom         float64 // 视场倍率 [relZoomMin, relZoomMax]（A9，用户口径）
	z            float64 // **有效**缩放 = zoom × PxPerDp（dp 世界 → 屏幕 px；含 DPI 换算）
	spx          float64 // Metric.PxPerSp（标签 px 换算；帧首刷新，0/1 = 1）
	dpx          float64 // Metric.PxPerDp（帧首刷新；0/1 = 1）
	offX, offY   float64 // 视口平移（screen = world×z + off）
	viewW, viewH float64 // 视口尺寸（px，帧首刷新）
	focusPending bool    // 下帧居中锚点（「回到当前」，A8）
	fitPending   bool    // 下帧把树拉回视野（拖后松手，D121 实测修；不重置 zoom）
	everFit      bool    // 已做过首帧整树适配（此后 rebuild 不动视场——数据变更不重置 zoom/视口）

	// 入场动画（§5.3）：首帧数据时从**根点**逐层展开到**收敛布局**（级联 + ease-out，
	// 秒级）。起点统一 = 根节点的收敛位置——「树从对话起点长出来」。
	animOn     bool
	animFrame  int
	animFrames int
	animOrigin relPoint   // 展开起点（根节点的收敛位置）
	animTo     []relPoint // 终点（收敛布局）

	tag    int            // 原始指针 tag（悬停/点击/右键/平移/拖节点）
	scroll gesture.Scroll // 滚轮（范围放开的纵向滚动 → wheel()）

	hover int // 悬停节点（本地索引；-1 无）

	// 主键按下：未越过拖拽 slop = 点击（释放同点 → /goto，A10）；
	// 越过 = 平移（按下空白）或**拖节点**（按下节点，D121）。
	pressed bool
	// 平移基准（绝对跟踪，主窗拖动铁律 2 同款：按下记起点，拖动按差值重算——不累计增量）。
	pressNode                  int
	dragging                   bool
	panStartOffX, panStartOffY float64
	panStartX, panStartY       float32

	// 节点拖拽（D121）：按下命中节点 + 越过 slop → 该节点临时固定于指针位置，
	// 每帧带锚松弛（周围被斥力推开）；松手锚消失 → 恢复自由、自然收敛。
	dragNode  int     // 被拖节点本地索引（-1 = 未拖节点）
	grabNode  int     // 抓手判定的节点（按下那一刻用 relGrabSlop 宽半径命中；-1 = 空白）
	dragGrabX float32 // 指针相对节点中心的偏移（抓取点保持，节点不跳到指针下）
	dragGrabY float32
	dragPX    float32 // 最新指针位置（帧层松弛用）
	dragPY    float32
	settling  bool // 松手后回归自由收敛中（交互态，收敛即停 = R3）
	relaxLeft int  // 松弛帧预算（硬顶，防病态输入下无限续帧）

	menuOpen bool // 右键菜单（节点上下文）
	menuNode int  // 菜单所属节点（本地索引）
	menuX    float32
	menuY    float32
	menuCopy widget.Clickable
	menuRm   widget.Clickable
	menuHome widget.Clickable
}

// newRelGraph 构造右图帧态（默认 zoom = 1.0；首帧数据适配整树，见 frame 的 everFit）。
func newRelGraph() *relGraph {
	return &relGraph{zoom: 1.0, z: 1, dpx: 1, spx: 1, hover: -1, pressNode: -1, grabNode: -1, dragNode: -1, tag: 1}
}

// rebuild 数据变更 / rebudget 时重排（R3）：派生几何 → 求解 → 重置交互态。
//
// **首帧数据**（everFit 未置位，即开窗）走入场动画（§5.3）：从根点逐层展开到收敛
// 布局（时长秒级）；此后数据变更**同步全解、不动视场**（不重置用户 zoom，D121
// 实测修）。视场只在开窗首帧适配（A13）与拖后松手时调整（releaseDrag）。
func (g *relGraph) rebuild(m *relModel, vis relVisible) {
	g.model = m
	g.vis = vis
	g.geo, g.anchor = relGeo(m, vis)
	if g.anchor < 0 && len(g.geo) > 0 {
		g.anchor = 0
	}
	if lay, ok := newRelLayout(g.geo); ok {
		g.lay = lay
		lay.relax(relMaxIter, relPin{})
		target := lay.solution() // 收敛布局（拖拽增量松弛也从这里接着走）
		if !g.everFit {
			origin := target.Pos[lay.root] // 根点：树从对话起点长出来
			g.sol = &relSolution{Pos: make([]relPoint, len(g.geo))}
			for i := range g.sol.Pos {
				g.sol.Pos[i] = origin
			}
			g.startEntryAnim(origin, target.Pos)
		} else {
			g.sol = target
		}
	} else {
		g.lay = nil
		g.sol = &relSolution{Pos: make([]relPoint, len(g.geo))}
	}
	g.hover = -1
	g.pressed = false
	g.pressNode = -1
	g.grabNode = -1
	g.dragNode = -1
	g.settling = false
	g.menuOpen = false
}

// startEntryAnim 启动入场动画（根点 → 收敛布局，级联 + ease-out，relEntryFrames 帧）。
func (g *relGraph) startEntryAnim(origin relPoint, to []relPoint) {
	g.animOn = true
	g.animFrame = 0
	g.animFrames = relEntryFrames
	g.animOrigin = origin
	g.animTo = to
}

// cancelEntryAnim 交互打断：任何按下即跳到收敛布局（不与用户抢控制权），并释放动画态。
func (g *relGraph) cancelEntryAnim() {
	if !g.animOn {
		return
	}
	g.animOn = false
	if g.animTo != nil {
		g.sol = &relSolution{Pos: g.animTo}
	}
	g.animTo = nil
}

// nodeRadius 节点半径（**dp**，不含缩放）：weight × 基准 ÷ 2（A4 与 degree 无关）。
// 屏幕半径 = nodeRadius × g.z（含 zoom 与 DPI 换算）。
func (g *relGraph) nodeRadius(m *relNode) float64 {
	return m.weight * relNodeBaseDp / 2
}

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

// focusAnchor 视场居中锚点（A8：布局器输出的锚点坐标，不窥探内部结构）。「回到当前」
// 按钮走这条（只挪视场，不动 zoom）。
func (g *relGraph) focusAnchor() {
	if g.sol == nil || g.anchor < 0 || g.anchor >= len(g.sol.Pos) {
		return
	}
	p := g.sol.Pos[g.anchor]
	g.offX = g.viewW/2 - p.X*g.z
	g.offY = g.viewH/2 - p.Y*g.z
}

// fitView 开窗首帧：整棵可见树**适配视口**（A13）——按卡片外接盒算最小 zoom 并对准盒心。
// 两处实测修正（D121）：
//
//   - **fit 下限 = relZoomLabelMin**（0.75）而非 relZoomMin（0.5）：适配把树压到
//     0.5 时标签全隐、只剩裸点——「适配」出来的是一张不可读的图。仍装不下（树太大）
//     则把**锚点**对准视口中心（锚点是当前对话的焦点，盒心会落到某个分支上）。
//   - 盒子取**入场动画起终点并集**（treeBox）：动画全程不越出视野。
//
// 「回到当前」仍用 focusAnchor（A8）；数据变更**不再**重设 zoom/视口（见 frame）。
func (g *relGraph) fitView() {
	minX, minY, maxX, maxY, ok := g.treeBox()
	if !ok {
		g.focusAnchor()
		return
	}
	w, h := maxX-minX, maxY-minY
	zf := math.Min(g.viewW/w, g.viewH/h) / g.dpx // 折回用户口径的 zoom（DPI 另算）
	if floor := relZoomLabelMin; zf < floor {    // 可读下限：fit 不得压成全裸点
		zf = floor
	}
	if zf > relZoomMax {
		zf = relZoomMax
	}
	g.zoom = zf
	g.z = zf * g.dpx
	if w*g.z > g.viewW || h*g.z > g.viewH { // 夹到下限仍装不下：锚点居中（焦点优先）
		g.focusAnchor()
		return
	}
	g.offX = g.viewW/2 - (minX+maxX)/2*g.z
	g.offY = g.viewH/2 - (minY+maxY)/2*g.z
}

// recenterTree 把树拉回视野（拖后松手，D121 实测修）：**保持用户 zoom**，只把树盒心
// 对准视口中心。树太大时锚点居中（recenter 之后用户自己缩放）。
func (g *relGraph) recenterTree() {
	minX, minY, maxX, maxY, ok := g.treeBox()
	if !ok {
		g.focusAnchor()
		return
	}
	g.offX = g.viewW/2 - (minX+maxX)/2*g.z
	g.offY = g.viewH/2 - (minY+maxY)/2*g.z
}

// treeBox 树的外接盒（世界坐标 dp）。入场动画进行中取**收敛布局并集**——展开全程
// 落在 [根点, 收敛位] 线段上 ⊆ 收敛盒（根点是收敛布局的一员），并集保证动画不越界。
func (g *relGraph) treeBox() (minX, minY, maxX, maxY float64, ok bool) {
	if g.sol == nil || len(g.vis.nodes) == 0 {
		return 0, 0, 0, 0, false
	}
	minX, minY = math.Inf(1), math.Inf(1)
	maxX, maxY = math.Inf(-1), math.Inf(-1)
	add := func(li int, p relPoint) {
		hw, hh := relCardHalf(g.geo[li]) // 世界坐标下的卡片占位（不随 zoom 变）
		minX, maxX = math.Min(minX, p.X-hw), math.Max(maxX, p.X+hw)
		minY, maxY = math.Min(minY, p.Y-hh), math.Max(maxY, p.Y+hh)
	}
	for li := range g.vis.nodes {
		add(li, g.sol.Pos[li])
		if g.animOn && g.animTo != nil {
			add(li, g.animTo[li])
		}
	}
	return minX, minY, maxX, maxY, true
}

// relCardHalf 布局输入里的卡片半宽/半高（dp）。视场适配与布局共用同一份占位，
// 不另算一套（否则「按 A 排、按 B 装」）。
func relCardHalf(gn relGeoNode) (float64, float64) {
	return gn.HalfW, gn.HalfH
}

// relBadgeBox 形状徽标盒（屏幕坐标）：节点直径 = weight × 基准（A4，与 degree 无关），
// 中心锚点即节点坐标——边连的也正是它。
func relBadgeBox(xs, ys, r float32) image.Rectangle {
	return image.Rect(int(xs-r), int(ys-r), int(xs+r), int(ys+r))
}

// relLabelBoxW 标签区宽（屏幕 px）：估宽上限封顶（relLabelMaxDp × z）——超出部分由
// MaxLines=1 的省略号吃掉，避免长摘要把卡片撑成横条。换算用 **PxPerSp**（D121 实测
// 修）：labelFont 已把 zoom 烤进 sp，再乘 z（= zoom×PxPerDp）就是双重乘 zoom——
// 低 zoom 下卡比文字窄、文字被截成半句。
func (g *relGraph) relLabelBoxW(txt string) int {
	w := relEstTextW(txt, float64(g.labelFont())) * g.spx
	if max := relLabelMaxDp * g.z; w > max {
		w = max
	}
	return int(w)
}

// nodeCardBox 节点卡盒（屏幕坐标；命中判定与绘制**共用**同一几何）。
// 标签可见（A5 预算 + zoom 下限）时卡 = [徽标 | 标签]（向右展开），否则退化为裸形状盒。
func (g *relGraph) nodeCardBox(gi int, xs, ys float32) image.Rectangle {
	m := g.model.nodes[gi]
	badge := relBadgeBox(xs, ys, float32(g.nodeRadius(&m)*g.z))
	txt := relLabelText(g.model, gi, g.zoom)
	if txt == "" {
		return badge
	}
	gap := int(relCardGapDp * g.z)
	pad := int(relCardPadDp * g.z)
	pv := int(relCardPadV * g.z)
	return image.Rect(badge.Min.X-pad, badge.Min.Y-pv,
		badge.Max.X+gap+g.relLabelBoxW(txt)+pad, badge.Max.Y+pv)
}

// hitTest 屏幕点 → 命中的可见节点（本地索引；-1 无）。按节点卡盒 + relHitSlop 外扩判定
// （卡含标签，故点文字也算点中这个节点）。
func (g *relGraph) hitTest(sx, sy float32) int {
	return g.hitTestSlop(sx, sy, relHitSlop)
}

// hitTestSlop 同 hitTest，外扩量可指定（抓手判定用更宽的 relGrabSlop，见 update）。
func (g *relGraph) hitTestSlop(sx, sy float32, slopDp float64) int {
	if g.sol == nil {
		return -1
	}
	slop := int(slopDp * g.z)
	for li, gi := range g.vis.nodes {
		xs, ys := g.toScreen(g.sol.Pos[li])
		box := g.nodeCardBox(gi, xs, ys).Inset(-slop)
		if image.Pt(int(sx), int(sy)).In(box) {
			return li
		}
	}
	return -1
}

// frame 右图单帧：手势消费 → 布局续松弛 → 视场适配/居中 → 绘制 → 菜单。
func (g *relGraph) frame(gtx layout.Context, th *material.Theme, u *UI, st *historyState) layout.Dimensions {
	g.viewW, g.viewH = float64(gtx.Constraints.Max.X), float64(gtx.Constraints.Max.Y)
	g.dpx = float64(gtx.Metric.PxPerDp)
	if g.dpx <= 0 {
		g.dpx = 1
	}
	g.spx = float64(gtx.Metric.PxPerSp)
	if g.spx <= 0 {
		g.spx = 1
	}
	g.z = g.zoom * g.dpx
	// 视场调整只在这两处：拖后松手把树拉回视野（fitPending，不重置 zoom）；
	// 开窗首帧整树适配（everFit，A13）。**数据变更不碰视场**——用户的 zoom/平移
	// 是私有的，一条新消息进来不抢（D121 实测修：此前每条消息都重置 zoom/视口）。
	if g.fitPending {
		g.fitPending = false
		g.recenterTree()
	}
	if !g.everFit && g.sol != nil {
		g.everFit = true
		g.fitView()
	}
	if g.focusPending {
		g.focusPending = false
		g.focusAnchor()
	}
	g.update(gtx, u, st)
	// 拖拽/收敛中的交互态：本帧松弛后若还要继续，追加一次重绘请求（Gio 的动画口径，
	// 比 Window.Invalidate 轻；收敛即停 → R3 随即恢复）。
	if g.tickLayout() {
		gtx.Execute(op.InvalidateCmd{})
	}
	if g.sol == nil {
		return layout.Center.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			l := material.Body2(th, "关系图未就绪")
			l.Color = textMuted
			return l.Layout(gtx)
		})
	}
	g.draw(gtx, th, u, st)
	// 悬停陈述（inspect，§3.4）暂缓：实测「tooltip 太大」，等改小/改位置再接回
	// （文案派生 `relStatementOf` 与其测试仍在，恢复时只需重接 hover + drawTooltip）。
	g.drawMenu(gtx, th, u, st)
	return layout.Dimensions{Size: gtx.Constraints.Max}
}

// tickLayout 交互态松弛（D121）：拖拽中每帧带「临时固定锚」走 relFrameIter 步；
// 松手后（settling）无锚收敛；入场动画（animOn）分帧插值（不跑求解器）。
// 返回是否需要续帧——收敛/播完即 false（不烧资源，R3）。
func (g *relGraph) tickLayout() bool {
	// 入场动画（§5.3）：从根点**逐层展开**到收敛布局。级联 = 前 leadFrac 帧按层序错峰
	// 起滑（浅层先动、深层后动），后 (1-leadFrac) 帧全部滑到收敛位；每节点 ease-out。
	// 播完落定即停（R3）。直接对收敛布局插值而非跑求解器——求解器收敛太快（≈11 帧），
	// 撑不起「几秒」的口径；且对常见树（种子≈收敛）求解路径本就几乎不动。
	if g.animOn {
		g.animFrame++
		if g.animFrame >= g.animFrames {
			g.animOn = false
			g.sol = &relSolution{Pos: g.animTo}
			g.animTo = nil
			return false
		}
		lead := int(float64(g.animFrames) * relAnimLeadFrac)
		slide := g.animFrames - lead
		if slide < 1 {
			slide = 1
		}
		maxLv := 0
		for _, gn := range g.geo {
			if gn.Level > maxLv {
				maxLv = gn.Level
			}
		}
		pos := make([]relPoint, len(g.animTo))
		for li := range pos {
			start := 0
			if maxLv > 0 {
				start = int(float64(g.geo[li].Level) / float64(maxLv) * float64(lead))
			}
			t := float64(g.animFrame-start) / float64(slide)
			switch {
			case t <= 0:
				pos[li] = g.animOrigin
			case t >= 1:
				pos[li] = g.animTo[li]
			default:
				t = 1 - (1-t)*(1-t)*(1-t) // ease-out cubic
				pos[li].X = g.animOrigin.X + (g.animTo[li].X-g.animOrigin.X)*t
				pos[li].Y = g.animOrigin.Y + (g.animTo[li].Y-g.animOrigin.Y)*t
			}
		}
		g.sol = &relSolution{Pos: pos}
		return true
	}
	if g.lay == nil || (g.dragNode < 0 && !g.settling) {
		return false
	}
	if g.dragNode >= 0 {
		// 抓取偏移每帧衰减（D121 实测修：光标与节点不许错位）：起拖第一帧节点还停在
		// 按下时的相对位置（零跳变），随后中心**平滑滑到光标正下方**并锁死跟手。
		g.dragGrabX *= relGrabDecay
		g.dragGrabY *= relGrabDecay
		if math.Abs(float64(g.dragGrabX)) < 0.5 && math.Abs(float64(g.dragGrabY)) < 0.5 {
			g.dragGrabX, g.dragGrabY = 0, 0
		}
	}
	pin := relPin{}
	if g.dragNode >= 0 {
		pin = relPin{Valid: true, Index: g.dragNode, Pos: g.dragPinWorld()}
	}
	_, settled := g.lay.relax(relFrameIter, pin)
	g.sol = g.lay.solution()
	g.relaxLeft--
	more := !settled && g.relaxLeft > 0
	if g.dragNode >= 0 && (g.dragGrabX != 0 || g.dragGrabY != 0) {
		more = true // 抓取偏移未衰减完：继续续帧（节点在滑向光标，指针停住也要动）
	}
	if g.dragNode < 0 {
		g.settling = more // 松手后收敛：收敛即停（R3）
	}
	return more
}

// grabAt 记录抓取偏移（指针相对节点中心的偏移，**按下那一刻**测）。它不恒定保持：
// 每帧衰减（tickLayout 里 relGrabDecay）——节点中心在起拖后 ~120ms 内平滑滑到光标
// 正下方并锁死（D121 实测修：光标与节点不许错位；直接清零则标签区起拖会瞬跳）。
func (g *relGraph) grabAt(li int, sx, sy float32) {
	if li < 0 || li >= len(g.sol.Pos) {
		return
	}
	xs, ys := g.toScreen(g.sol.Pos[li])
	g.dragGrabX, g.dragGrabY = sx-xs, sy-ys
}

// dragPinWorld 被拖节点的世界坐标（抓取点保持的残余偏移：衰减中的偏差让中心滑向光标）。
func (g *relGraph) dragPinWorld() relPoint {
	sx := float64(g.dragPX - g.dragGrabX)
	sy := float64(g.dragPY - g.dragGrabY)
	return relPoint{X: (sx - g.offX) / g.z, Y: (sy - g.offY) / g.z}
}

// update 手势状态机：平移 / 拖节点（D121）/ 缩放（gesture.Scroll）/ 悬停 / 点击 / 右键菜单。
func (g *relGraph) update(gtx layout.Context, u *UI, st *historyState) {
	// 滚轮：范围放开（主窗转写区的 D71 钉点口径不适用本窗，§10.2）。
	if d := g.scroll.Update(gtx.Metric, gtx.Source, gtx.Now, gesture.Vertical,
		pointer.ScrollRange{}, pointer.ScrollRange{Min: -relWheelMax, Max: relWheelMax}); d != 0 {
		g.wheel(d)
	}
	for {
		ev, ok := gtx.Event(pointer.Filter{
			Target: &g.tag,
			Kinds: pointer.Press | pointer.Release | pointer.Move | pointer.Drag |
				pointer.Enter | pointer.Leave | pointer.Cancel,
		})
		if !ok {
			break
		}
		e := ev.(pointer.Event)
		switch e.Kind {
		case pointer.Leave, pointer.Cancel:
			if e.Kind == pointer.Cancel {
				g.releaseDrag() // 指针被取消（拖出窗口/失焦）：锚撤销，回归自由
				g.pressed = false
				g.pressNode = -1
				g.grabNode = -1
				g.dragging = false
			}
		case pointer.Scroll:
			g.wheel(int(e.Scroll.Y))
		case pointer.Press:
			g.cancelEntryAnim() // 任何按下即打断入场动画（不与用户抢控制权）
			if e.Buttons.Contain(pointer.ButtonSecondary) {
				if h := g.hitTest(e.Position.X, e.Position.Y); h >= 0 {
					g.menuOpen = true
					g.menuNode = h
					g.menuX, g.menuY = e.Position.X, e.Position.Y
				}
				continue
			}
			if e.Buttons.Contain(pointer.ButtonPrimary) {
				if g.menuOpen {
					// 点菜单项（矩形内）→ 交给 Clickable 处理；菜单外 → 关闭。
					if e.Position.X >= g.menuX && e.Position.X <= g.menuX+float32(gtx.Dp(120)) &&
						e.Position.Y >= g.menuY && e.Position.Y <= g.menuY+float32(gtx.Dp(26)*3) {
						continue
					}
					g.menuOpen = false
					continue
				}
				g.pressed = true
				g.pressNode = g.hitTest(e.Position.X, e.Position.Y)
				// 抓手判定用**更宽的半径**（D121 实测修）：低 zoom 下标签隐藏、卡片塌缩成
				// 小徽标，严格命中极易差几像素落空，「拖节点」就静默退化成平移——最糟的
				// 失败方式（用户以为在拖节点，实际在挪视口）。抓手在**按下那一刻**定，
				// 拖动途中不再改判（否则拖过节点会被半路劫走）。
				g.grabNode = g.hitTestSlop(e.Position.X, e.Position.Y, relGrabSlop)
				if g.grabNode < 0 {
					g.grabNode = g.pressNode
				}
				g.panStartOffX, g.panStartOffY = g.offX, g.offY
				g.panStartX, g.panStartY = e.Position.X, e.Position.Y
				g.dragging = false
				if g.grabNode >= 0 { // 抓取偏移此刻测（节点才在指下）
					g.grabAt(g.grabNode, e.Position.X, e.Position.Y)
				}
			}
		case pointer.Drag:
			if g.pressed {
				dx := float64(e.Position.X - g.panStartX)
				dy := float64(e.Position.Y - g.panStartY)
				slop := relDragSlop * g.z // 阈值走有效缩放（曾漏乘：125% DPI 下 4dp 只剩 3.2dp）
				if !g.dragging && dx*dx+dy*dy > slop*slop {
					g.dragging = true
					if g.grabNode >= 0 { // 按在节点上 = 拖节点（不进平移）
						g.dragNode = g.grabNode
						g.relaxLeft = relSettleFrames
					}
				}
				if g.dragNode >= 0 {
					g.dragPX, g.dragPY = e.Position.X, e.Position.Y
				} else if g.dragging {
					g.offX = g.panStartOffX + dx
					g.offY = g.panStartOffY + dy
				}
			}
		case pointer.Release:
			if g.pressed {
				g.pressed = false
				if g.dragNode >= 0 {
					g.releaseDrag() // 拖过 = 移动节点，不投 /goto
				} else if !g.dragging && g.pressNode >= 0 &&
					g.pressNode == g.hitTest(e.Position.X, e.Position.Y) {
					g.gotoNode(u, st, g.pressNode)
				}
				g.pressNode = -1
				g.grabNode = -1
				g.dragging = false
			}
		}
	}
	event.Op(gtx.Ops, &g.tag)
	g.scroll.Add(gtx.Ops)
}

// releaseDrag 松手/取消：撤销临时固定锚，节点恢复自由并自然收敛（D121/A12），
// 随后把树拉回视野（fitPending → recenterTree，D121 实测修：拖开摊宽的树不丢）。
func (g *relGraph) releaseDrag() {
	if g.dragNode < 0 {
		return
	}
	g.dragNode = -1
	g.relaxLeft = relSettleFrames
	g.settling = true
	g.fitPending = true // 松手后重定视野（保持 zoom，只把树盒心对准视口）
}

// gotoNode 点节点 → 投 /goto <id>（A10；与键入同路径串行执行，缓冲满丢弃）。
func (g *relGraph) gotoNode(u *UI, st *historyState, li int) {
	if g.model == nil || li < 0 || li >= len(g.vis.nodes) {
		return
	}
	id := g.model.nodes[g.vis.nodes[li]].id
	select {
	case u.inCh <- port.UserInput{Command: &port.Command{Name: "goto", Args: []string{string(id)}}}:
	default:
	}
}

// draw 一帧：边（source 色 + 线型）→ 节点（形状/填充/描边）→ 标签（预算）。
// 视口 clip（D121 实测修）：剔除盒外扩 20%，那些节点照画不误——没有 clip 时
// 负坐标节点会盖到左栏会话列表上。
func (g *relGraph) draw(gtx layout.Context, th *material.Theme, u *UI, st *historyState) {
	defer clip.Rect(image.Rectangle{Max: image.Pt(int(g.viewW), int(g.viewH))}).Push(gtx.Ops).Pop()
	vp := image.Rect(
		-int(g.viewW*relCullPad), -int(g.viewH*relCullPad),
		int(g.viewW*(1+relCullPad)), int(g.viewH*(1+relCullPad)))
	// 全局 → 本地索引映射：relVisible.edges 存全局索引，sol.Pos 按本地（见 §6.1 两套索引）。
	local := make(map[int]int, len(g.vis.nodes))
	for li, gi := range g.vis.nodes {
		local[gi] = li
	}
	for _, e := range g.vis.edges {
		from, to := e[0], e[1] // 全局索引
		fli, lok1 := local[from]
		tli, lok2 := local[to]
		if !lok1 || !lok2 {
			continue // 端点不在可见集（预算裁剪），跳过
		}
		fx, fy := g.toScreen(g.sol.Pos[fli])
		tx, ty := g.toScreen(g.sol.Pos[tli])
		if !relInBox(vp, fx, fy) && !relInBox(vp, tx, ty) {
			continue
		}
		fill := depthMap[relPaletteIndex(g.model.nodes[from].level)] // 边色 = source 填充色
		// 主干（A11）改由**边描边 + 本色加粗**表达：先铺一条窗底色的粗线当描边（把主干
		// 从背景与相邻卡片里抬出来），再画本色细线。节点上不再套白圈。
		path := g.model.nodes[from].inPath && g.model.nodes[to].inPath
		if path {
			g.strokeLineW(gtx, fx, fy, tx, ty, th.Bg, relEdgeW*relEdgeHalo*float32(g.z))
		}
		g.drawEdge(gtx, fx, fy, tx, ty, fill,
			g.model.nodes[to].edgeKind == port.EdgeRevise, path)
	}
	for li, gi := range g.vis.nodes {
		xs, ys := g.toScreen(g.sol.Pos[li])
		if !relInBox(vp, xs, ys) {
			continue
		}
		g.drawNodeCard(gtx, th, gi, xs, ys)
	}
}

// relCardTint 节点卡底色 = 节点层色向**窗底色**靠拢（纯函数）。卡片因此「跟着节点走」：
// 浅色主题下是同色系淡彩，深色主题下自动变深（同一个公式，两版都对）。
func relCardTint(fill, bg color.NRGBA) color.NRGBA {
	mix := func(f, b uint8) uint8 {
		return uint8(float64(f)*relCardTintF + float64(b)*(1-relCardTintF))
	}
	return color.NRGBA{R: mix(fill.R, bg.R), G: mix(fill.G, bg.G), B: mix(fill.B, bg.B), A: 0xFF}
}

// drawNodeCard 节点卡（D121「内容入内」）：标签可见时 = 卡片底 + 形状徽标（Role 四形 ×
// Level 色，A2/§3.1 不变）+ 卡内标签（MaxLines=1，估宽内排版、超出走省略号）；
// 标签不可见（A5：低 zoom / 低 weight）时退化为「裸形状」——标签仍是预算，卡不是。
func (g *relGraph) drawNodeCard(gtx layout.Context, th *material.Theme, gi int, xs, ys float32) {
	m := g.model.nodes[gi]
	r := float32(g.nodeRadius(&m) * g.z)
	txt := relLabelText(g.model, gi, g.zoom)
	if txt == "" {
		g.drawNode(gtx, th, g.model, gi, xs, ys, r)
		return
	}
	box := g.nodeCardBox(gi, xs, ys)
	fill := depthMap[relPaletteIndex(m.level)]
	paint.FillShape(gtx.Ops, relCardTint(fill, th.Bg),
		clip.UniformRRect(box, int(relCardRoundP*r)).Op(gtx.Ops))
	g.drawNode(gtx, th, g.model, gi, xs, ys, r)
	// 标签：徽标右侧、卡片内垂直居中。
	badge := relBadgeBox(xs, ys, r)
	tw := g.relLabelBoxW(txt)
	off := op.Offset(image.Pt(badge.Max.X+int(relCardGapDp*g.z), badge.Min.Y-int(relCardPadV*g.z))).Push(gtx.Ops)
	gtx.Constraints.Min = image.Pt(0, 0)
	gtx.Constraints.Max = image.Pt(tw, badge.Dy()+2*int(relCardPadV*g.z))
	layout.Center.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		gtx.Constraints.Max.X = tw
		l := material.Label(th, unit.Sp(g.labelFont()), txt)
		l.Color = th.Fg
		l.MaxLines = 1 // 单行：卡片高只容一行，超出走省略号
		return l.Layout(gtx)
	})
	off.Pop()
}

// labelFont 标签字号 = clamp(12×zoom, 10, 16)sp（A9 排版硬约束）。
func (g *relGraph) labelFont() float32 {
	sp := relLabelBase * g.zoom
	if sp < relLabelMinSp {
		sp = relLabelMinSp
	}
	if sp > relLabelMaxSp {
		sp = relLabelMaxSp
	}
	return float32(sp)
}

// drawEdge 边：source 色；版本链 = 虚线段（§2.3）；末端箭头 = 对话推进方向（A3）。
// path = 该边在主干上（`InPath` 两端）时本色线加粗一档（A11，与描边叠加）。
func (g *relGraph) drawEdge(gtx layout.Context, ax, ay, bx, by float32, fill color.NRGBA, revise, path bool) {
	dx, dy := bx-ax, by-ay
	dist := float32(math.Hypot(float64(dx), float64(dy)))
	if dist < 1 {
		return
	}
	ux, uy := dx/dist, dy/dist
	w := relEdgeW * float32(g.z)
	if path {
		w *= relEdgePathW
	}
	if revise {
		step := float32(relDashOn+relDashOff) * float32(g.z)
		n := int(dist / step)
		for k := 0; k <= n; k++ {
			s := float32(k) * step
			e := s + float32(relDashOn)*float32(g.z)
			if e > dist {
				e = dist
			}
			g.strokeLineW(gtx, ax+ux*s, ay+uy*s, ax+ux*e, ay+uy*e, fill, w)
		}
	} else {
		g.strokeLineW(gtx, ax, ay, bx, by, fill, w)
	}
	g.arrowHead(gtx, ax, ay, bx, by, fill)
}

// strokeLine 直线段（细描边，默认边宽）。
func (g *relGraph) strokeLine(gtx layout.Context, ax, ay, bx, by float32, c color.NRGBA) {
	g.strokeLineW(gtx, ax, ay, bx, by, c, relEdgeW*float32(g.z))
}

// strokeLineW 直线段（指定线宽）：主干描边 = 先用窗底色铺一条更粗的线，再压本色细线。
func (g *relGraph) strokeLineW(gtx layout.Context, ax, ay, bx, by float32, c color.NRGBA, w float32) {
	var p clip.Path
	p.Begin(gtx.Ops)
	p.MoveTo(f32.Pt(ax, ay))
	p.LineTo(f32.Pt(bx, by))
	paint.FillShape(gtx.Ops, c, clip.Stroke{Path: p.End(), Width: w}.Op())
}

// arrowHead 子端小三角（父 → 子方向）。
func (g *relGraph) arrowHead(gtx layout.Context, ax, ay, bx, by float32, fill color.NRGBA) {
	dx, dy := bx-ax, by-ay
	dist := float32(math.Hypot(float64(dx), float64(dy)))
	if dist < 1 {
		return
	}
	ux, uy := dx/dist, dy/dist
	px, py := -uy, ux                         // 垂直方向
	s := float32(relArrowSize) * float32(g.z) // 有效缩放（含 DPI；曾误用 g.zoom）
	tip := f32.Pt(bx, by)
	base := f32.Pt(bx-ux*s, by-uy*s)
	var p clip.Path
	p.Begin(gtx.Ops)
	p.MoveTo(tip)
	p.LineTo(f32.Pt(base.X+px*s*0.5, base.Y+py*s*0.5))
	p.LineTo(f32.Pt(base.X-px*s*0.5, base.Y-py*s*0.5))
	p.Close()
	paint.FillShape(gtx.Ops, fill, clip.Outline{Path: p.End()}.Op())
}

// drawNode 节点徽标：填充（Level mod 4 深度色带）+ 形状（Role）+ 版本下标 +
// 分叉虚线外环（沿形状）。形状画在 2r×2r 的盒里，圆/方/菱形贴边、空心圆改用**描边**
// （此前「填实心 + 内填窗底」在节点卡上会露出底色错位的洞）。主干（A11）不在节点上。
func (g *relGraph) drawNode(gtx layout.Context, th *material.Theme, m *relModel, gi int, xs, ys, r float32) {
	fill := depthMap[relPaletteIndex(m.nodes[gi].level)]
	shape := relShapeOf(m.nodes[gi].role)
	box := image.Rect(int(xs-r), int(ys-r), int(xs+r), int(ys+r))
	switch shape {
	case relShapeSquare:
		paint.FillShape(gtx.Ops, fill, clip.UniformRRect(box, int(r*0.28)).Op(gtx.Ops))
	case relShapeDiamond:
		g.diamond(gtx, xs, ys, r, fill)
	case relShapeRing: // 空心圆（结构根）：描边而非挖洞
		ring := clip.UniformRRect(box, int(r)).Path(gtx.Ops)
		paint.FillShape(gtx.Ops, fill,
			clip.Stroke{Path: ring, Width: float32(relRingW*1.6) * float32(g.z)}.Op())
	default: // circle
		paint.FillShape(gtx.Ops, fill, clip.UniformRRect(box, int(r)).Op(gtx.Ops))
	}
	// 版本下标写进**图形内部**（D121）：菱形中心宽度为 0、空心圆无底，都放不下字——
	// 那两种形状不落字（分叉位点几乎都是 user/assistant，够用）。
	if shape != relShapeDiamond && shape != relShapeRing {
		if glyph := relVersionGlyph(m, gi); glyph != "" {
			g.drawGlyph(gtx, th, xs, ys, r, glyph)
		}
	}
	// 主干（A11）由**边**表达（描边 + 本色加粗，见 draw）：白圈会把形状切出白口、
	// 且在卡片里读作「贴纸边框」。分叉位点的虚线外环保留（另一条通道），且
	// **沿本节点自身形状**走（D121：圆节点套方虚线框读作两套语言）。
	if m.nodes[gi].sibCount > 1 {
		g.dashedOutline(gtx, xs, ys, r+float32(relRingW)*float32(g.z), shape)
	}
}

// drawGlyph 版本下标：白字居中于形状内（字号随半径缩放，小节点也不糊成一坨）。
func (g *relGraph) drawGlyph(gtx layout.Context, th *material.Theme, xs, ys, r float32, txt string) {
	badge := relBadgeBox(xs, ys, r*0.82)
	sp := math.Max(8, float64(r)*0.95)
	off := op.Offset(badge.Min).Push(gtx.Ops)
	gtx.Constraints.Min = image.Pt(0, 0)
	gtx.Constraints.Max = badge.Size()
	layout.Center.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		gtx.Constraints.Max.X = badge.Dx()
		l := material.Label(th, unit.Sp(sp), txt)
		l.Color = whiteText
		l.MaxLines = 1
		return l.Layout(gtx)
	})
	off.Pop()
}

// diamond 菱形（system：人格 / 压缩摘要）。半对角取 0.78r：满格菱形的尖会顶出卡片
// 内边距，看起来像被切了一刀。
func (g *relGraph) diamond(gtx layout.Context, xs, ys, r float32, fill color.NRGBA) {
	d := r * 0.78
	var p clip.Path
	p.Begin(gtx.Ops)
	p.MoveTo(f32.Pt(xs, ys-d))
	p.LineTo(f32.Pt(xs+d, ys))
	p.LineTo(f32.Pt(xs, ys+d))
	p.LineTo(f32.Pt(xs-d, ys))
	p.Close()
	paint.FillShape(gtx.Ops, fill, clip.Outline{Path: p.End()}.Op())
}

// dashedOutline 分叉位点的虚线外环，**沿节点自身形状**走（D121）：圆走圆、方走圆角
// 方、菱形走菱——圆节点套方虚线框会读成两套语言。
func (g *relGraph) dashedOutline(gtx layout.Context, xs, ys, r float32, shape relShape) {
	step := (relDashOn + relDashOff) * float64(g.z)
	stroke := relEdgeW * float32(g.z)
	pts := shapeOutline(shape, xs, ys, r, step)
	for i := 0; i+1 < len(pts); i += 2 {
		g.strokeLineW(gtx, pts[i].X, pts[i].Y, pts[i+1].X, pts[i+1].Y, textMuted, stroke)
	}
}

// shapeOutline 形状轮廓的折线采样（步长 step；虚段 = 一步、间隔 = 一步，故成对取点）。
func shapeOutline(shape relShape, xs, ys, r float32, step float64) []f32.Point {
	var pts []f32.Point
	switch shape {
	case relShapeDiamond:
		v := [4]f32.Point{
			{X: xs, Y: ys - r}, {X: xs + r, Y: ys}, {X: xs, Y: ys + r}, {X: xs - r, Y: ys},
		}
		for i := 0; i < 4; i++ {
			pts = relSampleSeg(pts, v[i], v[(i+1)%4], step)
		}
	case relShapeSquare:
		cr := r * 0.28 // 与填充的圆角半径同值
		c := [4]f32.Point{
			{X: xs + r - cr, Y: ys - r}, {X: xs + r - cr, Y: ys + r},
			{X: xs - r + cr, Y: ys + r}, {X: xs - r + cr, Y: ys - r},
		}
		cen := [4]f32.Point{
			{X: xs + r - cr, Y: ys - r + cr}, {X: xs + r - cr, Y: ys + r - cr},
			{X: xs - r + cr, Y: ys + r - cr}, {X: xs - r + cr, Y: ys - r + cr},
		}
		for i := 0; i < 4; i++ { // 圆角：每角按步长采样
			pts = relSampleArc(pts, cen[i], cr, -math.Pi/2+float64(i)*math.Pi/2, float64(i)*math.Pi/2, step)
			pts = relSampleSeg(pts, c[i], c[(i+1)%4], step)
		}
	default: // circle / ring
		n := relOutlineSegs // 固定段数：步长过小时不至于采样出上万点
		per := 2 * math.Pi / float64(n)
		for i := 0; i < n; i++ {
			a := float64(i) * per
			p0 := f32.Pt(xs+r*float32(math.Cos(a)), ys+r*float32(math.Sin(a)))
			a1 := a + per/2
			p1 := f32.Pt(xs+r*float32(math.Cos(a1)), ys+r*float32(math.Sin(a1)))
			pts = append(pts, p0, p1)
		}
	}
	return pts
}

// relSampleSeg 直线段按步长采样（虚段/间隔交替成对取点）。
func relSampleSeg(pts []f32.Point, a, b f32.Point, step float64) []f32.Point {
	d := math.Hypot(float64(b.X-a.X), float64(b.Y-a.Y))
	n := int(d / step)
	if n < 1 {
		n = 1
	}
	for i := 0; i < n; i++ {
		t0, t1 := float64(2*i)/float64(2*n), float64(2*i+1)/float64(2*n)
		p0 := f32.Pt(a.X+float32(t0)*(b.X-a.X), a.Y+float32(t0)*(b.Y-a.Y))
		p1 := f32.Pt(a.X+float32(t1)*(b.X-a.X), a.Y+float32(t1)*(b.Y-a.Y))
		pts = append(pts, p0, p1)
	}
	return pts
}

// relSampleArc 圆弧按步长采样（用于圆角方块的四个角）。
func relSampleArc(pts []f32.Point, c f32.Point, r float32, from, to, step float64) []f32.Point {
	sweep := math.Abs(to - from)
	if sweep <= 0 {
		return pts
	}
	n := int(sweep * float64(r) / step)
	if n < 1 {
		n = 1
	}
	for i := 0; i < n; i++ {
		t0 := from + sweep*float64(2*i)/float64(2*n)
		t1 := from + sweep*float64(2*i+1)/float64(2*n)
		pts = append(pts,
			f32.Pt(c.X+r*float32(math.Cos(t0)), c.Y+r*float32(math.Sin(t0))),
			f32.Pt(c.X+r*float32(math.Cos(t1)), c.Y+r*float32(math.Sin(t1))))
	}
	return pts
}

// drawMenu 右键菜单：复制 ID / 删除 / 回到当前。点击判定须**先于绘制**（Clickable 的
// Layout 也会消费程序化点击 requestClicks，先判后绘才可靠）；命中即就地关闭。
func (g *relGraph) drawMenu(gtx layout.Context, th *material.Theme, u *UI, st *historyState) {
	if !g.menuOpen {
		return
	}
	doCopy := g.menuCopy.Clicked(gtx)
	doRm := g.menuRm.Clicked(gtx)
	doHome := g.menuHome.Clicked(gtx)

	x, y := int(g.menuX), int(g.menuY)
	off := op.Offset(image.Pt(x, y)).Push(gtx.Ops)
	layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return g.menuItem(gtx, th, &g.menuCopy, "复制 ID")
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return g.menuItem(gtx, th, &g.menuRm, "删除（/rm）")
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return g.menuItem(gtx, th, &g.menuHome, "回到当前")
		}),
	)
	off.Pop()

	id := ""
	if g.menuNode >= 0 && g.menuNode < len(g.vis.nodes) && g.model != nil {
		id = string(g.model.nodes[g.vis.nodes[g.menuNode]].id)
	}
	switch {
	case doCopy:
		if id != "" {
			gtx.Execute(clipboard.WriteCmd{Type: "application/text",
				Data: io.NopCloser(strings.NewReader(id))})
		}
		g.menuOpen = false
	case doRm:
		if id != "" {
			select {
			case u.inCh <- port.UserInput{Command: &port.Command{Name: "rm", Args: []string{id}}}:
			default:
			}
		}
		g.menuOpen = false
	case doHome:
		g.focusPending = true
		g.menuOpen = false
	}
}

// menuItem 菜单行（clickable + 文案，悬停底色）。
func (g *relGraph) menuItem(gtx layout.Context, th *material.Theme, c *widget.Clickable, text string) layout.Dimensions {
	return c.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return layout.Stack{}.Layout(gtx,
			layout.Expanded(func(gtx layout.Context) layout.Dimensions {
				bg := pillBg
				if c.Hovered() {
					bg = cardSystem
				}
				paint.FillShape(gtx.Ops, bg, clip.RRect{Rect: image.Rectangle{Max: gtx.Constraints.Max},
					SE: 4, SW: 4, NE: 4, NW: 4}.Op(gtx.Ops))
				return layout.Dimensions{Size: image.Pt(gtx.Dp(120), gtx.Dp(26))}
			}),
			layout.Stacked(func(gtx layout.Context) layout.Dimensions {
				return layout.UniformInset(unit.Dp(4)).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					l := material.Body2(th, text)
					l.Color = th.Fg
					return l.Layout(gtx)
				})
			}),
		)
	})
}

// relInBox 屏幕点是否在视口盒内（float32 版）。
func relInBox(box image.Rectangle, x, y float32) bool {
	return x >= float32(box.Min.X) && x <= float32(box.Max.X) &&
		y >= float32(box.Min.Y) && y <= float32(box.Max.Y)
}
