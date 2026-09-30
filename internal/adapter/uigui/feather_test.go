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

// wantFeatherAlpha 参考实现（与 fadeFrame 独立推导，D48/§15.1/D62）：给定像素中心到
// 真轮廓的有符号距离 d（外正内负）与羽化宽 fw，按「边带外缘 alpha=1 → 轮廓 featherEdgeMin
// 的 smoothstep 渐隐」给出期望不透明度系数（0..1）。仅用于边带（-(fw+0.5) < d ≤ 0）。
func wantFeatherVis(d float64, fw int) float64 {
	d0 := -(float64(fw) + 0.5) + 1 // 渐隐首像素：alpha=1（与核心连续）
	span := -d0                    // 渐隐跨度
	t := (d - d0) / span
	if t < 0 {
		t = 0
	} else if t > 1 {
		t = 1
	}
	return featherEdgeMin + (1-featherEdgeMin)*(1-smoothstep(t))
}

// TestFadeFrameFeatherEdge 全帧合成的边缘渐隐（D45–D48/§15.1/D62）：边带外缘首像素
// alpha=1（与核心无缝），随 d 向轮廓 smoothstep 衰减到 featherEdgeMin，在轮廓处收尾——
// **只在形状内落笔**（轮廓外不外扩、不堆光晕）；核心不透明、带内不写（整块在带下）、
// 裁剪区外不写。src=nil 时用元素底色。
func TestFadeFrameFeatherEdge(t *testing.T) {
	const bandPx = 16
	m := unit.Metric{PxPerDp: 2, PxPerSp: 2} // 2× 下统一带宽（D70）= 3px，剖面可逐像素观测
	const side = 40                          // 固定几何、r=6 直边段充足——带宽与元素尺寸无关（D70），无需按参数反推边长
	const left, top = 10, 24                 // 形状左/上边界；top 在淡出带之下（带内由带渐变按 g(y) 衰减）
	w, h := left+side+40, top+side+20
	outline := image.Rect(left, top, left+side, top+side)
	shapes := []drawShape{{
		outline: outline,
		clip:    image.Rect(left+1, 0, outline.Max.X+10, h), // 左裁 1px：该像素在左渐隐带内 → 证明裁剪生效
		radius:  6,
		fill:    color.NRGBA{R: 0xFF, A: 0xFF}, // 底色红（src=nil 时的颜色来源）
	}}
	out := make([]byte, w*h*4)
	if !fadeFrame(nil, shapes, fadeBands{bottom: bandPx}, m, out, image.Pt(w, h)) {
		t.Fatal("应写入形状像素")
	}
	px := func(x, y int) (b, g, r, a byte) {
		i := (y*w + x) * 4
		return out[i], out[i+1], out[i+2], out[i+3]
	}
	al := func(x, y int) byte { return out[(y*w+x)*4+3] }
	// ① 带底以上不落笔（形状整块在带下；带内像素属揭示带管，无形状则全透明）。
	for y := 0; y < bandPx; y++ {
		for x := 0; x < w; x++ {
			if a := al(x, y); a != 0 {
				t.Fatalf("带内 (%d,%d) 不应有像素 a=%d", x, y, a)
			}
		}
	}
	// 渐隐带的横向范围按 featherWidth() 反推（d = x+0.5-轮廓右边，直边段上可精确解析）：
	//   first   = 轮廓右边 - fw → d = -fw+0.5 = 渐隐首像素（vis=1，与核心连续）
	//   first-1 → d = -(fw+0.5) = 核心上界
	//   lastIn  = 轮廓右边 - 1  → d = -0.5 = 轮廓内最后一像素
	//   outside = 轮廓右边      → d = +0.5 = 轮廓外（D48 不外扩，一律 0）
	fw := featherWidth(m, side, side)
	if fw < 3 {
		t.Fatalf("featherWidth(%d,%d)=%d，期望 ≥3（测试几何失效）", side, side, fw)
	}
	right, y := outline.Max.X, top+10 // 观测行落在直边段内（避开端/角）
	first, lastIn, outside := right-fw, right-1, right
	// ② 核心不透明（vis=1）、颜色 = 底色红。
	bc, gc, rc, ac := px(first-2, y)
	if ac != 255 {
		t.Fatalf("核心像素 (%d,%d) a=%d, want 255", first-2, y, ac)
	}
	if gc != 0 || bc != 0 || rc < 200 {
		t.Fatalf("核心颜色 = b=%d g=%d r=%d, want 底色红", bc, gc, rc)
	}
	// ③ 渐隐首像素 → 轮廓内末像素：逐点与参考剖面一致——任一「空洞/折点/轮廓处没收尾」
	// 都在此报错。
	prev := byte(255)
	for x := first; x <= lastIn; x++ {
		d := float64(x) + 0.5 - float64(right)
		want := byte(math.Round(wantFeatherVis(d, fw) * 255))
		if a := al(x, y); a != want {
			t.Fatalf("渐隐 (%d,%d) d=%.1f a=%d, want %d（首像素 %d，轮廓内末像素 %d）", x, y, d, a, want, first, lastIn)
		}
		if a := al(x, y); a > prev {
			t.Fatalf("向轮廓应单调不增: a(%d)=%d > a(%d)=%d", x, a, x-1, prev)
		}
		prev = al(x, y)
	}
	if prev >= 255 {
		t.Fatalf("轮廓处 a=%d 应低于核心 255（渐隐已发生）", prev)
	}
	// ④ 轮廓外不落笔（D48：只在形状内渐隐，不向外堆光晕）。
	if a := al(outside, y); a != 0 {
		t.Fatalf("轮廓外 (%d,%d) a=%d, want 0", outside, y, a)
	}
	// ⑤ 裁剪区外不写（该像素在左渐隐带内、却落在 clip 之外）。
	if a := al(left, y); a != 0 {
		t.Fatalf("裁剪区外 (%d,%d) a=%d, want 0", left, y, a)
	}
}

// TestFadeFrameFeatherUniformAcrossShapes 羽化带宽全元素统一（D70，用户实测：消息气泡
// 边缘与输入胶囊边缘羽化不一致）：同一帧内不同高度的形状沿直边的渐隐剖面必须逐像素
// 一致——高气泡（短边大）此前带宽封顶、胶囊（48dp 短边）只有其一半左右。修前本测试红。
func TestFadeFrameFeatherUniformAcrossShapes(t *testing.T) {
	const w, h = 460, 400
	m := unit.Metric{PxPerDp: 2, PxPerSp: 2} // 2× 下胶囊带宽 = 3px，剖面逐像素可分辨
	shapes := []drawShape{
		// 高气泡 300×150px（复现多行复合气泡的短边量级）。
		{outline: image.Rect(20, 60, 320, 210), clip: image.Rect(0, 0, w, h), radius: 24,
			fill: color.NRGBA{R: 0xFF, A: 0xFF}},
		// 输入胶囊 400×96px（= 48dp@2×，r = h/2 全圆）。
		{outline: image.Rect(20, 240, 420, 336), clip: image.Rect(0, 0, w, h), radius: 48,
			fill: color.NRGBA{R: 0xFF, A: 0xFF}},
	}
	out := make([]byte, w*h*4)
	if !fadeFrame(nil, shapes, fadeBands{}, m, out, image.Pt(w, h)) {
		t.Fatal("应写入形状像素")
	}
	px := func(x, y int) byte { return out[(y*w+x)*4+3] }
	// 直边段内取同一列（气泡底边直段 x∈[44,296]、胶囊 x∈[68,372] 的交集），
	// 自轮廓向内比对渐隐剖面。
	const x = 200
	for k := 0; k < 8; k++ {
		if a, b := px(x, 210-1-k), px(x, 336-1-k); a != b {
			t.Fatalf("轮廓内第 %d 像素羽化剖面应一致（D70 统一带宽）：气泡 a=%d, 胶囊 b=%d",
				k+1, a, b)
		}
	}
	// 剖面确实在渐隐（防两边恒 255 的假一致）：轮廓内首像素明显低于核心 255。
	if a := px(x, 210-1); a >= 200 {
		t.Fatalf("轮廓内首像素应明显渐隐: a=%d", a)
	}
}

// TestFadeFrameSamplesContent D48/D62：形状内（核心与边带）采样同帧 headless 内容
// （保文字/图标随渐隐自然淡出），而非一律涂底色——核心与边带处的绿色都应透出。
func TestFadeFrameSamplesContent(t *testing.T) {
	const w, h = 60, 60
	m := unit.Metric{PxPerDp: 1, PxPerSp: 1}
	src := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			src.SetRGBA(x, y, color.RGBA{G: 0xFF, A: 0xFF}) // 全绿（预乘直通）
		}
	}
	shapes := []drawShape{{
		outline: image.Rect(10, 10, 30, 30),
		clip:    image.Rect(0, 0, w, h),
		radius:  6,
		fill:    color.NRGBA{R: 0xFF, A: 0xFF}, // 底色红：若被误用，绿就不会出现
	}}
	out := make([]byte, w*h*4)
	if !fadeFrame(src, shapes, fadeBands{}, m, out, image.Pt(w, h)) {
		t.Fatal("应写入形状像素")
	}
	core := (20*w + 15) * 4               // 核心像素（远离边带）
	first := 30 - featherWidth(m, 20, 20) // 渐隐首像素（d = -fw+0.5）
	edge := (20*w + first) * 4
	for _, i := range []int{core, edge} {
		if out[i+3] == 0 {
			t.Fatalf("(%d) 应有像素", i/4/w)
		}
		if out[i+1] < 150 || out[i+2] != 0 { // BGRA：G 高、R=0 → 采样到绿色内容
			t.Fatalf("颜色应为内容绿: b=%d g=%d r=%d", out[i], out[i+1], out[i+2])
		}
	}
}

// TestFadeFrameCornerUniform D47/D48 回归：圆角形状沿对角（圆角法线）的核心+渐隐剖面
// 连续——不得因采样落空而缺像素（D46 的"十字/阶梯"），也不得在对角外堆出光晕。
func TestFadeFrameCornerUniform(t *testing.T) {
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
	if !fadeFrame(nil, shapes, fadeBands{}, m, out, image.Pt(w, h)) {
		t.Fatal("应写入形状像素")
	}
	px := func(x, y int) byte { return out[(y*w+x)*4+3] }
	// 几何按被测函数同参推导（反推 featherWidth/SDF）——改羽化宽参数时测试跟着走。
	hw, hh := float32(r.Dx())/2, float32(r.Dy())/2
	rrad := float32(15)
	cx, cy := float32(r.Min.X)+hw, float32(r.Min.Y)+hh
	inner := -float32(featherWidth(m, r.Dx(), r.Dy())) - 0.5 // 核心上界（d ≤ inner）
	// ① 沿圆角对角线由外向内扫：凡 d ≤ 0 的像素必须有值（D47 回归：采样落空会缺像素 →
	// "十字/阶梯"），且由外向内单调不减（边带渐入 → 核心 255，无折点）。
	prev, rings := byte(0), 0
	for k := 0; k < 24; k++ {
		x, y := r.Min.X+k, r.Min.Y+k
		d := rrectSD(float32(x)+0.5, float32(y)+0.5, cx, cy, hw, hh, rrad)
		if d > 0 {
			continue // 轮廓外：D48 不外扩
		}
		a := px(x, y)
		if a == 0 {
			t.Fatalf("对角 (%d,%d) d=%.2f 应无空洞（D47 回归：采样不得落空）", x, y, d)
		}
		if rings > 0 && a < prev {
			t.Fatalf("对角由外向内应单调不减: (%d,%d) a=%d < 前点=%d", x, y, a, prev)
		}
		if d <= inner && a != 255 {
			t.Fatalf("核心 (%d,%d) d=%.2f 应不透明: a=%d", x, y, d, a)
		}
		prev, rings = a, rings+1
	}
	if rings == 0 {
		t.Fatal("圆角对角上应有像素")
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

// TestFadeFrameBandContinuity D48/D62：跨带元素的左右边在带内按 g(y) 额外衰减，带底
// 与带下连续（带底 g→1）——同一位图内 g(y) 与 vis 连续相乘，无横缝。
func TestFadeFrameBandContinuity(t *testing.T) {
	const w, h, bandPx = 60, 60, 16
	m := unit.Metric{PxPerDp: 1, PxPerSp: 1}
	shapes := []drawShape{{
		outline: image.Rect(10, -20, 40, 40), // 跨带（顶部滚出视口）：左边直段覆盖整条带
		clip:    image.Rect(0, 0, w, h),
		radius:  8,
		fill:    color.NRGBA{R: 0xFF, A: 0xFF},
	}}
	out := make([]byte, w*h*4)
	if !fadeFrame(nil, shapes, fadeBands{bottom: bandPx}, m, out, image.Pt(w, h)) {
		t.Fatal("应写入形状像素")
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

// TestFadeFrameBandTopInvisible D54/D62：带顶以上不可见（揭示带全隐段）——带顶上方的
// 形状像素一律不写。
func TestFadeFrameBandTopInvisible(t *testing.T) {
	const w, h = 40, 40
	m := unit.Metric{PxPerDp: 1, PxPerSp: 1}
	shapes := []drawShape{{
		outline: image.Rect(5, 0, 35, 40), // 跨越整条带
		clip:    image.Rect(0, 0, w, h),
		radius:  4,
		fill:    color.NRGBA{R: 0xFF, A: 0xFF},
	}}
	out := make([]byte, w*h*4)
	if !fadeFrame(nil, shapes, fadeBands{top: 10, bottom: 20}, m, out, image.Pt(w, h)) {
		t.Fatal("带下应有形状像素")
	}
	px := func(x, y int) byte { return out[(y*w+x)*4+3] }
	for y := 0; y < 10; y++ {
		if a := px(20, y); a != 0 {
			t.Fatalf("带顶以上 (%d,%d) a=%d, want 0", 20, y, a)
		}
	}
	if a := px(20, 12); a == 0 {
		t.Fatal("带内首行应有像素（g>0）")
	}
}

// TestFadeFrameEmpty 空输入（无形状/零尺寸/缓冲不足）→ 不写、不 panic。
func TestFadeFrameEmpty(t *testing.T) {
	m := unit.Metric{PxPerDp: 1, PxPerSp: 1}
	out := make([]byte, 10*10*4)
	if fadeFrame(nil, nil, fadeBands{bottom: 4}, m, out, image.Pt(10, 10)) {
		t.Fatal("无形状应返回 false")
	}
	sh := []drawShape{{outline: image.Rect(0, 0, 5, 5), clip: image.Rect(0, 0, 10, 10), radius: 2}}
	if fadeFrame(nil, sh, fadeBands{bottom: 4}, m, out[:10], image.Pt(10, 10)) {
		t.Fatal("缓冲不足应返回 false")
	}
	if fadeFrame(nil, sh, fadeBands{bottom: 4}, m, out, image.Pt(0, 10)) {
		t.Fatal("零宽应返回 false")
	}
}
