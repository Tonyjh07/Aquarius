// Package webdeepseek 把 DeepSeek 的 Search / Fetch 两个 REST 端点接到
// port.WebSearch / port.WebFetch（§5.14）。
//
// 与模型无关：走的是 https://api.deepseek.com/v1/tools/{search,fetch}，不经过
// Responses API 的托管 web_search（那条路只有个别模型支持，换模型就静默失效）。
// 端点与密钥由装配根按条目自己的配置解析——默认借用对话 provider 那一份，也可以
// 独立填一份（搜索与抓取各有一格密钥槽，互不覆盖）。
package webdeepseek

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/Tonyjh07/Aquarius/internal/port"
)

// maxRespBytes 单次响应上限：搜索 50 条 × 长摘要、抓取整页正文都在几 MB 内，
// 超过就是上游异常或恶意端点，不能让它把内存吃穿。
const maxRespBytes = 8 << 20

// Config 构造参数。
type Config struct {
	// BaseURL provider 的 base_url（如 https://api.deepseek.com 或 …/v1）。
	BaseURL string
	// APIKey 明文密钥（装配根经 port.Secrets 解析后传入，不进日志）。
	APIKey string
	// Client 可注入的 HTTP 客户端（测试用）；nil = 带超时的缺省客户端。
	Client *http.Client
	// Now 时间源（把 freshness 换算成 after: 日期；测试注入固定时钟）。
	Now func() time.Time
}

// Client 同时实现 port.WebSearch 与 port.WebFetch。
type Client struct {
	base   string
	key    string
	client *http.Client
	now    func() time.Time
}

// New 构造客户端；base_url 或密钥为空时报错（装配根据此跳过该 provider 并警告）。
func New(cfg Config) (*Client, error) {
	base := strings.TrimSpace(cfg.BaseURL)
	if base == "" {
		return nil, fmt.Errorf("deepseek web: base_url is empty")
	}
	if _, err := url.Parse(base); err != nil {
		return nil, fmt.Errorf("deepseek web: invalid base_url: %w", err)
	}
	if strings.TrimSpace(cfg.APIKey) == "" {
		return nil, fmt.Errorf("deepseek web: api key is empty")
	}
	client := cfg.Client
	if client == nil {
		client = &http.Client{Timeout: 45 * time.Second}
	}
	now := cfg.Now
	if now == nil {
		now = time.Now
	}
	return &Client{base: strings.TrimRight(base, "/"), key: cfg.APIKey, client: client, now: now}, nil
}

// Provider 实现 port.WebSearch / port.WebFetch。
func (c *Client) Provider() string { return "deepseek" }

// endpoint 拼端点：base_url 可能带 /v1 也可能不带（两种写法用户都在用）。
func (c *Client) endpoint(path string) string {
	if strings.HasSuffix(c.base, "/v1") {
		return c.base + "/" + path
	}
	return c.base + "/v1/" + path
}

// Search 实现 port.WebSearch。
func (c *Client) Search(ctx context.Context, q port.WebQuery) (port.WebResults, error) {
	query, err := withFreshness(q.Query, q.Freshness, c.now())
	if err != nil {
		return port.WebResults{}, err
	}
	body := map[string]any{"query": query, "need_content": false}
	if q.Count > 0 {
		if q.Count > 50 {
			q.Count = 50
		}
		body["count"] = q.Count
	}
	var out struct {
		Results []struct {
			RerankScore float64 `json:"rerank_score"`
			Title       string  `json:"title"`
			URL         string  `json:"url"`
			Snippet     string  `json:"snippet"`
			Summary     string  `json:"summary"`
			Content     string  `json:"content"`
			SiteName    *string `json:"site_name"`
			Date        *string `json:"date"`
			Authority   int     `json:"authority"`
		} `json:"results"`
	}
	if err := c.post(ctx, "tools/search", body, &out); err != nil {
		return port.WebResults{}, err
	}
	items := make([]port.WebResult, 0, len(out.Results))
	for _, r := range out.Results {
		if strings.TrimSpace(r.URL) == "" {
			continue
		}
		item := port.WebResult{
			Title:     strings.TrimSpace(r.Title),
			URL:       strings.TrimSpace(r.URL),
			Snippet:   strings.TrimSpace(r.Snippet),
			Summary:   strings.TrimSpace(r.Summary),
			Score:     r.RerankScore,
			Authority: r.Authority,
		}
		if r.SiteName != nil {
			item.SiteName = strings.TrimSpace(*r.SiteName)
		}
		if r.Date != nil {
			item.Date = strings.TrimSpace(*r.Date)
		}
		items = append(items, item)
	}
	return port.WebResults{Items: items, Provider: c.Provider()}, nil
}

// Fetch 实现 port.WebFetch：返回上游已经转好的 Markdown（不截断）。
func (c *Client) Fetch(ctx context.Context, rawURL string) (port.WebPage, error) {
	rawURL = strings.TrimSpace(rawURL)
	u, err := url.Parse(rawURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return port.WebPage{}, fmt.Errorf("%w: not an http(s) URL: %q", port.ErrWebBadInput, rawURL)
	}
	if u.User != nil {
		return port.WebPage{}, fmt.Errorf("%w: URL must not carry credentials", port.ErrWebBadInput)
	}
	var out struct {
		Title   string `json:"title"`
		Content string `json:"content"`
	}
	if err := c.post(ctx, "tools/fetch", map[string]any{"url": rawURL}, &out); err != nil {
		return port.WebPage{}, err
	}
	if strings.TrimSpace(out.Content) == "" {
		return port.WebPage{}, fmt.Errorf("deepseek web: fetch returned no content for %s", rawURL)
	}
	return port.WebPage{
		URL:      rawURL,
		Title:    strings.TrimSpace(out.Title),
		Markdown: out.Content,
		Provider: c.Provider(),
	}, nil
}

// post 发一次 JSON 请求并把非 2xx 翻成带分类的错误（只有 400 是「别换一家」）。
func (c *Client) post(ctx context.Context, path string, body any, out any) error {
	buf, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("deepseek web: encode request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint(path), bytes.NewReader(buf))
	if err != nil {
		return fmt.Errorf("deepseek web: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.key)
	req.Header.Set("Accept", "application/json")
	resp, err := c.client.Do(req)
	if err != nil {
		return fmt.Errorf("deepseek web: %s: %w", path, err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxRespBytes))
	if err != nil {
		return fmt.Errorf("deepseek web: read response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return apiError(path, resp.StatusCode, data)
	}
	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("deepseek web: decode %s response: %w", path, err)
	}
	return nil
}

// apiError 把上游错误体翻成一句可读的英文错误；400 额外标注 ErrWebBadInput
// （换提供商也是同样的结果，失败切换链不该为它降级）。
func apiError(path string, status int, data []byte) error {
	var body struct {
		Error struct {
			Message string `json:"message"`
			Code    string `json:"code"`
		} `json:"error"`
	}
	_ = json.Unmarshal(data, &body)
	msg := strings.TrimSpace(body.Error.Message)
	if msg == "" {
		msg = strings.TrimSpace(string(data))
	}
	if len(msg) > 300 {
		msg = msg[:300] + "..."
	}
	err := fmt.Errorf("deepseek web: %s: HTTP %d: %s", path, status, msg)
	if status == http.StatusBadRequest {
		return fmt.Errorf("%w: %v", port.ErrWebBadInput, err)
	}
	return err
}

// withFreshness 把统一的时效档换算成 DeepSeek 的 after: 算符。
// 上游没有独立的 freshness 字段（见 API 文档的查询算符表），只能拼进 query。
func withFreshness(query, freshness string, now time.Time) (string, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return "", fmt.Errorf("%w: query is empty", port.ErrWebBadInput)
	}
	if n := len([]rune(query)); n > 128 {
		return "", fmt.Errorf("%w: query is %d characters; the provider accepts at most 128", port.ErrWebBadInput, n)
	}
	var days int
	switch strings.ToLower(strings.TrimSpace(freshness)) {
	case "":
		return query, nil
	case "day":
		days = 1
	case "week":
		days = 7
	case "month":
		days = 30
	case "year":
		days = 365
	default:
		return "", fmt.Errorf("%w: freshness %q is not one of day/week/month/year", port.ErrWebBadInput, freshness)
	}
	return fmt.Sprintf("%s after:%s", query, now.AddDate(0, 0, -days).Format("2006-01-02")), nil
}

var (
	_ port.WebSearch = (*Client)(nil)
	_ port.WebFetch  = (*Client)(nil)
)
