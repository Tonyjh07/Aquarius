package app

import (
	"context"
	"testing"

	"github.com/Tonyjh07/Aquarius/internal/domain/conversation"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

func injectPart(text string) []conversation.Part {
	return []conversation.Part{{Kind: conversation.PartText, Text: text}}
}

// TestSessionDropsForeignInjection 目标会话不是当前会话的包在排空时丢弃，防切换瞬间串台。
// 用一条普通命令触发入口排空（不依赖空闲唤醒）。
func TestSessionDropsForeignInjection(t *testing.T) {
	s, llm, _ := newTestSession(t, newMemStore())
	ctx := context.Background()
	before := len(s.Current().Path())

	if err := s.Inject(ctx, port.Injection{
		Source: "poller:j1", Session: "other", Parts: injectPart("wrong session"),
	}); err != nil {
		t.Fatalf("Inject: %v", err)
	}
	if _, err := handleCmd(s, "help"); err != nil {
		t.Fatalf("/help: %v", err)
	}
	if got := len(s.Current().Path()); got != before {
		t.Fatalf("path 长度 = %d, want %d（跨会话包应丢弃）", got, before)
	}
	if len(llm.requests) != 0 {
		t.Fatalf("Generate 次数 = %d, want 0", len(llm.requests))
	}
}

// TestSessionSwitchCancelsWatchers /new、/switch、删除当前会话都撤销旧会话的 watcher。
func TestSessionSwitchCancelsWatchers(t *testing.T) {
	s, _, _ := newTestSession(t, newMemStore())
	var cancelled []conversation.ID
	s.cancelWatchers = func(id conversation.ID) { cancelled = append(cancelled, id) }

	oldID := s.Current().ID
	if _, err := handleCmd(s, "new", "乙"); err != nil {
		t.Fatalf("/new: %v", err)
	}
	if len(cancelled) != 1 || cancelled[0] != oldID {
		t.Fatalf("cancelled = %v, want [%s]", cancelled, oldID)
	}

	newID := s.Current().ID
	if _, err := handleCmd(s, "switch", string(oldID)); err != nil {
		t.Fatalf("/switch: %v", err)
	}
	if len(cancelled) != 2 || cancelled[1] != newID {
		t.Fatalf("cancelled = %v, want 第二项 %s", cancelled, newID)
	}

	s.confirmer = &scriptConfirmer{t: t, answers: []bool{true}}
	if _, err := handleCmd(s, "rmconv", string(oldID)); err != nil {
		t.Fatalf("/rmconv: %v", err)
	}
	if len(cancelled) != 3 || cancelled[2] != oldID {
		t.Fatalf("cancelled = %v, want 第三项 %s", cancelled, oldID)
	}
}
