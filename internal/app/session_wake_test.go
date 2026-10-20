package app

import (
	"context"
	"testing"

	"github.com/Tonyjh07/Aquarius/internal/domain/conversation"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

// TestSessionWakeRunsTriggeredInjection 空闲唤醒：驱动式排空 + 自己跑一轮，落一条
// Trigger=true 的 inject 节点和它的 assistant 子节点，不追加 user 节点。
func TestSessionWakeRunsTriggeredInjection(t *testing.T) {
	s, llm, _ := newTestSession(t, newMemStore(), textStream("已汇报"))
	ctx := context.Background()
	before := len(s.Current().Path())

	if err := s.Inject(ctx, port.Injection{Source: "poller:j1", Parts: injectPart("Task j1 done")}); err != nil {
		t.Fatalf("Inject: %v", err)
	}
	out, err := s.Handle(ctx, port.UserInput{Wake: true})
	if err != nil || out != "" {
		t.Fatalf("Handle(Wake) = %q, %v", out, err)
	}
	path := s.Current().Path()
	if len(path) != before+2 {
		t.Fatalf("path 长度 = %d, want %d（inject + assistant）", len(path), before+2)
	}
	inject, assistant := path[len(path)-2], path[len(path)-1]
	if inject.Role != conversation.RoleInject || inject.Source != "poller:j1" || !inject.Trigger {
		t.Fatalf("inject = role %s source %q trigger %v", inject.Role, inject.Source, inject.Trigger)
	}
	if assistant.Role != conversation.RoleAssistant || assistant.Parent != inject.ID {
		t.Fatalf("assistant = role %s parent %s, want parent %s", assistant.Role, assistant.Parent, inject.ID)
	}
	if len(llm.requests) != 1 {
		t.Fatalf("Generate 次数 = %d, want 1", len(llm.requests))
	}
}

// TestSessionWakeNonTriggeredInjection 非触发注入只落地：不起轮、不落 user 节点。
func TestSessionWakeNonTriggeredInjection(t *testing.T) {
	s, llm, _ := newTestSession(t, newMemStore())
	ctx := context.Background()
	before := len(s.Current().Path())

	if err := s.Inject(ctx, port.Injection{Source: "tool:think", Parts: injectPart("note")}); err != nil {
		t.Fatalf("Inject: %v", err)
	}
	if _, err := s.Handle(ctx, port.UserInput{Wake: true}); err != nil {
		t.Fatalf("Handle(Wake): %v", err)
	}
	path := s.Current().Path()
	if len(path) != before+1 {
		t.Fatalf("path 长度 = %d, want %d", len(path), before+1)
	}
	if last := path[len(path)-1]; last.Role != conversation.RoleInject || last.Trigger {
		t.Fatalf("last = role %s trigger %v, want inject/false", last.Role, last.Trigger)
	}
	if len(llm.requests) != 0 {
		t.Fatalf("Generate 次数 = %d, want 0", len(llm.requests))
	}
}

// TestSessionWakeEmptyNoop 没有待落地注入时唤醒是 no-op。
func TestSessionWakeEmptyNoop(t *testing.T) {
	s, llm, _ := newTestSession(t, newMemStore())
	before := len(s.Current().Path())
	if _, err := s.Handle(context.Background(), port.UserInput{Wake: true}); err != nil {
		t.Fatalf("Handle(Wake): %v", err)
	}
	if got := len(s.Current().Path()); got != before {
		t.Fatalf("path 长度 = %d, want %d", got, before)
	}
	if len(llm.requests) != 0 {
		t.Fatalf("Generate 次数 = %d, want 0", len(llm.requests))
	}
}
