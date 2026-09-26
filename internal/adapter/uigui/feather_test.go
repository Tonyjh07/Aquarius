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

// TestFadeFeatherShapes 羽化环（D45）：边界内 alpha=1、界外 2px smoothstep 衰减、
// 3px 外为 0；颜色取内侧像素（红方块 → BGRA 红）。带内行随带渐变同步衰减
// （带顶附近 ≈0、带底 → 满值），与带渐变同风格衔接。
func TestFadeFeatherShapes(t *testing.T) {
	const w, h, bandPx = 40, 60, 16
	src := image.NewRGBA(image.Rect(0, 0, w, h))
	// 实心红块 [10,30)×[8,40)（跨带：顶边在带内）。
	for y := 8; y < 40; y++ {
		for x := 10; x < 30; x++ {
			i := src.PixOffset(x, y)
			src.Pix[i], src.Pix[i+1], src.Pix[i+2], src.Pix[i+3] = 255, 0, 0, 255
		}
	}
	shapes := []drawShape{{r: image.Rect(10, 8, 30, 40), radius: 6}}
	out := make([]byte, w*h*4)
	if !fadeFeatherShapes(src, shapes, bandPx, out, image.Pt(w, h)) {
		t.Fatal("应写入环像素")
	}
	px := func(x, y int) (b, g, r, a byte) {
		i := (y*w + x) * 4
		return out[i], out[i+1], out[i+2], out[i+3]
	}
	// ① 形状上方带顶附近（环区之外）恒零。
	for x := 0; x < w; x++ {
		if _, _, _, a := px(x, 1); a != 0 {
			t.Fatalf("带顶行 (%d,1) 不应有环像素 a=%d", x, a)
		}
	}
	// ② 带内边缘随带渐变衰减（0<a<255），小于带外同点（满值）→ 与带渐变同风格。
	_, _, _, aBand := px(29, 10)
	if aBand == 0 || aBand == 255 {
		t.Fatalf("带内边缘 (29,10) a=%d, want 0<a<255", aBand)
	}
	// ③ 右边界内 1px（d=-0.5、带外）：alpha=1、颜色 = 内侧红（BGRA R 在 +2）。
	_, g, r, a := px(29, 30)
	if a != 255 || r != 255 || g != 0 {
		t.Fatalf("界内 (29,30) = g=%d r=%d a=%d, want 0/255/255", g, r, a)
	}
	if aBand >= a {
		t.Fatalf("带内边缘 a=%d 应小于带外同点 a=%d（随带渐变衰减）", aBand, a)
	}
	// ④ 界外 1px（d=1.5）：0 < a < 255（衰减中）。
	if _, _, _, a := px(31, 30); a == 0 || a == 255 {
		t.Fatalf("界外 (31,30) a=%d, want 0<a<255", a)
	}
	// ⑤ 界外 4px：无像素。
	if _, _, _, a := px(34, 30); a != 0 {
		t.Fatalf("环外 (34,30) a=%d, want 0", a)
	}
	// ⑥ 形状内部深处（距边 >1px）：不写。
	if _, _, _, a := px(20, 25); a != 0 {
		t.Fatalf("内部 (20,25) a=%d, want 0", a)
	}
}

// TestFadeFeatherShapesEmpty 空输入（无形状/缓冲不足）→ 不写、不 panic。
func TestFadeFeatherShapesEmpty(t *testing.T) {
	src := image.NewRGBA(image.Rect(0, 0, 10, 10))
	out := make([]byte, 10*10*4)
	if fadeFeatherShapes(src, nil, 4, out, image.Pt(10, 10)) {
		t.Fatal("无形状应返回 false")
	}
	if fadeFeatherShapes(src, []drawShape{{r: image.Rect(0, 0, 5, 5), radius: 2}},
		4, out[:10], image.Pt(10, 10)) {
		t.Fatal("缓冲不足应返回 false")
	}
}
