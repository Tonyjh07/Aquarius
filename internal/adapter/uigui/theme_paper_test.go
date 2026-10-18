package uigui

// 次窗 Paper 令牌的对比度守卫（D148/D149）。
//
// 为什么要有这组测试：颜色是"看起来"调的，但可读性是**算出来的**。次窗从深色窗灰底
// 换到白面之后，原本在深底上"够灰"的说明文字直接掉到 3.1:1——渲染出来挺干净，低视力
// 用户与高亮屏下却读不动。这类退化不会让任何功能测试变红，只能靠数值钉住。

import (
	"image/color"
	"math"
	"testing"
)

// wcagLum 相对亮度（WCAG 2.x 定义）。
func wcagLum(c color.NRGBA) float64 {
	lin := func(v uint8) float64 {
		s := float64(v) / 255
		if s <= 0.03928 {
			return s / 12.92
		}
		return math.Pow((s+0.055)/1.055, 2.4)
	}
	return 0.2126*lin(c.R) + 0.7152*lin(c.G) + 0.0722*lin(c.B)
}

// wcagRatio 两色对比度（1..21）。
func wcagRatio(a, b color.NRGBA) float64 {
	la, lb := wcagLum(a), wcagLum(b)
	if la < lb {
		la, lb = lb, la
	}
	return (la + 0.05) / (lb + 0.05)
}

// TestPaperTextContrast 文字各档在它可能落在的每个面上都过 AA（4.5:1）。
//
// **面要逐个列**：同一档文字会同时出现在窗体面、卡面与下沉面（输入框/状态栏）；
// 只按白面算会漏掉最暗的那个组合（下沉面比白面低 0.4 左右）。
func TestPaperTextContrast(t *testing.T) {
	for _, tc := range []struct {
		name string
		p    paperPalette
	}{
		{"light", paperLight()},
		{"dark", paperDark()},
	} {
		surfaces := []struct {
			name string
			c    color.NRGBA
		}{
			{"win", tc.p.win},
			{"card", tc.p.card},
			{"sunken", tc.p.sunken},
		}
		for _, fg := range []struct {
			name string
			c    color.NRGBA
		}{
			{"text", tc.p.text},
			{"text2", tc.p.text2},
			{"text3", tc.p.text3},
		} {
			for _, bg := range surfaces {
				if r := wcagRatio(fg.c, bg.c); r < 4.5 {
					t.Errorf("%s: %s on %s = %.2f:1，低于 AA 4.5:1",
						tc.name, fg.name, bg.name, r)
				}
			}
		}
	}
}

// TestPaperAccentContrast 强调色两档分工必须成立：
//   - accent 承载 accentFg（按钮上的字）→ 4.5:1；
//   - brand 是与 accent 同色相的标识色，**不得**被当成按钮底（它的白字达不到 AA，
//     这正是当初拆成两个令牌的原因——把这条性质写成断言，防止以后又被合并回去）。
func TestPaperAccentContrast(t *testing.T) {
	for _, tc := range []struct {
		name string
		p    paperPalette
	}{
		{"light", paperLight()},
		{"dark", paperDark()},
	} {
		if r := wcagRatio(tc.p.accentFg, tc.p.accent); r < 4.5 {
			t.Errorf("%s: accentFg on accent = %.2f:1，低于 AA 4.5:1", tc.name, r)
		}
		if r := wcagRatio(color.NRGBA{R: 0xFF, G: 0xFF, B: 0xFF, A: 0xFF}, tc.p.brand); r >= 4.5 {
			t.Errorf("%s: brand 上白字 = %.2f:1，本就不该达标——两条令牌疑似被合并", tc.name, r)
		}
	}
}

// TestPaperSemanticContrast 语义色（错误/成功/警告）落在卡面上也要能读。
func TestPaperSemanticContrast(t *testing.T) {
	for _, tc := range []struct {
		name string
		p    paperPalette
	}{
		{"light", paperLight()},
		{"dark", paperDark()},
	} {
		for _, fg := range []struct {
			name string
			c    color.NRGBA
		}{
			{"danger", tc.p.danger},
			{"ok", tc.p.ok},
			{"warn", tc.p.warn},
		} {
			if r := wcagRatio(fg.c, tc.p.card); r < 4.5 {
				t.Errorf("%s: %s on card = %.2f:1，低于 AA 4.5:1", tc.name, fg.name, r)
			}
		}
	}
}

// TestPaperGeometryLadder 圆角阶梯四档必须严格递减（窗体 > 卡 > 控件 > chip）。
// 相等或倒挂会让"大容器更圆、小控件更方"的直觉失效——观感上像随手给的数字。
func TestPaperGeometryLadder(t *testing.T) {
	for _, tc := range []struct {
		name string
		p    paperPalette
	}{
		{"light", paperLight()},
		{"dark", paperDark()},
	} {
		p := tc.p
		if !(p.rWin > p.rCard && p.rCard > p.rCtl && p.rCtl > p.rChip && p.rChip > 0) {
			t.Errorf("%s: 圆角阶梯不成立：win=%d card=%d ctl=%d chip=%d",
				tc.name, p.rWin, p.rCard, p.rCtl, p.rChip)
		}
	}
}
