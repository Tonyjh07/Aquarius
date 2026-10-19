package uigui

// history_summary_test.go 「插入摘要」小卡（D152）测试：
//   - 图侧节点右键 → 菜单项「插入摘要」 → 动作槽 → 本窗开卡（会话根 / 首节点人格不出这一项）；
//   - 两个按钮 = 两种来源：手写档投 `/summary <id> <文本>`、模型档投 `/summary <id>`
//     （无文本，且忽略卡里已写的正文）；
//   - 空正文时「插入」不提交、卡保持开启（不丢用户刚敲的字）；
//   - Esc 与「取消」关闭；开卡期间背景（右图手势）不取事件；两层模态不共存。

import (
	"testing"

	"gioui.org/f32"
	"gioui.org/io/input"
	"gioui.org/io/key"
	"gioui.org/io/pointer"
	"gioui.org/layout"

	"github.com/Tonyjh07/Aquarius/internal/adapter/uigui/platform"
	"github.com/Tonyjh07/Aquarius/internal/domain/conversation"
)

// sumOpenCard 直接开卡并画一帧（与动作槽开卡同一条生产路径）。
func sumOpenCard(t *testing.T, u *UI, st *historyState, q *input.Router, id conversation.MessageID) {
	t.Helper()
	st.openSummary(u, id)
	histFrameFor(q, u, st)
	if st.sum == nil {
		t.Fatalf("节点 %s 未开卡", id)
	}
	if st.sum.id != id {
		t.Fatalf("卡目标 = %s, want %s", st.sum.id, id)
	}
}

// sumDrainCmd 取一条命令（无则报错）。
func sumDrainCmd(t *testing.T, u *UI) *portCommand {
	t.Helper()
	select {
	case in := <-u.inCh:
		if in.Command == nil {
			t.Fatalf("投递的不是命令: %+v", in)
		}
		return &portCommand{name: in.Command.Name, args: in.Command.Args}
	default:
		t.Fatal("未投出任何命令")
	}
	return nil
}

// portCommand 断言用的命令快照。
type portCommand struct {
	name string
	args []string
}

// TestSummaryCardOpensFromNodeMenu 图侧右键 → 「插入摘要」→ 动作槽 → 帧首开卡；
// 卡在屏时 modalActive 成立（背景手势不再取事件）。
func TestSummaryCardOpensFromNodeMenu(t *testing.T) {
	u, st, q := relGraphFixture(t)
	const id conversation.MessageID = "a1"

	li := relNodeIndex(t, st, id)
	q.Queue(relRightPress(st, li))
	q.Queue(relRightReleaseAt(st, li))
	relGFrame(q, u, st)
	u.menuChosen(platform.MenuBubble, cmdNodeSummary)
	histFrameFor(q, u, st) // 帧首排空动作槽 → 开卡并画一帧

	if st.sum == nil || st.sum.id != id {
		t.Fatalf("卡 = %+v, want 目标 %s", st.sum, id)
	}
	if !st.modalActive(u) {
		t.Fatal("卡在屏却不算模态（背景会照常取事件）")
	}
	if u.nodeAction.Load() != nil {
		t.Fatal("动作槽未排空（会在下一帧重复开卡）")
	}
}

// TestSummaryHandwrittenDispatches 手写档：卡里写正文 + 点「插入」→ `/summary <id> <文本>`
// 经 inCh、关卡、唤出主窗。
func TestSummaryHandwrittenDispatches(t *testing.T) {
	u, st, q := relGraphFixture(t)
	const id conversation.MessageID = "a1"
	sumOpenCard(t, u, st, q, id)
	st.sum.ed.SetText("手写摘要\n第二行")

	st.sum.insert.Click()
	histFrameFor(q, u, st)

	cmd := sumDrainCmd(t, u)
	if cmd.name != "summary" {
		t.Fatalf("命令 = %s, want summary", cmd.name)
	}
	if len(cmd.args) != 2 || cmd.args[0] != string(id) || cmd.args[1] != "手写摘要\n第二行" {
		t.Fatalf("Args = %v, want [%s 文本]", cmd.args, id)
	}
	if st.sum != nil {
		t.Fatal("提交后卡应关闭")
	}
	var se showExpandMsg
	if !relDrainAs(t, u, &se) {
		t.Fatal("提交后未唤出主窗（结果看不见）")
	}
}

// TestSummaryModelIgnoresTypedText 模型档：点「用模型总结以上历史」→ `/summary <id>`（无文本），
// 即便卡里已经写了字也不带出去（两种来源二选一）。
func TestSummaryModelIgnoresTypedText(t *testing.T) {
	u, st, q := relGraphFixture(t)
	const id conversation.MessageID = "a1"
	sumOpenCard(t, u, st, q, id)
	st.sum.ed.SetText("写了但改用模型")

	st.sum.model.Click()
	histFrameFor(q, u, st)

	cmd := sumDrainCmd(t, u)
	if cmd.name != "summary" || len(cmd.args) != 1 || cmd.args[0] != string(id) {
		t.Fatalf("命令 = %s %v, want /summary %s（无文本）", cmd.name, cmd.args, id)
	}
	if st.sum != nil {
		t.Fatal("提交后卡应关闭")
	}
}

// TestSummaryEmptyTextKeepsCard 空（或纯空白）正文点「插入」：不投命令、卡保持开启，
// 用户刚敲的字不丢。
func TestSummaryEmptyTextKeepsCard(t *testing.T) {
	u, st, q := relGraphFixture(t)
	sumOpenCard(t, u, st, q, "a1")
	st.sum.ed.SetText("   \n  ")

	st.sum.insert.Click()
	histFrameFor(q, u, st)

	select {
	case in := <-u.inCh:
		t.Fatalf("空正文不应提交: %+v", in)
	default:
	}
	if st.sum == nil {
		t.Fatal("空正文提交（应为空操作）不应关卡")
	}
	if st.sum.ed.Text() != "   \n  " {
		t.Fatalf("草稿被清 = %q", st.sum.ed.Text())
	}
}

// TestSummaryEscAndCancelClose Esc 与「取消」都关卡、都不投命令（卡里可能刚敲了字，
// 故不点空白关闭——由 TestSummaryNoOutsideClickClose 单独钉）。
func TestSummaryEscAndCancelClose(t *testing.T) {
	u, st, q := relGraphFixture(t)
	sumOpenCard(t, u, st, q, "a1")
	histFrameFor(q, u, st) // 焦点立起来（FocusCmd 在帧尾生效）
	q.Queue(key.Event{Name: key.NameEscape, State: key.Press})
	histFrameFor(q, u, st)
	if st.sum != nil {
		t.Fatal("Esc 未关卡")
	}

	sumOpenCard(t, u, st, q, "a1")
	st.sum.ed.SetText("写了一半")
	st.sum.cancel.Click()
	histFrameFor(q, u, st)
	if st.sum != nil {
		t.Fatal("「取消」未关卡")
	}
	select {
	case in := <-u.inCh:
		t.Fatalf("关闭动作不应投命令: %+v", in)
	default:
	}
}

// TestSummaryNoOutsideClickClose 点卡片外空白**不**关卡（正文可能刚写一半，误触即丢字）。
func TestSummaryNoOutsideClickClose(t *testing.T) {
	u, st, q := relGraphFixture(t)
	sumOpenCard(t, u, st, q, "a1")
	st.sum.ed.SetText("写了一半")

	ovClick(q, 2, 2) // 视口角落（卡片外）
	histFrameFor(q, u, st)
	if st.sum == nil {
		t.Fatal("点空白不应关卡（会把刚写的正文丢掉）")
	}
}

// TestSummaryModalBlocksGraph 卡在屏时右图手势失灵：点节点不再开消息遮罩、也不投命令
// （模态在 Gio 广播模型下的真实口径）；关卡后同一位置恢复响应。
func TestSummaryModalBlocksGraph(t *testing.T) {
	u, st, q := relGraphFixture(t)
	sumOpenCard(t, u, st, q, "a1")
	histFrameFor(q, u, st) // 图按真实面板尺寸就位

	li, sx := -1, f32.Point{}
	for i, gi := range st.graph.vis.nodes {
		x, y := st.graph.toScreen(st.graph.sol.Pos[gi])
		if st.graph.hitTest(x, y) == i && st.model.nodes[gi].id != "a1" {
			li, sx = i, f32.Pt(x, y)
			break
		}
	}
	if li < 0 {
		t.Skip("夹具里没有另一个可命中的节点")
	}
	wx, wy := int(sx.X)+200, int(sx.Y) // 右图栏坐标 → 窗口坐标（左栏 200dp）
	q.Queue(ovPointer(pointer.Move, wx, wy))
	histFrameFor(q, u, st)
	ovClick(q, wx, wy)
	histFrameFor(q, u, st)
	if st.ov != nil {
		t.Fatal("卡在屏时点节点仍开了遮罩（背景未被遮住）")
	}
	select {
	case in := <-u.inCh:
		t.Fatalf("卡在屏时背景仍取到事件: %+v", in)
	default:
	}
	if st.sum == nil {
		t.Fatal("背景点击不应关卡")
	}

	// 关卡后同一位置恢复响应：点节点开遮罩（D139 的单击语义）。
	st.closeSummary()
	histFrameFor(q, u, st)
	q.Queue(ovPointer(pointer.Move, wx, wy))
	histFrameFor(q, u, st)
	ovClick(q, wx, wy)
	histFrameFor(q, u, st)
	if st.ov == nil {
		t.Fatal("关卡后点节点未开遮罩（模态门没放开）")
	}
}

// TestSummaryRefusesRootAndPersona 会话根与会话首节点（人格）不开卡：根前无处可插；
// 人格之前插摘要会让摘要顶到路径第二位、被装配当成 persona。
func TestSummaryRefusesRootAndPersona(t *testing.T) {
	u, st, q := relGraphFixture(t)
	for _, id := range []conversation.MessageID{"r", "p"} {
		st.openSummary(u, id)
		histFrameFor(q, u, st)
		if st.sum != nil {
			t.Fatalf("节点 %s 不该开卡（%+v）", id, st.sum)
		}
	}
}

// TestSummaryModalLayersExclusive 两层模态不共存：开卡收遮罩、开遮罩收卡。
func TestSummaryModalLayersExclusive(t *testing.T) {
	u, st, q := relGraphFixture(t)
	histOpenOverlay(t, u, st, ovUserID(st), conversation.Fresh, overlayPageRendered)
	if st.ov == nil {
		t.Fatal("遮罩未开")
	}
	sumOpenCard(t, u, st, q, "a1")
	if st.ov != nil {
		t.Fatal("开卡未收遮罩")
	}
	st.openOverlay(u, ovUserID(st), conversation.Fresh, overlayPageRendered)
	if st.sum != nil {
		t.Fatal("开遮罩未收卡")
	}
}

// TestSummaryEditorIsMultiline 正文编辑区是多行编辑器（Enter 落换行、不提交）：
// 摘要是多行 markdown，单行编辑器会把正文压平。
func TestSummaryEditorIsMultiline(t *testing.T) {
	u, st, q := relGraphFixture(t)
	sumOpenCard(t, u, st, q, "a1")
	if st.sum.ed.SingleLine {
		t.Fatal("摘要编辑器应为多行（SingleLine=false）")
	}
	if st.sum.ed.Submit {
		t.Fatal("Enter 应落换行而非提交（提交靠按钮）")
	}
	if got := st.sum.list.List.Axis; got != layout.Vertical {
		t.Fatalf("正文区滚动轴 = %v, want 竖向（layout.List 零值是横向）", got)
	}
}
