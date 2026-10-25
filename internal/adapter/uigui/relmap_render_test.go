package uigui

// relmap_render_test.go 右图图形与预算：描边贴合徽标、形状顶点、卡片标签、
// 徽标居中、标签宽度估算、预算钳制与整窗渲染。

import (
	"image"
	"math"
	"testing"
	"time"

	"gioui.org/gpu/headless"
	"gioui.org/io/input"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/unit"
	"github.com/Tonyjh07/Aquarius/internal/domain/conversation"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

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

// TestRelTriangleOutline 注入节点的三角外环（D166）：三顶点朝上，采样点闭合在盒内，
// 且与其余四形的轮廓语言不同（上尖下宽）。
func TestRelTriangleOutline(t *testing.T) {
	const r = 24.0
	step := (relDashOn + relDashOff) * 1.25
	pts := shapeOutline(relShapeTriangle, 100, 100, r, step)
	if len(pts) < 9 {
		t.Fatalf("三角外环采样点太少: %d", len(pts))
	}
	top, bottom := math.Inf(1), math.Inf(-1)
	for _, p := range pts {
		y := float64(p.Y - 100)
		top, bottom = math.Min(top, y), math.Max(bottom, y)
	}
	// 顶点在 −0.95r，底边在 +0.475r：上下不对称是三角语言的一部分。
	if math.Abs(top+r*0.95) > 0.5 {
		t.Fatalf("三角顶点 y = %.2f, want %.2f", top, -r*0.95)
	}
	if math.Abs(bottom-r*0.475) > 0.5 {
		t.Fatalf("三角底边 y = %.2f, want %.2f", bottom, r*0.475)
	}
}

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
	storeTokens(lightPalette())
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

// TestRelBudgetControls 预算 −/＋ 钳制（用 150 节点链验证非平凡预算）。
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
	if len(st.vis.nodes) != 1 {
		t.Fatalf("预算 1 的呈现 = %d, want 1", len(st.vis.nodes))
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
