package uigui

import (
	"image"
	"path/filepath"
	"testing"

	"gioui.org/io/input"
	"gioui.org/unit"
)

// dpId 测试用 1:1 dp→px 换算（几何断言与设计值直接可比）。
func dpId(dp unit.Dp) int { return int(dp) }

// TestBallRect 锚点与 D49 球几何同源（16/48、行底 16、行上 8；D50 收起态夹取锚点）。
func TestBallRect(t *testing.T) {
	got := ballRect(image.Pt(608, 460), dpId)
	want := image.Rect(16, 396, 64, 444)
	if got != want {
		t.Fatalf("ballRect = %v, want %v", got, want)
	}
}

// TestClampAnchor 可见锚点夹取（D50 ①）：收起 = 球（透明窗体可越界）、展开 = 整窗；
// 顺序右/下先、左/上后（超尺寸时左上优先，与旧口径一致）。
func TestClampAnchor(t *testing.T) {
	work := rect{left: 0, top: 0, right: 1920, bottom: 1080}
	ball := image.Rect(16, 396, 64, 444)

	// 收起态右出：球右缘 2064 > 1920 → 回夹 144（窗口可留在屏外，球不出屏）。
	if got := clampAnchor(point{x: 2000, y: 10}, ball, work); got != (point{x: 1856, y: 10}) {
		t.Fatalf("球右夹 = %+v, want {1856 10}", got)
	}
	// 收起态左出：球左缘 -84 < 0 → 回夹到 0（窗口左缘可为负）。
	if got := clampAnchor(point{x: -100, y: 10}, ball, work); got != (point{x: -16, y: 10}) {
		t.Fatalf("球左夹 = %+v, want {-16 10}", got)
	}
	// 展开态 = 整窗锚点（等价旧 clampToWorkArea 行为）。
	win := image.Rect(0, 0, 608, 460)
	if got := clampAnchor(point{x: 1920, y: 0}, win, work); got != (point{x: 1312, y: 0}) {
		t.Fatalf("整窗右夹 = %+v, want {1312 0}", got)
	}
	if got := clampAnchor(point{x: 0, y: -5}, win, work); got != (point{x: 0, y: 0}) {
		t.Fatalf("整窗顶夹 = %+v, want {0 0}", got)
	}
}

// TestSnapDelta 四边吸附（D50 ②）：阈值内取最近边贴齐、超阈值不吸、已贴齐零位移。
func TestSnapDelta(t *testing.T) {
	work := rect{left: 0, top: 0, right: 1920, bottom: 1080}
	win := image.Rect(0, 0, 608, 460)
	thr := int32(snapDp)

	if d := snapDelta(point{x: 5, y: 500}, win, work, thr); d != (point{x: -5}) {
		t.Fatalf("贴左 = %+v, want {-5 0}", d)
	}
	if d := snapDelta(point{x: 1920 - 608 - 4, y: 500}, win, work, thr); d != (point{x: 4}) {
		t.Fatalf("贴右 = %+v, want {4 0}", d)
	}
	if d := snapDelta(point{x: 300, y: 1080 - 460 - 3}, win, work, thr); d != (point{y: 3}) {
		t.Fatalf("贴底 = %+v, want {0 3}", d)
	}
	if d := snapDelta(point{x: 300, y: 7}, win, work, thr); d != (point{y: -7}) {
		t.Fatalf("贴顶 = %+v, want {0 -7}", d)
	}
	if d := snapDelta(point{x: thr + 1, y: 500}, win, work, thr); d != (point{}) {
		t.Fatalf("超阈值 = %+v, want 零值", d)
	}
	if d := snapDelta(point{x: 0, y: 0}, win, work, thr); d != (point{}) {
		t.Fatalf("已贴齐 = %+v, want 零值", d)
	}
}

// TestDockableEdge 停靠边判定（D50 ③）：贴齐 + 外侧无相邻显示器；接缝边/不贴边 = 不可。
func TestDockableEdge(t *testing.T) {
	work := rect{left: 0, top: 0, right: 1920, bottom: 1080}
	ball := image.Rect(16, 396, 64, 444)
	thr := int32(snapDp)

	if e := dockableEdge(point{x: -16, y: 0}, ball, work, thr, true, true); e != "left" {
		t.Fatalf("贴左外侧空 = %q, want left", e)
	}
	// 接缝（左侧还有屏）→ 即使贴齐也不可停靠（滑出藏不住）。
	if e := dockableEdge(point{x: -16, y: 0}, ball, work, thr, false, true); e != "" {
		t.Fatalf("贴左接缝 = %q, want 空", e)
	}
	if e := dockableEdge(point{x: 1856, y: 0}, ball, work, thr, true, true); e != "right" {
		t.Fatalf("贴右 = %q, want right", e)
	}
	if e := dockableEdge(point{x: 500, y: 0}, ball, work, thr, true, true); e != "" {
		t.Fatalf("不贴边 = %q, want 空", e)
	}
}

// TestDockSlideAndPark 停靠/召回落位（D50）：亮态贴齐 → 滑出后恰留 sliver 窄条；
// 垂直方向不动。
func TestDockSlideAndPark(t *testing.T) {
	work := rect{left: 0, top: 0, right: 1920, bottom: 1080}
	ball := image.Rect(16, 396, 64, 444)
	sliver := int32(dockSliverDp)

	// 左：贴齐 = 球左缘贴 work.left（窗口 x = -16）。
	if p := parkPos(point{x: 999, y: 7}, ball, work, "left"); p != (point{x: -16, y: 7}) {
		t.Fatalf("贴齐左 = %+v, want {-16 7}", p)
	}
	s := dockSlidePos(point{x: 999, y: 7}, ball, work, "left", sliver)
	if s != (point{x: -56, y: 7}) {
		t.Fatalf("滑出左 = %+v, want {-56 7}", s)
	}
	// 屏内可见部分 = 球右缘 − work.left = sliver。
	if got := int32(ball.Max.X) + s.x; got != work.left+sliver {
		t.Fatalf("左窄条 = %dpx, want %d", got, sliver)
	}

	// 右：贴齐 = 球右缘贴 work.right（窗口 x = 1856）；滑出后球左缘 = work.right - sliver。
	if p := parkPos(point{x: 0, y: 0}, ball, work, "right"); p != (point{x: 1856, y: 0}) {
		t.Fatalf("贴齐右 = %+v, want {1856 0}", p)
	}
	s = dockSlidePos(point{x: 0, y: 0}, ball, work, "right", sliver)
	if got := int32(ball.Min.X) + s.x; got != work.right-sliver {
		t.Fatalf("右窄条起点 = %d, want %d", got, work.right-sliver)
	}
	if s.y != 0 {
		t.Fatalf("滑出不应动垂直位 = %+v", s)
	}
}

// TestEvalDock 停靠判定（D50 用户拍板语义）：布防 → 移开停靠；启动恢复（未布防）不停；
// 展开/拖动/不贴边不停；停靠 + 悬停窄条召回；召回后移开重停。
func TestEvalDock(t *testing.T) {
	// ① 光标在球上且贴可停靠边 → 布防、无动作。
	if armed, act := evalDock(dockInputs{collapsed: true, edge: "left", over: true}); !armed || act != dockNoneAct {
		t.Fatalf("布防 = (%v,%v), want (true,none)", armed, act)
	}
	// ② 布防后光标移开（仍收起、仍贴边）→ 停靠。
	if armed, act := evalDock(dockInputs{collapsed: true, edge: "left", armed: true}); armed || act != dockStartAct {
		t.Fatalf("移开停靠 = (%v,%v), want (false,dock)", armed, act)
	}
	// ③ 启动恢复（从未布防）+ 光标移开 → 不停（不自动滑出）。
	if armed, act := evalDock(dockInputs{collapsed: true, edge: "left"}); armed || act != dockNoneAct {
		t.Fatalf("恢复不停 = (%v,%v), want (false,none)", armed, act)
	}
	// ④ 光标在球上但不贴边 → 不布防。
	if armed, act := evalDock(dockInputs{collapsed: true, over: true}); armed || act != dockNoneAct {
		t.Fatalf("不贴边 = (%v,%v), want (false,none)", armed, act)
	}
	// ⑤ 展开态不可停靠（仅收起态）。
	if armed, act := evalDock(dockInputs{edge: "left", over: true}); armed || act != dockNoneAct {
		t.Fatalf("展开 = (%v,%v), want (false,none)", armed, act)
	}
	// ⑥ 拖动中不布防不停。
	if armed, act := evalDock(dockInputs{collapsed: true, dragging: true, edge: "left", over: true}); armed || act != dockNoneAct {
		t.Fatalf("拖动 = (%v,%v), want (false,none)", armed, act)
	}
	// ⑦ 停靠中 + 光标在窄条 → 召回。
	if _, act := evalDock(dockInputs{docked: true, collapsed: true, over: true}); act != dockRecallAct {
		t.Fatalf("召回 = %v, want recall", act)
	}
	// ⑧ 停靠中 + 光标不在 → 不动。
	if _, act := evalDock(dockInputs{docked: true, collapsed: true}); act != dockNoneAct {
		t.Fatalf("停靠静置 = %v, want none", act)
	}
	// ⑨ 召回后（光标仍在球上 → 已重新布防）移开 → 重停靠（拖回可停靠区同径）。
	if _, act := evalDock(dockInputs{collapsed: true, edge: "right", armed: true}); act != dockStartAct {
		t.Fatalf("召回后重停 = %v, want dock", act)
	}
}

// TestEaseOutCubic 停靠缓动：端点精确、单调不减、前段快于线性（ease-out）。
func TestEaseOutCubic(t *testing.T) {
	if easeOutCubic(0) != 0 || easeOutCubic(1) != 1 ||
		easeOutCubic(-0.5) != 0 || easeOutCubic(1.5) != 1 {
		t.Fatalf("端点越界夹取错误: 0=%v 1=%v neg=%v over=%v",
			easeOutCubic(0), easeOutCubic(1), easeOutCubic(-0.5), easeOutCubic(1.5))
	}
	prev := 0.0
	for i := 1; i <= 100; i++ {
		p := easeOutCubic(float64(i) / 100)
		if p < prev {
			t.Fatalf("非单调 @ i=%d: %v < %v", i, p, prev)
		}
		prev = p
	}
	if mid := easeOutCubic(0.5); mid <= 0.5 {
		t.Fatalf("ease-out 前段应快于线性, p(0.5)=%v", mid)
	}
}

// TestArmHeartbeat 心跳安全边界（D50）：headless（w=nil）布防不启动唤帧 goroutine；
// 未布防时已开的心跳关停且 channel 关闭（收敛，无泄漏）。
func TestArmHeartbeat(t *testing.T) {
	u := newHeadless(t, Options{})

	u.dockArm = true
	u.armHeartbeat()
	if u.armTick != nil {
		t.Fatal("headless 不应启动心跳")
	}

	ch := make(chan struct{})
	u.armTick = ch // 模拟运行中
	u.dockArm = false
	u.armHeartbeat()
	if u.armTick != nil {
		t.Fatal("未布防应关停心跳")
	}
	select {
	case <-ch:
	default:
		t.Fatal("心跳 channel 应已关闭")
	}
}

// TestHeartbeatNeed 心跳判据（D50 实测修订 / D53 扩展 / D84 扩展）：停靠中 / 布防中 /
// 收起态球停在可停靠边 / tips 在显 / 光标在输入行带内 → 需唤帧（形裁窄区与分层窗
// 透明区悬停可能零帧，判定全靠帧 + 光标直采）；否则静默零帧。光标项经 heartbeatNeedAt
// 参数化（真实光标不可控）。
func TestHeartbeatNeed(t *testing.T) {
	far := point{x: -1 << 29, y: -1 << 29} // 任何带外位置
	u := &UI{plat: newFakePlat()}
	if u.heartbeatNeedAt(far) {
		t.Fatal("展开态不应心跳")
	}
	u.collapsed = true
	if u.heartbeatNeedAt(far) {
		t.Fatal("收起态球不在可停靠边不应心跳")
	}
	u.edgeNow = "left"
	if !u.heartbeatNeedAt(far) {
		t.Fatal("收起态球在可停靠边应心跳")
	}
	u.edgeNow = ""
	u.dockArm = true
	if !u.heartbeatNeedAt(far) {
		t.Fatal("布防中应心跳")
	}
	u.dockArm = false
	u.docked = true
	if !u.heartbeatNeedAt(far) {
		t.Fatal("停靠中应心跳")
	}
	u.docked = false
	u.tipShown = true
	if !u.heartbeatNeedAt(far) {
		t.Fatal("tips 在显应心跳（D53 光标直采复评）")
	}
	u.tipShown = false
	if u.heartbeatNeedAt(far) {
		t.Fatal("全部条件清空应静默")
	}
}

// TestHeartbeatInputBand 输入行带心跳（D84）：光标落在输入行块（含上下透明边距）
// 及其上方 approach 余量内 → 心跳；带外（转写区深处/窗外）不因光标项心跳。
// 几何：608×460@1x，行块 = [396,444)+上下边距 = [388,460)，带顶 = 388−12 = 376。
func TestHeartbeatInputBand(t *testing.T) {
	u := &UI{plat: newFakePlat(), frameSize: image.Pt(608, 460), frameMetric: unit.Metric{PxPerDp: 1, PxPerSp: 1}}
	u.x, u.y = 100, 200 // 窗口屏幕位 → 带 = 屏幕 x∈[100,708) y∈[576,660)
	cases := []struct {
		cur  point
		want bool
		note string
	}{
		{point{x: 130, y: 650}, true, "行块下透明边距条（D84 哑窗场景）"},
		{point{x: 130, y: 590}, true, "行块内"},
		{point{x: 400, y: 585}, true, "行块顶上方 approach 余量内"},
		{point{x: 130, y: 575}, false, "带顶之上（转写区）"},
		{point{x: 60, y: 650}, false, "窗外左侧"},
		{point{x: 800, y: 650}, false, "窗外右侧"},
	}
	for _, tc := range cases {
		if got := u.cursorInInputBand(tc.cur); got != tc.want {
			t.Fatalf("%s: cursor=(%d,%d) inBand=%v, want %v", tc.note, tc.cur.x, tc.cur.y, got, tc.want)
		}
		if got := u.heartbeatNeedAt(tc.cur); got != tc.want {
			t.Fatalf("%s: heartbeatNeedAt=(%d,%d) = %v, want %v", tc.note, tc.cur.x, tc.cur.y, got, tc.want)
		}
	}
	// 帧几何未就绪（headless 构造零值）= 不心跳。
	if (&UI{plat: newFakePlat()}).cursorInInputBand(point{x: 130, y: 650}) {
		t.Fatal("frameSize 未就绪不应心跳")
	}
}

// TestCursorHitsLogo logo 门控（D85）：矩形直采 × WindowFromPoint 命中直证。
// headless（hwnd=0）退化为纯矩形直采——608×460@1x 窗口系 logo 钮 = [16,396,64,444)。
func TestCursorHitsLogo(t *testing.T) {
	u := &UI{plat: newFakePlat(), frameSize: image.Pt(608, 460), frameMetric: unit.Metric{PxPerDp: 1, PxPerSp: 1}}
	u.x, u.y = 227, 146
	cases := []struct {
		x, y int
		want bool
		note string
	}{
		{259, 566, true, "钮内（窗口系 32,420）"},
		{243, 542, true, "钮左上角内（16,396）"},
		{291, 589, false, "钮右下角外（64,444 恰在开区间外）"},
		{230, 560, false, "钮外左侧"},
		{259, 700, false, "钮下方远处"},
	}
	for _, tc := range cases {
		if got := u.cursorHitsLogo(point{x: int32(tc.x), y: int32(tc.y)}); got != tc.want {
			t.Fatalf("%s: cursor=(%d,%d) hits=%v, want %v", tc.note, tc.x, tc.y, got, tc.want)
		}
	}
	// 矩形内但 hwnd 非 0 且 OS 命中他窗：win32 路径不可 headless 驱动，此处仅锁
	// 「hwnd=0 退化直采」分支——真窗行为归 GUI 手工验收（D85）。
}

// TestConfirmBtnRects 确认态三钮矩形推导（D86）：默认窗宽 608×460 @1x 下胶囊右缘
// 532，最右钮右缘 = 532−12（D86 右内边距，含光学校正），⌀36、间距 8、行内垂直居中；
// 顺序左→右恒 [✗ 拒绝][✓ 允许][🔑 提升（仅 elevate）]。tips 直采（confirmTipAt）
// 与布局期 Flex 排布共用同一套 dp 常量，此处锁几何契约。
func TestConfirmBtnRects(t *testing.T) {
	dp := func(d unit.Dp) int { return int(d) }
	size := image.Pt(608, 460)

	rects2 := confirmBtnRects(size, dp, false)
	if len(rects2) != 2 {
		t.Fatalf("len = %d, want 2（无提升钮）", len(rects2))
	}
	want2 := []image.Rectangle{
		image.Rect(440, 402, 476, 438), // ✗ 拒绝
		image.Rect(484, 402, 520, 438), // ✓ 允许（最右）
	}
	for i, r := range rects2 {
		if r != want2[i] {
			t.Fatalf("rects2[%d] = %v, want %v", i, r, want2[i])
		}
	}

	rects3 := confirmBtnRects(size, dp, true)
	if len(rects3) != 3 {
		t.Fatalf("len = %d, want 3", len(rects3))
	}
	want3 := []image.Rectangle{
		image.Rect(396, 402, 432, 438), // ✗ 拒绝
		image.Rect(440, 402, 476, 438), // ✓ 允许
		image.Rect(484, 402, 520, 438), // 🔑 提升在最右
	}
	for i, r := range rects3 {
		if r != want3[i] {
			t.Fatalf("rects3[%d] = %v, want %v", i, r, want3[i])
		}
	}
}

// TestOverInputBtn tips 光标直采判定（D53）：圆钮矩形由 rowAnchor 派生（logo = 锚点
// 左上方块、右钮 = 右上方块），命中 = 窗口位 + 矩形含光标屏幕坐标；帧未就绪不命中。
// 分层窗透明像素/窗外零 pointer 事件，Hover 收不到 Leave——tips 熄灭全靠本判定。
func TestOverInputBtn(t *testing.T) {
	u := &UI{plat: newFakePlat()}
	u.frameSize = image.Pt(760, 575)
	u.frameMetric = unit.Metric{PxPerDp: 1, PxPerSp: 1}
	u.x, u.y = -16, 628

	a := rowAnchor(u.frameSize, u.frameMetric.Dp)
	logo := inputBtnRects(u.frameSize, u.frameMetric.Dp, false)
	send := inputBtnRects(u.frameSize, u.frameMetric.Dp, true)
	if logo.Min != a.Min || logo.Dx() != a.Dy() || logo.Dx() != logo.Dy() {
		t.Fatalf("logo 钮 = %v, want 锚点左上方块 ⌀%d", logo, a.Dy())
	}
	if send.Max != a.Max || send.Dx() != a.Dy() || send.Dy() != a.Dy() {
		t.Fatalf("右钮 = %v, want 锚点右上方块 ⌀%d", send, a.Dy())
	}
	mid := func(r image.Rectangle) point {
		return point{x: u.x + int32(r.Min.X+r.Dx()/2), y: u.y + int32(r.Min.Y+r.Dy()/2)}
	}
	if !u.overInputBtn(false, mid(logo)) {
		t.Fatal("光标在 logo 钮上应命中")
	}
	if !u.overInputBtn(true, mid(send)) {
		t.Fatal("光标在右钮上应命中右钮")
	}
	if u.overInputBtn(false, mid(send)) {
		t.Fatal("光标在右钮上不应命中 logo 钮")
	}
	if u.overInputBtn(false, point{x: u.x - 5, y: u.y - 5}) {
		t.Fatal("光标在窗外不应命中")
	}
	u.frameMetric = unit.Metric{}
	if u.overInputBtn(false, mid(logo)) {
		t.Fatal("frameMetric 未就绪不应命中")
	}
}

// TestCollapseAnimKeepsDockArm 收起动画保留抬手布防（D50 实测缺陷修订，§15.1）：
// endDrag 抬手即布防（「曾悬停」的证据），而收起动画 320+220ms 内 evalDockFrame 被
// 抑制——若 layout 连布防一起清，动画结束首帧重新布防又要求光标在球上、此刻光标通常
// 已移开 → 不悬停就永不停靠（收回后不自动吸附/停靠的实测缺陷）。展开方向仍须清
// （展开态不可停靠，default 分支同口径）。
func TestCollapseAnimKeepsDockArm(t *testing.T) {
	// ① 收起动画（beginCollapse 同序：先起动画再翻态）：抬手布防证据保留。
	u := newFrameUI()
	u.dockArm = true
	u.startExpandAnim(false)
	u.collapsed = true
	gtx, _ := frameGtx(input.Source{})
	u.layout(gtx)
	if !u.dockArm {
		t.Fatal("收起动画不应清抬手布防（动画结束的 evalDockFrame 靠它停靠）")
	}

	// ② 展开动画（beginExpand 同序）：清布防——展开态不可停靠。
	u2 := newFrameUI()
	u2.dockArm = true
	u2.collapsed = true
	u2.startExpandAnim(true)
	u2.collapsed = false
	gtx2, _ := frameGtx(input.Source{})
	u2.layout(gtx2)
	if u2.dockArm {
		t.Fatal("展开动画应清布防")
	}
}

// TestBeginDragBaseFollowsUndock 拖动基点取脱离停靠后的逻辑位（D55/§15.1 实测缺陷
// 修订）：停靠态按下先 undockInstant 记账、SetWindowPos 帧末才发——此刻 windowRectPx
// 还是滑出位；按 OS 矩形起基，点击期间 ≥1px 抖动的 moveDrag 会把贴齐位回退成滑出位
// （离边超 snapDp → 布防/停靠断链）。基点必须 = u.x/u.y（undockInstant 记账后即贴齐位）。
func TestBeginDragBaseFollowsUndock(t *testing.T) {
	u := newFrameUI()
	u.hwnd = 1 // 窗口就绪门控（headless 无真句柄，windowRectPx 恒失败）
	u.frameSize = image.Pt(760, 575)
	u.frameMetric = unit.Metric{PxPerDp: 1, PxPerSp: 1}
	u.collapsed = true
	u.docked = true
	u.dockHint.Store(dockLeftInt)
	u.x, u.y = -56, 628 // 停靠滑出位

	u.undockInstant() // 按下即脱离（帧内只记账：OS 矩形仍旧是滑出位）
	park := u.x
	u.beginDrag()
	if !u.dragging {
		t.Fatal("窗口就绪应起拖动")
	}
	if u.dragWin0.x != park || u.dragWin0.y != u.y {
		t.Fatalf("拖动基点 = (%d,%d), want 脱离后逻辑位 (%d,%d)",
			u.dragWin0.x, u.dragWin0.y, park, u.y)
	}
}

// TestEndDragClickKeepsPark 抬手纯点击不夹取（D50 实测缺陷修订，§15.1）：「点击脱离
// 停靠」把窗口落在半出屏贴边位（球锚点越界合法），展开态整窗锚点会把它推离边缘 →
// 收起后球离边超 snapDp、布防/停靠断链——纯点击必须原位保留；真拖动仍按态夹取。
func TestEndDragClickKeepsPark(t *testing.T) {
	u := newHeadless(t, Options{})
	u.frameSize = image.Pt(760, 575) // 展开态锚点 = 输入栏包围盒（D52）
	u.frameMetric = unit.Metric{PxPerDp: 1, PxPerSp: 1}
	u.collapsed = false

	// 纯点击：光标与按下点重合（位移 ≤ dragClickSlackPx）→ 跳过夹取，贴边位原样保留。
	u.dragging = true
	u.dragCur0 = u.cursorPos()
	u.x, u.y = -380, 628
	u.endDrag()
	if u.x != -380 {
		t.Fatalf("纯点击不应夹取: x=%d, want -380", u.x)
	}
	if u.dragging {
		t.Fatal("endDrag 应清 dragging")
	}

	// 真拖动（位移 > slack）：按输入栏锚点夹取（D52——透明边距可越界，夹到
	// 输入栏贴左缘 x = -sideMargin，而非整窗的 x ≥ 0）。
	u.dragging = true
	u.dragCur0 = point{x: u.cursorPos().x + 100, y: u.cursorPos().y}
	u.x, u.y = -380, 628
	u.endDrag()
	if want := int32(-dpId(sideMarginDp)); u.x != want {
		t.Fatalf("真拖动应按输入栏锚点夹取: x=%d, want %d", u.x, want)
	}
}

// TestRowAnchor 展开态锚点几何（D52）：输入栏三段包围盒 = [sideMargin, 镜像边距] ×
// [行上, 行高]——与 inputBar 同式，转写消息区不参与。
func TestRowAnchor(t *testing.T) {
	size, dp := image.Pt(760, 575), dpId
	r := rowAnchor(size, dp)
	if r.Min.X != dp(sideMarginDp) || r.Max.X != size.X-dp(sideMarginDp) {
		t.Fatalf("rowAnchor x = [%d,%d], want [%d,%d]",
			r.Min.X, r.Max.X, dp(sideMarginDp), size.X-dp(sideMarginDp))
	}
	if r.Min.Y != ballRect(size, dp).Min.Y || r.Max.Y-r.Min.Y != dp(inputRowDp) {
		t.Fatalf("rowAnchor y = %v, want top=%d h=%d",
			r, ballRect(size, dp).Min.Y, dp(inputRowDp))
	}
}

// TestExpandedClampRowEdge 展开态夹取只限制输入栏（D52）：输入栏贴左缘/提到上沿的
// 位置不被弹开（旧整窗口径会推开 0～20px 透明边距），再越即夹回。
func TestExpandedClampRowEdge(t *testing.T) {
	u := newHeadless(t, Options{})
	u.frameSize = image.Pt(760, 575)
	u.frameMetric = unit.Metric{PxPerDp: 1, PxPerSp: 1}
	u.collapsed = false
	margin := int32(dpId(sideMarginDp))
	top := int32(575 - dpId(inputRowBandDp) + dpId(pillTopDp))

	if x, _ := u.clampPos(-margin, 628); x != -margin {
		t.Fatalf("输入栏贴左缘位被弹开: x=%d, want %d", x, -margin)
	}
	if x, _ := u.clampPos(-margin-30, 628); x != -margin {
		t.Fatalf("过左应夹回贴缘位: x=%d, want %d", x, -margin)
	}
	if _, y := u.clampPos(0, -top); y != -top {
		t.Fatalf("输入栏提到上沿被挡: y=%d, want %d", y, -top)
	}
	if _, y := u.clampPos(0, -top-40); y != -top {
		t.Fatalf("输入栏越上沿应夹回: y=%d, want %d", y, -top)
	}
}

// TestShowExpandUndocks 呼出消息脱离停靠（D50）：停靠态 showExpand → 状态复位 +
// 复亮 + 落盘未停靠（headless 下 Win32 为无句柄空转，可断言状态与记忆）。
func TestShowExpandUndocks(t *testing.T) {
	u := newHeadless(t, Options{PosFile: filepath.Join(t.TempDir(), "pos.json")})

	// 模拟停靠态（事件循环侧状态在 post 之前写入，drainSync 提供 happens-before）。
	u.collapsed = true
	u.docked = true
	u.dockHint.Store(dockLeftInt)
	u.alpha = dockAlpha
	u.x, u.y = -56, 300
	u.post(showExpandMsg{})
	drainSync(t, u)

	if u.collapsed || !u.focusPending {
		t.Fatalf("showExpand 后 collapsed=%v focusPending=%v, want false/true",
			u.collapsed, u.focusPending)
	}
	if u.docked || u.dockHint.Load() != dockNoneInt {
		t.Fatalf("脱离后 docked=%v hint=%d, want false/0", u.docked, u.dockHint.Load())
	}
	if u.alpha != semiAlpha {
		t.Fatalf("脱离后 alpha=%d, want %d", u.alpha, semiAlpha)
	}
	p, ok := loadPos(u.opts.PosFile)
	if !ok || p.Docked != "" {
		t.Fatalf("落盘 = %+v,%v, want 未停靠", p, ok)
	}
}
