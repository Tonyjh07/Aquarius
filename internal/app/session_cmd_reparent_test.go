package app

import (
	"context"
	"strings"
	"testing"

	"github.com/Tonyjh07/Aquarius/internal/domain/conversation"
	"github.com/Tonyjh07/Aquarius/internal/i18n"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

// reparentFixture 造一棵 persona → q1 → a1 的会话，返回三个节点。
func reparentFixture(t *testing.T, s *Session) (persona, q1, a1 conversation.Message) {
	t.Helper()
	c := s.Current()
	persona = c.Path()[1]
	var err error
	if q1, err = c.Append(conversation.RoleUser, []conversation.Part{{Kind: conversation.PartText, Text: "q1"}}); err != nil {
		t.Fatalf("append q1: %v", err)
	}
	if a1, err = c.Append(conversation.RoleAssistant, []conversation.Part{{Kind: conversation.PartText, Text: "a1"}}); err != nil {
		t.Fatalf("append a1: %v", err)
	}
	return persona, q1, a1
}

// TestReparentCommandMovesAndPersists 命令面改父：子树挂到新父下、立即落盘，回执带条数与撤销提示。
func TestReparentCommandMovesAndPersists(t *testing.T) {
	store := newMemStore()
	s, _, _ := newTestSession(t, store)
	persona, _, a1 := reparentFixture(t, s)

	out, err := s.execCommand(context.Background(), port.Command{Name: "reparent", Args: []string{string(a1.ID), string(persona.ID)}})
	if err != nil {
		t.Fatalf("reparent: %v", err)
	}
	if !strings.Contains(out, i18n.Tf("reparent.done", "n", 1, "parent", persona.ID)) {
		t.Fatalf("output = %q, want 已改父 + 节点数", out)
	}
	got, _ := s.Current().Find(a1.ID)
	if got.Parent != persona.ID {
		t.Fatalf("a1.parent = %q, want %s", got.Parent, persona.ID)
	}
	// 落盘：store 里的那份也变了（不是只有内存）。
	saved, ok := store.convs[s.Current().ID]
	if !ok {
		t.Fatal("conversation not saved")
	}
	if m, _ := saved.Find(a1.ID); m.Parent != persona.ID {
		t.Fatalf("saved parent = %q, want %s（会话外应立即落盘）", m.Parent, persona.ID)
	}
	if n := len(s.Current().Detachments()); n != 1 {
		t.Fatalf("detachments = %d, want 1", n)
	}
}

// TestReparentCommandUndo 撤销：回到原父、记录被消费、回执说明撤销了什么。
func TestReparentCommandUndo(t *testing.T) {
	store := newMemStore()
	s, _, _ := newTestSession(t, store)
	persona, q1, a1 := reparentFixture(t, s)

	if _, err := s.execCommand(context.Background(), port.Command{Name: "reparent", Args: []string{string(a1.ID), string(persona.ID)}}); err != nil {
		t.Fatalf("reparent: %v", err)
	}
	out, err := s.execCommand(context.Background(), port.Command{Name: "reparent", Args: []string{"--undo"}})
	if err != nil {
		t.Fatalf("undo: %v", err)
	}
	if !strings.Contains(out, i18n.Tf("reparent.undone",
		"what", i18n.T("reparent.kind.move"), "n", 1, "anchor", q1.ID)) {
		t.Fatalf("output = %q, want 已撤销…挂回", out)
	}
	got, _ := s.Current().Find(a1.ID)
	if got.Parent != q1.ID {
		t.Fatalf("a1.parent after undo = %q, want %s", got.Parent, q1.ID)
	}
	if n := len(s.Current().Detachments()); n != 0 {
		t.Fatalf("detachments after undo = %d, want 0（撤销消费记录、不写新记录）", n)
	}
	// 再撤一次没有记录可撤：明确报错，不动树。
	if _, err := s.execCommand(context.Background(), port.Command{Name: "reparent", Args: []string{"--undo"}}); err == nil {
		t.Fatal("undo without records should fail")
	}
}

// TestReparentCommandRejectsRootAndBadArgs Root 不能作新父、参数不全给用法句。
func TestReparentCommandRejectsRootAndBadArgs(t *testing.T) {
	store := newMemStore()
	s, _, _ := newTestSession(t, store)
	persona, _, a1 := reparentFixture(t, s)
	_ = persona
	root := conversation.MessageID(s.Current().ID)

	if _, err := s.execCommand(context.Background(), port.Command{Name: "reparent", Args: []string{string(a1.ID), string(root)}}); err == nil {
		t.Fatal("reparent under root should fail")
	}
	if _, err := s.execCommand(context.Background(), port.Command{Name: "reparent", Args: []string{string(a1.ID)}}); err == nil ||
		err.Error() != i18n.T("usage.reparent") {
		t.Fatalf("short args = %v, want 用法 error", err)
	}
	if _, err := s.execCommand(context.Background(), port.Command{Name: "reparent", Args: []string{"--undo", "x"}}); err == nil {
		t.Fatal("--undo with extra arg should fail")
	}
	if err := s.Current().Validate(); err != nil {
		t.Fatalf("rejected commands broke tree: %v", err)
	}
}
