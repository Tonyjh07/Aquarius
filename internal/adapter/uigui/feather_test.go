package uigui

import (
	"image"
	"image/color"
	"math"
	"testing"

	"gioui.org/unit"
)

// TestRrectSD 圆角矩形 SDF（D45/D48 边缘渐隐的几何基础）：中心到近边、边界 0、外正；
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

// featherTestSide 按**当前羽化参数**反推出能给出 ≥minFW px 渐隐带的最小方形边长——测试
// 几何跟随参数走（featherRatio/featherMinDp/featherMaxDp 改了测试仍有效），取不到返回 0。
func featherTestSide(m unit.Metric, minFW int) int {
	for n := 8; n <= 400; n += 4 {
		if featherWidth(m, n, n) >= minFW {
			return n
		}
	}
	return 0
}

// wantFeatherAlpha 参考实现（与 fadeFeatherShapes 独立推导，D48/§15.1）：给定像素中心到
// 真轮廓的有符号距离 d（外正内负）与羽化宽 fw，按「region 边界 alpha=1 → 轮廓 featherEdgeMin
// 的 smoothstep 渐隐」给出期望不透明度（0..255）。仅用于形状内（d ≤ 0）、带下（g=1）。
func wantFeatherAlpha(d float64, fw int) int {
	d0 := -(float64(fw) + 0.5) + 1 // 渐隐首像素（region 边界外 1px）：alpha=1
	span := -d0                    // 渐隐跨度
	t := (d - d0) / span
	if t < 0 {
		t = 0
	} else if t > 1 {
		t = 1
	}
	return int(math.Round((featherEdgeMin + (1-featherEdgeMin)*(1-smoothstep(t))) * 255))
}

// TestFadeFeatherShapes 边缘内容渐隐（D45–D48/§15.1）：region 边界外首像素 alpha=1（与主窗
// 同不透明度、无缝），随 d 向轮廓 smoothstep 衰减到 featherEdgeMin，在轮廓处收尾——
// **只在形状内落笔**（轮廓外不外扩、不堆光晕）；region 区块、淡出带内、裁剪区外一律不写。
// src=nil 时用元素底色。
func TestFadeFeatherShapes(t *testing.T) {
	const bandPx = 16
	m := unit.Metric{PxPerDp: 1, PxPerSp: 1}
	side := featherTestSide(m, 3)
	if side == 0 {
		t.Fatal("当前羽化参数取不到 ≥3px 渐隐带（检查 featherRatio/featherMinDp/featherMaxDp）")
	}
	const left, top = 10, 24 // 形状左/上边界；top 在淡出带之下（带内由带渐变单独绘制）
	w, h := left+side+40, top+side+20
	outline := image.Rect(left, top, left+side, top+side)
	shapes := []drawShape{{
		outline: outline,
		clip:    image.Rect(left+1, 0, outline.Max.X+10, h), // 左裁 1px：该像素在左渐隐带内 → 证明裁剪生效
		radius:  6,
		fill:    color.NRGBA{R: 0xFF, A: 0xFF}, // 底色红（src=nil 时的颜色来源）
	}}
	out := make([]byte, w*h*4)
	if !fadeFeatherShapes(nil, shapes, 0, bandPx, m, out, image.Pt(w, h)) {
		t.Fatal("应写入渐隐像素")
	}
	px := func(x, y int) (b, g, r, a byte) {
		i := (y*w + x) * 4
		return out[i], out[i+1], out[i+2], out[i+3]
	}
	al := func(x, y int) byte { return out[(y*w+x)*4+3] }
	// ① 带底以上不落笔（整块在带下；带内由带渐变单独绘制）。
	for y := 0; y < bandPx; y++ {
		for x := 0; x < w; x++ {
			if a := al(x, y); a != 0 {
				t.Fatalf("带内 (%d,%d) 不应有渐隐像素 a=%d", x, y, a)
			}
		}
	}
	// 渐隐带的横向范围按 featherWidth() 反推（d = x+0.5-轮廓右边，直边段上可精确解析）：
	//   first   = 轮廓右边 - fw → d = -fw+0.5 = 渐隐首像素（alpha=1）
	//   first-1 → d = -(fw+0.5) = region 区块，overlay 不画
	//   lastIn  = 轮廓右边 - 1  → d = -0.5 = 轮廓内最后一像素
	//   outside = 轮廓右边      → d = +0.5 = 轮廓外（D48 不外扩，一律 0）
	fw := featherWidth(m, side, side)
	if fw < 3 {
		t.Fatalf("featherWidth(%d,%d)=%d，期望 ≥3（测试几何失效）", side, side, fw)
	}
	right, y := outline.Max.X, top+10 // 观测行落在直边段内（避开端/角）
	first, lastIn, outside := right-fw, right-1, right
	// ② 渐隐首像素：alpha=1，与主窗像素同不透明度 → 无接缝；颜色 = 底色红。
	b0, g0, r0, a0 := px(first, y)
	if a0 != 255 {
		t.Fatalf("渐隐首像素 (%d,%d) a=%d, want 255", first, y, a0)
	}
	if g0 != 0 || b0 != 0 || r0 < 200 {
		t.Fatalf("首像素颜色 = b=%d g=%d r=%d, want 底色红", b0, g0, r0)
	}
	// ③ 首像素 → 轮廓内末像素：逐点与参考剖面一致——任一「空洞/折点/轮廓处没收尾」都在此报错。
	prev := a0
	for x := first; x <= lastIn; x++ {
		d := float64(x) + 0.5 - float64(right)
		want := byte(wantFeatherAlpha(d, fw))
		if a := al(x, y); a != want {
			t.Fatalf("渐隐 (%d,%d) d=%.1f a=%d, want %d（首像素 %d，轮廓内末像素 %d）", x, y, d, a, want, first, lastIn)
		}
		if a := al(x, y); a > prev {
			t.Fatalf("向轮廓应单调不增: a(%d)=%d > a(%d)=%d", x, a, x-1, prev)
		}
		prev = al(x, y)
	}
	if prev >= a0 {
		t.Fatalf("轮廓处 a=%d 应低于首像素 %d（渐隐已发生）", prev, a0)
	}
	// ④ region 深处（d ≤ -(fw+0.5)）由主窗画，overlay 不写。
	if a := al(first-1, y); a != 0 {
		t.Fatalf("region 内 (%d,%d) a=%d, want 0", first-1, y, a)
	}
	// ⑤ 轮廓外不落笔（D48：只在形状内渐隐，不向外堆光晕）。
	if a := al(outside, y); a != 0 {
		t.Fatalf("轮廓外 (%d,%d) a=%d, want 0", outside, y, a)
	}
	// ⑥ 裁剪区外不写（该像素在左渐隐带内、却落在 clip 之外）。
	if a := al(left, y); a != 0 {
		t.Fatalf("裁剪区外 (%d,%d) a=%d, want 0", left, y, a)
	}
}

// TestFadeFeatherShapesSamplesContent D48：形状内渐隐采样同帧 headless 内容（保文字/图标随
// 渐隐自然淡出），而非一律涂底色——边带处的绿色应透出。
func TestFadeFeatherShapesSamplesContent(t *testing.T) {
	const w, h = 60, 60
	m := unit.Metric{PxPerDp: 1, PxPerSp: 1}
	src := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			src.SetRGBA(x, y, color.RGBA{G: 0xFF, A: 0xFF}) // 全绿（预乘直通）
		}
	}
	shapes := []drawShape{{
		outline: image.Rect(10, 24, 30, 44),
		clip:    image.Rect(0, 0, w, h),
		radius:  6,
		fill:    color.NRGBA{R: 0xFF, A: 0xFF}, // 底色红：若被误用，绿就不会出现
	}}
	out := make([]byte, w*h*4)
	if !fadeFeatherShapes(src, shapes, 0, 0, m, out, image.Pt(w, h)) {
		t.Fatal("应写入渐隐像素")
	}
	first := 30 - featherWidth(m, 20, 20) // 渐隐首像素（d = -fw+0.5，见 TestFadeFeatherShapes）
	i := (34*w + first) * 4
	if out[i+3] == 0 {
		t.Fatalf("边带内 (%d,34) 应有像素", first)
	}
	if out[i+1] < 150 || out[i+2] != 0 { // BGRA：G 高、R=0 → 采样到绿色内容
		t.Fatalf("边带颜色应为内容绿: b=%d g=%d r=%d", out[i], out[i+1], out[i+2])
	}
}

// TestFadeFeatherShapesCornerUniform D47/D48 回归：圆角形状沿对角（圆角法线）的渐隐剖面
// 与直边一致——不得因采样落空而缺像素（D46 的"十字/阶梯"），也不得在对角外堆出光晕。
func TestFadeFeatherShapesCornerUniform(t *testing.T) {
	const w, h = 90, 90
	m := unit.Metric{PxPerDp: 1, PxPerSp: 1}
	// 41×43 圆角 15（复现 GUI 小气泡/1 字气泡的几何）。
	r := image.Rect(20, 20, 61, 63)
	shapes := []drawShape{{
		outline: r,
		clip:    image.Rect(0, 0, w, h),
		radius:  15,
		fill:    color.NRGBA{R: 0x00, G: 0xAE, B: 0xEF, A: 0xFF},
	}}
	out := make([]byte, w*h*4)
	if !fadeFeatherShapes(nil, shapes, 0, 0, m, out, image.Pt(w, h)) {
		t.Fatal("应写入渐隐像素")
	}
	px := func(x, y int) byte { return out[(y*w+x)*4+3] }
	// 几何按被测函数同参推导（反推 featherWidth/SDF）——改羽化宽参数时测试跟着走。
	hw, hh := float32(r.Dx())/2, float32(r.Dy())/2
	rrad := float32(15)
	cx, cy := float32(r.Min.X)+hw, float32(r.Min.Y)+hh
	inner := -float32(featherWidth(m, r.Dx(), r.Dy())) - 0.5 // region 区块上界（d ≤ inner 不画）
	// ① 沿圆角对角线由外向内扫：凡落在渐隐带（inner < d ≤ 0）内的像素必须有值
	//（D47 回归：采样落空会缺像素 → "十字/阶梯"）且向外单调变淡（无折点）。
	prev, rings := byte(0), 0
	for k := 0; k < 24; k++ {
		x, y := r.Min.X+k, r.Min.Y+k
		d := rrectSD(float32(x)+0.5, float32(y)+0.5, cx, cy, hw, hh, rrad)
		if d > 0 {
			continue // 轮廓外：D48 不外扩
		}
		if d <= inner {
			continue // region 区块内：由主窗画
		}
		a := px(x, y)
		if a == 0 {
			t.Fatalf("对角 (%d,%d) d=%.2f 应无空洞（D47 回归：采样不得落空）", x, y, d)
		}
		if rings > 0 && a < prev {
			t.Fatalf("对角由外向内应单调不减: (%d,%d) a=%d < 前点=%d", x, y, a, prev)
		}
		prev, rings = a, rings+1
	}
	if rings == 0 {
		t.Fatal("圆角对角上应有渐隐像素")
	}
	// ② 轮廓外（圆角方角顶点）不得有光晕。
	if a := px(r.Min.X-1, r.Min.Y-1); a != 0 {
		t.Fatal("圆角对角外不应有光晕（D48：不外扩）")
	}
	// ③ 四角剖面一致（渐隐沿真实轮廓均匀，不因落在直边/圆弧而不同）。
	if a0, a1 := px(27, 27), px(53, 27); a0 != a1 {
		t.Fatalf("左右圆角剖面应一致: (27,27)=%d (53,27)=%d", a0, a1)
	}
}

// TestFadeFeatherShapesBandContinuity D48：跨带元素的左右边在带内按 g(y) 额外衰减，带底
// 与带下连续（带底 g→1）——消除旧「带内不落笔」在带底留下的横缝。
func TestFadeFeatherShapesBandContinuity(t *testing.T) {
	const w, h, bandPx = 60, 60, 16
	m := unit.Metric{PxPerDp: 1, PxPerSp: 1}
	shapes := []drawShape{{
		outline: image.Rect(10, -20, 40, 40), // 跨带（顶部滚出视口）：左边直段覆盖整条带
		clip:    image.Rect(0, 0, w, h),
		radius:  8,
		fill:    color.NRGBA{R: 0xFF, A: 0xFF},
	}}
	out := make([]byte, w*h*4)
	if !fadeFeatherShapes(nil, shapes, 0, bandPx, m, out, image.Pt(w, h)) {
		t.Fatal("应写入渐隐像素")
	}
	px := func(x, y int) byte { return out[(y*w+x)*4+3] }
	inBand, below := px(10, 5), px(10, 20) // 同一左边（d 相同）、带内 vs 带下
	if inBand == 0 || below == 0 {
		t.Fatalf("左边带内/带下都应有像素: 带内=%d 带下=%d", inBand, below)
	}
	if inBand >= below {
		t.Fatalf("带内应按 g(y) 更淡: 带内=%d 带下=%d", inBand, below)
	}
	// 带底连续：g(15)≈1，与带下首行几乎相等。
	top, bottom := px(10, bandPx-1), px(10, bandPx)
	if d := int(bottom) - int(top); d > 4 || d < -4 {
		t.Fatalf("带底应连续: g(15)=%d g(16)=%d", top, bottom)
	}
}

// TestFadeFeatherShapesEmpty 空输入（无形状/缓冲不足）→ 不写、不 panic。
func TestFadeFeatherShapesEmpty(t *testing.T) {
	m := unit.Metric{PxPerDp: 1, PxPerSp: 1}
	out := make([]byte, 10*10*4)
	if fadeFeatherShapes(nil, nil, 0, 4, m, out, image.Pt(10, 10)) {
		t.Fatal("无形状应返回 false")
	}
	sh := []drawShape{{outline: image.Rect(0, 0, 5, 5), clip: image.Rect(0, 0, 10, 10), radius: 2}}
	if fadeFeatherShapes(nil, sh, 0, 4, m, out[:10], image.Pt(10, 10)) {
		t.Fatal("缓冲不足应返回 false")
	}
}
