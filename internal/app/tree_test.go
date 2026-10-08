package app

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/Tonyjh07/Aquarius/internal/domain/conversation"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

// treeFixture 建一棵显式 CreatedAt 的树，用于断言同父孩子的创建序与下标（D80/§7.5）：
//
//	convT(root) → p(persona) → u1(user) ─┬─ a1(assistant, t+2s)
//	                                     └─ a2(assistant, t+3s)
//
// a1/a2 为同一父 u1 的同级分叉。
func treeFixture(t *testing.T) *conversation.Conversation {
	t.Helper()
	c := conversation.New(conversation.ID("convT"), "分支")
	txt := func(s string) []conversation.Part {
		return []conversation.Part{{Kind: conversation.PartText, Text: s}}
	}
	root := conversation.MessageID(c.ID)
	mk := func(id string, parent conversation.MessageID, role conversation.Role, at time.Time) {
		t.Helper()
		if err := c.AppendCommitted(conversation.Message{
			ID: conversation.MessageID(id), Parent: parent, Role: role,
			Content: txt(id), CreatedAt: at,
		}); err != nil {
			t.Fatalf("commit %s: %v", id, err)
		}
	}
	mk("p", root, conversation.RoleSystem, testTime)
	mk("u1", "p", conversation.RoleUser, testTime.Add(1*time.Second))
	mk("a1", "u1", conversation.RoleAssistant, testTime.Add(2*time.Second))
	mk("a2", "u1", conversation.RoleAssistant, testTime.Add(3*time.Second))
	if err := c.Validate(); err != nil {
		t.Fatalf("fixture validate: %v", err)
	}
	return c
}

// TestMakeTreeSnapshotBranches 快照对每个节点给出「同父孩子创建序 + 自身下标」。
func TestMakeTreeSnapshotBranches(t *testing.T) {
	snap := makeTreeSnapshot(treeFixture(t))

	cases := []struct {
		id    conversation.MessageID
		want  []conversation.MessageID
		index int
	}{
		{"a1", []conversation.MessageID{"a1", "a2"}, 0}, // 同级：创建序（a1 早于 a2）
		{"a2", []conversation.MessageID{"a1", "a2"}, 1},
		{"u1", []conversation.MessageID{"u1"}, 0},       // 独子：n=1（UI 不渲染按钮）
		{"p", []conversation.MessageID{"p"}, 0},         // 独子
		{"convT", []conversation.MessageID{"convT"}, 0}, // Root 的父键为 ""，故同级只有自身
	}
	for _, tc := range cases {
		got, ok := snap.branches[tc.id]
		if !ok {
			t.Fatalf("Branches(%s): 不在快照中", tc.id)
		}
		if len(got.IDs) != len(tc.want) {
			t.Fatalf("Branches(%s).IDs = %v, want %v", tc.id, got.IDs, tc.want)
		}
		for i := range tc.want {
			if got.IDs[i] != tc.want[i] {
				t.Fatalf("Branches(%s).IDs = %v, want %v", tc.id, got.IDs, tc.want)
			}
		}
		if got.Index != tc.index {
			t.Fatalf("Branches(%s).Index = %d, want %d", tc.id, got.Index, tc.index)
		}
	}

	if _, ok := snap.branches["zz"]; ok {
		t.Fatal("未知节点不应出现在快照中")
	}
}

// TestMakeTreeSnapshotOrderTieBreak CreatedAt 相等（Revise 同刻）时按 ID 升序——与
// domain `Conversation.Branches` 的排序口径一致，保证快照确定性。
func TestMakeTreeSnapshotOrderTieBreak(t *testing.T) {
	c := conversation.New(conversation.ID("convT"), "同刻")
	txt := []conversation.Part{{Kind: conversation.PartText, Text: "x"}}
	root := conversation.MessageID(c.ID)
	for _, id := range []conversation.MessageID{"p", "b", "a"} {
		parent := root
		if id != "p" {
			parent = "p"
		}
		if err := c.AppendCommitted(conversation.Message{
			ID: id, Parent: parent, Role: conversation.RoleUser, Content: txt, CreatedAt: testTime,
		}); err != nil {
			t.Fatalf("commit %s: %v", id, err)
		}
	}
	snap := makeTreeSnapshot(c)
	bi := snap.branches["a"]
	want := []conversation.MessageID{"a", "b"}
	if len(bi.IDs) != 2 || bi.IDs[0] != want[0] || bi.IDs[1] != want[1] {
		t.Fatalf("同刻兄弟排序 = %v, want %v（按 ID 升序）", bi.IDs, want)
	}
	if bi.Index != 0 {
		t.Fatalf("a 的下标 = %d, want 0", bi.Index)
	}
	if got := snap.branches["b"].Index; got != 1 {
		t.Fatalf("b 的下标 = %d, want 1", got)
	}
}

// TestSessionBranchesPublishesOnHandle 快照在构造完成与每次 Handle 后发布：Revise 造出
// 新兄弟节点后，UI 侧无须手动刷新即可读到新的同级集合与下标（D80/§7.5）。
func TestSessionBranchesPublishesOnHandle(t *testing.T) {
	store := newMemStore()
	s, _ := newConfirmedSession(t, store, nil)
	c := buildTree(t, s)

	// 构造/挂树后即可读（buildTree 替换了 s.cur）。
	if bi, ok := s.Branches("a1"); !ok || len(bi.IDs) != 1 || bi.Index != 0 {
		t.Fatalf("初始 Branches(a1) = %+v, %v；want 独子下标 0", bi, ok)
	}

	// Revise 造出 a1 的兄弟节点。
	if _, err := handleCmd(s, "edit", "a1", "更优雅的回复"); err != nil {
		t.Fatalf("edit: %v", err)
	}
	sib := revisedID(c, "a1")
	if sib == "" {
		t.Fatal("edit 未产生新节点")
	}

	bi, ok := s.Branches("a1")
	if !ok {
		t.Fatal("Revise 后 Branches(a1) 不可读")
	}
	if len(bi.IDs) != 2 || bi.IDs[0] != "a1" || bi.IDs[1] != sib {
		t.Fatalf("Branches(a1).IDs = %v, want [a1 %s]", bi.IDs, sib)
	}
	if bi.Index != 0 {
		t.Fatalf("a1 下标 = %d, want 0", bi.Index)
	}
	if got, ok := s.Branches(sib); !ok || got.Index != 1 {
		t.Fatalf("新兄弟下标 = %+v, %v；want 1", got, ok)
	}

	if _, ok := s.Branches("zz"); ok {
		t.Fatal("未知节点应 ok=false")
	}
}

// TestSessionBranchesUnknownBeforePublish 未发布快照时 Branches 安全返回 ok=false
// （零值 Session 可调用，不 panic）。
func TestSessionBranchesUnknownBeforePublish(t *testing.T) {
	var s Session
	if _, ok := s.Branches("a1"); ok {
		t.Fatal("未发布快照应 ok=false")
	}
	s.publishTree() // cur == nil：静默 no-op
	if _, ok := s.Branches("a1"); ok {
		t.Fatal("cur 为 nil 时 publishTree 不应发布空快照")
	}
	if _, ok := s.Tail("a1"); ok {
		t.Fatal("未发布快照 Tail 应 ok=false")
	}
}

// TestMakeTreeSnapshotTail 快照版本末端（D94）：叶子即自身；有孩子取子树 CreatedAt
// 最新的叶子；未知节点不在快照中。
func TestMakeTreeSnapshotTail(t *testing.T) {
	snap := makeTreeSnapshot(treeFixture(t))
	for id, want := range map[conversation.MessageID]conversation.MessageID{
		"a1": "a1", "a2": "a2", // 叶子即自身
		"u1": "a2", // 子树最新叶子（a2 晚于 a1）
		"p":  "a2",
	} {
		if got := snap.tails[id]; got != want {
			t.Fatalf("tails[%s] = %s, want %s", id, got, want)
		}
	}
	if _, ok := snap.tails["zz"]; ok {
		t.Fatal("未知节点不应出现在快照中")
	}
}

// TestSessionTail 端口实现（D94）：快照随 publishTree 发布，Tail 读最新发布值；
// buildTree 的 u1→a1→t1→u2 链唯一叶子是 u2。
func TestSessionTail(t *testing.T) {
	s, _ := newConfirmedSession(t, newMemStore(), nil)
	buildTree(t, s)

	if got, ok := s.Tail("u2"); !ok || got != "u2" {
		t.Fatalf("Tail(u2) = %q %v, want 自身", got, ok)
	}
	if got, ok := s.Tail("u1"); !ok || got != "u2" {
		t.Fatalf("Tail(u1) = %q %v, want u2（链唯一叶子）", got, ok)
	}
	if _, ok := s.Tail("zz"); ok {
		t.Fatal("未知节点 ok=false")
	}
}

// TestSessionBranchesConcurrentRead 并发契约（§15.5）：UI 侧多 goroutine 读快照、
// 装配根侧反复重建发布——-race 门禁下必须干净（D80/§7.5 的原子发布是唯一同步点）。
func TestSessionBranchesConcurrentRead(t *testing.T) {
	s, _ := newConfirmedSession(t, newMemStore(), nil)
	buildTree(t, s)

	stop := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
					_, _ = s.Branches("a1")
					_, _ = s.Graph() // D120⑦：整树快照同源，并发读同契约
					_ = s.List()     // D120⑥：会话列表快照同组发布，并发读同契约
				}
			}
		}()
	}
	for i := 0; i < 50; i++ { // 写侧：重建整树快照并原子发布
		s.publishTree()
		s.publishSessions(context.Background())
	}
	close(stop)
	wg.Wait()
}

// TestMakeTreeSnapshotGraph 整树快照（D112②/D120⑦）：节点集完备、Anchor = Head、
// 结构根无父边/无版本链、顺接边为 seq、同级下标与 BranchInfo 同源、节点按 (CreatedAt, ID)
// 升序（布局可复现）。
func TestMakeTreeSnapshotGraph(t *testing.T) {
	s, _ := newConfirmedSession(t, newMemStore(), nil)
	c := buildTree(t, s)

	g, ok := s.Graph()
	if !ok {
		t.Fatal("构造后 Graph 应已发布")
	}
	if g.Total != len(c.Nodes) || len(g.Nodes) != len(c.Nodes) {
		t.Fatalf("Total/Nodes = %d/%d, want %d", g.Total, len(g.Nodes), len(c.Nodes))
	}
	if g.Anchor != c.Head {
		t.Fatalf("Anchor = %q, want %q（当前 Head）", g.Anchor, c.Head)
	}

	byID := make(map[conversation.MessageID]port.GraphNode, len(g.Nodes))
	for _, n := range g.Nodes {
		byID[n.ID] = n
	}
	rootID := conversation.MessageID(c.ID)
	root := byID[rootID]
	if root.Parent != "" || root.EdgeKind != "" || root.Role != conversation.RoleRoot {
		t.Fatalf("结构根 = %+v, want Parent=\"\" EdgeKind=\"\" Role=root", root)
	}
	if p := byID["p"]; p.Parent != rootID || p.Role != conversation.RoleSystem || p.EdgeKind != port.EdgeSeq {
		t.Fatalf("persona = %+v, want 挂在 root 下、system、seq", p)
	}
	if a1 := byID["a1"]; a1.SiblingIdx != 0 || a1.SiblingCount != 1 {
		t.Fatalf("a1 同级 = %d/%d, want 0/1", a1.SiblingIdx, a1.SiblingCount)
	}
	if byID["a1"].Snippet == "" {
		t.Fatal("Snippet 不应为空（展示用摘要）")
	}
	// 排序：非降 (CreatedAt, ID)。
	for i := 1; i < len(g.Nodes); i++ {
		a, b := g.Nodes[i-1], g.Nodes[i]
		if a.CreatedAt.After(b.CreatedAt) || (a.CreatedAt.Equal(b.CreatedAt) && a.ID > b.ID) {
			t.Fatalf("节点序非 (CreatedAt,ID) 升序：%s 在 %s 之前", a.ID, b.ID)
		}
	}
}

// TestSessionGraphRevisionEdge 版本链边（D120§2.3）：/edit 产生的同级新节点，其与父的边为
// EdgeRevise，且同级计数与 BranchInfo 一致（graph 与 branches 自洽）。
func TestSessionGraphRevisionEdge(t *testing.T) {
	s, _ := newConfirmedSession(t, newMemStore(), nil)
	c := buildTree(t, s)
	if _, err := handleCmd(s, "edit", "a1", "更优雅的回复"); err != nil {
		t.Fatalf("edit: %v", err)
	}
	sib := revisedID(c, "a1")
	if sib == "" {
		t.Fatal("edit 未产生新节点")
	}
	g, ok := s.Graph()
	if !ok {
		t.Fatal("Graph 不可读")
	}
	var got [2]port.GraphNode
	found := 0
	for _, n := range g.Nodes {
		if n.ID == "a1" || n.ID == sib {
			got[found] = n
			found++
		}
	}
	if found != 2 {
		t.Fatalf("快照缺分叉节点：found=%d", found)
	}
	for _, n := range got {
		if n.SiblingCount != 2 {
			t.Fatalf("%s 同级数 = %d, want 2", n.ID, n.SiblingCount)
		}
	}
	if got[0].ID == "a1" {
		if got[0].EdgeKind != port.EdgeSeq {
			t.Fatalf("旧版本 a1 边 = %q, want seq", got[0].EdgeKind)
		}
		if got[1].EdgeKind != port.EdgeRevise {
			t.Fatalf("新版本 %s 边 = %q, want revise", got[1].ID, got[1].EdgeKind)
		}
	} else {
		if got[0].EdgeKind != port.EdgeRevise || got[1].EdgeKind != port.EdgeSeq {
			t.Fatalf("版本链边型错: %+v", got)
		}
	}
}

// TestSessionGraphUnknownBeforePublish 未发布快照时 Graph 安全返回 ok=false。
func TestSessionGraphUnknownBeforePublish(t *testing.T) {
	var s Session
	if _, ok := s.Graph(); ok {
		t.Fatal("未发布快照应 ok=false")
	}
}
