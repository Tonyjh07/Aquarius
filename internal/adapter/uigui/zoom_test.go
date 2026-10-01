package uigui

import (
	"math"
	"testing"

	"gioui.org/io/input"
	"gioui.org/unit"
)

// D90（§15.8）：双缩放旋钮——Metric 咽喉点、夹取回落、帧内生效。

// TestZoomedMetricIdentity 缺省旋钮（1.0/15，含未预存槽的零值 UI）恒等：与未缩放
// 渲染逐像素一致。
func TestZoomedMetricIdentity(t *testing.T) {
	u := newFrameUI()
	m := unit.Metric{PxPerDp: 1.25, PxPerSp: 1.25}
	if got := u.zoomedMetric(m); got != m {
		t.Fatalf("缺省旋钮应恒等: %+v", got)
	}
}

// TestZoomedMetricScales 叠加语义：PxPerDp ×= scale（元素几何等比）、PxPerSp ×=
// scale × font/15（Sp 字面量按正文 15 的等比阶梯解释——最终字号 = font × scale）。
func TestZoomedMetricScales(t *testing.T) {
	u := newFrameUI()
	u.SetZoom(2.0, 20)
	got := u.zoomedMetric(unit.Metric{PxPerDp: 1, PxPerSp: 1})
	if float64(got.PxPerDp) != 2 {
		t.Fatalf("元素缩放: PxPerDp = %v, want 2", got.PxPerDp)
	}
	if want := float32(2.0 * 20.0 / 15.0); got.PxPerSp != want {
		t.Fatalf("字号叠加: PxPerSp = %v, want %v", got.PxPerSp, want)
	}
}

// TestClampZoomKnobs 越界/NaN 回落缺省（scale∈[0.75,2.5] 缺省 1、font∈[10,28] 缺省 15）。
func TestClampZoomKnobs(t *testing.T) {
	cases := []struct{ scale, font, wScale, wFont float64 }{
		{0.5, 15, 1, 15},
		{3, 15, 1, 15},
		{1.5, 5, 1.5, 15},
		{1.5, 30, 1.5, 15},
		{0.75, 10, 0.75, 10},
		{2.5, 28, 2.5, 28},
		{1.25, 17.5, 1.25, 17.5},
	}
	for _, c := range cases {
		if got := clampKnobs(c.scale, c.font); got != (zoomKnobs{c.wScale, c.wFont}) {
			t.Fatalf("clampKnobs(%v,%v) = %+v, want (%v,%v)", c.scale, c.font, got, c.wScale, c.wFont)
		}
	}
	if got := clampKnobs(math.NaN(), math.NaN()); got != (zoomKnobs{1, 15}) {
		t.Fatalf("NaN 应回落缺省: %+v", got)
	}
}

// TestFrameZoomScalesShapes 帧内生效（咽喉点在 frame 顶）：scale 翻倍 → layout 存进
// frameMetric 的 PxPerDp 随之翻倍，球锚（ballRect 派生）等比——命中面与绘制同源缩放。
func TestFrameZoomScalesShapes(t *testing.T) {
	for _, scale := range []float64{1, 2} {
		u := newFrameUI()
		u.SetZoom(scale, 15)
		gtx, _ := frameGtx(input.Source{})
		u.frame(gtx, func() {})
		if float64(u.frameMetric.PxPerDp) != scale {
			t.Fatalf("scale %v: frameMetric.PxPerDp = %v", scale, u.frameMetric.PxPerDp)
		}
		ball := ballRect(u.frameSize, u.frameMetric.Dp)
		if got, want := ball.Dx(), int(scale*inputRowDp); got != want {
			t.Fatalf("scale %v: 球径 = %d, want %d", scale, got, want)
		}
		if got, want := ball.Min.X, int(scale*sideMarginDp); got != want {
			t.Fatalf("scale %v: 球左缘 = %d, want %d", scale, got, want)
		}
	}
}
