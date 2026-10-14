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
	"math"

	"gioui.org/f32"
	"gioui.org/gesture"
	"gioui.org/io/event"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"
	"gioui.org/widget/material"

	"github.com/Tonyjh07/Aquarius/internal/domain/conversation"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

// 右图档位（偶然实现，§7 可调；zoom 边界即 A9 的钳制区间）。
const (
	// relZoomMin / relZoomMax zoom 钳制区间（A9）。**D130 放宽**：原 [0.5, 1.5] 是源实现
	// `scaleLimit` 的偶然档位，实测堵死了「按树的大小动态适配」这条路——fit 算出 ~0.55 想
	// 把整树装进视口，被 0.5 一挡（外加 0.75 的标签可见下限）就装不下，用户看到的是
	// 「内容出视口 + 左侧大片空白」。现放到 [0.1, 2.0]：一屏能看整棵树，也能放大到逐字读。
	// **可读性不靠 zoom 下限，靠两条既有机制**：字号钳 [relLabelMinSp, relLabelMaxSp]；
	// 标签预算 relLabelVisible（zoom 低于 relZoomLabelMin 时标签全隐 ⇒ overview 只剩徽标
	// 与边）。卡片预留 HalfW/HalfH 仍按 relZoomLabelMin 的最坏口径算，与「低于该 zoom 不画
	// 标签」自洽。
	relZoomMin    = 0.1
	relZoomMax    = 2.0
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
	// relLabelLines 标签最大行数（D126「多行占满卡片」）：摘要换行到多行、占满卡片，
	// 不再一行省略号。上限 3 行 = 卡片内容高 ≈ 3 × 行高 + 内边距 ≈ 64dp < 兄弟槽距
	// （卡片 pitch，≥ relRestLen = 90dp）—— 卡片不会顶到上/下一张；再多数不过来，
	// 且静息布局的槽距兜不住。
	relLabelLines = 3
	// relLabelLineLead 行高系数（sp）：LineHeight = 字号 × 本系数。**显式钉住**（不依赖
	// 字体默认度量）⇒ 卡片高度可按「行数 × 行高」估算，与布局预留同口径。
	relLabelLineLead = 1.25
	// relLabelEmDp / relLabelLineHDp 标签块的**最坏缩放**口径（世界 dp）：zoom 下限
	// （relZoomLabelMin = 0.75）处字号被钳到 10sp 而标签盒宽随 z 线性缩小 ⇒ 每行字数
	// 最少（em 最宽）、行高最大。布局（HalfW/HalfH，relGeo）按此上界预留 ⇒ 任何 zoom 下
	// 文本都装得进自己的卡（R3：缩放不改布局）。推导：em = 10sp ÷ 0.75 ≈ 13.3dp。
	relLabelEmDp    = 13.4
	relLabelLineHDp = 16.7
	// relSettleFrames 物理续帧硬顶（帧）：常见树 ~1–2s（≤180 帧）内落定；深链退化形态
	// 需更长（实测 chain240 ≈ 600 帧——节点锚废除后深链的「手风琴模式」收敛时间 ∝ n²，
	// 靠弹簧 6.0 + 12 子步压住），触顶即停防病态输入下无限续帧（交互态也守 R3 的
	// 「稳定态不烧资源」）。续帧期间每帧成本 ~0.2ms（200 节点、12 子步），可接受。
	relSettleFrames = 900
	// relFitMargin 视场适配/居中的树盒外扩比例：物理平衡态比静息布局略宽（斥力把兄弟
	// 顶开一点），留点余量免得贴边被裁。
	relFitMargin = 0.06
	// relAnchorDatumX Head（锚点）的**视场右基准位**（D127 时间轴横树；视宽比例）：
	// 开窗首帧与「回到当前」都把 Head 放到这里——「现在」钉在右侧，左侧全留给过去。
	// A8 的 RSM「视口原点」在树上的落实：Head 是路径终点不是几何中心，故基准点而非原点。
	relAnchorDatumX = 0.72
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
	// fitFollow fit 回退（树太大装不下视口）时的**视图跟随锚点**（D126 实测修）：每帧
	// 把锚点当前位对准视口中心，收敛即停。深链展开要几秒到十几秒（手风琴模式），一
	// 次性按静息位对准会把当前消息甩出视口（实测 200 节点链偏 1900px）。用户自己
	// 平移/缩放（wheel / pan）即取消——不跟用户抢视场。
	fitFollow bool

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
	dragPX    float32 // 最新指针位置（帧层物理步进用）
	dragPY    float32
	relaxLeft int // 物理续帧预算（硬顶，防病态输入下无限续帧）

	// 右键菜单（D132：并入气泡菜单——按下武装、同指针原位抬起 = 请求菜单，项集与
	// 分发都在 menu.go；不再自绘 Gio 浮层，与主界面同一呈现）。
	menuArm  bool // 右键按下已武装（按在节点上）
	menuNode int  // 武装时的节点（本地索引；-1 = 按在空白处）

	// slots 手动摆放的槽位（D126「不锚定节点 ⇒ 自由移动」）：松手时把被拖节点的当前
	// 位置**写回**静息布局（lay.adoptSlot），树按新形状收敛、不再弹回旧形状。按消息 ID
	// 记 ⇒ rebuild（新消息到来 / rebudget）后按 ID 恢复，用户的排列不被自动布局冲掉。
	slots map[conversation.MessageID]relSlot
}

// relSlot 手动槽位（帧层按消息 ID 记，与 relmap_layout 的 setSlot 对接；D127 横树）。
// 非根节点：Sib = 相对父的**纵向**兄弟槽偏移（y）、Gap = 相对父的**横向**层距（x，世界 dp）。
// 结构根没有父：Root = true，OffX/OffY = 整树平移量（拖动根 = 拖着整树走）。
type relSlot struct {
	Sib        float64
	OffX, OffY float64
	Gap        float64
	Root       bool
}

// newRelGraph 构造右图帧态（默认 zoom = 1.0；首帧数据适配整树，见 frame 的 everFit）。
func newRelGraph() *relGraph {
	return &relGraph{zoom: 1.0, z: 1, dpx: 1, spx: 1, hover: -1, pressNode: -1,
		grabNode: -1, dragNode: -1, tag: 1, relaxLeft: relSettleFrames}
}

// rebuild 数据变更 / rebudget 时重排（R3）：派生几何 → 新物理布局 → 播种 → 重置交互态。
//
// **开窗首帧**（everFit 未置位）折叠播种（从根点收缩态物理展开）；此后数据变更**按节点
// ID 保留旧位置**（新节点从父位弹出）——增量形态变化，不整树瞬移；**手动摆放的槽位也按
// ID 保留**（D126：用户拖过的节点不被自动布局冲掉）。视场只在开窗首帧适配（A13）与拖后
// 松手时调整（releaseDrag）；数据变更不碰用户的 zoom/平移（D121）。
func (g *relGraph) rebuild(m *relModel, vis relVisible) {
	old := map[conversation.MessageID]relPoint{}
	if g.model != nil && g.sol != nil {
		for li, gi := range g.vis.nodes {
			if gi < len(g.model.nodes) && li < len(g.sol.Pos) {
				old[g.model.nodes[gi].id] = g.sol.Pos[li]
			}
		}
	}
	fresh := !g.everFit // 开窗首帧 → 折叠物理展开
	g.model = m
	g.vis = vis
	g.geo, g.anchor = relGeo(m, vis)
	if g.anchor < 0 && len(g.geo) > 0 {
		g.anchor = 0
	}
	if lay, ok := newRelLayout(g.geo); ok {
		g.lay = lay
		if fresh {
			lay.seedCollapsed()
		} else {
			lay.setPositions(g.preservedSeed(old))
			g.restoreSlots(m)
		}
		g.sol = lay.solution()
	} else {
		g.lay = nil
		g.sol = &relSolution{Pos: make([]relPoint, len(g.geo))}
	}
	g.relaxLeft = relSettleFrames
	g.hover = -1
	g.pressed = false
	g.pressNode = -1
	g.grabNode = -1
	g.dragNode = -1
	g.menuArm = false
	g.menuNode = -1
}

// restoreSlots 把手动槽位按消息 ID 套回新布局（D126）：新图里还在的节点保持用户摆过的
// 位置，已消失的节点槽位作废（顺手清map，防长期累积）。
func (g *relGraph) restoreSlots(m *relModel) {
	if len(g.slots) == 0 || g.lay == nil {
		return
	}
	for id, s := range g.slots {
		gi, ok := m.index[id]
		if !ok {
			delete(g.slots, id)
			continue
		}
		li := -1
		for k, v := range g.vis.nodes {
			if v == gi {
				li = k
				break
			}
		}
		if li < 0 { // 被预算裁掉：槽位还记着（展开全部时要用）
			continue
		}
		g.lay.setSlot(li, s)
	}
}

// preservedSeed 增量播种：按消息 ID 复用旧位置；新节点（旧图没有）从**父位置**弹出，
// 父也无则退回静息位。沿 BFS 序推进，保证父先于子确定。
func (g *relGraph) preservedSeed(old map[conversation.MessageID]relPoint) []relPoint {
	n := len(g.geo)
	seed := make([]relPoint, n)
	have := make([]bool, n)
	for li := range g.geo {
		gi := g.vis.nodes[li]
		if gi >= len(g.model.nodes) {
			continue
		}
		if p, ok := old[g.model.nodes[gi].id]; ok {
			seed[li], have[li] = p, true
		}
	}
	rest := g.lay.restPos()
	for _, li := range g.lay.order {
		if have[li] {
			continue
		}
		if p := g.geo[li].Parent; p >= 0 && have[p] {
			seed[li] = seed[p]
		} else {
			seed[li] = rest[li]
		}
		have[li] = true
	}
	return seed
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
// relAnchorDatumX 处（「现在」在右），纵向仍居中。「回到当前」按钮走这条（只挪视场，
// 不动 zoom）。基准 = **当前位形**的锚点位：点它的时候树已经静止，用户要看的正是那个
// 节点此刻在哪（物理位形与静息布局有接触推力造成的残差，按静息位对准会偏）。
func (g *relGraph) focusAnchor() {
	if g.sol == nil || g.anchor < 0 || g.anchor >= len(g.sol.Pos) {
		return
	}
	p := g.sol.Pos[g.anchor]
	g.offX = relAnchorDatumX*g.viewW - p.X*g.z
	g.offY = g.viewH/2 - p.Y*g.z
}

// focusAnchorFollow 视图跟随（D126 实测修，D127 沿用右基准，D128 改**有界滞后**）：树
// 太大装不下视口（zoom 触底仍超宽/超高；横树的长会话必然如此）时，**不再一次性按静息位
// 对准**——那是假设树已经展开完。深链/宽树要几秒到十几秒才落定，期间锚点（= 当前消息）
// 离静息位上千 dp，按静息位定视场会把当前消息甩出视口（实测 200 节点链偏 1900px，
// 「拖节点」点不到人）。改为跟随：置 fitFollow，每帧把锚点**当前位**向右基准位**缓慢**
// 逼近（followAnchorStep），收敛即停 ⇒ 当前消息始终在视口内、落定时正好在基准位。
//
// **D128：跟随不再是硬锁**（用户 2026-10-12：「视口不要锁着某个节点或者树，应该缓慢跟随，
// 用户才看得到动」）。硬锁每帧把锚点按到基准位 ⇒ 整树的平移被相机**完全抵消**，画面上
// 只剩相对运动；软跟随让相机带滞后地追，树自身的位移就可见了。滞后有界
// （relFollowLagMax）⇒ 锚点不会离开可视范围。
func (g *relGraph) focusAnchorFollow() {
	g.fitFollow = true
	g.followAnchorStep()
}

// relFollowAlpha 视口跟随的每帧逼近率（0 = 硬锁、1 = 瞬间到位）。0.10 ⇒ 一帧走 10%，
// 约 10 帧（167ms）消掉 63% 的偏差、20 帧 86%——「看得见的缓慢」，又不至于落后太多。
// （曾试过「对称滞后钳」限幅，实测在大位移时反而把视口一帧拽 528dp，等于硬锁换了个
// 阈值——已删，改用 followAnchorStep 里的可见性安全网。）
const relFollowAlpha = 0.10

// followAnchorStep 向目标视场**有界滞后地**逼近一步。返回是否仍有可见偏差。
//
// 两层，分工明确：
//   - **软**：横向朝「Head 落在右基准位」、纵向朝「Head 居中」按 relFollowAlpha 逐步
//     逼近（不是硬锁）——树自身的平移因此在画面上可见（用户要的效果）；
//   - **硬（安全网）**：软逼近之后若 Head 会滑出视口，才**最小幅度**把它拉回来。这层
//     不是审美约束而是可见性约束（A12「锚点在场」），只在必要时动作。
func (g *relGraph) followAnchorStep() bool {
	if g.sol == nil || g.anchor < 0 || g.anchor >= len(g.sol.Pos) {
		return false
	}
	p := g.sol.Pos[g.anchor]
	tx := g.offX + (relAnchorDatumX*g.viewW-p.X*g.z-g.offX)*relFollowAlpha
	ty := g.offY + (g.viewH/2-p.Y*g.z-g.offY)*relFollowAlpha
	g.offX, g.offY = tx, ty
	g.clampAnchorVisible() // 安全网：Head 卡必须留在视口内（只在必要时动）
	dx, dy := math.Abs(tx-g.offX), math.Abs(ty-g.offY)
	// 已经很近（< relFollowSnapPx）就**精确落位**：几何逼近会停在几 px 外（alpha 衰减
	// 到阈值以下就不再动），而「落定时 Head 压在基准位」是硬契约（A12），差几 px 也会被
	// 用户看见。2px 以内的修正本身不可见。
	if dx < relFollowSnapPx || dy < relFollowSnapPx {
		tx = relAnchorDatumX*g.viewW - p.X*g.z
		ty = g.viewH/2 - p.Y*g.z
		// 落位后仍要过安全网（极端视口下目标可能与可见性冲突）
		g.offX, g.offY = tx, ty
		return g.clampAnchorVisible()
	}
	g.offX, g.offY = tx, ty
	return true
}

// relFollowSnapPx 跟随的「精确落位」阈值（px）：软逼近到这个距离内就直接落到基准位。
const relFollowSnapPx = 2.0

// clampAnchorVisible 把 Head 卡拉回视口内（最小幅度）；返回是否发生了修正。软逼近与
// 精确落位之后都要过这一关——「Head 在场」比「Head 压在基准位」优先。
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

// fitView 开窗首帧：整棵可见树**适配视口**（A13，D127 横树版）——按卡片外接盒算最小
// zoom、盒心居中，再把 **Head 平移到右基准位**（relAnchorDatumX：时间轴的「现在」钉
// 右侧、左侧全留过去；平移钳在不越界范围内）。三处口径：
//
//   - **zoom 由树的大小算出**（D130，无阅读性地板）：除「整树装进视口」外，还要求
//     「过去部分（Head 左侧）装进基准位以左 + Head 卡装进右侧余量」，四者取最小 ⇒ 装得下
//     时 Head 精确落基准位。只有大得连 relZoomMin 都兜不住（极端长会话）才走 **Head 右
//     基准 + 视图跟随**（focusAnchorFollow）。可读性不在这里把关：字号有钳、标签有预算
//     （低于 relZoomLabelMin 全隐 ⇒ overview 只剩徽标与边）。
//   - 盒子取**静息布局**（treeBox，物理平衡态）：当前位形会动，不适合当视场基准。
//   - **zoom 口径含右基准**：除「整树装进视口」外，还要求「过去部分（Head 左侧）装进
//     基准位以左、Head 卡装进右侧余量」⇒ 装得下时 Head 精确落基准位（不是盒心贴边的
//     近似）；两者取更小 zoom。
//   - 数据变更**不**重设 zoom/视口（见 frame）；「回到当前」走 focusAnchor（A8 右基准）。
func (g *relGraph) fitView() {
	minX, minY, maxX, maxY, ok := g.treeBox()
	if !ok {
		g.focusAnchor()
		return
	}
	w, h := maxX-minX, maxY-minY
	zf := math.Min(g.viewW/w, g.viewH/h) / g.dpx // 整树装得下（折回用户口径，DPI 另算）
	if g.anchor >= 0 && g.anchor < len(g.lay.restX) {
		// D127：**过去部分**（Head 左侧整段）要装进「右基准位以左」的视口、Head 卡右半
		// 装进右侧余量 ⇒ Head 精确落在基准位（时间轴的「现在」位置稳定，不随树宽漂）。
		if hw, _ := relCardHalf(g.geo[g.anchor]); hw > 0 {
			if past := g.lay.restX[g.anchor] - minX; past > 0 {
				if z := (relAnchorDatumX * g.viewW / g.dpx) / past; z < zf {
					zf = z
				}
			}
			if z := ((1 - relAnchorDatumX) * g.viewW / g.dpx) / (2 * hw); z < zf {
				zf = z
			}
		}
	}
	if zf < relZoomMin { // 防呆下限：树大得离谱（/vac 病态输入）也不至于除出 0
		zf = relZoomMin
	}
	if zf > relZoomMax {
		zf = relZoomMax
	}
	g.zoom = zf
	g.z = zf * g.dpx
	if w*g.z > g.viewW || h*g.z > g.viewH { // 夹到下限仍装不下（横树的长会话必然如此）：Head 右基准 + 视图跟随（焦点优先）
		// 树还在展开（首帧 = 折叠播种态，深链要十几秒）⇒ 一次性对准会把当前消息甩出
		// 视口，改为**跟随**：每帧把锚点当前位对准中心，收敛即停（focusAnchorFollow）。
		g.focusAnchorFollow()
		return
	}
	// 装得下：zoom 口径（过去部分装进基准位左侧）已保证整树在视口内。开**视图跟随**——
	// 入场展开期间 Head 也钉在右基准位（「现在」不动、历史从它身上长出来），收敛即停、
	// 落定时正好在基准位。无锚点（理论不可达）才回退盒心居中。
	if g.anchor >= 0 && g.anchor < len(g.lay.restX) {
		g.focusAnchorFollow()
		return
	}
	g.offX = g.viewW/2 - (minX+maxX)/2*g.z
	g.offY = g.viewH/2 - (minY+maxY)/2*g.z
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
	minX, minY, maxX, maxY, ok := g.treeBox()
	if !ok {
		return
	}
	if (maxX-minX)*g.z <= g.viewW && (maxY-minY)*g.z <= g.viewH {
		g.offX = g.viewW/2 - (minX+maxX)/2*g.z // 装得下：盒心对准视口中心
		g.offY = g.viewH/2 - (minY+maxY)/2*g.z
		return
	}
	// 装不下（长会话）：Head 右基准 + 视图跟随（与 fitView 的回退口径一致）——至少当前
	// 消息可见。
	g.focusAnchorFollow()
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
//	focusScopeAll     整树：重新播种重排 + 视口容纳**整棵可见树**（回到初始态）
//	focusScopeLine    历史线：视口容纳 **root → Head 的祖先链**（这一轮对话的主干）
//	focusScopeNode    当前节点：视口容纳 **Head + 其父 + 其子**（一跳邻域；按钮文案写
//	                  「聚焦当前节点」，域比文案宽是刻意的——父是理解当前节点的前提，子是
//	                  「接下来会是什么」的答案）
//
// 共同口径：只挪视场、**不重置 zoom**（A8 沿用：用户 zoom 是私有的）；内容按**当前
// 物理位形**取盒（不是静息布局——用户此刻看到的树就是这一片）；装不下时沿用右基准 +
// 有界滞后跟随（与 fitView 同一回退）。
const (
	focusScopeAll = iota
	focusScopeLine
	focusScopeNode
)

// focusScope 对某片内容取盒并适配视口。indices = 参与取景的本地索引（空 = 理论不可达，
// 退化为整树）。不重置 zoom。
func (g *relGraph) focusScope(scope int) {
	if g.sol == nil || g.lay == nil {
		return
	}
	idx := g.scopeIndices(scope)
	if len(idx) == 0 {
		g.focusAnchor()
		return
	}
	minX, minY := math.Inf(1), math.Inf(1)
	maxX, maxY := math.Inf(-1), math.Inf(-1)
	for _, li := range idx {
		hw, hh := relCardHalf(g.geo[li])
		p := g.sol.Pos[li]
		minX, maxX = math.Min(minX, p.X-hw), math.Max(maxX, p.X+hw)
		minY, maxY = math.Min(minY, p.Y-hh), math.Max(maxY, p.Y+hh)
	}
	g.fitBox(minX, minY, maxX, maxY)
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

// fitBox 把给定世界盒装进视口（保持 zoom）：装得下 = 盒心对准视口中心；装不下 = Head
// 右基准 + 有界滞后跟随（与 fitView 的回退同一口径，绝不把当前消息甩出视口）。
func (g *relGraph) fitBox(minX, minY, maxX, maxY float64) {
	w, h := maxX-minX, maxY-minY
	if w <= 0 || h <= 0 {
		g.focusAnchor()
		return
	}
	if w*g.z > g.viewW || h*g.z > g.viewH {
		g.focusAnchorFollow()
		return
	}
	g.offX = g.viewW/2 - (minX+maxX)/2*g.z
	g.offY = g.viewH/2 - (minY+maxY)/2*g.z
}

// relayoutFromSeed 回到初始态：重新折叠播种（速度清零、时钟重上）⇒ 整树**重新排列**
// 一次（首帧即涌现的观感重放），并立刻把视场适配到能容下整树（剩余的排布由随后
// 15s 的物理展开完成，followAnchorStep 负责跟随）。
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

// treeBox 树的外接盒（世界坐标 dp）：按**静息布局**（物理平衡态）算——当前物理位形
// 会动，不适合当视场基准；外扩 relFitMargin 容纳斥力把兄弟顶开的那点余量。
func (g *relGraph) treeBox() (minX, minY, maxX, maxY float64, ok bool) {
	if g.lay == nil || len(g.vis.nodes) == 0 || len(g.lay.restX) == 0 {
		return 0, 0, 0, 0, false
	}
	rest := g.lay.restPos()
	minX, minY = math.Inf(1), math.Inf(1)
	maxX, maxY = math.Inf(-1), math.Inf(-1)
	for li := range g.vis.nodes {
		hw, hh := relCardHalf(g.geo[li]) // 世界坐标下的卡片占位（不随 zoom 变）
		p := rest[li]
		minX, maxX = math.Min(minX, p.X-hw), math.Max(maxX, p.X+hw)
		minY, maxY = math.Min(minY, p.Y-hh), math.Max(maxY, p.Y+hh)
	}
	padX := (maxX - minX) * relFitMargin
	padY := (maxY - minY) * relFitMargin
	return minX - padX, minY - padY, maxX + padX, maxY + padY, true
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

// labelBlock 标签文本块的**像素**几何（nodeCardBox 与 drawNodeCard 共用一份）：宽 = 估宽
// （封顶 relLabelMaxDp × z）、行数 = ⌈估宽 ÷ 盒宽⌉（钳 [1, relLabelLines]）、块高 = 行数 ×
// 行高。行高 = labelFont × spx × relLabelLineLead——**与 drawNodeCard 里显式设置的
// LineHeight 同值**：估的和画的必须一致，否则卡片装不下自己的文字。
func (g *relGraph) labelBlock(txt string) (w, h float32, lines int) {
	est := relEstTextW(txt, float64(g.labelFont())) * g.spx
	max := float32(relLabelMaxDp * g.z)
	w = float32(est)
	if w > max {
		w = max
	}
	lines = int(math.Ceil(float64(est) / float64(max)))
	if lines < 1 {
		lines = 1
	}
	if lines > relLabelLines {
		lines = relLabelLines
	}
	return w, float32(lines) * g.labelLineH(), lines
}

// labelLineH 一行标签的像素高 = 字号 × PxPerSp × 行高系数（与显式 LineHeight 同值）。
func (g *relGraph) labelLineH() float32 {
	return g.labelFont() * float32(g.spx) * relLabelLineLead
}

// nodeCardBox 节点卡盒（屏幕坐标；命中判定与绘制**共用**同一几何）。
// 标签可见（A5 预算 + zoom 下限）时卡 = [徽标 | 标签]（向右展开），否则退化为裸形状盒。
// 卡高 = max(徽标高, 标签块高) + 上下内边距（D126：多行摘要把卡片撑高，徽标仍居中）。
func (g *relGraph) nodeCardBox(gi int, xs, ys float32) image.Rectangle {
	m := g.model.nodes[gi]
	r := float32(g.nodeRadius(&m) * g.z)
	badge := relBadgeBox(xs, ys, r)
	txt := relLabelText(g.model, gi, g.zoom)
	if txt == "" {
		return badge
	}
	lw, lh, _ := g.labelBlock(txt)
	gap := int(relCardGapDp * g.z)
	pad := int(relCardPadDp * g.z)
	pv := int(relCardPadV * g.z)
	half := lh / 2
	if h := float32(badge.Dy()) / 2; h > half {
		half = h // 徽标比标签块高时以徽标为准（粗节点）
	}
	return image.Rect(badge.Min.X-pad, int(ys-half)-pv,
		badge.Max.X+gap+int(lw)+pad, int(ys+half)+pv)
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
	// 输入区 = 本栏矩形（clip）：本窗是「左栏会话列表 + 右图」两栏（§5.4），右图被
	// layout.Flexed 放在左栏之后转置的变换里绘制。若指针输入 op 仍挂在**根区域**
	// （整窗、单位变换）上，事件位置是窗口坐标，与绘制坐标相差一个左栏宽——点击整体
	// 右偏一个左栏宽（2026-10-09 实测报障），且左栏上的点击也会被本图误收。
	// 把 event.Op / gesture.Scroll 收进本栏 clip 后，Gio 用该 clip 的变换把窗口坐标
	// 反变换回栏内坐标，命中/拖拽/菜单位置与绘制完全对齐。
	stack := clip.Rect(image.Rectangle{Max: image.Pt(int(g.viewW), int(g.viewH))}).Push(gtx.Ops)
	// D139：遮罩开启期**不取手势**（模态）。绘制与物理继续走——背景要在 scrim 下保持
	// 可见与运动。隔离是**双保险**：遮罩的整窗 `event.Op` 输入区已按 Gio 命中链把本栏
	// 遮蔽（实测：遮罩开启时右图收不到指针事件），此处显式不取是纵深防御，且是左栏/
	// 状态栏这些**同链控件**的必需条件（它们不受本栏遮蔽影响，见 historyState.dispatch）。
	if !st.overlayActive() {
		g.update(gtx, u, st)
	}
	stack.Pop()
	// 拖拽/收敛中的交互态：本帧松弛后若还要继续，追加一次重绘请求（Gio 的动画口径，
	// 比 Window.Invalidate 轻；收敛即停 → R3 随即恢复）。
	if g.tickLayout() {
		gtx.Execute(op.InvalidateCmd{})
	}
	// 视图跟随（D128 有界滞后）：运动期间相机**缓慢逼近**右基准位（不是硬锁——硬锁会
	// 把整树的平移抵消掉，用户看不见动）；收敛后再留一条**收敛尾巴**把滞后走完，落定时
	// 正好压在基准位（A12 的落点契约）。**拖节点期间冻结**（D121：拖节点与拖空白必须
	// 区分开，拖节点不动视场）。
	if g.fitFollow && g.dragNode < 0 {
		moving := g.lay != nil && !g.lay.settled
		if !g.followAnchorStep() && !moving {
			g.fitFollow = false
		}
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
	// 右键菜单（D132）不自绘：openNodeMenu 投 shell，与主界面气泡菜单同一呈现。
	return layout.Dimensions{Size: gtx.Constraints.Max}
}

// tickLayout 物理步进（D126）：无 pin 时自由收敛；拖动中带「临时固定锚」——被拖者跟手，
// 其余节点经弹簧被带动（整树按物理规律动起来）。返回是否需要续帧——收敛即 false
// （不烧资源，R3）；任何交互（拖拽 / rebuild）会注入能量自动唤醒。
func (g *relGraph) tickLayout() bool {
	if g.lay == nil {
		return false
	}
	if g.lay.settled && g.dragNode < 0 {
		return false // 静止即停（R3）
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
	if g.relaxLeft <= 0 && g.dragNode < 0 {
		// 续帧预算耗尽 = 认账收敛（R3 硬顶）：软物理下深链/病态输入的收敛时间可能远超
		// 预算，不置位 settled 的话之后每个事件帧都会再白跑一次物理（树永远在慢爬）。
		g.lay.settled = true
		return false
	}
	pin := relPin{}
	if g.dragNode >= 0 {
		pin = relPin{Valid: true, Index: g.dragNode, Pos: g.dragPinWorld()}
	}
	g.lay.step(pin)
	g.sol = g.lay.solution()
	if g.relaxLeft > 0 {
		g.relaxLeft--
	}
	more := !g.lay.settled && g.relaxLeft > 0
	// 视图跟随的收敛尾巴也要续帧（视口跟随本身就是一段动画，D128）：树已静止但相机
	// 还有可见滞后时继续请求帧，否则落点会差几十 px（A12 的落点契约）。
	if g.fitFollow && g.dragNode < 0 {
		more = true
	}
	if g.dragNode >= 0 && (g.dragGrabX != 0 || g.dragGrabY != 0) {
		more = true // 抓取偏移未衰减完：继续续帧（节点在滑向光标，指针停住也要动）
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
			g.menuArm = false
			g.menuNode = -1
		case pointer.Scroll:
			g.wheel(int(e.Scroll.Y))
		case pointer.Press:
			if e.Buttons.Contain(pointer.ButtonSecondary) {
				// D132：右键 = 武装（同气泡口径 D92——按下武装、抬起才 fire，免得菜单在
				// 按键未松时就弹出来吃掉这次抬起）；按在空白处不武装。
				g.menuNode = g.hitTest(e.Position.X, e.Position.Y)
				g.menuArm = g.menuNode >= 0
				continue
			}
			if e.Buttons.Contain(pointer.ButtonPrimary) {
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
					g.fitFollow = false
					g.offX = g.panStartOffX + dx
					g.offY = g.panStartOffY + dy
				}
			}
		case pointer.Release:
			if g.menuArm {
				g.menuArm = false
				// 「原位」抬起（同一节点）才 fire——与气泡右键同口径（D92）。
				if h := g.menuNode; h >= 0 && h == g.hitTest(e.Position.X, e.Position.Y) {
					g.openNodeMenu(u, h)
				}
				g.menuNode = -1
				continue
			}
			if g.pressed {
				g.pressed = false
				if g.dragNode >= 0 {
					g.releaseDrag() // 拖过 = 移动节点，不开遮罩
				} else if !g.dragging && g.pressNode >= 0 &&
					g.pressNode == g.hitTest(e.Position.X, e.Position.Y) {
					// D139：原位抬起（未拖拽）= 开消息查看/编辑遮罩（`/goto` 移进图侧
					// 右键菜单「在主窗定位」）。
					g.inspectNode(u, st, g.pressNode)
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

// releaseDrag 松手/取消：撤销临时固定锚，**把当前位置固化成静息槽位**（D126「自由
// 移动」：不写回就会被弹簧拉回旧形状——用户实测「拖完还是回到一个固定的形状」），
// 唤醒物理按新形状收敛，并把树拉回视野（fitPending → recenterTree，D121 实测修：
// 拖开摊宽的树不丢）。
func (g *relGraph) releaseDrag() {
	if g.dragNode < 0 {
		return
	}
	if g.lay != nil {
		g.lay.adoptSlot(g.dragNode)
		g.recordSlot(g.dragNode)
	}
	g.dragNode = -1
	if g.lay != nil {
		g.lay.settled = false // 撤 pin：被拖节点仍有非零健康力 → 重新唤醒物理（否则会僵在拖拽位）
	}
	g.relaxLeft = relSettleFrames
	g.fitPending = true // 松手后重定视野（保持 zoom，只把树盒心对准视口）
}

// recordSlot 把手动槽位记进帧层（按消息 ID）：当前相对父的偏移 + 层距；结构根记整树
// 平移量。rebuild 后按 ID 恢复（restoreSlots）。
func (g *relGraph) recordSlot(li int) {
	if g.lay == nil || g.model == nil || li < 0 || li >= len(g.vis.nodes) {
		return
	}
	id := g.model.nodes[g.vis.nodes[li]].id
	if g.slots == nil {
		g.slots = map[conversation.MessageID]relSlot{}
	}
	if g.lay.nodes[li].Parent < 0 {
		// 根没有父：adoptSlot 已把整棵静息布局平移过去 ⇒ 记「当前根位」即累计平移量
		g.slots[id] = relSlot{Root: true, OffX: g.lay.restX[li], OffY: g.lay.restY[li]}
		return
	}
	g.slots[id] = relSlot{Sib: g.lay.restOff[li], Gap: g.lay.restGap[li]}
}

// inspectNode 点节点 → 开消息查看/编辑遮罩（D139：取代原单击的 /goto/A10；正文经
// port.TreeView.NodeText 在遮罩内取，快照未发布/取不到正文时开只读空卡）。
func (g *relGraph) inspectNode(u *UI, st *historyState, li int) {
	if g.model == nil || li < 0 || li >= len(g.vis.nodes) {
		return
	}
	st.openOverlay(u, g.model.nodes[g.vis.nodes[li]].id, conversation.Fresh, overlayPageRendered)
}

// gotoNode 投 /goto <id>（A10；图侧右键菜单「在主窗定位」经 nodeActGoto 走同路径）。
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

// drawNodeCard 节点卡（D121「内容入内」；D126「多行占满卡片」）：标签可见时 = 卡片底 +
// 形状徽标（Role 四形 × Level 色，A2/§3.1 不变）+ 卡内标签（按盒宽换行、最多
// relLabelLines 行，超出走省略号）；标签不可见（A5：低 zoom / 低 weight）时退化为
// 「裸形状」——标签仍是预算，卡不是。
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
	// 标签：徽标右侧、卡片内（多行块相对徽标垂直居中）。
	badge := relBadgeBox(xs, ys, r)
	lw, lh, lines := g.labelBlock(txt)
	lx := badge.Max.X + int(relCardGapDp*g.z)
	ly := int(ys) - int(lh)/2
	off := op.Offset(image.Pt(lx, ly)).Push(gtx.Ops)
	// **精确约束**（D126 实测修）：layout.Center 按 max(Constraints.Min, 子件尺寸)
	// 定位——Min 给 (0,0) 时等于不平移，文字贴在盒子左上角（数字/文本「不居中」的
	// 根因）。Min = 盒子尺寸 → Center 才真的把子件摆到盒子中央。
	gtx.Constraints = layout.Exact(image.Pt(int(lw), int(lh)))
	l := material.Label(th, unit.Sp(g.labelFont()), txt)
	l.Color = th.Fg
	l.MaxLines = lines // 多行占满卡片；超出行数走省略号
	l.LineHeight = unit.Sp(g.labelFont() * relLabelLineLead)
	l.Layout(gtx)
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
// **精确约束 + Center**（D126 实测修）：Gio 的 Center 按 max(Constraints.Min, 子件尺寸)
// 定位——Min 给 (0,0) 时等于不平移，数字贴在形状左上角（用户报障「数字居中问题」）；
// Min = 盒子尺寸才真的居中。反过来少了 Center 则退化为左顶对齐，一样不居中。
func (g *relGraph) drawGlyph(gtx layout.Context, th *material.Theme, xs, ys, r float32, txt string) {
	badge := relBadgeBox(xs, ys, r*0.82)
	sp := math.Max(8, float64(r)*0.95)
	off := op.Offset(badge.Min).Push(gtx.Ops)
	gtx.Constraints = layout.Exact(badge.Size())
	layout.Center.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		l := material.Label(th, unit.Sp(float32(sp)), txt)
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
		cen := [4]f32.Point{
			{X: xs + r - cr, Y: ys - r + cr}, // 右上
			{X: xs + r - cr, Y: ys + r - cr}, // 右下
			{X: xs - r + cr, Y: ys + r - cr}, // 左下
			{X: xs - r + cr, Y: ys - r + cr}, // 左上
		}
		for i := 0; i < 4; i++ {
			a0 := -math.Pi/2 + float64(i)*math.Pi/2
			a1 := a0 + math.Pi/2
			pts = relSampleArc(pts, cen[i], cr, a0, a1, step)
			// 直边必须贴 **±r**（= 填充圆角方的边所在），端点是「本角弧终点 → 下一角弧
			// 起点」。D126 实测修：旧版把直边画在 r−cr（弧心列）上，比填充内缩一个角
			// 半径（≈6.8px）——左右（上下）虚线画进徽标里，读作「虚线框嵌在节点中」。
			j := (i + 1) % 4
			b0 := -math.Pi/2 + float64(j)*math.Pi/2
			pts = relSampleSeg(pts, relArcPoint(cen[i], cr, a1), relArcPoint(cen[j], cr, b0), step)
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

// relArcPoint 弧上一点的屏幕坐标（弧心 + 角）。
func relArcPoint(c f32.Point, r float32, a float64) f32.Point {
	return f32.Pt(c.X+r*float32(math.Cos(a)), c.Y+r*float32(math.Sin(a)))
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

// openNodeMenu 图节点右键开菜单（D132）：与主界面气泡菜单**同一呈现、同一分发**——组好
// 上下文投 shell（requestBubbleMenu），项集/命令见 menu.go 的 nodeMenuItems。
func (g *relGraph) openNodeMenu(u *UI, li int) {
	ctx := g.nodeMenuCtx(u, li)
	if ctx == nil {
		return
	}
	u.requestBubbleMenu(ctx)
}

// nodeMenuCtx 组装图节点菜单上下文（D132）：
//   - id/角色 → 档位（user/assistant 走气泡七项；root/system 只给复制/查看原文）；
//   - 正文经 `port.TreeView.NodeText` 取**全文**（GraphNode.Snippet 只有 40 字摘要，拿它
//     预填编辑框会在提交时把消息截断成摘要——D132 的要点）；取不到（节点无正文/快照
//     未发布）→ edit/copy 空，项集随之收敛为图侧三项（nodeMenuItems 的 hasText 分支）；
//   - part = -1（缺省合段，/edit 语义：全部正文分片并为一条；与 NodeText 的拼接口径互逆）；
//   - raw = 正文原文（与主界面「原文 · <id>」同款标题）。
func (g *relGraph) nodeMenuCtx(u *UI, li int) *bubbleMenuCtx {
	if li < 0 || li >= len(g.vis.nodes) || g.model == nil {
		return nil
	}
	n := g.model.nodes[g.vis.nodes[li]]
	if n.id == "" {
		return nil
	}
	ctx := &bubbleMenuCtx{id: n.id, kind: relNodeKind(n.role), part: -1, graph: true}
	body := ""
	if u.opts.Tree != nil {
		if t, ok := u.opts.Tree.NodeText(n.id); ok {
			body = sanitizeControl(t) // 与气泡块同口径：控制序列不进菜单文本（§9）
		}
	}
	ctx.edit, ctx.copy = body, body
	ctx.raw = rawContent{title: "原文 · " + shortID(string(n.id)), text: body}
	return ctx
}

// relNodeKind 节点角色 → 气泡菜单档（D132）：user/assistant 与主界面气泡同档；root/system
// 不可编辑（blockPlain 分支只给复制/查看原文）。
func relNodeKind(r conversation.Role) blockKind {
	switch r {
	case conversation.RoleUser:
		return blockUser
	case conversation.RoleAssistant:
		return blockAssistant
	}
	return blockPlain
}

// relInBox 屏幕点是否在视口盒内（float32 版）。
func relInBox(box image.Rectangle, x, y float32) bool {
	return x >= float32(box.Min.X) && x <= float32(box.Max.X) &&
		y >= float32(box.Min.Y) && y <= float32(box.Max.Y)
}
