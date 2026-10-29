// Package webchain 把多家搜索/抓取提供商串成一条有序链（§5.14，D197）。
//
// 顺序就是用户在配置里写的优先级：先试第一家，失败换下一家；成功的提供商在结果里
// 留名，失败的按顺序记进 Fallbacks，工具层据此告诉模型「这次是降级结果」。
//
// 失败切换口径（与模型降级链同一套直觉）：
//   - 网络错误、超时、401/402/403/429/5xx → 换下一家（这家现在就是不可用）；
//   - 400（参数/查询非法）→ **不换**：换一家也是同样的结果，换了只是多花钱；
//   - 上游正常但结果为空 → 换下一家试一次（空结果常是引擎覆盖问题，不是输入问题）；
//   - 同一家连续失败 3 次 → 冷却 60 秒内直接跳过（避免每轮都为一家挂掉的服务付超时）。
//
// 这里只做路由，不做重试退避：重试是 LLM 端口的装饰器（D14），搜索没有「同家重试
// 更划算」的性质——换一家立刻就有答案。
package webchain

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/Tonyjh07/Aquarius/internal/port"
)

// 冷却参数：连续失败到阈值后跳过一段时间。
const (
	failThreshold = 3
	cooldown      = 60 * time.Second
)

// Search 有序搜索链（实现 port.WebSearch）。
type Search struct {
	providers []port.WebSearch
	now       func() time.Time
	mu        sync.Mutex
	state     []health
}

// Fetch 有序抓取链（实现 port.WebFetch）。
type Fetch struct {
	providers []port.WebFetch
	now       func() time.Time
	mu        sync.Mutex
	state     []health
}

type health struct {
	fails     int
	coolUntil time.Time
}

// NewSearch 按优先级构造搜索链；空列表 = 没有可用的搜索提供商（工具层据此报因）。
func NewSearch(providers []port.WebSearch) *Search {
	return &Search{providers: providers, now: time.Now, state: make([]health, len(providers))}
}

// NewFetch 按优先级构造抓取链。
func NewFetch(providers []port.WebFetch) *Fetch {
	return &Fetch{providers: providers, now: time.Now, state: make([]health, len(providers))}
}

// Provider 链本身的标识（写进结果的是实际应答的那家，不是 "chain"）。
func (s *Search) Provider() string { return "chain" }

// Provider 链本身的标识。
func (f *Fetch) Provider() string { return "chain" }

// Search 实现 port.WebSearch。
func (s *Search) Search(ctx context.Context, q port.WebQuery) (port.WebResults, error) {
	if len(s.providers) == 0 {
		return port.WebResults{}, fmt.Errorf("web search: no provider is configured")
	}
	var fallbacks []string
	var errs []string
	// 空结果不当成功：记下最后一家「答了但没结果」，换下一家再试。
	var empty *port.WebResults
	for i, p := range s.providers {
		if err := ctx.Err(); err != nil {
			return port.WebResults{}, err
		}
		if s.cooling(i) {
			fallbacks = append(fallbacks, p.Provider())
			continue
		}
		res, err := p.Search(ctx, q)
		if err == nil {
			s.succeeded(i)
			if len(res.Items) == 0 {
				res.Items = nil
				empty = &res
				fallbacks = append(fallbacks, p.Provider())
				continue
			}
			res.Fallbacks = append(fallbacks, res.Fallbacks...)
			return res, nil
		}
		if errors.Is(err, port.ErrWebBadInput) || ctx.Err() != nil {
			// 输入本身有问题：换家没用，别再多花一次钱。
			return port.WebResults{}, err
		}
		s.failed(i)
		errs = append(errs, fmt.Sprintf("%s: %v", p.Provider(), err))
		fallbacks = append(fallbacks, p.Provider())
	}
	if empty != nil {
		// 全链都没有命中：如实回「这次谁答的、之前谁失败了」，模型据此说明覆盖不足。
		// 应答的那家不算「降级到」它——从痕迹里去掉最后一项。
		if n := len(fallbacks); n > 0 && fallbacks[n-1] == empty.Provider {
			fallbacks = fallbacks[:n-1]
		}
		empty.Fallbacks = fallbacks
		return *empty, nil
	}
	if len(errs) > 0 {
		return port.WebResults{}, fmt.Errorf("web search: all providers failed: %s", strings.Join(errs, "; "))
	}
	return port.WebResults{}, fmt.Errorf("web search: no provider is available (all cooling down)")
}

// Fetch 实现 port.WebFetch。
func (f *Fetch) Fetch(ctx context.Context, rawURL string) (port.WebPage, error) {
	if len(f.providers) == 0 {
		return port.WebPage{}, fmt.Errorf("web fetch: no provider is configured")
	}
	var fallbacks []string
	var lastErr error
	for i, p := range f.providers {
		if err := ctx.Err(); err != nil {
			return port.WebPage{}, err
		}
		if f.cooling(i) {
			fallbacks = append(fallbacks, p.Provider())
			continue
		}
		page, err := p.Fetch(ctx, rawURL)
		if err == nil {
			f.succeeded(i)
			page.Fallbacks = append(fallbacks, page.Fallbacks...)
			return page, nil
		}
		lastErr = err
		if errors.Is(err, port.ErrWebBadInput) || ctx.Err() != nil {
			return port.WebPage{}, err
		}
		f.failed(i)
		fallbacks = append(fallbacks, p.Provider())
	}
	if lastErr != nil {
		return port.WebPage{}, fmt.Errorf("web fetch: all providers failed (%s): %w", strings.Join(fallbacks, ", "), lastErr)
	}
	return port.WebPage{}, fmt.Errorf("web fetch: no provider is available (all cooling down)")
}

func (s *Search) cooling(i int) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.now().Before(s.state[i].coolUntil)
}

func (s *Search) failed(i int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.state[i].fails++
	if s.state[i].fails >= failThreshold {
		s.state[i].coolUntil = s.now().Add(cooldown)
		s.state[i].fails = 0
	}
}

func (s *Search) succeeded(i int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.state[i] = health{}
}

func (f *Fetch) cooling(i int) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.now().Before(f.state[i].coolUntil)
}

func (f *Fetch) failed(i int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.state[i].fails++
	if f.state[i].fails >= failThreshold {
		f.state[i].coolUntil = f.now().Add(cooldown)
		f.state[i].fails = 0
	}
}

func (f *Fetch) succeeded(i int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.state[i] = health{}
}

var (
	_ port.WebSearch = (*Search)(nil)
	_ port.WebFetch  = (*Fetch)(nil)
)
