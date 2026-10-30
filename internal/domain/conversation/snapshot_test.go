package conversation

import (
	"reflect"
	"testing"
)

// TestSnapshotRestoreRoundTrip 导出再重建必须得到一棵等价的树（节点、边、版本链、Head 全等），
// 且重建后不变量成立。这是 storejson 落盘的地基：格式换了也得能原样装回来。
func TestSnapshotRestoreRoundTrip(t *testing.T) {
	c := buildTree(t)
	root := MessageID(c.ID)
	u1 := c.ChildIDs(root)[0]
	revised, err := c.Revise(u1, []Part{{Kind: PartText, Text: "问一（改）"}}, Carry)
	if err != nil {
		t.Fatalf("revise: %v", err)
	}
	want := c.Snapshot()

	back, err := Restore(want)
	if err != nil {
		t.Fatalf("restore: %v", err)
	}
	if err := back.Validate(); err != nil {
		t.Fatalf("重建后树不合法: %v", err)
	}
	if got := back.Snapshot(); !reflect.DeepEqual(got, want) {
		t.Fatalf("往返不一致:\n得到 %+v\n期望 %+v", got, want)
	}
	if _, ok := back.Find(revised.ID); !ok {
		t.Fatal("重建后丢了修订节点")
	}
	if old, ok := back.Edition(revised.ID); !ok || old != u1 {
		t.Fatalf("重建后版本链 = (%q, %v), want (%q, true)", old, ok, u1)
	}
}

// TestSnapshotCarriesCharacterMetadata 会话元数据（角色 slug 与尾部指令）随快照往返：
// 它们是元数据不是节点，不进树表，但必须一起搬出去、原样装回来（D243）。
func TestSnapshotCarriesCharacterMetadata(t *testing.T) {
	c := buildTree(t)
	c.Character = "aria"
	c.Tail = "stay in character"

	s := c.Snapshot()
	if s.Character != "aria" || s.Tail != "stay in character" {
		t.Fatalf("snapshot metadata = (%q, %q)", s.Character, s.Tail)
	}
	back, err := Restore(s)
	if err != nil {
		t.Fatalf("restore: %v", err)
	}
	if back.Character != "aria" || back.Tail != "stay in character" {
		t.Fatalf("restored metadata = (%q, %q)", back.Character, back.Tail)
	}
	// 元数据不进节点表：节点数不因它变化。
	if back.NodeCount() != c.NodeCount() {
		t.Fatalf("metadata changed the node count: %d vs %d", back.NodeCount(), c.NodeCount())
	}
}

// TestSnapshotNodesSortedByID 快照的节点表按 ID 升序：整棵树倒出来时顺序必须确定，
// 否则同一棵树每次导出的字节都不同，diff 与人工核对都失去意义。
func TestSnapshotNodesSortedByID(t *testing.T) {
	s := buildTree(t).Snapshot()
	for i := 1; i < len(s.Nodes); i++ {
		if s.Nodes[i-1].ID >= s.Nodes[i].ID {
			t.Fatalf("快照节点未按 ID 升序：%s 紧跟 %s", s.Nodes[i-1].ID, s.Nodes[i].ID)
		}
	}
	for i := 1; i < len(s.Edges); i++ {
		if s.Edges[i-1].Parent >= s.Edges[i].Parent {
			t.Fatalf("快照边表未按父 ID 升序：%q 紧跟 %q", s.Edges[i-1].Parent, s.Edges[i].Parent)
		}
	}
}

// TestSnapshotDoesNotShareState 快照与内部表不共享：改快照不影响树，改树也不影响已导出的快照。
//
// 这是「导出」与「泄露」的分界——storejson 会拿着快照去排序、裁剪、写盘。
func TestSnapshotDoesNotShareState(t *testing.T) {
	c := buildTree(t)
	s := c.Snapshot()
	before := c.NodeCount()

	s.Nodes[0].Content = append(s.Nodes[0].Content, Part{Kind: PartText, Text: "注入"})
	for _, e := range s.Edges {
		if len(e.Childs) > 0 {
			e.Childs[0] = "篡改"
		}
	}
	if got := c.NodeCount(); got != before {
		t.Fatalf("改快照后节点数变了：%d → %d", before, got)
	}
	for _, e := range c.children {
		for _, k := range e {
			if k == "篡改" {
				t.Fatal("快照的孩子切片与内部表共享底层数组")
			}
		}
	}
}

// TestRestoreRejectsBrokenSnapshot 磁盘上的数据是不可信输入：坏快照必须在重建时就明确拒绝，
// 而不是进内存后变成难查的怪状态。
func TestRestoreRejectsBrokenSnapshot(t *testing.T) {
	cases := []struct {
		name string
		mut  func(*Snapshot)
	}{
		{"重复节点", func(s *Snapshot) { s.Nodes = append(s.Nodes, s.Nodes[0]) }},
		{"Head 不在树中", func(s *Snapshot) { s.Head = "GHOST" }},
		{"根节点缺失", func(s *Snapshot) {
			keep := s.Nodes[:0]
			for _, m := range s.Nodes {
				if m.Role != RoleRoot {
					keep = append(keep, m)
				}
			}
			s.Nodes = keep
		}},
		{"孩子边指向树外", func(s *Snapshot) {
			for i := range s.Edges {
				if s.Edges[i].Parent == "" {
					s.Edges[i].Childs = []MessageID{"GHOST"}
				}
			}
		}},
		{"版本链指向树外", func(s *Snapshot) {
			s.RevisedFrom = append(s.RevisedFrom, SnapshotEdition{New: s.Nodes[0].ID, Old: "GHOST"})
		}},
	}
	for _, tc := range cases {
		s := buildTree(t).Snapshot()
		tc.mut(&s)
		if _, err := Restore(s); err == nil {
			t.Errorf("%s: 坏快照被接受", tc.name)
		}
	}
}

// TestRestoreCopiesParts 重建时对分片做防御性拷贝：调用方后续改自己那份，不得影响树。
func TestRestoreCopiesParts(t *testing.T) {
	s := buildTree(t).Snapshot()
	back, err := Restore(s)
	if err != nil {
		t.Fatalf("restore: %v", err)
	}
	// 找到第一个有内容分片的节点，改快照里那份的文本。
	idx := -1
	for i, m := range s.Nodes {
		if len(m.Content) > 0 {
			idx = i
			break
		}
	}
	if idx < 0 {
		t.Fatal("快照里没有带内容分片的节点")
	}
	orig := s.Nodes[idx].Content[0].Text
	s.Nodes[idx].Content[0].Text = "被改写"
	m, _ := back.Find(s.Nodes[idx].ID)
	if len(m.Content) > 0 && m.Content[0].Text != orig {
		t.Fatalf("重建后的节点内容跟着快照变了：%q → %q", orig, m.Content[0].Text)
	}
}
