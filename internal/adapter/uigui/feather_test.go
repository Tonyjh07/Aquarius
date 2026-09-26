package uigui

import (
	"image"
	"math"
	"testing"
)

// TestRrectSD 圆角矩形 SDF（D45 羽化环的几何基础）：中心到近边、边界 0、外正；
// 角落按圆弧而非方角（round 与 square 的判别点）。
func TestRrectSD(t *testing.T) {
	// 矩形 100×60、半径 10，中心 (50,30)。
	const cx, cy, hw, hh, rad = 50.0, 30.0, 50.0, 30.0, 10.0
	near := func(want, got float32, name string) {
		t.Helper()
		if d := got - want; d > 0.01 || d < -0.01 {
			t.Errorf("%s = %.3f, want %.3f", name, got, want)
		}
	}
	near(-30, rrectSD(cx, cy, cx, cy, hw, hh, rad), "中心（到近边距离）") // 上下边 30
	near(0, rrectSD(cx, 0, cx, cy, hw, hh, rad), "上边中点")         // 边界
	near(2, rrectSD(cx, -2, cx, cy, hw, hh, rad), "上外 2px")
	near(2, rrectSD(-2, cy, cx, cy, hw, hh, rad), "左外 2px")
	near(0, rrectSD(cx+hw, cy, cx, cy, hw, hh, rad), "右边中点")
	// 角外对角点（正方形角上 = 0，圆角 = 到角圆心距离 - 半径）——判别圆角生效。
	got := rrectSD(cx+hw, cy-hh, cx, cy, hw, hh, rad) // 世界点 (100,0) = 方角顶点
	want := float32(math.Sqrt(200) - 10)
	near(want, got, "右上角对角外点（圆弧）")
}

// TestFadeFeatherShapes 羽化环（D45/§15.1）：真轮廓内 (in+1)px alpha=1、向外 outPx
// smoothstep 衰减；带底以上不落笔（带内由带单绘——避免与带重复叠加出横缝）；只落在
// 可见裁剪区内。颜色取内侧像素（红方块 → BGRA 红）。
func TestFadeFeatherShapes(t *testing.T) {
	const w, h, bandPx = 60, 60, 16
	src := image.NewRGBA(image.Rect(0, 0, w, h))
	// 实心红块 [10,30)×[24,44)（整块在带底以下，隔离带内规则）。
	for y := 24; y < 44; y++ {
		for x := 10; x < 30; x++ {
			i := src.PixOffset(x, y)
			src.Pix[i], src.Pix[i+1], src.Pix[i+2], src.Pix[i+3] = 255, 0, 0, 255
		}
	}
	const inPx, outPx = 2, 6
	shapes := []drawShape{{
		outline: image.Rect(10, 24, 30, 44),
		clip:    image.Rect(0, 0, 36, h), // 右侧裁到 x=36：环不得越出裁剪区
		radius:  6,
	}}
	out := make([]byte, w*h*4)
	if !fadeFeatherShapes(src, shapes, bandPx, inPx, outPx, out, image.Pt(w, h)) {
		t.Fatal("应写入环像素")
	}
	px := func(x, y int) (b, g, r, a byte) {
		i := (y*w + x) * 4
		return out[i], out[i+1], out[i+2], out[i+3]
	}
	// ① 带底以上不落笔（带内由带渐变单独绘制）。
	for y := 0; y < bandPx; y++ {
		for x := 0; x < w; x++ {
			if _, _, _, a := px(x, y); a != 0 {
				t.Fatalf("带内 (%d,%d) 不应有环像素 a=%d", x, y, a)
			}
		}
	}
	// ② 右边界内 1px（带外）：alpha=1、颜色 = 内侧红（BGRA R 在 +2）。
	_, g, r, a := px(29, 34)
	if a != 255 || r != 255 || g != 0 {
		t.Fatalf("界内 (29,34) = g=%d r=%d a=%d, want 0/255/255", g, r, a)
	}
	// ③ 界外 1.5px：0 < a < 255（衰减中）。
	if _, _, _, a := px(31, 34); a == 0 || a == 255 {
		t.Fatalf("界外 (31,34) a=%d, want 0<a<255", a)
	}
	// ④ 界外超过 outPx：无像素。
	if _, _, _, a := px(38, 34); a != 0 {
		t.Fatalf("环外 (38,34) a=%d, want 0", a)
	}
	// ⑤ 内部深处（距边 > in+1）：不写。
	if _, _, _, a := px(20, 34); a != 0 {
		t.Fatalf("内部 (20,34) a=%d, want 0", a)
	}
	// ⑥ 裁剪区外不写。
	for x := 36; x < w; x++ {
		if _, _, _, a := px(x, 34); a != 0 {
			t.Fatalf("裁剪区外 (%d,34) a=%d, want 0", x, a)
		}
	}
}

// TestFadeFeatherShapesEmpty 空输入（无形状/缓冲不足）→ 不写、不 panic。
func TestFadeFeatherShapesEmpty(t *testing.T) {
	src := image.NewRGBA(image.Rect(0, 0, 10, 10))
	out := make([]byte, 10*10*4)
	if fadeFeatherShapes(src, nil, 4, 2, 6, out, image.Pt(10, 10)) {
		t.Fatal("无形状应返回 false")
	}
	sh := []drawShape{{outline: image.Rect(0, 0, 5, 5), clip: image.Rect(0, 0, 10, 10), radius: 2}}
	if fadeFeatherShapes(src, sh, 4, 2, 6, out[:10], image.Pt(10, 10)) {
		t.Fatal("缓冲不足应返回 false")
	}
}
