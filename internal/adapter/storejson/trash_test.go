package storejson

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Tonyjh07/Aquarius/internal/domain/conversation"
)

// newTrashFixture 建好「会话目录 + 回收站目录 + 一个已存会话」的最小环境。
func newTrashFixture(t *testing.T) (*Store, string) {
	t.Helper()
	convDir := t.TempDir()
	trashDir := filepath.Join(t.TempDir(), "trash")
	s, err := New(convDir)
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	return s, trashDir
}

// mustConvID 同 mustConv，但 ID 在建树前定死：根节点就是 ID 本身，事后改不动。
func mustConvID(t *testing.T, id conversation.ID) *conversation.Conversation {
	t.Helper()
	c := conversation.New(id, "测试会话")
	if _, err := c.Append(conversation.RoleUser, []conversation.Part{{Kind: conversation.PartText, Text: "你好"}}); err != nil {
		t.Fatalf("append user: %v", err)
	}
	if _, err := c.Append(conversation.RoleAssistant, []conversation.Part{{Kind: conversation.PartText, Text: "你好！"}}); err != nil {
		t.Fatalf("append assistant: %v", err)
	}
	return c
}

func TestMoveToTrashMovesWholeFileSet(t *testing.T) {
	s, trash := newTrashFixture(t)
	ctx := context.Background()
	c := mustConv(t)
	if err := s.Save(ctx, c); err != nil {
		t.Fatalf("save: %v", err)
	}
	// 备份代 + 临时残片 + 会话记忆：都是该跟着走的部分。
	if err := os.WriteFile(filepath.Join(s.dir, "conv-1.json.bak"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(s.dir, "conv-1.json.abc.tmp"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(s.dir, "conv-1.memory.md"), []byte("# 记忆"), 0o644); err != nil {
		t.Fatal(err)
	}
	// 第二个会话留在原地：前缀收集不许误伤。
	other := mustConvID(t, "conv-2")
	if err := s.Save(ctx, other); err != nil {
		t.Fatalf("save other: %v", err)
	}
	if err := s.MoveToTrash(ctx, trash, c.ID); err != nil {
		t.Fatalf("move to trash: %v", err)
	}
	for _, name := range []string{"conv-1.json", "conv-1.json.bak", "conv-1.json.abc.tmp", "conv-1.memory.md"} {
		if _, err := os.Stat(filepath.Join(trash, name)); err != nil {
			t.Fatalf("%s not in trash: %v", name, err)
		}
		if _, err := os.Stat(filepath.Join(s.dir, name)); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("%s still in conversations: %v", name, err)
		}
	}
	if _, err := os.Stat(filepath.Join(s.dir, "conv-2.json")); err != nil {
		t.Fatalf("other conversation vanished: %v", err)
	}
}

func TestMoveToTrashThenLoadGone(t *testing.T) {
	s, trash := newTrashFixture(t)
	ctx := context.Background()
	c := mustConv(t)
	if err := s.Save(ctx, c); err != nil {
		t.Fatalf("save: %v", err)
	}
	if err := s.MoveToTrash(ctx, trash, c.ID); err != nil {
		t.Fatalf("move: %v", err)
	}
	if _, err := s.Load(ctx, c.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("load after move = %v, want ErrNotFound", err)
	}
	// 重复移入：回收站已有同名 → 拒绝，不覆盖。
	if err := s.MoveToTrash(ctx, trash, c.ID); err == nil {
		t.Fatal("second move should fail")
	}
}

func TestRestoreFromTrashRoundtrip(t *testing.T) {
	s, trash := newTrashFixture(t)
	ctx := context.Background()
	c := mustConv(t)
	if err := s.Save(ctx, c); err != nil {
		t.Fatalf("save: %v", err)
	}
	if err := os.WriteFile(filepath.Join(s.dir, "conv-1.memory.md"), []byte("# 记忆"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := s.MoveToTrash(ctx, trash, c.ID); err != nil {
		t.Fatalf("move: %v", err)
	}
	if err := s.RestoreFromTrash(ctx, trash, c.ID); err != nil {
		t.Fatalf("restore: %v", err)
	}
	got, err := s.Load(ctx, c.ID)
	if err != nil {
		t.Fatalf("load after restore: %v", err)
	}
	if got.Title != c.Title || got.NodeCount() != c.NodeCount() {
		t.Fatalf("restored tree mismatch: got %q/%d", got.Title, got.NodeCount())
	}
	if _, err := os.Stat(filepath.Join(s.dir, "conv-1.memory.md")); err != nil {
		t.Fatalf("memory file not restored: %v", err)
	}
	if _, err := os.Stat(filepath.Join(trash, "conv-1.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("trash entry still there: %v", err)
	}
}

func TestRestoreConflictAndMissing(t *testing.T) {
	s, trash := newTrashFixture(t)
	ctx := context.Background()
	c := mustConv(t)
	if err := s.Save(ctx, c); err != nil {
		t.Fatalf("save: %v", err)
	}
	// 回收站没有这个会话：ErrNotFound。
	if err := s.RestoreFromTrash(ctx, trash, c.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("restore missing = %v, want ErrNotFound", err)
	}
	if err := s.MoveToTrash(ctx, trash, c.ID); err != nil {
		t.Fatalf("move: %v", err)
	}
	// 会话目录已有同 ID（新会话占位）：拒绝还原，两份都在。
	if err := s.Save(ctx, mustConvID(t, "conv-1")); err != nil {
		t.Fatalf("save new occupant: %v", err)
	}
	if err := s.RestoreFromTrash(ctx, trash, c.ID); err == nil {
		t.Fatal("restore onto existing ID should fail")
	}
	if _, err := os.Stat(filepath.Join(trash, "conv-1.json")); err != nil {
		t.Fatalf("trash entry must survive failed restore: %v", err)
	}
}

func TestListTrashSortedByDeletedAt(t *testing.T) {
	s, trash := newTrashFixture(t)
	ctx := context.Background()
	older := mustConvID(t, "older-conv")
	if err := s.Save(ctx, older); err != nil {
		t.Fatal(err)
	}
	newer := mustConvID(t, "newer-conv")
	if err := s.Save(ctx, newer); err != nil {
		t.Fatal(err)
	}
	if err := s.MoveToTrash(ctx, trash, older.ID); err != nil {
		t.Fatal(err)
	}
	time.Sleep(20 * time.Millisecond) // 保证两份 mtime 不同
	if err := s.MoveToTrash(ctx, trash, newer.ID); err != nil {
		t.Fatal(err)
	}
	entries, err := s.ListTrash(ctx, trash)
	if err != nil {
		t.Fatalf("list trash: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("entries = %d, want 2", len(entries))
	}
	if entries[0].ID != newer.ID || entries[1].ID != older.ID {
		t.Fatalf("order wrong: %s before %s", entries[0].ID, entries[1].ID)
	}
	if !entries[0].DeletedAt.After(entries[1].DeletedAt) {
		t.Fatalf("deleted_at not ordered: %v vs %v", entries[0].DeletedAt, entries[1].DeletedAt)
	}
	if entries[0].Title != newer.Title || entries[0].MessageN != newer.NodeCount()-1 {
		t.Fatalf("summary mismatch: %+v", entries[0])
	}
	// 没有回收站目录 = 空。
	empty, err := s.ListTrash(ctx, filepath.Join(t.TempDir(), "nope"))
	if err != nil || len(empty) != 0 {
		t.Fatalf("missing trash dir: entries=%v err=%v", empty, err)
	}
}

func TestSweepTrashRemovesOnlyAgedEntries(t *testing.T) {
	s, trash := newTrashFixture(t)
	ctx := context.Background()
	old := mustConvID(t, "old-conv")
	if err := s.Save(ctx, old); err != nil {
		t.Fatal(err)
	}
	fresh := mustConvID(t, "fresh-conv")
	if err := s.Save(ctx, fresh); err != nil {
		t.Fatal(err)
	}
	if err := s.MoveToTrash(ctx, trash, old.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.MoveToTrash(ctx, trash, fresh.ID); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(trash, "old-conv.memory.md"), []byte("# 记忆"), 0o644); err != nil {
		t.Fatal(err)
	}
	// 把 old-conv 的主文件改动时间拨到 40 天前。
	stamp := time.Now().Add(-40 * 24 * time.Hour)
	if err := os.Chtimes(filepath.Join(trash, "old-conv.json"), stamp, stamp); err != nil {
		t.Fatal(err)
	}
	n, err := s.SweepTrash(ctx, trash, 30*24*time.Hour)
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if n != 1 {
		t.Fatalf("removed = %d, want 1", n)
	}
	if _, err := os.Stat(filepath.Join(trash, "old-conv.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("aged entry survived: %v", err)
	}
	if _, err := os.Stat(filepath.Join(trash, "old-conv.memory.md")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("aged memory survived: %v", err)
	}
	if _, err := os.Stat(filepath.Join(trash, "fresh-conv.json")); err != nil {
		t.Fatalf("fresh entry wrongly swept: %v", err)
	}
	// 没有回收站目录 = 0 且不报错。
	if n, err := s.SweepTrash(ctx, filepath.Join(t.TempDir(), "nope"), 30*24*time.Hour); n != 0 || err != nil {
		t.Fatalf("missing trash dir: n=%d err=%v", n, err)
	}
}
