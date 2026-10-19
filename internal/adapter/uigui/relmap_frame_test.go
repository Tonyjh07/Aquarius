package uigui

// relmap_frame_test.go 右图自绘/手势/菜单（D120⑦）：headless 离屏帧 + input.Router
// 事件回放（§15.5，零网络零窗口）。覆盖 A8（锚点居中）、A9（缩放钳制 + 越界降级平移）、
// A10（点节点 → /goto）、悬停/右键菜单、拖拽平移、预算控件。

import (
	"fmt"
	"image"
	"math"
	"testing"
	"time"

	"gioui.org/f32"
	"gioui.org/gpu/headless"
	"gioui.org/io/input"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/unit"

	"github.com/Tonyjh07/Aquarius/internal/adapter/uigui/platform"
	"github.com/Tonyjh07/Aquarius/internal/domain/conversation"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

// relGraphFixture 右图测试夹具：固定图 + 全预算模型（本地索引 = 模型索引；锚点 u2 = 6）。
// 首帧后把物理跑完（从折叠播种展开至静止）**并等视口跟随收敛**（D128 起视口跟随本身
// 是一段有界滞后的动画，只等 lay.settled 会拿到差几十 px 的半截视场）——交互测试需要
// 一个**已收敛的静态基线**。
func relGraphFixture(t *testing.T) (*UI, *historyState, *input.Router) {
	t.Helper()
	texts := make(map[conversation.MessageID]string)
	for _, n := range relFixtureGraph().Nodes {
		texts[n.ID] = relFixtureBody(n.ID)
	}
	u := newHistUI(fakeRelTree{g: relFixtureGraph(), texts: texts}, fakeLister(nil))
	st := newHistoryState(u)
	q := new(input.Router)
	relGFrame(q, u, st)
	relDriveQuiet(q, u, st, relSettleFrames*3)
	if st.graph.lay == nil || !st.graph.lay.settled {
		t.Fatal("夹具物理未收敛")
	}
	return u, st, q
}

// relDriveQuiet 驱动到「物理静止 **且** 视口跟随收敛」（D128）。fitFollow 未收敛时继续
// 驱动——app 侧由 tickLayout 续帧对应（见 tickLayout 的 fitFollow 分支）。
func relDriveQuiet(q *input.Router, u *UI, st *historyState, max int) {
	for i := 0; i < max; i++ {
		relGFrame(q, u, st)
		if st.graph.lay != nil && st.graph.lay.settled && !st.graph.fitFollow {
			return
		}
	}
}

// relGFrame 一帧右图（500×500 视口）+ 提交 hit 树。
func relGFrame(q *input.Router, u *UI, st *historyState) {
	gtx, ops := frameGtxSize(q.Source(), 500, 500)
	st.graph.frame(gtx, u.th, u, st)
	q.Frame(ops)
}

// relPointer 鼠标事件（ID=7；primary = 按压中）。
func relPointer(kind pointer.Kind, p f32.Point, primary bool) pointer.Event {
	e := pointer.Event{Kind: kind, Position: p, PointerID: 7, Source: pointer.Mouse}
	if primary {
		e.Buttons = pointer.ButtonPrimary
	}
	return e
}

// relLongSnippetGraph 长摘要会话树（D126 多行卡片回归用）：摘要 40 字程 CJK，逼出多行
// 标签；a1 既是锚点（w3 最大徽标）又是分叉位点（版本下标 "1" + 虚线外环）。
func relLongSnippetGraph() port.TreeGraph {
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	at := func(i int) time.Time { return base.Add(time.Duration(i) * time.Second) }
	const long = "这是一段比较长的消息摘要文字用来验证多行卡片与虚线外环的绘制是否对齐徽标中心"
	mk := func(id, parent string, role conversation.Role, sibIdx, sibCount int) port.GraphNode {
		return port.GraphNode{
			ID: conversation.MessageID(id), Parent: conversation.MessageID(parent),
			Role: role, Snippet: long, EdgeKind: port.EdgeSeq,
			SiblingIdx: sibIdx, SiblingCount: sibCount,
		}
	}
	nodes := []port.GraphNode{
		mk("r", "", conversation.RoleRoot, 0, 1),
		mk("p", "r", conversation.RoleSystem, 0, 1),
		mk("u1", "p", conversation.RoleUser, 0, 2),
		mk("u1b", "p", conversation.RoleUser, 1, 2),
		mk("a1", "u1", conversation.RoleAssistant, 0, 2), // 锚点 + 分叉位点
		mk("u2", "a1", conversation.RoleUser, 0, 1),
	}
	for i := range nodes {
		nodes[i].CreatedAt = at(i)
	}
	return port.TreeGraph{Anchor: "a1", Nodes: nodes, Total: len(nodes)}
}

// TestRelDashedSquareOutlineHugsBadge D126 实测修：方形节点的虚线外环，四条**直边必须
// 贴 ±r**（= 填充圆角方的边所在；弧心列在 ±(r−cr)）。旧版把直边画在 r−cr 上，比徽标
// 内缩一个角半径（≈0.28r，实测 ~6.8px）——左右虚线画进节点里，读作「虚线框嵌在节点中」
// （用户截图报障）。弧不受影响。
func TestRelDashedSquareOutlineHugsBadge(t *testing.T) {
	const r = 24.0
	step := (relDashOn + relDashOff) * 1.25
	pts := shapeOutline(relShapeSquare, 100, 100, r, step)
	if len(pts) < 16 {
		t.Fatalf("方形外环采样点太少: %d", len(pts))
	}
	minD, maxD := math.Inf(1), 0.0
	for _, p := range pts {
		d := math.Hypot(float64(p.X-100), float64(p.Y-100))
		minD, maxD = math.Min(minD, d), math.Max(maxD, d)
	}
	// 环是「半径 r 的圆角方」：最近点 = 直边中点（恰在 r 上），最远点 = 角部弧。
	// 旧版 minD ≈ r−cr（≈r−6.8）——虚线落在徽标内部。
	if minD < r-0.01 {
		t.Fatalf("虚线外环有距心 %.2f < 环半径 %.2f 的点（直边画进徽标了）", minD, r)
	}
	if maxD < r {
		t.Fatalf("虚线外环没有到环半径: maxD = %.2f, want ≥ %.2f", maxD, r)
	}
	// 四边都得贴边：每个方向都要有 ≥ r−0.01 的采样点（否则「某一侧凹进去」测不出）
	for _, dir := range [][2]float64{{1, 0}, {-1, 0}, {0, 1}, {0, -1}} {
		best := 0.0
		for _, p := range pts {
			d := (float64(p.X)-100)*dir[0] + (float64(p.Y)-100)*dir[1]
			if d > best {
				best = d
			}
		}
		if best < r-0.01 {
			t.Fatalf("方向 %v 的虚线未贴到 ±r: 最远 %.2f < %.2f", dir, best, r)
		}
	}
}

// TestRelCardLabelMultiLine D126「多行占满卡片」：长摘要换行成多行、卡片随之撑高，
// 徽标仍在卡片内垂直居中；短摘要仍是一行、卡片不比徽标高（不制造空白）。
func TestRelCardLabelMultiLine(t *testing.T) {
	u := newHistUI(fakeRelTree{g: relLongSnippetGraph()}, fakeLister(nil))
	st := newHistoryState(u)
	g := st.graph
	li := g.anchor
	gi := st.vis.nodes[li]
	if relLabelText(st.model, gi, g.zoom) == "" {
		t.Fatal("锚点应有标签")
	}
	xs, ys := g.toScreen(g.sol.Pos[li])
	r := float32(g.nodeRadius(&st.model.nodes[gi]) * g.z)
	bare := relBadgeBox(xs, ys, r)
	box := g.nodeCardBox(gi, xs, ys)
	if _, _, lines := g.labelBlock(relLabelText(st.model, gi, g.zoom)); lines < 2 {
		t.Fatal("长摘要应换到多行")
	}
	if box.Dy() <= bare.Dy() {
		t.Fatalf("多行卡片未撑高: 卡高 %d ≤ 徽标高 %d", box.Dy(), bare.Dy())
	}
	// 徽标仍在卡内垂直居中（卡片以节点坐标为竖直中心）
	if c := float64(box.Min.Y+box.Max.Y) / 2; math.Abs(c-float64(ys)) > 1.5 {
		t.Fatalf("多行卡片未以节点为竖直中心: 卡心 %.1f, 节点 y %.1f", c, ys)
	}
	// 短摘要（夹具图 "s-a1"）保持一行、卡片不比徽标高
	_, st2, _ := relGraphFixture(t)
	g2 := st2.graph
	gi2 := st2.vis.nodes[g2.anchor]
	x2, y2 := g2.toScreen(g2.sol.Pos[g2.anchor])
	b2 := relBadgeBox(x2, y2, float32(g2.nodeRadius(&st2.model.nodes[gi2])*g2.z))
	box2 := g2.nodeCardBox(gi2, x2, y2)
	if _, _, lines := g2.labelBlock(relLabelText(st2.model, gi2, g2.zoom)); lines != 1 {
		t.Fatalf("短摘要应占 1 行, got %d", lines)
	}
	// 卡片高度 = max(徽标, 标签块) + 上下内边距（labelFont 有 10sp 下限：zoom 够低时
	// 一行文字本身就比徽标高——D130 把 fit 地板放到 relZoomMin 后这条路径常态可达）。
	txt2 := relLabelText(st2.model, gi2, g2.zoom)
	_, bh, _ := g2.labelBlock(txt2)
	want2 := b2.Dy()
	if txt2 != "" { // 标签显形：max(徽标, 标签块) + 上下内边距（D130 起 fit 可把树压到
		// 标签全隐的 zoom ⇒ 卡片塌缩为裸徽标，这条分支是「标签还在」时的契约）
		want2 = int(math.Max(float64(b2.Dy()), float64(bh))) + 2*int(relCardPadV*g2.z)
	}
	if math.Abs(float64(box2.Dy())-float64(want2)) > 2 {
		t.Fatalf("短摘要卡片高度不符: %d vs %d（txt=%q）", box2.Dy(), want2, txt2)
	}
}

// TestRelCardHalfCoversDrawnCard D126：布局预留（HalfW/HalfH，relGeo）必须覆盖**任何
// zoom 下**画出来的卡片——否则卡片画出自己的槽位（邻卡压字）且视场适配漏算边。
// 估宽/估高都是近似，故按标签 zoom 全程扫描（下限处字号被钳、盒宽随 z 缩小：最坏情形）。
func TestRelCardHalfCoversDrawnCard(t *testing.T) {
	u := newHistUI(fakeRelTree{g: relLongSnippetGraph()}, fakeLister(nil))
	st := newHistoryState(u)
	g := st.graph
	for _, zoom := range []float64{relZoomLabelMin, 0.85, 1.0, 1.2, relZoomLabelRef, relZoomMax} {
		g.zoom, g.z = zoom, zoom*g.dpx
		for li, gi := range st.vis.nodes {
			xs, ys := g.toScreen(g.sol.Pos[li])
			box := g.nodeCardBox(gi, xs, ys)
			hw := float64(box.Dx()) / 2 / g.z
			hh := float64(box.Dy()) / 2 / g.z
			if hw > g.geo[li].HalfW+0.5 {
				t.Fatalf("zoom %.2f 节点 %d 卡片半宽 %.1f > 预留 %.1f", zoom, li, hw, g.geo[li].HalfW)
			}
			if hh > g.geo[li].HalfH+0.5 {
				t.Fatalf("zoom %.2f 节点 %d 卡片半高 %.1f > 预留 %.1f", zoom, li, hh, g.geo[li].HalfH)
			}
		}
	}
}

// TestRelGlyphCenteredInBadge D126 实测修：版本下标（白字）必须画在形状**正中**。旧版
// 用 `Constraints.Min=(0,0)` + layout.Center 排版——Gio 的 Center 按 max(Min, 子件尺寸)
// 定位，Min 为 0 等于不平移，数字贴在形状左上角（用户报障「数字居中问题」）。
// 这里渲染离屏帧后逐像素验证：白字包围盒中心 ≈ 徽标中心。
func TestRelGlyphCenteredInBadge(t *testing.T) {
	applyPalette(lightPalette())
	u := newHistUI(fakeRelTree{g: relLongSnippetGraph()}, fakeLister(nil))
	st := newHistoryState(u)
	g := st.graph
	// 先离线把物理跑到静止（软物理下 ~10s 才落定；不收敛就渲染 = 拍一个运动中的帧，
	// 之后读到的坐标与画出来的对不上）。headless 只渲染最后那一帧。
	q := new(input.Router)
	for i := 0; i < relSettleFrames*2 && g.lay != nil && !g.lay.settled; i++ {
		relGFrame(q, u, st)
	}
	if g.lay == nil || !g.lay.settled {
		t.Fatal("夹具物理未收敛")
	}
	w, err := headless.NewWindow(500, 500)
	if err != nil {
		t.Skipf("离屏渲染不可用: %v", err)
	}
	defer w.Release()
	var ops op.Ops
	gtx := layout.Context{Ops: &ops, Source: input.Source{},
		Metric:      unit.Metric{PxPerDp: 1.25, PxPerSp: 1.25},
		Constraints: layout.Exact(image.Pt(500, 500))}
	g.frame(gtx, u.th, u, st)
	if err := w.Frame(&ops); err != nil {
		t.Fatalf("渲染失败: %v", err)
	}
	img := image.NewRGBA(image.Rect(0, 0, 500, 500))
	if err := w.Screenshot(img); err != nil {
		t.Fatalf("截图失败: %v", err)
	}
	// 锚点徽标盒（屏幕坐标）
	at, li := relAnchorScreen(t, g)
	gi := st.vis.nodes[li]
	r := float32(g.nodeRadius(&st.model.nodes[gi]) * g.z)
	badge := relBadgeBox(at.X, at.Y, r)
	bx, by := float64(badge.Min.X+badge.Max.X)/2, float64(badge.Min.Y+badge.Max.Y)/2
	// 白字像素（只取徽标内切圆内：盒角区域可能漏出边线/主干描边的窗底色，不是字形）。
	// **像素质心**而非包围盒中心（D126 实测修）：数字字形不对称（「1」的顶部横旗向左
	// 伸出 ~8px），包围盒中心会偏出视觉中心 3.9px——质心以笔画质量加权，量的是「看起来
	// 在不在正中」。
	var sx, sy float64
	n := 0
	inner := float64(r) * 0.9
	for y := badge.Min.Y; y < badge.Max.Y; y++ {
		for x := badge.Min.X; x < badge.Max.X; x++ {
			if math.Hypot(float64(x)-bx, float64(y)-by) > inner {
				continue
			}
			c := img.RGBAAt(x, y)
			if c.R >= 0xF0 && c.G >= 0xF0 && c.B >= 0xF0 {
				sx += float64(x)
				sy += float64(y)
				n++
			}
		}
	}
	if n < 6 {
		t.Fatalf("徽标内没找到白字像素（%d）——字形没画在形状里？", n)
	}
	cx, cy := sx/float64(n), sy/float64(n)
	if math.Abs(cx-bx) > 2.5 || math.Abs(cy-by) > 2.5 {
		t.Fatalf("版本下标未居中: 字心 (%.1f,%.1f), 徽标心 (%.1f,%.1f)", cx, cy, bx, by)
	}
}

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

// relWideGraph 宽树夹具：root → system → fan 个 user 分叉，每分叉再挂 depth 级链。
// 源档按「距父 100dp」拉、不看静息槽位 ⇒ 落定盒比静息盒**纵向**大一大截（宽树的病态档）。
func relWideGraph(fan, depth int) port.TreeGraph {
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	n := 0
	at := func() time.Time { n++; return base.Add(time.Duration(n) * time.Second) }
	var nodes []port.GraphNode
	mk := func(id, parent string, role conversation.Role, sibIdx, sibCount int) {
		nodes = append(nodes, port.GraphNode{
			ID: conversation.MessageID(id), Parent: conversation.MessageID(parent),
			Role: role, Snippet: "s-" + id, EdgeKind: port.EdgeSeq,
			SiblingIdx: sibIdx, SiblingCount: sibCount, CreatedAt: at(),
		})
	}
	mk("r", "", conversation.RoleRoot, 0, 1)
	mk("p", "r", conversation.RoleSystem, 0, 1)
	last := "p"
	for i := 0; i < fan; i++ {
		id := fmt.Sprintf("b%d", i)
		mk(id, "p", conversation.RoleUser, i, fan)
		cur := id
		for d := 1; d < depth; d++ {
			nid := fmt.Sprintf("%s_%d", id, d)
			role := conversation.RoleAssistant
			if d%2 == 1 {
				role = conversation.RoleUser
			}
			mk(nid, cur, role, 0, 1)
			cur = nid
		}
		last = cur
	}
	return port.TreeGraph{Anchor: conversation.MessageID(last), Nodes: nodes, Total: len(nodes)}
}

// relBoxOf 给定本地索引集在当前物理位形下的卡片外接盒（含 relFitMargin）。测试侧**独立
// 复算**：被测口径就是「可见卡片都在视口内」，若测试与实现借同一个函数，算错法相同就测不出错。
func relBoxOf(g *relGraph, idx []int) (minX, minY, maxX, maxY float64) {
	minX, minY = math.Inf(1), math.Inf(1)
	maxX, maxY = math.Inf(-1), math.Inf(-1)
	for _, li := range idx {
		hw, hh := relCardHalf(g.geo[li])
		p := g.sol.Pos[li]
		minX, maxX = math.Min(minX, p.X-hw), math.Max(maxX, p.X+hw)
		minY, maxY = math.Min(minY, p.Y-hh), math.Max(maxY, p.Y+hh)
	}
	padX := (maxX - minX) * relFitMargin
	padY := (maxY - minY) * relFitMargin
	return minX - padX, minY - padY, maxX + padX, maxY + padY
}

// relCurBox 全可见树的当前盒。
func relCurBox(g *relGraph) (minX, minY, maxX, maxY float64) {
	idx := make([]int, len(g.vis.nodes))
	for i := range idx {
		idx[i] = i
	}
	return relBoxOf(g, idx)
}

// relCardOutOfView 该可见节点的卡片盒是否越出视口（±1px 容差）。
func relCardOutOfView(g *relGraph, li int) bool {
	if g.sol == nil || li < 0 || li >= len(g.sol.Pos) {
		return true
	}
	xs, ys := g.toScreen(g.sol.Pos[li])
	hw, hh := relCardHalf(g.geo[li])
	x, y := float64(xs), float64(ys)
	return x-hw*g.z < -1 || x+hw*g.z > g.viewW+1 || y-hh*g.z < -1 || y+hh*g.z > g.viewH+1
}

// relViewGaps 盒相对视口的上/下留白（px；负 = 越界）。
func relViewGaps(g *relGraph) (top, bottom float64) {
	_, minY, _, maxY := relCurBox(g)
	return minY*g.z + g.offY, g.viewH - (maxY*g.z + g.offY)
}

// relFill 给定盒在视口里的贴满度（两维取大；1 = 恰有一维贴满视口）。
func relFill(g *relGraph, minX, minY, maxX, maxY float64) float64 {
	return math.Max((maxX-minX)*g.z/g.viewW, (maxY-minY)*g.z/g.viewH)
}

// relFillOf 给定索引集域盒的贴满度。
func relFillOf(g *relGraph, idx []int) float64 {
	minX, minY, maxX, maxY := relBoxOf(g, idx)
	return relFill(g, minX, minY, maxX, maxY)
}

// relBoxFill 全可见树盒的贴满度。
func relBoxFill(g *relGraph) float64 {
	minX, minY, maxX, maxY := relCurBox(g)
	return relFill(g, minX, minY, maxX, maxY)
}

// relDriveDone 驱动到「物理静止 **且** 视口跟随收敛」，收敛不了直接失败（跟随不许空转）。
func relDriveDone(t *testing.T, q *input.Router, u *UI, st *historyState) {
	t.Helper()
	relDriveQuiet(q, u, st, relSettleFrames*4)
	if st.graph.lay == nil || !st.graph.lay.settled {
		t.Fatal("物理未收敛")
	}
	if st.graph.fitFollow {
		t.Fatal("视口跟随未收敛（空转重绘）")
	}
}

// TestRelGraphSettledBoxStaysInView V1a/V2（D140 主复现）：舒展落定后，可见节点的卡片必须
// **全在视口内**；只有 zoom 触底（relZoomMin，超长会话）才退化为「Head 在场」（V2）。
// 修前：wide12x3 38 节点出 27、上越界 461px（用户截图那一幕）。
func TestRelGraphSettledBoxStaysInView(t *testing.T) {
	cases := []struct {
		name string
		g    port.TreeGraph
	}{
		{"fixture7", relFixtureGraph()},
		{"wide12x3", relWideGraph(12, 3)},
		{"wide24x1", relWideGraph(24, 1)},
		{"chain120", relChainGraph(120)}, // zoom 触底档：只承诺 V2
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			u := newHistUI(fakeRelTree{g: c.g}, fakeLister(nil))
			st := newHistoryState(u)
			q := new(input.Router)
			relGFrame(q, u, st)
			relDriveDone(t, q, u, st)
			g := st.graph
			floor := g.zoom <= relZoomMin+1e-9
			for li := range st.vis.nodes {
				if !relCardOutOfView(g, li) || floor {
					continue
				}
				xs, ys := g.toScreen(g.sol.Pos[li])
				t.Fatalf("落定后节点 %d 出视口 (%v,%v)，视口 %v×%v（zoom %.3f）",
					li, xs, ys, g.viewW, g.viewH, g.zoom)
			}
			if li := g.anchor; li >= 0 && li < len(st.vis.nodes) { // V2：Head 恒在场
				if relCardOutOfView(g, li) {
					xs, ys := g.toScreen(g.sol.Pos[li])
					t.Fatalf("落定后 Head 出视口 (%v,%v)，视口 %v×%v", xs, ys, g.viewW, g.viewH)
				}
			}
		})
	}
}

// TestRelGraphSettledViewBalanced V1b/V1c（D140；用户「舒展后下方空白小一点」）：盒装得下
// 时上下留白必须**对称**（P2 盒心居中；修前 fixture7 是 225/78、wide12x3 是 −461/197），
// 且未触上下限时至少一维贴满（zoom 按当前盒收口，不是按静息盒）。
func TestRelGraphSettledViewBalanced(t *testing.T) {
	cases := []struct {
		name string
		g    port.TreeGraph
	}{
		{"fixture7", relFixtureGraph()},
		{"wide12x3", relWideGraph(12, 3)},
		{"wide24x1", relWideGraph(24, 1)},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			u := newHistUI(fakeRelTree{g: c.g}, fakeLister(nil))
			st := newHistoryState(u)
			q := new(input.Router)
			relGFrame(q, u, st)
			relDriveDone(t, q, u, st)
			g := st.graph
			_, minY, _, maxY := relCurBox(g)
			if (maxY-minY)*g.z > g.viewH {
				return // 纵向装不下（触底档）：留白无从谈起
			}
			top, bot := relViewGaps(g)
			if top < -1 || bot < -1 {
				t.Fatalf("纵向越界：上留白 %.0f 下留白 %.0f（视口高 %.0f）", top, bot, g.viewH)
			}
			if math.Abs(top-bot) > 8 {
				t.Fatalf("上下留白不对称：上 %.0f 下 %.0f（P2 应盒心居中）", top, bot)
			}
			if g.zoom > relZoomMin+1e-9 && g.zoom < relZoomMax-1e-9 {
				if f := relBoxFill(g); f < 0.99 {
					t.Fatalf("两维都没贴满（贴满度 %.2f）：zoom 没按当前盒收口", f)
				}
			}
		})
	}
}

// TestRelGraphFollowZoomNoBounce D2 改订（D140）：展开期（物理未静止）zoom **单调不增**
// ——盒子只长 ⇒ 只缩，不会有「放大—缩回」的来回摆动（抖动会让人眼盯不住树）。落定后允许
// 放大一次把留白填掉（V1c），故判据只在 settled 之前生效。
func TestRelGraphFollowZoomNoBounce(t *testing.T) {
	u := newHistUI(fakeRelTree{g: relWideGraph(12, 3)}, fakeLister(nil))
	st := newHistoryState(u)
	q := new(input.Router)
	relGFrame(q, u, st)
	g := st.graph
	prev := g.zoom
	expanding := false
	for i := 0; i < relSettleFrames*4; i++ {
		relGFrame(q, u, st)
		if g.lay == nil || g.lay.settled {
			break // 落定帧起的「放大填留白」不算回弹
		}
		expanding = true
		if g.zoom > prev+1e-9 {
			t.Fatalf("展开期 zoom 回弹（第 %d 帧）：%.4f → %.4f", i, prev, g.zoom)
		}
		prev = g.zoom
	}
	if !expanding {
		t.Fatal("夹具没经历展开期（测试失效）")
	}
}

// TestRelGraphLabelFontClamp 字号钳制（A9）：clamp(12×zoom, 10, 16)。
func TestRelGraphLabelFontClamp(t *testing.T) {
	g := &relGraph{zoom: 0.5}
	if g.labelFont() != 10 {
		t.Fatalf("zoom0.5 字号 = %v, want 10", g.labelFont())
	}
	g.zoom = 1.0
	if g.labelFont() != 12 {
		t.Fatalf("zoom1.0 字号 = %v, want 12", g.labelFont())
	}
	g.zoom = 1.5
	if g.labelFont() != 16 {
		t.Fatalf("zoom1.5 字号 = %v, want 16", g.labelFont())
	}
}

// relFixtureBody 夹具节点的正文全文（D132）：刻意长得跟 40 字摘要（"s-<id>"）不同，
// 断言「拿到的是全文不是摘要」一眼可辨。
func relFixtureBody(id conversation.MessageID) string {
	return "【全文】" + string(id) + "：这一段正文比节点摘要长得多，用来验证编辑预填取的是全文。"
}

// relRightPress / relRightReleaseAt 右键按下 / 原位抬起（本地索引 li 的屏幕位置）。
func relRightPress(st *historyState, li int) pointer.Event {
	g := st.graph
	xs, ys := g.toScreen(g.sol.Pos[li])
	return pointer.Event{Kind: pointer.Press, Position: f32.Pt(xs, ys), PointerID: 7,
		Source: pointer.Mouse, Buttons: pointer.ButtonSecondary}
}

func relRightReleaseAt(st *historyState, li int) pointer.Event {
	e := relRightPress(st, li)
	e.Kind = pointer.Release
	e.Buttons = 0
	return e
}

// relNodeIndex 本地索引 li（st.vis.nodes[li]）满足模型节点 id 命中（全预算夹具里节点全可见）。
func relNodeIndex(t *testing.T, st *historyState, id conversation.MessageID) int {
	t.Helper()
	for li, mi := range st.vis.nodes {
		if st.model.nodes[mi].id == id {
			return li
		}
	}
	t.Fatalf("节点 %s 不在可见集", id)
	return -1
}

// relDrainAs 从收件箱取出指定类型的桥接消息（菜单分发经 post 投递，§15.6）。
func relDrainAs(t *testing.T, u *UI, dst any) bool {
	t.Helper()
	for {
		select {
		case m := <-u.inbox:
			switch d := dst.(type) {
			case *editMsg:
				if v, ok := m.(editMsg); ok {
					*d = v
					return true
				}
			case *inputMsg:
				if v, ok := m.(inputMsg); ok {
					*d = v
					return true
				}
			case *quoteMsg:
				if v, ok := m.(quoteMsg); ok {
					*d = v
					return true
				}
			case *showExpandMsg:
				if v, ok := m.(showExpandMsg); ok {
					*d = v
					return true
				}
			}
		default:
			return false
		}
	}
}

// TestRelGraphRightClickMenu 图节点右键 → 菜单上下文（D132：与主界面气泡菜单同一呈现/
// 分发，项集 = 气泡项 + 图侧三项）：右键按下武装、**原位抬起**才 fire（菜单不在按键未
// 松时弹出，免得吃掉这次抬起）；上下文 id = 节点、正文取全文（不是 40 字摘要）。
func TestRelGraphRightClickMenu(t *testing.T) {
	u, st, q := relGraphFixture(t)
	g := st.graph
	sx, li := relAnchorScreen(t, g)
	_ = li
	want := st.model.nodes[st.vis.nodes[g.anchor]].id
	if ctx := u.bubbleMenu.Load(); ctx != nil {
		t.Fatal("夹具不应残留菜单上下文")
	}
	// 右键按下（secondary）：只武装，不出菜单
	q.Queue(pointer.Event{Kind: pointer.Press, Position: sx, PointerID: 7,
		Source: pointer.Mouse, Buttons: pointer.ButtonSecondary})
	relGFrame(q, u, st)
	if !g.menuArm || g.menuNode != g.anchor {
		t.Fatalf("按下未武装: arm=%v node=%d, want true/%d", g.menuArm, g.menuNode, g.anchor)
	}
	if u.bubbleMenu.Load() != nil {
		t.Fatal("右键按下就出菜单（应为抬起才 fire）")
	}
	// 原位抬起 → 菜单上下文就位
	q.Queue(pointer.Event{Kind: pointer.Release, Position: sx, PointerID: 7,
		Source: pointer.Mouse})
	relGFrame(q, u, st)
	ctx := u.bubbleMenu.Load()
	if ctx == nil {
		t.Fatal("右键抬起未请求菜单")
	}
	if ctx.id != want || !ctx.graph {
		t.Fatalf("菜单上下文 = id %q graph=%v, want %q/true", ctx.id, ctx.graph, want)
	}
	if ctx.edit != relFixtureBody(want) {
		t.Fatalf("编辑预填 = %q, want 节点正文全文 %q（不是摘要）", ctx.edit, relFixtureBody(want))
	}
	if g.menuArm {
		t.Fatal("fire 后武装未解除")
	}
	// 项集 = 气泡七项 + 分隔线 + 图侧四项（D139 加「转到」；D152 加「插入摘要」）
	items := u.menuItems(platform.MenuBubble)
	wantLabels := []string{"编辑", "编辑并转移历史", "编辑并复制历史", "重新生成", "复制",
		"引用", "查看原文", "", "复制 ID", "插入摘要", "删除该节点及子树", "转到"}
	if len(items) != len(wantLabels) {
		t.Fatalf("项数 = %d, want %d", len(items), len(wantLabels))
	}
	for i, w := range wantLabels {
		if items[i].Label != w {
			t.Fatalf("第 %d 项 = %q, want %q", i, items[i].Label, w)
		}
	}
	if !ctx.canSummary {
		t.Fatal("普通节点（非根/非人格）应可插摘要")
	}
}

// TestRelGraphRightClickNoSummaryForRootAndPersona 会话根与会话首节点（人格）不出
// 「插入摘要」：根前无处可插；人格之前插摘要会让摘要顶到路径第二位、被装配当成 persona。
func TestRelGraphRightClickNoSummaryForRootAndPersona(t *testing.T) {
	u, st, q := relGraphFixture(t)
	for _, id := range []conversation.MessageID{"r", "p"} {
		li := relNodeIndex(t, st, id)
		q.Queue(relRightPress(st, li))
		q.Queue(relRightReleaseAt(st, li))
		relGFrame(q, u, st)
		ctx := u.bubbleMenu.Load()
		if ctx == nil || ctx.id != id {
			t.Fatalf("节点 %s 右键未出上下文: %+v", id, ctx)
		}
		if ctx.canSummary {
			t.Fatalf("节点 %s（kind=%v）不应可插摘要", id, ctx.kind)
		}
		for _, it := range u.menuItems(platform.MenuBubble) {
			if it.Label == "插入摘要" {
				t.Fatalf("节点 %s 的菜单不该有「插入摘要」", id)
			}
		}
		u.bubbleMenu.Store(nil)
	}
}

// TestRelGraphRightClickNoMenuOnDragAway 右键按下后**移开抬起**（非原位）→ 不出菜单。
func TestRelGraphRightClickNoMenuOnDragAway(t *testing.T) {
	u, st, q := relGraphFixture(t)
	sx, _ := relAnchorScreen(t, st.graph)
	q.Queue(pointer.Event{Kind: pointer.Press, Position: sx, PointerID: 7,
		Source: pointer.Mouse, Buttons: pointer.ButtonSecondary})
	relGFrame(q, u, st)
	q.Queue(pointer.Event{Kind: pointer.Release, Position: f32.Pt(sx.X+260, sx.Y+180), PointerID: 7,
		Source: pointer.Mouse})
	relGFrame(q, u, st)
	if u.bubbleMenu.Load() != nil {
		t.Fatal("非原位抬起不应出菜单")
	}
}

// TestRelGraphMenuEditOpensOverlayWithFullText 菜单「编辑并转移历史」→ **开遮罩并预选
// Carry**（D139：不再投主窗 editMsg）；正文在遮罩内经 NodeText 现取 = **全文**（D132 的
// 要点：摘要会截断消息）；part 缺省合段（遮罩提交不带 --part）。
func TestRelGraphMenuEditOpensOverlayWithFullText(t *testing.T) {
	u, st, q := relGraphFixture(t)
	const li = 4 // a1（assistant，正文块）
	wantID := st.model.nodes[st.vis.nodes[li]].id
	q.Queue(relRightPress(st, li))
	q.Queue(relRightReleaseAt(st, li))
	relGFrame(q, u, st)
	u.menuChosen(platform.MenuBubble, cmdBubbleEditKeep)

	act := u.nodeAction.Load()
	if act == nil || act.kind != nodeActOpenOverlay {
		t.Fatalf("图侧编辑未投开遮罩动作: %+v", act)
	}
	if act.id != wantID || act.mode != conversation.Carry || act.page != overlayPageRendered {
		t.Fatalf("开遮罩动作 = %+v, want id=%s Carry/呈现", act, wantID)
	}
	// 历史窗帧首排空 → 遮罩开，正文 = 全文（不是 40 字摘要）。
	histFrameFor(q, u, st)
	if st.ov == nil {
		t.Fatal("遮罩未开启")
	}
	if st.ov.src != relFixtureBody(wantID) {
		t.Fatalf("遮罩源码 = %q, want 全文 %q", st.ov.src, relFixtureBody(wantID))
	}
	if st.ov.mode != conversation.Carry || !st.ov.editable {
		t.Fatalf("遮罩态 = mode %v editable %v, want Carry/true", st.ov.mode, st.ov.editable)
	}
	// 图侧编辑**不得**再投 editMsg（否则跨窗又去主窗输入栏）。
	var got editMsg
	if relDrainAs(t, u, &got) {
		t.Fatal("图侧编辑不应再投 editMsg（D139 改走遮罩）")
	}
}

// TestRelGraphMenuRawOpensOverlaySourcePage 菜单「查看原文」→ 开遮罩并**定位源码页签**
// （D139：图侧不再另开 winRaw 次窗）。
func TestRelGraphMenuRawOpensOverlaySourcePage(t *testing.T) {
	u, st, q := relGraphFixture(t)
	const li = 4
	q.Queue(relRightPress(st, li))
	q.Queue(relRightReleaseAt(st, li))
	relGFrame(q, u, st)
	u.menuChosen(platform.MenuBubble, cmdBubbleRaw)
	act := u.nodeAction.Load()
	if act == nil || act.kind != nodeActOpenOverlay || act.page != overlayPageSource {
		t.Fatalf("查看原文动作 = %+v, want 开遮罩/源码页", act)
	}
	histFrameFor(q, u, st)
	if st.ov == nil || st.ov.page != overlayPageSource {
		t.Fatalf("遮罩页签 = %+v, want 源码页", st.ov)
	}
	if st.ov.ed.Text() == "" {
		t.Fatal("源码页编辑器未预填正文")
	}
}

// TestRelGraphMenuRegenAndQuote 菜单「重新生成」/「引用」与主界面同命令路径：/regen <id>
// 与 quoteMsg（引用文本 = 全文）。
func TestRelGraphMenuRegenAndQuote(t *testing.T) {
	u, st, q := relGraphFixture(t)
	const li = 4
	wantID := st.model.nodes[st.vis.nodes[li]].id
	q.Queue(relRightPress(st, li))
	q.Queue(relRightReleaseAt(st, li))
	relGFrame(q, u, st)
	u.menuChosen(platform.MenuBubble, cmdBubbleRegen)
	var in inputMsg
	if !relDrainAs(t, u, &in) {
		t.Fatal("重新生成未投 inputMsg")
	}
	if in.text != "/regen "+string(wantID) {
		t.Fatalf("inputMsg = %q, want /regen %s", in.text, wantID)
	}
	u.menuChosen(platform.MenuBubble, cmdBubbleQuote)
	var qm quoteMsg
	if !relDrainAs(t, u, &qm) {
		t.Fatal("引用未投 quoteMsg")
	}
	if qm.text != relFixtureBody(wantID) {
		t.Fatalf("引用文本 = %q, want 全文 %q", qm.text, relFixtureBody(wantID))
	}
}

// TestRelGraphMenuRootNodeReduced 根节点（Role=root，不可编辑）→ 项集只给复制/查看原文 +
// 图侧四项，不给编辑三方式与重新生成（D132：无正文可编辑的节点不给编辑类项）。
func TestRelGraphMenuRootNodeReduced(t *testing.T) {
	u, st, q := relGraphFixture(t)
	q.Queue(relRightPress(st, 0)) // 0 = 局部 r（结构根）
	q.Queue(relRightReleaseAt(st, 0))
	relGFrame(q, u, st)
	items := u.menuItems(platform.MenuBubble)
	want := []string{"复制", "查看原文", "", "复制 ID", "删除该节点及子树", "转到"}
	if len(items) != len(want) {
		t.Fatalf("根节点项数 = %d, want %d", len(items), len(want))
	}
	for i, w := range want {
		if items[i].Label != w {
			t.Fatalf("第 %d 项 = %q, want %q", i, items[i].Label, w)
		}
	}
}

// TestRelGraphMenuNoBodyNode 图侧取不到正文（NodeText 未命中）→ 只剩图侧项：不拿空
// 文本预填编辑框（D132 的 hasText 分支；内核也会拒空修订文本，但菜单不该诱导）。
func TestRelGraphMenuNoBodyNode(t *testing.T) {
	u := newHistUI(fakeRelTree{g: relFixtureGraph()}, fakeLister(nil)) // 不配 texts
	st := newHistoryState(u)
	q := new(input.Router)
	relGFrame(q, u, st)
	q.Queue(relRightPress(st, 4))
	q.Queue(relRightReleaseAt(st, 4))
	relGFrame(q, u, st)
	ctx := u.bubbleMenu.Load()
	if ctx == nil || ctx.copy != "" {
		t.Fatalf("无正文节点上下文 = %+v, want copy 为空", ctx)
	}
	items := u.menuItems(platform.MenuBubble)
	want := []string{"", "复制 ID", "插入摘要", "删除该节点及子树", "转到"}
	if len(items) != len(want) {
		t.Fatalf("无正文节点项数 = %d, want %d", len(items), len(want))
	}
	for i, w := range want {
		if items[i].Label != w {
			t.Fatalf("第 %d 项 = %q, want %q", i, items[i].Label, w)
		}
	}
}

// TestRelGraphMenuRm 菜单「删除」→ /rm <id> 经 inCh（与键入同路径；分发在 shell 线程，
// 故这里直接走 menuChosen）。
func TestRelGraphMenuRm(t *testing.T) {
	u, st, q := relGraphFixture(t)
	const li = 4
	want := st.model.nodes[st.vis.nodes[li]].id
	q.Queue(relRightPress(st, li))
	q.Queue(relRightReleaseAt(st, li))
	relGFrame(q, u, st)
	u.menuChosen(platform.MenuBubble, cmdNodeRm)
	select {
	case in := <-u.inCh:
		if in.Command == nil || in.Command.Name != "rm" ||
			len(in.Command.Args) != 1 || conversation.MessageID(in.Command.Args[0]) != want {
			t.Fatalf("inCh = %+v, want /rm %s", in, want)
		}
	default:
		t.Fatal("inCh 空：菜单删除未投 /rm")
	}
}

// TestRelGraphMenuCopyIDAction 图侧「复制 ID」走动作槽 → **历史窗帧**内落剪贴板
// （剪贴板写入必须在 Gio 帧内，分发处不能直连，§15.6）。
func TestRelGraphMenuCopyIDAction(t *testing.T) {
	u, st, q := relGraphFixture(t)
	const li = 4
	want := st.model.nodes[st.vis.nodes[li]].id
	q.Queue(relRightPress(st, li))
	q.Queue(relRightReleaseAt(st, li))
	relGFrame(q, u, st)

	u.menuChosen(platform.MenuBubble, cmdNodeCopyID)
	act := u.nodeAction.Load()
	if act == nil || act.kind != nodeActCopyID || act.id != want {
		t.Fatalf("动作槽 = %+v, want nodeActCopyID %s", act, want)
	}
	gtx, _ := frameGtxSize(new(input.Router).Source(), 720, 560)
	st.drainNodeAction(u, gtx)      // 复制 ID 的剪贴板写入在帧内执行（headless 无外部可观察量，
	if u.nodeAction.Load() != nil { // 故只断言「动作被消费、不重复执行」）
		t.Fatal("动作槽未排空")
	}
}

// TestRelBudgetControls 预算 −/＋ 钳制与「展开全部」（用 150 节点链验证非平凡预算）。
func TestRelBudgetControls(t *testing.T) {
	u := newHistUI(fakeRelTree{g: relChainGraph(150)}, fakeLister(nil))
	st := newHistoryState(u)
	if st.budget != relDefaultBudget {
		t.Fatalf("初始预算 = %d, want %d", st.budget, relDefaultBudget)
	}
	st.applyBudget(+10)
	if st.budget != relDefaultBudget+10 {
		t.Fatalf("＋ 后 budget = %d, want %d", st.budget, relDefaultBudget+10)
	}
	for i := 0; i < 100; i++ {
		st.applyBudget(-10)
	}
	if st.budget != 1 {
		t.Fatalf("− 应钳到 1，实为 %d", st.budget)
	}
	st.expandAll()
	if st.budget != 150 { // 150 < relHardCap → 全量
		t.Fatalf("展开全部 = %d, want 150", st.budget)
	}
	if len(st.vis.nodes) != 150 {
		t.Fatalf("展开全部呈现 = %d, want 150", len(st.vis.nodes))
	}
	_ = u
}

// relChainGraph n 节点链（根 + 依次顺接；锚点 = 末节点）——预算/大图测试用。
func relChainGraph(n int) port.TreeGraph {
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	nodes := make([]port.GraphNode, n)
	for i := 0; i < n; i++ {
		role := conversation.RoleAssistant
		if i%2 == 1 {
			role = conversation.RoleUser
		}
		if i == 0 {
			role = conversation.RoleRoot
		}
		parent := ""
		if i > 0 {
			parent = "n" + itoa(i-1)
		}
		nodes[i] = port.GraphNode{
			ID: conversation.MessageID("n" + itoa(i)), Parent: conversation.MessageID(parent),
			Role: role, Snippet: "s", CreatedAt: base.Add(time.Duration(i) * time.Second),
			EdgeKind: port.EdgeSeq,
		}
	}
	return port.TreeGraph{Anchor: conversation.MessageID("n" + itoa(n-1)), Nodes: nodes, Total: n}
}

// TestRelGraphRenderFullWindow 整窗渲染（含状态栏控件与图例）不 panic（离屏帧）。
func TestRelGraphRenderFullWindow(t *testing.T) {
	u, st, q := relGraphFixture(t)
	gtx, ops := frameGtxSize(q.Source(), 720, 560)
	historyFrame(gtx, u.th, u, st)
	q.Frame(ops)
}

// TestRelGraphBudgetCulledNoPanic 回归：预算裁剪使可见集非连续（全局≠本地索引）时
// 渲染不越界——曾以全局索引查本地 sol.Pos 崩溃（index out of range）。150 节点链 +
// 默认预算 120 → 可见集 = anchor 父链一段，全局索引从 30 起、本地 0..119。
func TestRelGraphBudgetCulledNoPanic(t *testing.T) {
	u := newHistUI(fakeRelTree{g: relChainGraph(150)}, fakeLister(nil))
	st := newHistoryState(u)
	if len(st.vis.nodes) != 120 {
		t.Fatalf("预算切片 = %d, want 120", len(st.vis.nodes))
	}
	q := new(input.Router)
	gtx, ops := frameGtxSize(q.Source(), 720, 560)
	st.graph.frame(gtx, u.th, u, st) // 不 panic = 通过
	q.Frame(ops)
}

// —— D121：节点拖拽（临时固定 → 松手回归自由）——

// TestRelGraphTwoNodeDragKeepsAnimating 2026-10-18 实测修：**少节点**（会话根 + 人格两个
// 节点）下快速拖动后，树会僵在非平衡态，「再拖一次」才继续动。
//
// 根因两处：① 源档的 friction 既是步长又停判时钟，按住期间每帧衰减——两个节点时邻居
// 早就到位、逐帧位移判停立刻成立，拖动第十几帧就 settled；② tickLayout 只在「抓取偏移
// 未衰减完」时续帧，于是指针停住后不再有下一帧把树拉回平衡。松手只清 settled、不拨回
// 时钟，步长≈0 ⇒ 首帧即判停（画面僵住）；再拖一次走 warmUp 才复活。
//
// 断言：按住期间 tickLayout 恒续帧（不被 settled 掐断）、时钟保持初值；松手后计时重上、
// 且能在续帧预算内正常收敛。
func TestRelGraphTwoNodeDragKeepsAnimating(t *testing.T) {
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	mk := func(id, parent string, role conversation.Role, i int) port.GraphNode {
		return port.GraphNode{
			ID: conversation.MessageID(id), Parent: conversation.MessageID(parent),
			Role: role, Snippet: "s-" + id, EdgeKind: port.EdgeSeq,
			SiblingIdx: 0, SiblingCount: 1, CreatedAt: base.Add(time.Duration(i) * time.Second),
		}
	}
	g := port.TreeGraph{
		Anchor: "p",
		Nodes: []port.GraphNode{
			mk("r", "", conversation.RoleRoot, 0),
			mk("p", "r", conversation.RoleSystem, 1),
		},
	}
	g.Total = len(g.Nodes)
	texts := map[conversation.MessageID]string{"r": "root", "p": "persona"}
	u := newHistUI(fakeRelTree{g: g, texts: texts}, fakeLister(nil))
	st := newHistoryState(u)
	q := new(input.Router)
	relGFrame(q, u, st)
	relDriveQuiet(q, u, st, relSettleFrames*3)

	gr := st.graph
	li := gr.anchor
	sx, ys := gr.toScreen(gr.sol.Pos[li])
	at := f32.Pt(sx, ys)
	q.Queue(relPointer(pointer.Press, at, true))
	relGFrame(q, u, st)
	to := f32.Pt(at.X+120, at.Y+60)
	q.Queue(relPointer(pointer.Move, to, true))
	relGFrame(q, u, st)
	if gr.dragNode != li {
		t.Fatalf("未进入拖节点: dragNode=%d, want %d", gr.dragNode, li)
	}
	// 按住不动：连续步进几百帧，物理时钟不得被拖拽耗尽、续帧不得断。
	for i := 0; i < relSettleFrames/2; i++ {
		if !gr.tickLayout() {
			t.Fatalf("拖拽第 %d 帧起不再续帧（树会僵在半路）: settled=%v friction=%.4f",
				i, gr.lay.settled, gr.lay.friction)
		}
		if gr.lay.settled {
			t.Fatalf("拖拽第 %d 帧被判定 settled（时钟被拖拽耗尽）", i)
		}
	}
	if got := gr.lay.friction; got < relSrcFriction*0.99 {
		t.Fatalf("拖拽期间的时钟被消耗: friction=%.4f, want ≈%.2f", got, relSrcFriction)
	}

	q.Queue(relPointer(pointer.Release, to, false))
	relGFrame(q, u, st) // Release → releaseDrag（撤 pin、重上时钟）
	if gr.dragNode >= 0 {
		t.Fatalf("松手后仍固定着节点 %d", gr.dragNode)
	}
	if got := gr.lay.friction; got < relSrcFriction*0.99 {
		t.Fatalf("松手未重上时钟: friction=%.4f（下一帧会立刻判停）", got)
	}
	relDriveDone(t, q, u, st)
}

// TestRelGraphDragNodePinsAndSettles 拖节点：视场不平移（那是空白拖拽的事）、节点被临时
// 固定在指针下、周围被斥力推开；松手后锚撤销、节点回归自由并收敛回原位。
func TestRelGraphDragNodePinsAndSettles(t *testing.T) {
	u, st, q := relGraphFixture(t)
	g := st.graph
	sx, li := relAnchorScreen(t, g)
	ox, oy := g.offX, g.offY
	before := append([]relPoint(nil), g.sol.Pos...)

	q.Queue(relPointer(pointer.Press, sx, true))
	relGFrame(q, u, st)
	to := f32.Pt(sx.X+80, sx.Y+40)
	q.Queue(relPointer(pointer.Move, to, true)) // 按压中 Move → Router 转 Drag
	relGFrame(q, u, st)

	if g.dragNode != li {
		t.Fatalf("拖动中的节点 = %d, want %d（锚点）", g.dragNode, li)
	}
	if g.offX != ox || g.offY != oy {
		t.Fatalf("拖节点不应平移视场: off=(%v,%v), want (%v,%v)", g.offX, g.offY, ox, oy)
	}
	if px, py := g.toScreen(g.sol.Pos[li]); math.Abs(float64(px-to.X)) > 2 || math.Abs(float64(py-to.Y)) > 2 {
		t.Fatalf("节点未跟随指针: 屏幕 (%v,%v), 指针 %v", px, py, to)
	}
	// 拖拽期间逐帧松弛：按住若干帧，周围应被斥力推开（渐进推开，不是一步到位）。
	for i := 0; i < 12; i++ {
		relGFrame(q, u, st)
	}
	moved := false
	for i, p := range g.sol.Pos {
		if i != li && (math.Abs(p.X-before[i].X) > 0.5 || math.Abs(p.Y-before[i].Y) > 0.5) {
			moved = true
			break
		}
	}
	for i, p := range g.sol.Pos {
		t.Logf("node %d: %+v (Δ=%.2f)", i, p, math.Hypot(p.X-before[i].X, p.Y-before[i].Y))
	}
	if !moved {
		t.Fatal("拖开节点后周围毫无位移（斥力未展现）")
	}

	q.Queue(relPointer(pointer.Release, to, false))
	relGFrame(q, u, st)
	if g.dragNode >= 0 {
		t.Fatalf("松手后仍固定着节点 %d（临时固定应为非持久）", g.dragNode)
	}
	// 松手后物理自然收敛：跑到静止即停（R3：稳定态不烧资源）。
	for i := 0; i < relSettleFrames*4 && (g.dragNode >= 0 || (g.lay != nil && !g.lay.settled)); i++ {
		relGFrame(q, u, st)
	}
	if g.dragNode >= 0 || g.lay == nil || !g.lay.settled {
		t.Fatal("松手后未收敛即停（R3：稳定态不烧资源）")
	}
	// 位置本身不断言「回到拖前」：力导向是**路径相关**的——拖开过程把周遭推到了新的
	// 平衡态，松手后收敛即停，不保证逐点还原（要还原得数据变更/rebudget 触发重排）。
	// 这里断言的是契约本身：锚已撤销、布局有界、且此后不再自己动。
	settled := append([]relPoint(nil), g.sol.Pos...)
	relGFrame(q, u, st)
	relGFrame(q, u, st)
	for i, p := range g.sol.Pos {
		if math.Abs(p.X-settled[i].X) > 0.01 || math.Abs(p.Y-settled[i].Y) > 0.01 {
			t.Fatalf("已收敛却仍在漂移（节点 %d）: %+v → %+v", i, settled[i], p)
		}
	}
	select {
	case in := <-u.inCh:
		t.Fatalf("拖节点不应投命令（拖过 ≠ 点击）: %+v", in)
	default:
	}
}

// TestRelGraphDragNodeNearMiss 实测修（D121）：**按下时命中落空**（低 zoom 卡片塌缩、
// 指针差几像素）不得静默退化成平移——起拖那一刻用更宽的抓手半径补命中，命中即拖该节点。
func TestRelGraphDragNodeNearMiss(t *testing.T) {
	u, st, q := relGraphFixture(t)
	g := st.graph
	li := g.anchor
	// 复现现场：低 zoom ⇒ 标签隐藏、卡片塌缩成小徽标（点不准的典型形态）
	// zoom 取 relZoomLabelMin 下方一档即可——再低（D130 起 fit 可压到 0.1）抓手/命中
	// 外扩换算成像素后都趋近 0，「命中落空」与「补抓」都不剩可测的像素窗口。
	g.zoom = relZoomLabelMin - 0.01
	g.z = g.zoom * g.dpx
	g.focusAnchor() // 夹具是按 fit 档（size-driven 后可能压得很低）定的视场：改 zoom 后
	// 重对一次，否则锚点已漂出视口，「命中落空」就不再是差几像素而是整棵树在别处。
	xs, ys := g.toScreen(g.sol.Pos[li])
	bare := relBadgeBox(xs, ys, float32(g.nodeRadius(&st.model.nodes[g.vis.nodes[li]])*g.z))
	if g.nodeCardBox(g.vis.nodes[li], xs, ys) != bare {
		t.Fatal("低 zoom 下卡片应塌缩为裸形状（本测试的前提）")
	}
	// 落点：徽标右侧「命中外扩之外、抓手外扩之内」的一小段
	near := f32.Pt(float32(bare.Max.X)+float32(relHitSlop*g.z)+1, ys)
	if got := g.hitTest(near.X, near.Y); got >= 0 {
		t.Fatalf("前提不成立：落点 %v 本就命中 %d", near, got)
	}
	ox, oy := g.offX, g.offY
	q.Queue(relPointer(pointer.Press, near, true))
	relGFrame(q, u, st)
	to := f32.Pt(near.X+30, near.Y+10)
	q.Queue(relPointer(pointer.Move, to, true))
	relGFrame(q, u, st)
	if g.dragNode != li {
		t.Fatalf("补抓失败：dragNode=%d, want %d（且不得平移）", g.dragNode, li)
	}
	if g.offX != ox || g.offY != oy {
		t.Fatalf("补抓时不应平移视场: off=(%v,%v), want (%v,%v)", g.offX, g.offY, ox, oy)
	}
}

// TestRelGraphDragCancelReleasesPin 指针被取消（拖出窗口/失焦）：锚同样撤销，不得卡在拖动态。
func TestRelGraphDragCancelReleasesPin(t *testing.T) {
	u, st, q := relGraphFixture(t)
	g := st.graph
	sx, li := relAnchorScreen(t, g)
	q.Queue(relPointer(pointer.Press, sx, true))
	relGFrame(q, u, st)
	q.Queue(relPointer(pointer.Move, f32.Pt(sx.X+40, sx.Y), true))
	relGFrame(q, u, st)
	if g.dragNode != li {
		t.Fatalf("未进入拖节点: %d", g.dragNode)
	}
	q.Queue(pointer.Event{Kind: pointer.Cancel, Position: f32.Pt(sx.X+40, sx.Y), PointerID: 7, Source: pointer.Mouse})
	relGFrame(q, u, st)
	if g.dragNode >= 0 || g.pressed || g.dragging {
		t.Fatalf("取消后交互态未清: drag=%d pressed=%v dragging=%v", g.dragNode, g.pressed, g.dragging)
	}
}

// TestRelGraphDragSnapsToCursor 实测修（D121）：**偏心按下**起拖后，节点中心在 ~120ms
// 内平滑滑到光标正下方并锁死——「光标和节点的位置不一样」不得持续存在。同时验证起拖
// 第一帧不瞬跳（偏移保留，衰减起步）。
func TestRelGraphDragSnapsToCursor(t *testing.T) {
	u, st, q := relGraphFixture(t)
	g := st.graph
	li := g.anchor
	// 偏心按下：标签区右缘（仍在卡片内；锚点 w3 在 zoom1.0 必有标签）。
	// 改 zoom 后先把 Head 收回右基准位——宽树在 zoom1.0 下锚点会跑到视口外，而 clip 只
	// 放行视口内的指针事件（D125），视口外按下会被静默丢弃。
	g.zoom = 1.0
	g.z = g.zoom * g.dpx
	g.focusAnchor()
	xs, ys := g.toScreen(g.sol.Pos[li])
	box := g.nodeCardBox(g.vis.nodes[li], xs, ys)
	ecc := f32.Pt(float32(box.Max.X-4), ys)
	if got := g.hitTest(ecc.X, ecc.Y); got != li {
		t.Fatalf("偏心点 %v 未命中节点 %d（卡片几何变了？）", ecc, got)
	}
	q.Queue(relPointer(pointer.Press, ecc, true))
	relGFrame(q, u, st)
	to := f32.Pt(ecc.X+40, ecc.Y+15)
	q.Queue(relPointer(pointer.Move, to, true))
	relGFrame(q, u, st)
	if g.dragNode != li {
		t.Fatalf("未拖到节点: %d", g.dragNode)
	}
	// 起拖第一帧：偏移尚未衰减完 → 节点中心 ≠ 光标（不瞬跳，本测试的要点之一）
	if px, py := g.toScreen(g.sol.Pos[li]); math.Abs(float64(px-to.X)) < 1 && math.Abs(float64(py-to.Y)) < 1 {
		t.Fatalf("起拖首帧节点就贴住光标（偏移未保留，标签区起拖会瞬跳）: (%v,%v)", px, py)
	}
	// 衰减逐帧推进：指针停住也要继续滑向光标（tickLayout 续帧）
	for i := 0; i < 30 && (g.dragGrabX != 0 || g.dragGrabY != 0); i++ {
		relGFrame(q, u, st)
	}
	if g.dragGrabX != 0 || g.dragGrabY != 0 {
		t.Fatalf("抓取偏移未衰减完: (%v,%v)", g.dragGrabX, g.dragGrabY)
	}
	if px, py := g.toScreen(g.sol.Pos[li]); math.Abs(float64(px-to.X)) > 2 || math.Abs(float64(py-to.Y)) > 2 {
		t.Fatalf("节点中心未贴住光标: 屏幕 (%v,%v), 光标 %v", px, py, to)
	}
	q.Queue(relPointer(pointer.Release, to, false))
	relGFrame(q, u, st)
}

// TestRelGraphPhysicsUnfoldOnOpen D124：首帧数据从**折叠态**物理展开到静息布局
// （弹簧 + 斥力 + 阻尼，秒级），收敛即停（R3）。曾为 lerp 入场动画（D123），现改为
// 常驻物理——开窗的运动就是物理过程本身（不是独立时间线）。
func TestRelGraphPhysicsUnfoldOnOpen(t *testing.T) {
	u := newHistUI(fakeRelTree{g: relChainGraph(150)}, fakeLister(nil))
	st := newHistoryState(u) // sync → rebuild → 折叠播种
	g := st.graph
	q := new(input.Router)
	if g.lay == nil || g.lay.settled {
		t.Fatal("rebuild 后应处于物理展开态（而非一次全解静止）")
	}
	rest := g.lay.restPos()
	start := append([]relPoint(nil), g.sol.Pos...)
	shrunk := 0
	for i, p := range start {
		if math.Hypot(p.X-rest[i].X, p.Y-rest[i].Y) > 1 {
			shrunk++
		}
	}
	if shrunk == 0 {
		t.Fatal("起帧不应已是静息布局（没有可展开的初态）")
	}
	for i := 0; i < 20; i++ {
		relGFrame(q, u, st)
	}
	moved := false
	for i, p := range g.sol.Pos {
		if math.Hypot(p.X-start[i].X, p.Y-start[i].Y) > 0.5 {
			moved = true
			break
		}
	}
	if !moved {
		t.Fatal("展开 20 帧后仍在原位（没有在动）")
	}
	for i := 0; i < relSettleFrames*4 && g.lay != nil && !g.lay.settled; i++ {
		relGFrame(q, u, st)
	}
	if g.lay == nil || !g.lay.settled {
		t.Fatal("物理展开未收敛")
	}
	settled := append([]relPoint(nil), g.sol.Pos...)
	relGFrame(q, u, st)
	relGFrame(q, u, st)
	for i, p := range g.sol.Pos {
		if math.Hypot(p.X-settled[i].X, p.Y-settled[i].Y) > 0.01 {
			t.Fatalf("收敛后仍在漂移（节点 %d）", i)
		}
	}
}

// TestRelNodeCardCarriesLabel 节点卡承载标签（D121「内容入内」）：标签可见时卡比徽标宽、
// 高度仍等于徽标（宽度不改变节点直径语义，A4）；标签不可见（低于 zoom 下限）时退化为
// 居中方块。
func TestRelNodeCardCarriesLabel(t *testing.T) {
	_, st, _ := relGraphFixture(t)
	g := st.graph
	g.zoom, g.z = relZoomLabelMin, relZoomLabelMin*g.dpx // 标签可见档（本测试验的是卡片承载标签）
	gi := st.vis.nodes[g.anchor]
	if relLabelText(st.model, gi, g.zoom) == "" {
		t.Fatal("锚点应有标签")
	}
	xs, ys := g.toScreen(g.sol.Pos[g.anchor])
	r := float32(g.nodeRadius(&st.model.nodes[gi]) * g.z)
	bare := relBadgeBox(xs, ys, r)
	box := g.nodeCardBox(gi, xs, ys)
	if box.Dx() <= bare.Dx()+int(relCardGapDp*g.z) {
		t.Fatalf("标签卡宽 %d 未超出徽标 %d（标签没进卡里）", box.Dx(), bare.Dx())
	}
	// 徽标带内边距地贴在卡内（菱形尖/空心圆不被切边，分叉虚线环不越界）
	pad, pv := int(relCardPadDp*g.z), int(relCardPadV*g.z)
	if box.Min.X != bare.Min.X-pad || box.Min.Y != bare.Min.Y-pv || box.Max.Y != bare.Max.Y+pv {
		t.Fatalf("标签卡背离徽标锚点: %v vs %v +内边距 %d/%d", box, bare, pad, pv)
	}
	// 低于 zoom 下限 → 无标签 → 裸形状（直接改 zoom 时须同步有效缩放 g.z）
	g.zoom = relZoomLabelMin - 0.01
	g.z = g.zoom * g.dpx
	bare = relBadgeBox(xs, ys, float32(g.nodeRadius(&st.model.nodes[gi])*g.z))
	if box := g.nodeCardBox(gi, xs, ys); box != bare {
		t.Fatalf("无标签时卡 %v ≠ 裸形状 %v（应退化）", box, bare)
	}
}

// TestRelEstTextWidth 文本宽估算：CJK 按全角记、同字数下宽于 ASCII（卡片宽度量级正确）。
func TestRelEstTextWidth(t *testing.T) {
	cjk := relEstTextW("你今天想聊点什么", 12)
	ascii := relEstTextW("abcdefghijkl", 12)
	if cjk <= ascii {
		t.Fatalf("CJK 宽 %v 应大于等长 ASCII %v", cjk, ascii)
	}
	if got := relEstTextW("", 12); got != 0 {
		t.Fatalf("空串宽 = %v, want 0", got)
	}
	if got := relEstTextW("ab", 10); got != 11 { // 2 × 0.55 × 10
		t.Fatalf("ASCII 估宽 = %v, want 11", got)
	}
}
