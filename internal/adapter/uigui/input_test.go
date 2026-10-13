package uigui

// input_test.go 输入栏（§15.2）：编辑态生命周期、编辑/复制/引用入队、淡出源自绘
// （D62 caret / D133 选区高亮——material.Editor 的 caret 与选区同受 gtx.Focused 门控，
// 淡出源 Source 为零值 ⇒ 两样都得自绘才进可见位图）。

import (
	"image"
	"testing"
	"time"

	"gioui.org/io/input"
)

// TestFadeEditorSelectionPainted D133：淡出源（主窗**可见位图**）必须画出编辑器的选区
// 高亮。同帧同几何下「有选区 vs 无选区」的差异像素即选区，且必须落在胶囊内。
func TestFadeEditorSelectionPainted(t *testing.T) {
	u := newFrameUI()
	u.editor.SingleLine = true
	u.editor.Submit = true
	u.hwnd = 1 // 非 0 即过 fadeCompose 门控
	u.editor.SetText("hello world 选区可见性")
	q := new(input.Router)
	// 一帧 = 真窗 pass（layout + 提交）+ 淡出源 pass。获焦标记在两遍之间按真窗 pass 的
	// 口径置位（headless 无 Focus 源，gtx.Focused 恒 false，故测试手动喂真窗那一遍的值）。
	frame := func(focused bool) {
		gtx, ops := frameGtx(q.Source())
		u.stepExpand(time.Now()) // 帧驱动同真窗 frame：推进展开进度/pillAlpha（D77 内容显隐）
		u.stepAnim()
		u.layout(gtx)
		q.Frame(ops)
		u.caretFocused = focused
		if !u.fadeCompose() {
			t.Fatal("淡出源合成未产出")
		}
	}
	u.editor.SetCaret(0, 0)
	frame(true)
	if u.fade.img == nil {
		t.Fatal("淡出源未就绪（fadeCompose 未产出）")
	}
	var pill image.Rectangle
	for _, sh := range u.shapes {
		if sh.outline.Dx() > 200 && sh.outline.Dy() >= 30 && sh.outline.Dy() <= 90 {
			pill = sh.outline
		}
	}
	if pill.Empty() {
		t.Fatal("未找到输入胶囊矩形")
	}
	base := copyRGBA(u.fade.img)

	// 选中前 5 个字符（caret 起点同为 0 ⇒ caret 位置不变，差异只可能来自选区）。
	u.editor.SetCaret(0, 5)
	frame(true)
	n, in, out := fadeDiff(base, u.fade.img, pill)
	if n == 0 {
		t.Fatal("淡出源位图无差异：选区高亮未画进可见位图（D133 回归）")
	}
	if out > 0 {
		t.Fatalf("有 %d 个差异像素落在胶囊外（选区几何算错）", out)
	}
	if in < 20 {
		t.Fatalf("选区差异像素 = %d（胶囊内 %d），量级不像 5 个字的选区", n, in)
	}

	// 清选区 → 回到基线（不残留）。
	u.editor.SetCaret(0, 0)
	frame(true)
	if n, _, _ := fadeDiff(base, u.fade.img, pill); n != 0 {
		t.Fatalf("清选区后仍有 %d 个差异像素（应与基线一致）", n)
	}

	// 未获焦不画（Gio 的 paintSelection 同门控，勿越权）：与「未获焦 + 无选区」帧比——
	// 未获焦时 caret 也不画，故基线取未获焦帧（caret 的有无不混进差异）。
	u.editor.SetCaret(0, 0)
	frame(false)
	dimBase := copyRGBA(u.fade.img)
	u.editor.SetCaret(0, 5)
	frame(false)
	if n, _, _ := fadeDiff(dimBase, u.fade.img, pill); n != 0 {
		t.Fatalf("未获焦仍画了选区（%d 差异像素）", n)
	}
}

// fadeDiff 两张位图的差异像素计数（n）与其中落在 rect 内/外的个数。
func fadeDiff(a, b *image.RGBA, rect image.Rectangle) (n, in, out int) {
	if a == nil || b == nil || a.Rect != b.Rect {
		return 0, 0, 0
	}
	for y := a.Rect.Min.Y; y < a.Rect.Max.Y; y++ {
		for x := a.Rect.Min.X; x < a.Rect.Max.X; x++ {
			if a.RGBAAt(x, y) == b.RGBAAt(x, y) {
				continue
			}
			n++
			if (image.Point{x, y}).In(rect) {
				in++
			} else {
				out++
			}
		}
	}
	return n, in, out
}

// copyRGBA 位图快照（fade.img 逐帧复用，比对需拷贝）。
func copyRGBA(src *image.RGBA) *image.RGBA {
	if src == nil {
		return nil
	}
	dst := image.NewRGBA(src.Rect)
	copy(dst.Pix, src.Pix)
	return dst
}
