package main

import (
	"encoding/json"
	"testing"
)

// TestProviderHeadersConfig 自定义请求头从 config 解析进 providerConfig（D253）；
// 缺键即 nil（不落盘、不上线）。
func TestProviderHeadersConfig(t *testing.T) {
	var cfg fileConfig
	raw := `{"model":{"providers":[{"name":"p","base_url":"u",
		"headers":{"X-Gateway":"route-a","X-Tenant":"t1"}}]}}`
	if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	h := cfg.Model.Providers[0].Headers
	if h["X-Gateway"] != "route-a" || h["X-Tenant"] != "t1" {
		t.Fatalf("headers = %+v, want X-Gateway=route-a X-Tenant=t1", h)
	}

	var empty fileConfig
	if err := json.Unmarshal([]byte(`{"model":{"providers":[{"name":"p","base_url":"u"}]}}`), &empty); err != nil {
		t.Fatalf("unmarshal empty: %v", err)
	}
	if empty.Model.Providers[0].Headers != nil {
		t.Fatalf("缺键 headers 应为 nil，得 %+v", empty.Model.Providers[0].Headers)
	}
}
