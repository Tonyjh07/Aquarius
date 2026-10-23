package uigui

// 暂存附件的缩略图（D187 口径四）：图片类附件在 chip 里显示一张小图代替回形针，
// 让「贴的是哪张图」一眼可辨。
//
// 两条纪律：
//   - **解码前先看尺寸**（`image.DecodeConfig`）：像素数超预算就不解，chip 回落回形针。
//     一张 8000×8000 的 PNG 解码要几百毫秒，放在 UI 线程上就是肉眼可见的卡帧。
//   - **解码不占 UI 线程**：路径附件在暂存时起后台 goroutine 读文件，粘图路径本来就在
//     后台 goroutine 里——两边都把结果投回事件循环，再挂到附件项上。

import (
	"bytes"
	"image"
	"image/draw"
	"io"

	"gioui.org/f32"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
)

// 缩略图口径。
const (
	attachThumbDp = 20 // chip 内缩略图边长（与回形针槽同高）
	thumbPx       = 40 // 预缩放边长（2x：高 DPI 下仍是下采样，边缘干净）
	// thumbMaxPixels 解码预算：超过就只认尺寸不认像素。24M ≈ 一张 6000×4000 的手机
	// 原图；再大基本不是「往对话里贴的图」，不值得为它冒卡帧风险。
	thumbMaxPixels = 24 << 20
	// thumbReadBytes 读缩略图的字节上限：单图上传上限的若干倍即可（超限的图在入口
	// 就被拒了），这里只是防御性夹取，避免误读超大文件。
	thumbReadBytes = 64 << 20
)

// thumbSrc 一张已预缩放好的缩略图。纹理由首次绘制时懒建（`NewImageOp` 假设底层
// 不可变，每帧新建会让 Gio 反复上传同一张图）。
type thumbSrc struct {
	img *image.RGBA
	op  paint.ImageOp
	ok  bool
}

// imageOp 取（必要时建）绘制用的纹理。
func (t *thumbSrc) imageOp() (paint.ImageOp, bool) {
	if t == nil || t.img == nil {
		return paint.ImageOp{}, false
	}
	if !t.ok {
		t.op = paint.NewImageOp(t.img)
		t.ok = true
	}
	return t.op, true
}

// loadThumb 从读取器解码缩略图。尺寸超预算、格式不认识、读失败都返回 nil——调用方
// 回落回形针，不报错：图标画不出来不该打断附件的正常使用。
func loadThumb(r io.Reader) *thumbSrc { return loadThumbLimit(r, thumbMaxPixels) }

// loadThumbLimit 同上，解码预算参数化（单测用小预算验证门槛，不必真造一张巨图）。
func loadThumbLimit(r io.Reader, maxPixels int) *thumbSrc {
	return loadThumbSide(r, thumbPx, maxPixels)
}

// loadThumbSide 解码成边长 side 的方图（转写区缩略图比 chip 图标大一档，共用同一
// 预算门与方图口径：方图意味着绘制方只需要按格宽等比铺，不必在两处各写一遍比例）。
func loadThumbSide(r io.Reader, side, maxPixels int) *thumbSrc {
	// 只读一遍字节：DecodeConfig 与 Decode 都从头开始，读成字节再各包一个 Reader，
	// 避免对同一文件句柄做两次 seek。
	data, err := io.ReadAll(io.LimitReader(r, thumbReadBytes))
	if err != nil || len(data) == 0 {
		return nil
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || cfg.Width <= 0 || cfg.Height <= 0 {
		return nil
	}
	if maxPixels > 0 && cfg.Width*cfg.Height > maxPixels {
		return nil // 超出解码预算：只认尺寸不认像素（防 UI 线程卡帧）
	}
	src, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil
	}
	img := scaleToSquare(src, side)
	if img == nil {
		return nil
	}
	return &thumbSrc{img: img}
}

// scaleToSquare 等比缩到最长边 = side 的方图，居中留透明边（不拉伸变形）。
func scaleToSquare(src image.Image, side int) *image.RGBA {
	b := src.Bounds()
	if b.Empty() || side <= 0 {
		return nil
	}
	w, h := b.Dx(), b.Dy()
	scale := float64(side) / float64(max(w, h))
	tw, th := max(1, int(float64(w)*scale+0.5)), max(1, int(float64(h)*scale+0.5))
	small := scaleBox(src, image.Pt(tw, th))
	dst := image.NewRGBA(image.Rect(0, 0, side, side))
	ox, oy := (side-tw)/2, (side-th)/2
	draw.Draw(dst, image.Rect(ox, oy, ox+tw, oy+th), small, image.Point{}, draw.Over)
	return dst
}

// drawThumb 在 box 内画缩略图（圆角裁剪；源已是方图，直接铺满）。
func drawThumb(gtx layout.Context, box image.Rectangle, src paint.ImageOp) {
	sz := src.Size()
	if box.Dx() <= 0 || box.Dy() <= 0 || sz.X <= 0 || sz.Y <= 0 {
		return
	}
	defer clip.UniformRRect(box, min(box.Dx(), box.Dy())/4).Push(gtx.Ops).Pop()
	// 先平移到 box 左上，再按边比缩放（源为方图，X/Y 比一致）。
	defer op.Offset(box.Min).Push(gtx.Ops).Pop()
	s := float32(box.Dx()) / float32(sz.X)
	defer op.Affine(f32.Affine2D{}.Scale(f32.Point{}, f32.Pt(s, s))).Push(gtx.Ops).Pop()
	src.Add(gtx.Ops)
	paint.PaintOp{}.Add(gtx.Ops)
}
