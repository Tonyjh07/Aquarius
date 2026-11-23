package uigui

import (
	"image"
	"testing"

	"gioui.org/io/input"
	"gioui.org/layout"

	"github.com/Tonyjh07/Aquarius/internal/port"
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

// TestConfirmStripAutoOpensWhileAsking 正在问的时候条要自己展开（D329 修订）：收起态只写
// 「待答 1」，问句看不见就等于没问。答完自动收回一行；用户手点开的保持展开。
func TestConfirmStripAutoOpensWhileAsking(t *testing.T) {
	t.Parallel()
	u := newFrameUI()
	reply := make(chan port.ConfirmAnswer, 1)
	u.m.startConfirm(port.ConfirmRequest{Tool: "term_exec", Prompt: "run term_exec?"}, reply)

	it := blockView{kind: blockConfirm, bi: 0, confirms: u.m.blocks[0].confirms}
	if !u.confirmIsOpen(0) {
		t.Fatal("确认还在等答复，条应自动展开")
	}
	open := measureWidget(t, u, func(gtx layout.Context) layout.Dimensions {
		w, _, _, _, _ := u.rowStyle(gtx, it, nil)
		return w(gtx)
	}, 600)

	u.m.replyConfirm(port.ConfirmAnswer{Allow: true})
	if u.confirmIsOpen(0) {
		t.Fatal("答完条应自动收回一行")
	}
	collapsed := measureWidget(t, u, func(gtx layout.Context) layout.Dimensions {
		w, _, _, _, _ := u.rowStyle(gtx, it, nil)
		return w(gtx)
	}, 600)
	if open.Y <= collapsed.Y {
		t.Fatalf("在问时未铺开条目: 展开 %d / 收起 %d", open.Y, collapsed.Y)
	}

	// 用户手动点开：没有在等的确认，也保持展开。
	u.confirmOpen = map[int]bool{0: true}
	if !u.confirmIsOpen(0) {
		t.Fatal("手动展开态应保持")
	}
}

// measureWidget 以松散约束量一次部件（帧约束是 Exact，直接量会被拉满窗尺寸）。
func measureWidget(t *testing.T, u *UI, w layout.Widget, width int) image.Point {
	t.Helper()
	gtx, _ := frameGtx(input.Source{})
	gtx.Constraints = layout.Constraints{Max: image.Pt(width, 10000)}
	return w(gtx).Size
}
