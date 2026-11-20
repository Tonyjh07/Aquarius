package inhistory

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// tmpPath 一份新历史文件路径（目录存在，文件不存在）。
func tmpPath(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "input_history.jsonl")
}

// TestPushThenReopen 写入磁盘的意义就在这一条：重启后还读得到。
func TestPushThenReopen(t *testing.T) {
	path := tmpPath(t)
	s := Open(path)
	s.Push("first line")
	s.Push("second line")

	again := Open(path)
	lines := again.Lines()
	if len(lines) != 2 || lines[0] != "first line" || lines[1] != "second line" {
		t.Fatalf("reopened lines = %v", lines)
	}
}

// TestPushSkipsBlankAndAdjacent 空行不入历史；与最后一条相同的也不入（跨重启同样成立）。
func TestPushSkipsBlankAndAdjacent(t *testing.T) {
	path := tmpPath(t)
	s := Open(path)
	s.Push("  ")
	s.Push("same")
	s.Push("same")
	if got := s.Lines(); len(got) != 1 {
		t.Fatalf("lines = %v, want one entry", got)
	}
	// 重启后再推同一条：内存里已载入这一条，写侧就能判重，文件不会长出一行新的。
	again := Open(path)
	again.Push("same")
	if got := again.Lines(); len(got) != 1 {
		t.Fatalf("after reopen lines = %v, want one entry", got)
	}
	data, _ := os.ReadFile(path)
	if n := strings.Count(string(data), "\n"); n != 1 {
		t.Fatalf("file has %d lines, want 1", n)
	}
}

// TestBadLinesSkipped 坏行（截断的 JSON、手工编辑留下的裸文本）跳过，其余照读。
func TestBadLinesSkipped(t *testing.T) {
	path := tmpPath(t)
	body := "\"good one\"\nnot json\n\"good two\"\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	lines := Open(path).Lines()
	if len(lines) != 2 || lines[0] != "good one" || lines[1] != "good two" {
		t.Fatalf("lines = %v", lines)
	}
}

// TestKeepsOnlyMax 超过上限只留最近 Max 条，且顺序是旧的在前。
func TestKeepsOnlyMax(t *testing.T) {
	path := tmpPath(t)
	s := Open(path)
	for i := 0; i < Max+10; i++ {
		s.Push(fmt.Sprintf("entry %d", i))
	}
	lines := Open(path).Lines()
	if len(lines) != Max {
		t.Fatalf("lines = %d, want %d", len(lines), Max)
	}
	if lines[0] != "entry 10" || lines[Max-1] != fmt.Sprintf("entry %d", Max+9) {
		t.Fatalf("window = %q .. %q", lines[0], lines[Max-1])
	}
}

// TestCompactsLongFile 追加写会把文件拉长；过阈值后压实回 Max 条。
func TestCompactsLongFile(t *testing.T) {
	path := tmpPath(t)
	s := Open(path)
	for i := 0; i < compactAt+20; i++ {
		s.Push(fmt.Sprintf("entry %d", i))
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(string(data), "\n"); n > compactAt {
		t.Fatalf("file still has %d lines after compaction, want <= %d", n, compactAt)
	}
	if got := Open(path).Lines(); len(got) != Max {
		t.Fatalf("reopened lines = %d, want %d", len(got), Max)
	}
}

// TestEmptyPathStaysInMemory 空路径 = 只用内存（禁用写入磁盘时不报错、不写文件）。
func TestEmptyPathStaysInMemory(t *testing.T) {
	s := Open("")
	s.Push("only in memory")
	if got := s.Lines(); len(got) != 1 || got[0] != "only in memory" {
		t.Fatalf("lines = %v", got)
	}
}

// TestUnwritablePathDegrades 写不进去不影响内存里的历史（历史上限该退化成内存历史）。
func TestUnwritablePathDegrades(t *testing.T) {
	dir := t.TempDir()
	blocker := filepath.Join(dir, "blocked")
	if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	s := Open(filepath.Join(blocker, "sub", "input_history.jsonl"))
	s.Push("still remembered")
	if got := s.Lines(); len(got) != 1 {
		t.Fatalf("lines = %v, want the in-memory entry", got)
	}
}
