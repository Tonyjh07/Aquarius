package main

import (
	"bytes"
	"fmt"
	"io"
	"strings"
	"testing"
)

// TestPromptSpecMapping D144：config prompts 段 → app.PromptSpec 映射（模块树 + 有序引用 + 兼容单串）。
func TestPromptSpecMapping(t *testing.T) {
	cfg := &fileConfig{
		SystemPrompt: "LEGACY",
		Prompts: promptsConfig{
			Modules: map[string]promptModule{
				"a": {Text: "A"},
				"g": {Text: "G", Children: []string{"a"}},
			},
			Persona: []string{"g"},
		},
	}
	spec := cfg.promptSpec()
	if spec.Legacy != "LEGACY" || len(spec.Persona) != 1 || spec.Persona[0] != "g" {
		t.Fatalf("spec = %+v", spec)
	}
	if spec.Modules["a"].Text != "A" {
		t.Fatalf("模块 a = %+v", spec.Modules["a"])
	}
	if got := spec.Modules["g"]; got.Text != "G" || len(got.Children) != 1 || got.Children[0] != "a" {
		t.Fatalf("组 g = %+v", got)
	}
	empty := (&fileConfig{}).promptSpec() // 空 prompts ⇒ 无模块、无引用（默认人格由 composer 兜底）
	if len(empty.Modules) != 0 || len(empty.Persona) != 0 {
		t.Fatalf("空 spec = %+v", empty)
	}
}

// TestRunPersonaModulesE2E D144：prompts 模块树 → persona 首节点 = 展开结果（组头 + 多级/换行），
// 且随请求体发给模型；未引用的 system_prompt 不参与。
func TestRunPersonaModulesE2E(t *testing.T) {
	srv, reqs := scriptServer(t, []string{"好的"})
	dir := t.TempDir()
	cfg := fmt.Sprintf(`{"model":{"providers":[{"name":"p","base_url":%q,"api_key":"secret:X","models":["m"]}],"name":"m"},"ui":{"kind":"repl"},
  "system_prompt":"LEGACY-IGNORED-WHEN-PERSONA",
  "prompts":{"modules":{"identity":{"text":"IDENT"},"style":{"text":"STYLE"},"house":{"text":"HOUSE:","children":["identity","style"]}},"persona":["house"]},
  "limits":{"max_turns":8,"max_context_tokens":64000,"tool_output_chars":20000,"tool_timeout_sec":60}}`, srv.URL)
	writeProfileLayout(t, dir, cfg)
	t.Setenv("X", "k")

	var out bytes.Buffer
	if code := run([]string{"-data", dir}, strings.NewReader("hi\n/quit\n"), &out, io.Discard); code != 0 {
		t.Fatalf("code = %d, out = %q", code, out.String())
	}
	want := "HOUSE:\nIDENT\nSTYLE"
	c := loadTree(t, dir)
	persona := c.Path()[1]
	if persona.Content[0].Text != want {
		t.Fatalf("persona = %q, want %q", persona.Content[0].Text, want)
	}
	if !strings.Contains(string(reqs.at(0).body), "HOUSE:") {
		t.Fatalf("请求体缺组合人格: %.400s", reqs.at(0).body)
	}
	if strings.Contains(string(reqs.at(0).body), "LEGACY-IGNORED-WHEN-PERSONA") {
		t.Fatalf("未引用的 system_prompt 不应出现在 persona: %.400s", reqs.at(0).body)
	}
}
