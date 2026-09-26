package uigui

import (
	"bytes"
	"image"
	"image/png"
	"sync"

	"gioui.org/f32"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"

	"github.com/Tonyjh07/Aquarius/assets"
)

// logoPng 品牌图标（assets 内嵌 PNG，透明/自有底），解码一次（懒、只在帧循环首绘）。
var (
	logoOnce sync.Once
	logoPng  image.Image
)

func logoImage() image.Image {
	logoOnce.Do(func() {
		if img, err := png.Decode(bytes.NewReader(assets.LogoPNG)); err == nil {
			logoPng = img
		}
	})
	return logoPng
}

// drawLogo 品牌色圆钮 + 内嵌品牌图标（§15.1 单组件悬浮球、§15.2 logo 实装）。
// box = 圆钮在当前坐标系的矩形。图标先裁进圆（图标若带不透明角也保持圆轮廓），
// 缩放经仿射变换（f32.AffineId().Scale().Offset() = 先缩放后平移到 box）。
func drawLogo(gtx layout.Context, box image.Rectangle) {
	if box.Dx() <= 0 || box.Dy() <= 0 {
		return
	}
	st := clip.UniformRRect(box, box.Dx()/2).Push(gtx.Ops)
	paint.Fill(gtx.Ops, brandColor) // 图标透明处露出品牌蓝底
	if im := logoImage(); im != nil {
		b := im.Bounds()
		sx := float32(box.Dx()) / float32(b.Dx())
		sy := float32(box.Dy()) / float32(b.Dy())
		aff := f32.AffineId().Scale(f32.Point{}, f32.Pt(sx, sy)).
			Offset(f32.Pt(float32(box.Min.X), float32(box.Min.Y)))
		ts := op.Affine(aff).Push(gtx.Ops)
		paint.NewImageOp(im).Add(gtx.Ops)
		ts.Pop()
	}
	st.Pop()
}
