package app

import (
	"context"
	"strings"
	"testing"

	"github.com/Tonyjh07/Aquarius/internal/domain/conversation"
	"github.com/Tonyjh07/Aquarius/internal/i18n"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

// loreCardWithKeys 一张既有常驻条目、又有按键条目的卡（/lore 用例）。
func loreCardWithKeys() port.CharacterCard {
	c := charCard()
	c.Profile.BookEntries = []port.BookEntry{
		{Comment: "常驻设定", Content: "每轮都在的设定", Constant: true, Enabled: true},
		{Comment: "灰塔", Content: "守门人叫 Old Marn", Keys: []string{"灰塔"}, Order: 100, Enabled: true},
		{Comment: "港口", Content: "港口在南边", Keys: []string{"港口"}, Order: 50, Enabled: true},
	}
	return c
}

// newLoreSession 建一个绑了卡的会话，并在里面说一句话。
func newLoreSession(t *testing.T, text string) (*Session, *recorder) {
	t.Helper()
	rec := &recorder{}
	cat := &fakeCharCatalog{cards: map[string]port.CharacterCard{"aria": loreCardWithKeys()}}
	agent := newAgent(t, &scriptLLM{t: t}, rec, Deps{Characters: cat},
		Config{Lore: LoreOptions{Enabled: true}})
	s, err := NewSession(context.Background(), SessionDeps{
		Store:         newMemStore(),
		Agent:         agent,
		IDs:           &seqIDs{},
		Clock:         fixedClock{testTime},
		UI:            rec,
		Persona:       NewPersonaComposer(PromptSpec{}, RuntimeEnv{}, fixedNow),
		Characters:    cat,
		CharacterOpts: CharacterOptions{InjectConstantBook: true},
	})
	if err != nil {
		t.Fatalf("new session: %v", err)
	}
	s.cur.Character = "aria"
	if _, err := s.cur.Append(conversation.RoleUser,
		[]conversation.Part{{Kind: conversation.PartText, Text: text}}); err != nil {
		t.Fatal(err)
	}
	return s, rec
}

// TestLoreCommandListsHits /lore 列出命中条目：标题、order 与条数都在；常驻条目不出现在列表里。
func TestLoreCommandListsHits(t *testing.T) {
	s, _ := newLoreSession(t, "我们去灰塔")
	out, err := s.execLore(context.Background(), port.Command{Name: "lore"})
	if err != nil {
		t.Fatalf("execLore: %v", err)
	}
	for _, want := range []string{"Aria", "灰塔", "100", i18n.Tf("session.lore.foot", "on", 1, "tok", estimateTokens("## 灰塔\n守门人叫 Old Marn"))} {
		if !strings.Contains(out, want) {
			t.Fatalf("/lore 缺 %q:\n%s", want, out)
		}
	}
	if n := strings.Count(out, "\n- "); n != 1 {
		t.Fatalf("列表行数 = %d, want 1:\n%s", n, out)
	}
	if strings.Contains(out, "常驻设定") {
		t.Fatalf("常驻条目出现在激活列表里:\n%s", out)
	}
	if strings.Contains(out, "港口") {
		t.Fatalf("没命中的条目出现在列表里:\n%s", out)
	}
}

// TestLoreCommandEmpty 没有命中时说清「一条都没中」，而不是给一张空表。
func TestLoreCommandEmpty(t *testing.T) {
	s, _ := newLoreSession(t, "今天天气不错")
	out, err := s.execLore(context.Background(), port.Command{Name: "lore"})
	if err != nil {
		t.Fatalf("execLore: %v", err)
	}
	// 参与检索的只有两条非常驻条目。
	if !strings.Contains(out, i18n.Tf("session.lore.empty", "total", 2)) {
		t.Fatalf("/lore 空态文案不对:\n%s", out)
	}
	if strings.Contains(out, "\n- ") {
		t.Fatalf("空态仍列了条目:\n%s", out)
	}
}

// TestLoreCommandNoCard 普通会话（没绑卡）直说没有世界书。
func TestLoreCommandNoCard(t *testing.T) {
	s, _ := newLoreSession(t, "灰塔")
	s.cur.Character = ""
	out, err := s.execLore(context.Background(), port.Command{Name: "lore"})
	if err != nil {
		t.Fatalf("execLore: %v", err)
	}
	if !strings.Contains(out, i18n.T("session.lore.none")) {
		t.Fatalf("/lore 无卡文案不对:\n%s", out)
	}
}

// TestUsageShowsLore /usage 在真有命中时多一行，说明上下文为什么变大。
func TestUsageShowsLore(t *testing.T) {
	s, _ := newLoreSession(t, "我们去灰塔")
	out, err := s.execUsage(context.Background(), port.Command{Name: "usage"})
	if err != nil {
		t.Fatalf("execUsage: %v", err)
	}
	// token 数是估算出来的，只取这句里与条数有关的那半段。
	head := strings.Split(i18n.Tf("session.usage.lore", "on", 1, "tok", 0), "·")[0]
	if !strings.Contains(out, head) {
		t.Fatalf("/usage 缺世界书一行（找 %q）:\n%s", head, out)
	}
	// 没命中时不加这一行（噪音）。
	s2, _ := newLoreSession(t, "今天天气不错")
	out2, err := s2.execUsage(context.Background(), port.Command{Name: "usage"})
	if err != nil {
		t.Fatalf("execUsage: %v", err)
	}
	if strings.Contains(out2, head) {
		t.Fatalf("没命中仍打了世界书一行:\n%s", out2)
	}
}
