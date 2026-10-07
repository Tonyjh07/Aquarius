package main

// provider 列表管理面测试（D110②/③）：快照映射、全量应用（改名保密钥、无关键
// 保留、fallback 悬挂清理）、校验矩阵与连通性测试（假 server，禁真实网络）。

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Tonyjh07/Aquarius/internal/adapter/uigui"
)

// writeProvidersConfig 双 provider 模板（fallback 链 + 无关键探测）。
func writeProvidersConfig(t *testing.T) (string, string) {
	t.Helper()
	cfgPath := filepath.Join(t.TempDir(), "config.json")
	cfg := `{
  "model": {
    "providers": [
      {"name": "a", "base_url": "http://a/v1", "api_key": "secret:KA", "models": ["m1"], "tokenizer": "", "custom_hint": "keep-me"},
      {"name": "b", "base_url": "http://b/v1", "api_key": "secret:KB", "models": ["mb"], "tokenizer": ""}
    ],
    "primary": "a",
    "fallback": ["b"],
    "name": "m1"
  },
  "ui": {"kind": "repl"}
}`
	if err := os.WriteFile(cfgPath, []byte(cfg), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return cfgPath, cfg
}

func TestProviderManagerSnapshot(t *testing.T) {
	cfgPath, _ := writeProvidersConfig(t)
	m := &providerManager{cfgPath: cfgPath, stderr: os.Stderr}
	snap := m.Snapshot()
	if len(snap) != 2 || snap[0].Name != "a" || snap[1].Name != "b" {
		t.Fatalf("snapshot = %+v", snap)
	}
	if !snap[0].Primary || snap[1].Primary {
		t.Fatalf("primary 标记 = %+v", snap)
	}
	if snap[0].Models != "m1" {
		t.Fatalf("models 展示 = %q", snap[0].Models)
	}
	// 旧格式/缺失文件：容错返回空（不阻断开窗）。
	if got := (&providerManager{cfgPath: "missing.json"}).Snapshot(); got != nil {
		t.Fatalf("缺失文件 snapshot = %+v, want nil", got)
	}
}

func TestProviderManagerApply(t *testing.T) {
	cfgPath, _ := writeProvidersConfig(t)
	m := &providerManager{cfgPath: cfgPath, stderr: os.Stderr}

	// 改名 a→a2（保 Orig 密钥与无关键）+ 删除 b + 新增 c。
	err := m.Apply("a2", []uigui.ProviderPatch{
		{Orig: "a", Name: "a2", BaseURL: "http://a2/v1", Models: "m1, m2", Tokenizer: "", Unsupported: "reasoning_effort"},
		{Orig: "", Name: "c", BaseURL: "http://c/v1", APIKey: "secret:KC", Models: "mc"},
	})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	cfg, err := loadConfig(cfgPath)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if len(cfg.Model.Providers) != 2 || cfg.Model.Providers[0].Name != "a2" || cfg.Model.Providers[1].Name != "c" {
		t.Fatalf("providers = %+v", cfg.Model.Providers)
	}
	if cfg.Model.Providers[0].APIKey != "secret:KA" {
		t.Fatalf("改名后 api_key 丢失: %+v", cfg.Model.Providers[0])
	}
	if len(cfg.Model.Providers[0].Models) != 2 || cfg.Model.Providers[0].Models[1] != "m2" {
		t.Fatalf("models 解析 = %+v", cfg.Model.Providers[0].Models)
	}
	if len(cfg.Model.Providers[0].UnsupportedParams) != 1 || cfg.Model.Providers[0].UnsupportedParams[0] != "reasoning_effort" {
		t.Fatalf("unsupported 解析 = %+v", cfg.Model.Providers[0].UnsupportedParams)
	}
	if cfg.Model.Primary != "a2" {
		t.Fatalf("primary = %q", cfg.Model.Primary)
	}
	if len(cfg.Model.Fallback) != 0 {
		t.Fatalf("fallback 悬挂未清理: %+v", cfg.Model.Fallback)
	}
	data, _ := os.ReadFile(cfgPath)
	if !strings.Contains(string(data), `"custom_hint": "keep-me"`) {
		t.Fatal("provider 条目无关键未保留（Q10 口径破坏）")
	}

	// APIKey 空 = 保持原值。
	if err := m.Apply("c", []uigui.ProviderPatch{
		{Orig: "a2", Name: "a2", BaseURL: "http://a2/v1", Models: "m1"},
		{Orig: "c", Name: "c", BaseURL: "http://c/v1", Models: "mc"},
	}); err != nil {
		t.Fatalf("Apply2: %v", err)
	}
	cfg, _ = loadConfig(cfgPath)
	if cfg.Model.Providers[1].APIKey != "secret:KC" {
		t.Fatalf("APIKey 空应保持原值: %+v", cfg.Model.Providers[1])
	}
}

func TestProviderManagerApplyValidation(t *testing.T) {
	cfgPath, _ := writeProvidersConfig(t)
	m := &providerManager{cfgPath: cfgPath, stderr: os.Stderr}
	ok := func(name, base, models string) uigui.ProviderPatch {
		return uigui.ProviderPatch{Orig: name, Name: name, BaseURL: base, Models: models}
	}
	for _, tc := range []struct {
		name    string
		primary string
		patches []uigui.ProviderPatch
		want    string
	}{
		{"空列表", "a", nil, "至少保留一个"},
		{"名字为空", "a", []uigui.ProviderPatch{{Name: "", BaseURL: "http://x", Models: "m"}}, "名字不可为空"},
		{"名字重复", "a", []uigui.ProviderPatch{ok("a", "http://a", "m"), {Name: "a", BaseURL: "http://x", Models: "m"}}, "重复"},
		{"base_url 空", "a", []uigui.ProviderPatch{{Name: "a", BaseURL: " ", Models: "m"}}, "base_url 不可为空"},
		{"models 空", "a", []uigui.ProviderPatch{{Name: "a", BaseURL: "http://a", Models: ", "}}, "models 不可为空"},
		{"primary 未命中", "zz", []uigui.ProviderPatch{ok("a", "http://a", "m")}, "不在 provider 列表"},
	} {
		if err := m.Apply(tc.primary, tc.patches); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("%s: err = %v, want 含 %q", tc.name, err, tc.want)
		}
	}
}

func TestProviderManagerTest(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/models" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"data":[{"id":"m1"}]}`))
	}))
	t.Cleanup(srv.Close)

	cfgPath, _ := writeProvidersConfig(t)
	t.Setenv("KA", "k") // Orig 取回的 config 密钥引用 secret:KA
	m := &providerManager{cfgPath: cfgPath, stderr: os.Stderr}

	// 原密钥（APIKey 空按 Orig 取 config 值）+ 真实最小请求（假 server）。
	if err := m.Test(uigui.ProviderPatch{Orig: "a", Name: "a", BaseURL: srv.URL, Models: "m"}); err != nil {
		t.Fatalf("Test: %v", err)
	}
	// 显式密钥 + 失败路径。
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	t.Cleanup(bad.Close)
	if err := m.Test(uigui.ProviderPatch{Name: "x", BaseURL: bad.URL, APIKey: "secret:NOPE", Models: "m"}); err == nil {
		t.Fatal("401 应报错")
	}
	// base_url 非法。
	if err := m.Test(uigui.ProviderPatch{Name: "x", BaseURL: "not-a-url", APIKey: "k", Models: "m"}); err == nil || !errors.Is(err, err) {
		t.Fatal("非法 base_url 应报错")
	}
	// 密钥环境变量缺失 → 指名报因。
	t.Setenv("NOPE2", "")
	t.Setenv("AQ_MISSING", "")
	if err := m.Test(uigui.ProviderPatch{Name: "x", BaseURL: srv.URL, APIKey: "secret:AQ_MISSING", Models: "m"}); err == nil {
		t.Fatal("缺失密钥环境变量应报错")
	}
	_ = context.Background()
}
