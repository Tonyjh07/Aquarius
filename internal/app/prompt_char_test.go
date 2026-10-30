package app

import (
	"strings"
	"testing"

	"github.com/Tonyjh07/Aquarius/internal/port"
)

// composeChar 以固定时钟与零环境组合一份带卡的人格（测试共用的脚手架）。
func composeChar(t *testing.T, spec PromptSpec) string {
	t.Helper()
	got, missing := NewPersonaComposer(spec, RuntimeEnv{}, fixedNow).Compose()
	if len(missing) != 0 {
		t.Fatalf("missing = %v", missing)
	}
	return got
}

// envBlockZero 零环境的 @env 展开（仅日期行），供逐字节断言。
const envBlockZero = "Runtime environment:\n- Current date: 2026-10-18"

// TestComposeCharacterFields 卡字段进模块树（§5.17/D247）：字段各成独立叶子，
// 默认模式下用户人格在前、卡的人格段接上；空字段不出（不计缺失）。
func TestComposeCharacterFields(t *testing.T) {
	spec := PromptSpec{Character: &CharacterPrompt{
		Display:     "Aria",
		Description: "A helpful bot.",
		Personality: "Calm",
		Scenario:    "A quiet room",
	}}
	got := composeChar(t, spec)

	want := defaultSystem + "\n\n" + envBlockZero + "\n\n" +
		"You are Aria.\nDescription:\nA helpful bot.\nPersonality:\nCalm\nScenario:\nA quiet room"
	if got != want {
		t.Fatalf("character compose mismatch:\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}

	// 三个描述字段全空：仍出身份行（每个字段是独立叶子，空组不会连组头一起吞掉）。
	bare := composeChar(t, PromptSpec{Character: &CharacterPrompt{Display: "Aria"}})
	if !strings.Contains(bare, "You are Aria.") {
		t.Fatalf("identity line lost on a field-empty card:\n%s", bare)
	}
	if strings.Contains(bare, "Description:") {
		t.Fatalf("empty field must not emit its label:\n%s", bare)
	}
}

// TestComposeCharacterCardTextMacros 卡文本在投影时做宏替换（§7.7）：落树即终稿。
func TestComposeCharacterCardTextMacros(t *testing.T) {
	spec := PromptSpec{
		UserName: "Sam",
		Character: &CharacterPrompt{
			Display:     "Aria",
			Description: "{{char}} belongs to {{user}}.",
			Scenario:    "Today is {{date}}.",
		},
	}
	got := composeChar(t, spec)
	for _, want := range []string{"Description:\nAria belongs to Sam.", "Scenario:\nToday is 2026-10-18."} {
		if !strings.Contains(got, want) {
			t.Fatalf("macro not expanded in card text, missing %q:\n%s", want, got)
		}
	}
}

// TestComposeCharacterDefaultRefsAppend 默认模式引用 = 用户引用原样 + 追加卡模块；
// 卡的人格段排在用户人格之后（D243：用户人格不被默认覆盖）。
func TestComposeCharacterDefaultRefsAppend(t *testing.T) {
	spec := PromptSpec{
		Legacy:    "MY RULES",
		Character: &CharacterPrompt{Display: "Aria", Personality: "Calm"},
	}
	got := composeChar(t, spec)
	// 用户人格（@default）在前、卡段在后。
	iUser := strings.Index(got, "MY RULES")
	iChar := strings.Index(got, "You are Aria.")
	if iUser < 0 || iChar < 0 || iUser > iChar {
		t.Fatalf("user persona must lead the card section:\n%s", got)
	}
}

// TestComposeCharacterReplacementMode 替换态（prefer_system_prompt 且 S 非空）：
// 用户人格引用整体让位、@env 保留；{{original}} 展开为 U（不含 @env）。
func TestComposeCharacterReplacementMode(t *testing.T) {
	spec := PromptSpec{
		Legacy:    "MY RULES",
		UserName:  "Sam",
		Character: &CharacterPrompt{Display: "Aria", System: "Follow: {{original}}", PreferSystem: true},
	}
	got := composeChar(t, spec)

	if !strings.HasPrefix(got, "Follow: MY RULES") {
		t.Fatalf("replacement mode must lead with @char.system:\n%s", got)
	}
	// @env 保留（运行事实不能跟着人格一起丢）。
	if !strings.Contains(got, "Runtime environment:") {
		t.Fatalf("replacement mode must keep @env:\n%s", got)
	}
	// 卡的人格段始终在（替换只动主提示）。
	if !strings.Contains(got, "You are Aria.") {
		t.Fatalf("replacement mode keeps the character section:\n%s", got)
	}
	// U 只出现一次（不再由 @default 单独展开）。
	if n := strings.Count(got, "MY RULES"); n != 1 {
		t.Fatalf("U should appear exactly once, got %d:\n%s", n, got)
	}

	// S 为空则等同默认：用户人格照常在前。
	fallback := composeChar(t, PromptSpec{Legacy: "MY RULES", Character: &CharacterPrompt{Display: "Aria", PreferSystem: true}})
	if !strings.HasPrefix(fallback, "MY RULES") {
		t.Fatalf("empty S must fall back to default mode:\n%s", fallback)
	}
}

// TestComposeCharacterConstantBook 常驻世界书（§5.17/D247）：只取 constant 且 enabled 的
// 条目，标题作小节标题；开关关掉即不进上下文。
func TestComposeCharacterConstantBook(t *testing.T) {
	card := &CharacterPrompt{
		Display:    "Aria",
		InjectBook: true,
		BookEntries: []port.BookEntry{
			{Comment: "Lore", Content: "The {{char}} rules this room.", Constant: true, Enabled: true},
			{Comment: "Off", Content: "SECRET-OFF", Constant: true, Enabled: false},
			{Comment: "NotConst", Content: "NONCONST-XX", Constant: false, Enabled: true},
		},
	}
	got := composeChar(t, PromptSpec{Character: card})
	if !strings.Contains(got, "## Lore\nThe Aria rules this room.") {
		t.Fatalf("constant entry missing / not macro-expanded:\n%s", got)
	}
	if strings.Contains(got, "SECRET-OFF") || strings.Contains(got, "NONCONST-XX") {
		t.Fatalf("only constant && enabled entries may enter:\n%s", got)
	}

	// 开关关闭：条目保留在目录里，但不进上下文。
	card.InjectBook = false
	if off := composeChar(t, PromptSpec{Character: card}); strings.Contains(off, "## Lore") {
		t.Fatalf("inject off must not emit the book:\n%s", off)
	}

	// 标题为空：正文仍出（不作空小节标题）。
	card.InjectBook = true
	card.BookEntries = []port.BookEntry{{Content: "BODY-ONLY", Constant: true, Enabled: true}}
	if b := composeChar(t, PromptSpec{Character: card}); !strings.Contains(b, "BODY-ONLY") {
		t.Fatalf("body-only entry lost:\n%s", b)
	}
}

// TestComposeNoCharacterByteIdentical 安全绳（§5.17）：不带卡时输出逐字节不变——
// 这是整批角色卡改动的底线；`@char.*` 是条件内置，留在引用里也不改普通会话的输出。
func TestComposeNoCharacterByteIdentical(t *testing.T) {
	baseline := defaultSystem + "\n\n" + envBlockZero

	got, missing := NewPersonaComposer(PromptSpec{}, RuntimeEnv{}, fixedNow).Compose()
	if got != baseline {
		t.Fatalf("no-card baseline drifted:\n--- got ---\n%s\n--- want ---\n%s", got, baseline)
	}
	if len(missing) != 0 {
		t.Fatalf("baseline missing = %v", missing)
	}

	// 普通会话里引用 @char.*：模块为空、不计缺失、输出与不引用时相同。
	ref, missing := NewPersonaComposer(PromptSpec{Persona: []string{"@default", "@env", "@char", "@char.description"}}, RuntimeEnv{}, fixedNow).Compose()
	if ref != baseline {
		t.Fatalf("@char.* in a non-character session changed the output:\n%s", ref)
	}
	if len(missing) != 0 {
		t.Fatalf("@char.* must not count as missing, got %v", missing)
	}
}
