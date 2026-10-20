package main

// provider 列表管理面测试（D110②/③）：快照映射、全量应用（改名保密钥、无关键
// 保留、fallback 悬挂清理）、校验矩阵与连通性测试（假 server，禁真实网络）。

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
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
      {"name": "a", "base_url": "http://a/v1", "keys": [{"api_key": "secret:KA"}], "models": [{"name": "m1", "caps": ["tools", "vision"]}], "tokenizer": "", "custom_hint": "keep-me"},
      {"name": "b", "base_url": "http://b/v1", "keys": [{"api_key": "secret:KB"}], "models": [{"name": "mb"}], "tokenizer": ""}
    ],
    "primary": "a",
    "fallback": [{"provider": "b"}],
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
	m := &providerManager{cfgPath: cfgPath, secrets: testSecrets(t), stderr: os.Stderr}
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
	if got := (&providerManager{cfgPath: "missing.json", secrets: testSecrets(t)}).Snapshot(); got != nil {
		t.Fatalf("缺失文件 snapshot = %+v, want nil", got)
	}
}

func TestProviderManagerApply(t *testing.T) {
	cfgPath, _ := writeProvidersConfig(t)
	m := &providerManager{cfgPath: cfgPath, secrets: testSecrets(t), stderr: os.Stderr}

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
	if got := cfg.Model.Providers[0].keyRefs()[0]; got != "secret:KA" {
		t.Fatalf("改名后 api_key 丢失: %+v", cfg.Model.Providers[0])
	}
	if len(cfg.Model.Providers[0].Models) != 2 || cfg.Model.Providers[0].Models[1].Name != "m2" {
		t.Fatalf("models 解析 = %+v", cfg.Model.Providers[0].Models)
	}
	// 能力位按名字继承（D173）：改模型列表不该把已有的 caps 抹掉。
	if e := cfg.Model.Providers[0].modelByName("m1"); e == nil || e.Caps == nil {
		t.Fatalf("m1 的 caps 应保留: %+v", cfg.Model.Providers[0].Models)
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
	if got := cfg.Model.Providers[1].keyRefs()[0]; got != "secret:KC" {
		t.Fatalf("APIKey 空应保持原值: %+v", cfg.Model.Providers[1])
	}
}

func TestProviderManagerApplyValidation(t *testing.T) {
	cfgPath, _ := writeProvidersConfig(t)
	m := &providerManager{cfgPath: cfgPath, secrets: testSecrets(t), stderr: os.Stderr}
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

// TestProviderManagerApplyStoresPlaintext D161：编辑区填明文 → 写进本机密钥库 +
// config 只留 `secret:<名>`；config 字节里搜不到明文；写库失败即报错、config 不动。
func TestProviderManagerApplyStoresPlaintext(t *testing.T) {
	cfgPath, _ := writeProvidersConfig(t)
	ch, bin := testChain(t, nil)
	stores := 0
	m := &providerManager{cfgPath: cfgPath, secrets: ch, bin: bin, stderr: io.Discard, onStore: func() { stores++ }}

	// 改名 a→a2 并填新明文：键名沿用原引用名 KA（改名不 churn 引用）。
	err := m.Apply("a2", []uigui.ProviderPatch{
		{Orig: "a", Name: "a2", BaseURL: "http://a2/v1", APIKey: "sk-typed-plain-1", Models: "m1"},
		{Orig: "b", Name: "b", BaseURL: "http://b/v1", Models: "mb"},
	})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	cfg, err := loadConfig(cfgPath)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if got := cfg.Model.Providers[0].keyRefs()[0]; got != "secret:KA" {
		t.Fatalf("api_key = %q，want 沿用原引用名", got)
	}
	if got, _ := bin.Get(context.Background(), "KA"); got != "sk-typed-plain-1" {
		t.Fatalf("明文应已入库: %q", got)
	}
	if raw, _ := os.ReadFile(cfgPath); strings.Contains(string(raw), "sk-typed-plain-1") {
		t.Fatalf("config 里不该再出现明文密钥")
	}
	// 第二条没填密钥：原引用保持（只写不回显）。
	if got := cfg.Model.Providers[1].keyRefs()[0]; got != "secret:KB" {
		t.Fatalf("留空应保持原引用: %q", got)
	}
	// onStore 每次明文真的落库才回调（S9-6b/D163）：第一次 Apply 只填了一条 → 恰好 1 次。
	if stores != 1 {
		t.Fatalf("onStore 调用 = %d, want 1（只有一条填了明文）", stores)
	}

	// 新 provider 从空起步 + 填明文：键名按 provider 名派生。
	if err := m.Apply("a2", []uigui.ProviderPatch{
		{Orig: "a2", Name: "a2", BaseURL: "http://a2/v1", Models: "m1"},
		{Orig: "", Name: "My-API", BaseURL: "http://my/v1", APIKey: "sk-typed-plain-2", Models: "m2"},
	}); err != nil {
		t.Fatalf("Apply2: %v", err)
	}
	cfg, _ = loadConfig(cfgPath)
	if got := cfg.Model.Providers[1].keyRefs()[0]; got != "secret:AQUARIUS_MY_API_API_KEY" {
		t.Fatalf("新条目键名应派生: %q", got)
	}
	if got, _ := bin.Get(context.Background(), "AQUARIUS_MY_API_API_KEY"); got != "sk-typed-plain-2" {
		t.Fatalf("新条目明文应已入库: %q", got)
	}
	// 第二次 Apply 填了一条新明文 → 累计 2 次。
	if stores != 2 {
		t.Fatalf("onStore 累计 = %d, want 2", stores)
	}

	// 库未接线：填明文即报错，且 config 不被改动。
	before, _ := os.ReadFile(cfgPath)
	mNoBin := &providerManager{cfgPath: cfgPath, secrets: ch, stderr: io.Discard}
	if err := mNoBin.Apply("a2", []uigui.ProviderPatch{
		{Orig: "a2", Name: "a2", BaseURL: "http://a2/v1", APIKey: "sk-typed-plain-3", Models: "m1"},
	}); err == nil {
		t.Fatal("库未接线时填明文应报错（不得静默退回明文）")
	}
	if after, _ := os.ReadFile(cfgPath); string(after) != string(before) {
		t.Fatal("写库失败时 config 不应被改动")
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
	m := &providerManager{cfgPath: cfgPath, secrets: testSecrets(t), stderr: os.Stderr}

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

// TestProviderManagerTestRespectsKind kind = anthropic 的 provider：「测试/获取模型」
// 要按该协议造客户端（x-api-key + anthropic-version），否则拿 Bearer 打必然 401——
// 报回来的因还看不出是配置不对还是服务端的事。kind 不在编辑面，故按 Orig 名回查 config。
func TestProviderManagerTestRespectsKind(t *testing.T) {
	var mu sync.Mutex
	var apiKeys, versions, auths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		apiKeys = append(apiKeys, r.Header.Get("x-api-key"))
		versions = append(versions, r.Header.Get("anthropic-version"))
		auths = append(auths, r.Header.Get("Authorization"))
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"data":[{"id":"claude-x"}]}`))
	}))
	t.Cleanup(srv.Close)

	cfgPath := filepath.Join(t.TempDir(), "config.json")
	cfg := `{
  "model": {
    "providers": [
      {"name": "an", "kind": "anthropic", "base_url": "http://an/v1",
       "keys": [{"api_key": "secret:KA"}], "models": [{"name": "claude-x"}]}
    ],
    "primary": "an",
    "name": "claude-x"
  },
  "ui": {"kind": "repl"}
}`
	if err := os.WriteFile(cfgPath, []byte(cfg), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	t.Setenv("KA", "k-an")
	m := &providerManager{cfgPath: cfgPath, secrets: testSecrets(t), stderr: os.Stderr}

	got, err := m.Models(uigui.ProviderPatch{Orig: "an", Name: "an", BaseURL: srv.URL})
	if err != nil {
		t.Fatalf("Models: %v", err)
	}
	if len(got) != 1 || got[0] != "claude-x" {
		t.Fatalf("models = %v", got)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(apiKeys) != 1 || apiKeys[0] != "k-an" {
		t.Fatalf("x-api-key = %v, want k-an", apiKeys)
	}
	if len(versions) != 1 || versions[0] != "2023-06-01" {
		t.Fatalf("anthropic-version = %v", versions)
	}
	if auths[0] != "" {
		t.Fatalf("不该发 Authorization: %q", auths[0])
	}
}

// TestProviderManagerModels 「获取模型」数据面（D153）：去重保序、空 id 跳过、失败报因；
// 密钥解析与 Test 同口径（Orig 取 config 原值）。
func TestProviderManagerModels(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		// 服务端乱序 + 重复 + 空 id：只按响应顺序去重，不排序（用户看到的顺序即服务端顺序）。
		w.Write([]byte(`{"data":[{"id":"m2"},{"id":"m1"},{"id":"m2"},{"id":""},{"id":"m3"}]}`))
	}))
	t.Cleanup(srv.Close)
	cfgPath, _ := writeProvidersConfig(t)
	t.Setenv("KA", "k")
	m := &providerManager{cfgPath: cfgPath, secrets: testSecrets(t), stderr: os.Stderr}

	got, err := m.Models(uigui.ProviderPatch{Orig: "a", Name: "a", BaseURL: srv.URL})
	if err != nil {
		t.Fatalf("Models: %v", err)
	}
	// 顺序 = llm 适配器给的顺序（该层已按名字排序；本层只去重、不重排）。
	want := []string{"m1", "m2", "m3"}
	if len(got) != len(want) {
		t.Fatalf("Models = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("Models = %v, want %v（去重保序）", got, want)
		}
	}

	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	t.Cleanup(bad.Close)
	if _, err := m.Models(uigui.ProviderPatch{Name: "x", BaseURL: bad.URL, APIKey: "k"}); err == nil {
		t.Fatal("401 应报错")
	}
}
