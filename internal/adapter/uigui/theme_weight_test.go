package uigui

// 字重分层的像素守卫（D147）。
//
// 背景：曾有一张「字重分层前后对照」图声称生产主题丢失了粗体，实际两侧像素逐行全等
// ——因为 `text.WithCollection` 是**追加**字体而非替换，Gio 仍扫描系统字体库，粗体
// 一直由系统那份 `msyhbd.ttc` 提供（详见 docs/decisions.md D147）。
//
// 本测试把结论变成可执行断言，防止两件事：
//  1. 字体加载方式被改动后，粗体**悄悄退化成全常规**（没有任何测试会发现）；
//  2. 换到带 Medium 面的字体族后，`SemiBold ≡ Bold` 这个前提不再成立却不自知。
//
// 两个用例都需要真实字形栅格化，因此走 headless 离屏渲染，并在无 CJK 系统字体
// （或离屏不可用）的机器上跳过。

import (
	"image"
	"image/color"
	"os"
	"testing"

	"gioui.org/font"
	"gioui.org/gpu/headless"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"
	"gioui.org/widget/material"
)

// weightProbe 一段文字的栅格化结果：着墨像素数（区分粗细）+ 像素哈希（判同一字形）。
type weightProbe struct {
	ink  int
	hash uint64
}

// shapeOnce 用 th 以给定字重渲染 s，返回着墨量与像素哈希。
// 画布固定、白底、单行左对齐——只求可比，不求好看。
func shapeOnce(t *testing.T, th *material.Theme, wt font.Weight, s string, sp float32) weightProbe {
	t.Helper()
	const (
		w, h = 460, 60
	)
	win, err := headless.NewWindow(w, h)
	if err != nil {
		t.Skipf("离屏渲染不可用: %v", err)
	}
	defer win.Release()

	var ops op.Ops
	gtx := layout.Context{
		Ops:         &ops,
		Metric:      unit.Metric{PxPerDp: 1, PxPerSp: 1},
		Constraints: layout.Constraints{Max: image.Pt(w, h)},
	}
	// 固定白底：否则透明区域的 RGB 参与哈希会掩盖字形差异。
	func() {
		defer clip.Rect(image.Rect(0, 0, w, h)).Push(&ops).Pop()
		paint.ColorOp{Color: color.NRGBA{R: 255, G: 255, B: 255, A: 255}}.Add(&ops)
		paint.PaintOp{}.Add(&ops)
	}()

	func() {
		defer op.Offset(image.Pt(4, 6)).Push(&ops).Pop()
		l := material.Label(th, unit.Sp(sp), s)
		l.Font.Weight = wt
		l.Color = color.NRGBA{R: 17, G: 17, B: 20, A: 255}
		l.MaxLines = 1
		l.Layout(gtx)
	}()

	if err := win.Frame(&ops); err != nil {
		t.Fatalf("渲染帧: %v", err)
	}
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	if err := win.Screenshot(img); err != nil {
		t.Fatalf("截图: %v", err)
	}

	p := weightProbe{hash: 1469598103934665603} // FNV-1a 偏移基准
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			c := img.RGBAAt(x, y)
			p.hash = (p.hash ^ uint64(c.R)) * 1099511628211
			p.hash = (p.hash ^ uint64(c.G)) * 1099511628211
			p.hash = (p.hash ^ uint64(c.B)) * 1099511628211
			lum := (299*int(c.R) + 587*int(c.G) + 114*int(c.B)) / 1000
			if lum < 150 {
				p.ink++
			}
		}
	}
	return p
}

// weightProbeTheme 生产主题（newTheme 的真实装配路径）。
// 无 CJK 系统字体时返回 nil —— 此时字重由 gofont 提供，断言无意义。
func weightProbeTheme(t *testing.T) *material.Theme {
	t.Helper()
	cands := newPlatform(Options{}, true).SystemFontCandidates()
	if len(loadCJKFaces(cands)) == 0 {
		return nil
	}
	return newTheme(cands)
}

// TestWeightTiersBoldIsHeavier 粗体必须比常规**显著**更粗。
//
// 这是「字重是否真的生效」的唯一自动化证据：若字体集合被改成只含常规面、或误加
// text.NoSystemFonts() 把系统字体扫描关掉（两者都会让 Bold 退化成 Regular），
// 本条立刻失败。
func TestWeightTiersBoldIsHeavier(t *testing.T) {
	if os.Getenv("AQUARIUS_SKIP_PIXEL") != "" {
		t.Skip("AQUARIUS_SKIP_PIXEL 已设")
	}
	th := weightProbeTheme(t)
	if th == nil {
		t.Skip("本机无 CJK 系统字体，字重断言不适用")
	}
	const s = "令牌系统 v2 · 令牌系统"
	normal := shapeOnce(t, th, font.Normal, s, 15)
	bold := shapeOnce(t, th, font.Bold, s, 15)

	if normal.ink == 0 {
		t.Fatal("常规字重着墨为 0：渲染或字体加载失败")
	}
	// 实测 421 → 768（约 1.82 倍）；门槛取 1.2 留足字体族差异（黑体/宋体笔画更重）。
	if float64(bold.ink) < float64(normal.ink)*1.2 {
		t.Fatalf("粗体没有更粗：Normal ink=%d, Bold ink=%d（比值 %.2f，期望 ≥1.2）\n"+
			"多半是字体集合只剩常规面，或 shaper 加了 text.NoSystemFonts()（D147）",
			normal.ink, bold.ink, float64(bold.ink)/float64(normal.ink))
	}
}

// TestWeightTiersNoMediumFace 记录「当前 CJK 字体族没有 Medium 面」这一事实。
//
// SemiBold 与 Bold 必须渲染成**同一个字形**（像素哈希全等）。这条断言失败意味着
// 换到了带 Medium 的家族——那是好消息，但必须同步更新 DESIGN §15.4 与 D147
// 的「二元档」结论，否则文档就与实现脱节了。
func TestWeightTiersNoMediumFace(t *testing.T) {
	if os.Getenv("AQUARIUS_SKIP_PIXEL") != "" {
		t.Skip("AQUARIUS_SKIP_PIXEL 已设")
	}
	th := weightProbeTheme(t)
	if th == nil {
		t.Skip("本机无 CJK 系统字体，字重断言不适用")
	}
	const s = "令牌系统 v2 · 令牌系统"
	semi := shapeOnce(t, th, font.SemiBold, s, 15)
	bold := shapeOnce(t, th, font.Bold, s, 15)

	if semi.hash != bold.hash {
		t.Fatalf("SemiBold 与 Bold 字形不同（hash %016x vs %016x）：当前字体族已提供 Medium 面。\n"+
			"这是能力升级，但请同步更新 DESIGN §15.4「字体」段与 docs/decisions.md D147 "+
			"的「二元档」结论，再改本断言。", semi.hash, bold.hash)
	}
	// 顺带守住：SemiBold 也不能等同于 Regular（否则「强调」就没有视觉差异了）。
	if normal := shapeOnce(t, th, font.Normal, s, 15); semi.hash == normal.hash {
		t.Fatalf("SemiBold 与 Normal 字形相同（hash %016x）：粗体已退化成常规（D147）", semi.hash)
	}
}
