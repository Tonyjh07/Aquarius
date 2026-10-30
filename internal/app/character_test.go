package app

import (
	"reflect"
	"strings"
	"testing"

	"github.com/Tonyjh07/Aquarius/internal/port"
)

// testCard 一张字段齐全的卡（投影测试共用）。
func testCard() port.CharacterCard {
	return port.CharacterCard{
		Info: port.CharacterInfo{Slug: "aria", Name: "Aria", Display: "Aria"},
		Profile: port.CharacterProfile{
			Description:  "helps {{user}}",
			Personality:  "calm",
			Scenario:     "on {{date}}",
			SystemPrompt: "Rules for {{char}}.{{original}}",
			BookEntries: []port.BookEntry{
				{Comment: "Lore", Content: "the {{char}} rules", Constant: true, Enabled: true},
				{Comment: "Off", Content: "SECRET", Constant: true, Enabled: false},
			},
			Examples:    "<START>\n{{user}}: hi\n{{char}}: hello {{user}}",
			FirstMes:    "Hi {{user}}, I am {{char}}.",
			PostHistory: "After history, stay {{char}}.{{original}}",
		},
	}
}

// TestCharacterPromptFromCard 卡字段与开关如实搬进组合器取值。
func TestCharacterPromptFromCard(t *testing.T) {
	cp := CharacterPromptFromCard(testCard(), CharacterOptions{PreferSystemPrompt: true, InjectConstantBook: true})
	if cp.Display != "Aria" || cp.Description != "helps {{user}}" || cp.Personality != "calm" ||
		cp.Scenario != "on {{date}}" || cp.System != "Rules for {{char}}.{{original}}" {
		t.Fatalf("prompt = %+v", cp)
	}
	if !cp.PreferSystem || !cp.InjectBook {
		t.Fatalf("switches not carried: %+v", cp)
	}
	if len(cp.BookEntries) != 2 {
		t.Fatalf("book entries = %d, want 2 (all, filtering is the composer's job)", len(cp.BookEntries))
	}
}

// TestProjectCharacterDefault 默认态投影：人格含卡模块、示例与尾部指令展开、原组合器不动。
func TestProjectCharacterDefault(t *testing.T) {
	base := NewPersonaComposer(PromptSpec{}, RuntimeEnv{}, fixedNow)
	start := base.ProjectCharacter(testCard(), CharacterOptions{InjectConstantBook: true, InjectExamples: true})

	if start.Slug != "aria" {
		t.Fatalf("slug = %q", start.Slug)
	}
	for _, want := range []string{
		"You are Aria.",
		"Description:\nhelps User", // {{user}} 缺省 = User
		"Scenario:\non 2026-10-18", // {{date}} = 展开当天
		"## Lore\nthe Aria rules",  // 常驻世界书 + 宏
	} {
		if !strings.Contains(start.Persona, want) {
			t.Fatalf("persona missing %q:\n%s", want, start.Persona)
		}
	}
	if strings.Contains(start.Persona, "SECRET") {
		t.Fatalf("disabled book entry leaked:\n%s", start.Persona)
	}
	if len(start.Missing) != 0 {
		t.Fatalf("missing = %v", start.Missing)
	}
	// 示例：<START> 保留为分块符，宏展开。
	wantEx := "<START>\nUser: hi\nAria: hello User"
	if start.Examples != wantEx {
		t.Fatalf("examples = %q, want %q", start.Examples, wantEx)
	}
	// 尾部指令：默认态 {{original}} 按空处理。
	if start.Tail != "After history, stay Aria." {
		t.Fatalf("tail = %q", start.Tail)
	}
	// 原组合器未被改动。
	if got, _ := base.Compose(); strings.Contains(got, "Aria") {
		t.Fatalf("ProjectCharacter mutated the base composer:\n%s", got)
	}
}

// TestProjectCharacterExamplesOff 开关关时示例恒空（尾部指令仍展开）。
func TestProjectCharacterExamplesOff(t *testing.T) {
	base := NewPersonaComposer(PromptSpec{}, RuntimeEnv{}, fixedNow)
	start := base.ProjectCharacter(testCard(), CharacterOptions{})
	if start.Examples != "" {
		t.Fatalf("examples = %q, want empty when InjectExamples is off", start.Examples)
	}
	if start.Tail == "" {
		t.Fatalf("tail must be expanded regardless of the examples switch")
	}
	// 常驻世界书关：人格里没有它，但条目仍在取值里（交回目录/回执处理）。
	if strings.Contains(start.Persona, "## Lore") {
		t.Fatalf("book must not enter when InjectConstantBook is off:\n%s", start.Persona)
	}
}

// TestProjectCharacterReplacement {{original}} 在替换态展开为 U（不含 @env）：人格的
// system 段与尾部指令两处都填上同一份 U。
func TestProjectCharacterReplacement(t *testing.T) {
	base := NewPersonaComposer(PromptSpec{Legacy: "MY RULES", UserName: "Sam"}, RuntimeEnv{}, fixedNow)
	start := base.ProjectCharacter(testCard(), CharacterOptions{PreferSystemPrompt: true, InjectConstantBook: true})

	if !strings.HasPrefix(start.Persona, "Rules for Aria.MY RULES") {
		t.Fatalf("replacement system must lead with U expanded:\n%s", start.Persona)
	}
	if strings.Count(start.Persona, "MY RULES") != 1 {
		t.Fatalf("U should appear once in the persona:\n%s", start.Persona)
	}
	if !strings.Contains(start.Persona, "Runtime environment:") {
		t.Fatalf("replacement mode must keep @env:\n%s", start.Persona)
	}
	if start.Tail != "After history, stay Aria.MY RULES" {
		t.Fatalf("tail {{original}} = %q, want U expanded", start.Tail)
	}
}

// TestProjectCharacterTailNoOriginal 卡里没写 {{original}} 时尾部指令原样（不追加 U）。
func TestProjectCharacterTailNoOriginal(t *testing.T) {
	card := testCard()
	card.Profile.PostHistory = "Stay in character."
	base := NewPersonaComposer(PromptSpec{Legacy: "MY RULES"}, RuntimeEnv{}, fixedNow)
	start := base.ProjectCharacter(card, CharacterOptions{PreferSystemPrompt: true})
	if start.Tail != "Stay in character." {
		t.Fatalf("tail = %q", start.Tail)
	}
}

// TestExpandCardTextNoCharacter 没有卡时原样返回（这些文本只在角色会话里消费）。
func TestExpandCardTextNoCharacter(t *testing.T) {
	c := NewPersonaComposer(PromptSpec{}, RuntimeEnv{}, fixedNow)
	in := "{{char}} and {{user}} and <START>"
	if got := c.ExpandCardText(in); got != in {
		t.Fatalf("ExpandCardText without a card = %q, want verbatim", got)
	}
}

// TestWithCharacterIsolation 绑定卡不改动被复制的组合器（副本各自独立）。
func TestWithCharacterIsolation(t *testing.T) {
	base := NewPersonaComposer(PromptSpec{Legacy: "RULES"}, RuntimeEnv{}, fixedNow)
	withCard := base.WithCharacter(&CharacterPrompt{Display: "Aria"})
	if !strings.Contains(mustCompose(t, withCard), "You are Aria.") {
		t.Fatalf("copy must include the card")
	}
	if strings.Contains(mustCompose(t, base), "Aria") {
		t.Fatalf("original must stay card-free")
	}
}

// mustCompose 组合并断言无缺失。
func mustCompose(t *testing.T, c *PersonaComposer) string {
	t.Helper()
	got, missing := c.Compose()
	if len(missing) != 0 {
		t.Fatalf("missing = %v", missing)
	}
	return got
}

// TestProjectCharacterOpenings 开场白合并列表：先 first_mes、后备用问候（保位、含空串），
// 宏在投影时逐个展开。
func TestProjectCharacterOpenings(t *testing.T) {
	card := testCard()
	card.Profile.Greetings = []string{"Alt {{char}}", ""}
	base := NewPersonaComposer(PromptSpec{UserName: "Sam"}, RuntimeEnv{}, fixedNow)
	start := base.ProjectCharacter(card, CharacterOptions{})

	want := []string{"Hi Sam, I am Aria.", "Alt Aria", ""}
	if !reflect.DeepEqual(start.Openings, want) {
		t.Fatalf("openings = %#v, want %#v", start.Openings, want)
	}
}

// TestCharacterStartGreeting 序号 1 起：<=1 = first_mes，n>1 = 备用问候；越界报因并列出可选数；
// 无开场白时返回空串（由调用方决定不落节点）。
func TestCharacterStartGreeting(t *testing.T) {
	cs := CharacterStart{Openings: []string{"first", "alt1", "alt2"}}
	for _, tc := range []struct {
		n    int
		want string
	}{{0, "first"}, {1, "first"}, {2, "alt1"}, {3, "alt2"}} {
		got, err := cs.Greeting(tc.n)
		if err != nil || got != tc.want {
			t.Fatalf("Greeting(%d) = (%q, %v), want %q", tc.n, got, err, tc.want)
		}
	}
	if _, err := cs.Greeting(4); err == nil || !strings.Contains(err.Error(), "3") {
		t.Fatalf("Greeting(4) err = %v, want 越界并列出 3 条", err)
	}
	if got, err := (CharacterStart{}).Greeting(1); err != nil || got != "" {
		t.Fatalf("Greeting on no openings = (%q, %v), want empty", got, err)
	}
}
