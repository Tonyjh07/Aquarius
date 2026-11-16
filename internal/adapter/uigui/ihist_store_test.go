package uigui

import (
	"path/filepath"
	"testing"

	"github.com/Tonyjh07/Aquarius/internal/inhistory"
)

// TestHistoryLoadedOnStart 构造界面时从落盘历史载入：重启后 ↑ 能翻到上次输入的句子。
func TestHistoryLoadedOnStart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "input_history.jsonl")
	seed := inhistory.Open(path)
	seed.Push("earlier sentence")

	u := newUI(Options{History: seed}, false)
	if got := u.ihist.lines; len(got) != 1 || got[0] != "earlier sentence" {
		t.Fatalf("ihist.lines = %v, want the persisted entry", got)
	}
}

// TestRememberInputPersists 提交一次输入就落一条：下次启动的文件里读得到。
func TestRememberInputPersists(t *testing.T) {
	path := filepath.Join(t.TempDir(), "input_history.jsonl")
	store := inhistory.Open(path)
	u := newUI(Options{History: store}, false)
	u.rememberInput("remember this")

	if got := u.ihist.lines; len(got) != 1 || got[0] != "remember this" {
		t.Fatalf("in-memory history = %v", got)
	}
	again := inhistory.Open(path).Lines()
	if len(again) != 1 || again[0] != "remember this" {
		t.Fatalf("persisted history = %v", again)
	}
}

// TestRememberInputWithoutStore 没注入存储时照旧只在内存里（headless 测试路径）。
func TestRememberInputWithoutStore(t *testing.T) {
	u := newUI(Options{}, false)
	u.rememberInput("memory only")
	if got := u.ihist.lines; len(got) != 1 || got[0] != "memory only" {
		t.Fatalf("in-memory history = %v", got)
	}
}
