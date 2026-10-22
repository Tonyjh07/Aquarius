package uigui

// relmap_frame_test.go 右图自绘/手势/菜单（D120⑦）的共享夹具：headless 离屏帧 +
// input.Router 事件回放（§15.5，零网络零窗口）。用例按主题分在同目录的
// relmap_render_test.go（图形与预算）、relmap_view_test.go（取景与命中）、
// relmap_settle_test.go（收敛与视场平衡）、relmap_menu_test.go（右键菜单）、
// relmap_drag_test.go（拖拽与物理）。

import (
	"testing"
	"time"

	"gioui.org/f32"
	"gioui.org/io/input"
	"gioui.org/io/pointer"

	"github.com/Tonyjh07/Aquarius/internal/domain/conversation"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

// relGraphFixture 右图测试夹具：固定图 + 全预算模型（本地索引 = 模型索引；锚点 u2 = 6）。
// 首帧后把物理跑完（从折叠播种展开至静止）**并等视口跟随收敛**（D128 起视口跟随本身
// 是一段有界滞后的动画，只等 lay.settled 会拿到差几十 px 的半截视场）——交互测试需要
// 一个**已收敛的静态基线**。
func relGraphFixture(t *testing.T) (*UI, *historyState, *input.Router) {
	t.Helper()
	texts := make(map[conversation.MessageID]string)
	for _, n := range relFixtureGraph().Nodes {
		texts[n.ID] = relFixtureBody(n.ID)
	}
	u := newHistUI(fakeRelTree{g: relFixtureGraph(), texts: texts}, fakeLister(nil))
	st := newHistoryState(u)
	q := new(input.Router)
	relGFrame(q, u, st)
	relDriveQuiet(q, u, st, relSettleFrames*3)
	if st.graph.lay == nil || !st.graph.lay.settled {
		t.Fatal("夹具物理未收敛")
	}
	return u, st, q
}

// relDriveQuiet 驱动到「物理静止 **且** 视口跟随收敛」（D128）。fitFollow 未收敛时继续
// 驱动——app 侧由 tickLayout 续帧对应（见 tickLayout 的 fitFollow 分支）。
func relDriveQuiet(q *input.Router, u *UI, st *historyState, max int) {
	for i := 0; i < max; i++ {
		relGFrame(q, u, st)
		if st.graph.lay != nil && st.graph.lay.settled && !st.graph.fitFollow {
			return
		}
	}
}

// relGFrame 一帧右图（500×500 视口）+ 提交 hit 树。
func relGFrame(q *input.Router, u *UI, st *historyState) {
	gtx, ops := frameGtxSize(q.Source(), 500, 500)
	st.graph.frame(gtx, u.th, u, st)
	q.Frame(ops)
}

// relPointer 鼠标事件（ID=7；primary = 按压中）。
func relPointer(kind pointer.Kind, p f32.Point, primary bool) pointer.Event {
	e := pointer.Event{Kind: kind, Position: p, PointerID: 7, Source: pointer.Mouse}
	if primary {
		e.Buttons = pointer.ButtonPrimary
	}
	return e
}

// relLongSnippetGraph 长摘要会话树（D126 多行卡片回归用）：摘要 40 字程 CJK，逼出多行
// 标签；a1 既是锚点（w3 最大徽标）又是分叉位点（版本下标 "1" + 虚线外环）。
func relLongSnippetGraph() port.TreeGraph {
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	at := func(i int) time.Time { return base.Add(time.Duration(i) * time.Second) }
	const long = "这是一段比较长的消息摘要文字用来验证多行卡片与虚线外环的绘制是否对齐徽标中心"
	mk := func(id, parent string, role conversation.Role, sibIdx, sibCount int) port.GraphNode {
		return port.GraphNode{
			ID: conversation.MessageID(id), Parent: conversation.MessageID(parent),
			Role: role, Snippet: long, EdgeKind: port.EdgeSeq,
			SiblingIdx: sibIdx, SiblingCount: sibCount,
		}
	}
	nodes := []port.GraphNode{
		mk("r", "", conversation.RoleRoot, 0, 1),
		mk("p", "r", conversation.RoleSystem, 0, 1),
		mk("u1", "p", conversation.RoleUser, 0, 2),
		mk("u1b", "p", conversation.RoleUser, 1, 2),
		mk("a1", "u1", conversation.RoleAssistant, 0, 2), // 锚点 + 分叉位点
		mk("u2", "a1", conversation.RoleUser, 0, 1),
	}
	for i := range nodes {
		nodes[i].CreatedAt = at(i)
	}
	return port.TreeGraph{Anchor: "a1", Nodes: nodes, Total: len(nodes)}
}
