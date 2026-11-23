package uigui

// motion.go 折叠块的展开/收起动画（D332）：思考、工具 chip、命令条、确认条、Turn 过程
// 摘要、注入块共用一套时长与曲线——展开 200ms（easeOutQuint）、收起 140ms
// （easeInQuart），只动「高度 + 透明度」，不缩放文字。
//
// 进度是**帧前定值**（与 D50/D54 同一条铁律）：布局会被 headless 二次调用，进度若放
// 布局里会双倍推进。这里布局只做两件事——声明目标（`revealFor`，目标变了才起动画，
// 同帧幂等）与读取现值（`revealBody` / 行揭示），推进在 `stepReveals`（frame() 里、
// layout 之前），唤帧由本文件的 ticker 负责。

import (
	"image"
	"math"
	"time"

	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"
)

const (
	motionOpenMs     = 200 // 展开时长
	motionCloseMs    = 140 // 收起时长（比展开快：收起要利落）
	motionStaggerMs  = 12  // Turn 组内子行的错峰步距
	motionStaggerMax = 4   // 错峰最多 4 档（封顶 48ms，总时长不失控）
	motionMinScale   = 0.35
)

// blockKey 一个折叠单元（块序 + 种类）。Turn 组用 blockTurn + 组 slot（组尾 assistant
// 块序）；组内子行的「整行揭示」与子块自己的「正文揭示」是两个键，互不干扰。
type blockKey struct {
	kind blockKind
	bi   int
}

// revealAnim 一个折叠单元的揭示时间线（v = 0 全隐、1 全显）。
type revealAnim struct {
	v     float64
	from  float64
	to    float64
	start time.Time
	dur   time.Duration
	run   bool
}

// cubicBezier CSS 同式缓动（P0=(0,0)、P3=(1,1)，x 控制点 x1/x2、y 控制点 y1/y2）：
// 先解 x(s) = t（牛顿迭代，退化时二分兜底），再取 y(s)。纯函数，可测。
func cubicBezier(x1, y1, x2, y2, t float64) float64 {
	if t <= 0 {
		return 0
	}
	if t >= 1 {
		return 1
	}
	s := t
	for i := 0; i < 8; i++ {
		dx := bezierAt(s, x1, x2) - t
		if math.Abs(dx) < 1e-6 {
			break
		}
		d := bezierAtD(s, x1, x2)
		if math.Abs(d) < 1e-6 {
			break // 端点导数为零（x2=1 的收起曲线）：交给二分
		}
		s -= dx / d
		if s < 0 {
			s = 0
		} else if s > 1 {
			s = 1
		}
	}
	if math.Abs(bezierAt(s, x1, x2)-t) > 1e-3 {
		lo, hi := 0.0, 1.0
		for i := 0; i < 24; i++ {
			mid := (lo + hi) / 2
			if bezierAt(mid, x1, x2) < t {
				lo = mid
			} else {
				hi = mid
			}
		}
		s = (lo + hi) / 2
	}
	return bezierAt(s, y1, y2)
}

// easeOpen 展开曲线 cubic-bezier(.22, 1, .36, 1)：起手快、尾巴长，末段慢慢贴合，
// 不作过冲（高度过冲会让文字抖一下）。
func easeOpen(t float64) float64 { return cubicBezier(0.22, 1, 0.36, 1, t) }

// easeClose 收起曲线 cubic-bezier(.4, 0, 1, 1)：慢起、加速收掉，终点干脆。
func easeClose(t float64) float64 { return cubicBezier(0.4, 0, 1, 1, t) }

// revealAlpha 揭示因子对应的文字透明度：v ≥ 0.7 全实，往下线性淡出（v = 0.1 时为零）。
// 同一条式子对两个方向都成立——展开时文字比高度早到位，收起时文字在最后 30% 才淡掉。
func revealAlpha(v float64) float32 {
	a := (v - 0.1) / 0.6
	if a <= 0 {
		return 0
	}
	if a >= 1 {
		return 1
	}
	return float32(a)
}

// reveal 该块的动画时间线 get-or-create（不存在的块按调用方给的目标值直接落定——
// 首次出现就已是终态，不做入场动画）。
func (u *UI) reveal(k blockKey) *revealAnim {
	if u.reveals == nil {
		u.reveals = map[blockKey]*revealAnim{}
	}
	a := u.reveals[k]
	if a == nil {
		a = &revealAnim{}
		u.reveals[k] = a
	}
	return a
}

// revealValue 该块当前揭示因子（没有时间线 = 0）。纯读。
func (u *UI) revealValue(k blockKey) float64 {
	if a := u.reveals[k]; a != nil {
		return a.v
	}
	return 0
}

// revealFor 声明该块的目标开合态，返回当前揭示因子。目标变了才起动画（同帧第二次
// layout 看到 to 已是目标 → 不再重排），起点取现值——中途反向不跳变。
func (u *UI) revealFor(k blockKey, open bool) float64 { return u.revealForDelay(k, open, 0) }

// revealForDelay 同 revealFor，但可延后 motionStaggerMs 的整数倍起跑（Turn 组内错峰）。
func (u *UI) revealForDelay(k blockKey, open bool, delay time.Duration) float64 {
	target := 0.0
	if open {
		target = 1
	}
	a := u.reveal(k)
	if u.motionInstant { // 测试夹具：没有帧循环推动画，揭示直接落定
		a.v, a.from, a.to, a.run = target, target, target, false
		return a.v
	}
	if a.to == target && (a.run || a.v == target) {
		return a.v
	}
	dist := math.Abs(target - a.v)
	if dist < 1e-3 { // 已在目标（或只差一个浮点尾数）：落定，不起表
		a.v, a.from, a.to, a.run = target, target, target, false
		return a.v
	}
	base := motionOpenMs
	if target < a.to {
		base = motionCloseMs
	}
	scale := math.Max(motionMinScale, dist)
	a.from, a.to = a.v, target
	a.dur = time.Duration(float64(base)*scale) * time.Millisecond
	a.start = time.Now().Add(delay)
	a.run = true
	u.startRevealTicker()
	return a.v
}

// stepReveals 推进所有揭示时间线到 now（frame() 里、**layout 之前**调用）。
func (u *UI) stepReveals(now time.Time) {
	running := false
	for _, a := range u.reveals {
		if !a.run {
			continue
		}
		if now.Before(a.start) { // 错峰延迟段：保持起点
			running = true
			continue
		}
		t := 1.0
		if a.dur > 0 {
			t = float64(now.Sub(a.start)) / float64(a.dur)
		}
		if t >= 1 {
			a.v = a.to
			a.run = false
			continue
		}
		e := easeOpen
		if a.to < a.from {
			e = easeClose
		}
		a.v = a.from + (a.to-a.from)*e(t)
		running = true
	}
	if !running {
		u.stopRevealTicker()
	}
}

// startRevealTicker 起唤帧循环（幂等；headless 无窗口不起——测试里手动 stepReveals）。
func (u *UI) startRevealTicker() {
	if u.w == nil || u.revealStop != nil {
		return
	}
	ch := make(chan struct{})
	u.revealStop = ch
	go func() {
		t := time.NewTicker(16 * time.Millisecond)
		defer t.Stop()
		for {
			select {
			case <-t.C:
				u.w.Invalidate() // 并发安全（同 startExpandAnim）
			case <-ch:
				return
			case <-u.done:
				return
			}
		}
	}()
}

// stopRevealTicker 关停唤帧循环（幂等）。
func (u *UI) stopRevealTicker() {
	if u.revealStop != nil {
		close(u.revealStop)
		u.revealStop = nil
	}
}

// revealBody 把一个折叠体按 v 铺进手排纵列：v ≥ 1 原样铺（稳态零开销）、v ≤ 0 不铺、
// 中间态录成宏后按高度裁剪 + 透明度。
//
// body 收到「本行内容原点 → 折叠体起点」的偏移基址：行选键几何按它加自己内部的 y，
// 与不折叠时逐像素一致（v ≥ 1 走的就是同一条算路）。gapDp 是头行与正文之间的间距，
// 由本函数铺（折叠时不留空隙）。
func (u *UI) revealBody(vs *vstack, v float64, gapDp unit.Dp, body func(base image.Point) layout.Widget) {
	if v <= 0 {
		return
	}
	vs.gap(gapDp)
	base := image.Pt(0, vs.y)
	if v >= 1 {
		vs.add(body(base))
		return
	}
	vs.add(func(gtx layout.Context) layout.Dimensions {
		m := op.Record(gtx.Ops)
		quiet := u.revealQuiet
		u.revealQuiet = true // 半隐的正文不落行选键（选区不落在看不见的字上）
		d := body(base)(gtx)
		u.revealQuiet = quiet
		mac := m.Stop()
		h := int(math.Round(v * float64(d.Size.Y)))
		if h <= 0 {
			return layout.Dimensions{Size: image.Pt(d.Size.X, 0)}
		}
		st := clip.Rect{Max: image.Pt(d.Size.X, h)}.Push(gtx.Ops)
		opacity := paint.PushOpacity(gtx.Ops, revealAlpha(v))
		mac.Add(gtx.Ops)
		opacity.Pop()
		st.Pop()
		return layout.Dimensions{Size: image.Pt(d.Size.X, h)}
	})
}
