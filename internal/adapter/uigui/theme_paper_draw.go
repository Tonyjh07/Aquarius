package uigui

// theme_paper_draw.go 次窗 Paper 的绘制原语：描边面板、柔阴影、表面。
//
// Gio 没有描边也没有模糊，这两件事都得靠自己叠：
//   - 描边 = 先铺描边色的圆角矩形，再内缩 bd 铺面色（直接给透明底描边会把整块涂成描边色）；
//   - 阴影 = 同心外扩逐层叠加，且**必须从小到大画**（见 paperShadow）。
//
// 这两个函数只服务次窗。主窗是形裁窗 + 亚帧透明度，阴影与 hairline 在它那儿不成立。

import (
	"image"
	"image/color"

	"gioui.org/layout"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"
)

// paperPanel 描边面板：r = 面色区，bd = 描边宽（px；0 = 无描边）。
// 描边画在 r 的**外侧**（r.Inset(-bd)），所以 r 保持为调用方算出的版面矩形——
// 内容按 r 排，描边不会挤占内边距。
func paperPanel(gtx layout.Context, r image.Rectangle, rad, bd int, fill, border color.NRGBA) {
	if r.Empty() {
		return
	}
	if border.A > 0 && bd > 0 {
		paint.FillShape(gtx.Ops, border, clip.UniformRRect(r.Inset(-bd), rad+bd).Op(gtx.Ops))
	}
	if fill.A > 0 {
		paint.FillShape(gtx.Ops, fill, clip.UniformRRect(r, rad).Op(gtx.Ops))
	}
}

// paperShadow 柔阴影近似（Gio 无模糊：只能同心外扩叠加）。
//
// 每层**等 alpha**，靠层数累积出衰减——最外圈只剩一层，所以最淡。若让外层 alpha
// 更大（直觉写法），最外几 dp 会变成一道实心灰边，观感像给卡片镶了宽灰框。
//
// spread 是扩散半径（dp）：卡片取 3（层次主要交给 1px 描边与面阶），浮层才要 10+。
// 调用方必须**先画阴影再画面**：本函数只铺阴影，不画面。
func paperShadow(gtx layout.Context, r image.Rectangle, rad, spread, dy int, peak uint8) {
	if r.Empty() || spread <= 0 {
		return
	}
	a := uint8(int(peak) / spread)
	if a == 0 {
		a = 1
	}
	// **必须从小到大画**：每层都是实心圆角矩形，大的会盖住小的。从大到小的话每层
	// 都被后续更大的层重复铺满，外层 alpha 叠加到满 → 等宽实心灰边，而非衰减。
	for i := 1; i <= spread; i++ {
		d := gtx.Dp(unit.Dp(i))
		rr := r.Inset(-d).Add(image.Pt(0, gtx.Dp(unit.Dp(dy))))
		paint.FillShape(gtx.Ops, color.NRGBA{A: a},
			clip.UniformRRect(rr, rad+d).Op(gtx.Ops))
	}
}

// paperCard 卡片：阴影（可选）+ 1px hairline + 面色。次窗的分层靠这三件套，
// 不靠大色块——大面积浅灰底在 16:9 窗体上会读成「页面没加载完」。
func paperCard(gtx layout.Context, pp paperPalette, r image.Rectangle, rad int, fill color.NRGBA) {
	if pp.shadow {
		paperShadow(gtx, r, rad, 3, 1, 0x1C)
	}
	paperPanel(gtx, r, rad, gtx.Dp(1), fill, pp.hairline)
}

// paperSurface 次级面（输入框、只读块、状态栏、模块磁贴）：比卡片下沉一档，
// 边界同样是 hairline。sunken 面上不要再叠阴影——那是「凹进去」，不是「浮起来」。
func paperSurface(gtx layout.Context, pp paperPalette, r image.Rectangle, rad int) {
	paperPanel(gtx, r, rad, gtx.Dp(1), pp.sunken, pp.hairline)
}
