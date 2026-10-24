package main

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Tonyjh07/Aquarius/internal/adapter/secretbin"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

// baseWebConfig 一份能过模型校验的最小配置（联网段由各用例改写）。
func baseWebConfig(t *testing.T) *fileConfig {
	t.Helper()
	cfg := &fileConfig{
		Model: modelConfig{
			Providers: []providerConfig{{
				Name:    "deepseek",
				BaseURL: "https://api.deepseek.com",
				Keys:    []keyConfig{{APIKey: "secret:AQUARIUS_DEEPSEEK_API_KEY"}},
				Models:  []modelEntry{{Name: "deepseek-flash"}},
			}},
			Name: "deepseek-flash",
		},
	}
	if err := cfg.Model.normalize(); err != nil {
		t.Fatalf("模型段应能过校验: %v", err)
	}
	return cfg
}

func TestNormalizeWebAcceptsProviderLists(t *testing.T) {
	cfg := baseWebConfig(t)
	cfg.Web = webConfig{
		Search: webSearchConfig{
			Providers: []webProvider{
				{Kind: webKindDeepSeek},
				{Kind: webKindBrave, Key: "secret:AQUARIUS_BRAVE_API_KEY"},
			},
			Count: 8,
		},
		Fetch: webFetchConfig{
			Providers: []webProvider{{Kind: webKindDeepSeek, From: "deepseek"}, {Kind: webKindLocal}},
			MaxChars:  16000,
		},
	}
	if err := cfg.normalizeWeb(); err != nil {
		t.Fatalf("normalizeWeb: %v", err)
	}
}

func TestNormalizeWebEmptyIsFine(t *testing.T) {
	// 没配置 = 联网工具执行时报因，不是启动失败。
	if err := baseWebConfig(t).normalizeWeb(); err != nil {
		t.Fatalf("空 web 段应可启动: %v", err)
	}
}

func TestNormalizeWebRejectsUnknownKind(t *testing.T) {
	cfg := baseWebConfig(t)
	cfg.Web.Search.Providers = []webProvider{{Kind: "google"}}
	err := cfg.normalizeWeb()
	if err == nil || !strings.Contains(err.Error(), "web.search.providers[0].kind") {
		t.Fatalf("未知搜索提供商应报错，得到 %v", err)
	}

	cfg = baseWebConfig(t)
	cfg.Web.Fetch.Providers = []webProvider{{Kind: webKindBrave}}
	err = cfg.normalizeWeb()
	if err == nil || !strings.Contains(err.Error(), "web.fetch.providers[0].kind") {
		t.Fatalf("抓取侧不支持 brave，应报错，得到 %v", err)
	}
}

func TestNormalizeWebRejectsMissingProviderRef(t *testing.T) {
	cfg := baseWebConfig(t)
	cfg.Web.Fetch.Providers = []webProvider{{Kind: webKindDeepSeek, From: "nope"}}
	err := cfg.normalizeWeb()
	if err == nil || !strings.Contains(err.Error(), "nope") {
		t.Fatalf("引用不存在的 provider 应报错，得到 %v", err)
	}
}

func TestNormalizeWebRejectsOutOfRangeBudgets(t *testing.T) {
	cfg := baseWebConfig(t)
	cfg.Web.Search.Count = 50
	if err := cfg.normalizeWeb(); err == nil {
		t.Fatal("count 超出 1-20 应报错")
	}
	cfg = baseWebConfig(t)
	cfg.Web.Fetch.MaxChars = 100000
	if err := cfg.normalizeWeb(); err == nil {
		t.Fatal("max_chars 超出硬顶应报错")
	}
}

func TestWebProviderFromDefaultsToKind(t *testing.T) {
	if got := webProviderFrom(webProvider{Kind: webKindDeepSeek}); got != "deepseek" {
		t.Fatalf("缺省引用 = %q", got)
	}
	if got := webProviderFrom(webProvider{Kind: webKindDeepSeek, From: "ds2"}); got != "ds2" {
		t.Fatalf("显式引用 = %q", got)
	}
}

// TestNormalizeWebAcceptsExplicitEndpoint 联网条目自己填了端点就不必引用 provider：
// 端点、密钥与对话 provider 完全脱钩（D222）。
func TestNormalizeWebAcceptsExplicitEndpoint(t *testing.T) {
	cfg := baseWebConfig(t)
	cfg.Web = webConfig{
		Search: webSearchConfig{Providers: []webProvider{{
			Kind: webKindDeepSeek, BaseURL: "https://search.example.com/v1", Key: "secret:MY_SEARCH",
		}}},
		Fetch: webFetchConfig{Providers: []webProvider{{
			Kind: webKindDeepSeek, BaseURL: "https://fetch.example.com/v1",
		}}},
	}
	if err := cfg.normalizeWeb(); err != nil {
		t.Fatalf("独立端点应通过校验: %v", err)
	}
}

// TestNormalizeWebExplicitEndpointWithStaleFrom 引用失效但端点自己填了：不该拦启动
// （借不到密钥只让这一家不可用，装配根会警告）。
func TestNormalizeWebExplicitEndpointWithStaleFrom(t *testing.T) {
	cfg := baseWebConfig(t)
	cfg.Web.Fetch.Providers = []webProvider{{
		Kind: webKindDeepSeek, From: "gone", BaseURL: "https://fetch.example.com/v1",
	}}
	if err := cfg.normalizeWeb(); err != nil {
		t.Fatalf("自己填了端点就不该因失效引用报错: %v", err)
	}
}

// TestWebResolveEndpointPrecedence 取值优先级（D222）：显式字段 → 借 from → 回落
// 按侧派生的密钥名。借只补缺的那一项。
func TestWebResolveEndpointPrecedence(t *testing.T) {
	cfg := baseWebConfig(t) // 只有一家 deepseek，端点 https://api.deepseek.com，密钥 secret:AQUARIUS_DEEPSEEK_API_KEY
	fallback := deriveKeyName("deepseek_search")

	// ① 只写 kind：端点与密钥都借同名 provider。
	ep, err := cfg.resolveEndpoint("", "", "deepseek", fallback)
	if err != nil {
		t.Fatalf("只写 kind 应可解析: %v", err)
	}
	if ep.BaseURL != "https://api.deepseek.com" || ep.KeyRef != "secret:AQUARIUS_DEEPSEEK_API_KEY" ||
		ep.Borrowed != "deepseek" {
		t.Fatalf("全借 = %+v", ep)
	}

	// ② 自己填了两项：一点都不借。
	ep, err = cfg.resolveEndpoint("https://mine.example.com/v1", "secret:MINE", "deepseek", fallback)
	if err != nil || ep.BaseURL != "https://mine.example.com/v1" || ep.KeyRef != "secret:MINE" ||
		ep.Borrowed != "" {
		t.Fatalf("全显式 = %+v / %v", ep, err)
	}

	// ③ 只填端点：密钥仍借 provider 的第一把。
	ep, err = cfg.resolveEndpoint("https://mine.example.com/v1", "", "deepseek", fallback)
	if err != nil || ep.BaseURL != "https://mine.example.com/v1" ||
		ep.KeyRef != "secret:AQUARIUS_DEEPSEEK_API_KEY" || ep.Borrowed != "deepseek" {
		t.Fatalf("只填端点 = %+v / %v", ep, err)
	}

	// ④ 只填密钥：端点仍借。
	ep, err = cfg.resolveEndpoint("", "secret:MINE", "deepseek", fallback)
	if err != nil || ep.BaseURL != "https://api.deepseek.com" || ep.KeyRef != "secret:MINE" {
		t.Fatalf("只填密钥 = %+v / %v", ep, err)
	}

	// ⑤ 什么都不填、也不借：密钥回落按侧派生的名字。
	ep, err = cfg.resolveEndpoint("https://mine.example.com/v1", "", "", fallback)
	if err != nil || ep.KeyRef != "secret:"+fallback {
		t.Fatalf("回落派生名 = %+v / %v", ep, err)
	}
}

// TestWebResolveEndpointStaleFrom 借的条目不存在：端点没着落才报错；自己填了端点就
// 只当没借到密钥（装配根随后按回落名取）。
func TestWebResolveEndpointStaleFrom(t *testing.T) {
	cfg := baseWebConfig(t)
	if _, err := cfg.resolveEndpoint("", "", "gone", ""); err == nil {
		t.Fatal("端点无着落应报错")
	}
	ep, err := cfg.resolveEndpoint("https://mine.example.com/v1", "", "gone", "AQUARIUS_X_API_KEY")
	if err != nil || ep.BaseURL != "https://mine.example.com/v1" ||
		ep.KeyRef != "secret:AQUARIUS_X_API_KEY" || ep.Borrowed != "" {
		t.Fatalf("自己填了端点 = %+v / %v", ep, err)
	}
}

// TestDeepSeekWebUsesOwnEndpointAndKey 装配路径的取值（D222）：条目自己填的端点与密钥
// 直接生效，不再去看 model.providers 里那条同名的。
func TestDeepSeekWebUsesOwnEndpointAndKey(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		io.WriteString(w, `{"results":[]}`)
	}))
	defer srv.Close()

	cfg := baseWebConfig(t) // 对话 provider 在 https://api.deepseek.com，密钥 secret:AQUARIUS_DEEPSEEK_API_KEY
	bin, err := secretbin.New(t.TempDir())
	if err != nil {
		t.Fatalf("secretbin.New: %v", err)
	}
	ctx := context.Background()
	if err := bin.Set(ctx, "MY_SEARCH", "sk-own-9999"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	w := &wiring{cfg: cfg, secrets: newSecretChain(bin, io.Discard), stderr: io.Discard}
	c, err := w.deepseekWeb(ctx, webProvider{
		Kind:    webKindDeepSeek,
		BaseURL: srv.URL,
		Key:     "secret:MY_SEARCH",
	}, webSideSearch)
	if err != nil {
		t.Fatalf("deepseekWeb: %v", err)
	}
	if _, err := c.Search(ctx, port.WebQuery{Query: "q"}); err != nil {
		t.Fatalf("Search: %v", err)
	}
	if gotAuth != "Bearer sk-own-9999" {
		t.Fatalf("应使用条目自己的密钥，Authorization = %q", gotAuth)
	}
}

// TestDeepSeekWebFallsBackPerSide 两侧都不填时按侧回落（D222）：搜索与抓取各取自己
// 那格，不碰对话 provider 的 AQUARIUS_DEEPSEEK_API_KEY。
func TestDeepSeekWebFallsBackPerSide(t *testing.T) {
	cfg := baseWebConfig(t)
	cfg.Model.Providers[0].Keys = []keyConfig{{APIKey: ""}} // 对话 provider 没有可借的钥匙
	bin, err := secretbin.New(t.TempDir())
	if err != nil {
		t.Fatalf("secretbin.New: %v", err)
	}
	ctx := context.Background()
	for name, val := range map[string]string{
		"AQUARIUS_DEEPSEEK_SEARCH_API_KEY": "sk-search-1",
		"AQUARIUS_DEEPSEEK_FETCH_API_KEY":  "sk-fetch-2",
		"AQUARIUS_DEEPSEEK_API_KEY":        "sk-chat-3",
	} {
		if err := bin.Set(ctx, name, val); err != nil {
			t.Fatalf("Set %s: %v", name, err)
		}
	}
	w := &wiring{cfg: cfg, secrets: newSecretChain(bin, io.Discard), stderr: io.Discard}

	check := func(side webSide, want string) {
		t.Helper()
		var gotAuth string
		srv := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
			gotAuth = r.Header.Get("Authorization")
			io.WriteString(rw, `{"results":[]}`)
		}))
		defer srv.Close()
		c, err := w.deepseekWeb(ctx, webProvider{Kind: webKindDeepSeek, BaseURL: srv.URL}, side)
		if err != nil {
			t.Fatalf("%v: deepseekWeb: %v", side.label(), err)
		}
		if _, err := c.Search(ctx, port.WebQuery{Query: "q"}); err != nil {
			t.Fatalf("%v: Search: %v", side.label(), err)
		}
		if gotAuth != "Bearer "+want {
			t.Fatalf("%v 应回落自己的密钥槽，Authorization = %q", side.label(), gotAuth)
		}
	}
	check(webSideSearch, "sk-search-1")
	check(webSideFetch, "sk-fetch-2")
}
