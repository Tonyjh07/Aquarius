package app

import (
	"context"
	"strings"
	"testing"

	"github.com/Tonyjh07/Aquarius/internal/domain/conversation"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

func injectPart(text string) []conversation.Part {
	return []conversation.Part{{Kind: conversation.PartText, Text: text}}
}

// TestSessionLandsInOtherSession 目标会话不是当前会话的包落到**那个会话**的树上（D261），
// 不塞进当前会话、也不丢。
func TestSessionLandsInOtherSession(t *testing.T) {
	store := newMemStore()
	s, llm, _ := newTestSession(t, store)
	ctx := context.Background()

	other := conversation.New("other", "另一个会话")
	if err := store.Save(ctx, other); err != nil {
		t.Fatalf("save other: %v", err)
	}
	before := len(s.Current().Path())

	if err := s.Inject(ctx, port.Injection{
		Source: "poller:j1", Session: "other", Parts: injectPart("别的会话的报告"),
	}); err != nil {
		t.Fatalf("Inject: %v", err)
	}
	if _, err := handleCmd(s, "help"); err != nil {
		t.Fatalf("/help: %v", err)
	}
	if got := len(s.Current().Path()); got != before {
		t.Fatalf("当前会话 path 长度 = %d, want %d（不得落进当前会话）", got, before)
	}
	if len(llm.requests) != 0 {
		t.Fatalf("Generate 次数 = %d, want 0", len(llm.requests))
	}
	got, err := store.Load(ctx, "other")
	if err != nil {
		t.Fatalf("reload other: %v", err)
	}
	if p := got.Path(); len(p) != 2 || p[1].Role != conversation.RoleInject {
		t.Fatalf("目标会话 path = %+v, want root + inject", p)
	}
}

// TestSessionSwitchKeepsWatchers 切换不撤销 watcher（D261）：/new、/switch 都不撤销；
// 删除会话才撤销——那是报告真的无处可落的时刻。
func TestSessionSwitchKeepsWatchers(t *testing.T) {
	s, _, _ := newTestSession(t, newMemStore())
	cancel := &fakeWatchCancel{}
	s.watchCancel = cancel

	oldID := s.Current().ID
	if _, err := handleCmd(s, "new", "乙"); err != nil {
		t.Fatalf("/new: %v", err)
	}
	if len(cancel.cancelled) != 0 {
		t.Fatalf("cancelled = %v, want 空（新建不撤销）", cancel.cancelled)
	}

	if _, err := handleCmd(s, "switch", string(oldID)); err != nil {
		t.Fatalf("/switch: %v", err)
	}
	if len(cancel.cancelled) != 0 {
		t.Fatalf("cancelled = %v, want 空（切换不撤销）", cancel.cancelled)
	}

	s.confirmer = &scriptConfirmer{t: t, answers: []bool{true}}
	if _, err := handleCmd(s, "rmconv", string(oldID)); err != nil {
		t.Fatalf("/rmconv: %v", err)
	}
	if len(cancel.cancelled) != 1 || cancel.cancelled[0] != oldID {
		t.Fatalf("cancelled = %v, want [%s]（删除才撤销）", cancel.cancelled, oldID)
	}
}

// TestReportSurvivesSwitchAway 转交与后台任务的验收场景（D261）：在 A 起的活报告迟到、
// 期间切到 B——报告落到 A 的树上；切回 A 时随历史回放出现在转写区。
func TestReportSurvivesSwitchAway(t *testing.T) {
	store := newMemStore()
	s, _, rec := newTestSession(t, store)
	ctx := context.Background()
	idA := s.Current().ID

	if _, err := handleCmd(s, "new", "乙"); err != nil {
		t.Fatalf("/new: %v", err)
	}
	if err := s.Inject(ctx, port.Injection{
		Source:  "job:j9",
		Parts:   injectPart("任务完成：构建通过"),
		Session: idA,
	}); err != nil {
		t.Fatalf("Inject: %v", err)
	}
	if _, err := handleCmd(s, "help"); err != nil { // 一次命令触发入口排空
		t.Fatalf("/help: %v", err)
	}

	stored, err := store.Load(ctx, idA)
	if err != nil {
		t.Fatalf("load A: %v", err)
	}
	path := stored.Path()
	if last := path[len(path)-1]; last.Source != "job:j9" {
		t.Fatalf("A 的末节点 = %+v, want job:j9", last)
	}

	before := len(rec.events)
	if _, err := handleCmd(s, "switch", string(idA)); err != nil {
		t.Fatalf("/switch: %v", err)
	}
	found := false
	for _, ev := range rec.events[before:] {
		h, ok := ev.(port.HistoryEvent)
		if !ok {
			continue
		}
		for _, p := range h.Message.Content {
			if strings.Contains(p.Text, "构建通过") {
				found = true
			}
		}
	}
	if !found {
		t.Fatal("切回 A 后回放里没有那条报告")
	}
}
