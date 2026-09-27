package uigui

import (
	"bytes"
	"fmt"
	"image"
	"image/png"
	"math"
	"sync"

	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"

	"github.com/Tonyjh07/Aquarius/assets"
)

// logoIconPx 图标预缩放尺寸（名义 125% 缩放的球径 = 48dp × 1.25，D49 行高 48）：解码时
// 一次缩好，绘制只做纯平移——不用每帧仿射缩放（实证渲染不稳），ImageOp 单实例缓存纹理
// 键稳定。更高 DPI 下图标略小于球（露出品牌蓝边，无感知）；更低 DPI 裁圆裁到图标自带蓝底。
const logoIconPx = 60

// logoState 解码 + 预缩放一次（帧循环懒调；失败留 nil 并记日志，圆钮退回纯色）。
var (
	logoOnce sync.Once
	logoOp   paint.ImageOp
	logoOK   bool
)

func logoImageOp() (paint.ImageOp, bool) {
	logoOnce.Do(func() {
		if len(assets.LogoPNG) == 0 {
			fmt.Println("[logo] 图标未内嵌（assets.LogoPNG 为空）")
			return
		}
		img, err := png.Decode(bytes.NewReader(assets.LogoPNG))
		if err != nil {
			fmt.Printf("[logo] 图标解码失败: %v\n", err)
			return
		}
		sz := img.Bounds().Size()
		scaled := scaleBox(img, image.Pt(logoIconPx, logoIconPx))
		logoOp = paint.NewImageOp(scaled) // *image.RGBA 分支：句柄稳定可缓存
		logoOK = true
		fmt.Printf("[logo] 图标就绪 %dx%d → %dx%d\n", sz.X, sz.Y, logoIconPx, logoIconPx)
	})
	return logoOp, logoOK
}

// scaleBox 面积平均缩放（纯 Go）：源按 16 位预乘值平均后直存（image.RGBA 即预乘语义）。
func scaleBox(src image.Image, dstSz image.Point) *image.RGBA {
	sb := src.Bounds()
	dst := image.NewRGBA(image.Rectangle{Max: dstSz})
	if sb.Empty() || dstSz.X <= 0 || dstSz.Y <= 0 {
		return dst
	}
	sx := float64(sb.Dx()) / float64(dstSz.X)
	sy := float64(sb.Dy()) / float64(dstSz.Y)
	for y := 0; y < dstSz.Y; y++ {
		y0, y1 := float64(y)*sy, float64(y+1)*sy
		for x := 0; x < dstSz.X; x++ {
			x0, x1 := float64(x)*sx, float64(x+1)*sx
			var r, g, b, a, n float64
			for iy := int(y0); iy < int(math.Ceil(y1)); iy++ {
				for ix := int(x0); ix < int(math.Ceil(x1)); ix++ {
					pr, pg, pb, pa := src.At(sb.Min.X+ix, sb.Min.Y+iy).RGBA() // 16 位预乘
					r += float64(pr)
					g += float64(pg)
					b += float64(pb)
					a += float64(pa)
					n++
				}
			}
			if n == 0 {
				continue
			}
			i := dst.PixOffset(x, y)
			dst.Pix[i+0] = byte((r / n) / 257)
			dst.Pix[i+1] = byte((g / n) / 257)
			dst.Pix[i+2] = byte((b / n) / 257)
			dst.Pix[i+3] = byte((a / n) / 257)
		}
	}
	return dst
}

// drawLogo 品牌色圆钮 + 内嵌品牌图标（§15.1 单组件悬浮球、§15.2 logo 实装）。
// 图标先裁进圆（带不透明角也保圆轮廓），平移居中放置（预缩放版，无缩放变换）。
func drawLogo(gtx layout.Context, box image.Rectangle) {
	if box.Dx() <= 0 || box.Dy() <= 0 {
		return
	}
	st := clip.UniformRRect(box, box.Dx()/2).Push(gtx.Ops)
	paint.Fill(gtx.Ops, brandColor) // 图标透明处露出品牌蓝底
	if im, ok := logoImageOp(); ok {
		x := box.Min.X + (box.Dx()-logoIconPx)/2
		y := box.Min.Y + (box.Dy()-logoIconPx)/2
		ts := op.Offset(image.Pt(x, y)).Push(gtx.Ops)
		// ImageOp.Add 只设材质不落绘制——须紧跟 PaintOp（paint.Fill = ColorOp +
		// PaintOp 的实证口径）：缺则图标永不显形，只剩品牌蓝底。
		im.Add(gtx.Ops)
		paint.PaintOp{}.Add(gtx.Ops)
		ts.Pop()
	}
	st.Pop()
}
