package blobfs

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Tonyjh07/Aquarius/internal/domain/conversation"
)

// TestPutGetRoundtrip 写入-读取往返：引用字段完整、内容一致。
func TestPutGetRoundtrip(t *testing.T) {
	s, err := New(t.TempDir())
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	data := []byte("你好，Aquarius")
	ref, err := s.Put(context.Background(), bytes.NewReader(data), "text/plain", "note.txt")
	if err != nil {
		t.Fatalf("put: %v", err)
	}
	if len(ref.Hash) != 64 || ref.MIME != "text/plain" || ref.Name != "note.txt" || ref.Size != int64(len(data)) {
		t.Fatalf("ref = %+v", ref)
	}
	rc, err := s.Get(context.Background(), ref)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	got, _ := io.ReadAll(rc)
	_ = rc.Close()
	if !bytes.Equal(got, data) {
		t.Fatalf("content = %q", got)
	}
	if ok, err := s.Stat(context.Background(), ref); err != nil || !ok {
		t.Fatalf("stat = %v, %v", ok, err)
	}
}

// TestPutDedup 同内容只存一份：两次 Put 同哈希，目录只有一个内容文件。
func TestPutDedup(t *testing.T) {
	dir := t.TempDir()
	s, err := New(dir)
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	data := []byte("same content")
	r1, err := s.Put(context.Background(), bytes.NewReader(data), "a", "1.bin")
	if err != nil {
		t.Fatalf("put1: %v", err)
	}
	r2, err := s.Put(context.Background(), bytes.NewReader(data), "b", "2.bin")
	if err != nil {
		t.Fatalf("put2: %v", err)
	}
	if r1.Hash != r2.Hash {
		t.Fatalf("hash = %s vs %s", r1.Hash, r2.Hash)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Fatalf("files = %d, want 1", len(entries))
	}
}

// TestStatMissingAndInvalidHash 缺失引用返回 false；非法哈希（穿越）报错。
func TestStatMissingAndInvalidHash(t *testing.T) {
	s, err := New(t.TempDir())
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	missing := conversation.BlobRef{Hash: strings.Repeat("a", 64)}
	if ok, err := s.Stat(context.Background(), missing); err != nil || ok {
		t.Fatalf("stat = %v, %v", ok, err)
	}
	for _, bad := range []string{"", "../evil", strings.Repeat("A", 64), strings.Repeat("z", 64)} {
		if _, err := s.Stat(context.Background(), conversation.BlobRef{Hash: bad}); err == nil {
			t.Fatalf("非法哈希 %q 应报错", bad)
		}
		if _, err := s.Get(context.Background(), conversation.BlobRef{Hash: bad}); err == nil {
			t.Fatalf("Get 非法哈希 %q 应报错", bad)
		}
	}
}

// TestGetMissing 缺失附件报错（装配据此明确报因）。
func TestGetMissing(t *testing.T) {
	s, err := New(t.TempDir())
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	if _, err := s.Get(context.Background(), conversation.BlobRef{Hash: strings.Repeat("b", 64)}); err == nil {
		t.Fatal("缺失附件应报错")
	}
}

// TestGCKeepsReferenced 只清 keep 之外的内容文件与过期临时文件，不动进行中的临时文件。
func TestGCKeepsReferenced(t *testing.T) {
	dir := t.TempDir()
	s, err := New(dir)
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	ctx := context.Background()
	keepRef, _ := s.Put(ctx, bytes.NewReader([]byte("keep")), "text/plain", "k")
	dropRef, _ := s.Put(ctx, bytes.NewReader([]byte("drop")), "text/plain", "d")
	// 过期与新鲜的临时文件。
	stale := filepath.Join(dir, ".put-stale.tmp")
	if err := os.WriteFile(stale, []byte("x"), 0o644); err != nil {
		t.Fatalf("seed stale: %v", err)
	}
	old := time.Now().Add(-2 * time.Hour)
	if err := os.Chtimes(stale, old, old); err != nil {
		t.Fatalf("chtimes: %v", err)
	}
	fresh := filepath.Join(dir, ".put-fresh.tmp")
	if err := os.WriteFile(fresh, []byte("y"), 0o644); err != nil {
		t.Fatalf("seed fresh: %v", err)
	}

	if err := s.GC(ctx, map[string]bool{keepRef.Hash: true}); err != nil {
		t.Fatalf("gc: %v", err)
	}
	if ok, _ := s.Stat(ctx, keepRef); !ok {
		t.Fatal("keep 的附件不应被清")
	}
	if ok, _ := s.Stat(ctx, dropRef); ok {
		t.Fatal("未引用的附件应被清")
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Fatal("过期临时文件应被清")
	}
	if _, err := os.Stat(fresh); err != nil {
		t.Fatal("新鲜临时文件不应被清")
	}
}

// TestPutConcurrentDedup 并发 Put 同内容：无错误、只存一份。
func TestPutConcurrentDedup(t *testing.T) {
	s, err := New(t.TempDir())
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	data := bytes.Repeat([]byte("z"), 100000)
	var wg sync.WaitGroup
	refs := make([]conversation.BlobRef, 8)
	errs := make([]error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			refs[i], errs[i] = s.Put(context.Background(), bytes.NewReader(data), "x", "same.bin")
		}(i)
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("put %d: %v", i, err)
		}
		if refs[i].Hash != refs[0].Hash {
			t.Fatalf("hash %d = %s, want %s", i, refs[i].Hash, refs[0].Hash)
		}
	}
	entries, _ := os.ReadDir(s.dir)
	files := 0
	for _, e := range entries {
		if !strings.HasPrefix(e.Name(), ".put-") {
			files++
		}
	}
	if files != 1 {
		t.Fatalf("files = %d, want 1（且无临时残留）", files)
	}
}

// TestPutCanceled ctx 取消中断写入且不留临时文件。
func TestPutCanceled(t *testing.T) {
	dir := t.TempDir()
	s, err := New(dir)
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := s.Put(ctx, bytes.NewReader([]byte("x")), "text/plain", "x"); err == nil {
		t.Fatal("取消应报错")
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 0 {
		t.Fatalf("取消后残留: %v", entries)
	}
}

// TestNewValidation 空目录拒绝。
func TestNewValidation(t *testing.T) {
	if _, err := New("  "); err == nil {
		t.Fatal("空目录应报错")
	}
}
