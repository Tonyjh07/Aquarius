package atomicfile

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
)

// residue 列出 dir 中残留的临时文件（<base>.<随机>.tmp）。
func residue(t *testing.T, dir string) []string {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(dir, "*.tmp"))
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	return matches
}

// TestWriteFileNewFile 新建文件：内容正确、用调用方 perm、无临时残留。
func TestWriteFileNewFile(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "a.txt")
	if err := WriteFile(target, []byte("hello"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	data, err := os.ReadFile(target)
	if err != nil || string(data) != "hello" {
		t.Fatalf("content = %q, %v", data, err)
	}
	if r := residue(t, dir); len(r) != 0 {
		t.Fatalf("残留临时文件: %v", r)
	}
	if runtime.GOOS != "windows" { // Windows 无 POSIX 权限位，仅只读位有意义
		if fi, err := os.Stat(target); err != nil || fi.Mode().Perm() != 0o644 {
			t.Fatalf("perm = %v, err = %v, want 0644", fi.Mode().Perm(), err)
		}
	}
}

// TestWriteFileInheritsPerm 覆盖既有文件沿用其权限位（0600 覆盖后不放宽成 0644）。
func TestWriteFileInheritsPerm(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "secret.txt")
	if err := os.WriteFile(target, []byte("old"), 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := WriteFile(target, []byte("new"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	data, err := os.ReadFile(target)
	if err != nil || string(data) != "new" {
		t.Fatalf("content = %q, %v", data, err)
	}
	if runtime.GOOS != "windows" {
		if fi, err := os.Stat(target); err != nil || fi.Mode().Perm() != 0o600 {
			t.Fatalf("perm = %v, err = %v, want 继承 0600", fi.Mode().Perm(), err)
		}
	}
	if r := residue(t, dir); len(r) != 0 {
		t.Fatalf("残留临时文件: %v", r)
	}
}

// TestWriteFileConcurrent 并发写同一目标：全部成功、最终内容是某个完整写入值
// （不交错）、无残留——固定 tmp 名时代会互相覆盖/踩踏。
func TestWriteFileConcurrent(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "hot.txt")

	const n = 8
	payloads := make([]string, n)
	for i := range payloads {
		payloads[i] = fmt.Sprintf("payload-%02d-%s", i, strings.Repeat("x", 4096))
	}
	var wg sync.WaitGroup
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			errs[i] = WriteFile(target, []byte(payloads[i]), 0o644)
		}(i)
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("write %d: %v", i, err)
		}
	}
	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	got := string(data)
	found := false
	for _, p := range payloads {
		if got == p {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("最终内容不是任一完整写入值（发生交错），len = %d", len(got))
	}
	if r := residue(t, dir); len(r) != 0 {
		t.Fatalf("残留临时文件: %v", r)
	}
}

// TestWriteFileMissingDir 父目录不存在时报错（MkdirAll 归调用方）。
func TestWriteFileMissingDir(t *testing.T) {
	dir := t.TempDir()
	if err := WriteFile(filepath.Join(dir, "no", "such", "a.txt"), []byte("x"), 0o644); err == nil {
		t.Fatal("父目录缺失应报错")
	}
}
