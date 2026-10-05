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
	"strings"
	"testing"

	"gioui.org/f32"
	"gioui.org/io/input"
	"gioui.org/io/key"
	"gioui.org/io/pointer"

	"github.com/Tonyjh07/Aquarius/internal/domain/tool"
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

// --- S1c 验收修正（2026-10-05，D91 更正）：行带取整交叠下的行内二分 ---

// selLineGroups 折行键的逐视觉行 rune 分组（独立于 caretIn 的线性扫描真值）：
// 每 rune 的 Regions(m,m+1) 盒顶与 keyRects 行带顶逐一相等（同出 makeRegion），
// 按顶值归组——组序 = 行序，组内 = 该行 rune 区间 [首,末)。
func selLineGroups(t *testing.T, u *UI, k int) [][]int {
	t.Helper()
	g := &u.keyRects[k]
	if !g.laid || len(g.lines) < 2 {
		t.Fatalf("前提：键 %d 折成多行 laid=%v lines=%d", k, g.laid, len(g.lines))
	}
	s := u.selFor(k)
	topOf := map[int]int{} // 行带顶（widget 系）→ 行序
	for i, ln := range g.lines {
		topOf[ln.Min.Y-g.origin.Y] = i
	}
	groups := make([][]int, len(g.lines))
	for m := 0; m < g.nrunes; m++ {
		b := s.Regions(m, m+1, nil)
		if len(b) == 0 {
			continue
		}
		li, ok := topOf[b[0].Bounds.Min.Y]
		if !ok {
			t.Fatalf("rune %d 盒顶 %d 不在任何行带上（几何口径破裂）", m, b[0].Bounds.Min.Y)
		}
		groups[li] = append(groups[li], m)
	}
	return groups
}

// TestSelCaretInStaysInPointedLine 行内二分回归（D91 更正）：行带交叠 ~1px 时
// e 门槛若用本行底会把下一行 rune 混入区间、Min.X 谓词非单调，落点随机漂到
// 下一行。锁定：每行**左缘**落点 = 本行首 rune（错行即此 bug），且逐行严格递增。
func TestSelCaretInStaysInPointedLine(t *testing.T) {
	u := newFrameUI()
	u.m.add(blockAssistant, strings.Repeat("这一段话足够长以便在转写区宽度内折行成多个视觉行。", 6))
	q := new(input.Router)
	selFrame(q, u)
	groups := selLineGroups(t, u, 0)
	g := &u.keyRects[0]

	prevFirst := -1
	for i, grp := range groups {
		if len(grp) == 0 {
			t.Fatalf("行 %d 无 rune（分组破裂）", i)
		}
		ln := g.lines[i]
		left := image.Pt(ln.Min.X+1, (ln.Min.Y+ln.Max.Y)/2)
		if got := u.caretIn(0, left); got != grp[0] {
			t.Fatalf("行 %d 左缘落点 = %d, want 本行首 rune %d（漂移到别行 = D91 更正前的 bug）",
				i, got, grp[0])
		}
		if got := u.caretIn(0, left); got <= prevFirst && i > 0 {
			t.Fatalf("行 %d 左缘落点 %d 未随行递增（前值 %d）", i, got, prevFirst)
		}
		prevFirst = grp[0]
	}
}

// TestSelSameBatchDragCrossLine 同事件批 press+越 slop 拖动（生产鼠标高频上报的
// 常见形态，锚走几何路径）：首行左缘起手拖到末行右缘——锚/焦点都必须跨行到位，
// 回拖首行左缘焦点须回到首行（D91 更正回归）。
func TestSelSameBatchDragCrossLine(t *testing.T) {
	u := newFrameUI()
	u.m.add(blockAssistant, strings.Repeat("同批次按压与拖动的事件批形态也需要跨行可选。", 8))
	q := new(input.Router)
	selFrame(q, u)
	groups := selLineGroups(t, u, 0)
	g := &u.keyRects[0]
	first, last := g.lines[0], g.lines[len(g.lines)-1]

	q.Queue(
		selPointer(pointer.Press, image.Pt(first.Min.X+1, (first.Min.Y+first.Max.Y)/2), true),
		selPointer(pointer.Move, image.Pt(last.Max.X-1, (last.Min.Y+last.Max.Y)/2), true),
	)
	selFrame(q, u)
	if !u.sel.active {
		t.Fatal("同批越 slop 应激活")
	}
	if u.sel.anchor != (selPoint{key: 0, rune: groups[0][0]}) {
		t.Fatalf("锚 = %+v, want {0 %d}（首行首 rune——漂到别行 = 二分越行）",
			u.sel.anchor, groups[0][0])
	}
	if u.sel.focus.rune != g.nrunes {
		t.Fatalf("焦点 = %+v, want 末行尾 %d", u.sel.focus, g.nrunes)
	}

	// 回拖首行左缘：焦点须回首行（≤ 首行末 rune），不得滞留尾部。
	q.Queue(selPointer(pointer.Move, image.Pt(first.Min.X+1, (first.Min.Y+first.Max.Y)/2), true))
	selFrame(q, u)
	endOfFirst := groups[0][len(groups[0])-1] + 1 // 首行右缘 caret（可为次行首）
	if u.sel.focus.rune > endOfFirst {
		t.Fatalf("回拖后焦点 = %d, want ≤ %d（首行右缘）", u.sel.focus.rune, endOfFirst)
	}
}

// TestSelDragAcrossMDParagraphs 同一助手气泡内两个 markdown 段落（vstack 双键）
// 跨块拖选：跨度两键、焦点落次键（D91 ② 手排纵列的块内偏移路径）。
func TestSelDragAcrossMDParagraphs(t *testing.T) {
	u := newFrameUI()
	u.m.add(blockAssistant, "第一段落比较长一些这样能够折行。\n\n第二段落也需要有内容用于拖选目标。")
	q := new(input.Router)
	selFrame(q, u)
	if len(u.keyRects) != 2 {
		t.Fatalf("键数 = %d, want 2", len(u.keyRects))
	}
	r0, r1 := selKeyRect(t, u, 0), selKeyRect(t, u, 1)

	q.Queue(selPointer(pointer.Press, image.Pt(r0.Min.X+2, (r0.Min.Y+r0.Max.Y)/2), true))
	selFrame(q, u)
	if !u.sel.cand {
		t.Fatal("按压段 1 应入候选")
	}
	q.Queue(selPointer(pointer.Move, image.Pt(r1.Max.X-2, (r1.Min.Y+r1.Max.Y)/2), true))
	selFrame(q, u)
	if !u.sel.active {
		t.Fatal("跨段拖动应激活")
	}
	if u.sel.focus.key != 1 || len(u.selSpansBuf) != 2 {
		t.Fatalf("焦点 = %+v 跨度数 = %d, want 键 1 / 2 段跨度", u.sel.focus, len(u.selSpansBuf))
	}
}

// TestSelDragAcrossCollapsedChip 跨折叠工具 chip 拖选：text → chip（键 1/2 不铺开）
// → text，跨度恰两段（空键跳过、chip 头不捕获）。
func TestSelDragAcrossCollapsedChip(t *testing.T) {
	u := newFrameUI()
	u.m.add(blockAssistant, "工具前的正文内容")
	u.m.addToolCall(tool.Call{ID: "c1", Name: "think"})
	u.m.attachToolResult(tool.Result{CallID: "c1", OK: true})
	u.m.add(blockAssistant, "工具后的正文内容")
	q := new(input.Router)
	selFrame(q, u)
	if len(u.keyRects) != 4 {
		t.Fatalf("键数 = %d, want 4（text/chip×2/text）", len(u.keyRects))
	}
	r0, r3 := selKeyRect(t, u, 0), selKeyRect(t, u, 3)
	if u.keyRects[1].laid || u.keyRects[2].laid {
		t.Fatal("折叠 chip 的键不应铺开")
	}

	q.Queue(selPointer(pointer.Press, image.Pt(r0.Min.X+2, (r0.Min.Y+r0.Max.Y)/2), true))
	selFrame(q, u)
	q.Queue(selPointer(pointer.Move, image.Pt(r3.Max.X-2, (r3.Min.Y+r3.Max.Y)/2), true))
	selFrame(q, u)
	if !u.sel.active {
		t.Fatal("跨 chip 拖动应激活")
	}
	if u.sel.focus.key != 3 || len(u.selSpansBuf) != 2 {
		t.Fatalf("焦点 = %+v 跨度数 = %d, want 键 3 / 2 段跨度", u.sel.focus, len(u.selSpansBuf))
	}
}

// selPointerMod 带修饰键的指针事件（D101 Shift 扩选测试用；其余同 selPointer）。
func selPointerMod(kind pointer.Kind, p image.Point, mods key.Modifiers) pointer.Event {
	e := selPointer(kind, p, true)
	e.Modifiers = mods
	return e
}

// TestSelShiftClickExtend D101：选区 active 时 Shift+点击跨块扩选——锚不动、焦点
// 跳到点击处（可反向）、抬手保持、Ctrl+C 按锚→焦拼接；无激活选区的 Shift+点击
// 仍走原生路径（入候选不激活，D91 口径）。
func TestSelShiftClickExtend(t *testing.T) {
	u, q := newSelUI()
	selArmDrag(t, q, u) // 选区 {0,0}..{1,n1} armed
	if u.sel.anchor != (selPoint{key: 0, rune: 0}) {
		t.Fatalf("锚 = %+v, want {0 0}", u.sel.anchor)
	}

	// Shift+点击键 0 中部：焦点跳回键 0（跨度缩为单键、方向不变）。
	r0 := selKeyRect(t, u, 0)
	mid := image.Pt((r0.Min.X+r0.Max.X)/2, (r0.Min.Y+r0.Max.Y)/2)
	q.Queue(selPointerMod(pointer.Press, mid, key.ModShift))
	selFrame(q, u)
	if !u.sel.active {
		t.Fatal("Shift 扩选应保持激活")
	}
	if u.sel.anchor != (selPoint{key: 0, rune: 0}) {
		t.Fatalf("锚被移动: %+v", u.sel.anchor)
	}
	if u.sel.focus.key != 0 || u.sel.focus.rune == 0 {
		t.Fatalf("焦点应落在键 0 中部: %+v", u.sel.focus)
	}
	q.Queue(selPointer(pointer.Release, mid, false))
	selFrame(q, u)
	q.Queue(key.Event{Name: "C", State: key.Press, Modifiers: key.ModShortcut})
	selFrame(q, u)
	_, content, ok := q.WriteClipboard()
	if !ok {
		t.Fatal("Ctrl+C 应写剪贴板")
	}
	if want := "alpha bravo"[:u.sel.focus.rune]; string(content) != want {
		t.Fatalf("剪贴板 = %q, want %q", content, want)
	}

	// 反向扩选：Shift+点击键 1 尾部 → 焦点越过锚、跨度恢复两键。
	r1 := selKeyRect(t, u, 1)
	q.Queue(selPointerMod(pointer.Press, r1.Max.Add(image.Pt(-1, -1)), key.ModShift))
	selFrame(q, u)
	if u.sel.focus.key != 1 || len(u.selSpansBuf) != 2 {
		t.Fatalf("焦点/跨度 = %+v/%d, want 键 1/2", u.sel.focus, len(u.selSpansBuf))
	}

	// 无激活选区：Shift+点击 = 原生路径（候选、不激活；无清除语义可触发）。
	q.Queue(key.Event{Name: key.NameEscape, State: key.Press})
	selFrame(q, u)
	if u.sel.active {
		t.Fatal("Escape 应清选区")
	}
	q.Queue(selPointerMod(pointer.Press, r0.Min, key.ModShift))
	selFrame(q, u)
	if u.sel.active || !u.sel.cand {
		t.Fatalf("无选区 Shift+点击 = active %v cand %v, want false/true", u.sel.active, u.sel.cand)
	}
}
