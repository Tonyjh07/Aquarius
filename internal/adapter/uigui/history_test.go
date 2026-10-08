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
type fakeRelTree struct{ g port.TreeGraph }

func (f fakeRelTree) Branches(conversation.MessageID) (port.BranchInfo, bool) {
	return port.BranchInfo{}, false
}
func (f fakeRelTree) Tail(id conversation.MessageID) (conversation.MessageID, bool) {
	return id, true
}
func (f fakeRelTree) Graph() (port.TreeGraph, bool) { return f.g, true }

// fakeLister 固定会话列表的 SessionLister 替身。
type fakeLister []port.SessionSummary

func (f fakeLister) List() []port.SessionSummary { return f }

// newHistUI 最小历史窗测试 UI（帧级 headless + 断言 inCh）。
func newHistUI(tree port.TreeView, list port.SessionLister) *UI {
	u := newFrameUI()
	u.opts.Tree = tree
	u.opts.Lister = list
	u.inCh = make(chan port.UserInput, 8)
	return u
}

// TestHistorySyncModelAndSessions 左栏与右图数据面同步：模型/预算建立、会话行数对齐。
func TestHistorySyncModelAndSessions(t *testing.T) {
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

// TestHistorySigRebuild 快照签名变化才重建（R3 懒重排）：末节点变化 → 模型重算。
func TestHistorySigRebuild(t *testing.T) {
	g := relFixtureGraph()
	u := newHistUI(fakeRelTree{g: g}, fakeLister(nil))
	st := newHistoryState(u)
	if st.model.total != 7 {
		t.Fatalf("初始 total = %d, want 7", st.model.total)
	}
	old := st.sig

	// 追加一节点（Total 8）→ 签名变化 → 重建。
	g2 := relFixtureGraph()
	g2.Nodes = append(g2.Nodes, port.GraphNode{
		ID: "u3", Parent: "u2", Role: conversation.RoleUser, Snippet: "x",
		CreatedAt: g2.Nodes[len(g2.Nodes)-1].CreatedAt.Add(1), EdgeKind: port.EdgeSeq,
	})
	g2.Total = len(g2.Nodes)
	u.opts.Tree = fakeRelTree{g: g2}
	st.sync(u)
	if st.sig == old {
		t.Fatal("签名未变化")
	}
	if st.model.total != 8 {
		t.Fatalf("重建后 total = %d, want 8", st.model.total)
	}
}

// TestHistoryBudgetClampsToHardCap 预算上限钳到 relHardCap（§5.4.3）。
func TestHistoryBudgetClampsToHardCap(t *testing.T) {
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
