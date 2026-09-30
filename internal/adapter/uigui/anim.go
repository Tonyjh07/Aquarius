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
	expandMsgMs   = 440 // 展开·消息揭示（CSS ease）
	collapseMsgMs = 320 // 收起·消息揭示（CSS ease）
	collapseBarMs = 220 // 收起·输入栏（easeInSine）
)

// D77 胶囊内容淡入/淡出：时长 fadeMs、曲线 CSS ease；收起淡出延迟 fadeOutDelayMs 起跑
// → 与消息区收起（collapseMsgMs）同一刻结束（消息区先开始、内容后开始、同时收尾）。
const (
	fadeMs         = 180                    // 内容淡入/淡出时长（ms）
	fadeOutDelayMs = collapseMsgMs - fadeMs // 收起淡出延迟起跑（ms）= 140
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
// barP = 输入栏行程进度（= send 行程，D76：0 = 收起球、1 = D49 终位，过冲可 >1）；msgP = 消息揭示进度
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

// pillFade 胶囊内容显隐的独立 alpha 时间线（D77）：淡入 = 展开输入栏阶段完成触发、
// 淡出 = 收起布防后延迟 fadeOutDelayMs 起跑（与消息区收起同一刻结束）；时长 fadeMs、
// 曲线 CSS ease。仅事件循环 goroutine 读写。
type pillFade struct {
	alpha   float64   // 当前值（静止由 collapsed 推导回写；动画无时间线时保持——反向续住）
	running bool      // 时间线进行中（含延迟段）
	from    float64   // 起点
	to      float64   // 目标
	start   time.Time // 计划起点（可晚于布防时刻 = 延迟起跑，段内取 from）
	durMs   float64
}

// arm 布防：自 start 起 durMs 内从 from 缓动到 to（start 可为未来时刻 = 延迟起跑）。
func (f *pillFade) arm(from float64, start time.Time, to float64, durMs float64) {
	f.running = true
	f.from, f.to, f.start, f.durMs = from, to, start, durMs
}

// cancel 取消进行中的时间线、保持现值——反向重展开用。**不按墙钟回算**：现值来自
// 最近一帧定帧（≤16ms 新），回算会把已推进的现值拉回去造成闪变。
func (f *pillFade) cancel() {
	f.running = false
}

// value 时间线在 now 的现值（纯推进，可测）：延迟段取 from；跑完落定 f.alpha 并停表。
func (f *pillFade) value(now time.Time) float64 {
	if !f.running {
		return f.alpha
	}
	t := float64(now.Sub(f.start)) / (f.durMs * float64(time.Millisecond))
	if t <= 0 {
		return f.from // 延迟起跑段内保持起点
	}
	if t >= 1 {
		f.running = false
		f.alpha = f.to
		return f.to
	}
	f.alpha = f.from + (f.to-f.from)*cssEase(t)
	return f.alpha
}

// contentAlpha 胶囊内容 alpha（D77，stepExpand 每帧定帧）：时间线优先（推进并回写现值）
// → 展开动画中无时间线则保持现值（fresh expand = 静止收起同步的 0）→ 静止由 collapsed
// 推导回写（expandProgress 同款，直接翻 collapsed 的调用方不必手工同步）。
func (u *UI) contentAlpha(now time.Time) float64 {
	f := &u.pillFade
	if f.running {
		return f.value(now)
	}
	if u.expandAn.active {
		return f.alpha
	}
	if u.collapsed {
		f.alpha = 0
	} else {
		f.alpha = 1
	}
	return f.alpha
}

// stepExpand 每帧前推展开动画与内容淡入时间线（runWindow 帧分支、**layout 之前**调用，
// D54/D50/D77：headless 二次 layout 必须与主窗同帧同进度 → pillAlpha 在此定帧、两遍
// layout 只读）。完成后顺手关停唤帧循环——**动画与时间线都结束才停**（D77 淡入可越过
// 动画收尾）。
func (u *UI) stepExpand(now time.Time) {
	a := &u.expandAn
	if a.active {
		enteredPhase2 := !a.phase2
		a.advance(now)
		// D77 淡入：展开输入栏阶段完成（bar 到位、进消息阶段）→ 胶囊内容 fadeMs CSS ease
		// 淡满，与消息揭示并行（不等双通道全完成）。起点取 advance 转段时写入的 a.start
		//（= bar 计划终点；无距离瞬跳则为当帧时刻）——帧分片不推后触发。
		if a.expand && enteredPhase2 && a.phase2 {
			u.pillFade.arm(u.contentAlpha(a.start), a.start, 1, fadeMs)
		}
	}
	u.pillAlpha = u.contentAlpha(now) // 定帧（推进时间线并回写现值）
	if !a.active && !u.pillFade.running {
		u.stopExpandTicker() // 幂等；淡入未完不关（ticker 续命到两者皆终）
	}
}

// startExpandAnim 启动（或反向重定）动画：起点 = 当前有效进度，**中途反向不跳变**。
// headless（u.w == nil）只落状态、不起 ticker（D54：测试里没有帧循环去推进它）。
func (u *UI) startExpandAnim(expand bool) {
	a := &u.expandAn
	if a.active && a.expand == expand {
		return // 同向已在进行
	}
	now := time.Now()
	// D77 胶囊内容显隐，须在 expandAn 翻转前取值（contentAlpha 读 expandAn/collapsed）：
	// 收起 → 淡出布防、延迟 fadeOutDelayMs 起跑（与消息区收起同一刻结束，同一 now 保证
	// 与 a.start 对齐）；反向重展开 → 取消进行中的淡出、保持现值（内容不闪隐），淡入由
	// bar 完成触发（stepExpand）。
	if expand {
		u.pillFade.cancel()
	} else {
		u.pillFade.arm(u.contentAlpha(now), now.Add(fadeOutDelayMs*time.Millisecond), 0, fadeMs)
	}
	barP, msgP := u.expandProgress() // 反向时 = 中途进度；静止时 = collapsed 推导值
	a.barP, a.msgP = barP, msgP
	a.active = true
	a.expand = expand
	a.phase2 = false
	a.start = now
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

// barP 当前输入栏行程进度（= send 行程：0 = 收起球，1 = D49 终位；过冲可 >1；
// D76 分段几何的时钟——胶囊几何由它分段导出）。
func (u *UI) barP() float64 {
	p, _ := u.expandProgress()
	return p
}

// rowRectsFromSend 输入栏几何按 send 行程进度分段导出（D76，修订 D54 单标量插值；纯逻辑、
// 可测）：send = logo→终位线性插值（`barP` 即 send 行程，回弹过冲 p>1 按同一式外推越出终位
// 再回落；w>0 时先 `clampRowX` 收界，D57）；胶囊按分界点 split（send.Min 抵达成圆处 ≈120/528）
// 分两支——
//   - sendP ≥ split（缩/长段）：左缘钉 pillEnd.Min（与 logo 间隙恒 gap），右缘 = send.Min − gap
//     （与 send 间隙恒 gap；send 全程同步移动，胶囊由**夹后** send 导出 → 过冲贴边夹掉后
//     双间隙仍恒 gap）；
//   - sendP < split（平移/合球段）：胶囊已成 ⌀48 圆（宽 = 行高、停 pillEnd.Min），与 send
//     同步插值到 logo 合球（展开反向：圆先弹出到 split 再长宽）。
//
// 单函数无方向参数 → 两方向同一路径，split 处连续、中途反向天然不跳变。split ≤ 0（退化
// 窄窗）恒走平移支、胶囊贴 logo 不动。
func rowRectsFromSend(logo, pillEnd, sendEnd image.Rectangle, sendP float64, w int) (image.Rectangle, image.Rectangle) {
	if sendP < 0 {
		sendP = 0
	}
	send := lerpRect(logo, sendEnd, sendP)
	if w > 0 {
		send = clampRowX(send, w)
	}
	gap := pillEnd.Min.X - logo.Max.X
	rowH := logo.Dy()
	// split = send.Min 抵达成圆处（pillEnd.Min + rowH + gap）的行程进度。
	split := 0.0
	if span := float64(sendEnd.Min.X - logo.Min.X); span > 0 {
		split = float64(pillEnd.Min.X+rowH+gap-logo.Min.X) / span
	}
	if split > 0 && sendP >= split {
		// 缩/长段：左缘钉终位、右缘随 send − gap → 只改宽不移位。
		return image.Rectangle{
			Min: pillEnd.Min,
			Max: image.Pt(send.Min.X-gap, pillEnd.Max.Y),
		}, send
	}
	// 平移/合球段：胶囊圆与 send 同步插值（q=0 → logo 圆、q→1 → 成圆位）。
	q := 0.0
	if split > 0 {
		q = sendP / split
	}
	circle := image.Rectangle{
		Min: pillEnd.Min,
		Max: image.Pt(pillEnd.Min.X+rowH, pillEnd.Min.Y+rowH),
	}
	return lerpRect(logo, circle, q), send
}

// clampRowX 过冲几何夹回窗口边界（D57 纯逻辑，可测）：easeOutBack 峰值（barP≈1.053）下
// 右钮右缘会越过窗宽被窗边切平（无窗口实测 620 > 608，越界部分被窗界裁掉）——行右边
// 距只剩 sideMarginDp，回弹位移却 = (p-1)×(终位−原点) ≈ 28px。只**平移收界**、不改尺寸：
// 窗内 16px 行边距留给回弹，D54 的「越出终位再回落」在窗内仍可见。只夹 X——行 Y 是行内
// 局部坐标（真实位置经 absY 另加）、D49 行位不随动画变，不会越界。
func clampRowX(r image.Rectangle, w int) image.Rectangle {
	if w <= 0 {
		return r
	}
	if r.Dx() > w { // 极窄窗兜底：保不住尺寸才退化为压缩
		r.Max.X = r.Min.X + w
	}
	if r.Max.X > w {
		r = r.Sub(image.Pt(r.Max.X-w, 0))
	}
	if r.Min.X < 0 {
		r = r.Add(image.Pt(-r.Min.X, 0))
	}
	return r
}

// revealBand 消息揭示带（D54 纯逻辑，可测）：把「内容静止、淡化区域从底向上移动」建模为
// 一条动画的顶部淡出带——带顶 top 从转写区底 h（全隐）升到 0（静息），带内 smoothstep
// 淡入，带顶以上不可见（位图不写），带底以下全可见。
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
