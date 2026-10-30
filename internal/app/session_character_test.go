package app

import (
	"context"
	"strings"
	"testing"

	"github.com/Tonyjh07/Aquarius/internal/domain/conversation"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

// fakeCharCatalog 内存角色卡目录替身（§5.17）。
type fakeCharCatalog struct {
	cards map[string]port.CharacterCard
	err   error
}

func (f *fakeCharCatalog) List(context.Context) ([]port.CharacterInfo, error) {
	if f.err != nil {
		return nil, f.err
	}
	out := make([]port.CharacterInfo, 0, len(f.cards))
	for _, c := range f.cards {
		out = append(out, c.Info)
	}
	return out, nil
}

func (f *fakeCharCatalog) Load(_ context.Context, slug string) (port.CharacterCard, error) {
	if f.err != nil {
		return port.CharacterCard{}, f.err
	}
	c, ok := f.cards[slug]
	if !ok {
		return port.CharacterCard{}, port.ErrCharacterNotFound
	}
	return c, nil
}

// charCard 一张字段齐全的卡（会话侧测试共用），宏留待展开。
func charCard() port.CharacterCard {
	return port.CharacterCard{
		Info: port.CharacterInfo{Slug: "aria", Name: "Aria", Display: "Aria"},
		Profile: port.CharacterProfile{
			Description:  "A bot.",
			SystemPrompt: "Rules.",
			Examples:     "<START>\n{{user}}: hi\n{{char}}: hello",
			FirstMes:     "Hi {{user}}, I am {{char}}.",
			Greetings:    []string{"Second {{char}} opening."},
			PostHistory:  "Stay {{char}}.",
			BookEntries:  []port.BookEntry{{Comment: "Lore", Content: "lore", Constant: true, Enabled: true}},
		},
	}
}

// newCharSession 造一个带角色卡目录的会话（无脚本流：本组测试不打 Turn）。
func newCharSession(t *testing.T, store port.ConversationStore, opts CharacterOptions) (*Session, *recorder) {
	t.Helper()
	rec := &recorder{}
	llm := &scriptLLM{t: t}
	agent := newAgent(t, llm, rec, Deps{}, Config{})
	cat := &fakeCharCatalog{cards: map[string]port.CharacterCard{"aria": charCard()}}
	s, err := NewSession(context.Background(), SessionDeps{
		Store:         store,
		Agent:         agent,
		IDs:           &seqIDs{},
		Clock:         fixedClock{testTime},
		UI:            rec,
		Persona:       NewPersonaComposer(PromptSpec{}, RuntimeEnv{}, fixedNow),
		Characters:    cat,
		CharacterOpts: opts,
	})
	if err != nil {
		t.Fatalf("new session: %v", err)
	}
	return s, rec
}

// TestStartCharacterLandsPersonaExamplesGreeting /character start：新会话 = Root + persona
// 快照 + 示例注入节点 + 开场白；slug 与尾部指令记进元数据；Head 落在开场白。
func TestStartCharacterLandsPersonaExamplesGreeting(t *testing.T) {
	store := newMemStore()
	s, rec := newCharSession(t, store, CharacterOptions{InjectConstantBook: true, InjectExamples: true})
	before := s.Current().ID

	out, err := s.startCharacter(context.Background(), "aria", 0)
	if err != nil {
		t.Fatalf("startCharacter: %v", err)
	}
	if !strings.Contains(out, "Aria") {
		t.Fatalf("notice = %q, want the display name", out)
	}
	cur := s.Current()
	if cur.ID == before {
		t.Fatalf("current conversation did not change")
	}
	if cur.Character != "aria" {
		t.Fatalf("character = %q, want aria", cur.Character)
	}
	if cur.Tail != "Stay Aria." {
		t.Fatalf("tail = %q, want expanded post-history", cur.Tail)
	}
	path := cur.Path()
	if len(path) != 4 {
		t.Fatalf("path len = %d, want 4 (root, persona, examples, greeting)", len(path))
	}
	persona, ex, greeting := path[1], path[2], path[3]
	if persona.Role != conversation.RoleSystem || !strings.Contains(persona.Content[0].Text, "You are Aria.") ||
		!strings.Contains(persona.Content[0].Text, "## Lore") {
		t.Fatalf("persona = %+v", persona)
	}
	if ex.Role != conversation.RoleInject || ex.Source != "character:aria" || ex.Trust != conversation.TrustHost || ex.Trigger {
		t.Fatalf("examples node = %+v", ex)
	}
	if !strings.Contains(ex.Content[0].Text, "Aria: hello") {
		t.Fatalf("examples text = %q", ex.Content[0].Text)
	}
	if greeting.Role != conversation.RoleAssistant || greeting.Content[0].Text != "Hi User, I am Aria." {
		t.Fatalf("greeting = %+v", greeting)
	}
	if cur.Head != greeting.ID {
		t.Fatalf("head = %s, want greeting %s", cur.Head, greeting.ID)
	}
	// 落盘副本带齐元数据与节点。
	saved := store.convs[cur.ID]
	if saved == nil || saved.Character != "aria" || saved.Tail != "Stay Aria." || saved.NodeCount() != 4 {
		t.Fatalf("saved = %+v", saved)
	}
	// 清屏 + 回放：首事件为 ClearEvent，其后有回放提示。
	if len(rec.events) == 0 {
		t.Fatal("expected a clear event and replay")
	}
	if _, ok := rec.events[0].(port.ClearEvent); !ok {
		t.Fatalf("first event = %T, want ClearEvent", rec.events[0])
	}
}

// TestStartCharacterSelectsAlternateGreeting 序号 2 = alternate_greetings[0]（合并列表口径）。
func TestStartCharacterSelectsAlternateGreeting(t *testing.T) {
	s, _ := newCharSession(t, newMemStore(), CharacterOptions{InjectExamples: true})
	if _, err := s.startCharacter(context.Background(), "aria", 2); err != nil {
		t.Fatalf("startCharacter: %v", err)
	}
	path := s.Current().Path()
	last := path[len(path)-1]
	if last.Role != conversation.RoleAssistant || last.Content[0].Text != "Second Aria opening." {
		t.Fatalf("greeting = %+v", last)
	}
}

// TestStartCharacterOutOfRange 越界序号报错并保留当前会话（不落盘、不切换）。
func TestStartCharacterOutOfRange(t *testing.T) {
	store := newMemStore()
	s, _ := newCharSession(t, store, CharacterOptions{InjectExamples: true})
	before := s.Current().ID
	saves := store.saves

	_, err := s.startCharacter(context.Background(), "aria", 9)
	if err == nil || !strings.Contains(err.Error(), "2") {
		t.Fatalf("err = %v, want 越界并列出可选条数", err)
	}
	if s.Current().ID != before {
		t.Fatalf("current conversation changed on an out-of-range greeting")
	}
	if store.saves != saves {
		t.Fatalf("a rejected start must not save: saves %d -> %d", saves, store.saves)
	}
}

// TestStartCharacterEmptyGreetingNoNode 空开场不造假消息：只留 persona（无示例、无开场）。
func TestStartCharacterEmptyGreetingNoNode(t *testing.T) {
	cat := &fakeCharCatalog{cards: map[string]port.CharacterCard{
		"aria": func() port.CharacterCard {
			c := charCard()
			c.Profile.Examples = ""
			c.Profile.FirstMes = ""
			c.Profile.Greetings = nil
			c.Profile.PostHistory = ""
			return c
		}(),
	}}
	rec := &recorder{}
	agent := newAgent(t, &scriptLLM{t: t}, rec, Deps{}, Config{})
	s, err := NewSession(context.Background(), SessionDeps{
		Store: newMemStore(), Agent: agent, IDs: &seqIDs{}, Clock: fixedClock{testTime},
		UI: rec, Persona: NewPersonaComposer(PromptSpec{}, RuntimeEnv{}, fixedNow), Characters: cat,
	})
	if err != nil {
		t.Fatalf("new session: %v", err)
	}
	if _, err := s.startCharacter(context.Background(), "aria", 0); err != nil {
		t.Fatalf("startCharacter: %v", err)
	}
	cur := s.Current()
	if cur.NodeCount() != 2 {
		t.Fatalf("nodes = %d, want 2 (root + persona, no empty greeting)", cur.NodeCount())
	}
	if cur.Tail != "" {
		t.Fatalf("tail = %q, want empty", cur.Tail)
	}
}

// TestStartCharacterUnknownSlug 未导入的 slug 报因（包装 ErrCharacterNotFound）。
func TestStartCharacterUnknownSlug(t *testing.T) {
	s, _ := newCharSession(t, newMemStore(), CharacterOptions{})
	_, err := s.startCharacter(context.Background(), "nope", 0)
	if err == nil || !strings.Contains(err.Error(), "载入角色卡") {
		t.Fatalf("err = %v, want a load failure", err)
	}
}

// TestStartCharacterNoCatalog 未接目录时如实报未接入。
func TestStartCharacterNoCatalog(t *testing.T) {
	s, _, _ := newTestSession(t, newMemStore())
	if _, err := s.startCharacter(context.Background(), "aria", 0); err == nil {
		t.Fatal("missing catalog must error")
	}
}

// TestBuildRequestAppendsTail 会话尾部指令：开关开且非空时追加在历史之后（序列最末）。
func TestBuildRequestAppendsTail(t *testing.T) {
	llm := &scriptLLM{t: t}
	rec := &recorder{}
	a := newAgent(t, llm, rec, Deps{}, Config{ApplyPostHistory: true})
	c := conversation.New(conversation.ID("tail"), "t")
	commitNode(t, c, conversation.MessageID(c.ID), conversation.RoleSystem, "persona")
	commitNode(t, c, c.Head, conversation.RoleUser, "hi")
	c.Tail = "Stay in character."

	req, err := a.buildRequest(context.Background(), c, nil)
	if err != nil {
		t.Fatalf("buildRequest: %v", err)
	}
	last := req.Messages[len(req.Messages)-1]
	if last.Role != port.PromptSystem || msgText(last) != "Stay in character." {
		t.Fatalf("last message = %+v, want the tail system", last)
	}
	if req.Messages[len(req.Messages)-2].Role != port.PromptUser {
		t.Fatalf("tail must come after history: %+v", req.Messages)
	}
}

// TestBuildRequestTailSwitchOff 开关关时只存不发：Tail 仍在会话元数据里，但不进请求。
func TestBuildRequestTailSwitchOff(t *testing.T) {
	llm := &scriptLLM{t: t}
	rec := &recorder{}
	a := newAgent(t, llm, rec, Deps{}, Config{ApplyPostHistory: false})
	c := conversation.New(conversation.ID("tail-off"), "t")
	commitNode(t, c, conversation.MessageID(c.ID), conversation.RoleSystem, "persona")
	commitNode(t, c, c.Head, conversation.RoleUser, "hi")
	c.Tail = "Stay in character."

	req, err := a.buildRequest(context.Background(), c, nil)
	if err != nil {
		t.Fatalf("buildRequest: %v", err)
	}
	for _, m := range req.Messages {
		if msgText(m) == "Stay in character." {
			t.Fatalf("tail leaked with the switch off: %+v", req.Messages)
		}
	}
}

// TestBuildRequestBlankTailSkipped 空/纯空白 Tail 不追加空 system。
func TestBuildRequestBlankTailSkipped(t *testing.T) {
	llm := &scriptLLM{t: t}
	rec := &recorder{}
	a := newAgent(t, llm, rec, Deps{}, Config{ApplyPostHistory: true})
	c := conversation.New(conversation.ID("tail-blank"), "t")
	commitNode(t, c, conversation.MessageID(c.ID), conversation.RoleSystem, "persona")
	commitNode(t, c, c.Head, conversation.RoleUser, "hi")
	c.Tail = "   \n  "

	req, err := a.buildRequest(context.Background(), c, nil)
	if err != nil {
		t.Fatalf("buildRequest: %v", err)
	}
	if req.Messages[len(req.Messages)-1].Role != port.PromptUser {
		t.Fatalf("blank tail must not add a message: %+v", req.Messages)
	}
}
