package app

import (
	"context"
	"errors"
	"testing"

	"github.com/Tonyjh07/Aquarius/internal/domain/conversation"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

// TestSessionListPublishesOnHandle 会话列表快照（D120⑥）：构造后即可读（当前项标记）、
// 命令改变列表后同点刷新（/new 增项、/title --id 改名、/rmconv 删项）。
func TestSessionListPublishesOnHandle(t *testing.T) {
	store := newMemStore()
	s, _ := newConfirmedSession(t, store, []bool{true})

	items := s.List()
	if len(items) != 1 {
		t.Fatalf("构造后列表 = %d 条, want 1", len(items))
	}
	idA := items[0].ID
	if !items[0].Current || idA != s.Current().ID {
		t.Fatalf("首项 = %+v, want 当前会话 %s", items[0], s.Current().ID)
	}
	if items[0].Messages != 1 { // Root 不计：仅 persona
		t.Fatalf("Messages = %d, want 1", items[0].Messages)
	}

	// /new：新增一条，Current 移到新会话。
	if _, err := handleCmd(s, "new", "乙"); err != nil {
		t.Fatalf("/new: %v", err)
	}
	idB := s.Current().ID
	items = s.List()
	if len(items) != 2 {
		t.Fatalf("/new 后列表 = %d 条, want 2", len(items))
	}
	if cur := currentOf(items); cur == nil || cur.ID != idB || cur.ID == idA {
		t.Fatalf("Current = %+v, want %s", cur, idB)
	}

	// /title --id：非当前会话改名反映到列表。
	if _, err := handleCmd(s, "title", "--id", string(idA), "甲改名"); err != nil {
		t.Fatalf("title --id: %v", err)
	}
	if it := findByID(s.List(), idA); it == nil || it.Title != "甲改名" {
		t.Fatalf("列表未反映改名: %+v", it)
	}
	// 当前会话仍指向乙、标题未串。
	if it := findByID(s.List(), idB); it == nil || it.Title != "乙" || !it.Current {
		t.Fatalf("当前会话项 = %+v, want 标题乙/Current", it)
	}

	// /rmconv：删非当前会话后列表减一。
	if _, err := handleCmd(s, "rmconv", string(idA)); err != nil {
		t.Fatalf("rmconv: %v", err)
	}
	items = s.List()
	if len(items) != 1 || items[0].ID != idB {
		t.Fatalf("rmconv 后列表 = %+v, want 仅 %s", items, idB)
	}
}

// TestSessionListUnknownBeforePublish 未发布快照时 List 返回 nil（零值 Session 可调用）。
func TestSessionListUnknownBeforePublish(t *testing.T) {
	var s Session
	if s.List() != nil {
		t.Fatal("未发布快照应返回 nil")
	}
}

// TestSessionListCarriesCharacterAvatar §5.17：会话列表项带出角色 slug（存于会话元数据）与
// 卡目录里的头像路径（装配时按 slug 现取）；非角色会话两者皆空。
func TestSessionListCarriesCharacterAvatar(t *testing.T) {
	ctx := context.Background()
	store := newMemStore()
	agent := newAgent(t, &scriptLLM{t: t}, &recorder{}, Deps{}, Config{})
	cat := &fakeCharCatalog{cards: map[string]port.CharacterCard{
		"alice": {Info: port.CharacterInfo{Slug: "alice", AvatarPath: `/cards/alice/avatar.png`}},
	}}
	s, err := NewSession(ctx, SessionDeps{
		Store:      store,
		Agent:      agent,
		IDs:        &seqIDs{},
		Clock:      fixedClock{testTime},
		Characters: cat,
	})
	if err != nil {
		t.Fatalf("new session: %v", err)
	}
	id := s.Current().ID

	// 绑定角色后重发快照：slug 进列表项，头像路径由卡目录映射而来。
	s.cur.Character = "alice"
	if err := store.Save(ctx, s.cur); err != nil {
		t.Fatalf("save: %v", err)
	}
	s.publishSessions(ctx)
	it := findByID(s.List(), id)
	if it == nil {
		t.Fatalf("列表缺会话 %s", id)
	}
	if it.Character != "alice" {
		t.Fatalf("Character = %q, want alice", it.Character)
	}
	if it.AvatarPath != `/cards/alice/avatar.png` {
		t.Fatalf("AvatarPath = %q, want 卡目录里的路径", it.AvatarPath)
	}

	// 解绑（非角色会话）：两者皆空，列表仍可用。
	s.cur.Character = ""
	if err := store.Save(ctx, s.cur); err != nil {
		t.Fatalf("save2: %v", err)
	}
	s.publishSessions(ctx)
	it = findByID(s.List(), id)
	if it.Character != "" || it.AvatarPath != "" {
		t.Fatalf("非角色会话 = (%q, %q), want 空", it.Character, it.AvatarPath)
	}
}

// TestSessionListAvatarSurvivesCatalogError §5.17：卡目录读失败不阻断列表——行内回落默认头像，
// slug 照常带出（会话本身仍可用）。
func TestSessionListAvatarSurvivesCatalogError(t *testing.T) {
	ctx := context.Background()
	store := newMemStore()
	agent := newAgent(t, &scriptLLM{t: t}, &recorder{}, Deps{}, Config{})
	s, err := NewSession(ctx, SessionDeps{
		Store:      store,
		Agent:      agent,
		IDs:        &seqIDs{},
		Clock:      fixedClock{testTime},
		Characters: &fakeCharCatalog{err: errors.New("卡目录读失败")},
	})
	if err != nil {
		t.Fatalf("new session: %v", err)
	}
	s.cur.Character = "alice"
	if err := store.Save(ctx, s.cur); err != nil {
		t.Fatalf("save: %v", err)
	}
	s.publishSessions(ctx)
	it := findByID(s.List(), s.Current().ID)
	if it == nil {
		t.Fatal("列表应仍可用")
	}
	if it.Character != "alice" || it.AvatarPath != "" {
		t.Fatalf("读失败时 = (%q, %q), want (alice, 空)", it.Character, it.AvatarPath)
	}
}

func currentOf(items []port.SessionSummary) *port.SessionSummary {
	for i := range items {
		if items[i].Current {
			return &items[i]
		}
	}
	return nil
}

func findByID(items []port.SessionSummary, id conversation.ID) *port.SessionSummary {
	for i := range items {
		if items[i].ID == id {
			return &items[i]
		}
	}
	return nil
}
