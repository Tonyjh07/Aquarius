package storejson

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Tonyjh07/Aquarius/internal/domain/conversation"
)

// mustConv 造一棵小会话：u1(user) ← a1(assistant)。
func mustConv(t *testing.T) *conversation.Conversation {
	t.Helper()
	c := conversation.New(conversation.ID("conv-1"), "测试会话")
	if _, err := c.Append(conversation.RoleUser, []conversation.Part{{Kind: conversation.PartText, Text: "你好"}}); err != nil {
		t.Fatalf("append user: %v", err)
	}
	if _, err := c.Append(conversation.RoleAssistant, []conversation.Part{{Kind: conversation.PartText, Text: "你好！"}}); err != nil {
		t.Fatalf("append assistant: %v", err)
	}
	return c
}

func TestSaveLoadRoundtrip(t *testing.T) {
	s, err := New(t.TempDir())
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	ctx := context.Background()
	src := mustConv(t)

	if err := s.Save(ctx, src); err != nil {
		t.Fatalf("save: %v", err)
	}
	got, err := s.Load(ctx, src.ID)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if err := got.Validate(); err != nil {
		t.Fatalf("loaded conv invalid: %v", err)
	}
	if got.ID != src.ID || got.Title != src.Title || got.Head != src.Head {
		t.Fatalf("meta mismatch: got %+v want %+v", got, src)
	}
	if len(got.Nodes) != len(src.Nodes) {
		t.Fatalf("nodes = %d, want %d", len(got.Nodes), len(src.Nodes))
	}
	for id, want := range src.Nodes {
		gnode, ok := got.Find(id)
		if !ok {
			t.Fatalf("node %s missing", id)
		}
		if gnode.Role != want.Role || len(gnode.Content) != len(want.Content) {
			t.Fatalf("node %s mismatch: got %+v want %+v", id, gnode, want)
		}
		if len(want.Content) > 0 && gnode.Content[0].Text != want.Content[0].Text {
			t.Fatalf("node %s text = %q, want %q", id, gnode.Content[0].Text, want.Content[0].Text)
		}
	}
	// 首次保存不应产生 .bak（无旧文件可留）。
	if _, err := os.Stat(filepath.Join(s.dir, "conv-1.json.bak")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("unexpected .bak after first save: %v", err)
	}
}

func TestSaveKeepsOneGenerationBak(t *testing.T) {
	s, err := New(t.TempDir())
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	ctx := context.Background()
	c := mustConv(t)

	if err := s.Save(ctx, c); err != nil {
		t.Fatalf("save#1: %v", err)
	}
	// 第一次保存后追加一条消息（标题也改），再保存：旧代应落进 .bak。
	if _, err := c.Append(conversation.RoleUser, []conversation.Part{{Kind: conversation.PartText, Text: "第二句"}}); err != nil {
		t.Fatalf("append: %v", err)
	}
	c.Title = "改过标题"
	if err := s.Save(ctx, c); err != nil {
		t.Fatalf("save#2: %v", err)
	}

	bakData, err := os.ReadFile(filepath.Join(s.dir, "conv-1.json.bak"))
	if err != nil {
		t.Fatalf("read .bak: %v", err)
	}
	var bak conversation.Conversation
	if err := json.Unmarshal(bakData, &bak); err != nil {
		t.Fatalf("parse .bak: %v", err)
	}
	if bak.Title != "测试会话" {
		t.Fatalf(".bak title = %q, want 上一代标题", bak.Title)
	}
	if len(bak.Nodes) != 3 {
		t.Fatalf(".bak nodes = %d, want 3（上一代：root+u+a）", len(bak.Nodes))
	}

	cur, err := s.Load(ctx, c.ID)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(cur.Nodes) != 4 || cur.Title != "改过标题" {
		t.Fatalf("current = %d nodes title %q, want 4 nodes 改过标题", len(cur.Nodes), cur.Title)
	}
}

func TestLoadFallsBackToBakWhenMainCorrupt(t *testing.T) {
	s, err := New(t.TempDir())
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	ctx := context.Background()
	c := mustConv(t)
	if err := s.Save(ctx, c); err != nil {
		t.Fatalf("save#1: %v", err)
	}
	if _, err := c.Append(conversation.RoleUser, []conversation.Part{{Kind: conversation.PartText, Text: "追加"}}); err != nil {
		t.Fatalf("append: %v", err)
	}
	if err := s.Save(ctx, c); err != nil {
		t.Fatalf("save#2: %v", err)
	}
	// 损坏主文件。
	if err := os.WriteFile(s.path(c.ID), []byte("{corrupt!!"), 0o644); err != nil {
		t.Fatalf("corrupt: %v", err)
	}
	got, err := s.Load(ctx, c.ID)
	if err != nil {
		t.Fatalf("load should fall back to .bak: %v", err)
	}
	if len(got.Nodes) != 3 {
		t.Fatalf("fallback nodes = %d, want 3（.bak 一代：root+u+a）", len(got.Nodes))
	}
}

func TestLoadMissingReturnsNotFound(t *testing.T) {
	s, err := New(t.TempDir())
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	if _, err := s.Load(context.Background(), conversation.ID("nope")); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestListSortedAndSkipsBak(t *testing.T) {
	s, err := New(t.TempDir())
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	ctx := context.Background()

	a := conversation.New(conversation.ID("conv-a"), "A")
	if _, err := a.Append(conversation.RoleUser, []conversation.Part{{Kind: conversation.PartText, Text: "a"}}); err != nil {
		t.Fatalf("append: %v", err)
	}
	if err := s.Save(ctx, a); err != nil {
		t.Fatalf("save a: %v", err)
	}
	b := conversation.New(conversation.ID("conv-b"), "B")
	if err := s.Save(ctx, b); err != nil {
		t.Fatalf("save b: %v", err)
	}
	// 让 b 更新（UpdatedAt 更晚）。
	if _, err := b.Append(conversation.RoleUser, []conversation.Part{{Kind: conversation.PartText, Text: "b"}}); err != nil {
		t.Fatalf("append b: %v", err)
	}
	if err := s.Save(ctx, b); err != nil {
		t.Fatalf("save b#2: %v", err)
	}
	// 一个损坏文件不应让列表瘫痪。
	if err := os.WriteFile(filepath.Join(s.dir, "conv-x.json"), []byte("garbage"), 0o644); err != nil {
		t.Fatalf("write garbage: %v", err)
	}

	list, err := s.List(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("list len = %d, want 2（损坏文件与 .bak 被跳过）", len(list))
	}
	if list[0].ID != "conv-b" || list[1].ID != "conv-a" {
		t.Fatalf("order = %s, %s; want conv-b 在前（最新）", list[0].ID, list[1].ID)
	}
	if list[0].Title != "B" || list[0].MessageN != 1 {
		t.Fatalf("summary b = %+v", list[0])
	}
	if list[1].MessageN != 1 {
		t.Fatalf("summary a messageN = %d, want 1", list[1].MessageN)
	}
}

func TestRemoveDeletesFileAndBak(t *testing.T) {
	s, err := New(t.TempDir())
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	ctx := context.Background()
	c := mustConv(t)
	if err := s.Save(ctx, c); err != nil {
		t.Fatalf("save#1: %v", err)
	}
	if err := s.Save(ctx, c); err != nil {
		t.Fatalf("save#2 (产生 .bak): %v", err)
	}

	if err := s.Remove(ctx, c.ID); err != nil {
		t.Fatalf("remove: %v", err)
	}
	for _, p := range []string{s.path(c.ID), s.path(c.ID) + ".bak"} {
		if _, err := os.Stat(p); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("%s should be gone: %v", p, err)
		}
	}
	if err := s.Remove(ctx, c.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("second remove err = %v, want ErrNotFound", err)
	}
	if _, err := s.Load(ctx, c.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("load after remove err = %v, want ErrNotFound", err)
	}
}

func TestSaveRejectsInvalidTree(t *testing.T) {
	s, err := New(t.TempDir())
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	c := mustConv(t)
	// 手工把树改坏：Head 指向不存在的节点。
	c.Head = conversation.MessageID("GHOST")
	if err := s.Save(context.Background(), c); err == nil {
		t.Fatal("save invalid tree should fail")
	}
	if _, err := os.Stat(s.path(c.ID)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("invalid tree must not be written: %v", err)
	}
}

// TestLoadRejectsLegacyVirtualRootFormat D19 破坏性切换：M0 虚拟 Root 格式必须明确报因。
func TestLoadRejectsLegacyVirtualRootFormat(t *testing.T) {
	s, err := New(t.TempDir())
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	legacy := `{
  "id": "conv-old",
  "title": "旧会话",
  "nodes": {"U1": {"id": "U1", "parent": "", "role": "user", "content": [{"kind": "text", "text": "hi"}], "created_at": "2026-01-01T00:00:00Z"}},
  "children": {"": ["U1"]},
  "head": "",
  "revised_from": {},
  "created_at": "2026-01-01T00:00:00Z",
  "updated_at": "2026-01-01T00:00:00Z"
}`
	if err := os.WriteFile(filepath.Join(s.dir, "conv-old.json"), []byte(legacy), 0o644); err != nil {
		t.Fatalf("write legacy: %v", err)
	}
	_, err = s.Load(context.Background(), conversation.ID("conv-old"))
	if err == nil || !strings.Contains(err.Error(), "旧格式") {
		t.Fatalf("err = %v, want 旧格式报因", err)
	}
}

func TestCanceledContextRejected(t *testing.T) {
	s, err := New(t.TempDir())
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := s.Save(ctx, mustConv(t)); !errors.Is(err, context.Canceled) {
		t.Fatalf("save err = %v, want context.Canceled", err)
	}
	if _, err := s.List(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("list err = %v, want context.Canceled", err)
	}
}
