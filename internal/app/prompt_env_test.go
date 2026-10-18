package app

import (
	"strings"
	"testing"
	"time"
)

// fixedNow @env.date 断言用固定时刻（会话创建当天）。
func fixedNow() time.Time { return time.Date(2026, 10, 18, 9, 0, 0, 0, time.UTC) }

// TestPersonaComposerEnv D37/D144：环境信息内置模块逐字段展开；空字段不出行；
// @env.date 恒出（= 会话创建当天）；非绝对沙盒不附提示（139d585 口径）。
func TestPersonaComposerEnv(t *testing.T) {
	compose := func(env RuntimeEnv) string {
		got, missing := NewPersonaComposer(PromptSpec{}, env, fixedNow).Compose()
		if len(missing) != 0 {
			t.Fatalf("missing = %v", missing)
		}
		return got
	}

	// 零环境：默认人格 + 环境块（此时仅日期行）。
	got := compose(RuntimeEnv{})
	if !strings.HasPrefix(got, defaultSystem) {
		t.Fatalf("base prompt not kept:\n%s", got)
	}
	if !strings.Contains(got, "Runtime environment:") || !strings.Contains(got, "- Current date: 2026-10-18") {
		t.Fatalf("date line missing:\n%s", got)
	}
	for _, no := range []string{"- Platform:", "- Terminal:", "sandbox directory", "Tool timeout"} {
		if strings.Contains(got, no) {
			t.Fatalf("zero env must not add %q:\n%s", no, got)
		}
	}

	full := RuntimeEnv{
		Platform:   "windows/amd64",
		Terminal:   "tui",
		TERM:       "xterm-256color",
		SandboxDir: `C:\data\sandbox`,
	}
	got = compose(full)
	for _, want := range []string{
		"Runtime environment:",
		"- Platform: windows/amd64",
		"- Terminal: tui (TERM=xterm-256color)",
		`- Privileged sandbox directory: "C:\\data\\sandbox"`,
		"skip confirmation from the strict permission level upward",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("env block missing %q:\n%s", want, got)
		}
	}

	// TERM 缺省显示 unset；非绝对沙盒不附提示。
	got = compose(RuntimeEnv{Platform: "linux/amd64", Terminal: "repl", SandboxDir: "relative/dir"})
	if !strings.Contains(got, "- Terminal: repl (TERM unset)") {
		t.Fatalf("TERM unset missing:\n%s", got)
	}
	if strings.Contains(got, "sandbox directory") {
		t.Fatalf("relative sandbox must not be hinted:\n%s", got)
	}

	// 超时说明行（D38 发现性）：缺省秒数与保留参数口径；0 不附。
	got = compose(RuntimeEnv{Platform: "linux/amd64", ToolTimeoutSec: 60})
	if !strings.Contains(got, "- Tool timeout: default 60s per call") ||
		!strings.Contains(got, `"timeout_sec" argument (1-3600)`) {
		t.Fatalf("tool timeout line missing:\n%s", got)
	}
	if g := compose(RuntimeEnv{Platform: "linux/amd64"}); strings.Contains(g, "Tool timeout") {
		t.Fatalf("zero ToolTimeoutSec must not add the line:\n%s", g)
	}

	// 自定义人格（system_prompt → @default 正文）+ 环境块：基础在前、环境在后。
	composer := NewPersonaComposer(PromptSpec{Legacy: "Custom persona."}, RuntimeEnv{Platform: "darwin/arm64"}, fixedNow)
	got, _ = composer.Compose()
	if !strings.HasPrefix(got, "Custom persona.") || !strings.Contains(got, "darwin/arm64") {
		t.Fatalf("custom persona + env = %q", got)
	}
}

// TestPersonaComposeTree D144：模块树递归展开（组/叶/多级嵌套/组头/空组/缺名/环引用/默认引用）。
func TestPersonaComposeTree(t *testing.T) {
	t.Run("组与叶/多级嵌套/组头", func(t *testing.T) {
		spec := PromptSpec{
			Modules: map[string]PromptModule{
				"identity": {Text: "IDENT"},
				"style":    {Text: "STYLE"},
				"house":    {Text: "HOUSE:", Children: []string{"identity", "style"}}, // 组头 + 子
				"g1":       {Children: []string{"g2"}},                                // 多级：g1 → g2 → leaf
				"g2":       {Children: []string{"deep"}},
				"deep":     {Text: "DEEP"},
			},
			Persona: []string{"house", "g1"},
		}
		got, missing := NewPersonaComposer(spec, RuntimeEnv{}, fixedNow).Compose()
		if len(missing) != 0 {
			t.Fatalf("missing = %v", missing)
		}
		// 组内子以换行连接、组头在前；顶层以空行连接；多级嵌套展开到叶。
		if !strings.HasPrefix(got, "HOUSE:\nIDENT\nSTYLE") {
			t.Fatalf("group expansion wrong:\n%s", got)
		}
		if !strings.Contains(got, "\n\nDEEP") {
			t.Fatalf("multi-level nesting wrong:\n%s", got)
		}
	})

	t.Run("空组不出", func(t *testing.T) {
		spec := PromptSpec{
			Modules: map[string]PromptModule{
				"empty": {Text: "HEADER", Children: []string{"blank"}},
				"blank": {Text: "   "}, // 仅空白 ⇒ 视同无文本
				"keep":  {Text: "KEEP"},
			},
			Persona: []string{"empty", "keep"},
		}
		got, _ := NewPersonaComposer(spec, RuntimeEnv{}, fixedNow).Compose()
		if strings.Contains(got, "HEADER") {
			t.Fatalf("empty group must be dropped (incl. header):\n%s", got)
		}
		if !strings.Contains(got, "KEEP") {
			t.Fatalf("keep missing:\n%s", got)
		}
	})

	t.Run("缺名跳过并登记", func(t *testing.T) {
		spec := PromptSpec{
			Modules: map[string]PromptModule{"a": {Text: "A"}},
			Persona: []string{"a", "ghost"},
		}
		got, missing := NewPersonaComposer(spec, RuntimeEnv{}, fixedNow).Compose()
		if !strings.Contains(got, "A") {
			t.Fatalf("a missing:\n%s", got)
		}
		if len(missing) != 1 || missing[0] != "ghost" {
			t.Fatalf("missing = %v, want [ghost]", missing)
		}
	})

	t.Run("环引用截断", func(t *testing.T) {
		spec := PromptSpec{
			Modules: map[string]PromptModule{
				"x": {Text: "X", Children: []string{"x"}}, // 自指环
				"a": {Text: "A"},
			},
			Persona: []string{"x", "a"},
		}
		got, _ := NewPersonaComposer(spec, RuntimeEnv{}, fixedNow).Compose()
		// 自指环在 x 处截断 ⇒ x 无有效子文本 ⇒ 整组不出；兄弟 a 仍展开（且无死循环）。
		if strings.Contains(got, "X") {
			t.Fatalf("self-cycle group should be dropped:\n%s", got)
		}
		if !strings.Contains(got, "A") {
			t.Fatalf("sibling lost:\n%s", got)
		}
	})

	t.Run("默认引用与回落默认人格", func(t *testing.T) {
		// persona 空 ⇒ 默认 ["@default","@env"]。
		got, _ := NewPersonaComposer(PromptSpec{}, RuntimeEnv{}, fixedNow).Compose()
		if !strings.HasPrefix(got, defaultSystem) || !strings.Contains(got, "Runtime environment:") {
			t.Fatalf("default refs wrong:\n%s", got)
		}
		// persona 全部缺失 ⇒ 整段空 ⇒ 回落内置默认人格。
		spec := PromptSpec{Persona: []string{"nope"}}
		got, missing := NewPersonaComposer(spec, RuntimeEnv{}, fixedNow).Compose()
		if got != defaultSystem {
			t.Fatalf("fallback = %q, want defaultSystem", got)
		}
		if len(missing) != 1 || missing[0] != "nope" {
			t.Fatalf("missing = %v", missing)
		}
	})

	t.Run("用户模块 @ 前缀被忽略", func(t *testing.T) {
		spec := PromptSpec{
			Modules: map[string]PromptModule{"@env": {Text: "HACK"}}, // 占用保留名：忽略
			Persona: []string{"@env"},
		}
		got, _ := NewPersonaComposer(spec, RuntimeEnv{Platform: "x/y"}, fixedNow).Compose()
		if strings.Contains(got, "HACK") {
			t.Fatalf("reserved @name must be ignored:\n%s", got)
		}
		if !strings.Contains(got, "Runtime environment:") {
			t.Fatalf("builtin @env should win:\n%s", got)
		}
	})
}
