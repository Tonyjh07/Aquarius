// Package webbrave 接入 Brave Search API（port.WebSearch，DESIGN §5.14）。
//
// 只做搜索：Brave 没有对应的「抓网页转 Markdown」端点，抓取交给 webdeepseek 或
// webfetchlocal。端点 https://api.search.brave.com/res/v1/web/search，鉴权走
// X-Subscription-Token 头。
package webbrave

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/Tonyjh07/Aquarius/internal/port"
)

// defaultBaseURL Brave 的公开端点；自建网关/镜像可覆盖。
const defaultBaseURL = "https://api.search.brave.com/res/v1"

// maxRespBytes 单次响应上限（20 条结果 + 额外片段，几 MB 足够）。
const maxRespBytes = 8 << 20

// Config 构造参数。
type Config struct {
	// APIKey Brave 订阅令牌（X-Subscription-Token）。
	APIKey string
	// BaseURL 端点前缀；空 = 官方端点。
	BaseURL string
	// Country 结果国家（如 CN/US）；空 = 用 Brave 缺省（US）。
	Country string
	// Lang 结果语言（如 zh-hans/en）；空 = Brave 缺省（en）。
	Lang string
	// Client 可注入的 HTTP 客户端（测试用）。
	Client *http.Client
}

// Client 实现 port.WebSearch。
type Client struct {
	key     string
	base    string
	country string
	lang    string
	client  *http.Client
}

// New 构造客户端；令牌为空时报错（装配根据此跳过该 provider 并警告）。
func New(cfg Config) (*Client, error) {
	if strings.TrimSpace(cfg.APIKey) == "" {
		return nil, fmt.Errorf("brave web: api key is empty")
	}
	base := strings.TrimSpace(cfg.BaseURL)
	if base == "" {
		base = defaultBaseURL
	}
	if _, err := url.Parse(base); err != nil {
		return nil, fmt.Errorf("brave web: invalid base_url: %w", err)
	}
	client := cfg.Client
	if client == nil {
		client = &http.Client{Timeout: 20 * time.Second}
	}
	return &Client{
		key:     strings.TrimSpace(cfg.APIKey),
		base:    strings.TrimRight(base, "/"),
		country: strings.TrimSpace(cfg.Country),
		lang:    strings.TrimSpace(cfg.Lang),
		client:  client,
	}, nil
}

// Provider 实现 port.WebSearch。
func (c *Client) Provider() string { return "brave" }

// Search 实现 port.WebSearch。
func (c *Client) Search(ctx context.Context, q port.WebQuery) (port.WebResults, error) {
	query := strings.TrimSpace(q.Query)
	if query == "" {
		return port.WebResults{}, fmt.Errorf("%w: query is empty", port.ErrWebBadInput)
	}
	count := q.Count
	if count <= 0 {
		count = 10
	}
	if count > 20 {
		count = 20 // Brave 上限 20
	}
	params := url.Values{}
	params.Set("q", query)
	params.Set("count", strconv.Itoa(count))
	if f := freshnessParam(q.Freshness); f != "" {
		params.Set("freshness", f)
	}
	if c.country != "" {
		params.Set("country", c.country)
	}
	if c.lang != "" {
		params.Set("search_lang", c.lang)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+"/web/search?"+params.Encode(), nil)
	if err != nil {
		return port.WebResults{}, fmt.Errorf("brave web: build request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Accept-Encoding", "gzip")
	req.Header.Set("X-Subscription-Token", c.key)
	resp, err := c.client.Do(req)
	if err != nil {
		return port.WebResults{}, fmt.Errorf("brave web: search: %w", err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxRespBytes))
	if err != nil {
		return port.WebResults{}, fmt.Errorf("brave web: read response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return port.WebResults{}, apiError(resp.StatusCode, data)
	}
	var out struct {
		Web struct {
			Results []struct {
				Title       string `json:"title"`
				URL         string `json:"url"`
				Description string `json:"description"`
				Age         string `json:"age"`
				PageAge     string `json:"page_age"`
				Profile     struct {
					Name string `json:"name"`
				} `json:"profile"`
				ExtraSnippets []string `json:"extra_snippets"`
			} `json:"results"`
		} `json:"web"`
	}
	if err := json.Unmarshal(data, &out); err != nil {
		return port.WebResults{}, fmt.Errorf("brave web: decode response: %w", err)
	}
	items := make([]port.WebResult, 0, len(out.Web.Results))
	for _, r := range out.Web.Results {
		if strings.TrimSpace(r.URL) == "" {
			continue
		}
		item := port.WebResult{
			Title:     strings.TrimSpace(r.Title),
			URL:       strings.TrimSpace(r.URL),
			Snippet:   strings.TrimSpace(r.Description),
			SiteName:  strings.TrimSpace(r.Profile.Name),
			Authority: -1,
		}
		// extra_snippets 是 Brave 给模型的补充摘录：拼进 Summary，工具层优先读它。
		if extra := strings.TrimSpace(strings.Join(r.ExtraSnippets, " ")); extra != "" {
			item.Summary = extra
		}
		if age := firstNonEmpty(r.PageAge, r.Age); age != "" {
			item.Date = age
		}
		items = append(items, item)
	}
	return port.WebResults{Items: items, Provider: c.Provider()}, nil
}

// freshnessParam 统一时效档 → Brave 的 pd/pw/pm/py；未识别返回空（= 不过滤）。
func freshnessParam(freshness string) string {
	switch strings.ToLower(strings.TrimSpace(freshness)) {
	case "day":
		return "pd"
	case "week":
		return "pw"
	case "month":
		return "pm"
	case "year":
		return "py"
	default:
		return ""
	}
}

// apiError 把上游错误翻成可读错误；400 标注 ErrWebBadInput（不降级）。
func apiError(status int, data []byte) error {
	msg := strings.TrimSpace(string(data))
	if len(msg) > 300 {
		msg = msg[:300] + "..."
	}
	err := fmt.Errorf("brave web: HTTP %d: %s", status, msg)
	if status == http.StatusBadRequest || status == http.StatusUnprocessableEntity {
		return fmt.Errorf("%w: %v", port.ErrWebBadInput, err)
	}
	return err
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if s := strings.TrimSpace(v); s != "" {
			return s
		}
	}
	return ""
}

var _ port.WebSearch = (*Client)(nil)
