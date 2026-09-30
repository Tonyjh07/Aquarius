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

// logoIconPx 图标预缩放的名义尺寸（= 球径 48dp @ 125% 缩放 = 60px，D49 行高 48）。
// 仅作日志/测试参考：实际绘制按**球的实际像素边长**取预缩放图（见 logoImageOp），
// 否则固定 60px 会在 100% 缩放下被 48px 的球裁掉外圈——图标自带品牌蓝底，裁掉的是
// 图标本体（用户实测：100% 缩放下 logo 边缘被截掉、看起来被放大裁切）。
const logoIconPx = 60

// logoState 解码一次 + 按目标像素边长预缩放（缓存最近一档；帧循环懒调，DPI/尺寸变化
// 才重建）。失败留 nil 并记日志，圆钮退回纯色。
var (
	logoOnce sync.Once
	logoErr  bool          // 源图解码失败（只报一次）
	logoSrc  image.Image   // 解码后的源图（128×128）
	logoPx   int           // 当前缓存对应的球径（px）
	logoOp   paint.ImageOp // 该球径的预缩放图
	logoOK   bool
)

// logoImageOp 取目标球径 pxPx 的预缩放图标（缺失/失败 → ok=false）。绘制只做纯平移——
// 不用每帧仿射缩放（实证渲染不稳），ImageOp 单实例缓存纹理键稳定。
func logoImageOp(pxPx int) (paint.ImageOp, bool) {
	if pxPx <= 0 {
		return paint.ImageOp{}, false
	}
	logoOnce.Do(func() {
		if len(assets.LogoPNG) == 0 {
			fmt.Println("[logo] 图标未内嵌（assets.LogoPNG 为空）")
			logoErr = true
			return
		}
		img, err := png.Decode(bytes.NewReader(assets.LogoPNG))
		if err != nil {
			fmt.Printf("[logo] 图标解码失败: %v\n", err)
			logoErr = true
			return
		}
		logoSrc = img
		sz := img.Bounds().Size()
		fmt.Printf("[logo] 图标就绪 %dx%d（按球径预缩放）\n", sz.X, sz.Y)
	})
	if logoErr || logoSrc == nil {
		return paint.ImageOp{}, false
	}
	if logoOK && logoPx == pxPx {
		return logoOp, true
	}
	logoOp = paint.NewImageOp(scaleBox(logoSrc, image.Pt(pxPx, pxPx)))
	logoPx, logoOK = pxPx, true
	return logoOp, true
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
// 图标先裁进圆（带不透明角也保圆轮廓），按**球的实际像素边长**预缩放后平移居中
// （D83：固定 60px 只在 125% 缩放恰好合球，100% 下会被 48px 的圆裁掉外圈）。
func drawLogo(gtx layout.Context, box image.Rectangle) {
	if box.Dx() <= 0 || box.Dy() <= 0 {
		return
	}
	st := clip.UniformRRect(box, box.Dx()/2).Push(gtx.Ops)
	paint.Fill(gtx.Ops, brandColor) // 图标透明处露出品牌蓝底
	// 球为正方形时图标按边长取；非方（理论上不会有）取短边，避免越界。
	side := box.Dx()
	if box.Dy() < side {
		side = box.Dy()
	}
	if im, ok := logoImageOp(side); ok {
		x := box.Min.X + (box.Dx()-side)/2
		y := box.Min.Y + (box.Dy()-side)/2
		ts := op.Offset(image.Pt(x, y)).Push(gtx.Ops)
		// ImageOp.Add 只设材质不落绘制——须紧跟 PaintOp（paint.Fill = ColorOp +
		// PaintOp 的实证口径）：缺则图标永不显形，只剩品牌蓝底。
		im.Add(gtx.Ops)
		paint.PaintOp{}.Add(gtx.Ops)
		ts.Pop()
	}
	st.Pop()
}
