package uigui

// history_treeedit.go 树编辑模式（D257/D258）在会话历史窗的呈现。
//
// 状态只有一份、在内核（`/tree-edit` 进出，port.TreeView.TreeEditState 读回）：本文件
// 只是把那份状态画成状态栏右端的一个开关，外加脏时的「已改 N 处 / 放弃」。UI **不自己
// 记**模式开关——两份状态一旦分叉，就会出现「开关说开着、内核说没有」这种自相矛盾的界面。
//
// 光带、拖动改挂、图区胶囊见 relmap_glow.go / relmap_edit.go（§5.6.2/§5.6.3）。

import (
	"image"
	"image/color"
	"time"

	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"
	"gioui.org/widget/material"

	"github.com/Tonyjh07/Aquarius/internal/port"
)

// 开关档位（§5.6.1）：轨道 44×24dp、圆钮 20dp、两端各留 2dp 内边距，钮位移 160ms
// easeOutCubic。这些是 L4 呈现层的偶然实现（§7 可调），不影响任何语义。
const (
	relTreeSwitchW  = 44
	relTreeSwitchH  = 24
	relTreeKnobDp   = 20
	relTreeKnobPad  = 2
	relTreeSwitchMs = 160
)

// relSwitchKnob 圆钮颜色：恒白（浅色轨道上靠描边区分，深色轨道上靠亮度区分）。
// 不取 accentFg——深色主题的 accentFg 是深色，「白钮」会变成深钮。
var relSwitchKnob = color.NRGBA{R: 0xFF, G: 0xFF, B: 0xFF, A: 0xFF}

// relEaseOutCubic 缓动（§5.6.1）：起步快、收尾缓，开关钮的「弹到位」感全在这里。
func relEaseOutCubic(t float64) float64 {
	if t <= 0 {
		return 0
	}
	if t >= 1 {
		return 1
	}
	u := 1 - t
	return 1 - u*u*u
}

// relTween 单段补间的帧态：布防时记下起点与目标，此后每帧按墙钟取现值。
//
// 光带（§5.6.2 的扫亮/呼吸）与开关钮共用它——两处的共同点都是「目标变了才起跑，
// 同目标每帧调用不动」，这正是 retarget 的判据。
type relTween struct {
	v       float64               // 当前显示值（静止时即终值）
	from    float64               // 本段起点
	to      float64               // 本段目标
	start   time.Time             // 本段起跑时刻
	durMs   float64               // 段时长
	ease    func(float64) float64 // 缓动（nil = 线性）
	running bool                  // 段进行中
}

// retarget 目标变化才起跑（同目标重复调用不重置起点——否则每帧重起、永远走不到头）。
func (a *relTween) retarget(now time.Time, to, durMs float64, ease func(float64) float64) {
	if a.running {
		if a.to == to {
			return
		}
	} else if a.v == to {
		return
	}
	a.from, a.to, a.start, a.durMs, a.ease = a.v, to, now, durMs, ease
	a.running = true
}

// at 时间线在 now 的现值（纯推进，可测）：跑完落定并停表。
func (a *relTween) at(now time.Time) float64 {
	if !a.running {
		return a.v
	}
	t := float64(now.Sub(a.start)) / (a.durMs * float64(time.Millisecond))
	if t <= 0 {
		return a.from
	}
	if t >= 1 {
		a.v, a.running = a.to, false
		return a.to
	}
	e := a.ease
	if e == nil {
		e = func(t float64) float64 { return t }
	}
	a.v = a.from + (a.to-a.from)*e(t)
	return a.v
}

// active 本帧是否还要续帧（过渡期才请求重绘，静止即停——与物理收敛同一纪律 R3）。
func (a *relTween) active() bool { return a.running }

// syncTreeEdit 帧首取树编辑状态（端口是唯一来源；端口缺失 = 不在模式里）。
func (st *historyState) syncTreeEdit(u *UI) {
	if u.opts.Tree == nil {
		st.teOn, st.teEdits = false, 0
		return
	}
	st.teOn, st.teEdits = u.opts.Tree.TreeEditState()
}

// sendTreeEdit 投一家子 `/tree-edit`（与键入同路径，壳内不碰结构）：args 空 = 进入，
// `--exit` = 关开关（有改动按 §5.6.4 三选一），`--cancel` = 放弃改动退出。
func (st *historyState) sendTreeEdit(u *UI, args ...string) {
	select {
	case u.inCh <- port.UserInput{Command: &port.Command{Name: "tree-edit", Args: args}}:
	default:
	}
}

// histTreeEditLabel 开关左侧的常显标签（§5.6.1：标签常显，不靠开关颜色猜含义）；
// 开着时用正文色，关着时退一档。
func histTreeEditLabel(gtx layout.Context, th *material.Theme, on bool) layout.Dimensions {
	l := material.Caption(th, "树编辑")
	l.Color = paperOf().text2
	if on {
		l.Color = paperOf().text
	}
	return l.Layout(gtx)
}

// histTreeSwitch 状态栏右端的树编辑开关：44×24 轨道 + 20dp 白钮（160ms easeOutCubic
// 位移，轨道色同步从 hairline 过渡到 accent）。
func histTreeSwitch(gtx layout.Context, th *material.Theme, st *historyState) layout.Dimensions {
	target := 0.0
	if st.teOn {
		target = 1.0
	}
	st.teKnob.retarget(gtx.Now, target, relTreeSwitchMs, relEaseOutCubic)
	v := st.teKnob.at(gtx.Now)
	if st.teKnob.active() {
		gtx.Execute(op.InvalidateCmd{}) // 过渡期续帧；落定即停
	}
	sz := image.Pt(gtx.Dp(unit.Dp(relTreeSwitchW)), gtx.Dp(unit.Dp(relTreeSwitchH)))
	return st.btnTree.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		gtx.Constraints = layout.Exact(sz)
		pp := paperOf()
		track := relMixColor(pp.hairline, pp.accent, float32(v))
		paint.FillShape(gtx.Ops, track,
			clip.UniformRRect(image.Rectangle{Max: sz}, sz.Y/2).Op(gtx.Ops))
		kr := float64(gtx.Dp(unit.Dp(relTreeKnobDp))) / 2
		pad := float64(gtx.Dp(unit.Dp(relTreeKnobPad)))
		cx := pad + kr + v*(float64(sz.X)-2*pad-2*kr)
		cy := float64(sz.Y) / 2
		knob := image.Rect(int(cx-kr+0.5), int(cy-kr+0.5), int(cx+kr+0.5), int(cy+kr+0.5))
		paint.FillShape(gtx.Ops, relSwitchKnob, clip.UniformRRect(knob, int(kr)).Op(gtx.Ops))
		return layout.Dimensions{Size: sz}
	})
}

// histVDivider 状态栏里的一段竖分隔（1dp 宽）：把「视图控件」与「树编辑」分成两组。
// 两侧各留 margin，上下各缩 h 的一半——不缩则它顶着行的上下沿，读成表格线。
func histVDivider(gtx layout.Context, gap, h int) layout.Dimensions {
	w := gtx.Dp(unit.Dp(1))
	total := gtx.Dp(unit.Dp(h))
	return layout.Inset{Left: unit.Dp(gap), Right: unit.Dp(gap)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		paint.FillShape(gtx.Ops, paperOf().hairline,
			clip.Rect(image.Rect(0, 0, w, total)).Op())
		return layout.Dimensions{Size: image.Pt(w, total)}
	})
}

// relMixColor 两色线性插值（t=0 取 a、t=1 取 b），alpha 同步插值。
func relMixColor(a, b color.NRGBA, t float32) color.NRGBA {
	if t <= 0 {
		return a
	}
	if t >= 1 {
		return b
	}
	mix := func(x, y uint8) uint8 { return uint8(float32(x) + (float32(y)-float32(x))*t) }
	return color.NRGBA{R: mix(a.R, b.R), G: mix(a.G, b.G), B: mix(a.B, b.B), A: mix(a.A, b.A)}
}
