package uigui

import (
	"image"
	"math"
	"os"
	"testing"

	"gioui.org/unit"
)

// refFade 参考实现：与 fadeFrame 独立推导（math.Pow 精确公式、无 LUT），
// 用于交叉验证查表快路径。
func refFade(c byte, al, av float64) float64 {
	colorLin := srgbDecode(float64(c)/255) / al
	if colorLin > 1 {
		colorLin = 1
	}
	return srgbEncode(colorLin) * av
}

func smoothstep(t float64) float64 { return t * t * (3 - 2*t) }

// TestFadeFrameBand 全帧合成的带渐变（D44/§15.3/D62）：带行内容 × smoothstep g(y)
// 预乘合法（C ≤ A）、与参考实现逐字节一致（±1）、渐变单调、带顶以上不可见、
// 带下不衰减（g=1）、全透明无内容、缓冲过短拒绝。
func TestFadeFrameBand(t *testing.T) {
	const w, h, bandH = 4, 6, 3
	// 单个越界矩形形状：左右越出缓冲 → 观测列恒在核心（直边段），radius=0 无圆角影响。
	src := image.NewRGBA(image.Rect(0, 0, w, h))
	// (1,0)(2,0) 半透明红（模拟 spike 实测值188/0/0/128）；
	// (3,0) 不透明纯蓝；其余不透明白（含带下基准行）。
	set := func(x, y int, r, g, b, a uint8) {
		i := src.PixOffset(x, y)
		src.Pix[i], src.Pix[i+1], src.Pix[i+2], src.Pix[i+3] = r, g, b, a
	}
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			set(x, y, 255, 255, 255, 255)
		}
	}
	set(1, 0, 188, 0, 0, 128)
	set(2, 0, 188, 0, 0, 128)
	set(3, 0, 0, 0, 255, 255)

	shapes := []drawShape{{
		// 四周越界 → 全部观测像素落在核心（vis=1，D83 后形状边缘像素确实会羽化，
		// 本测试只考察带渐变 g(y)，观测点必须避开边带）。
		outline: image.Rect(-40, -40, w+40, h+40),
		clip:    image.Rect(0, 0, w, h),
		radius:  0,
	}}
	out := make([]byte, w*h*4)
	if !fadeFrame(src, shapes, fadeBands{bottom: bandH}, m1x(), out, image.Pt(w, h)) {
		t.Fatal("应有内容")
	}
	at := func(x, y, ch int) byte { // ch: 0=B 1=G 2=R 3=A
		return out[(y*w+x)*4+ch]
	}
	// ① 预乘合法 + 带顶以上……本例带顶=0；预乘合法性全图检查。
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			a := at(x, y, 3)
			for ch := 0; ch < 3; ch++ {
				if at(x, y, ch) > a {
					t.Fatalf("(%d,%d) ch%d = %d > A=%d：非预乘", x, y, ch, at(x, y, ch), a)
				}
			}
		}
	}
	// ② 带行与参考实现交叉验证（±1）：al × g(y)。
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
				want := math.Round(refFade(src.Pix[i+ch], al, al*g) * 255)
				if diff := math.Abs(float64(at(x, y, outCh)) - want); diff > 1 {
					t.Fatalf("(%d,%d) ch%d = %d, want %.0f", x, y, ch, at(x, y, outCh), want)
				}
			}
		}
	}
	// ③ 渐变单调：同列自带顶向带底 alpha 递增。
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
	// ④ 带下不衰减（g=1，核心 vis=1 → alpha = 内容 alpha）。
	for y := bandH; y < h; y++ {
		for x := 0; x < w; x++ {
			i := src.PixOffset(x, y)
			if want := src.Pix[i+3]; at(x, y, 3) != want {
				t.Fatalf("带下 (%d,%d) A = %d, want %d", x, y, at(x, y, 3), want)
			}
		}
	}
	// ⑤ 带顶以上不可见：带顶>0 时其上全零。
	out2 := make([]byte, w*h*4)
	if !fadeFrame(src, shapes, fadeBands{top: 2, bottom: bandH}, m1x(), out2, image.Pt(w, h)) {
		t.Fatal("带下应有内容")
	}
	for y := 0; y < 2; y++ {
		for x := 0; x < w; x++ {
			if a := out2[(y*w+x)*4+3]; a != 0 {
				t.Fatalf("带顶以上 (%d,%d) A = %d, want 0", x, y, a)
			}
		}
	}
	// ⑥ 全透明内容 → 无内容。
	empty := image.NewRGBA(image.Rect(0, 0, w, h))
	out3 := make([]byte, w*h*4)
	if fadeFrame(empty, shapes, fadeBands{bottom: bandH}, m1x(), out3, image.Pt(w, h)) {
		t.Fatal("全透明内容不应报告有内容")
	}
	// ⑦ 参数防御：缓冲过短拒绝。
	if fadeFrame(src, shapes, fadeBands{bottom: bandH}, m1x(), out[:10], image.Pt(w, h)) {
		t.Fatal("短缓冲应拒绝")
	}
}

// TestFadeFrameBottomBand D79 底部矮带：带内 g = 1 → 0 smoothstep 渐隐到带底（末像素
// 近 0）、**带上方与带下方都不衰减（g=1）**——带下即状态行/输入栏，绝不能被淡化；与
// 顶带重叠段两带相乘；零值（无底带）不改变任何像素。逐行与参考式交叉验证（±1）。
func TestFadeFrameBottomBand(t *testing.T) {
	const w, h = 4, 6
	src := image.NewRGBA(image.Rect(0, 0, w, h))
	for i := range src.Pix {
		src.Pix[i] = 0xFF // 全不透明白（预乘 255）
	}
	run := func(b fadeBands, clip image.Rectangle) []byte {
		out := make([]byte, w*h*4)
		shapes := []drawShape{{
			// 四周越界 → 观测像素恒在核心（同 TestFadeFrameBand；D83 后边带确实羽化）
			outline: image.Rect(-40, -40, w+40, h+40),
			clip:    clip,
			radius:  0,
		}}
		if !fadeFrame(src, shapes, b, m1x(), out, image.Pt(w, h)) {
			t.Fatal("应有内容")
		}
		return out
	}
	// 转写视口 clip（带底 == 窗底时即整窗）；带底 < 窗底的 chrome 口径见
	// TestFadeFrameLowBandSkipsChrome。
	viewport := image.Rectangle{Max: image.Pt(w, h)}
	at := func(out []byte, x, y int) byte { return out[(y*w+x)*4+3] } // ch3 = A

	// ① 带 [3,6)：带外（y<3）g=1 原样；带内逐行 = 255×smoothstep((带底−y−0.5)/带高)，
	//    单调递减，末行近 0（不为 0——与带外连续、无 1px 空洞）。
	out := run(fadeBands{lowTop: 3, lowEnd: h}, viewport)
	for y := 0; y < h; y++ {
		g := 1.0
		if y >= 3 {
			g = smoothstep((float64(h-y) - 0.5) / float64(h-3))
		}
		want := math.Round(255 * g)
		for x := 0; x < w; x++ {
			if diff := math.Abs(float64(at(out, x, y)) - want); diff > 1 {
				t.Fatalf("(%d,%d) A = %d, want %.0f", x, y, at(out, x, y), want)
			}
		}
	}
	for y := 4; y < h; y++ {
		if cur, prev := at(out, 0, y), at(out, 0, y-1); cur >= prev {
			t.Fatalf("带内行 %d alpha 应递减: %d >= %d", y, cur, prev)
		}
	}
	if a := at(out, 0, h-1); a == 0 || a > 32 {
		t.Fatalf("带底 alpha = %d，应近 0 但非 0（≤32）", a)
	}
	if a := at(out, 0, 2); a != 0xFF {
		t.Fatalf("带上方 alpha = %d, want 255（带外不衰减）", a)
	}

	// ② 单行带（span=1，除零/舍入边界）：带内居中 g=0.5；**带下不属转写形状**——
	//    转写 clip 底 == 带底，带下（状态行/输入栏区）由 chrome 形状覆盖，
	//    见 TestFadeFrameLowBandSkipsChrome（chrome 跨带保持 255）。
	out2 := run(fadeBands{lowTop: 3, lowEnd: 4}, image.Rect(0, 0, w, 4))
	if a := at(out2, 0, 3); a < 96 || a > 160 {
		t.Fatalf("span=1 带内 alpha = %d, want ≈128（g=0.5）", a)
	}
	for y := 4; y < h; y++ {
		for x := 0; x < w; x++ {
			if a := at(out2, x, y); a != 0 {
				t.Fatalf("带下 (%d,%d) A = %d, want 0（转写 clip 止于带底，不画到带下）", x, y, a)
			}
		}
	}

	// ③ 与顶带重叠：两带相乘（顶带 0→1 × 底带 1→0），带顶以上仍全零。
	out3 := run(fadeBands{top: 2, bottom: 4, lowTop: 3, lowEnd: 6}, viewport)
	for y := 0; y < 2; y++ {
		for x := 0; x < w; x++ {
			if a := at(out3, x, y); a != 0 {
				t.Fatalf("顶带以上 (%d,%d) A = %d, want 0", x, y, a)
			}
		}
	}
	want3 := math.Round(255 * smoothstep((3-2+0.5)/2.0) * smoothstep((6-3-0.5)/3.0))
	if diff := math.Abs(float64(at(out3, 0, 3)) - want3); diff > 1 {
		t.Fatalf("重叠行 alpha = %d, want %.0f（相乘）", at(out3, 0, 3), want3)
	}

	// ④ 零值（无底带，收起态口径）= 不衰减。
	out4 := run(fadeBands{}, viewport)
	for x := 0; x < w; x++ {
		if a := at(out4, x, h-1); a != 0xFF {
			t.Fatalf("无底带末行 A = %d, want 255", a)
		}
	}
}

// TestFadeFrameLowBandSkipsChrome D79 底带作用域（实测 bug：tooltip 被淡化）：底带只乘
// 在**转写视口登记**的形状上（clip 底 ≤ 带底）；chrome 形状（悬浮 tips `hoverTip`、状态
// 行、输入栏——整窗 clip，且 tips 画在胶囊上沿之上、几何上跨进带内）一律保持 g=1，
// 否则 tips 卡片顶沿半透明。转写行本身照常在带内渐隐。
func TestFadeFrameLowBandSkipsChrome(t *testing.T) {
	const w, h = 8, 8
	const lowTop, lowEnd = 4, 6 // 带 [4,6)：带下 2 行 = 状态行/输入栏区（带底 < 窗底）
	src := image.NewRGBA(image.Rect(0, 0, w, h))
	for i := range src.Pix {
		src.Pix[i] = 0xFF // 全不透明白
	}
	// 四周越界矩形：观测像素恒在核心直边段（同 TestFadeFrameBand，radius=0；D83 后
	// 形状边缘像素确实羽化，跨带的上下沿也必须越出缓冲才不被边带影响）。
	run := func(clip image.Rectangle) []byte {
		out := make([]byte, w*h*4)
		shapes := []drawShape{{
			outline: image.Rect(-40, 3-40, w+40, 7+40), // 覆盖 y ∈ [3,7) 并上下越界
			clip:    clip,
			radius:  0,
		}}
		if !fadeFrame(src, shapes, fadeBands{lowTop: lowTop, lowEnd: lowEnd},
			m1x(), out, image.Pt(w, h)) {
			t.Fatal("应有内容")
		}
		return out
	}
	at := func(out []byte, x, y int) byte { return out[(y*w+x)*4+3] } // ch3 = A

	// ① chrome（整窗 clip = 悬浮 tips 口径）：跨进带内的顶沿**不淡化**（全 255）。
	chrome := run(image.Rectangle{Max: image.Pt(w, h)})
	for y := 3; y < 7; y++ {
		for x := 0; x < w; x++ {
			if a := at(chrome, x, y); a != 0xFF {
				t.Fatalf("chrome 形状 (%d,%d) A = %d, want 255（chrome 不受底带，D79）", x, y, a)
			}
		}
	}

	// ② 转写行（clip = 转写视口，底 == 带底）：带外原样、带内按参考式渐隐。
	rows := run(image.Rect(0, 0, w, lowEnd))
	for x := 0; x < w; x++ {
		if a := at(rows, x, 3); a != 0xFF {
			t.Fatalf("带上方 (%d,3) A = %d, want 255", x, a)
		}
		for y := lowTop; y < lowEnd; y++ {
			want := math.Round(255 * smoothstep((float64(lowEnd-y)-0.5)/float64(lowEnd-lowTop)))
			if diff := math.Abs(float64(at(rows, x, y)) - want); diff > 1 {
				t.Fatalf("转写行 (%d,%d) A = %d, want %.0f（带内应渐隐）", x, y, at(rows, x, y), want)
			}
		}
	}
}

// m1x 1× DPI 度量（测试共用）。
func m1x() (m unit.Metric) { return unit.Metric{PxPerDp: 1, PxPerSp: 1} }

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

// TestFadeFeatherDisabled AQUARIUS_NO_FEATHER 对比开关（D62 后评估）：禁用后边带像素
// 硬切为全量 alpha（vis 恒 1），核心与轮廓裁剪语义不变。
func TestFadeFeatherDisabled(t *testing.T) {
	const w, h = 220, 120
	m := unit.Metric{PxPerDp: 2, PxPerSp: 2} // 2× 下统一带宽（D70）= 3px，边带才有可观测的渐隐中段
	src := image.NewRGBA(image.Rect(0, 0, w, h))
	for i := range src.Pix {
		src.Pix[i] = 0xFF // 全不透明白
	}
	shapes := []drawShape{{
		outline: image.Rect(10, 10, 210, 110),
		clip:    image.Rect(0, 0, w, h),
		radius:  8,
	}}
	out := make([]byte, w*h*4)
	if !fadeFrame(src, shapes, fadeBands{}, m, out, image.Pt(w, h)) {
		t.Fatal("应有内容")
	}
	atA := func(x, y int) byte { return out[(y*w+x)*4+3] }
	if core := atA(105, 60); core != 0xFF {
		t.Fatalf("核心 alpha = %d, want 255", core)
	}
	// 采样点取轮廓内 1.5px（fw=3 时 d=-1.5 在渐隐带中段）。
	if edge := atA(11, 60); edge >= 0xFF {
		t.Fatalf("默认羽化：边带 alpha = %d, 应 < 255", edge)
	}

	featherDisabled = true
	defer func() { featherDisabled = false }()
	out2 := make([]byte, w*h*4)
	if !fadeFrame(src, shapes, fadeBands{}, m, out2, image.Pt(w, h)) {
		t.Fatal("应有内容")
	}
	if a := out2[(60*w+11)*4+3]; a != 0xFF {
		t.Fatalf("禁用羽化：边带 alpha = %d, want 255（硬切）", a)
	}
	if a := out2[(60*w+105)*4+3]; a != 0xFF {
		t.Fatalf("禁用羽化不应影响核心: %d", a)
	}
	if a := out2[(60*w+9)*4+3]; a != 0 {
		t.Fatalf("轮廓外 alpha = %d, want 0（禁用只去渐隐，不去形状裁剪）", a)
	}
}

// TestEnvBool 环境布尔解析：1/true/yes/on 为真；0/false/off/空/未设为假——
// 显式「0」是「不禁用」，不是禁用（AQUARIUS_NO_FEATHER=0 误禁羽化的教训）。
func TestEnvBool(t *testing.T) {
	t.Setenv("AQUARIUS_NO_FEATHER", "0")
	if envBool("AQUARIUS_NO_FEATHER") {
		t.Fatal(`"0" 应为假`)
	}
	for _, v := range []string{"1", "true", "TRUE", "Yes", "on"} {
		t.Setenv("AQUARIUS_NO_FEATHER", v)
		if !envBool("AQUARIUS_NO_FEATHER") {
			t.Fatalf("%q 应为真", v)
		}
	}
	for _, v := range []string{"", "false", "OFF", "no", "junk"} {
		t.Setenv("AQUARIUS_NO_FEATHER", v)
		if envBool("AQUARIUS_NO_FEATHER") {
			t.Fatalf("%q 应为假", v)
		}
	}
	os.Unsetenv("AQUARIUS_NO_FEATHER")
	if envBool("AQUARIUS_NO_FEATHER") {
		t.Fatal("未设应为假")
	}
}
