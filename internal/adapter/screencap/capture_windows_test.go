//go:build windows

package screencap

import (
	"bytes"
	"image/png"
	"testing"
)

// TestCopyOverlap 弹窗合成只覆盖相交矩形（越界裁剪），完全在外则不动主图。
func TestCopyOverlap(t *testing.T) {
	const dw, dh = 6, 5
	base := func() []byte {
		b := make([]byte, dw*dh*4)
		for i := range b {
			b[i] = 1
		}
		return b
	}
	// 2×2 弹窗，像素值 9。
	src := make([]byte, 2*2*4)
	for i := range src {
		src[i] = 9
	}
	pixAt := func(b []byte, x, y int) byte { return b[(y*dw+x)*4] }

	dst := base()
	copyOverlap(dst, dw, dh, src, 2, 2, 4, 3) // 右下角相交 2×2（行 3–4）
	if pixAt(dst, 4, 3) != 9 || pixAt(dst, 5, 3) != 9 || pixAt(dst, 4, 4) != 9 || pixAt(dst, 5, 4) != 9 {
		t.Fatalf("相交区未覆盖: %v", dst)
	}
	if pixAt(dst, 3, 3) != 1 {
		t.Fatalf("相交区外被改写: %v", dst)
	}

	dst = base()
	copyOverlap(dst, dw, dh, src, 2, 2, -1, -1) // 左上越界：只剩 (0,0) 一格
	if pixAt(dst, 0, 0) != 9 || pixAt(dst, 1, 0) != 1 || pixAt(dst, 0, 1) != 1 {
		t.Fatalf("左上裁剪错误: %v", dst)
	}

	dst = base()
	copyOverlap(dst, dw, dh, src, 2, 2, 100, 100) // 完全在外
	for i, v := range dst {
		if v != 1 {
			t.Fatalf("完全在外的弹窗改写了主图（偏移 %d）", i)
		}
	}
}

// TestBlankDetectsUnrenderedFrames 整张纯黑判失败；单色/有内容的图都是合法内容（D178）。
func TestBlankDetectsUnrenderedFrames(t *testing.T) {
	const w, h = 40, 30
	zero := make([]byte, w*h*4)
	if !blank(zero, w, h) {
		t.Fatal("全零像素应判空白")
	}

	solid := make([]byte, w*h*4)
	for i := 0; i < len(solid); i += 4 {
		solid[i], solid[i+1], solid[i+2], solid[i+3] = 250, 250, 250, 0xff
	}
	if blank(solid, w, h) {
		t.Fatal("纯白窗口是合法内容，不能按单色判空白")
	}

	grad := make([]byte, w*h*4)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			i := (y*w + x) * 4
			grad[i], grad[i+1], grad[i+2], grad[i+3] = byte(x*3), byte(y*5), 100, 0xff
		}
	}
	if blank(grad, w, h) {
		t.Fatal("有内容的像素不应判空白")
	}

	// 只有角落一个亮点也不算空白（整图扫描，不只看中心）。
	sparse := make([]byte, w*h*4)
	for i := 0; i < len(sparse); i += 4 {
		sparse[i+3] = 0xff
	}
	sparse[(h-1)*w*4+2] = 1
	if blank(sparse, w, h) {
		t.Fatal("角落有内容不应判空白")
	}
}

// TestEncodeProducesPNG 编码结果可解码、尺寸一致、alpha 不透明。
func TestEncodeProducesPNG(t *testing.T) {
	const w, h = 6, 4
	pix := make([]byte, w*h*4)
	for i := 0; i < len(pix); i += 4 {
		pix[i], pix[i+1], pix[i+2], pix[i+3] = 10, 20, 30, 0xff
	}
	img, err := encode(pix, w, h)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	if img.Width != w || img.Height != h || len(img.Data) == 0 {
		t.Fatalf("image = %dx%d, %d bytes", img.Width, img.Height, len(img.Data))
	}
	dec, err := png.Decode(bytes.NewReader(img.Data))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if b := dec.Bounds(); b.Dx() != w || b.Dy() != h {
		t.Fatalf("bounds = %v", b)
	}
	r, g, bl, a := dec.At(1, 1).RGBA()
	if r>>8 != 10 || g>>8 != 20 || bl>>8 != 30 || a>>8 != 0xff {
		t.Fatalf("pixel = %d,%d,%d,%d", r>>8, g>>8, bl>>8, a>>8)
	}
}
