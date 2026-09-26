package uigui

import (
	"image"
	"math"
	"testing"
)

// refFade 参考实现：与 fadePremultiplyBand 独立推导（math.Pow 精确公式、无 LUT），
// 用于交叉验证查表快路径。
func refFade(c byte, al, g float64) float64 {
	colorLin := srgbDecode(float64(c)/255) / al
	if colorLin > 1 {
		colorLin = 1
	}
	return srgbEncode(colorLin) * al * g
}

func smoothstep(t float64) float64 { return t * t * (3 - 2*t) }

// TestFadePremultiplyBand 淡出带转换（D44/§15.3）：预乘合法性（C ≤ A）、
// 与参考实现逐字节一致（±1）、渐变单调、透明输入无内容、缓冲过短拒绝。
func TestFadePremultiplyBand(t *testing.T) {
	const w, bandH = 4, 3
	src := image.NewRGBA(image.Rect(0, 0, w, bandH))
	// (0,0) 透明；(1,0)(2,0) 半透明红（模拟 spike 实测值188/0/0/128）；
	// (3,0)(*,1) 不透明纯蓝；(*,2) 不透明白。
	set := func(x, y int, r, g, b, a uint8) {
		i := src.PixOffset(x, y)
		src.Pix[i], src.Pix[i+1], src.Pix[i+2], src.Pix[i+3] = r, g, b, a
	}
	set(1, 0, 188, 0, 0, 128)
	set(2, 0, 188, 0, 0, 128)
	set(3, 0, 0, 0, 255, 255)
	for x := 0; x < w; x++ {
		set(x, 1, 0, 0, 255, 255)
		set(x, 2, 255, 255, 255, 255)
	}

	out := make([]byte, w*bandH*4)
	if !fadePremultiplyBand(src, w, bandH, out) {
		t.Fatal("带内有不透明内容却报告无内容")
	}

	at := func(x, y, ch int) byte { // ch: 0=B 1=G 2=R 3=A
		return out[(y*w+x)*4+ch]
	}
	// ① 预乘合法 + 透明像素归零。
	for y := 0; y < bandH; y++ {
		for x := 0; x < w; x++ {
			a := at(x, y, 3)
			for ch := 0; ch < 3; ch++ {
				if at(x, y, ch) > a {
					t.Fatalf("(%d,%d) ch%d = %d > A=%d：非预乘", x, y, ch, at(x, y, ch), a)
				}
			}
			_ = a
		}
	}
	if at(0, 0, 3) != 0 || at(0, 0, 2) != 0 {
		t.Fatalf("透明像素应全零: %v", out[0:4])
	}

	// ② 与参考实现交叉验证（±1 容差）。
	for y := 0; y < bandH; y++ {
		g := smoothstep((float64(y) + 0.5) / float64(bandH))
		for x := 0; x < w; x++ {
			i := src.PixOffset(x, y)
			al := float64(src.Pix[i+3]) / 255
			if al == 0 {
				continue
			}
			wantA := math.Round(al * g * 255)
			if diff := math.Abs(float64(at(x, y, 3)) - wantA); diff > 1 {
				t.Fatalf("(%d,%d) A = %d, want %.0f", x, y, at(x, y, 3), wantA)
			}
			for ch, outCh := range []int{2, 1, 0} { // R,G,B → BGRA 输出位
				want := math.Round(refFade(src.Pix[i+ch], al, g) * 255)
				if diff := math.Abs(float64(at(x, y, outCh)) - want); diff > 1 {
					t.Fatalf("(%d,%d) ch%d = %d, want %.0f", x, y, ch, at(x, y, outCh), want)
				}
			}
		}
	}

	// ③ 渐变单调：同列自带顶向带底 alpha 递增（透明像素除外）。
	for x := 0; x < w; x++ {
		prev := at(x, 0, 3)
		for y := 1; y < bandH; y++ {
			cur := at(x, y, 3)
			if cur < prev {
				t.Fatalf("列 %d 行 %d alpha 递减: %d < %d", x, y, cur, prev)
			}
			prev = cur
		}
	}

	// ④ 全透明带 → 无内容（overlay 应隐藏）。
	empty := image.NewRGBA(image.Rect(0, 0, w, bandH))
	if fadePremultiplyBand(empty, w, bandH, out) {
		t.Fatal("全透明带不应报告有内容")
	}

	// ⑤ 参数防御：缓冲过短/非法尺寸拒绝。
	if fadePremultiplyBand(src, w, bandH, out[:10]) {
		t.Fatal("短缓冲应拒绝")
	}
	if fadePremultiplyBand(src, w, 0, out) {
		t.Fatal("零高应拒绝")
	}
}

// TestSRoundtrip sRGB 编解码恒等（LUT 两端引用同一对公式）。
func TestSRoundtrip(t *testing.T) {
	for _, v := range []float64{0, 0.001, 0.04, 0.041, 0.2, 0.5, 0.8, 1} {
		if got := srgbEncode(srgbDecode(v)); math.Abs(got-v) > 1e-9 {
			t.Fatalf("roundtrip(%v) = %v", v, got)
		}
	}
	if got := encodeLUTAt(0.5); math.Abs(got-srgbEncode(0.5)) > 1.0/4096 {
		t.Fatalf("encodeLUTAt(0.5) = %v, want %v", got, srgbEncode(0.5))
	}
	if encodeLUTAt(-1) != 0 || encodeLUTAt(2) != 1 {
		t.Fatal("encodeLUTAt 越界钳制失败")
	}
}
