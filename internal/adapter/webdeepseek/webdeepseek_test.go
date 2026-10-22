package webdeepseek

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Tonyjh07/Aquarius/internal/port"
)

func newTestClient(t *testing.T, h http.Handler) *Client {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	c, err := New(Config{
		BaseURL: srv.URL,
		APIKey:  "test-key",
		Now:     func() time.Time { return time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC) },
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return c
}

func TestSearchMapsResults(t *testing.T) {
	var gotPath, gotAuth string
	var gotBody map[string]any
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotAuth = r.URL.Path, r.Header.Get("Authorization")
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &gotBody)
		io.WriteString(w, `{"results":[{"rerank_score":2.4,"title":"T","url":"https://e.com/a",
			"snippet":"short","summary":"long","site_name":"Site","date":"2026-06-01T00:00:00+00:00",
			"authority":3},{"title":"no url"}]}`)
	}))
	res, err := c.Search(context.Background(), port.WebQuery{Query: "deepseek v4", Count: 5})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if gotPath != "/v1/tools/search" {
		t.Fatalf("path = %q，应为 /v1/tools/search（base_url 不带 /v1 时补上）", gotPath)
	}
	if gotAuth != "Bearer test-key" {
		t.Fatalf("Authorization = %q", gotAuth)
	}
	if gotBody["count"] != float64(5) || gotBody["need_content"] != false {
		t.Fatalf("请求体 = %v", gotBody)
	}
	if res.Provider != "deepseek" || len(res.Items) != 1 {
		t.Fatalf("结果 = %+v（无 URL 的条目应被丢弃）", res)
	}
	it := res.Items[0]
	if it.Title != "T" || it.URL != "https://e.com/a" || it.Summary != "long" || it.SiteName != "Site" ||
		it.Date == "" || it.Authority != 3 || it.Score != 2.4 {
		t.Fatalf("字段映射 = %+v", it)
	}
}

func TestSearchBaseURLWithV1Suffix(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/tools/search" {
			t.Errorf("path = %q", r.URL.Path)
		}
		io.WriteString(w, `{"results":[]}`)
	}))
	defer srv.Close()
	c, err := New(Config{BaseURL: srv.URL + "/v1", APIKey: "k"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := c.Search(context.Background(), port.WebQuery{Query: "q"}); err != nil {
		t.Fatalf("Search: %v", err)
	}
}

func TestSearchFreshnessBecomesAfterOperator(t *testing.T) {
	var gotBody map[string]any
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &gotBody)
		io.WriteString(w, `{"results":[]}`)
	}))
	if _, err := c.Search(context.Background(), port.WebQuery{Query: "golang", Freshness: "week"}); err != nil {
		t.Fatalf("Search: %v", err)
	}
	if q, _ := gotBody["query"].(string); q != "golang after:2026-10-15" {
		t.Fatalf("query = %q", q)
	}
}

func TestSearchBadInput(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		io.WriteString(w, `{"error":{"message":"'query' is invalid","code":"invalid_request_error"}}`)
	}))
	_, err := c.Search(context.Background(), port.WebQuery{Query: "q"})
	if !errors.Is(err, port.ErrWebBadInput) {
		t.Fatalf("400 应标注 ErrWebBadInput，得到 %v", err)
	}
	if !strings.Contains(err.Error(), "invalid") {
		t.Fatalf("错误应带上游消息：%v", err)
	}

	// 本地校验：空查询、超长查询、未知时效档都不发请求。
	if _, err := c.Search(context.Background(), port.WebQuery{Query: "  "}); !errors.Is(err, port.ErrWebBadInput) {
		t.Fatalf("空查询 = %v", err)
	}
	long := strings.Repeat("字", 129)
	if _, err := c.Search(context.Background(), port.WebQuery{Query: long}); !errors.Is(err, port.ErrWebBadInput) {
		t.Fatalf("超长查询 = %v", err)
	}
	if _, err := c.Search(context.Background(), port.WebQuery{Query: "q", Freshness: "hour"}); !errors.Is(err, port.ErrWebBadInput) {
		t.Fatalf("未知时效 = %v", err)
	}
}

func TestSearchServerErrorIsRetryable(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		io.WriteString(w, `{"error":{"message":"rate limited"}}`)
	}))
	_, err := c.Search(context.Background(), port.WebQuery{Query: "q"})
	if err == nil || errors.Is(err, port.ErrWebBadInput) {
		t.Fatalf("429 应是可降级错误，得到 %v", err)
	}
	if !strings.Contains(err.Error(), "429") {
		t.Fatalf("错误应含状态码：%v", err)
	}
}

func TestFetch(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/tools/fetch" {
			t.Errorf("path = %q", r.URL.Path)
		}
		io.WriteString(w, `{"title":"Page","content":"# Page\n\nbody"}`)
	}))
	page, err := c.Fetch(context.Background(), "https://example.com/a")
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if page.Title != "Page" || !strings.Contains(page.Markdown, "body") || page.Provider != "deepseek" {
		t.Fatalf("page = %+v", page)
	}
}

func TestFetchRejectsBadURLsLocally(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("不该发请求")
	}))
	for _, u := range []string{"", "file:///etc/passwd", "https://user:pw@example.com/a", "not a url"} {
		if _, err := c.Fetch(context.Background(), u); !errors.Is(err, port.ErrWebBadInput) {
			t.Fatalf("%q = %v，应为 ErrWebBadInput", u, err)
		}
	}
}

func TestFetchEmptyContentIsError(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"title":"","content":""}`)
	}))
	if _, err := c.Fetch(context.Background(), "https://example.com/a"); err == nil {
		t.Fatal("空正文应报错（空结果不计费，也不该当成功回填）")
	}
}

func TestNewValidatesConfig(t *testing.T) {
	if _, err := New(Config{APIKey: "k"}); err == nil {
		t.Fatal("缺 base_url 应报错")
	}
	if _, err := New(Config{BaseURL: "https://api.deepseek.com"}); err == nil {
		t.Fatal("缺密钥应报错")
	}
}
