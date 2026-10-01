package uigui

import (
	"image"
	"testing"

	"gioui.org/io/input"
	"gioui.org/unit"
)

// D90（§15.8）：主窗像素尺寸——粗界/地板夹取、右停靠重贴、极端尺寸布局地板。

// TestClampWindowPx 粗界 [200,3840]×[200,2160] + 布局地板（240dp × DPI × scale 抬下限）。
func TestClampWindowPx(t *testing.T) {
	cases := []struct {
		name         string
		w, h         int
		dpi, scale   float64
		wantW, wantH int
	}{
		{"缺省原样", 608, 460, 1, 1, 608, 460},
		{"粗界夹取", 100, 5000, 1, 1, 240, 2160},
		{"宽 0 回粗界下限再抬地板", 0, 460, 1, 1, 240, 460},
		{"地板随 DPI×scale", 300, 300, 1.25, 2, 600, 600},
		{"地板不抬合法值", 304, 920, 1.25, 1, 304, 920},
	}
	for _, c := range cases {
		gotW, gotH := clampWindowPx(c.w, c.h, c.dpi, c.scale)
		if gotW != c.wantW || gotH != c.wantH {
			t.Fatalf("%s: clampWindowPx(%d,%d,%v,%v) = (%d,%d), want (%d,%d)",
				c.name, c.w, c.h, c.dpi, c.scale, gotW, gotH, c.wantW, c.wantH)
		}
	}
}

// TestDockedPosWidthInvariant 停靠位与窗宽无关（D90 实证性质）：球锚左定于 sideMargin
// （ballRect.x = margin），滑出位只依赖工作区边缘、球径与 sliver——热改窗宽后停靠中的
// 窗口无需重锚，贴条位置天然不变。
func TestDockedPosWidthInvariant(t *testing.T) {
	work := rect{left: 0, top: 0, right: 1920, bottom: 1080}
	sliver := int32(dpId(unit.Dp(dockSliverDp)))
	for _, edge := range []string{"left", "right"} {
		var want int32
		for i, w := range []int{608, 304, 920, 200} {
			a := ballRect(image.Pt(w, 460), dpId)
			got := dockSlidePos(point{}, a, work, edge, sliver).x
			if i == 0 {
				want = got
				continue
			}
			if got != want {
				t.Fatalf("%s 停靠 w=%d: x = %d, want 与 w=608 相同的 %d", edge, w, got, want)
			}
		}
	}
}

// TestExtremeShortWindowLayout 极矮窗地板（D90）：带高夹 ≤ transH/4（顶/底带同规），
// 转写区高度耗尽时区间收空、不越界不恐慌（fade 层「极矮窗整片渐隐」预留归口）。
func TestExtremeShortWindowLayout(t *testing.T) {
	cases := []struct {
		h                    int
		wantBand, wantLowTop int
	}{
		{460, 56, 460 - 72 - 12}, // 默认高：带不夹
		{200, 32, 200 - 72 - 12}, // transH=128 → 顶带夹 32（底带 12 < 32 不夹）
		{80, 2, 6},               // transH=8 → 带夹 2
		{60, 0, 0},               // transH<0 → 夹 0，区间空
	}
	for _, c := range cases {
		u := newFrameUI()
		gtx, _ := frameGtxSize(input.Source{}, 608, c.h)
		u.frame(gtx, func() {})
		if u.bandBottom-u.bandTop != c.wantBand {
			t.Fatalf("h=%d: 顶带高 = %d, want %d", c.h, u.bandBottom-u.bandTop, c.wantBand)
		}
		if u.bandLowTop != c.wantLowTop {
			t.Fatalf("h=%d: bandLowTop = %d, want %d", c.h, u.bandLowTop, c.wantLowTop)
		}
	}
}

// TestNarrowWindowShapesInsideFrame 极窄窗（304px 口径）：布局重排后全部形状仍在窗内
// （D71 行级手势与 ULW 位图都以 frameSize 为界）。
func TestNarrowWindowShapesInsideFrame(t *testing.T) {
	u := newFrameUI()
	u.m.add(blockUser, "窄窗问题")
	u.m.add(blockAssistant, "窄窗回答，内容长一些以触发多行重排。")
	gtx, _ := frameGtxSize(input.Source{}, 304, 460)
	u.frame(gtx, func() {})
	for _, s := range u.shapes {
		if s.outline.Min.X < 0 || s.outline.Max.X > u.frameSize.X ||
			s.outline.Min.Y < 0 || s.outline.Max.Y > u.frameSize.Y {
			t.Fatalf("形状越窗: %v（frame=%v）", s.outline, u.frameSize)
		}
	}
}
