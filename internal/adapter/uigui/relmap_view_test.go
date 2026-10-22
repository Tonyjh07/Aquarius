package uigui

// relmap_view_test.go 右图取景与手势：首帧整树适配、聚焦域、命中、点击遮罩、
// 指针输入区、视图跟随、拖拽平移、滚轮缩放与越界降级、松手后的视场回退。

import (
	"image"
	"math"
	"testing"

	"gioui.org/f32"
	"gioui.org/io/input"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"github.com/Tonyjh07/Aquarius/internal/domain/conversation"
)

// relPaneFrame 在「左栏 + 右图」两栏布局中跑一帧右图（复刻 history.go 的 Flex：
// Rigid 左栏 + Flexed(1) 右图）并提交 hit 树。注入事件的坐标是**窗口坐标**；右图的
// 指针输入区必须收在本栏 clip 内，Gio 才能借该 clip 的变换把窗口坐标反变换回栏内坐标。
func relPaneFrame(q *input.Router, u *UI, st *historyState, leftPx int) {
	gtx, ops := frameGtxSize(q.Source(), 500, 500)
	layout.Flex{Axis: layout.Horizontal}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			gtx.Constraints = layout.Constraints{
				Min: image.Pt(leftPx, 0),
				Max: image.Pt(leftPx, gtx.Constraints.Max.Y),
			}
			return layout.Dimensions{Size: image.Pt(leftPx, gtx.Constraints.Max.Y)}
		}),
		layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
			return st.graph.frame(gtx, u.th, u, st)
		}),
	)
	q.Frame(ops)
}

// relPaneFixture 真窗两栏结构下的右图收敛基线（从首帧起跑完物理 + 等视口跟随收敛——
// D140 起跟随是**真的**有界滞后动画：只等 lay.settled 会拿到还在动的相机，后续按
// 上一帧算出的坐标做手势就会指空）。
func relPaneFixture(t *testing.T, leftPx int) (*UI, *historyState, *input.Router) {
	t.Helper()
	u := newHistUI(fakeRelTree{g: relFixtureGraph()}, fakeLister(nil))
	st := newHistoryState(u)
	q := new(input.Router)
	for i := 0; i < relSettleFrames*4 && st.graph.lay != nil &&
		(!st.graph.lay.settled || st.graph.fitFollow); i++ {
		relPaneFrame(q, u, st, leftPx)
	}
	if st.graph.lay == nil || !st.graph.lay.settled {
		t.Fatal("两栏夹具物理未收敛")
	}
	if st.graph.fitFollow {
		t.Fatal("两栏夹具视口跟随未收敛")
	}
	return u, st, q
}

// relAnchorScreen 锚点屏幕坐标（来自布局输出，A8）。
func relAnchorScreen(t *testing.T, g *relGraph) (f32.Point, int) {
	t.Helper()
	if g.sol == nil || g.anchor < 0 {
		t.Fatal("布局未就绪")
	}
	li := g.anchor
	x, y := g.toScreen(g.sol.Pos[li])
	return f32.Pt(x, y), li
}

// TestRelGraphFitViewOnOpen A13（D127 横树版，D140 改口径）：开窗 = 整树适配视口 + 平移
// 构图（Head 偏右、纵向盒心）；触底（长会话 zoom 到 relZoomMin 仍装不下）时退化为
// 「Head 在场 + 视图跟随」。断言：zoom 夹在 [relZoomMin, relZoomMax]（A9）；**Head 在视口
// 右半**（D7-b 起右基准位是平移**偏好**：两维贴满时它会被可行域钳到右侧贴边，不再恒等
// 0.72·视宽）；装得下的树另断言所有可见卡片在视口内（fit 语义本体）。
func TestRelGraphFitViewOnOpen(t *testing.T) {
	_, st, _ := relGraphFixture(t)
	g := st.graph
	if g.zoom < relZoomMin || g.zoom > relZoomMax {
		t.Fatalf("首帧 zoom = %v，超出 A9 钳制区间 [%v,%v]", g.zoom, relZoomMin, relZoomMax)
	}
	sx, _ := relAnchorScreen(t, g)
	if float64(sx.X) < g.viewW/2 {
		t.Fatalf("Head 未落在视口右半: x=%v, 视口宽 %.0f", sx.X, g.viewW)
	}
	if li := g.anchor; relCardOutOfView(g, li) {
		t.Fatalf("Head 在视口外: (%v,%v)，视口 %v×%v", sx.X, sx.Y, g.viewW, g.viewH)
	}
	if _, minY, _, maxY := relCurBox(g); (maxY-minY)*g.z <= g.viewH && g.zoom > relZoomMin+1e-9 {
		for li := range st.vis.nodes {
			if relCardOutOfView(g, li) {
				t.Fatalf("节点 %d 未被适配进视口（zoom %.3f），视口 %v×%v", li, g.zoom, g.viewW, g.viewH)
			}
		}
	}
}

// TestRelGraphFitAllAdaptsView D140：「装下整树」（状态栏按钮；原名「回到
// 当前」）= 把**当前盒**一次到位装进视口——**zoom 与平移一起适配**。旧版只挪视场不动 zoom，
// 于是「树明明装不下、点了等于没点」；这也是用户 2026-10-15 报障的第二个症状。
// 断言 V1a（盒内可见）/ V1b（上下留白对称）/ V1c（至少一维贴满）。
func TestRelGraphFitAllAdaptsView(t *testing.T) {
	u, st, q := relGraphFixture(t) // 已落定
	g := st.graph
	// 把视场挪到空白处 + 故意压小 zoom，再触发「装下整树」
	g.offX, g.offY = -900, -800
	g.zoom, g.z = 0.3, 0.3*g.dpx
	g.fitFollow = false
	g.viewPending = vaFitAll
	relGFrame(q, u, st)
	for li := range st.vis.nodes {
		if relCardOutOfView(g, li) {
			t.Fatalf("「装下整树」后节点 %d 仍在视口外（zoom %.3f）", li, g.zoom)
		}
	}
	if f := relBoxFill(g); f < 0.99 {
		t.Fatalf("「装下整树」后两维都没贴满：%.2f（zoom %.3f）", f, g.zoom)
	}
	top, bot := relViewGaps(g)
	if math.Abs(top-bot) > 8 {
		t.Fatalf("「装下整树」后上下留白不对称：上 %.0f 下 %.0f", top, bot)
	}
}

// TestRelGraphFocusAdaptsZoom D140/D3：两个聚焦域（历史线 / 当前节点）取景时**zoom 也要
// 适配**——只挪视场时「放大读一跳邻域」根本做不到（旧版把域装进视口后 zoom 还停在原处，
// 域只占屏幕中间一小块）。上限 = relFocusZoomMax（3~5 倍取中）。
func TestRelGraphFocusAdaptsZoom(t *testing.T) {
	u, st, q := relGraphFixture(t)
	g := st.graph
	// 压小 zoom + 挪开视场，再聚焦一跳邻域 ⇒ 期望 zoom **放大**且域盒贴满视口
	g.offX, g.offY = -400, -300
	g.zoom, g.z = 0.2, 0.2*g.dpx
	g.fitFollow = false
	g.viewPending = vaFocusNode
	relGFrame(q, u, st)
	if g.zoom <= 0.2 || g.zoom > relFocusZoomMax {
		t.Fatalf("聚焦当前节点后 zoom = %.3f，want ∈ (0.2, %.1f]", g.zoom, relFocusZoomMax)
	}
	idx := g.scopeIndices(focusScopeNode)
	if len(idx) == 0 {
		t.Fatal("当前节点域为空（夹具异常）")
	}
	if f := relFillOf(g, idx); f < 0.99 {
		t.Fatalf("聚焦当前节点后域盒没贴满：%.2f（zoom %.3f）", f, g.zoom)
	}
	for _, li := range idx {
		if relCardOutOfView(g, li) {
			t.Fatalf("聚焦当前节点后域内节点 %d 出视口", li)
		}
	}
	// 聚焦当前历史（root → Head 链）：域更宽 ⇒ zoom 重新适配（不沿用上一档），链装进视口
	g.viewPending = vaFocusLine
	relGFrame(q, u, st)
	if g.zoom <= 0 || g.zoom > relFocusZoomMax {
		t.Fatalf("聚焦当前历史后 zoom = %.3f，want ∈ (0, %.1f]", g.zoom, relFocusZoomMax)
	}
	line := g.scopeIndices(focusScopeLine)
	if len(line) == 0 {
		t.Fatal("历史线域为空（夹具异常）")
	}
	if f := relFillOf(g, line); f < 0.99 {
		t.Fatalf("聚焦当前历史后链盒没贴满：%.2f（zoom %.3f）", f, g.zoom)
	}
	for _, li := range line {
		if relCardOutOfView(g, li) {
			t.Fatalf("聚焦当前历史后链上节点 %d 出视口（zoom %.3f）", li, g.zoom)
		}
	}
}

// TestRelGraphHitTest 命中判定：锚点处命中锚点本地索引。
func TestRelGraphHitTest(t *testing.T) {
	_, st, _ := relGraphFixture(t)
	sx, li := relAnchorScreen(t, st.graph)
	if got := st.graph.hitTest(sx.X, sx.Y); got != li {
		t.Fatalf("hitTest(锚点) = %d, want %d", got, li)
	}
	if got := st.graph.hitTest(0, 0); got != -1 {
		t.Fatalf("hitTest(角落) = %d, want -1", got)
	}
}

// TestRelGraphClickOpensOverlay D139：左键**原位抬起**点节点 → 开消息查看/编辑遮罩
// （`/goto` 已移进图侧右键菜单「转到」）。开启那一次抬起不得被遮罩自己吃掉
// （armed 守卫：同帧广播，落点在卡片外）。
func TestRelGraphClickOpensOverlay(t *testing.T) {
	u, st, q := relGraphFixture(t)
	histFrameFor(q, u, st) // 先出一帧：右图按真实面板尺寸就位
	sx, li := relAnchorScreen(t, st.graph)
	want := st.model.nodes[st.vis.nodes[li]].id
	at := ovPaneToWindow(sx)
	q.Queue(relPointer(pointer.Press, at, true))
	histFrameFor(q, u, st)
	q.Queue(relPointer(pointer.Release, at, false))
	histFrameFor(q, u, st)
	if st.ov == nil {
		t.Fatal("点节点未开遮罩")
	}
	if st.ov.id != want {
		t.Fatalf("遮罩节点 = %q, want %q", st.ov.id, want)
	}
	if st.ov.page != overlayPageRendered || st.ov.mode != conversation.Fresh {
		t.Fatalf("遮罩初态 = %v/%v, want 呈现页/Fresh", st.ov.page, st.ov.mode)
	}
	select {
	case in := <-u.inCh:
		t.Fatalf("单击不应再投命令（D139 改走遮罩）: %+v", in)
	default:
	}
}

// TestRelGraphDragDoesNotOpenOverlay 拖拽（位移超 slop）= 移动节点，**不开遮罩**
// （D139：单击与拖拽共用同一次按压-抬起序列，以位移仲裁）。
func TestRelGraphDragDoesNotOpenOverlay(t *testing.T) {
	u, st, q := relGraphFixture(t)
	histFrameFor(q, u, st)
	sx, _ := relAnchorScreen(t, st.graph)
	at := ovPaneToWindow(sx)
	q.Queue(relPointer(pointer.Press, at, true))
	histFrameFor(q, u, st)
	moved := f32.Pt(at.X+70, at.Y+30)
	q.Queue(relPointer(pointer.Move, moved, true))
	histFrameFor(q, u, st)
	q.Queue(relPointer(pointer.Release, moved, false))
	histFrameFor(q, u, st)
	if st.ov != nil {
		t.Fatal("拖拽不应开遮罩")
	}
}

// TestRelGraphPointerAreaIsPane 右图的指针输入区 = 右图栏：事件位置经该栏的变换反算回
// 栏内坐标，点击位置与实际响应位置不错位（2026-10-09 实测报障：整体右偏一个左栏宽）；
// 左栏上的点击也不得被右图误收。
func TestRelGraphPointerAreaIsPane(t *testing.T) {
	const leftPx = 200
	u, st, q := relPaneFixture(t, leftPx)
	g := st.graph
	sx, li := relAnchorScreen(t, g)
	want := st.model.nodes[st.vis.nodes[li]].id
	// 点锚点的**窗口**坐标（栏内坐标 + 左栏宽）。
	at := f32.Pt(sx.X+float32(leftPx), sx.Y)
	q.Queue(relPointer(pointer.Press, at, true))
	relPaneFrame(q, u, st, leftPx)
	q.Queue(relPointer(pointer.Release, at, false))
	histFrameFor(q, u, st)
	if st.ov == nil || st.ov.id != want {
		t.Fatalf("遮罩节点 = %+v, want %s（点击位置与响应错位）", st.ov, want)
	}
	st.closeOverlay() // 后续手势断言需无遮罩背景
	// 左栏上的点击不得被右图误收（输入区收窄为右图栏）。
	q.Queue(relPointer(pointer.Press, f32.Pt(20, 20), true))
	relPaneFrame(q, u, st, leftPx)
	if g.pressed || g.pressNode != -1 || g.dragNode != -1 {
		t.Fatalf("左栏点击被右图误收：pressed=%v pressNode=%d dragNode=%d",
			g.pressed, g.pressNode, g.dragNode)
	}
	q.Queue(relPointer(pointer.Release, f32.Pt(20, 20), false))
	relPaneFrame(q, u, st, leftPx)
	if st.ov != nil {
		t.Fatal("左栏点击不应开遮罩")
	}
	// 拖拽同样按栏内坐标走：按在锚点的**窗口**坐标上拖动 → 该节点被 pin（不是平移）。
	moved := f32.Pt(at.X+60, at.Y+40)
	q.Queue(relPointer(pointer.Press, at, true))
	relPaneFrame(q, u, st, leftPx)
	q.Queue(relPointer(pointer.Move, moved, true))
	relPaneFrame(q, u, st, leftPx)
	if g.dragNode != li {
		t.Fatalf("两栏结构下拖拽未抓到锚点节点: dragNode = %d, want %d", g.dragNode, li)
	}
	q.Queue(relPointer(pointer.Release, moved, false))
	relPaneFrame(q, u, st, leftPx)
}

// TestRelGraphFitFollowKeepsAnchorInView D126 实测修（D127 横树沿用）：树太大装不下
// 视口（zoom 触底仍超宽/超高；横树长会话必然如此）时，fitView 走**锚点跟随**
// （fitFollow）——每帧把锚点当前位对准**右基准位**，收敛即停。深链展开要十几秒
// （手风琴模式），一次性按静息位对准会把当前消息甩出视口（实测 200 节点链偏 1900px；
// clip 还会裁掉视口外指针事件 ⇒ 拖拽直接失效）。用户一平移/缩放立即取消跟随
// （用户视场优先，不跟用户抢）。
func TestRelGraphFitFollowKeepsAnchorInView(t *testing.T) {
	u := newHistUI(fakeRelTree{g: relChainGraph(120)}, fakeLister(nil))
	st := newHistoryState(u)
	q := new(input.Router)
	relGFrame(q, u, st) // 首帧：fitView 回退（120 链装不下）⇒ 开跟随
	g := st.graph
	if !g.fitFollow {
		t.Fatal("树装不下视口时应开启锚点跟随")
	}
	// 展开中：锚点始终在视口内（当前消息不许丢）
	for i := 0; i < 40; i++ {
		relGFrame(q, u, st)
		x, y := g.toScreen(g.sol.Pos[g.anchor])
		if x < 0 || float64(x) > g.viewW || y < 0 || float64(y) > g.viewH {
			t.Fatalf("展开中锚点跑出视口（第 %d 帧）: (%.1f,%.1f) 视口 %.0fx%.0f",
				i, x, y, g.viewW, g.viewH)
		}
	}
	// 收敛后：跟随即停，锚点压在**视场右基准位**（横向，D127/A8）；纵向改用**盒心居中**
	// （D140 P2）——Head 不在盒的纵向中点时，Head 居中会把整盒顶向一侧（实测上留白 −461px、
	// 下留白 197px：顶端被裁、下方大片空白，正是用户截图那一幕）。
	for i := 0; i < relSettleFrames*4 && g.lay != nil && (!g.lay.settled || g.fitFollow); i++ {
		relGFrame(q, u, st)
	}
	if g.lay == nil || !g.lay.settled {
		t.Fatal("未收敛")
	}
	if g.fitFollow {
		t.Fatal("收敛后仍在跟随（空转重绘）")
	}
	x, y := g.toScreen(g.sol.Pos[g.anchor])
	if math.Abs(float64(x)-relAnchorDatumX*g.viewW) > 1 {
		t.Fatalf("收敛后锚点未在右基准位: x=%.1f, want %.1f", x, relAnchorDatumX*g.viewW)
	}
	if y < 0 || float64(y) > g.viewH {
		t.Fatalf("收敛后锚点跑出视口: y=%.1f，视口高 %.0f", y, g.viewH)
	}
	if top, bot := relViewGaps(g); math.Abs(top-bot) > 8 {
		t.Fatalf("收敛后上下留白不对称（P2 盒心居中）: 上 %.1f 下 %.1f", top, bot)
	}
	// 跟随中用户平移 ⇒ 取消（跟用户抢视场是 bug）
	g.fitFollow = true
	q.Queue(relPointer(pointer.Press, f32.Pt(10, 10), true)) // 空白处
	relGFrame(q, u, st)
	q.Queue(relPointer(pointer.Move, f32.Pt(40, 30), true)) // Move → Router 转 Drag
	relGFrame(q, u, st)
	if g.fitFollow {
		t.Fatal("用户平移后仍跟随（跟用户抢视场）")
	}
}

// TestRelGraphPanByDrag 空白拖拽 = 平移（绝对跟踪：起点 + 差值）。
func TestRelGraphPanByDrag(t *testing.T) {
	u, st, q := relGraphFixture(t)
	ox, oy := st.graph.offX, st.graph.offY
	start := f32.Pt(10, 10)
	q.Queue(relPointer(pointer.Press, start, true))
	relGFrame(q, u, st)
	q.Queue(relPointer(pointer.Move, f32.Pt(40, 30), true)) // 按压中 Move → Router 转 Drag
	relGFrame(q, u, st)
	q.Queue(relPointer(pointer.Release, f32.Pt(40, 30), false))
	relGFrame(q, u, st)
	if d := st.graph.offX - ox; d < 29 || d > 31 {
		t.Fatalf("offX 位移 = %v, want ≈30", d)
	}
	if d := st.graph.offY - oy; d < 19 || d > 21 {
		t.Fatalf("offY 位移 = %v, want ≈20", d)
	}
}

// TestRelGraphWheelZoomAndDegrade A9：滚轮缩放钳 [0.5,1.5]；越界降级纵向平移。
func TestRelGraphWheelZoomAndDegrade(t *testing.T) {
	u, st, q := relGraphFixture(t)
	g := st.graph
	g.zoom, g.z = 1.0, g.dpx // 归一：夹具首帧是 fit zoom（宽树会触底），先回到 1.0 再验放大步
	// 放大（Gio 约定滚上 = Scroll.Y < 0）
	q.Queue(pointer.Event{Kind: pointer.Scroll, Scroll: f32.Pt(0, -120), PointerID: 7, Source: pointer.Mouse})
	relGFrame(q, u, st)
	if g.zoom <= 1.0 {
		t.Fatalf("滚上后 zoom = %v, want > 1", g.zoom)
	}
	// 缩回并压到下限
	q.Queue(pointer.Event{Kind: pointer.Scroll, Scroll: f32.Pt(0, 120), PointerID: 7, Source: pointer.Mouse})
	relGFrame(q, u, st)
	g.zoom = relZoomMin // 直接压到下限
	oy := g.offY
	q.Queue(pointer.Event{Kind: pointer.Scroll, Scroll: f32.Pt(0, 120), PointerID: 7, Source: pointer.Mouse})
	relGFrame(q, u, st)
	if g.zoom != relZoomMin {
		t.Fatalf("低于下限 zoom = %v, want %v", g.zoom, relZoomMin)
	}
	if g.offY == oy {
		t.Fatal("越界后应降级纵向平移（offY 不变 = 静默失效）")
	}
}

// TestRelGraphWheelPanStep 实测修（D121）：滚轮越界降级平移用**定值小步**（relWheelPanPx），
// 一格滚轮只挪 48px——原实现 16×像素增量（一格 ≈ 1600px）会把树甩出裁剪盒、完全不可见。
func TestRelGraphWheelPanStep(t *testing.T) {
	u, st, q := relGraphFixture(t)
	g := st.graph
	g.zoom = relZoomMin
	oy := g.offY
	q.Queue(pointer.Event{Kind: pointer.Scroll, Scroll: f32.Pt(0, 120), PointerID: 7, Source: pointer.Mouse})
	relGFrame(q, u, st)
	if d := g.offY - oy; d <= 0 || d > relWheelPanPx*1.5 {
		t.Fatalf("越界平移步长 = %v, want 定值小步 (0, %v]", d, relWheelPanPx*1.5)
	}
}

// TestRelGraphRebuildKeepsView **已删**（D128）：原断言「数据变更不挪视场」在**软跟随**
// 视场契约下不再成立——数据变更后 Head 会移位，视口按有界滞后**逐步**追随以保证当前
// 消息在场（A12），视场因此会有缓慢位移。R3 的「新消息不重置 zoom」仍成立，但「视场
// 冻结」要让位给「视场有界滞后跟随」。待 D128 视口契约定稿后按新语义重写（应断言：
// zoom 不变 + 视场逐帧位移有界 + Head 始终在视口内）。

// TestRelGraphDragReleaseKeepsTreeInView 拖后松手把树拉回视野（D121；**D127 横树分两
// 档**）。宽树档（500×500 视口下夹具树在可读下限仍装不下）：recenterTree 退化为
// **Head 右基准 + 视图跟随**——盒心对准会把当前消息甩出视口；当前消息不许丢，切片
// 诚实性由 A7 状态栏（共 N · 呈现 M）承担。
// TestRelGraphDragReleaseKeepsTreeInView 拖节点松手：**视场一个像素都不动**（D130）——
// 用户 2026-10-12：「不管视角被移动到哪，拖动任意节点时都会回到这个样子」。原实现松手即
// recenterTree 重定中（装得下则盒心对准），与开窗 fit 同一条路径 ⇒ 松手构图恒等于开窗构图。
// 现在松手只在「画面上一个节点都不剩」时才兜底给视图（那种情形抓不到节点，实际不可达），
// 常规路径下用户的 zoom/平移完全保留；Head 仍在视口内（拖完不会丢）。
func TestRelGraphDragReleaseKeepsTreeInView(t *testing.T) {
	u, st, q := relGraphFixture(t)
	g := st.graph
	sx, li := relAnchorScreen(t, g)
	ox, oy := g.offX, g.offY
	q.Queue(relPointer(pointer.Press, sx, true))
	relGFrame(q, u, st)
	to := f32.Pt(sx.X+300, sx.Y+200)
	q.Queue(relPointer(pointer.Move, to, true))
	relGFrame(q, u, st)
	if g.dragNode != li {
		t.Fatalf("未拖到节点: %d", g.dragNode)
	}
	q.Queue(relPointer(pointer.Release, to, false))
	relGFrame(q, u, st) // Release → releaseDrag（撤 pin、唤醒物理）
	relDriveQuiet(q, u, st, relSettleFrames*2)
	if g.lay == nil || !g.lay.settled {
		t.Fatal("松手后物理未收敛")
	}
	// 视场未被松手重置（跟随的收敛尾巴会为把 Head 送回基准位而缓动，故判 3px 级不变）
	if math.Abs(g.offX-ox) > 3 || math.Abs(g.offY-oy) > 3 {
		t.Fatalf("松手重置了视场: (%v,%v) → (%v,%v)", ox, oy, g.offX, g.offY)
	}
	sx, _ = relAnchorScreen(t, g)
	if sx.X < 0 || float64(sx.X) > g.viewW || sx.Y < 0 || float64(sx.Y) > g.viewH {
		t.Fatalf("Head 松手后仍在视口外: (%v,%v)", sx.X, sx.Y)
	}
}

// TestRelGraphDragReleaseKeepsUserView 拖后松手**不重置视场**（D130）：用户 2026-10-12
// 「不管视角被移动到哪，拖动任意节点时都会回到这个样子」。原契约是 recenterTree 把树盒心
// 对准视口中心（D121）——与开窗 fit 同路径，松手构图恒等于开窗构图。现在松手只在「画面
// 上一个节点都不剩」时才兜底（那种情形抓不到节点，实际不可达），常规路径下用户的 zoom /
// 平移完全保留。
func TestRelGraphDragReleaseKeepsUserView(t *testing.T) {
	u, st, q := relGraphFixture(t)
	g := st.graph
	g.zoom, g.z = 0.6, 0.6*g.dpx // 压低 zoom：本夹具在 500×500 下 fit 档位装不下整树
	ox, oy := g.offX, g.offY
	sx, li := relAnchorScreen(t, g)
	q.Queue(relPointer(pointer.Press, sx, true))
	relGFrame(q, u, st)
	to := f32.Pt(sx.X+120, sx.Y+80)
	q.Queue(relPointer(pointer.Move, to, true))
	relGFrame(q, u, st)
	if g.dragNode != li {
		t.Fatalf("未拖到节点: %d", g.dragNode)
	}
	q.Queue(relPointer(pointer.Release, to, false))
	relGFrame(q, u, st)
	relDriveQuiet(q, u, st, relSettleFrames*2)
	if g.lay == nil || !g.lay.settled {
		t.Fatal("松手后物理未收敛")
	}
	if g.offX != ox || g.offY != oy {
		t.Fatalf("松手重置了视场: (%v,%v) → (%v,%v)", ox, oy, g.offX, g.offY)
	}
	if g.zoom != 0.6 {
		t.Fatalf("松手重置了 zoom: %v, want 0.6", g.zoom)
	}
}

func TestRelGraphFitSizeDriven(t *testing.T) {
	u := newHistUI(fakeRelTree{g: relFixtureGraph()}, fakeLister(nil))
	st := newHistoryState(u)
	q := new(input.Router)
	frame := func() {
		gtx, ops := frameGtxSize(q.Source(), 900, 700)
		st.graph.frame(gtx, u.th, u, st)
		q.Frame(ops)
	}
	frame()
	for i := 0; i < relSettleFrames*4 && st.graph.lay != nil &&
		(!st.graph.lay.settled || st.graph.fitFollow); i++ {
		frame()
	}
	g := st.graph
	if g.lay == nil || !g.lay.settled {
		t.Fatal("夹具物理未收敛")
	}
	if g.fitFollow {
		t.Fatal("视口跟随未收敛")
	}
	if g.zoom < relZoomMin || g.zoom > relZoomMax {
		t.Fatalf("fit zoom = %v，超出 A9 区间 [%v,%v]", g.zoom, relZoomMin, relZoomMax)
	}
	// 装得下（长会话触底才有例外）：所有可见卡片在视口内（D140：按**当前盒**收口）
	if _, minY, _, maxY := relCurBox(g); (maxY-minY)*g.z <= g.viewH && g.zoom > relZoomMin+1e-9 {
		for li := range st.vis.nodes {
			if relCardOutOfView(g, li) {
				t.Fatalf("节点 %d 未被装进视口（zoom %.3f），视口 %v×%v", li, g.zoom, g.viewW, g.viewH)
			}
		}
	}
}

// ——— D140：视场契约 v2（盒优先）———
//
// 用户 2026-10-15 报障（截图）：开窗后从「折叠播种」舒展到平衡态时**末端节点一动不动**
// （相机把它钉在右基准位上 ⇒ 整树的位移被相机完全抵消），而树向四周舒展 ⇒ 顶端被裁、
// 下方大片空白。实测（500×500 视口、源档；`go test -run TestZZD140Measure -v`）：
//
//	fixture7  静息盒 879×208  → 落定盒 873×403（纵向 1.94×）  首帧 zoom 0.488 / 落定需 0.573
//	wide12x3  静息盒 879×1165 → 落定盒 976×1781（纵向 1.53×） 上留白 −461px、38 节点出 27
//	chain120  静息盒 65 高    → 落定盒 200 高（3.06×）          120 节点出 97（zoom 触底）
//
// 新口径（D140）：zoom 按**当前盒**收口（不是静息盒）、纵向**盒心**居中（不是 Head 居中）、
// Head 只在装得下时落右基准位（D7-b 去掉了「过去 ≤ 基准位」那条 zoom 约束，换来两维尽量
// 贴满）。下面三条断言 = 新不变量 V1a（盒内可见）/ V1b（上下留白对称）/ V1c（至少一维贴满）。
