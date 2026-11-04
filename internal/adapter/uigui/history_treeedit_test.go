package uigui

// 树编辑模式的状态栏开关（D257/D258）测试：
//   - 开关是**内核状态的投影**：假树视图说「在模式里」才亮，UI 不自己记；
//   - 点开关投 `/tree-edit`（开）/ `--exit`（关）；脏时项集多出「已改 N 处」+「放弃」，
//     点「放弃」投 `--cancel`；
//   - 不脏时状态栏仍是原来那 7 项（既有契约不被这条新增拖下水）。

import (
	"testing"
	"time"

	"gioui.org/io/input"
	"gioui.org/io/pointer"
	"gioui.org/widget"

	"github.com/Tonyjh07/Aquarius/internal/i18n"
)

// histTreeUI 带可改树编辑状态的夹具（指针接收，测试中途可翻状态）。
func histTreeUI(t *testing.T, on bool, edits int) (*UI, *historyState, *fakeRelTree) {
	t.Helper()
	tree := &fakeRelTree{g: relFixtureGraph(), editOn: on, editEdits: edits}
	u := newHistUI(tree, fakeLister(nil))
	return u, newHistoryState(u), tree
}

// TestHistTreeSwitchMirrorsKernelState 开关不自己记状态：端口说开就开、说关就关。
func TestHistTreeSwitchMirrorsKernelState(t *testing.T) {
	u, st, tree := histTreeUI(t, false, 0)
	q := new(input.Router)
	histFrameFor(q, u, st)
	if st.teOn {
		t.Fatal("开关初始应为关（端口说不在模式里）")
	}
	tree.editOn, tree.editEdits = true, 3
	histFrameFor(q, u, st)
	if !st.teOn || st.teEdits != 3 {
		t.Fatalf("端口翻成开/3 处后状态 = %v/%d", st.teOn, st.teEdits)
	}
	tree.editOn, tree.editEdits = false, 0
	histFrameFor(q, u, st)
	if st.teOn || st.teEdits != 0 {
		t.Fatalf("端口翻回关后状态 = %v/%d", st.teOn, st.teEdits)
	}
}

// TestHistTreeSwitchSendsCommands 点开关/放弃各投一条命令（壳内不改结构，铁律 5）。
func TestHistTreeSwitchSendsCommands(t *testing.T) {
	u, st, tree := histTreeUI(t, false, 0)
	q := new(input.Router)
	histFrameFor(q, u, st)
	drainCh(u)

	// 关 → 开：投 /tree-edit（无参）。
	x, y := histBottomBtnXY(t, q, u, st, &st.btnTree, "树编辑开关")
	drainCh(u)
	ovClick(q, x, y)
	histFrameFor(q, u, st)
	if in := <-u.inCh; in.Command == nil || in.Command.Name != "tree-edit" || len(in.Command.Args) != 0 {
		t.Fatalf("开开关投出 %+v, want /tree-edit（无参）", in.Command)
	}

	// 开 → 关：投 --exit（有改动时的三选一由内核问，UI 不代劳）。
	tree.editOn, tree.editEdits = true, 2
	histFrameFor(q, u, st)
	drainCh(u)
	x, y = histBottomBtnXY(t, q, u, st, &st.btnTree, "树编辑开关")
	drainCh(u)
	ovClick(q, x, y)
	histFrameFor(q, u, st)
	if in := <-u.inCh; in.Command == nil || in.Command.Name != "tree-edit" ||
		len(in.Command.Args) != 1 || in.Command.Args[0] != "--exit" {
		t.Fatalf("关开关投出 %+v, want /tree-edit --exit", in.Command)
	}

	// 「放弃」：投 --cancel（放弃全部改动并退出）。
	drainCh(u)
	x, y = histBottomBtnXY(t, q, u, st, &st.btnTreeDiscard, "放弃")
	drainCh(u)
	ovClick(q, x, y)
	histFrameFor(q, u, st)
	if in := <-u.inCh; in.Command == nil || in.Command.Name != "tree-edit" ||
		len(in.Command.Args) != 1 || in.Command.Args[0] != "--cancel" {
		t.Fatalf("放弃投出 %+v, want /tree-edit --cancel", in.Command)
	}
}

// TestHistTreeDirtyItems 脏时才多出两项：不脏仍是原来那 7 项（D159 契约不变）。
func TestHistTreeDirtyItems(t *testing.T) {
	u, st, tree := histTreeUI(t, true, 0)
	q := new(input.Router)
	histFrameFor(q, u, st)
	if got := len(st.histStatusCtlItems()); got != 7 {
		t.Fatalf("在模式里但没改过：项数 = %d, want 7", got)
	}

	tree.editEdits = 3
	histFrameFor(q, u, st)
	items := st.histStatusCtlItems()
	if len(items) != 9 {
		t.Fatalf("脏时项数 = %d, want 9", len(items))
	}
	if items[7].label != i18n.Tf("ui.history.edits_count", "n", 3) || items[7].click != nil {
		t.Fatalf("第 8 项 = %+v, want 只读「已改 3 处」", items[7])
	}
	if items[8].label != i18n.T("ui.history.discard") || items[8].click == nil {
		t.Fatalf("第 9 项 = %+v, want 可点「放弃」", items[8])
	}

	// 退出模式后脏标记与「放弃」一并消失（模式外的处数没有意义）。
	tree.editOn, tree.editEdits = false, 3
	histFrameFor(q, u, st)
	if got := len(st.histStatusCtlItems()); got != 7 {
		t.Fatalf("退出模式后项数 = %d, want 7", got)
	}
}

// TestRelTweenRunsToTarget 补间本体（纯时间函数，可测）：起点不动、中途在两端之间、
// 到点落定并停表；同目标重复布防不重置起点（否则每帧重起、永远走不到头）。
func TestRelTweenRunsToTarget(t *testing.T) {
	var a relTween
	t0 := time.Now()
	a.retarget(t0, 1, 160, relEaseOutCubic)
	if v := a.at(t0); v != 0 {
		t.Fatalf("起点值 = %v, want 0", v)
	}
	mid := a.at(t0.Add(80 * time.Millisecond))
	if mid <= 0 || mid >= 1 {
		t.Fatalf("半程值 = %v，应严格在 (0,1) 之间", mid)
	}
	if relEaseOutCubic(0.5) < 0.8 { // easeOutCubic 过半已走完 87.5%（「起步快」）
		t.Fatalf("easeOutCubic(0.5) = %v, want ≥0.8", relEaseOutCubic(0.5))
	}
	// 飞行中重复布防同一目标：不得重置起点。
	start := a.start
	a.retarget(t0.Add(100*time.Millisecond), 1, 160, relEaseOutCubic)
	if !a.start.Equal(start) {
		t.Fatal("同目标重复布防重置了起点")
	}
	if v := a.at(t0.Add(160 * time.Millisecond)); v != 1 || a.active() {
		t.Fatalf("到点值 = %v（running=%v）, want 1/停表", v, a.active())
	}
}

// histBottomBtnXY 扫出状态栏里某个点击件的坐标（判据 = Hovered；按 3px 步长扫，
// 命中后再回退到该点——开关只有 44×24dp，不用全窗逐像素扫）。
func histBottomBtnXY(t *testing.T, q *input.Router, u *UI, st *historyState,
	c *widget.Clickable, what string) (int, int) {
	t.Helper()
	// 扫描区要容得下**语言差**：en 的按钮名更长，状态栏右端的这一串按钮整体左移
	// （实测开编辑后「树编辑开关」已落到 x<600），只扫最右一小条会在 en 下扫空。
	for y := 480; y < 560; y += 3 {
		for x := 330; x < 716; x += 3 {
			q.Queue(ovPointer(pointer.Move, x, y))
			histFrameFor(q, u, st)
			if c.Hovered() {
				return x, y
			}
		}
	}
	t.Fatalf("状态栏里扫不到%s", what)
	return 0, 0
}
