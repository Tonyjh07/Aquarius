package main

// main_model_test.go 模型配置：provider 列表归一与逐项报因、旧格式迁移拒绝、
// 上下文预算、模型富化与启用开关、输出器开关解析。

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Tonyjh07/Aquarius/internal/app"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

// TestModelNormalize D173 provider 列表校验矩阵：缺省归一（primary/name）与
// 非法配置逐项报因。
func TestModelNormalize(t *testing.T) {
	// good 每用例重建：Providers 底层数组随 struct 拷贝共享，先例污染后续用例。
	good := func() modelConfig {
		return modelConfig{Providers: []providerConfig{
			{Name: "a", BaseURL: "http://a", Models: []modelEntry{{Name: "m1"}, {Name: "m2"}}},
			{Name: "b", BaseURL: "http://b", Models: []modelEntry{{Name: "mb"}}},
		}}
	}
	t.Run("缺省归一", func(t *testing.T) {
		c := good()
		if err := c.normalize(); err != nil {
			t.Fatalf("normalize: %v", err)
		}
		if c.Primary != "a" || c.Name != "m1" {
			t.Fatalf("primary/name = %q/%q, want a/m1", c.Primary, c.Name)
		}
	})
	t.Run("显式 primary 与 name", func(t *testing.T) {
		c := good()
		c.Primary, c.Name = "b", "mb"
		if err := c.normalize(); err != nil {
			t.Fatalf("normalize: %v", err)
		}
	})
	t.Run("fallback 合法", func(t *testing.T) {
		c := good()
		c.Fallback = []fallbackEntry{{Provider: "b"}}
		if err := c.normalize(); err != nil {
			t.Fatalf("normalize: %v", err)
		}
	})
	t.Run("同 provider 不同模型可同时兜底", func(t *testing.T) {
		c := good()
		c.Fallback = []fallbackEntry{{Provider: "a", Model: "m2"}, {Provider: "b", Model: "mb"}}
		if err := c.normalize(); err != nil {
			t.Fatalf("normalize: %v", err)
		}
	})
	// 能力位（D173）：整链取**与**——任一环节缺某项就整轮按不支持处理。
	t.Run("能力位取与", func(t *testing.T) {
		c := good()
		if !c.chainCaps().Tools || c.chainCaps().Vision {
			t.Fatalf("openai-chat 缺省应只有工具+思考: %+v", c.chainCaps())
		}
		if !c.chainCaps().Tools || !c.chainCaps().Reasoning {
			t.Fatalf("openai-chat 缺省工具+思考应为开: %+v", c.chainCaps())
		}
		vision := []string{"tools", "vision"}
		c.Providers[0].Models[0].Caps = &vision
		if !c.chainCaps().Vision {
			t.Fatal("仅 primary 声明时（无 fallback）应报支持")
		}
		c.Fallback = []fallbackEntry{{Provider: "b"}}
		if c.chainCaps().Vision {
			t.Fatal("fallback 未声明 vision 时不应报支持")
		}
		c.Providers[1].Models[0].Caps = &vision
		if !c.chainCaps().Vision {
			t.Fatal("全链声明后应报支持")
		}
	})
	t.Run("协议缺省能力位", func(t *testing.T) {
		c := good()
		c.Providers[0].Kind = kindAnthropic
		caps := c.currentCaps()
		if !caps.Tools || !caps.Vision || caps.Reasoning {
			t.Fatalf("anthropic 缺省 = %+v, want 工具+图片", caps)
		}
		c.Providers[0].Kind = kindOpenAIResponses
		caps = c.currentCaps()
		if !caps.Tools || !caps.Vision || !caps.Reasoning {
			t.Fatalf("openai-responses 缺省 = %+v, want 全开", caps)
		}
		// 显式空数组 = 三项全关（与「键缺失」是两回事）。
		empty := []string{}
		c.Providers[0].Models[0].Caps = &empty
		if caps := c.currentCaps(); caps.Tools || caps.Reasoning || caps.Vision {
			t.Fatalf("显式空 caps = %+v, want 全关", caps)
		}
	})
	// 上下文窗口（D177）：预算 = min(模型窗口, 全局上限)；不声明 = 全局。
	t.Run("上下文窗口取小", func(t *testing.T) {
		c := good()
		if err := c.normalize(); err != nil {
			t.Fatalf("normalize: %v", err)
		}
		if got := c.effectiveMaxContext(64000); got != 64000 {
			t.Fatalf("未声明窗口应只看全局: %d", got)
		}
		if got := c.currentContextWindow(); got != 0 {
			t.Fatalf("未声明窗口 = %d, want 0", got)
		}
		c.Providers[0].Models[0].ContextWindow = 128000
		if got := c.effectiveMaxContext(64000); got != 64000 {
			t.Fatalf("全局更小时应取全局: %d", got)
		}
		if got := c.currentContextWindow(); got != 128000 {
			t.Fatalf("当前窗口 = %d, want 128000", got)
		}
		if got := c.effectiveMaxContext(200000); got != 128000 {
			t.Fatalf("模型窗口更小时应取模型: %d", got)
		}
		if got := c.effectiveMaxContext(0); got != 128000 {
			t.Fatalf("全局未设时应落到模型窗口: %d", got)
		}
	})
	t.Run("窗口只看当前模型", func(t *testing.T) {
		// 降级链上的小窗口不压当前预算（D177 口径三）：装配发生在换模型之前。
		c := good()
		if err := c.normalize(); err != nil {
			t.Fatalf("normalize: %v", err)
		}
		c.Providers[0].Models[0].ContextWindow = 128000
		c.Providers[1].Models[0].ContextWindow = 8000
		c.Fallback = []fallbackEntry{{Provider: "b"}}
		if got := c.effectiveMaxContext(200000); got != 128000 {
			t.Fatalf("预算不该被降级目标拉低: %d", got)
		}
		// 切到别的模型名后按新模型算。
		c.Name = "m2"
		c.Providers[0].Models[1].ContextWindow = 32000
		if got := c.effectiveMaxContext(200000); got != 32000 {
			t.Fatalf("切模型后应按新窗口: %d", got)
		}
	})
	for _, tc := range []struct {
		name string
		mut  func(*modelConfig)
		want string
	}{
		{"providers 空", func(c *modelConfig) { c.Providers = nil }, "providers 不可为空"},
		{"名字缺失", func(c *modelConfig) { c.Providers[0].Name = "" }, "name 不可为空"},
		{"名字重复", func(c *modelConfig) { c.Providers[1].Name = "a" }, "重复"},
		{"base_url 缺失", func(c *modelConfig) { c.Providers[0].BaseURL = "" }, "base_url 不可为空"},
		{"models 缺失", func(c *modelConfig) { c.Providers[0].Models = nil }, "models 不可为空"},
		{"模型名缺失", func(c *modelConfig) { c.Providers[0].Models[1].Name = "" }, "models[1].name 不可为空"},
		{"窗口负数", func(c *modelConfig) { c.Providers[0].Models[0].ContextWindow = -1 }, "context_window 不可为负数"},
		{"模型重复", func(c *modelConfig) { c.Providers[0].Models[1].Name = "m1" }, "模型 \"m1\" 重复"},
		{"kind 不认识", func(c *modelConfig) { c.Providers[0].Kind = "ollama" }, "kind \"ollama\" 不认识"},
		{"primary 未命中", func(c *modelConfig) { c.Primary = "zz" }, "不在 model.providers"},
		{"fallback 未命中", func(c *modelConfig) { c.Fallback = []fallbackEntry{{Provider: "zz"}} }, "不在 model.providers"},
		{"fallback 缺 provider", func(c *modelConfig) { c.Fallback = []fallbackEntry{{Model: "x"}} }, "都要写 provider"},
		{"fallback 是 primary 本身", func(c *modelConfig) { c.Fallback = []fallbackEntry{{Provider: "a"}} }, "不应含当前生效"},
		{"fallback 重复", func(c *modelConfig) { c.Fallback = []fallbackEntry{{Provider: "b"}, {Provider: "b"}} }, "重复项"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := good()
			tc.mut(&c)
			err := c.normalize()
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want 含 %q", err, tc.want)
			}
		})
	}
}

// TestRunLegacyProfileConfigRejected 旧单 provider 平铺字段（D110② 前格式）的
// profile 配置启动即显式报因并给出改写指引——绝不静默零值解析（D95 同口径）。
func TestRunLegacyProfileConfigRejected(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "profiles", "default"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(pointerTemplate), 0o644); err != nil {
		t.Fatal(err)
	}
	legacy := `{"model":{"name":"m","base_url":"http://127.0.0.1:1","api_key":"secret:X"},"ui":{"kind":"repl"}}`
	if err := os.WriteFile(filepath.Join(dir, "profiles", "default", "config.json"), []byte(legacy), 0o644); err != nil {
		t.Fatal(err)
	}
	var out, errBuf bytes.Buffer
	if code := run([]string{"-data", dir}, strings.NewReader(""), &out, &errBuf); code != 1 {
		t.Fatalf("code = %d, want 1", code)
	}
	msg := errBuf.String()
	for _, want := range []string{"旧格式", "平铺", "providers"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("stderr 缺 %q: %q", want, msg)
		}
	}
}

// TestRunLegacyProviderShapeRejected D173 前的 provider 列表形状（api_key 单值 /
// models 字符串 / provider 上的 vision / fallback 名字数组）启动即显式报因：泛键解析
// 会把它们静默读成零值，等于悄悄丢掉所有模型与密钥。
func TestRunLegacyProviderShapeRejected(t *testing.T) {
	cases := map[string]string{
		"api_key 单值":         `{"model":{"providers":[{"name":"p","base_url":"http://127.0.0.1:1","api_key":"secret:X","models":[{"name":"m"}]}],"name":"m"},"ui":{"kind":"repl"}}`,
		"models 字符串":         `{"model":{"providers":[{"name":"p","base_url":"http://127.0.0.1:1","keys":[{"api_key":"secret:X"}],"models":["m"]}],"name":"m"},"ui":{"kind":"repl"}}`,
		"provider 上的 vision": `{"model":{"providers":[{"name":"p","base_url":"http://127.0.0.1:1","keys":[{"api_key":"secret:X"}],"models":[{"name":"m"}],"vision":true}],"name":"m"},"ui":{"kind":"repl"}}`,
		"fallback 名字数组":      `{"model":{"providers":[{"name":"p","base_url":"http://127.0.0.1:1","keys":[{"api_key":"secret:X"}],"models":[{"name":"m"}]}],"fallback":["q"],"name":"m"},"ui":{"kind":"repl"}}`,
	}
	for name, cfg := range cases {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			prof := writeProfileLayout(t, dir, cfg)
			t.Setenv("X", "k")
			var out, errBuf bytes.Buffer
			if code := run([]string{"-data", dir}, strings.NewReader(""), &out, &errBuf); code != 1 {
				t.Fatalf("code = %d, want 1", code)
			}
			msg := errBuf.String()
			if !strings.Contains(msg, "手工改写") || !strings.Contains(msg, "不做自动迁移") {
				t.Fatalf("stderr 应给出迁移指引: %q", msg)
			}
			if _, err := os.Stat(filepath.Join(prof, "config.json")); err != nil {
				t.Fatalf("报因时不得改写配置: %v", err)
			}
		})
	}
}

// TestWiringContextBudget D177：装配根的三处消费者（压缩阈值 / /usage / 截断）都读
// maxContextBudget；它必须与 /model 展示的口径同源，否则会出现「显示还有空间，发出去
// 却被拒」这种分叉。
func TestWiringContextBudget(t *testing.T) {
	cfg := &fileConfig{}
	cfg.Limits.MaxContextTokens = 64000
	cfg.Model = modelConfig{
		Providers: []providerConfig{{
			Name: "a", BaseURL: "http://a",
			Models: []modelEntry{
				{Name: "plain"},
				{Name: "small", ContextWindow: 32000},
				{Name: "big", ContextWindow: 200000},
			},
		}},
	}
	if err := cfg.Model.normalize(); err != nil {
		t.Fatalf("normalize: %v", err)
	}
	w := &wiring{cfg: cfg}

	// 当前模型没声明窗口（normalize 把 name 落到 models[0] = plain）→ 全局。
	if got := w.maxContextBudget(); got != 64000 {
		t.Fatalf("未声明窗口 = %d, want 64000", got)
	}
	// 模型窗口更小 → 模型侧压。
	cfg.Model.Name = "small"
	if got := w.maxContextBudget(); got != 32000 {
		t.Fatalf("小窗口模型 = %d, want 32000", got)
	}
	if b := w.contextBudget(); b.Effective != 32000 || b.ModelWindow != 32000 || b.GlobalLimit != 64000 {
		t.Fatalf("contextBudget = %+v", b)
	}
	// 模型窗口更大 → 全局压。
	cfg.Model.Name = "big"
	if got := w.maxContextBudget(); got != 64000 {
		t.Fatalf("大窗口模型 = %d, want 64000", got)
	}
	// 全局未设 → 内置缺省兜底，再和模型取小。
	cfg.Limits.MaxContextTokens = 0
	if got := w.maxContextBudget(); got != app.DefaultMaxContextTokens {
		t.Fatalf("全局未设 = %d, want %d", got, app.DefaultMaxContextTokens)
	}
	cfg.Model.Name = "small"
	if got := w.maxContextBudget(); got != 32000 {
		t.Fatalf("全局未设 + 小窗口 = %d, want 32000", got)
	}
}

// TestModelEnrichModels D177：服务端 /models 只给名字，能力位与窗口来自配置——
// 补齐只填不删（列表可用性以服务端为准），未声明的模型保持零值。
func TestModelEnrichModels(t *testing.T) {
	m := modelConfig{
		Providers: []providerConfig{{
			Name: "a", BaseURL: "http://a", Kind: kindOpenAIResponses,
			Models: []modelEntry{
				{Name: "gpt-big", ContextWindow: 128000},
			},
		}},
	}
	if err := m.normalize(); err != nil {
		t.Fatalf("normalize: %v", err)
	}
	got := m.enrichModels([]port.ModelInfo{
		{Name: "gpt-big"}, {Name: "unlisted"},
	})
	if len(got) != 2 || got[0].Name != "gpt-big" || got[1].Name != "unlisted" {
		t.Fatalf("enrich = %+v", got)
	}
	if got[0].ContextWindow != 128000 {
		t.Fatalf("声明窗口未补齐: %+v", got[0])
	}
	// 未声明 caps → 按协议缺省（responses = 全开）。
	if !got[0].Caps.Tools || !got[0].Caps.Reasoning || !got[0].Caps.Vision {
		t.Fatalf("协议缺省能力未补齐: %+v", got[0])
	}
	if got[1].ContextWindow != 0 || got[1].Caps != (port.ModelCaps{}) {
		t.Fatalf("未声明的模型应保持零值: %+v", got[1])
	}
}

// TestModelEnabledFlag D180：模型启用位——键缺失 = 启用；缺省请求模型取首个启用项；
// `/model` 清单不列未启用的声明项（当前模型例外，它是正在跑的事实）；一家全关 = 启动校验拒绝。
func TestModelEnabledFlag(t *testing.T) {
	off, on := false, true
	m := modelConfig{
		Providers: []providerConfig{{
			Name: "a", BaseURL: "http://a",
			Models: []modelEntry{
				{Name: "m1", Enabled: &off, ContextWindow: 8000},
				{Name: "m2", ContextWindow: 128000},
				{Name: "m3", Enabled: &on},
			},
		}},
		// 未指定 name：normalize 落到首个启用项（m2），不是 m1。
	}
	if err := m.normalize(); err != nil {
		t.Fatalf("normalize: %v", err)
	}
	if m.Name != "m2" {
		t.Fatalf("缺省模型 = %q, want m2（首个启用）", m.Name)
	}
	if got := m.primaryProvider().models0(); got != "m2" {
		t.Fatalf("models0 = %q", got)
	}
	if got := m.primaryProvider().enabledModels(); len(got) != 2 || got[0] != "m2" || got[1] != "m3" {
		t.Fatalf("enabledModels = %v", got)
	}

	// 清单：m1 声明了但未启用 → 不列；m4 未声明 → 照旧列出（可用性以服务端为准）。
	got := m.enrichModels([]port.ModelInfo{{Name: "m1"}, {Name: "m2"}, {Name: "m4"}})
	if len(got) != 2 || got[0].Name != "m2" || got[1].Name != "m4" {
		t.Fatalf("enrich 应滤掉未启用项: %+v", got)
	}
	// 当前模型即使未启用也要留（否则用户以为配错了）。
	m.Name = "m1"
	got = m.enrichModels([]port.ModelInfo{{Name: "m1"}, {Name: "m2"}})
	if len(got) != 2 || got[0].Name != "m1" {
		t.Fatalf("当前模型应保留: %+v", got)
	}

	// 一家全关：启动校验拒绝，并指名是哪一家。
	bad := modelConfig{
		Providers: []providerConfig{{
			Name: "a", BaseURL: "http://a",
			Models: []modelEntry{{Name: "m1", Enabled: &off}},
		}},
	}
	if err := bad.normalize(); err == nil || !strings.Contains(err.Error(), "全部未启用") {
		t.Fatalf("全部未启用 = %v, want 拒绝", err)
	}
}

// TestLoadConfigOutput 输出器开关解析：键缺失 = false（保守），显式 true 生效。
func TestLoadConfigOutput(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "config.json")
	if err := os.WriteFile(p, []byte(`{"model":{"name":"m"}}`), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	cfg, err := loadConfig(p)
	if err != nil || cfg.Output.Notify {
		t.Fatalf("缺键 = %+v, err = %v", cfg.Output, err)
	}
	if err := os.WriteFile(p, []byte(`{"output":{"notify":true,"tts":false}}`), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	cfg, err = loadConfig(p)
	if err != nil || !cfg.Output.Notify || cfg.Output.TTS {
		t.Fatalf("output = %+v, err = %v", cfg.Output, err)
	}
}
