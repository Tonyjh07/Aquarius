package uigui

// scroll.go 尾随滚动的平滑追及（D333）与转写区「↓ 最新」回底按钮（D334）。
//
// 旧实现里尾随是每帧硬赋值 `scrollPx = overflow`：内容高多少，画面就跳多远。高 TPS 下
// 一帧可能落进来两三行，观感是「整块顿一下」。这里把「目标位置」（overflow，每帧由
// transcript 量高声明）与「渲染位置」（pos，按时间追）拆开：
//
//   - 追及 = 指数趋近，稳态落后 ∝ 内容增长速度（tau = 90ms，约半行到一行）；
//   - 落后上限 96dp：超过就按帧补足，保证最新内容不会离屏底太远；
//   - 整屏级的跳变（一块工具结果落地）不是流式，直接落定，平滑反而让人等；
//   - 手滚（滚轮 / 拖选贴边 / 键盘）一律当帧生效，并把 pos 对齐——追及只服务尾随。
//
// 进度同样是**帧前定值**（D50/D54/D332 同一条铁律）：stepScroll 在 frame() 里、layout
// 之前推进；布局只声明目标（updateScroll）与读取现值（scrollPx）。

import (
	"image"
	"math"
	"time"

	"gioui.org/io/event"
	"gioui.org/io/key"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/widget/material"

	"github.com/Tonyjh07/Aquarius/internal/i18n"
)

const (
	// scrollTauMs 追及时间常数：每 90ms 走掉约 63% 的差距。太小（30ms）等于没做，
	// 太大（200ms）在生成停顿后迟迟不贴底、末行长期悬在视口外。
	scrollTauMs = 90
	// scrollLagMaxDp 落后上限：超过就按帧补足。按字高约 3 行——再远用户会觉得
	// 「没跟上」，而不是「滚得慢」。
	scrollLagMaxDp = 96
	// scrollJumpMs 回底按钮的定长缓动：距离可能几千 px，用追及曲线会「先糊后慢」。
	scrollJumpMs = 260
	// scrollMaxStepMs 单帧 dt 上限：窗口挂起后回来不追一大段。
	scrollMaxStepMs = 64
	// scrollLandPx 落定阈值（px）：小于它就认为到站，停表（D79 的尾部留白依赖
	// 静止时 scrollPx 精确等于 overflow）。
	scrollLandPx = 0.5

	// scrollPillDp 回底按钮高；PadX 左右内衬；GapDp 与转写区下沿的间隙；
	// FadeMs 出现/消失淡变时长。
	scrollPillDp     = 26
	scrollPillPadXDp = 10
	scrollPillGapDp  = 8
	scrollPillFadeMs = 120
)

// scrollAnim 尾随位置的时间线。jump = 定长缓动（回底按钮），false = 指数追及。
type scrollAnim struct {
	pos   float64
	from  float64
	start time.Time
	dur   time.Duration
	jump  bool
	run   bool
}

// pillAnim 回底按钮的出现/消失（淡变 + 6dp 上浮）。
type pillAnim struct {
	v        float64
	from, to float64
	start    time.Time
	dur      time.Duration
	run      bool
}

// stepScroll 推进尾随位置与回底按钮的淡变到 now（frame() 里、layout 之前），返回
// 是否还有东西在跑（供唤帧心跳决定是否继续唤帧）。纯推进，不碰布局。
func (u *UI) stepScroll(now time.Time) bool {
	u.stepPill(now)
	a := &u.scrollAn
	if !a.run {
		u.scrollLast = now
		return u.pillAn.run
	}
	dt := now.Sub(u.scrollLast)
	u.scrollLast = now
	if dt < 0 {
		dt = 0
	}
	if ms := dt.Seconds() * 1000; ms > scrollMaxStepMs {
		dt = scrollMaxStepMs * time.Millisecond
	}
	if !u.followTail { // 已脱离：位置归手滚管，时间线就此停表
		a.run, a.jump = false, false
		return u.pillAn.run
	}
	target := float64(u.scrollTarget)
	if a.jump {
		t := 1.0
		if a.dur > 0 {
			t = float64(now.Sub(a.start)) / float64(a.dur)
		}
		if t >= 1 || math.Abs(target-a.pos) < scrollLandPx {
			a.pos, a.run, a.jump = target, false, false
			return u.pillAn.run
		}
		a.pos = a.from + (target-a.from)*easeOutCubic(t)
		return true
	}
	lag := target - a.pos
	switch {
	case lag <= scrollLandPx: // 到站（含内容变矮的反向：直接落定，收缩方向没有观看价值）
		a.pos, a.run = target, false
		return u.pillAn.run
	case u.transH > 0 && lag > float64(u.transH): // 整屏级：一块东西落地，不是流式
		a.pos = target
		return u.pillAn.run
	}
	ms := dt.Seconds() * 1000
	a.pos += lag * (1 - math.Exp(-ms/scrollTauMs))
	if maxLag := float64(u.scrollLagPx); maxLag > 0 && target-a.pos > maxLag {
		a.pos = target - maxLag
	}
	return true
}

// stepPill 推进回底按钮的淡变。
func (u *UI) stepPill(now time.Time) {
	a := &u.pillAn
	if !a.run {
		return
	}
	t := 1.0
	if a.dur > 0 {
		t = float64(now.Sub(a.start)) / float64(a.dur)
	}
	if t >= 1 {
		a.v, a.run = a.to, false
		return
	}
	a.v = a.from + (a.to-a.from)*easeOutCubic(t)
}

// pillTarget 声明回底按钮的目标显隐（同帧幂等：目标没变就不重起表）。
func (u *UI) pillTarget(show bool) {
	a := &u.pillAn
	target := 0.0
	if show {
		target = 1
	}
	if a.to == target && (a.run || a.v == target) {
		return
	}
	if u.motionInstant || math.Abs(target-a.v) < 0.01 {
		a.v, a.from, a.to, a.run = target, target, target, false
		return
	}
	a.from, a.to = a.v, target
	a.start = time.Now()
	a.dur = scrollPillFadeMs * time.Millisecond
	a.run = true
	u.startMotionTicker()
}

// snapScroll 位置与手滚/重置对齐：停表、清掉定长缓动。
func (u *UI) snapScroll() {
	u.scrollAn.pos = float64(u.scrollPx)
	u.scrollAn.run, u.scrollAn.jump = false, false
}

// jumpToBottom 回底按钮：定长缓动跑到当前底（增长中会被逐帧追上），并恢复跟随。
func (u *UI) jumpToBottom() {
	a := &u.scrollAn
	u.followTail = true
	a.from = a.pos
	a.start = time.Now()
	a.dur = scrollJumpMs * time.Millisecond
	a.jump, a.run = true, true
	u.startMotionTicker()
}

// scrollKeys 键盘滚动（D333）：PageUp/PageDown 翻一屏；Home/End 只在输入栏为空时
// 让给滚动——胶囊里有字时那是光标语义。展开态编辑器与提问面板一律不接（它们自己
// 用这三组键）。
func (u *UI) scrollKeys(gtx layout.Context, viewH, overflow int) {
	if u.m.ask != nil || u.inputExtraPx(gtx) > 0 {
		return
	}
	filters := []event.Filter{
		key.Filter{Name: key.NamePageUp},
		key.Filter{Name: key.NamePageDown},
	}
	if u.editor.Text() == "" {
		filters = append(filters, key.Filter{Name: key.NameHome}, key.Filter{Name: key.NameEnd})
	}
	for {
		ev, ok := gtx.Event(filters...)
		if !ok {
			return
		}
		ke, isKey := ev.(key.Event)
		if !isKey || ke.State != key.Press {
			continue
		}
		step := viewH - viewH/10
		if step < 1 {
			step = 1
		}
		switch ke.Name {
		case key.NamePageUp:
			u.followTail = false
			u.scrollPx -= step
		case key.NamePageDown:
			u.scrollPx += step
		case key.NameHome:
			u.followTail = false
			u.scrollPx = 0
		case key.NameEnd:
			u.scrollPx = overflow
		}
		if u.scrollPx < 0 {
			u.scrollPx = 0
		}
		if u.scrollPx > overflow {
			u.scrollPx = overflow
		}
		if ke.Name == key.NamePageDown || ke.Name == key.NameEnd {
			if u.scrollPx >= overflow {
				u.followTail = true
			}
		}
		u.snapScroll()
	}
}

// scrollPill 转写区右下角的回底按钮「↓ 最新」（D334）：脱离跟随且有溢出的内容时出现。
//
// 它压在行选观察者之上（同一命中链），点击只做一次回底——回底本身幂等，误起的
// 选区候选下一帧就自愈。**不进量高**（total）：覆盖层一旦参与布局会「按钮把自己顶高
// → 产生新 overflow → 位置又变」自我循环。
func (u *UI) scrollPill(gtx layout.Context, w, h int, viewport image.Rectangle) {
	show := !u.followTail && u.contentH > u.transH
	u.pillTarget(show)
	if u.pillAn.v <= 0.01 {
		return
	}
	l := material.Caption(u.th, i18n.T("ui.scroll.latest"))
	l.Color = tok().pillLatestText
	// 量宽要给宽松约束：帧级 gtx 常带 Exact(w,h)，直接量会把整窗尺寸当成文字宽。
	mg := gtx
	mg.Constraints = layout.Constraints{Max: image.Pt(1<<30, 1<<30)}
	tw, th := measure(mg, l)
	ph := gtx.Dp(scrollPillDp)
	pw := tw + 2*gtx.Dp(scrollPillPadXDp)
	if pw < ph {
		pw = ph // 极窄窗/短文案：退化成圆钮
	}
	slide := gtx.Dp(6)
	y2 := h - gtx.Dp(scrollPillGapDp) - int(math.Round(float64(slide)*(1-u.pillAn.v)))
	rect := image.Rect(w-gtx.Dp(sideMarginDp)-pw, y2-ph, w-gtx.Dp(sideMarginDp), y2)
	radius := ph / 2

	// 淡变 + 上浮：只用透明度与 6dp 位移，不改行几何。
	st := op.Record(gtx.Ops)
	paint.FillShape(gtx.Ops, tok().pillLatest, clip.UniformRRect(rect, radius).Op(gtx.Ops))
	o := op.Offset(image.Pt(rect.Min.X+(pw-tw)/2, rect.Min.Y+(ph-th)/2)).Push(gtx.Ops)
	l.Layout(gtx)
	o.Pop()
	mac := st.Stop()
	opacity := paint.PushOpacity(gtx.Ops, float32(u.pillAn.v))
	mac.Add(gtx.Ops)
	opacity.Pop()
	// chrome 语义的 clip（同 tips / 输入胶囊 / 状态行：整窗）：底带只乘「clip 底 ≤ 带底」
	// 的形状，挂转写视口会让按钮下半截被底带一起淡化——它浮在内容之上，不继承滚动内容
	// 的淡化。frameSize 未定（headless 直接铺转写区）时退回视口，避免不登记形状。
	clipRect := image.Rectangle{Max: u.frameSize}
	if clipRect.Empty() {
		clipRect = viewport
	}
	u.record(rect, radius, tok().pillLatest, clipRect)

	if clickRect(gtx, &u.pillBtn, rect) {
		u.jumpToBottom()
	}
}
