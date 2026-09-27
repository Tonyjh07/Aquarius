package uigui

import (
	"image"
	"math"
	"time"
)

// 展开/收起动画（D54，§15.1）：双通道**严格先后**——展开 = 输入栏先、消息区后；
// 收起 = 消息区先、输入栏后。进度在 runWindow 帧分支、layout **之前**现算（D50 底座
// 复用：layout 会被 headless 二次调用，进度放里面会双倍推进），ticker 只负责 Invalidate。
const (
	expandBarMs   = 260 // 展开·输入栏（easeOutBack 轻回弹）
	expandMsgMs   = 220 // 展开·消息揭示（CSS ease）
	collapseMsgMs = 160 // 收起·消息揭示（CSS ease）
	collapseBarMs = 220 // 收起·输入栏（easeInSine）
)

// backOutC1 easeOutBack 回弹系数（c1=1.2 → 峰值过冲约 5.3% ≈ 轻回弹 6%，D54 拍板）。
const backOutC1 = 1.2

// backOut easeOutBack：起步快、越过终点后回落（纯函数，可测）。
func backOut(t, c1 float64) float64 {
	if t <= 0 {
		return 0
	}
	if t >= 1 {
		return 1
	}
	u := t - 1
	return 1 + (c1+1)*u*u*u + c1*u*u
}

// inSine easeInSine：慢起快收（收起输入栏——缩回球）。
func inSine(t float64) float64 {
	if t <= 0 {
		return 0
	}
	if t >= 1 {
		return 1
	}
	return 1 - math.Cos(t*math.Pi/2)
}

// bezierAt 三次贝塞尔标量值（P0=0、P3=1、控制点 p1/p2）。
func bezierAt(s, p1, p2 float64) float64 {
	a := 1 - s
	return 3*a*a*s*p1 + 3*a*s*s*p2 + s*s*s
}

// bezierAtD 导数。
func bezierAtD(s, p1, p2 float64) float64 {
	a := 1 - s
	return 3*p1*a*(a-2*s) + 3*p2*s*(2*a-s) + 3*s*s
}

// cssEase CSS 全局缓动 ease = cubic-bezier(.25, .1, .25, 1)（消息区曲线，D54 拍板）。
// x 控制点两侧同为 0.25 → x(s) 单调且导数恒正（极小值 0.5625），牛顿迭代稳定收敛；
// 另留定点保护避免数值噪声（纯函数，可测）。
func cssEase(t float64) float64 {
	if t <= 0 {
		return 0
	}
	if t >= 1 {
		return 1
	}
	s := t
	for i := 0; i < 8; i++ {
		d := bezierAtD(s, 0.25, 0.25)
		if d <= 1e-9 {
			break
		}
		ns := s - (bezierAt(s, 0.25, 0.25)-t)/d
		if ns < 0 {
			ns = 0
		} else if ns > 1 {
			ns = 1
		}
		if math.Abs(ns-s) < 1e-7 {
			s = ns
			break
		}
		s = ns
	}
	return bezierAt(s, 0.1, 1.0)
}

// easeOutBack 轻回弹（展开·输入栏，c1 = backOutC1）。
func easeOutBack(t float64) float64 { return backOut(t, backOutC1) }

// expandPhase 双通道阶段表（D54 拍板时序）：
//
//	展开 phase1 = 输入栏 barP→1 / phase2 = 消息 msgP→1
//	收起 phase1 = 消息 msgP→0 / phase2 = 输入栏 barP→0
//
// 返回：本阶段推进哪条通道（bar）、目标值、时长（ms）、缓动。
func expandPhase(expand, phase2 bool) (bar bool, to, durMs float64, ease func(float64) float64) {
	switch {
	case expand && !phase2:
		return true, 1, expandBarMs, easeOutBack
	case expand:
		return false, 1, expandMsgMs, cssEase
	case !phase2:
		return false, 0, collapseMsgMs, cssEase
	default:
		return true, 0, collapseBarMs, inSine
	}
}

// expandAnim 展开/收起动画状态（仅事件循环 goroutine 读写；ticker goroutine 只持有 stop）。
// barP = 输入栏几何进度（0 = 收起几何、1 = D49 终位，过冲可 >1）；msgP = 消息揭示进度
// （0 = 全隐、1 = 静息顶带）。静止态不看这两个字段，由 collapsed 推导（见 expandProgress）。
type expandAnim struct {
	active bool
	expand bool      // 当前方向：true = 展开
	phase2 bool      // 已进第二阶段
	start  time.Time // 当前阶段起始时刻
	from   float64   // 当前阶段通道的起点值
	barP   float64
	msgP   float64
	stop   chan struct{} // 唤帧循环退出信号（nil = 未运行）
}

func (a *expandAnim) get(bar bool) float64 {
	if bar {
		return a.barP
	}
	return a.msgP
}

func (a *expandAnim) set(bar bool, v float64) {
	if bar {
		a.barP = v
	} else {
		a.msgP = v
	}
}

// advance 把进度推进到 now（每帧一次）：本阶段跑完即无缝切入下一阶段；两阶段都完成
// 则 active=false。**无距离的阶段瞬时跳过**——中途反向后常见（如展开到消息阶段再反收，
// 此时 barP 已是 1，收起第一阶段无位移），避免空转吃掉时长。
func (a *expandAnim) advance(now time.Time) {
	for a.active {
		bar, to, durMs, ease := expandPhase(a.expand, a.phase2)
		if a.from == to {
			// 无距离的阶段（中途反向后常见）→ 瞬时跳过，不吃时长。
			a.set(bar, to)
			if !a.nextPhaseAt(now) {
				return
			}
			continue
		}
		t := float64(now.Sub(a.start)) / (durMs * float64(time.Millisecond))
		if t <= 0 {
			a.set(bar, a.from)
			return
		}
		if t >= 1 {
			// 本阶段按**计划时长**收尾、余量结转给下一阶段（总时长精确，不因帧边界多吃一帧）。
			a.set(bar, to)
			if !a.nextPhaseAt(a.start.Add(time.Duration(durMs) * time.Millisecond)) {
				return
			}
			continue
		}
		a.set(bar, a.from+(to-a.from)*ease(t))
		return
	}
}

// nextPhaseAt 进入第二阶段，以其计划起点 t0 计时；已在第二阶段 → 收尾。
// 新阶段起点值 = 该通道当前值（该通道在上一阶段未被改动）。
func (a *expandAnim) nextPhaseAt(t0 time.Time) bool {
	if a.phase2 {
		a.active = false
		return false
	}
	a.phase2 = true
	a.start = t0
	bar, _, _, _ := expandPhase(a.expand, true)
	a.from = a.get(bar)
	return true
}

// expandProgress 当前双通道进度（纯读，可测）：动画中取现算值，静止由 collapsed 推导——
// 直接翻 collapsed 的调用方不必手工同步 barP/msgP。
func (u *UI) expandProgress() (barP, msgP float64) {
	if u.expandAn.active {
		return u.expandAn.barP, u.expandAn.msgP
	}
	if u.collapsed {
		return 0, 0
	}
	return 1, 1
}

// stepExpand 每帧前推展开动画（runWindow 帧分支、**layout 之前**调用，D54/D50：
// headless 二次 layout 必须与主窗同帧同进度）。完成后顺手关停唤帧循环。
func (u *UI) stepExpand(now time.Time) {
	if !u.expandAn.active {
		return
	}
	u.expandAn.advance(now)
	if !u.expandAn.active {
		u.stopExpandTicker()
	}
}

// startExpandAnim 启动（或反向重定）动画：起点 = 当前有效进度，**中途反向不跳变**。
// headless（u.w == nil）只落状态、不起 ticker（D54：测试里没有帧循环去推进它）。
func (u *UI) startExpandAnim(expand bool) {
	a := &u.expandAn
	if a.active && a.expand == expand {
		return // 同向已在进行
	}
	barP, msgP := u.expandProgress() // 反向时 = 中途进度；静止时 = collapsed 推导值
	a.barP, a.msgP = barP, msgP
	a.active = true
	a.expand = expand
	a.phase2 = false
	a.start = time.Now()
	bar, _, _, _ := expandPhase(expand, false)
	a.from = a.get(bar)
	u.stopExpandTicker()
	if u.w == nil {
		return // headless：状态到位即可
	}
	ch := make(chan struct{})
	a.stop = ch
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

// stopExpandTicker 关停唤帧循环（幂等）。
func (u *UI) stopExpandTicker() {
	if u.expandAn.stop != nil {
		close(u.expandAn.stop)
		u.expandAn.stop = nil
	}
}

// beginExpand 展开（呼出/单击球/从收起中途反向，D54）：先脱离停靠（D50，锚点按收起
// 球算须先于 collapsed 翻转），再起动画、翻逻辑态。
// **先起动画再翻 collapsed**——expandProgress 静止态由 collapsed 推导，翻早了会把起点
// 读成 0/0（收起态）导致动画无距离、瞬时完成。
func (u *UI) beginExpand() {
	u.undockInstant()
	u.focusPending = true // 展开即入焦点（呼出语义，§15.1）
	if !u.collapsed && !u.expandAn.active {
		u.invalidate()
		return // 已展开：呼出只补焦点
	}
	u.startExpandAnim(true)
	u.collapsed = false
	u.invalidate()
}

// beginCollapse 收起回球（单击 logo / 互切 / 从展开中途反向，D54）。同理先起动画再翻态。
func (u *UI) beginCollapse() {
	if u.collapsed && !u.expandAn.active {
		return // 已收起且无动画可反向
	}
	u.startExpandAnim(false)
	u.collapsed = true
	u.invalidate()
}

// toggleExpand 单击 logo = 互切；动画中则**反向续跑**（从当前进度出发，不跳变）。
func (u *UI) toggleExpand() {
	if u.expandAn.active {
		if u.expandAn.expand {
			u.beginCollapse()
		} else {
			u.beginExpand()
		}
		return
	}
	if u.collapsed {
		u.beginExpand()
	} else {
		u.beginCollapse()
	}
}

// invalidate 请求重绘（headless 无窗口，静默跳过）。
func (u *UI) invalidate() {
	if u.w != nil {
		u.w.Invalidate()
	}
}

// barP 当前输入栏展开进度（0 = 收起几何，1 = D49 终位；过冲可 >1）。
func (u *UI) barP() float64 {
	p, _ := u.expandProgress()
	return p
}

// lerpRowRects 三段几何按展开进度插值（D54 纯逻辑，可测）：p≤0 时胶囊/右钮都退化为
// logo 同尺寸圆；p=1 取终位（插值在此精确）；p>1 按同一式外推——easeOutBack 过冲段
// 右钮/胶囊就此越出终位再回落。
func lerpRowRects(logo, pill, send image.Rectangle, p float64) (image.Rectangle, image.Rectangle) {
	if p < 0 {
		p = 0
	}
	return lerpRect(logo, pill, p), lerpRect(logo, send, p)
}

// revealBand 消息揭示带（D54 纯逻辑，可测）：把「内容静止、淡化区域从底向上移动」建模为
// 一条动画的顶部淡出带——带顶 top 从转写区底 h（全隐）升到 0（静息），带内 smoothstep
// 淡入，带顶以上不可见（形裁裁掉 + overlay 不写），带底以下全可见。
//   - msgP=1（静息）：退化为现有顶带 [0, bandPx)，与 §15.3 逐像素重合；
//   - msgP=0（全隐）：带退化为空、top=bottom=h → 转写区整片不可见；
//   - 中间：**带底夹在 h 内**——展开到一半的带绝不压到状态行/刚弹出的输入行。
//
// 返回 (top, bottom)。
func revealBand(msgP float64, h, bandPx int) (top, bottom int) {
	if h < 0 {
		h = 0
	}
	if bandPx < 0 {
		bandPx = 0
	}
	if msgP >= 1 {
		return 0, bandPx // 静息 = 现有顶带（不按 h 夹，保持既有口径）
	}
	if msgP < 0 {
		msgP = 0
	}
	top = int(math.Round((1 - msgP) * float64(h)))
	if top > h {
		top = h
	}
	bottom = top + bandPx
	if bottom > h {
		bottom = h
	}
	if bottom < top {
		bottom = top
	}
	return top, bottom
}

// lerpRect 矩形线性插值（两端角各自插值；D54 输入栏几何：p=0 时 pill/send 都退化为
// logo 同尺寸圆，p=1 = 终位，过冲 p>1 时按同一式外推）。
func lerpRect(a, b image.Rectangle, t float64) image.Rectangle {
	return image.Rectangle{
		Min: image.Pt(lerpInt(a.Min.X, b.Min.X, t), lerpInt(a.Min.Y, b.Min.Y, t)),
		Max: image.Pt(lerpInt(a.Max.X, b.Max.X, t), lerpInt(a.Max.Y, b.Max.Y, t)),
	}
}
