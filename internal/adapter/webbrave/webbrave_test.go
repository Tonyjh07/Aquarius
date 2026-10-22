package webbrave

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Tonyjh07/Aquarius/internal/port"
)

func newTestClient(t *testing.T, h http.Handler) *Client {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	c, err := New(Config{APIKey: "brave-key", BaseURL: srv.URL, Country: "CN", Lang: "zh-hans"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return c
}

func TestSearchMapsResultsAndParams(t *testing.T) {
	var query map[string][]string
	var token string
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/web/search" {
			t.Errorf("path = %q", r.URL.Path)
		}
		query = r.URL.Query()
		token = r.Header.Get("X-Subscription-Token")
		io.WriteString(w, `{"web":{"results":[
			{"title":"T","url":"https://e.com/a","description":"desc","page_age":"2026-06-01",
			 "profile":{"name":"Site"},"extra_snippets":["more","detail"]},
			{"title":"","url":""}]}}`)
	}))
	res, err := c.Search(context.Background(), port.WebQuery{Query: "golang", Count: 3, Freshness: "week"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if token != "brave-key" {
		t.Fatalf("X-Subscription-Token = %q", token)
	}
	if got := query["q"]; len(got) != 1 || got[0] != "golang" {
		t.Fatalf("q = %v", got)
	}
	if got := query["count"]; len(got) != 1 || got[0] != "3" {
		t.Fatalf("count = %v", got)
	}
	if got := query["freshness"]; len(got) != 1 || got[0] != "pw" {
		t.Fatalf("freshness = %v（week → pw）", got)
	}
	if got := query["country"]; len(got) != 1 || got[0] != "CN" {
		t.Fatalf("country = %v", got)
	}
	if got := query["search_lang"]; len(got) != 1 || got[0] != "zh-hans" {
		t.Fatalf("search_lang = %v", got)
	}
	if len(res.Items) != 1 || res.Provider != "brave" {
		t.Fatalf("结果 = %+v（无 URL 的条目应被丢弃）", res)
	}
	it := res.Items[0]
	if it.Title != "T" || it.Snippet != "desc" || it.SiteName != "Site" || it.Date != "2026-06-01" {
		t.Fatalf("字段映射 = %+v", it)
	}
	if it.Summary != "more detail" {
		t.Fatalf("extra_snippets 应拼进 Summary，得到 %q", it.Summary)
	}
	if it.Authority != -1 {
		t.Fatalf("Brave 不提供权威度，应为 -1，得到 %d", it.Authority)
	}
}

func TestSearchClampsCount(t *testing.T) {
	var query map[string][]string
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		query = r.URL.Query()
		io.WriteString(w, `{"web":{"results":[]}}`)
	}))
	if _, err := c.Search(context.Background(), port.WebQuery{Query: "q", Count: 99}); err != nil {
		t.Fatalf("Search: %v", err)
	}
	if got := query["count"]; len(got) != 1 || got[0] != "20" {
		t.Fatalf("count 应夹到 20，得到 %v", got)
	}
}

func TestSearchErrors(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		io.WriteString(w, `{"error":"bad query"}`)
	}))
	if _, err := c.Search(context.Background(), port.WebQuery{Query: "q"}); !errors.Is(err, port.ErrWebBadInput) {
		t.Fatalf("400 应标注 ErrWebBadInput，得到 %v", err)
	}

	c2 := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		io.WriteString(w, `{"error":"bad token"}`)
	}))
	if _, err := c2.Search(context.Background(), port.WebQuery{Query: "q"}); err == nil || errors.Is(err, port.ErrWebBadInput) {
		t.Fatalf("401 应是可降级错误，得到 %v", err)
	}

	if _, err := c2.Search(context.Background(), port.WebQuery{Query: "   "}); !errors.Is(err, port.ErrWebBadInput) {
		t.Fatalf("空查询 = %v", err)
	}
}

func TestNewRequiresKey(t *testing.T) {
	if _, err := New(Config{}); err == nil {
		t.Fatal("缺密钥应报错")
	}
}
