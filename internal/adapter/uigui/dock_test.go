package uigui

import (
	"image"
	"path/filepath"
	"testing"

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

// TestHeartbeatNeed 心跳判据（D50 实测修订）：停靠中 / 布防中 / 收起态球停在可停靠边
// → 需唤帧（形裁窄区悬停可能零帧，判定全靠帧 + 光标直采）；展开态与球不在可停靠边
// → 静默。
func TestHeartbeatNeed(t *testing.T) {
	u := &UI{}
	if u.heartbeatNeed() {
		t.Fatal("展开态不应心跳")
	}
	u.collapsed = true
	if u.heartbeatNeed() {
		t.Fatal("收起态球不在可停靠边不应心跳")
	}
	u.edgeNow = "left"
	if !u.heartbeatNeed() {
		t.Fatal("收起态球在可停靠边应心跳")
	}
	u.edgeNow = ""
	u.dockArm = true
	if !u.heartbeatNeed() {
		t.Fatal("布防中应心跳")
	}
	u.dockArm = false
	u.docked = true
	if !u.heartbeatNeed() {
		t.Fatal("停靠中应心跳")
	}
}

// TestEndDragClickKeepsPark 抬手纯点击不夹取（D50 实测缺陷修订，§15.1）：「点击脱离
// 停靠」把窗口落在半出屏贴边位（球锚点越界合法），展开态整窗锚点会把它推离边缘 →
// 收起后球离边超 snapDp、布防/停靠断链——纯点击必须原位保留；真拖动仍按态夹取。
func TestEndDragClickKeepsPark(t *testing.T) {
	u := newHeadless(t, Options{})
	u.frameSize = image.Pt(760, 575) // 展开态锚点 = 整窗
	u.collapsed = false

	// 纯点击：光标与按下点重合（位移 ≤ dragClickSlackPx）→ 跳过夹取，贴边位原样保留。
	u.dragging = true
	u.dragCur0 = cursorPos()
	u.x, u.y = -380, 628
	u.endDrag()
	if u.x != -380 {
		t.Fatalf("纯点击不应夹取: x=%d, want -380", u.x)
	}
	if u.dragging {
		t.Fatal("endDrag 应清 dragging")
	}

	// 真拖动（位移 > slack）：整窗锚点夹取照常（x=-380 中心仍在主屏 → 夹回 ≥ work.left）。
	u.dragging = true
	u.dragCur0 = point{x: cursorPos().x + 100, y: cursorPos().y}
	u.x, u.y = -380, 628
	u.endDrag()
	if u.x == -380 || u.x < 0 {
		t.Fatalf("真拖动应夹入工作区: x=%d, want 变化且 ≥0", u.x)
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
