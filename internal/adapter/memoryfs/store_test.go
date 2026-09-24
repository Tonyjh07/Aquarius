package memoryfs

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Tonyjh07/Aquarius/internal/domain/conversation"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

// newStore 临时目录上的记忆存储（全局 + conversations 同居 tmp 下，贴合 D23 布局）。
func newStore(t *testing.T) *Store {
	t.Helper()
	dir := t.TempDir()
	s, err := New(filepath.Join(dir, port.GlobalMemoryDoc), filepath.Join(dir, "conversations"))
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	return s
}

// TestWriteReadRoundtrip 全局与会话文档写读往返；ModTime 落盘后回读。
func TestWriteReadRoundtrip(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()

	for _, name := range []string{
		port.GlobalMemoryDoc,
		port.SessionMemoryDoc(conversation.ID("01JXTEST")),
	} {
		if err := s.Write(ctx, port.MemoryDoc{Name: name, Content: "# 标题\n内容正文"}); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
		doc, err := s.Read(ctx, name)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if doc.Name != name || doc.Content != "# 标题\n内容正文" {
			t.Fatalf("doc = %+v", doc)
		}
		if doc.ModTime.IsZero() {
			t.Fatalf("%s ModTime 为零", name)
		}
	}
}

// TestReadMissing 不存在报 ErrNotFound（含会话目录未创建时的会话文档）。
func TestReadMissing(t *testing.T) {
	s := newStore(t)
	if _, err := s.Read(context.Background(), port.GlobalMemoryDoc); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
	if _, err := s.Read(context.Background(), port.SessionMemoryDoc("nope")); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

// TestRejectInvalidNames 路径穿越与未知名字一律拒绝（不触碰 tmp 之外）。
func TestRejectInvalidNames(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	for _, name := range []string{
		"../evil.md", "..\\evil.md", "sub/evil.md", `sub\evil.md`,
		"evil.md", ".memory.md", "x.txt", "",
	} {
		if _, err := s.Read(ctx, name); err == nil {
			t.Fatalf("Read(%q) 应报错", name)
		}
		if err := s.Write(ctx, port.MemoryDoc{Name: name, Content: "x"}); err == nil {
			t.Fatalf("Write(%q) 应报错", name)
		}
	}
}

// TestIndex 索引只列有内容的文档、摘要取首个非空行、按名排序。
func TestIndex(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()

	entries, err := s.Index(ctx)
	if err != nil || len(entries) != 0 {
		t.Fatalf("空存储 index = %v, %v", entries, err)
	}

	if err := s.Write(ctx, port.MemoryDoc{Name: port.GlobalMemoryDoc, Content: "  \n## 用户偏好\n喜欢简洁回答"}); err != nil {
		t.Fatal(err)
	}
	if err := s.Write(ctx, port.MemoryDoc{Name: port.SessionMemoryDoc("convB"), Content: "会话B记忆"}); err != nil {
		t.Fatal(err)
	}
	if err := s.Write(ctx, port.MemoryDoc{Name: port.SessionMemoryDoc("convA"), Content: "  "}); err != nil {
		t.Fatal(err) // 空白文件写出成功但不进索引
	}

	entries, err = s.Index(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("index = %+v, want 2 项（空白文档不计）", entries)
	}
	if entries[0].Name != port.SessionMemoryDoc("convB") || entries[1].Name != port.GlobalMemoryDoc {
		t.Fatalf("排序或成员错误: %+v", entries)
	}
	if entries[1].Summary != "用户偏好" {
		t.Fatalf("summary = %q, want 去记号的首个非空行", entries[1].Summary)
	}
}

// TestSearch 关键词命中行号与片段、大小写不敏感、空词报错。
func TestSearch(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	content := "第一行\n喜欢 Go 语言\nGO 是一门语言\n第三行"
	if err := s.Write(ctx, port.MemoryDoc{Name: port.GlobalMemoryDoc, Content: content}); err != nil {
		t.Fatal(err)
	}

	hits, err := s.Search(ctx, "go")
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 2 {
		t.Fatalf("hits = %+v, want 2", hits)
	}
	if hits[0].Line != 2 || !strings.Contains(hits[0].Snippet, "Go") {
		t.Fatalf("hit[0] = %+v", hits[0])
	}
	if hits[1].Line != 3 {
		t.Fatalf("hit[1] = %+v", hits[1]) // 大小写不敏感
	}

	if _, err := s.Search(ctx, "  "); err == nil {
		t.Fatal("空检索词应报错")
	}
	if _, err := s.Search(context.Background(), "go"); err != nil { // 取消的 ctx
		t.Fatalf("正常 ctx: %v", err)
	}
	cctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := s.Search(cctx, "go"); !errors.Is(err, context.Canceled) {
		t.Fatalf("取消 ctx err = %v, want Canceled", err)
	}
}

// TestRemove 删除后读报 ErrNotFound；重复删除同样报 ErrNotFound。
func TestRemove(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	name := port.SessionMemoryDoc("convX")
	if err := s.Write(ctx, port.MemoryDoc{Name: name, Content: "x"}); err != nil {
		t.Fatal(err)
	}
	if err := s.Remove(ctx, name); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if _, err := s.Read(ctx, name); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
	if err := s.Remove(ctx, name); !errors.Is(err, ErrNotFound) {
		t.Fatalf("重复删除 err = %v, want ErrNotFound", err)
	}
	if err := s.Remove(ctx, "../x.md"); err == nil {
		t.Fatal("非法名删除应报错")
	}
}

// TestWriteCreatesDirs 会话记忆写入自动创建 conversations 目录；无 .tmp 残留。
func TestWriteCreatesDirs(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	name := port.SessionMemoryDoc("01ABC")
	if err := s.Write(ctx, port.MemoryDoc{Name: name, Content: "会话记忆"}); err != nil {
		t.Fatalf("write: %v", err)
	}
	file := filepath.Join(filepath.Dir(s.global), "conversations", name)
	if _, err := os.Stat(file); err != nil {
		t.Fatalf("会话记忆文件未落盘: %v", err)
	}
	matches, _ := filepath.Glob(file + "*")
	for _, m := range matches {
		if strings.HasSuffix(m, ".tmp") {
			t.Fatalf("残留临时文件: %s", m)
		}
	}
}

// TestNewValidation 空路径拒绝。
func TestNewValidation(t *testing.T) {
	if _, err := New("", "x"); err == nil {
		t.Fatal("空全局路径应报错")
	}
	if _, err := New("x", ""); err == nil {
		t.Fatal("空会话目录应报错")
	}
}
