package app

// session_cmd_fork_test.go /fork 的命令面行为（D312）：子树摘成新会话、ID 重生、
// 溯源不跨会话、附件引用随行、原会话一字不动。

import (
	"context"
	"strings"
	"testing"

	"github.com/Tonyjh07/Aquarius/internal/domain/conversation"
	"github.com/Tonyjh07/Aquarius/internal/i18n"
)

// forkFixture 当前会话带一个分叉：root → user「q」 → assistant a1；a2 是 user 的另一个孩子。
// 返回当前会话、user / a1 / a2 的节点 ID 与会话存储。
func forkFixture(t *testing.T) (*Session, *memStore, conversation.MessageID, conversation.MessageID, conversation.MessageID) {
	t.Helper()
	s, store, _ := newBatchSession(t, "甲")
	cur := s.Current()
	u, err := cur.Append(conversation.RoleUser, []conversation.Part{{Kind: conversation.PartText, Text: "q"}})
	if err != nil {
		t.Fatalf("append user: %v", err)
	}
	a1, err := cur.Append(conversation.RoleAssistant, []conversation.Part{{Kind: conversation.PartText, Text: "a1"}})
	if err != nil {
		t.Fatalf("append a1: %v", err)
	}
	if err := cur.Checkout(u.ID); err != nil {
		t.Fatalf("checkout: %v", err)
	}
	a2, err := cur.Append(conversation.RoleAssistant, []conversation.Part{{Kind: conversation.PartText, Text: "a2"}})
	if err != nil {
		t.Fatalf("append a2: %v", err)
	}
	if err := store.Save(context.Background(), cur); err != nil {
		t.Fatalf("save: %v", err)
	}
	return s, store, u.ID, a1.ID, a2.ID
}

// TestForkTakesWholeSubtree 分叉点在分叉节点：user 的整棵子树（含 a1、a2 两个分支）都跟过去，
// Head 落在分叉节点；原会话的树、Head、节点 ID 一字不动。
func TestForkTakesWholeSubtree(t *testing.T) {
	s, store, u, a1, a2 := forkFixture(t)
	before := s.Current().Snapshot()

	out, err := handleCmd(s, "fork", string(u))
	if err != nil {
		t.Fatalf("fork: %v", err)
	}
	forked := s.Current()
	if forked.ID == before.ID {
		t.Fatal("current conversation must switch to the fork")
	}
	snap := forked.Snapshot()
	if want := 4; len(snap.Nodes) != want { // root + user + a1 + a2
		t.Fatalf("nodes = %d, want %d", len(snap.Nodes), want)
	}
	if snap.Head == conversation.MessageID(u) {
		t.Fatalf("head = %s, want the fork node's NEW id (old ids must not carry over)", snap.Head)
	}
	// Head 是分叉节点的**新 ID**：老 ID 全部不在新会话里。
	for _, old := range []conversation.MessageID{u, a1, a2} {
		if _, ok := forked.Find(old); ok {
			t.Fatalf("old node id %s must not exist in the fork", old)
		}
	}
	// 两个分支都在：user 的孩子恰两个，正文对得上。
	var forkedU conversation.Message
	for _, m := range snap.Nodes {
		if m.Parent == conversation.MessageID(forked.ID) {
			forkedU = m
		}
	}
	if forkedU.ID == "" {
		t.Fatal("fork node missing")
	}
	if got := len(forked.ChildIDs(forkedU.ID)); got != 2 {
		t.Fatalf("branch count = %d, want 2", got)
	}
	if want := i18n.Tf("tree.fork.done", "id", forked.ID, "title", forked.Title, "from", u); !strings.Contains(out, want) {
		t.Fatalf("out = %q, want %q", out, want)
	}
	// 原会话一字不动：快照逐字段一致（连 Head 与标题）。
	after := mustLoad(t, store, before.ID).Snapshot()
	if after.Head != before.Head || after.Title != before.Title || len(after.Nodes) != len(before.Nodes) {
		t.Fatalf("source conversation changed: %+v vs %+v", after, before)
	}
}

func mustLoad(t *testing.T, store *memStore, id conversation.ID) *conversation.Conversation {
	t.Helper()
	c, err := store.Load(context.Background(), id)
	if err != nil {
		t.Fatalf("load %s: %v", id, err)
	}
	return c
}

// TestForkLeafTakesOnlyItsSubtree 分叉点在叶子：只有该节点跟过去，兄弟不带走。
func TestForkLeafTakesOnlyItsSubtree(t *testing.T) {
	s, _, _, a1, _ := forkFixture(t)
	if _, err := handleCmd(s, "fork", string(a1)); err != nil {
		t.Fatalf("fork: %v", err)
	}
	snap := s.Current().Snapshot()
	if len(snap.Nodes) != 2 { // root + a1
		t.Fatalf("nodes = %d, want 2", len(snap.Nodes))
	}
	if s.Current().Head == conversation.MessageID(a1) {
		t.Fatalf("head must be the fork node's NEW id, not %s", a1)
	}
}

// TestForkTitleDefaultsAndOverride 标题缺省沿用原会话标题；给了就用新的。
func TestForkTitleDefaultsAndOverride(t *testing.T) {
	s, _, u, _, _ := forkFixture(t)
	srcTitle := s.Current().Title
	if _, err := handleCmd(s, "fork", string(u)); err != nil {
		t.Fatalf("fork: %v", err)
	}
	if s.Current().Title != srcTitle {
		t.Fatalf("default title = %q, want %q", s.Current().Title, srcTitle)
	}
	// 第二次分叉站在新会话里：分叉节点的新 ID 要现取（老 ID 不跨会话）。
	var forkPoint conversation.MessageID
	for _, m := range s.Current().Snapshot().Nodes {
		if m.Parent == conversation.MessageID(s.Current().ID) {
			forkPoint = m.ID
		}
	}
	if _, err := handleCmd(s, "fork", string(forkPoint), "新题目"); err != nil {
		t.Fatalf("fork with title: %v", err)
	}
	if s.Current().Title != "新题目" {
		t.Fatalf("title = %q, want 新题目", s.Current().Title)
	}
}

// TestForkRootRejected 会话根不可分叉。
func TestForkRootRejected(t *testing.T) {
	s, _, _, _, _ := forkFixture(t)
	if _, err := handleCmd(s, "fork", string(s.Current().ID)); err == nil ||
		!strings.Contains(err.Error(), i18n.T("tree.fork.root")) {
		t.Fatalf("err = %v, want 根不可分叉", err)
	}
}

// TestForkDropsEditions 「修订自」不跨会话复制：源里有一条版本链，分叉出的会话一条没有。
func TestForkDropsEditions(t *testing.T) {
	s, _, u, a1, _ := forkFixture(t)
	// 给 a1 建一条树内版本链（Clone：深拷贝成新节点）。
	if _, err := s.Current().Revise(a1, []conversation.Part{{Kind: conversation.PartText, Text: "a1 改"}}, conversation.Clone); err != nil {
		t.Fatalf("revise: %v", err)
	}
	if len(s.Current().Snapshot().RevisedFrom) == 0 {
		t.Fatal("setup: source must carry an edition")
	}
	if _, err := handleCmd(s, "fork", string(u)); err != nil {
		t.Fatalf("fork: %v", err)
	}
	if got := len(s.Current().Snapshot().RevisedFrom); got != 0 {
		t.Fatalf("fork editions = %d, want 0", got)
	}
}

// TestForkCarriesAttachments 附件引用随子树走：同一个 BlobRef 原样带上（字节共享，不复制）。
func TestForkCarriesAttachments(t *testing.T) {
	s, _, u, _, _ := forkFixture(t)
	ref := &conversation.BlobRef{Hash: "abc123", MIME: "image/png", Name: "shot.png", Size: 7}
	if _, err := s.Current().Append(conversation.RoleUser, []conversation.Part{
		{Kind: conversation.PartImage, Ref: ref},
	}); err != nil {
		t.Fatalf("append image: %v", err)
	}
	if _, err := handleCmd(s, "fork", string(u)); err != nil {
		t.Fatalf("fork: %v", err)
	}
	found := false
	for _, m := range s.Current().Snapshot().Nodes {
		for _, p := range m.Content {
			if p.Ref != nil {
				found = true
				if p.Ref.Hash != ref.Hash || p.Ref.Name != ref.Name {
					t.Fatalf("ref = %+v, want %+v", p.Ref, ref)
				}
			}
		}
	}
	if !found {
		t.Fatal("attachment reference missing in the fork")
	}
}

// TestForkCarriesCharacterAndTail Character 与 Tail 是会话属性，随分叉带过去。
func TestForkCarriesCharacterAndTail(t *testing.T) {
	s, _, u, _, _ := forkFixture(t)
	s.Current().Character = "aria"
	s.Current().Tail = "After history, stay Aria."
	if _, err := handleCmd(s, "fork", string(u)); err != nil {
		t.Fatalf("fork: %v", err)
	}
	if s.Current().Character != "aria" || s.Current().Tail != "After history, stay Aria." {
		t.Fatalf("character/tail = %q/%q, want aria/…", s.Current().Character, s.Current().Tail)
	}
}
