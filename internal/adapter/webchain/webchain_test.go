package webchain

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Tonyjh07/Aquarius/internal/port"
)

// fakeSearch 可编程的搜索提供商：按脚本返回结果或错误。
type fakeSearch struct {
	name  string
	calls int
	fn    func(call int) (port.WebResults, error)
}

func (f *fakeSearch) Provider() string { return f.name }

func (f *fakeSearch) Search(ctx context.Context, q port.WebQuery) (port.WebResults, error) {
	f.calls++
	return f.fn(f.calls)
}

// fakeFetch 可编程的抓取提供商。
type fakeFetch struct {
	name  string
	calls int
	fn    func(call int) (port.WebPage, error)
}

func (f *fakeFetch) Provider() string { return f.name }

func (f *fakeFetch) Fetch(ctx context.Context, rawURL string) (port.WebPage, error) {
	f.calls++
	return f.fn(f.calls)
}

func okSearch(name string) *fakeSearch {
	return &fakeSearch{name: name, fn: func(int) (port.WebResults, error) {
		return port.WebResults{Items: []port.WebResult{{Title: name, URL: "https://e.com/" + name}}, Provider: name}, nil
	}}
}

func TestSearchFirstProviderWins(t *testing.T) {
	a, b := okSearch("a"), okSearch("b")
	chain := NewSearch([]port.WebSearch{a, b})
	res, err := chain.Search(context.Background(), port.WebQuery{Query: "q"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if res.Provider != "a" || b.calls != 0 {
		t.Fatalf("应只问第一家：res=%+v b.calls=%d", res, b.calls)
	}
	if len(res.Fallbacks) != 0 {
		t.Fatalf("没有失败就不该有降级痕迹：%v", res.Fallbacks)
	}
}

func TestSearchFallsBackAndRecordsTrail(t *testing.T) {
	a := &fakeSearch{name: "a", fn: func(int) (port.WebResults, error) {
		return port.WebResults{}, errors.New("boom")
	}}
	b := okSearch("b")
	res, err := NewSearch([]port.WebSearch{a, b}).Search(context.Background(), port.WebQuery{Query: "q"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if res.Provider != "b" || len(res.Fallbacks) != 1 || res.Fallbacks[0] != "a" {
		t.Fatalf("降级痕迹 = %+v", res)
	}
}

func TestSearchBadInputStopsTheChain(t *testing.T) {
	a := &fakeSearch{name: "a", fn: func(int) (port.WebResults, error) {
		return port.WebResults{}, fmt.Errorf("%w: query too long", port.ErrWebBadInput)
	}}
	b := okSearch("b")
	_, err := NewSearch([]port.WebSearch{a, b}).Search(context.Background(), port.WebQuery{Query: "q"})
	if !errors.Is(err, port.ErrWebBadInput) {
		t.Fatalf("应原样上抛 ErrWebBadInput，得到 %v", err)
	}
	if b.calls != 0 {
		t.Fatalf("输入非法不该再问第二家，b.calls=%d", b.calls)
	}
}

func TestSearchEmptyResultTriesNext(t *testing.T) {
	a := &fakeSearch{name: "a", fn: func(int) (port.WebResults, error) {
		return port.WebResults{Items: nil, Provider: "a"}, nil
	}}
	b := okSearch("b")
	res, err := NewSearch([]port.WebSearch{a, b}).Search(context.Background(), port.WebQuery{Query: "q"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if res.Provider != "b" || a.calls != 1 {
		t.Fatalf("空结果应换下一家：%+v a.calls=%d", res, a.calls)
	}
}

func TestSearchAllEmptyReturnsEmptyWithProvider(t *testing.T) {
	a := &fakeSearch{name: "a", fn: func(int) (port.WebResults, error) {
		return port.WebResults{Provider: "a"}, nil
	}}
	b := &fakeSearch{name: "b", fn: func(int) (port.WebResults, error) {
		return port.WebResults{Provider: "b"}, nil
	}}
	res, err := NewSearch([]port.WebSearch{a, b}).Search(context.Background(), port.WebQuery{Query: "q"})
	if err != nil {
		t.Fatalf("全链无命中不算错误：%v", err)
	}
	if len(res.Items) != 0 || res.Provider != "b" {
		t.Fatalf("应回最后一家 + 空结果：%+v", res)
	}
	if len(res.Fallbacks) != 1 || res.Fallbacks[0] != "a" {
		t.Fatalf("空结果也是降级：%v", res.Fallbacks)
	}
}

func TestSearchAllFail(t *testing.T) {
	a := &fakeSearch{name: "a", fn: func(int) (port.WebResults, error) { return port.WebResults{}, errors.New("a down") }}
	b := &fakeSearch{name: "b", fn: func(int) (port.WebResults, error) { return port.WebResults{}, errors.New("b down") }}
	_, err := NewSearch([]port.WebSearch{a, b}).Search(context.Background(), port.WebQuery{Query: "q"})
	if err == nil || !strings.Contains(err.Error(), "a down") || !strings.Contains(err.Error(), "b down") {
		t.Fatalf("应保留最后一次错误：%v", err)
	}
}

func TestSearchCooldownSkipsBrokenProvider(t *testing.T) {
	broken := &fakeSearch{name: "broken", fn: func(int) (port.WebResults, error) {
		return port.WebResults{}, errors.New("down")
	}}
	good := okSearch("good")
	chain := NewSearch([]port.WebSearch{broken, good})
	now := time.Now()
	chain.now = func() time.Time { return now }

	for i := 0; i < failThreshold; i++ {
		if _, err := chain.Search(context.Background(), port.WebQuery{Query: "q"}); err != nil {
			t.Fatalf("第 %d 轮: %v", i, err)
		}
	}
	if broken.calls != failThreshold {
		t.Fatalf("冷却前应重试到阈值，calls=%d", broken.calls)
	}
	if _, err := chain.Search(context.Background(), port.WebQuery{Query: "q"}); err != nil {
		t.Fatalf("冷却轮: %v", err)
	}
	if broken.calls != failThreshold {
		t.Fatalf("冷却期内不该再问它，calls=%d", broken.calls)
	}
	// 冷却到期后重新尝试。
	now = now.Add(cooldown + time.Second)
	if _, err := chain.Search(context.Background(), port.WebQuery{Query: "q"}); err != nil {
		t.Fatalf("冷却后: %v", err)
	}
	if broken.calls != failThreshold+1 {
		t.Fatalf("冷却到期应重新尝试，calls=%d", broken.calls)
	}
}

func TestSearchSuccessResetsFailureCount(t *testing.T) {
	broken := &fakeSearch{name: "x", fn: func(call int) (port.WebResults, error) {
		if call <= 2 {
			return port.WebResults{}, errors.New("blip")
		}
		return port.WebResults{Items: []port.WebResult{{URL: "https://e.com/x"}}, Provider: "x"}, nil
	}}
	good := okSearch("good")
	chain := NewSearch([]port.WebSearch{broken, good})
	for i := 0; i < 6; i++ {
		if _, err := chain.Search(context.Background(), port.WebQuery{Query: "q"}); err != nil {
			t.Fatalf("第 %d 轮: %v", i, err)
		}
	}
	if broken.calls != 6 {
		t.Fatalf("calls = %d（应每轮都试它）", broken.calls)
	}
	if chain.cooling(0) {
		t.Fatal("成功过就不该累计到冷却")
	}
}

func TestSearchNoProviders(t *testing.T) {
	if _, err := NewSearch(nil).Search(context.Background(), port.WebQuery{Query: "q"}); err == nil {
		t.Fatal("没有提供商应报错")
	}
}

func TestFetchFallbackAndCooldown(t *testing.T) {
	remote := &fakeFetch{name: "remote", fn: func(int) (port.WebPage, error) {
		return port.WebPage{}, errors.New("402 insufficient balance")
	}}
	local := &fakeFetch{name: "local", fn: func(int) (port.WebPage, error) {
		return port.WebPage{URL: "https://e.com/a", Markdown: "body", Provider: "local"}, nil
	}}
	chain := NewFetch([]port.WebFetch{remote, local})
	page, err := chain.Fetch(context.Background(), "https://e.com/a")
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if page.Provider != "local" || len(page.Fallbacks) != 1 || page.Fallbacks[0] != "remote" {
		t.Fatalf("降级痕迹 = %+v", page)
	}

	// 抓取的坏输入（URL 非法）不降级：本地抓取对同一个 URL 也是同样的拒绝。
	bad := &fakeFetch{name: "bad", fn: func(int) (port.WebPage, error) {
		return port.WebPage{}, fmt.Errorf("%w: scheme", port.ErrWebBadInput)
	}}
	after := &fakeFetch{name: "after", fn: func(int) (port.WebPage, error) {
		t.Error("不该被调用")
		return port.WebPage{}, nil
	}}
	if _, err := NewFetch([]port.WebFetch{bad, after}).Fetch(context.Background(), "file:///x"); !errors.Is(err, port.ErrWebBadInput) {
		t.Fatalf("应原样上抛 ErrWebBadInput，得到 %v", err)
	}
}
