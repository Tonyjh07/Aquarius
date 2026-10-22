package main

import (
	"strings"
	"testing"
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
