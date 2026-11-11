package uigui

// 浮层底板（D282）：提示卡、补全候选、档位列表三处共用一张底板——跟主题走。
//
// 主窗没有阴影可用（整窗是一张 ULW 位图，阴影要自己画在窗口内，浮层又不该占别人的
// 地盘），所以分层靠面阶与 1dp 描边：浅色主题白面 + 灰边，深色主题亮一阶的深面 +
// 更亮一档的边。令牌见 theme.go 的 pop*。

import (
	"image"

	"gioui.org/layout"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
)

// popRadiusDp 浮层圆角（与其他浮起面同一档）。
const popRadiusDp = 10

// popCard 画一层浮层底板并登记到 ULW 形状表。
// r = 当前坐标系里的矩形（输入栏内是局部坐标，转写区内是窗口坐标），win = 窗口坐标，
// 登记形状要用那一份。
func (u *UI) popCard(gtx layout.Context, r image.Rectangle, radius int, win image.Rectangle) {
	rr := clip.RRect{Rect: r, NE: radius, NW: radius, SE: radius, SW: radius}
	st := rr.Push(gtx.Ops)
	paint.Fill(gtx.Ops, tok().popBg)
	st.Pop()
	paint.FillShape(gtx.Ops, tok().popEdge,
		clip.Stroke{Path: rr.Path(gtx.Ops), Width: float32(gtx.Dp(1))}.Op())
	u.record(win, radius, tok().popBg, image.Rectangle{Max: u.frameSize})
}
