package uigui

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"testing"

	"github.com/Tonyjh07/Aquarius/assets"
)

// TestLogoAssetDecode 内嵌品牌图标可解码（§15.2 logo 实装的前置——解码失败时
// 圆钮退回纯色并记日志）。
func TestLogoAssetDecode(t *testing.T) {
	if len(assets.LogoPNG) == 0 {
		t.Fatal("assets.LogoPNG 未内嵌")
	}
	img, err := png.Decode(bytes.NewReader(assets.LogoPNG))
	if err != nil {
		t.Fatalf("解码: %v", err)
	}
	if b := img.Bounds(); b.Dx() < 32 || b.Dy() < 32 {
		t.Fatalf("图标过小: %v", b)
	}
}

// TestLogoImageSizedToBall D83 回归：图标按**球的实际像素边长**预缩放，不用固定 60px
// —— 100% 缩放下球径 48px，固定 60px 的图标居中后被 48px 的圆裁掉外圈（用户实测：
// logo 边缘被截、看着像被放大裁切）。换 DPI/尺寸档时缓存必须重建。
func TestLogoImageSizedToBall(t *testing.T) {
	// 100% 缩放：球径 48px。
	if _, ok := logoImageOp(48); !ok {
		t.Fatal("图标应可用（assets.LogoPNG 已内嵌）")
	}
	if logoPx != 48 {
		t.Fatalf("100%% 缩放下图标边长 = %d, want 48（球径）", logoPx)
	}
	// 125% 缩放：球径 60px——换档必须重建，不能沿用上一档。
	if _, ok := logoImageOp(60); !ok {
		t.Fatal("125% 档应可用")
	}
	if logoPx != 60 {
		t.Fatalf("125%% 缩放下图标边长 = %d, want 60", logoPx)
	}
	// 非法边长（零/负）不返图，也不污染缓存。
	if _, ok := logoImageOp(0); ok {
		t.Fatal("零边长不应返回图")
	}
	if logoPx != 60 {
		t.Fatalf("零边长不应改变缓存: logoPx = %d, want 60", logoPx)
	}
}

// TestScaleBox 面积平均缩放（logo 预缩放路径）：尺寸正确、纯色保持。
func TestScaleBox(t *testing.T) {
	// 纯色不透明 → 缩放后仍纯色。
	src := image.NewNRGBA(image.Rect(0, 0, 128, 128))
	for y := 0; y < 128; y++ {
		for x := 0; x < 128; x++ {
			src.SetNRGBA(x, y, color.NRGBA{R: 10, G: 200, B: 30, A: 255})
		}
	}
	dst := scaleBox(src, image.Pt(logoIconPx, logoIconPx))
	if b := dst.Bounds(); b.Dx() != logoIconPx || b.Dy() != logoIconPx {
		t.Fatalf("尺寸 = %v, want %d", b, logoIconPx)
	}
	if c := dst.RGBAAt(10, 10); c.R != 10 || c.G != 200 || c.B != 30 || c.A != 255 {
		t.Fatalf("纯色保持失败: %+v", c)
	}

	// 半透明区平均后仍带 alpha（预乘值经 16 位平均回写）。
	half := image.NewNRGBA(image.Rect(0, 0, 8, 8))
	for y := 0; y < 8; y++ {
		for x := 0; x < 8; x++ {
			half.SetNRGBA(x, y, color.NRGBA{R: 255, G: 255, B: 255, A: 128})
		}
	}
	got := scaleBox(half, image.Pt(4, 4))
	if c := got.RGBAAt(2, 2); c.A == 0 {
		t.Fatalf("半透明缩放后 alpha 丢失: %+v", c)
	}
}
