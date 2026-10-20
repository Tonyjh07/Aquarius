package main

// 首次运行模板的一致性守卫：模板是用户看到的第一份配置，它必须本身就是**合法的 v2 形状**
// （能被 loadConfig 解析、能过 normalize 校验），且带上 v2 真正的键——`kind`/`keys`/`caps`/
// `context_window`。改坏模板不会再靠「首次运行才炸」暴露。

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Tonyjh07/Aquarius/internal/port"
)

// TestDefaultConfigTemplateIsValidV2 模板可解析、可规范化，且含 v2 的模型/密钥形状。
func TestDefaultConfigTemplateIsValidV2(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(defaultConfig), 0o644); err != nil {
		t.Fatalf("写模板: %v", err)
	}
	cfg, err := loadConfig(path)
	if err != nil {
		t.Fatalf("模板应能解析: %v", err)
	}
	if err := cfg.Model.normalize(); err != nil {
		t.Fatalf("模板应能过校验: %v", err)
	}
	if len(cfg.Model.Providers) != 1 {
		t.Fatalf("providers = %d, want 1", len(cfg.Model.Providers))
	}
	p := cfg.Model.Providers[0]
	if p.Kind != kindOpenAIChat {
		t.Fatalf("kind = %q, want %q", p.Kind, kindOpenAIChat)
	}
	if refs := p.keyRefs(); len(refs) != 1 || refs[0] != "secret:AQUARIUS_OPENAI_KEY" {
		t.Fatalf("密钥池 = %v", refs)
	}
	// 两个模型：一个省略 caps（按协议缺省）、一个显式声明；都给窗口。
	m := p.modelByName("gpt-4o")
	if m == nil || m.Caps == nil {
		t.Fatalf("gpt-4o 应是显式能力声明: %+v", p.Models)
	}
	if got := p.capsOf(*m); got != (port.ModelCaps{Tools: true, Reasoning: true, Vision: true}) {
		t.Fatalf("gpt-4o 能力 = %+v", got)
	}
	for _, name := range []string{"gpt-4o-mini", "gpt-4o"} {
		e := p.modelByName(name)
		if e == nil || e.ContextWindow != 128000 {
			t.Fatalf("%s 窗口 = %+v, want 128000", name, e)
		}
	}
	if cfg.Model.Name != "gpt-4o-mini" {
		t.Fatalf("当前模型 = %q", cfg.Model.Name)
	}
}
