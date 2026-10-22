package uigui

// relmap_frame.go 右图帧态与帧循环（D120⑦，relation-map-design §5.2/§5.4）。
//
// 本文件只放「一个右图是什么、每帧怎么走」：档位常量与文本估算、relGraph 帧态、
// rebuild/槽位恢复、帧首刷新与续帧判定、手势消费（拖拽/平移/缩放/点击）。
// 视场取景见 relmap_view.go，几何与命中见 relmap_geom.go，绘制见
// relmap_draw.go，节点动作与右键菜单见 relmap_node.go。
//
// 绘制符号全部由派生纯函数（relmap.go）喂入：fill = 深度色带[Level mod 4]、shape = Role
// 四形、直径 = weight 查表（A4：与 degree 无关）、描边 = 主干亮环 ⊕ 分叉虚线环、边色 =
// source 填充色、边型 = EdgeKind（顺接实线 / 版本链虚线）、标签 = 权重预算 + zoom 字号。
//
// 手势（§5.2 动词表）：拖拽空白 = pan；滚轮 = zoom（夹 [0.5,1.5]，**越界降级纵向平移**
// A9）；悬停 = 陈述 tooltip（inspect，暂缓）；左键节点 = 开消息查看/编辑遮罩（D139；
// 会话根 = 复制会话 ID，2026-10-25 用户批注）；右键节点 = 菜单（生成响应 / 复制 ID /
// 插入摘要 / 删除该节点及子树 / 转到；会话根无菜单）。
//
// 性能纪律（§5.4）：视口外扩裁剪、懒重排（平移/缩放/悬停不重排——重排只由数据变更 /
// rebudget 触发，R3）。

import (
	"image"
	"math"

	"gioui.org/gesture"
	"gioui.org/io/event"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/widget/material"

	"github.com/Tonyjh07/Aquarius/internal/domain/conversation"
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
	relZoomMin  = 0.1
	relZoomMax  = 2.0
	relZoomStep = 1.1 // 每格滚轮缩放倍率
	// relFocusZoomMax 显式取景动作（装下整树 / 聚焦当前历史 / 聚焦当前节点）的 zoom 上限
	// （D140/D3：3~5 倍取中）。滚轮仍受 A9 的 relZoomMax 钳制——取景是「一次到位的构图」，
	// 用户滚轮是「微调」，两者的合理区间不必相同（取景要能把一跳邻域顶到近前读）。
	relFocusZoomMax = 4.0
	relNodeBaseDp   = 34.0 // w=1.00 的直径基准（dp）；直径 = weight × base（A4）
	relRingW        = 2.5  // 分叉虚线外环描边宽
	relEdgeW        = 1.5  // 边线宽
	relLabelBase    = 12.0 // 标签基准字号（sp）× zoom，钳 [10,16]（A9）
	relLabelMinSp   = 10.0
	relLabelMaxSp   = 16.0
	relCullPad      = 0.2 // 视口外扩 20% 裁剪
	relDashOn       = 6.0 // 虚线段长（dp）
	relDashOff      = 5.0 // 虚线间隔（dp）
	relHitSlop      = 6.0 // 命中判定外扩（dp）
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
	// 「现在」钉在右侧、左侧全留给过去。D140 起这是**平移偏好**（P1）而非硬约束——
	// 整盒装得下时 Head 精确落这里；装不下时由可行域钳制让步（Head 仍在场，A12）。
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

	zoom         float64    // 视场倍率 [relZoomMin, relZoomMax]（A9，用户口径）
	z            float64    // **有效**缩放 = zoom × PxPerDp（dp 世界 → 屏幕 px；含 DPI 换算）
	spx          float64    // Metric.PxPerSp（标签 px 换算；帧首刷新，0/1 = 1）
	dpx          float64    // Metric.PxPerDp（帧首刷新；0/1 = 1）
	offX, offY   float64    // 视口平移（screen = world×z + off）
	viewW, viewH float64    // 视口尺寸（px，帧首刷新）
	viewPending  viewAction // 下帧执行的视场动作（显式取景；D140 统一状态栏/菜单入口）
	fitPending   bool       // 下帧把树拉回视野（拖后松手，D121 实测修；不重置 zoom）
	everFit      bool       // 已做过首帧整树适配（此后 rebuild 不动视场——数据变更不重置 zoom/视口）
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
		if li < 0 { // 被预算裁掉：槽位还记着（预算放开时要用）
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
	// 视场调整只在这三处：拖后松手把树拉回视野（fitPending，不重置 zoom）；开窗首帧整树
	// 适配（everFit，A13）；显式取景动作（viewPending：装下整树 / 两个聚焦域，D140）。
	// **数据变更不碰视场**——用户的 zoom/平移是私有的，一条新消息进来不抢
	// （D121 实测修：此前每条消息都重置 zoom/视口）。三者都在帧首消费：viewW/viewH/dpx
	// 与 g.z 刚刷新完，取景算出的平移量才是本帧的（与 fitPending/everFit 同一口径）。
	if g.fitPending {
		g.fitPending = false
		g.recenterTree()
	}
	if !g.everFit && g.sol != nil {
		g.everFit = true
		g.fitView()
	}
	if act := g.viewPending; act != vaNone {
		g.viewPending = vaNone
		switch act {
		case vaFitAll:
			g.fitAll()
		case vaFocusLine:
			g.focusScope(focusScopeLine)
		case vaFocusNode:
			g.focusScope(focusScopeNode)
		}
	}
	// 输入区 = 本栏矩形（clip）：本窗是「左栏会话列表 + 右图」两栏（§5.4），右图被
	// layout.Flexed 放在左栏之后转置的变换里绘制。若指针输入 op 仍挂在**根区域**
	// （整窗、单位变换）上，事件位置是窗口坐标，与绘制坐标相差一个左栏宽——点击整体
	// 右偏一个左栏宽（2026-10-09 实测报障），且左栏上的点击也会被本图误收。
	// 把 event.Op / gesture.Scroll 收进本栏 clip 后，Gio 用该 clip 的变换把窗口坐标
	// 反变换回栏内坐标，命中/拖拽/菜单位置与绘制完全对齐。
	stack := clip.Rect(image.Rectangle{Max: image.Pt(int(g.viewW), int(g.viewH))}).Push(gtx.Ops)
	// D139/D157：遮罩或确认弹窗开启期**不取手势**（模态）。绘制与物理继续走——背景要在
	// scrim 下保持可见与运动。隔离是**双保险**：模态层的整窗 `event.Op` 输入区已按 Gio 命中链
	// 把本栏遮蔽（实测：遮罩开启时右图收不到指针事件），此处显式不取是纵深防御，且是左栏/
	// 状态栏这些**同链控件**的必需条件（它们不受本栏遮蔽影响，见 historyState.dispatch）。
	if !st.modalActive(u) {
		g.update(gtx, u, st)
	}
	stack.Pop()
	// 拖拽/收敛中的交互态：本帧松弛后若还要继续，追加一次重绘请求（Gio 的动画口径，
	// 比 Window.Invalidate 轻；收敛即停 → R3 随即恢复）。
	if g.tickLayout() {
		gtx.Execute(op.InvalidateCmd{})
	}
	// 视图跟随（D140）：运动期间相机**缓慢逼近**目标视场（盒在视口内 + Head 落右基准位 +
	// 纵向盒心居中），同时把 zoom 收到「当前盒装得下」——**不是硬锁**（硬锁会把整树的平移
	// 抵消掉，用户看不见动，见 followSpanStep 的 D140 记录）。收敛后再留一条**收敛尾巴**把
	// 滞后走完。**拖节点期间冻结**（D121：拖节点与拖空白必须区分开，拖节点不动视场）。
	if g.fitFollow && g.dragNode < 0 {
		moving := g.lay != nil && !g.lay.settled
		if !g.followSpanStep(false) && !moving {
			g.fitFollow = false
		}
	}
	if g.sol == nil {
		return layout.Center.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			l := material.Body2(th, "关系图未就绪")
			l.Color = paperOf().text2
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
	if g.dragNode >= 0 {
		// 按住期间恒续帧（2026-10-18 实测修）：拖拽是用户驱动的一段会话，几何上「谁还在
		// 动」不可预见（邻居被弹簧带、抓取偏移在衰减），提前判停会让树僵在非平衡态，
		// 而指针不再产生事件时就没有下一帧来纠正它。松手后由 settled 接管，照常收敛即停。
		more = true
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
				// 会话根**不武装**（2026-10-25 用户批注：root 的右键菜单整份删掉）。
				g.menuNode = g.hitTest(e.Position.X, e.Position.Y)
				if g.menuNode >= 0 && g.isRoot(g.menuNode) {
					g.menuNode = -1
				}
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
					// 原位抬起（未拖拽）：会话根 = 复制会话 ID（2026-10-25 用户批注）；
					// 其余节点 = 开消息查看/编辑遮罩（D139，`/goto` 移进图侧右键菜单「转到」）。
					g.activateNode(gtx, u, st, g.pressNode)
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
		g.lay.wake() // 撤 pin：重上物理时钟并唤醒（否则可能僵在拖拽位，2026-10-18 实测修）
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
