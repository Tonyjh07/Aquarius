package uigui

import (
	"image"
	"testing"

	"gioui.org/io/input"
	"gioui.org/layout"
)

// TestConfirmStripCollapsedIsOneRow 连续确认默认只占一行：三十次并发确认也是一行高，
// 点开才按条铺开。这是「确认信息挡消息」的直接回归闸——收起态高过一行就等于没折。
func TestConfirmStripCollapsedIsOneRow(t *testing.T) {
	t.Parallel()
	u := newFrameUI()
	var es []confirmEntry
	for i := 0; i < 30; i++ {
		es = append(es, confirmEntry{q: "run term_exec?", a: "允许"})
	}
	it := blockView{kind: blockConfirm, bi: 0, confirms: es}

	collapsed := measureWidget(t, u, func(gtx layout.Context) layout.Dimensions {
		w, _, _, _, _ := u.rowStyle(gtx, it, nil)
		return w(gtx)
	}, 600)

	u.confirmOpen = map[int]bool{0: true}
	expanded := measureWidget(t, u, func(gtx layout.Context) layout.Dimensions {
		w, _, _, _, _ := u.rowStyle(gtx, it, nil)
		return w(gtx)
	}, 600)

	if collapsed.Y > 24 {
		t.Fatalf("收起态高 %d px，超过一行", collapsed.Y)
	}
	if expanded.Y <= collapsed.Y {
		t.Fatalf("展开态未铺开条目: 收起 %d / 展开 %d", collapsed.Y, expanded.Y)
	}
	if n := it.selCount(); n != len(es) {
		t.Fatalf("键数 = %d, want %d（折叠不铺正文但键数占位防漂移）", n, len(es))
	}
}

// measureWidget 以松散约束量一次部件（帧约束是 Exact，直接量会被拉满窗尺寸）。
func measureWidget(t *testing.T, u *UI, w layout.Widget, width int) image.Point {
	t.Helper()
	gtx, _ := frameGtx(input.Source{})
	gtx.Constraints = layout.Constraints{Max: image.Pt(width, 10000)}
	return w(gtx).Size
}
