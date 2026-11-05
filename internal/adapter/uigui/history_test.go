package uigui

// 会话历史次窗（D112/S3）第 6 步测试：左栏数据面同步、状态栏 N/M 分源（A7）、
// 点会话行 → /switch 投递、快照签名驱动重排。headless（§15.5：无窗口、离屏帧）。

import (
	"testing"

	"gioui.org/io/input"

	"github.com/Tonyjh07/Aquarius/internal/domain/conversation"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

// fakeRelTree 固定整树快照的 TreeView 替身（Graph 有值；Branches/Tail 用不上）。
// texts = D132 的 NodeText 数据源（按 id 给正文全文；未列出的 id = 无正文）。
// editOn/editEdits = D258 树编辑模式的只读状态（状态栏开关的数据源）。
type fakeRelTree struct {
	g         port.TreeGraph
	texts     map[conversation.MessageID]string
	editOn    bool
	editEdits int
}

func (f fakeRelTree) Branches(conversation.MessageID) (port.BranchInfo, bool) {
	return port.BranchInfo{}, false
}
func (f fakeRelTree) Tail(id conversation.MessageID) (conversation.MessageID, bool) {
	return id, true
}
func (f fakeRelTree) Graph() (port.TreeGraph, bool) { return f.g, true }
func (f fakeRelTree) NodeText(id conversation.MessageID) (string, bool) {
	t, ok := f.texts[id]
	return t, ok
}

// NodeAttachments D188：测试替身默认无附件（附件 chip 另有专项用例）。
func (f fakeRelTree) NodeAttachments(conversation.MessageID) []port.AttachmentView {
	return nil
}

// TreeEditState D258：替身按字段回放树编辑状态（默认不在模式里）。
func (f fakeRelTree) TreeEditState() (bool, int) { return f.editOn, f.editEdits }

// fakeLister 固定会话列表的 SessionLister 替身。
type fakeLister []port.SessionSummary

func (f fakeLister) List() []port.SessionSummary { return f }

// newHistUI 最小历史窗测试 UI（帧级 headless + 断言 inCh/inbox：菜单分发的 post 走 inbox，
// 图内命令走 inCh）。
func newHistUI(tree port.TreeView, list port.SessionLister) *UI {
	u := newFrameUI()
	u.opts.Tree = tree
	u.opts.Lister = list
	u.inCh = make(chan port.UserInput, 8)
	u.inbox = make(chan uiMsg, 8)
	return u
}

// TestHistorySyncModelAndSessions 左栏与右图数据面同步：模型/预算建立、会话行数对齐。
func TestHistorySyncModelAndSessions(t *testing.T) {
	t.Parallel()
	sum := func(id, title string, cur bool) port.SessionSummary {
		return port.SessionSummary{ID: conversation.ID(id), Title: title, Messages: 3, Current: cur}
	}
	list := fakeLister{sum("C1", "甲", true), sum("C2", "乙", false)}
	u := newHistUI(fakeRelTree{g: relFixtureGraph()}, list)
	st := newHistoryState(u)

	if st.model == nil || st.model.total != 7 {
		t.Fatalf("模型未建立或 total=%v, want 7", st.model.total)
	}
	if len(st.vis.nodes) != 7 { // 7 节点 ≤ relDefaultBudget → 全呈现
		t.Fatalf("呈现 = %d, want 7", len(st.vis.nodes))
	}
	if len(st.sessions) != 2 {
		t.Fatalf("左栏行数 = %d, want 2", len(st.sessions))
	}
	// 渲染整窗不 panic（headless 离屏帧）。
	var src input.Source
	gtx, _ := frameGtxSize(src, 720, 560)
	historyFrame(gtx, u.th, u, st)
}

// TestHistorySwitchDispatch 点会话行 → /switch <id> 经 inCh（与键入同路径）。
func TestHistorySwitchDispatch(t *testing.T) {
	t.Parallel()
	u := newHistUI(fakeRelTree{g: relFixtureGraph()}, fakeLister(nil))
	st := newHistoryState(u)
	st.sendSwitch(u, conversation.ID("C42"))
	select {
	case in := <-u.inCh:
		if in.Command == nil || in.Command.Name != "switch" ||
			len(in.Command.Args) != 1 || in.Command.Args[0] != "C42" {
			t.Fatalf("inCh = %+v, want /switch C42", in)
		}
	default:
		t.Fatal("inCh 空：未投递 /switch")
	}
}

// TestHistoryRevisionRebuild 发布号变了才重建（R3 懒重排）：同号不重建、换号才重算。
func TestHistoryRevisionRebuild(t *testing.T) {
	t.Parallel()
	g := relFixtureGraph()
	g.Revision = 1
	u := newHistUI(fakeRelTree{g: g}, fakeLister(nil))
	st := newHistoryState(u)
	if st.model.total != 7 {
		t.Fatalf("初始 total = %d, want 7", st.model.total)
	}
	model := st.model

	// 同号（内容据称未变）→ 模型原样保留，连指针都不换。
	st.sync(u)
	if st.model != model {
		t.Fatal("发布号未变却重建了模型")
	}

	// 追加一节点 → 发布号换新 → 重建。
	g2 := relFixtureGraph()
	g2.Revision = 2
	g2.Nodes = append(g2.Nodes, port.GraphNode{
		ID: "u3", Parent: "u2", Role: conversation.RoleUser, Snippet: "x",
		CreatedAt: g2.Nodes[len(g2.Nodes)-1].CreatedAt.Add(1), EdgeKind: port.EdgeSeq,
	})
	g2.Total = len(g2.Nodes)
	u.opts.Tree = fakeRelTree{g: g2}
	st.sync(u)
	if st.rev != 2 {
		t.Fatalf("rev = %d, want 2", st.rev)
	}
	if st.model.total != 8 {
		t.Fatalf("重建后 total = %d, want 8", st.model.total)
	}
}

// TestHistoryReparentRebuildsModel 改父必须重排（2026-10-31 实测报障）：把 a2 从 u1 挪到
// a1 下——节点数、锚点、末节点全没动，只有一条边换了父。若变更判定只看这三样，这一改就
// 落进缝里：模型不重建，松手后线仍连原父。
func TestHistoryReparentRebuildsModel(t *testing.T) {
	t.Parallel()
	g := relFixtureGraph()
	g.Revision = 1
	u := newHistUI(fakeRelTree{g: g}, fakeLister(nil))
	st := newHistoryState(u)
	if p, want := st.model.nodes[relIdx(t, st.model, "a2")].parent, relIdx(t, st.model, "u1"); p != want {
		t.Fatalf("初始 a2 的父 = %d, want %d", p, want)
	}

	moved := relFixtureGraph()
	moved.Revision = 2 // 内容变了 → 发布号换新（app 侧按内容判等决定）
	for i := range moved.Nodes {
		if moved.Nodes[i].ID == "a2" {
			moved.Nodes[i].Parent = "a1"
		}
	}
	u.opts.Tree = fakeRelTree{g: moved}
	st.sync(u)

	if p, want := st.model.nodes[relIdx(t, st.model, "a2")].parent, relIdx(t, st.model, "a1"); p != want {
		t.Fatalf("改父后 a2 的父 = %d, want %d（模型没重建，线仍连原父）", p, want)
	}
}

// TestHistoryBudgetClampsToHardCap 预算上限钳到 relHardCap（§5.4.3）。
func TestHistoryBudgetClampsToHardCap(t *testing.T) {
	t.Parallel()
	g := relFixtureGraph()
	u := newHistUI(fakeRelTree{g: g}, fakeLister(nil))
	st := newHistoryState(u)
	// relBudget 内部已钳：传超大预算仍 ≤ hardCap 且 ≥ 节点数时全呈现。
	big := relBudget(st.model, 1<<20)
	if len(big.nodes) > relHardCap {
		t.Fatalf("预算超 hardCap: %d", len(big.nodes))
	}
	if len(big.nodes) != st.model.total {
		t.Fatalf("全量预算 = %d, want %d", len(big.nodes), st.model.total)
	}
}
