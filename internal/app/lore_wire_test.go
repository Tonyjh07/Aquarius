package app

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Tonyjh07/Aquarius/internal/domain/conversation"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

// fakeLoreCatalog 角色卡目录替身：只实现 Load 用得上的部分，顺带数一数读了几次。
type fakeLoreCatalog struct {
	cards map[string]port.CharacterCard
	err   error
	loads int
}

func (f *fakeLoreCatalog) List(ctx context.Context) ([]port.CharacterInfo, error) {
	return nil, nil
}

func (f *fakeLoreCatalog) Load(ctx context.Context, slug string) (port.CharacterCard, error) {
	f.loads++
	if f.err != nil {
		return port.CharacterCard{}, f.err
	}
	c, ok := f.cards[slug]
	if !ok {
		return port.CharacterCard{}, port.ErrCharacterNotFound
	}
	return c, nil
}

// loreCardFixture 一张带常驻条目与按键条目的卡。
func loreCardFixture() *fakeLoreCatalog {
	return &fakeLoreCatalog{cards: map[string]port.CharacterCard{
		"aria": {
			Info: port.CharacterInfo{Slug: "aria", Display: "Aria"},
			Profile: port.CharacterProfile{BookEntries: []port.BookEntry{
				{Comment: "灰塔", Content: "塔的守门人叫 Old Marn", Keys: []string{"灰塔"}, Enabled: true},
				{Comment: "常驻设定", Content: "这是一条每轮都在的常驻条目", Constant: true, Enabled: true},
			}},
		},
	}}
}

// loreConv 一个绑了卡、说过一句话的会话。
func loreConv(t *testing.T, slug, text string) *conversation.Conversation {
	t.Helper()
	c := conversation.New(conversation.ID("lore1"), "t")
	c.Character = slug
	if _, err := c.Append(conversation.RoleUser,
		[]conversation.Part{{Kind: conversation.PartText, Text: text}}); err != nil {
		t.Fatal(err)
	}
	return c
}

// TestBuildRequestAttachesLore 命中条目并进首条 system，常驻条目不在这一段里（它在 persona）。
func TestBuildRequestAttachesLore(t *testing.T) {
	cat := loreCardFixture()
	a := newAgent(t, &scriptLLM{t: t}, &recorder{}, Deps{Characters: cat},
		Config{Lore: LoreOptions{Enabled: true}})
	c := loreConv(t, "aria", "我们去灰塔看看")

	req, err := a.buildRequest(context.Background(), c, nil)
	if err != nil {
		t.Fatalf("buildRequest: %v", err)
	}
	if len(req.Messages) == 0 || req.Messages[0].Role != port.PromptSystem {
		t.Fatalf("首条不是 system: %+v", req.Messages)
	}
	var first strings.Builder
	for _, p := range req.Messages[0].Content {
		first.WriteString(p.Text)
	}
	got := first.String()
	if !strings.Contains(got, "Old Marn") {
		t.Fatalf("首条 system 缺命中条目: %q", got)
	}
	if strings.Contains(got, "每轮都在") {
		t.Fatalf("常驻条目被激活器重复注入: %q", got)
	}
	// 只出现在 system 里：不额外多一条消息（不进树的直接证据）。
	for _, m := range req.Messages[1:] {
		for _, p := range m.Content {
			if strings.Contains(p.Text, "Old Marn") {
				t.Fatalf("命中条目出现在非首条消息里: %+v", m)
			}
		}
	}
}

// TestLoreActivationSkipsWhenOff 关掉时不读卡、不动请求。
func TestLoreActivationSkipsWhenOff(t *testing.T) {
	cat := loreCardFixture()
	a := newAgent(t, &scriptLLM{t: t}, &recorder{}, Deps{Characters: cat}, Config{})
	c := loreConv(t, "aria", "我们去灰塔看看")
	if act := a.loreActivation(context.Background(), c, c.Path()); !act.empty() {
		t.Fatalf("off 仍有激活: %+v", act)
	}
	if cat.loads != 0 {
		t.Fatalf("off 仍读了卡: %d 次", cat.loads)
	}
}

// TestLoreActivationCachesCards 同一 slug 只读一次卡：语料里有 17 MB 的卡，每轮读一遍太贵。
func TestLoreActivationCachesCards(t *testing.T) {
	cat := loreCardFixture()
	a := newAgent(t, &scriptLLM{t: t}, &recorder{}, Deps{Characters: cat},
		Config{Lore: LoreOptions{Enabled: true}})
	c := loreConv(t, "aria", "灰塔")
	for i := 0; i < 3; i++ {
		a.loreActivation(context.Background(), c, c.Path())
	}
	if cat.loads != 1 {
		t.Fatalf("loads = %d, want 1", cat.loads)
	}
}

// TestLoreActivationDegradesWhenCardMissing 卡读不出来时当作没有世界书，请求照常。
func TestLoreActivationDegradesWhenCardMissing(t *testing.T) {
	cases := []struct {
		name string
		cat  *fakeLoreCatalog
		slug string
	}{
		{"卡不在目录", loreCardFixture(), "nobody"},
		{"读卡报错", &fakeLoreCatalog{err: errors.New("disk on fire")}, "aria"},
		{"会话没绑卡", loreCardFixture(), ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a := newAgent(t, &scriptLLM{t: t}, &recorder{}, Deps{Characters: tc.cat},
				Config{Lore: LoreOptions{Enabled: true}})
			c := loreConv(t, tc.slug, "我们去灰塔看看")
			if act := a.loreActivation(context.Background(), c, c.Path()); !act.empty() {
				t.Fatalf("应当降级成没有世界书: %+v", act)
			}
			if _, err := a.buildRequest(context.Background(), c, nil); err != nil {
				t.Fatalf("降级时装配失败: %v", err)
			}
		})
	}
}

// TestLoreNoCatalog 没有注入角色卡目录时（直接构造 Agent 的测试）不启用世界书。
func TestLoreNoCatalog(t *testing.T) {
	a := newAgent(t, &scriptLLM{t: t}, &recorder{}, Deps{},
		Config{Lore: LoreOptions{Enabled: true}})
	c := loreConv(t, "aria", "灰塔")
	if act := a.loreActivation(context.Background(), c, c.Path()); !act.empty() {
		t.Fatalf("没有目录仍激活: %+v", act)
	}
}

// TestUsageReportCarriesLore /usage 拿得到激活条数，且与装配走同一份判定。
func TestUsageReportCarriesLore(t *testing.T) {
	cat := loreCardFixture()
	a := newAgent(t, &scriptLLM{t: t}, &recorder{}, Deps{Characters: cat},
		Config{Lore: LoreOptions{Enabled: true}})
	c := loreConv(t, "aria", "灰塔")
	rep, err := a.UsageReport(context.Background(), c)
	if err != nil {
		t.Fatalf("UsageReport: %v", err)
	}
	if rep.Lore.Hits != 1 || len(rep.Lore.Entries) != 1 {
		t.Fatalf("lore = %+v, want 1 hit", rep.Lore)
	}
	depth, budget, maxCtx := a.LoreFacts()
	if depth != DefaultLoreScanDepth || budget <= 0 || maxCtx <= 0 {
		t.Fatalf("facts = %d %d %d", depth, budget, maxCtx)
	}
}
