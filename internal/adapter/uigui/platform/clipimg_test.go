package platform

// 剪贴板位图解码测试（D187 口径二）：Windows 剪贴板里三种常见形态的假内存块 →
// PNG 往返。纯字节构造，不碰 Win32，可在任意平台跑。

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	"image/png"
	"testing"
)

// dibHeader 造一个 BITMAPINFOHEADER（40 字节）+ 可选掩码，返回头部字节。
func dibHeader(w, h int, bitCount int, compression uint32, masks ...uint32) []byte {
	b := make([]byte, 40)
	le := binary.LittleEndian
	le.PutUint32(b[0:], 40)
	le.PutUint32(b[4:], uint32(int32(w)))
	le.PutUint32(b[8:], uint32(int32(h)))
	le.PutUint16(b[12:], 1) // biPlanes
	le.PutUint16(b[14:], uint16(bitCount))
	le.PutUint32(b[16:], compression)
	for i, m := range masks {
		mb := make([]byte, 4)
		le.PutUint32(mb, m)
		b = append(b, mb...)
		_ = i
	}
	return b
}

// pad4 行尾补齐到 4 字节对齐。
func pad4(b []byte) []byte {
	for len(b)%4 != 0 {
		b = append(b, 0)
	}
	return b
}

// decodePNG 把转换结果解码回来（断言像素）。
func decodePNG(t *testing.T, b []byte) image.Image {
	t.Helper()
	img, err := png.Decode(bytes.NewReader(b))
	if err != nil {
		t.Fatalf("PNG 解码失败: %v", err)
	}
	return img
}

// rgbAt 取像素的 8 位 RGBA（预乘转直通）。
func rgbAt(img image.Image, x, y int) (byte, byte, byte, byte) {
	c := img.At(x, y)
	n := color.NRGBAModel.Convert(c).(color.NRGBA)
	return n.R, n.G, n.B, n.A
}

// TestClipDIB24BottomUp CF_DIB 24bpp、bottom-up（Windows 缺省）：像素顺序翻转要正确。
func TestClipDIB24BottomUp(t *testing.T) {
	// 3x2：第 0 行（顶）红绿蓝，第 1 行（底）白黑灰。
	top := []byte{0x00, 0x00, 0xFF, 0x00, 0xFF, 0x00, 0xFF, 0x00, 0x00} // BGR
	bottom := []byte{0xFF, 0xFF, 0xFF, 0x00, 0x00, 0x00, 0x80, 0x80, 0x80}
	dib := append(dibHeader(3, 2, 24, dibRGB), pad4(bottom)...)
	dib = append(dib, pad4(top)...)

	out, err := clipDIBToPNG(dib)
	if err != nil {
		t.Fatalf("转换失败: %v", err)
	}
	img := decodePNG(t, out)
	if img.Bounds().Size() != image.Pt(3, 2) {
		t.Fatalf("尺寸 = %v", img.Bounds().Size())
	}
	if r, g, b, a := rgbAt(img, 0, 0); r != 0xFF || g != 0 || b != 0 || a != 0xFF {
		t.Fatalf("左上 = %d,%d,%d,%d，want 红", r, g, b, a)
	}
	if r, g, b, _ := rgbAt(img, 2, 0); r != 0 || g != 0 || b != 0xFF {
		t.Fatalf("右上 = %d,%d,%d，want 蓝", r, g, b)
	}
	if r, g, b, _ := rgbAt(img, 0, 1); r != 0xFF || g != 0xFF || b != 0xFF {
		t.Fatalf("左下 = %d,%d,%d，want 白", r, g, b)
	}
	if r, _, _, _ := rgbAt(img, 1, 1); r != 0 {
		t.Fatalf("中下应为黑，got R=%d", r)
	}
}

// TestClipDIB32BitfieldsAlpha CF_DIBV5 32bpp：BI_BITFIELDS + alpha 掩码，透明度要保留。
func TestClipDIB32BitfieldsAlpha(t *testing.T) {
	// BITMAPV5HEADER = 124 字节；掩码在头部 40/44/48/52 偏移。
	hdr := make([]byte, 124)
	le := binary.LittleEndian
	le.PutUint32(hdr[0:], 124)
	le.PutUint32(hdr[4:], 2)
	le.PutUint32(hdr[8:], 1)
	le.PutUint16(hdr[12:], 1)
	le.PutUint16(hdr[14:], 32)
	le.PutUint32(hdr[16:], dibBitfields)
	le.PutUint32(hdr[40:], 0x00FF0000)
	le.PutUint32(hdr[44:], 0x0000FF00)
	le.PutUint32(hdr[48:], 0x000000FF)
	le.PutUint32(hdr[52:], 0xFF000000)

	// BGRA：半透明红、全透明绿。
	dib := append(hdr, []byte{0x00, 0x00, 0xFF, 0x80, 0x00, 0xFF, 0x00, 0x00}...)
	out, err := clipDIBToPNG(dib)
	if err != nil {
		t.Fatalf("转换失败: %v", err)
	}
	img := decodePNG(t, out)
	if r, g, b, a := rgbAt(img, 0, 0); r != 0xFF || g != 0 || b != 0 || a != 0x80 {
		t.Fatalf("半透明红 = %d,%d,%d,%d", r, g, b, a)
	}
	if _, g, _, a := rgbAt(img, 1, 0); g != 0xFF || a != 0 {
		t.Fatalf("全透明绿 = g:%d a:%d", g, a)
	}
}

// TestClipDIB32RGBOpaque CF_DIB 32bpp 无掩码：第 4 字节按 GDI 约定不是 alpha，
// 必须当不透明（否则截图工具贴进来的图整张透明）。
func TestClipDIB32RGBOpaque(t *testing.T) {
	dib := append(dibHeader(1, 1, 32, dibRGB), 0x10, 0x20, 0x30, 0x00)
	out, err := clipDIBToPNG(dib)
	if err != nil {
		t.Fatalf("转换失败: %v", err)
	}
	img := decodePNG(t, out)
	if r, g, b, a := rgbAt(img, 0, 0); r != 0x30 || g != 0x20 || b != 0x10 || a != 0xFF {
		t.Fatalf("像素 = %d,%d,%d,%d，want 不透明", r, g, b, a)
	}
}

// TestClipPNGPassthrough 注册格式 "PNG"：合法 PNG 原样返回（不重编码）。
func TestClipPNGPassthrough(t *testing.T) {
	var buf bytes.Buffer
	src := image.NewNRGBA(image.Rect(0, 0, 2, 2))
	src.SetNRGBA(1, 1, color.NRGBA{R: 0x12, G: 0x34, B: 0x56, A: 0xFF})
	if err := png.Encode(&buf, src); err != nil {
		t.Fatal(err)
	}
	raw := buf.Bytes()
	out, err := clipPNGToPNG(raw)
	if err != nil {
		t.Fatalf("校验失败: %v", err)
	}
	if !bytes.Equal(out, raw) {
		t.Fatal("合法 PNG 应原样返回")
	}
	if _, err := clipPNGToPNG([]byte("not a png")); err == nil {
		t.Fatal("非 PNG 应报错")
	}
}

// TestClipDIBRejects 异常输入必须报错而不是产出黑图或 panic。
func TestClipDIBRejects(t *testing.T) {
	cases := map[string][]byte{
		"空":       nil,
		"头不足":     make([]byte, 20),
		"尺寸为零":    dibHeader(0, 1, 24, dibRGB),
		"位深不支持":   dibHeader(1, 1, 7, dibRGB),
		"压缩不支持":   dibHeader(1, 1, 24, 9),
		"像素数据不完整": append(dibHeader(10, 10, 24, dibRGB), 0, 0, 0),
	}
	for name, b := range cases {
		if _, err := clipDIBToPNG(b); err == nil {
			t.Fatalf("%s：应报错", name)
		}
	}
}

// TestClipDIBPalette 8bpp 调色板：索引位图要按调色板取色（不是当灰度）。
func TestClipDIBPalette(t *testing.T) {
	hdr := dibHeader(2, 1, 8, dibRGB)
	// 调色板 256 项（clrUsed = 0 → 满表）：第 1 项绿、第 2 项蓝。
	pal := make([]byte, 256*4)
	pal[4], pal[5], pal[6] = 0x00, 0xFF, 0x00
	pal[8], pal[9], pal[10] = 0xFF, 0x00, 0x00
	dib := append(hdr, pal...)
	dib = append(dib, 1, 2, 0, 0)

	out, err := clipDIBToPNG(dib)
	if err != nil {
		t.Fatalf("转换失败: %v", err)
	}
	img := decodePNG(t, out)
	if r, g, b, _ := rgbAt(img, 0, 0); r != 0 || g != 0xFF || b != 0 {
		t.Fatalf("索引 1 = %d,%d,%d，want 绿", r, g, b)
	}
	if r, g, b, _ := rgbAt(img, 1, 0); r != 0 || g != 0 || b != 0xFF {
		t.Fatalf("索引 2 = %d,%d,%d，want 蓝", r, g, b)
	}
}
