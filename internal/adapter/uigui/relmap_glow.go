package uigui

// relmap_glow.go 树编辑模式的光带（D257/§5.6.2）：右图面板内缘一圈 12dp 的四色光带，
// 进入时从顶边中点顺时针扫亮、扫完呼吸到静止亮度；退出时整体淡出（不反向扫）。
//
// 为什么用「光带」而不是改边框色：整圈光晕是**面积**信号，余光就能看见；改一条 1dp 边框
// 色要盯着看才分辨得出。它的唯一职责是回答「我现在会不会拖一下就改到结构」。
//
// 实现约束（Gio）：`paint.LinearGradientOp` 只有两个色标，所以四色过渡靠**八段带子**
// （四边各两段）各画一道两色渐变——每段在接缝端掺 30% 邻色、在中点端纯本色，读出「沿周长
// 过渡」而不出现硬接缝；代价是接缝不是硬性 20dp 分界，见设计文档 §5.6.2 的实测记录。

import (
	"image"
	"image/color"
	"time"

	"gioui.org/f32"
	"gioui.org/layout"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"
)

// 光带档位：带宽、外缘圆角、扫亮/呼吸/淡出三段时长与两个亮度档、接缝掺色比例、
// 浅色主题的 alpha 增益——全是呈现常数，改这些只改观感，不动任何契约。
const (
	relGlowBand   = 12.0 // 最外层带宽（dp）；其余三层按比例缩
	relGlowRound  = 10.0 // 外缘圆角（dp）
	relGlowSweep  = 260  // 进入扫亮时长（ms）
	relGlowBreath = 600  // 扫完后的呼吸时长（ms）：0.85 → 0.7
	relGlowFade   = 160  // 退出淡出时长（ms）
	relGlowLit    = 0.85 // 扫亮期间与刚落定时的亮度
	relGlowRest   = 0.70 // 呼吸后的静止亮度
	// relGlowSeamOwn 接缝端本色占比：0.7 = 掺 30% 邻色。再低四色读不出来，再高接缝露白点。
	relGlowSeamOwn = 0.7
	// relGlowLightGain 浅色主题的 alpha 增益（§5.6.2：白底上不加强几乎看不见）。
	relGlowLightGain = 1.25
)

// relGlowLayers 四层描边的「宽度占比 + alpha」（§5.6.2）：最内 1dp 亮线给锐边，外侧三层
// 给晕开的衰减——单层描边在浅底上会像一条脏边。
var relGlowLayers = [4]struct{ W, A float64 }{
	{12, 0.10}, {8, 0.14}, {3, 0.32}, {1, 0.55},
}

// 四色（顶紫、右蓝、底青、左金），顺时针：top → right → bottom → left。
var relGlowFaces = [4]color.NRGBA{
	{R: 0x8B, G: 0x5C, B: 0xF6, A: 0xFF}, // 紫
	{R: 0x3B, G: 0x82, B: 0xF6, A: 0xFF}, // 蓝
	{R: 0x1F, G: 0xB5, B: 0xB0, A: 0xFF}, // 青
	{R: 0xD9, G: 0x9A, B: 0x2B, A: 0xFF}, // 金
}

// relGlowSweepBase 八段带子在扫亮序列里的起点（全局周长比例，0 = 顶边中点）。
// 顶边两段特别：右半最先扫（0 起），左半最后扫（0.875 起）——「从顶边中点顺时针」。
var relGlowSweepBase = [4][2]float64{
	{0.875, 0.0},  // 顶：左半 / 右半
	{0.125, 0.25}, // 右：上半 / 下半
	{0.375, 0.5},  // 底：右半 / 左半
	{0.625, 0.75}, // 左：下半 / 上半
}

// relGlowHalf 一段带子的扫亮起点跨度（周长比例）：八段等分。
const relGlowHalf = 0.125

// glowState 光带的动画帧态（仅历史窗 goroutine 读写）。
type glowState struct {
	on    bool     // 目标态（= 内核的模式状态；翻转时起跑）
	swept bool     // 扫亮已跑完（之后进呼吸段）
	sweep relTween // 扫亮进度 0..1
	level relTween // 整体亮度 0..relGlowLit..relGlowRest
}

// stepGlow 推进光带时间线（§5.6.2），返回本帧是否还要续帧。
//
// 进入：亮度直接到 0.85（可见范围由扫亮裁剪，不做整体淡入）、扫亮 260ms easeOutCubic；
// 扫完再 600ms 呼吸到 0.7 定住。退出：160ms easeInSine 整体淡出，扫亮进度不动（不反向扫）。
// 中途反向（进了又退）时从当前亮度接着淡——不跳变。
func (g *relGraph) stepGlow(now time.Time, on bool) bool {
	gl := &g.glow
	if on != gl.on {
		gl.on = on
		if on {
			gl.swept = false
			gl.sweep.v = 0
			gl.level.v = relGlowLit // 扫亮期间恒亮
			gl.sweep.retarget(now, 1, relGlowSweep, relEaseOutCubic)
		} else {
			gl.level.retarget(now, 0, relGlowFade, inSine)
			gl.sweep.cancel() // 退出不反向扫：扫亮就地停住，剩下的交给淡出
		}
	}
	if gl.on && !gl.swept && !gl.sweep.active() && gl.sweep.v >= 1 {
		gl.swept = true
		gl.level.retarget(now, relGlowRest, relGlowBreath, inSine)
	}
	gl.sweep.at(now)
	gl.level.at(now)
	return gl.sweep.active() || gl.level.active()
}

// drawGlow 光带单帧：四层 × 八段（见 file 头注的 Gio 约束）。
func (g *relGraph) drawGlow(gtx layout.Context) {
	level := g.glow.level.v
	if level <= 0.002 || g.viewW <= 0 || g.viewH <= 0 {
		return
	}
	light := !paperOf().dark
	w0 := float64(gtx.Dp(unit.Dp(relGlowBand)))
	// 外缘圆角（§5.6.2 的 10dp）：四段带子的转角自带 45° 斜切，外缘这一侧再靠圆角裁一次。
	defer clip.UniformRRect(
		image.Rect(0, 0, int(g.viewW), int(g.viewH)),
		int(gtx.Dp(unit.Dp(relGlowRound))),
	).Push(gtx.Ops).Pop()
	for _, L := range relGlowLayers {
		w := w0 * L.W / relGlowBand
		a := L.A * level
		if light {
			a *= relGlowLightGain
		}
		if a > 1 {
			a = 1
		}
		for side := 0; side < 4; side++ {
			for half := 0; half < 2; half++ {
				t0, t1, ok := glowHalfRange(side, half, g.glow.sweep.v)
				if !ok {
					continue
				}
				c0, c1 := glowHalfColors(side, half, a, light)
				g.glowBand(gtx, side, t0, t1, w, c0, c1)
			}
		}
	}
}

// glowHalfRange 某一段带子在扫亮进度 p 下的可见参数区间（t 沿该边顺时针、0..1 为整边）。
// p = 0 → 不可见；p = 1 → 整段。
func glowHalfRange(side, half int, p float64) (float64, float64, bool) {
	q := (p - relGlowSweepBase[side][half]) / relGlowHalf
	if q <= 0 {
		return 0, 0, false
	}
	if q > 1 {
		q = 1
	}
	t0 := float64(half) * 0.5
	return t0, t0 + 0.5*q, true
}

// glowHalfColors 一段带子的两端颜色：接缝端掺邻色、中点端纯本色（见 file 头注）。
// side 顺时针序为 顶 → 右 → 底 → 左；half 0 = 该边顺时针前半，1 = 后半。
func glowHalfColors(side, half int, a float64, light bool) (color.NRGBA, color.NRGBA) {
	own := relGlowFaces[side]
	prev := relGlowFaces[(side+3)%4]
	next := relGlowFaces[(side+1)%4]
	at := func(c color.NRGBA) color.NRGBA { return glowShade(c, a, light) }
	if half == 0 {
		return at(relMixColor(prev, own, relGlowSeamOwn)), at(own)
	}
	return at(own), at(relMixColor(own, next, relGlowSeamOwn))
}

// glowShade 上 alpha + 浅色主题降一档饱和（§5.6.2：白底上强化 alpha、降饱和，深色反之）。
// 降饱和只把颜色朝自身的灰度拉 18%——再多金色就读成土黄了。
func glowShade(c color.NRGBA, a float64, light bool) color.NRGBA {
	if light {
		gray := float64(c.R)*0.299 + float64(c.G)*0.587 + float64(c.B)*0.114
		lerp := func(v uint8) uint8 {
			return uint8(float64(v)*0.82 + gray*0.18)
		}
		c = color.NRGBA{R: lerp(c.R), G: lerp(c.G), B: lerp(c.B), A: c.A}
	}
	c.A = uint8(a*255 + 0.5)
	return c
}

// glowBand 一段带子：外沿 + 内沿围成的四边形（含两端的 45° 斜切），沿 t 方向两色渐变。
func (g *relGraph) glowBand(gtx layout.Context, side int, t0, t1, w float64,
	c0, c1 color.NRGBA) {
	q := glowBandQuad(side, float32(t0), float32(t1), float32(w), float32(g.viewW), float32(g.viewH))
	var p clip.Path
	p.Begin(gtx.Ops)
	p.MoveTo(q[0])
	p.LineTo(q[1])
	p.LineTo(q[2])
	p.LineTo(q[3])
	p.Close()
	defer clip.Outline{Path: p.End()}.Op().Push(gtx.Ops).Pop()
	paint.LinearGradientOp{Stop1: q[0], Color1: c0, Stop2: q[1], Color2: c1}.Add(gtx.Ops)
	paint.PaintOp{}.Add(gtx.Ops)
}

// glowBandQuad 一段带子的四个顶点（外沿 t0 → 外沿 t1 → 内沿 t1 → 内沿 t0）。
//
// 内沿比外沿短 2w，两端因此是斜角：相邻两段在斜角处严丝合缝（八段正好铺满整圈环带，
// 面积可逐项相加验证——见 relmap_glow_test 的铺满断言）。
func glowBandQuad(side int, t0, t1, w, W, H float32) [4]f32.Point {
	x0, y0 := w, w
	x1, y1 := W-w, H-w // 内沿矩形（外沿各缩 w）
	var oa, ob, ia, ib f32.Point
	switch side {
	case 0: // 顶：左 → 右
		oa, ob = f32.Pt(0, 0), f32.Pt(W, 0)
		ia, ib = f32.Pt(x0, y0), f32.Pt(x1, y0)
	case 1: // 右：上 → 下
		oa, ob = f32.Pt(W, 0), f32.Pt(W, H)
		ia, ib = f32.Pt(x1, y0), f32.Pt(x1, y1)
	case 2: // 底：右 → 左
		oa, ob = f32.Pt(W, H), f32.Pt(0, H)
		ia, ib = f32.Pt(x1, y1), f32.Pt(x0, y1)
	default: // 左：下 → 上
		oa, ob = f32.Pt(0, H), f32.Pt(0, 0)
		ia, ib = f32.Pt(x0, y1), f32.Pt(x0, y0)
	}
	lerp := func(a, b f32.Point, t float32) f32.Point {
		return f32.Pt(a.X+t*(b.X-a.X), a.Y+t*(b.Y-a.Y))
	}
	return [4]f32.Point{
		lerp(oa, ob, t0), lerp(oa, ob, t1), lerp(ia, ib, t1), lerp(ia, ib, t0),
	}
}
