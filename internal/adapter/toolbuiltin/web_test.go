package toolbuiltin

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/Tonyjh07/Aquarius/internal/domain/tool"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

// fakeWebSearch 记录请求并回脚本结果。
type fakeWebSearch struct {
	got  port.WebQuery
	res  port.WebResults
	err  error
	call int
}

func (f *fakeWebSearch) Provider() string { return "fake" }

func (f *fakeWebSearch) Search(ctx context.Context, q port.WebQuery) (port.WebResults, error) {
	f.call++
	f.got = q
	return f.res, f.err
}

type fakeWebFetch struct {
	gotURL string
	page   port.WebPage
	err    error
}

func (f *fakeWebFetch) Provider() string { return "fake" }

func (f *fakeWebFetch) Fetch(ctx context.Context, rawURL string) (port.WebPage, error) {
	f.gotURL = rawURL
	return f.page, f.err
}

func callWith(t *testing.T, name string, args map[string]any) tool.Call {
	t.Helper()
	raw, err := json.Marshal(args)
	if err != nil {
		t.Fatalf("marshal args: %v", err)
	}
	return tool.Call{ID: "c1", Name: name, Args: raw}
}

func TestWebSearchFormatsResults(t *testing.T) {
	search := &fakeWebSearch{res: port.WebResults{
		Provider: "deepseek",
		Items: []port.WebResult{
			{Title: "First", URL: "https://e.com/1", Summary: "long summary", Snippet: "short",
				Date: "2026-06-01", SiteName: "Site", Authority: 3, Score: 2.4},
			{Title: "", URL: "https://e.com/2", Snippet: "only snippet", Authority: -1},
		},
	}}
	tl := &webSearchTool{search: search, count: 5}
	res, err := tl.Execute(context.Background(), callWith(t, "web_search", map[string]any{"query": "go"}))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !res.OK {
		t.Fatalf("res = %+v", res)
	}
	for _, want := range []string{
		"Results from deepseek",
		"1. First",
		"https://e.com/1",
		"2026-06-01 | Site | authority 3/4 | score 2.40",
		"long summary",
		"2. (untitled)",
		"only snippet",
	} {
		if !strings.Contains(res.Output, want) {
			t.Fatalf("输出缺 %q：\n%s", want, res.Output)
		}
	}
	if strings.Contains(res.Output, "authority -1") {
		t.Fatalf("未知权威度不该回填：\n%s", res.Output)
	}
	if search.got.Count != 5 {
		t.Fatalf("缺省条数应取工具配置，得到 %d", search.got.Count)
	}
}

func TestWebSearchNotesFallback(t *testing.T) {
	search := &fakeWebSearch{res: port.WebResults{
		Provider:  "brave",
		Fallbacks: []string{"deepseek"},
		Items:     []port.WebResult{{Title: "T", URL: "https://e.com/1", Snippet: "s"}},
	}}
	tl := &webSearchTool{search: search}
	res, err := tl.Execute(context.Background(), callWith(t, "web_search", map[string]any{"query": "go"}))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(res.Output, "fell back after deepseek failed") {
		t.Fatalf("降级应如实回填：\n%s", res.Output)
	}
}

func TestWebSearchEmptyResults(t *testing.T) {
	search := &fakeWebSearch{res: port.WebResults{Provider: "deepseek"}}
	tl := &webSearchTool{search: search}
	res, err := tl.Execute(context.Background(), callWith(t, "web_search", map[string]any{"query": "nothing"}))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(res.Output, emptyMatches) {
		t.Fatalf("空结果应明确说明：\n%s", res.Output)
	}
}

func TestWebSearchValidatesArgs(t *testing.T) {
	search := &fakeWebSearch{}
	tl := &webSearchTool{search: search}
	cases := []map[string]any{
		{"query": "   "},
		{"query": strings.Repeat("字", 129)},
	}
	for _, args := range cases {
		if _, err := tl.Execute(context.Background(), callWith(t, "web_search", args)); err == nil {
			t.Fatalf("%v 应报错", args)
		}
	}
	if search.call != 0 {
		t.Fatal("非法参数不该发请求")
	}
	if _, err := (&webSearchTool{}).Execute(context.Background(), callWith(t, "web_search", map[string]any{"query": "q"})); err == nil ||
		!strings.Contains(err.Error(), "no search provider is configured") {
		t.Fatalf("未配置应明确报因，得到 %v", err)
	}
}

func TestWebSearchClampsCount(t *testing.T) {
	search := &fakeWebSearch{res: port.WebResults{Provider: "deepseek", Items: []port.WebResult{{URL: "https://e.com/1"}}}}
	tl := &webSearchTool{search: search, count: 99}
	if _, err := tl.Execute(context.Background(), callWith(t, "web_search", map[string]any{"query": "q", "count": 999})); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if search.got.Count != webSearchMaxCount {
		t.Fatalf("条数应夹到 %d，得到 %d", webSearchMaxCount, search.got.Count)
	}
}

func TestWebFetchFormatsPage(t *testing.T) {
	fetch := &fakeWebFetch{page: port.WebPage{
		URL: "https://e.com/a", Title: "Article", Markdown: "# Head\n\nbody text", Provider: "local",
	}}
	tl := &webFetchTool{fetch: fetch}
	res, err := tl.Execute(context.Background(), callWith(t, "web_fetch", map[string]any{"url": "https://e.com/a"}))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	for _, want := range []string{"Title: Article", "URL: https://e.com/a", "Fetched by: local", "# Head", "body text"} {
		if !strings.Contains(res.Output, want) {
			t.Fatalf("输出缺 %q：\n%s", want, res.Output)
		}
	}
}

func TestWebFetchTruncates(t *testing.T) {
	long := strings.Repeat("x", 5000)
	fetch := &fakeWebFetch{page: port.WebPage{URL: "https://e.com/a", Markdown: long, Provider: "local"}}
	tl := &webFetchTool{fetch: fetch}
	res, err := tl.Execute(context.Background(), callWith(t, "web_fetch", map[string]any{"url": "https://e.com/a", "max_chars": 1000}))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(res.Output, "truncated at 1000 characters") {
		t.Fatalf("截断应说明并给出后续办法：\n%s", res.Output)
	}
	if len([]rune(res.Output)) > 1200 {
		t.Fatalf("输出未按预算截断（%d 字符）", len([]rune(res.Output)))
	}
}

func TestWebFetchValidatesArgs(t *testing.T) {
	fetch := &fakeWebFetch{}
	tl := &webFetchTool{fetch: fetch}
	if _, err := tl.Execute(context.Background(), callWith(t, "web_fetch", map[string]any{"url": " "})); err == nil {
		t.Fatal("空 url 应报错")
	}
	if fetch.gotURL != "" {
		t.Fatal("非法参数不该发请求")
	}
	if _, err := (&webFetchTool{}).Execute(context.Background(), callWith(t, "web_fetch", map[string]any{"url": "https://e.com"})); err == nil ||
		!strings.Contains(err.Error(), "no fetch provider is configured") {
		t.Fatalf("未配置应明确报因，得到 %v", err)
	}
}

func TestWebFetchPropagatesErrors(t *testing.T) {
	fetch := &fakeWebFetch{err: errors.New("boom")}
	tl := &webFetchTool{fetch: fetch}
	_, err := tl.Execute(context.Background(), callWith(t, "web_fetch", map[string]any{"url": "https://e.com/a"}))
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("错误应上抛，得到 %v", err)
	}
}

func TestWebToolsAreRegistered(t *testing.T) {
	names := map[string]bool{}
	for _, n := range Names() {
		names[n] = true
	}
	for _, want := range []string{"web_search", "web_fetch"} {
		if !names[want] {
			t.Fatalf("%s 应出现在内置工具清单里（设置窗工具页按此列出）", want)
		}
		if DisplayName(want) == want {
			t.Fatalf("%s 缺中文词条", want)
		}
	}
}
