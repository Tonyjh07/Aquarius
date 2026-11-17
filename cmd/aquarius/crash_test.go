package main

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestWriteCrashHasStack 报告里必须有 panic 值与栈：只有一句「崩了」等于没写。
func TestWriteCrashHasStack(t *testing.T) {
	dir := t.TempDir()
	setCrashDir(dir)
	path, err := writeCrash("boom at the top")
	if err != nil {
		t.Fatalf("writeCrash: %v", err)
	}
	if !strings.HasPrefix(filepath.Base(path), "crash-") || !strings.HasSuffix(path, ".txt") {
		t.Fatalf("path = %q, want crash-<ts>.txt", path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read report: %v", err)
	}
	body := string(data)
	for _, want := range []string{"panic: boom at the top", "goroutine", "version: ", "platform: "} {
		if !strings.Contains(body, want) {
			t.Fatalf("report missing %q:\n%s", want, body)
		}
	}
}

// TestPendingCrashOnce 同一份报告只提示一次：记账后再问就是空的。
func TestPendingCrashOnce(t *testing.T) {
	dir := t.TempDir()
	if got := pendingCrash(dir); got != "" {
		t.Fatalf("empty dir gave %q", got)
	}
	old := filepath.Join(dir, "crash-20260101-000000.txt")
	newer := filepath.Join(dir, "crash-20260102-000000.txt")
	for _, p := range []string{old, newer} {
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// 取最新那份（文件名带时间戳，字典序即时间序）。
	if got := pendingCrash(dir); got != newer {
		t.Fatalf("pending = %q, want the newest %q", got, newer)
	}
	ackCrash(newer)
	if got := pendingCrash(dir); got != "" {
		t.Fatalf("after ack pending = %q, want empty", got)
	}
}

// TestPendingCrashIgnoresAckFile 记账文件本身与别的文件都不算报告。
func TestPendingCrashIgnoresAckFile(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"crash-ack", "aquarius-2026-01-01.log", "notes.txt"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if got := pendingCrash(dir); got != "" {
		t.Fatalf("pending = %q, want empty", got)
	}
}

// TestGuardTurnsPanicIntoExitCode 顶层 panic 转成非零码，且报告写入磁盘、stderr 有一句话。
func TestGuardTurnsPanicIntoExitCode(t *testing.T) {
	dir := t.TempDir()
	setCrashDir(dir)
	var stderr strings.Builder
	code := guard(func() int {
		var p *int
		_ = *p // 真 nil 解引用：不是手工 panic 才算「崩了」
		return 0
	}, &stderr)
	if code != crashExitCode {
		t.Fatalf("code = %d, want %d", code, crashExitCode)
	}
	if stderr.Len() == 0 {
		t.Fatal("stderr is empty, want a one-line notice")
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "crash-") && strings.HasSuffix(e.Name(), ".txt") {
			found = true
		}
	}
	if !found {
		t.Fatalf("no crash report in %s", dir)
	}
}

// TestGuardKeepsNormalExit 没 panic 时原样返回，不写文件。
func TestGuardKeepsNormalExit(t *testing.T) {
	dir := t.TempDir()
	setCrashDir(dir)
	if code := guard(func() int { return 7 }, io.Discard); code != 7 {
		t.Fatalf("code = %d, want 7", code)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Fatalf("unexpected files: %v", entries)
	}
}

// TestWriteCrashUnwritableDir 写不进去时返回错误，调用方仍按崩溃退出（不改变退出码）。
func TestWriteCrashUnwritableDir(t *testing.T) {
	// 路径被一个普通文件占住：MkdirAll 必然失败，报告写不出来。
	blocker := filepath.Join(t.TempDir(), "blocked")
	if err := os.WriteFile(blocker, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	setCrashDir(filepath.Join(blocker, "logs"))
	if _, err := writeCrash("boom"); err == nil {
		t.Fatal("want an error when the directory cannot be created")
	}
	var stderr strings.Builder
	if code := guard(func() int { panic("boom") }, &stderr); code != crashExitCode {
		t.Fatalf("code = %d, want %d even when the report cannot be written", code, crashExitCode)
	}
}
