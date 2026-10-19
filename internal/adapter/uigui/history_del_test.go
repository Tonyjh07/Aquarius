package uigui

// 会话行悬浮删除 + 跨窗同步二次确认弹窗（D157）测试：
//   - 左栏行尾「✕」**仅悬浮该行时显示**（真指针事件驱动，几何靠扫描而非硬编码排版常数）；
//   - 点「✕」投出的是 `/rmconv <全 ID>`（不是 `/switch`——一次点击会同时命中行，靠顺序定夺）；
//   - 确认弹窗：投影发布 → 在屏；点「允许」→ 应答回主窗（带快照身份）；
//   - 身份校验：迟到的应答不得落到刚新起的下一条确认上；
//   - 模态：弹窗在屏时背景（行 / ✕）一律不取事件。

import (
	"testing"

	"gioui.org/io/input"
	"gioui.org/io/pointer"

	"github.com/Tonyjh07/Aquarius/internal/port"
)

// delFixture 左栏两行会话的历史窗夹具（帧级 headless + 真实指针路由）。
func delFixture(t *testing.T) (*UI, *historyState, *input.Router) {
	t.Helper()
	list := fakeLister{
		{ID: "C1", Title: "甲", Messages: 3, Current: true},
		{ID: "C2", Title: "乙", Messages: 5},
	}
	u := newHistUI(fakeRelTree{g: relFixtureGraph()}, list)
	st := newHistoryState(u)
	q := new(input.Router)
	histFrameFor(q, u, st) // 首帧：登记行命中区
	return u, st, q
}

// delRowY 扫出第 i 行在窗口里的**纵向中心**（**不硬编码排版常数**：左栏头部标签高度取决于
// 字体度量，写死会在换字体后静默漂移）。判据 = 该行「✕」按悬浮门显形（st.delShown 是生产
// 判据本身，不是替身）。左栏中部 x 必落在行命中区里、避开 ✕ 槽。
func delRowY(t *testing.T, q *input.Router, u *UI, st *historyState, i int) int {
	t.Helper()
	const x = 100
	first, last := -1, -1
	for y := 1; y < 400; y++ {
		q.Queue(ovPointer(pointer.Move, x, y))
		histFrameFor(q, u, st)
		shown := i < len(st.delShown) && st.delShown[i]
		if shown {
			if first < 0 {
				first = y
			}
			last = y
			continue
		}
		if first >= 0 {
			break // 出了这一行的行带
		}
	}
	if first < 0 {
		t.Fatalf("扫不到第 %d 行的行带（左栏排版变了？）", i)
	}
	return (first + last) / 2
}

// delX 在第 y 行带里扫出「✕」的横坐标（判据 = ✕ 自身 hover：只有压在它的命中区上才真）。
// 每点两帧：hover 的进入事件在下一帧才被点击件消费。
func delX(t *testing.T, q *input.Router, u *UI, st *historyState, y int) int {
	t.Helper()
	for x := 190; x > 90; x -= 2 {
		q.Queue(ovPointer(pointer.Move, x, y))
		histFrameFor(q, u, st)
		histFrameFor(q, u, st)
		if len(st.dels) > 0 && st.dels[0].Hovered() {
			return x
		}
	}
	t.Fatalf("第 %d 行扫不到「✕」（行尾槽未登记命中区？）", y)
	return 0
}

// TestHistoryRowDelHoverReveal ✕ 只在指针悬浮该行时显示：不在行上 → 全不显示；
// 移到第 0 行 → 只有第 0 行显示；移到第 1 行 → 只有第 1 行显示。
func TestHistoryRowDelHoverReveal(t *testing.T) {
	u, st, q := delFixture(t)

	// 指针落在左栏内衬里（不在任何行上）。
	q.Queue(ovPointer(pointer.Move, 100, 2))
	histFrameFor(q, u, st)
	for i, shown := range st.delShown {
		if shown {
			t.Fatalf("第 %d 行未悬浮却显示「✕」", i)
		}
	}

	y0 := delRowY(t, q, u, st, 0)
	q.Queue(ovPointer(pointer.Move, 100, y0)) // 回到第 0 行中心（扫描结束时指针停在下一行的行带上）
	histFrameFor(q, u, st)
	if len(st.delShown) < 2 || !st.delShown[0] || st.delShown[1] {
		t.Fatalf("指针在第 0 行（y=%d）时「✕」显隐 = %v, want 仅第 0 行", y0, st.delShown)
	}
	y1 := delRowY(t, q, u, st, 1)
	if y1 <= y0 {
		t.Fatalf("第 1 行（y=%d）应在第 0 行（y=%d）下方", y1, y0)
	}
	q.Queue(ovPointer(pointer.Move, 100, y1))
	histFrameFor(q, u, st)
	if !st.delShown[1] || st.delShown[0] {
		t.Fatalf("指针在第 1 行（y=%d）时「✕」显隐 = %v, want 仅第 1 行", y1, st.delShown)
	}

	// 回到内衬：✕ 收回。
	q.Queue(ovPointer(pointer.Move, 100, 2))
	histFrameFor(q, u, st)
	for i, shown := range st.delShown {
		if shown {
			t.Fatalf("指针离开行后第 %d 行仍显示「✕」", i)
		}
	}
}

// TestHistoryRowDelDispatchesRmconv 点行尾「✕」→ `/rmconv <全 ID>`；且**不**同时投 /switch
// （Gio 把一次点击投给所有命中 handler，而 ✕ 画在行命中区之内）。
func TestHistoryRowDelDispatchesRmconv(t *testing.T) {
	u, st, q := delFixture(t)
	y := delRowY(t, q, u, st, 0)
	x := delX(t, q, u, st, y)

	// 真点击：预热一帧（✕ 已显形并登记），再 Move+Press+Release。
	q.Queue(ovPointer(pointer.Move, x, y))
	histFrameFor(q, u, st)
	ovClick(q, x, y)
	histFrameFor(q, u, st)

	select {
	case in := <-u.inCh:
		if in.Command == nil || in.Command.Name != "rmconv" ||
			len(in.Command.Args) != 1 || in.Command.Args[0] != "C1" {
			t.Fatalf("点「✕」投递 = %+v, want /rmconv C1", in)
		}
	default:
		t.Fatal("点「✕」未投递任何命令")
	}
	select {
	case extra := <-u.inCh:
		t.Fatalf("点「✕」多投了一条（应与切换会话二取一）：%+v", extra)
	default:
	}
}

// TestHistoryRowDelModalBlocksBackground 确认弹窗在屏时，行与「✕」都不取事件（模态）。
func TestHistoryRowDelModalBlocksBackground(t *testing.T) {
	u, st, q := delFixture(t)
	y := delRowY(t, q, u, st, 0)
	x := delX(t, q, u, st, y)

	u.publishConfirm(&confirmShare{prompt: "确认删除会话？"})
	if !st.confirmActive(u) {
		t.Fatal("确认投影发布后弹窗未在屏")
	}
	q.Queue(ovPointer(pointer.Move, x, y))
	histFrameFor(q, u, st)
	ovClick(q, x, y) // 点「✕」位置
	histFrameFor(q, u, st)
	ovClick(q, 100, y) // 点行中部
	histFrameFor(q, u, st)
	select {
	case in := <-u.inCh:
		t.Fatalf("弹窗在屏时背景仍取到事件：%+v", in)
	default:
	}
}

// TestHistoryConfirmDialogAnswers 跨窗应答：投影发布 → 弹窗在屏；点「允许」→ 应答带快照身份
// 投回主窗事件循环 → 同一 replyConfirm 送达 runner，且投影撤下。
func TestHistoryConfirmDialogAnswers(t *testing.T) {
	u, st, q := delFixture(t)
	reply := make(chan port.ConfirmAnswer, 1)
	u.m.startConfirm("确认删除会话 C1？", reply) // 与主窗同路：startConfirm 即发布投影
	if !st.confirmActive(u) {
		t.Fatal("startConfirm 未发布确认投影")
	}
	histFrameFor(q, u, st) // 画一帧弹窗（不 panic）

	st.cfm.allow.Click()
	histFrameFor(q, u, st)
	select {
	case msg := <-u.inbox:
		if !u.apply(msg) {
			t.Fatal("事件循环应继续")
		}
	default:
		t.Fatal("弹窗未把应答投回主窗")
	}
	select {
	case ans := <-reply:
		if !ans.Allow {
			t.Fatal("「允许」应答丢失")
		}
	default:
		t.Fatal("应答未送达 runner")
	}
	if u.m.confirm != nil || st.confirmActive(u) {
		t.Fatal("应答后确认态与投影都应撤下")
	}
}

// TestHistoryConfirmAnswerIdentity 快照身份即「哪一次确认」：迟到的应答不得落到刚新起的
// 下一条确认上；身份相符的应答照常生效。
func TestHistoryConfirmAnswerIdentity(t *testing.T) {
	u, _, _ := delFixture(t)
	m := u.m

	replyA := make(chan port.ConfirmAnswer, 1)
	m.startConfirm("第一条？", replyA)
	shareA := u.confirmShare.Load()
	if shareA == nil {
		t.Fatal("第一条未发布投影")
	}
	m.replyConfirm(port.ConfirmAnswer{Allow: true})
	if u.confirmShare.Load() != nil {
		t.Fatal("收尾未撤下投影")
	}
	<-replyA

	replyB := make(chan port.ConfirmAnswer, 1)
	m.startConfirm("第二条？", replyB)
	u.apply(confirmResultMsg{ans: port.ConfirmAnswer{Allow: true}, share: shareA})
	if m.confirm == nil {
		t.Fatal("迟到应答误答了下一次确认")
	}
	select {
	case <-replyB:
		t.Fatal("迟到应答被采纳")
	default:
	}

	u.apply(confirmResultMsg{ans: port.ConfirmAnswer{Allow: true}, share: u.confirmShare.Load()})
	if m.confirm != nil {
		t.Fatal("同身份应答未生效")
	}
	select {
	case ans := <-replyB:
		if !ans.Allow {
			t.Fatal("同身份应答丢失")
		}
	default:
		t.Fatal("同身份应答未送达 runner")
	}
}

// TestHistoryConfirmDialogRefuses 点「拒绝」→ 拒绝应答（Allow = false）同样送达，且不阻塞。
func TestHistoryConfirmDialogRefuses(t *testing.T) {
	u, st, q := delFixture(t)
	reply := make(chan port.ConfirmAnswer, 1)
	u.m.startConfirm("确认删除会话 C1？", reply)
	histFrameFor(q, u, st)

	st.cfm.deny.Click()
	histFrameFor(q, u, st)
	select {
	case msg := <-u.inbox:
		u.apply(msg)
	default:
		t.Fatal("弹窗未把应答投回主窗")
	}
	select {
	case ans := <-reply:
		if ans.Allow {
			t.Fatal("「拒绝」应答丢了 Allow=false")
		}
	default:
		t.Fatal("应答未送达 runner")
	}
	if u.m.confirm != nil {
		t.Fatal("应答后确认态应收尾")
	}
}
