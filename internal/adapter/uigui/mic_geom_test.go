package uigui

import (
	"math"
	"testing"
)

// TestMicGeomEqualGap 托架与胶囊头的空隙**处处相等**：侧边（两臂内缘贴头侧）与底部
// （弧内缘贴头下缘）量出的净间隙必须同一个数。这是图标观感的核心约束——托架不是
// 「包住头的一个框」，而是头下半轮廓的等距外扩。
func TestMicGeomEqualGap(t *testing.T) {
	for _, d := range []int{16, 24, 32, 40, 48, 56, 64, 96, 128, 480} {
		for _, stroke := range []float32{2, 2 * float32(d) / 32, float32(d) * 0.055} {
			g := micGeomOf(d, stroke)
			side, bottom := g.sideGap(), g.bottomGap()
			// 两条路径都是同一个 gap 的代数恒等式，差别应只剩浮点误差。
			if math.Abs(float64(side-bottom)) > 1e-4 {
				t.Errorf("d=%d stroke=%g: 侧间隙 %.6f ≠ 底间隙 %.6f", d, stroke, side, bottom)
			}
			// 间隙为正：托架不能压进头里。
			if side <= 0 || bottom <= 0 {
				t.Errorf("d=%d stroke=%g: 间隙非正（side=%g bottom=%g）", d, stroke, side, bottom)
			}
		}
	}
	// 口径钉在字面量上：净间隙 = 0.0525d（改它 = 改图标留白）。
	if g := micGeomOf(48, 48*0.055); math.Abs(float64(g.gap-48*0.0525)) > 1e-4 {
		t.Fatalf("48px 净间隙 = %g，应为 0.0525×48", g.gap)
	}
}

// TestMicGeomInsideButton 图标整枝必须画进圆钮里：沿托架（两臂 + 底部半圆）、竖杆、
// 底座采样，逐点把描边半宽算进去，离圆心都不超过半径。加深托架 / 加宽底座时这里先响，
// 不必等到肉眼看见白边被圆角切掉。
func TestMicGeomInsideButton(t *testing.T) {
	const steps = 64
	for _, d := range []int{16, 24, 32, 48, 64, 128, 480} {
		g := micGeomOf(d, float32(d)*0.055)
		r := float64(d) / 2
		ink := float64(g.stroke) / 2
		center := float64(g.cx)
		// 允许贴边时按像素取整产生的一点点溢出，但不许真的越界。
		const slack = 0.75

		check := func(name string, x, y float64) {
			t.Helper()
			if dd := math.Hypot(x-float64(g.cx), y-float64(d)/2); dd+ink > r+slack {
				t.Errorf("d=%d: %s 点 (%.2f,%.2f) 越出圆钮（距心 %.3f + 墨 %.3f > 半径 %.3f）",
					d, name, x, y, dd, ink, r)
			}
		}
		// 两臂（外缘 x 含墨）。
		for _, sx := range []float64{-1, 1} {
			x := float64(g.cx) + sx*(float64(g.half)+ink)
			for i := 0; i <= steps; i++ {
				t0 := float64(i) / steps
				check("臂", x, lerp(float64(g.armTop), float64(g.capCy), t0))
			}
		}
		// 底部半圆（半径 = half + ink，圆心 = 头下圆角圆心）。
		for i := 0; i <= steps; i++ {
			a := math.Pi * (0.5 + float64(i)/steps) // π/2 → 3π/2 的下半圆
			check("弧", center+(float64(g.half)+ink)*math.Cos(a),
				float64(g.capCy)+(float64(g.half)+ink)*math.Sin(a))
		}
		// 竖杆与底座。
		for i := 0; i <= steps; i++ {
			t0 := float64(i) / steps
			check("杆", center, lerp(float64(g.arcBottom), float64(g.baseY), t0))
		}
		for i := 0; i <= steps; i++ {
			t0 := float64(i) / steps
			check("底", lerp(center-float64(g.baseHalf), center+float64(g.baseHalf), t0), float64(g.baseY)+ink)
		}
	}
}

// TestMicGeomOrder 自上而下的次序：头顶 < 臂顶 < 头下圆角圆心 < 托架最底 < 杆底（底座）。
// 次序错了画出来就不是麦克风（托架跑到头上、杆短到看不见）。
func TestMicGeomOrder(t *testing.T) {
	for _, d := range []int{16, 24, 32, 48, 64, 128, 480} {
		g := micGeomOf(d, float32(d)*0.055)
		if !(g.headTop < g.armTop && g.armTop < g.capCy && g.capCy < g.headBottom) {
			t.Errorf("d=%d: 头顶(%g) < 臂顶(%g) < 头下圆心(%g) < 头底(%g) 不成立",
				d, g.headTop, g.armTop, g.capCy, g.headBottom)
		}
		if !(g.headBottom < g.arcBottom && g.arcBottom < g.baseY) {
			t.Errorf("d=%d: 头底(%g) < 托架最底(%g) < 杆底(%g) 不成立",
				d, g.headBottom, g.arcBottom, g.baseY)
		}
	}
}

// lerp 线性插值（测试采样用）。
func lerp(a, b, t float64) float64 { return a + (b-a)*t }
