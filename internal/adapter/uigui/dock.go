package uigui

import (
	"fmt"
	"image"
	"math"
	"time"

	"gioui.org/unit"
)

// D50 位置行为（§15.1）：可见锚点夹取 + 四边吸附 + 左右停靠隐藏（滑出窄条 + 整窗
// 淡化）+ 悬停召回。几何/判定全部为纯函数（多屏工作区与外侧屏幕由调用方注入），
// 平台数据取自 platformWorkArea / platformMonitorAt（win32 实装；非 Windows 桩返回
// 不可用 → 夹取/吸附/停靠整体退化为不干预，行为与 D50 之前一致）。
//
// 状态机（仅事件循环 goroutine 读写）：
//
//	正常·收起 ─停在可停靠边+曾悬停+光标移开─► 停靠（滑出+淡化，留窄条）
//	停靠 ─悬停窄条─► 召回（滑回复亮） ─无操作光标移开─► 重停靠（同一判定）
//	召回/停靠中 ─点击·按下·呼出（Alt+A/托盘显示）─► 立即脱离（归位复亮）
//	正常·展开 ─抬手贴边─► 仅吸附贴齐（不隐藏——仅收起态可停靠）
//
// 动画 = easeOutCubic p(t) 纯函数 + 16ms ticker → Invalidate 唤帧；进度与插值在
// frame 的 stepAnim 现算（layout 会被 fadeCompose 以零值 Source 二次调用，位置/alpha
// 前推不能放进 layout；且须排在两遍 layout **之前**，D55），ticker goroutine 只读自有
// channel → 无共享可变状态（-race 友好）。

// 常量（物理 px 经 frameMetric.Dp 换算；位置恢复路径按窗口高推比例，见 restoreDock）。
const (
	snapDp       = 12 // 抬手吸附阈值（距任一边 ≤ 该值 → 贴齐）
	dockSliverDp = 8  // 停靠窄条宽（球留在屏内的部分 = 悬停召回区）
	dockDurMs    = 220
)

// dockAlpha 停靠淡化 alpha（semiAlpha=235 → 96 ≈ 41%：窄条"变暗但仍可寻"）。
const dockAlpha byte = 96

// 停靠边取值（posRec.docked 与 dockHint 原子共用）。
const (
	dockNoneInt  = int32(0)
	dockLeftInt  = int32(1)
	dockRightInt = int32(2)
)

// edgeCode 停靠边 → dockHint 编码。
func edgeCode(edge string) int32 {
	switch edge {
	case "left":
		return dockLeftInt
	case "right":
		return dockRightInt
	default:
		return dockNoneInt
	}
}

// edgeName dockHint 编码 → 停靠边。
func edgeName(code int32) string {
	switch code {
	case dockLeftInt:
		return "left"
	case dockRightInt:
		return "right"
	default:
		return ""
	}
}

// clampAnchor 把窗口位置 pos 夹进 work，使 anchor（窗口系）完全落在 work 内。
// 顺序与旧 clampToWorkArea 一致（右/下先、左/上后 → 超尺寸时左上优先）。
func clampAnchor(pos point, anchor image.Rectangle, work rect) point {
	if aR := pos.x + int32(anchor.Max.X); aR > work.right {
		pos.x -= aR - work.right
	}
	if aB := pos.y + int32(anchor.Max.Y); aB > work.bottom {
		pos.y -= aB - work.bottom
	}
	if aL := pos.x + int32(anchor.Min.X); aL < work.left {
		pos.x += work.left - aL
	}
	if aT := pos.y + int32(anchor.Min.Y); aT < work.top {
		pos.y += work.top - aT
	}
	return pos
}

// snapDelta 抬手吸附：锚点距 work 某边 [0, thr] → 返回贴齐位移（取最近边）；
// 不吸 = 零值。仅在屏内侧吸附（已被夹取保证，超边距的负间隙不吸）。
func snapDelta(pos point, anchor image.Rectangle, work rect, thr int32) point {
	aL := pos.x + int32(anchor.Min.X)
	aT := pos.y + int32(anchor.Min.Y)
	aR := pos.x + int32(anchor.Max.X)
	aB := pos.y + int32(anchor.Max.Y)
	best := int32(-1)
	var res point
	consider := func(dist, dx, dy int32) {
		if dist < 0 || dist > thr {
			return
		}
		if best < 0 || dist < best {
			best, res = dist, point{x: dx, y: dy}
		}
	}
	consider(aL-work.left, work.left-aL, 0)
	consider(work.right-aR, work.right-aR, 0)
	consider(aT-work.top, 0, work.top-aT)
	consider(work.bottom-aB, 0, work.bottom-aB)
	return res
}

// dockableEdge 停靠边判定（D50）：锚点贴齐左/右（±thr 容差）**且该边外侧无相邻
// 显示器**（outLeft/outRight 由 platformMonitorAt 探针给出——跨屏接缝不触发，"滑出"
// 才藏得住）。上下边只吸附不停靠（底边窄条会被任务栏遮挡）。
func dockableEdge(pos point, anchor image.Rectangle, work rect, thr int32, outLeft, outRight bool) string {
	if outLeft {
		if d := pos.x + int32(anchor.Min.X) - work.left; d >= -thr && d <= thr {
			return "left"
		}
	}
	if outRight {
		if d := work.right - (pos.x + int32(anchor.Max.X)); d >= -thr && d <= thr {
			return "right"
		}
	}
	return ""
}

// parkPos 贴齐停靠边的亮态位（召回/未滑出的目标位）。
func parkPos(pos point, anchor image.Rectangle, work rect, edge string) point {
	switch edge {
	case "left":
		pos.x = work.left - int32(anchor.Min.X)
	case "right":
		pos.x = work.right - int32(anchor.Max.X)
	}
	return pos
}

// dockSlidePos 停靠位：锚点自贴齐位滑出至仅剩 sliver 宽留在屏内（窄条 = 召回区）。
func dockSlidePos(pos point, anchor image.Rectangle, work rect, edge string, sliver int32) point {
	pos = parkPos(pos, anchor, work, edge)
	d := int32(anchor.Dx())
	switch edge {
	case "left":
		pos.x -= d - sliver
	case "right":
		pos.x += d - sliver
	}
	return pos
}

// easeOutCubic 缓动（停靠/召回共用；展开/收起的非线性曲线接入时统一收编）。
func easeOutCubic(t float64) float64 {
	if t <= 0 {
		return 0
	}
	if t >= 1 {
		return 1
	}
	u := 1 - t
	return 1 - u*u*u
}

// lerpInt 线性插值取整。
func lerpInt(a, b int, t float64) int {
	return int(math.Round(float64(a) + float64(b-a)*t))
}

// dockAct 停靠评估动作。
type dockAct int

const (
	dockNoneAct   dockAct = iota
	dockStartAct          // 光标移开 → 滑出停靠
	dockRecallAct         // 悬停窄条 → 滑回召回
)

// dockInputs 停靠评估输入（evalDockFrame 每帧组装；纯判定可测）。
type dockInputs struct {
	docked, collapsed, dragging, over bool
	armed                             bool // 上一帧的布防态
	edge                              string
}

// evalDock D50 状态判定（用户拍板语义）：
//   - 停靠中 + 光标在窄条上 → 召回；
//   - 布防（停在可停靠边 + 光标在球上 + 收起 + 非拖动）→ 下一帧光标移开 → 停靠。
//
// 「曾悬停」前置（armed）使启动恢复不自动滑出，而召回移开、召回后拖回可停靠区移开
// 都走同一判定重停靠；全部按帧直采光标与球矩形的包含关系，不依赖 Hover 进出事件时序。
func evalDock(in dockInputs) (armed bool, act dockAct) {
	if in.docked {
		if in.over && !in.dragging {
			return false, dockRecallAct
		}
		return false, dockNoneAct
	}
	now := in.collapsed && !in.dragging && in.edge != "" && in.over
	if in.armed && !in.over && !in.dragging && in.collapsed && in.edge != "" {
		return false, dockStartAct
	}
	return now, dockNoneAct
}

// ballRect 收起球几何（= 展开态 logo 位，D49/§15.2 换形不跳动；与 layoutCollapsed 同式）。
func ballRect(size image.Point, dp func(unit.Dp) int) image.Rectangle {
	x := dp(sideMarginDp)
	y := size.Y - dp(inputRowDp+pillTopDp+16) + dp(pillTopDp)
	d := dp(inputRowDp)
	return image.Rect(x, y, x+d, y+d)
}

// ballAnchor 收起球锚点（窗口系）；false = 尚无帧（frameSize/frameMetric 未就绪）。
func (u *UI) ballAnchor() (image.Rectangle, bool) {
	if u.frameSize.X <= 0 || u.frameSize.Y <= 0 || u.frameMetric.PxPerDp <= 0 {
		return image.Rectangle{}, false
	}
	return ballRect(u.frameSize, u.frameMetric.Dp), true
}

// rowAnchor 展开态输入栏锚点（窗口系，D52）：三段包围盒 = [sideMargin, 镜像边距] ×
// [行上, 行高 48dp]，与 inputBar 同式（行上取 ballRect 同源式 = 底定高、上 8 下 16）。
// 转写消息区不参与——允许越出桌面上沿，只限制输入栏（§15.1）。
func rowAnchor(size image.Point, dp func(unit.Dp) int) image.Rectangle {
	logo, _, send := inputRowRects(size.X, dp(pillTopDp), dp(inputRowDp), dp(inputGapDp), dp(sideMarginDp))
	top := ballRect(size, dp).Min.Y // 行上 = logo 上（D49 换形不跳动，与 layout 同式）
	return image.Rectangle{Min: image.Pt(logo.Min.X, top), Max: image.Pt(send.Max.X, top+dp(inputRowDp))}
}

// inputBtnRects 输入栏圆钮窗口系矩形（rowAnchor 派生，tips 光标直采判定用，D53）：
// logo = 锚点左上角方块、右钮 = 右上角方块（边长 = 行高 48dp）。
func inputBtnRects(size image.Point, dp func(unit.Dp) int, right bool) image.Rectangle {
	a := rowAnchor(size, dp)
	h := a.Dy()
	if right {
		return image.Rect(a.Max.X-h, a.Min.Y, a.Max.X, a.Max.Y)
	}
	return image.Rect(a.Min.X, a.Min.Y, a.Min.X+h, a.Min.Y+h)
}

// overInputBtn 光标是否在输入栏圆钮上（cur = 光标屏幕坐标，生产传 cursorPos 直采）。
// 分层窗按像素 alpha 命中穿透：光标到透明像素/窗外后零 pointer 事件，Hover 收不到
// Leave（实测 tips 移开不消，D53）——显隐判定在事件态之上叠光标直采，与停靠悬停同口径。
func (u *UI) overInputBtn(right bool, cur point) bool {
	if u.frameSize.X <= 0 || u.frameMetric.PxPerDp <= 0 {
		return false
	}
	r := inputBtnRects(u.frameSize, u.frameMetric.Dp, right)
	return insideRect(r.Add(image.Pt(int(u.x), int(u.y))), cur)
}

// cursorHitsLogo 光标命中 logo 钮（D85，tips 门控的最终判定）：矩形直采（上）×
// OS 命中直证——WindowFromPoint 是鼠标路由的同一份真相，窗口出现在静止光标下、
// 首次悬停等零事件场景照常成立，且天然排除被遮挡（命中他窗）与矩形圆角外穿透像素。
// gesture.Hover 的事件态不可靠（D85 实测：Enter 在 Move 下永不投递，仅 Press 会送），
// 故不再参与本判定。无窗口句柄（headless）退化为纯矩形直采。
func (u *UI) cursorHitsLogo(cur point) bool {
	if !u.overInputBtn(false, cur) {
		return false
	}
	if u.hwnd == 0 {
		return true
	}
	return windowFromPoint(cur) == u.hwnd
}

// anchorFor 当前状态的可见锚点（夹取/吸附共用口径，D50/D52）：收起 = 球、
// 展开 = 输入栏包围盒（转写消息区可越出上沿）。
func (u *UI) anchorFor() (image.Rectangle, bool) {
	if u.collapsed {
		return u.ballAnchor()
	}
	if u.frameSize.X <= 0 || u.frameSize.Y <= 0 || u.frameMetric.PxPerDp <= 0 {
		return image.Rectangle{}, false
	}
	return rowAnchor(u.frameSize, u.frameMetric.Dp), true
}

// anchorCenter 锚点中心（屏幕系）——最近显示器判定用。
func anchorCenter(pos point, a image.Rectangle) point {
	return point{
		x: pos.x + int32(a.Min.X+a.Max.X)/2,
		y: pos.y + int32(a.Min.Y+a.Max.Y)/2,
	}
}

// insideRect 点是否落在矩形内（含 Min、不含 Max）。
func insideRect(r image.Rectangle, p point) bool {
	return p.x >= int32(r.Min.X) && p.x < int32(r.Max.X) &&
		p.y >= int32(r.Min.Y) && p.y < int32(r.Max.Y)
}

// clampPos 状态锚点夹取（最近显示器工作区；平台无数据/未就绪 = 原样返回）。
func (u *UI) clampPos(x, y int32) (int32, int32) {
	a, ok := u.anchorFor()
	if !ok {
		return x, y
	}
	work, wok := platformWorkArea(anchorCenter(point{x: x, y: y}, a))
	if !wok {
		return x, y
	}
	c := clampAnchor(point{x: x, y: y}, a, work)
	return c.x, c.y
}

// evalDockFrame 每帧停靠评估（layout 在 updateLogo 之后、仅收起/停靠态调用）。
// 光标位置直采（GetCursorPos 查询类，铁律 1 不限）；平台无数据直接返回。
func (u *UI) evalDockFrame() {
	u.edgeNow = "" // 缺省不可停靠；下述算出后回写（armHeartbeat 心跳判据）
	a, ok := u.ballAnchor()
	if !ok {
		return
	}
	pos := point{x: u.x, y: u.y}
	center := anchorCenter(pos, a)
	work, wok := platformWorkArea(center)
	if !wok {
		return
	}
	edge := ""
	{
		outL := !platformMonitorAt(point{x: work.left - 2, y: center.y})
		outR := !platformMonitorAt(point{x: work.right + 2, y: center.y})
		edge = dockableEdge(pos, a, work, int32(u.frameMetric.Dp(snapDp)), outL, outR)
	}
	// 召回滑行中：目标边即停靠边（中段不贴齐，dockableEdge 会落空——沿用停靠边
	// 才能实现"滑行中光标移开 → 反向重停"）。
	if edge == "" && u.dockAn.active && !u.dockAn.toDock {
		edge = edgeName(u.dockHint.Load())
	}
	u.edgeNow = edge // 当帧可停靠边（心跳判据，见 armHeartbeat）
	over := insideRect(a.Add(image.Pt(int(u.x), int(u.y))), cursorPos())
	armed, act := evalDock(dockInputs{
		docked:    u.docked,
		collapsed: u.collapsed,
		dragging:  u.dragging,
		over:      over,
		armed:     u.dockArm,
		edge:      edge,
	})
	u.dockArm = armed
	switch act {
	case dockStartAct:
		u.startDock(edge, pos, a, work)
	case dockRecallAct:
		u.startUndock(pos, a, work)
	}
}

// tipBandSlackDp 输入行带上方余量（D84）：光标自转写区贴近输入行的 approach 过渡带，
// 让贴着输入行徘徊的直采门控提前进入帧驱动。
const tipBandSlackDp = 12

// heartbeatNeed 心跳判据（状态读取；光标项现取 cursorPos）：停靠中 / 布防中 / 收起态
// 球停在可停靠边（edgeNow 由当帧 evalDockFrame 写入）/ tips 在显（D53：光标直采判定
// 需帧驱动，事件静默时 50ms 复评）/ 光标在输入行带内（D84）→ 需要主动唤帧；否则静默
// 零帧。
func (u *UI) heartbeatNeed() bool {
	return u.heartbeatNeedAt(cursorPos())
}

// heartbeatNeedAt 同 heartbeatNeed，光标项参数化（可测，D84）。
func (u *UI) heartbeatNeedAt(cur point) bool {
	return u.docked || u.dockArm || (u.collapsed && u.edgeNow != "") || u.tipShown ||
		u.cursorInInputBand(cur)
}

// cursorInInputBand 光标是否在输入行带内（D84，窗口系直采）：带 = 窗口全宽 ×
// [输入行块顶 − slack, 窗底]，输入行块含上下透明边距。ULW 位图 alpha=0 即穿透
// （D62）——光标停在边距条上零事件零帧，所有直采门控（tips/布防）失聪，故带内恒
// 心跳供帧。frameMetric/frameSize 未就绪 = false。
func (u *UI) cursorInInputBand(cur point) bool {
	if u.frameSize.X <= 0 || u.frameSize.Y <= 0 || u.frameMetric.PxPerDp <= 0 {
		return false
	}
	dp := u.frameMetric.Dp
	top := u.frameSize.Y - dp(inputRowDp+pillTopDp+16) - dp(tipBandSlackDp)
	r := image.Rect(0, top, u.frameSize.X, u.frameSize.Y).Add(image.Pt(int(u.x), int(u.y)))
	return insideRect(r, cur)
}

// armHeartbeat 收起/停靠态心跳（D50 实测修订）：悬停/移开判定 = 帧 + 光标直采，而
// 位图窄条上的指针悬停可能零帧（窄条悬停 2s 无一帧 → 召回永不触发、点击必有帧才唤出）。
// 故按 heartbeatNeed 以 50ms 主动 Invalidate 唤帧，让悬停召回 / 布防 / 移开停靠按帧
// 直采跑起来。停靠/召回动画期间另有动画 ticker 唤帧；need 不看动画态：召回收尾帧后
// 动画 ticker 已停，心跳须能接续。展开态 layout 先清 dockArm 再来本函数 → 天然收敛
// 关停，无泄漏。
func (u *UI) armHeartbeat() {
	if need := u.heartbeatNeed(); need && u.w != nil {
		if u.armTick != nil {
			return
		}
		ch := make(chan struct{})
		u.armTick = ch
		go func() {
			t := time.NewTicker(50 * time.Millisecond)
			defer t.Stop()
			for {
				select {
				case <-t.C:
					u.w.Invalidate() // 并发安全（与 dockAnim ticker 同模式）
				case <-ch:
					return
				case <-u.done:
					return
				}
			}
		}()
		return
	}
	if u.armTick != nil {
		close(u.armTick)
		u.armTick = nil
	}
}

// savePosRec 持久化当前位置（TopMost 现查；docked = 停靠边，"" = 未停靠）。
// 停靠态存的 X/Y 仅为回退值——恢复时只要 docked 非空即按当前工作区重算停靠位。
func (u *UI) savePosRec(docked string) {
	if u.opts.PosFile == "" {
		return
	}
	tm := topMostQuery()
	savePos(u.opts.PosFile, posRec{X: u.x, Y: u.y, TopMost: &tm, Docked: docked})
}

// startDock 进入停靠：滑出 + 淡化（记忆先存贴齐位 + 停靠边）。
func (u *UI) startDock(edge string, pos point, a image.Rectangle, work rect) {
	if u.w == nil || edge == "" {
		return
	}
	to := dockSlidePos(pos, a, work, edge, int32(u.frameMetric.Dp(dockSliverDp)))
	u.docked = true
	u.dockArm = false
	u.dockHint.Store(edgeCode(edge))
	u.savePosRec(edge)
	fmt.Printf("[dock] 停靠 → %s\n", edge)
	u.beginDockAnim(true, to, dockAlpha)
}

// startUndock 召回：滑回贴齐位 + 复亮（停靠边记忆保留到动画完成，供滑行中的
// 重停判定与中途脱离计算落位）。
func (u *UI) startUndock(pos point, a image.Rectangle, work rect) {
	if u.w == nil {
		return
	}
	edge := edgeName(u.dockHint.Load())
	to := parkPos(pos, a, work, edge)
	u.docked = false
	u.dockArm = false // 下帧重算（光标在球上 → 重新布防）
	fmt.Printf("[dock] 召回 ← %s\n", edge)
	u.beginDockAnim(false, to, semiAlpha)
}

// undockInstant 立即脱离停靠（按下拖动/点击展开/呼出）：停动画、落回贴齐位、复亮。
// 锚点须按收起球算，调用方须在 collapsed 翻转之前调用。
func (u *UI) undockInstant() {
	if !u.docked && !u.dockAn.active {
		return
	}
	if u.dockAn.active && !u.dockAn.toDock {
		// 召回滑行中：直接跳到它本要落的贴齐位。
		u.x, u.y = u.dockAn.toPos.x, u.dockAn.toPos.y
	} else if edge := edgeName(u.dockHint.Load()); edge != "" {
		if a, ok := u.ballAnchor(); ok {
			if work, wok := platformWorkArea(anchorCenter(point{x: u.x, y: u.y}, a)); wok {
				p := parkPos(point{x: u.x, y: u.y}, a, work, edge)
				u.x, u.y = p.x, p.y
			}
		}
	}
	was := u.docked || u.dockAn.active
	u.stopDockAnim()
	u.docked = false
	u.dockArm = false
	u.dockHint.Store(dockNoneInt)
	u.requestMove() // D55：帧内只记账，commitWinGeom 一拍提交
	u.alpha = semiAlpha
	if was {
		fmt.Println("[dock] 脱离停靠")
		u.savePosRec("")
	}
}

// dockAnim 停靠/召回动画（事件循环 goroutine 独占；ticker goroutine 只持有 stop）。
type dockAnim struct {
	active             bool
	toDock             bool      // true = 滑出停靠；false = 滑回召回
	start              time.Time // 起始时刻（进度 = time.Since(start)/dockDurMs）
	fromPos            point     // 起点窗口位（含中途 retarget 的当前位）
	toPos              point     // 终点窗口位
	fromAlpha, toAlpha byte
	stop               chan struct{} // 关闭 = 唤帧循环退出
}

// beginDockAnim 启动（或反向重定）动画：从当前位/当前 alpha 插值到目标。
func (u *UI) beginDockAnim(toDock bool, toPos point, toAlpha byte) {
	u.stopDockAnim()
	a := dockAnim{
		active:    true,
		toDock:    toDock,
		start:     time.Now(),
		fromPos:   point{x: u.x, y: u.y},
		toPos:     toPos,
		fromAlpha: u.alpha,
		toAlpha:   toAlpha,
	}
	if u.w == nil {
		u.dockAn = a // headless：状态到位即可（停靠本不会发生，防御）
		return
	}
	ch := make(chan struct{})
	a.stop = ch
	u.dockAn = a
	go func() {
		t := time.NewTicker(16 * time.Millisecond)
		defer t.Stop()
		for {
			select {
			case <-t.C:
				u.w.Invalidate() // 并发安全（gui.go post 侧同用）
			case <-ch:
				return
			case <-u.done:
				return
			}
		}
	}()
}

// stopDockAnim 终止动画（幂等；终值由调用方负责落位）。
func (u *UI) stopDockAnim() {
	if u.dockAn.stop != nil {
		close(u.dockAn.stop)
	}
	u.dockAn = dockAnim{}
}

// stepAnim 每帧推进停靠动画（frame 调，置于两遍 layout 之前；layout 被 headless 二次
// 调用，位置/alpha 前推只能每帧一次）。完成时收尾：召回侧清停靠记忆并落盘。
func (u *UI) stepAnim() {
	a := &u.dockAn
	if !a.active {
		return
	}
	t := float64(time.Since(a.start)) / float64(dockDurMs*time.Millisecond)
	p := easeOutCubic(t)
	u.x = int32(lerpInt(int(a.fromPos.x), int(a.toPos.x), p))
	u.y = int32(lerpInt(int(a.fromPos.y), int(a.toPos.y), p))
	u.requestMove() // D55：帧内只记账，commitWinGeom 一拍提交
	if al := byte(lerpInt(int(a.fromAlpha), int(a.toAlpha), p)); al != u.alpha {
		u.alpha = al // D62：alpha 随下一帧 ULW 位图同拍提交
	}
	if t >= 1 {
		toDock := a.toDock
		u.stopDockAnim()
		if !toDock {
			// 召回完成：落盘亮态贴齐位 + 未停靠（滑出侧在 startDock 已落盘）。
			u.dockHint.Store(dockNoneInt)
			u.savePosRec("")
		}
	}
}

// restorePx 恢复期 dp→px 换算（frameMetric 未就绪：窗高 / winHeightDp 即 DPI 比例——
// 窗口刚建为默认尺寸，比例精确。restoreDock 与普通恢复夹取共用口径，D50/D52）。
func restorePx(hPx int32) func(dp int) int32 {
	scale := float64(hPx) / float64(winHeightDp)
	return func(dp int) int32 { return int32(math.Round(float64(dp) * scale)) }
}

// restoreDock 位置记忆的停靠恢复（D50）：停靠位按当前工作区重算（存的 X/Y 忽略）；
// 外侧边判定失效（接缝/分辨率变化）→ 返回 false 落回普通恢复（贴边可见）。
// dp→px 比例按窗口高推（此刻 frameMetric 未就绪；窗口刚建即默认尺寸，比例即 DPI
// 缩放——Windows 缩放档 100/125/150/… 皆有理，比例精确）。
func (u *UI) restoreDock(p posRec, rc rect) bool {
	hPx := rc.bottom - rc.top
	if hPx <= 0 {
		return false
	}
	px := restorePx(hPx)
	bx := px(sideMarginDp)
	by := hPx - px(inputRowDp+pillTopDp+16) + px(pillTopDp)
	bd := px(inputRowDp)
	anchor := image.Rect(int(bx), int(by), int(bx+bd), int(by+bd))
	pos := point{x: p.X, y: p.Y}
	center := anchorCenter(pos, anchor)
	work, ok := platformWorkArea(center)
	if !ok {
		return false
	}
	outL := !platformMonitorAt(point{x: work.left - 2, y: center.y})
	outR := !platformMonitorAt(point{x: work.right + 2, y: center.y})
	if (p.Docked == "left" && !outL) || (p.Docked == "right" && !outR) {
		return false
	}
	slide := dockSlidePos(pos, anchor, work, p.Docked, px(dockSliverDp))
	moveWindowTo(slide.x, slide.y)
	u.x, u.y = slide.x, slide.y
	u.alpha = dockAlpha // D62：随首帧 ULW 生效（restoreDock 在首帧前，无需单独下发）
	u.docked = true
	u.dockArm = false
	u.dockHint.Store(edgeCode(p.Docked))
	u.collapsed = true // 停靠只属于收起态（§15.1）
	return true
}
