package uigui

// select_test.go D91 跨块拖选：候选/激活状态机、跨块跨度与 Ctrl+C 复制、清除源
// （主键新按压 / Escape / 编辑器获焦 / 结构指纹 / 清屏）、锚点两条路径（同批几何
// vs 上批 widget 选区）、slop 防误触、pointToCaret/caretIn 几何、selSpansOf 方向归一。
// 全走 input.Router 头less 回放：帧 = 布局 + 提交 hit 树，事件经 q.Queue 注入，剪贴板
// 断言读 q.WriteClipboard，零网络零窗口。
// 注意：pointer.Kind 是位掩码；入队只有 Press/Move/Release/Cancel/Scroll 合法
// （Drag 不可入队——按压中的 Move 由 Router 转为 Drag）。

import (
	"image"
	"testing"

	"gioui.org/f32"
	"gioui.org/io/input"
	"gioui.org/io/key"
	"gioui.org/io/pointer"
)

// selFrame 一帧布局 + 提交 hit 树（Gio 次序：渲染之后、present 之前）。
func selFrame(q *input.Router, u *UI) {
	gtx, ops := frameGtx(q.Source())
	u.layout(gtx)
	q.Frame(ops)
}

// newSelUI 两行固定语料（user + assistant）+ 首帧几何就绪。
func newSelUI() (*UI, *input.Router) {
	u := newFrameUI()
	u.m.add(blockUser, "alpha bravo")
	u.m.add(blockAssistant, "charlie delta")
	q := new(input.Router)
	selFrame(q, u)
	return u, q
}

// selKeyRect 取已铺开键的窗口系行盒（未就绪即测试前提破产）。
func selKeyRect(t *testing.T, u *UI, k int) image.Rectangle {
	t.Helper()
	if k >= len(u.keyRects) || !u.keyRects[k].laid || u.keyRects[k].rect.Empty() {
		t.Fatalf("键 %d 几何未就绪: len=%d", k, len(u.keyRects))
	}
	return u.keyRects[k].rect
}

// selPointer 组一个同指针 ID 的鼠标事件（ID=7 区分默认零值；primary = 按压中；
// 拖动用 Kind=Move——按压中 Router 自动转 Drag）。
func selPointer(kind pointer.Kind, p image.Point, primary bool) pointer.Event {
	e := pointer.Event{
		Kind: kind, Position: f32.Pt(float32(p.X), float32(p.Y)),
		PointerID: 7, Source: pointer.Mouse,
	}
	if primary {
		e.Buttons = pointer.ButtonPrimary
	}
	return e
}

// selArmDrag 起手拖选并抬手：press 键 0 左缘 → 越 slop 拖至键 1 右缘 → Release。
// 停在选区保持（armed）态，供清除源系列测试复用。
func selArmDrag(t *testing.T, q *input.Router, u *UI) {
	t.Helper()
	r0 := selKeyRect(t, u, 0)
	r1 := selKeyRect(t, u, 1)
	q.Queue(selPointer(pointer.Press, r0.Min, true))
	selFrame(q, u)
	q.Queue(selPointer(pointer.Move, r1.Max.Add(image.Pt(-1, -1)), true))
	selFrame(q, u)
	if !u.sel.active {
		t.Fatal("起手拖选应激活")
	}
	q.Queue(selPointer(pointer.Release, r1.Max, false))
	selFrame(q, u)
	if !u.sel.active {
		t.Fatal("抬手应保留选区")
	}
}

// TestSelDragAcrossBlocksCopy D91 主链路：块内按压候选（未越 slop 不激活）→
// 跨块拖动激活 → 锚 0 焦末的整段两键跨度 → 抬手保持 → Ctrl+C 写系统剪贴板
// （键间 \n 拼接，方向不敏感——本例锚在前）。
func TestSelDragAcrossBlocksCopy(t *testing.T) {
	u, q := newSelUI()
	r0, r1 := selKeyRect(t, u, 0), selKeyRect(t, u, 1)

	q.Queue(selPointer(pointer.Press, r0.Min, true))
	selFrame(q, u)
	if !u.sel.cand || u.sel.active {
		t.Fatalf("按压应入候选未激活: cand=%v active=%v", u.sel.cand, u.sel.active)
	}

	// slop 防误触：3dp 内的微抖不激活（D91 ①）。
	q.Queue(selPointer(pointer.Move, r0.Min.Add(image.Pt(2, 0)), true))
	selFrame(q, u)
	if u.sel.active {
		t.Fatal("slop 内微抖不应激活")
	}

	q.Queue(selPointer(pointer.Move, r1.Max.Add(image.Pt(-1, -1)), true))
	selFrame(q, u)
	if !u.sel.active {
		t.Fatal("越 slop 拖过块边界应激活")
	}
	if len(u.selSpansBuf) != 2 {
		t.Fatalf("跨度数 = %d, want 2（整段跨两键）", len(u.selSpansBuf))
	}
	if u.sel.anchor != (selPoint{key: 0, rune: 0}) {
		t.Fatalf("锚 = %+v, want {0 0}（press 几何落键 0 行首）", u.sel.anchor)
	}
	if n1 := u.keyRects[1].nrunes; u.sel.focus != (selPoint{key: 1, rune: n1}) {
		t.Fatalf("焦点 = %+v, want {1 %d}（拖至键 1 行尾）", u.sel.focus, n1)
	}

	q.Queue(selPointer(pointer.Release, r1.Max, false))
	selFrame(q, u)
	if !u.sel.active {
		t.Fatal("抬手后选区应保持（armed）")
	}

	q.Queue(key.Event{Name: "C", State: key.Press, Modifiers: key.ModShortcut})
	selFrame(q, u)
	_, content, ok := q.WriteClipboard()
	if !ok {
		t.Fatal("Ctrl+C 应写剪贴板")
	}
	if got, want := string(content), "alpha bravo\ncharlie delta"; got != want {
		t.Fatalf("剪贴板 = %q, want %q", got, want)
	}
}

// TestSelSlopReleaseKeepsCandidate 微抖 + 抬手全程不激活：候选被 Release 收掉，
// 选区保持无（D91 ①——块内点按不误建选区，原生手势不受干扰）。
func TestSelSlopReleaseKeepsCandidate(t *testing.T) {
	u, q := newSelUI()
	r0 := selKeyRect(t, u, 0)

	q.Queue(selPointer(pointer.Press, r0.Min, true))
	selFrame(q, u)
	q.Queue(selPointer(pointer.Move, r0.Min.Add(image.Pt(2, 2)), true))
	selFrame(q, u)
	q.Queue(selPointer(pointer.Release, r0.Min, false))
	selFrame(q, u)
	if u.sel.active || u.sel.cand {
		t.Fatalf("微抖抬手不应留态: active=%v cand=%v", u.sel.active, u.sel.cand)
	}
}

// TestSelAnchorFromWidgetSelection 锚点 else 分支（D91 ②）：Press 落在上一批帧、
// 越 slop 在本批激活 → pressedNow=false → 锚取 widget Selection().start（双击选词后
// 拖拽锚词首的通路），而非按压几何点。
func TestSelAnchorFromWidgetSelection(t *testing.T) {
	u, q := newSelUI()
	r0, r1 := selKeyRect(t, u, 0), selKeyRect(t, u, 1)

	q.Queue(selPointer(pointer.Press, r0.Min, true))
	selFrame(q, u)
	if !u.sel.cand {
		t.Fatal("Press 应入候选")
	}
	st, _ := u.selFor(0).Selection()
	if st != 0 {
		t.Fatalf("前提：单击后 widget 选区起点 = 按压处 caret, got %d（本测试 press 行首）", st)
	}

	q.Queue(selPointer(pointer.Move, r1.Max.Add(image.Pt(-1, -1)), true))
	selFrame(q, u)
	if !u.sel.active {
		t.Fatal("跨批拖动应激活")
	}
	if u.sel.anchor.key != 0 || u.sel.anchor.rune != st {
		t.Fatalf("锚 = %+v, want {0 %d}（widget 选区起点）", u.sel.anchor, st)
	}
}

// TestSelClearSources D91 ④ 清除源全集：结构指纹变化（增行 → keyFp 变）、Escape、
// 主键新按压、编辑器获焦（focusPending 唤出）、系统 Cancel、model 清屏。
func TestSelClearSources(t *testing.T) {
	u, q := newSelUI()

	// ① 结构指纹：增行 → 下帧 transcript 重算 keyFp → 再下帧消费者比对失配即清
	//（updateSel 先于 transcript，故需两帧；选区不漂移到错块）。
	selArmDrag(t, q, u)
	u.m.add(blockNotice, "结构变了")
	selFrame(q, u)
	selFrame(q, u)
	if u.sel.active {
		t.Fatal("结构变化应清选")
	}

	// ② Escape 放弃。
	selArmDrag(t, q, u)
	q.Queue(key.Event{Name: key.NameEscape, State: key.Press})
	selFrame(q, u)
	if u.sel.active {
		t.Fatal("Escape 应清选")
	}

	// ③ 主键新按压清除（副键不碰——留给 S2 右键菜单）。
	selArmDrag(t, q, u)
	r0 := selKeyRect(t, u, 0)
	q.Queue(selPointer(pointer.Press, r0.Min, true))
	selFrame(q, u)
	if u.sel.active {
		t.Fatal("主键新按压应清选")
	}

	// ④ 编辑器获焦 = 要打字 → 放弃选区。
	selArmDrag(t, q, u)
	u.focusPending = true
	selFrame(q, u)
	selFrame(q, u)
	if u.sel.active {
		t.Fatal("编辑器获焦应清选")
	}

	// ⑤ 系统 Cancel 只收候选，已建选区保留（他者接管不吞用户已有选区）。
	selArmDrag(t, q, u)
	q.Queue(selPointer(pointer.Cancel, r0.Min, false))
	selFrame(q, u)
	if !u.sel.active {
		t.Fatal("Cancel 不应清已建选区")
	}

	// ⑥ model 清屏（/new、/switch）：选区与键几何一并归零。
	selArmDrag(t, q, u)
	u.m.clear()
	if u.sel.active {
		t.Fatal("清屏应清选区")
	}
	if len(u.keyRects) != 0 || u.keyFp != 0 {
		t.Fatalf("清屏应清键几何: len=%d fp=%d", len(u.keyRects), u.keyFp)
	}
}

// TestPointToCaret 几何：行首/行尾 caret 边界、rect 内命中取本键、rect 上取最近键。
func TestPointToCaret(t *testing.T) {
	u, _ := newSelUI()
	r0, r1 := selKeyRect(t, u, 0), selKeyRect(t, u, 1)
	n0 := u.keyRects[0].nrunes

	if got := u.caretIn(0, r0.Min); got != 0 {
		t.Fatalf("行首 caret = %d, want 0", got)
	}
	if got := u.caretIn(0, image.Pt(r0.Max.X-1, (r0.Min.Y+r0.Max.Y)/2)); got != n0 {
		t.Fatalf("行尾 caret = %d, want %d", got, n0)
	}
	mid0 := image.Pt((r0.Min.X+r0.Max.X)/2, (r0.Min.Y+r0.Max.Y)/2)
	if p, ok := u.pointToCaret(mid0); !ok || p.key != 0 {
		t.Fatalf("rect 内命中 = %+v ok=%v, want 键 0", p, ok)
	}
	if p, ok := u.pointToCaret(r1.Min); !ok || p.key != 1 {
		t.Fatalf("键 1 rect 起点 = %+v ok=%v, want 键 1", p, ok)
	}
}

// TestSelSpansOfDirection selSpansOf 纯逻辑：方向归一（反向拖选同果）、同键取区间、
// 两键之间全选、空键跳过。
func TestSelSpansOfDirection(t *testing.T) {
	nr := map[int]int{0: 3, 1: 4, 2: 5}
	count := func(k int) int { return nr[k] }

	want := []selSpan{{key: 0, start: 1, end: 3}, {key: 1, start: 0, end: 4}, {key: 2, start: 0, end: 2}}
	fwd := selSpansOf(selPoint{key: 0, rune: 1}, selPoint{key: 2, rune: 2}, count)
	if len(fwd) != len(want) {
		t.Fatalf("正向跨度数 = %d, want %d", len(fwd), len(want))
	}
	for i := range want {
		if fwd[i] != want[i] {
			t.Fatalf("正向跨度[%d] = %+v, want %+v", i, fwd[i], want[i])
		}
	}
	rev := selSpansOf(selPoint{key: 2, rune: 2}, selPoint{key: 0, rune: 1}, count)
	if len(rev) != len(want) {
		t.Fatalf("反向跨度数 = %d, want %d", len(rev), len(want))
	}
	for i := range want {
		if rev[i] != want[i] {
			t.Fatalf("反向跨度[%d] = %+v, want %+v（方向归一）", i, rev[i], want[i])
		}
	}

	same := selSpansOf(selPoint{key: 1, rune: 1}, selPoint{key: 1, rune: 3}, count)
	if len(same) != 1 || same[0] != (selSpan{key: 1, start: 1, end: 3}) {
		t.Fatalf("同键跨度 = %+v, want [{1 1 3}]", same)
	}

	nr[1] = 0 // 空键（未铺开）：跳过且不吞键间空隙
	skip := selSpansOf(selPoint{key: 0, rune: 1}, selPoint{key: 2, rune: 2}, count)
	if len(skip) != 2 || skip[0].key != 0 || skip[1].key != 2 {
		t.Fatalf("空键跳过 = %+v, want 键 0 与键 2", skip)
	}
}
