package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/Tonyjh07/Aquarius/internal/adapter/webbrave"
	"github.com/Tonyjh07/Aquarius/internal/adapter/webchain"
	"github.com/Tonyjh07/Aquarius/internal/adapter/webdeepseek"
	"github.com/Tonyjh07/Aquarius/internal/adapter/webfetchlocal"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

// buildWeb 按配置顺序装配联网提供商（D197）：列表顺序即优先级，失败由 webchain
// 换下一家。任何一家装不起来（缺密钥、base_url 为空）只警告并跳过——其余提供商
// 照常可用，联网能力降级而不是整个启动失败。
func (w *wiring) buildWeb(ctx context.Context) (port.WebSearch, port.WebFetch) {
	searches := make([]port.WebSearch, 0, len(w.cfg.Web.Search.Providers))
	for i, p := range w.cfg.Web.Search.Providers {
		switch p.Kind {
		case webKindDeepSeek:
			c, err := w.deepseekWeb(ctx, p, webSideSearch)
			if err != nil {
				w.webWarn("搜索", i, p, err)
				continue
			}
			searches = append(searches, c)
		case webKindBrave:
			key, err := w.webKey(ctx, "brave", p.Key)
			if err != nil {
				w.webWarn("搜索", i, p, err)
				continue
			}
			c, err := webbrave.New(webbrave.Config{
				APIKey:  key,
				BaseURL: p.BaseURL,
				Country: p.Country,
				Lang:    p.Lang,
			})
			if err != nil {
				w.webWarn("搜索", i, p, err)
				continue
			}
			searches = append(searches, c)
		}
	}
	fetches := make([]port.WebFetch, 0, len(w.cfg.Web.Fetch.Providers))
	for i, p := range w.cfg.Web.Fetch.Providers {
		switch p.Kind {
		case webKindDeepSeek:
			c, err := w.deepseekWeb(ctx, p, webSideFetch)
			if err != nil {
				w.webWarn("抓取", i, p, err)
				continue
			}
			fetches = append(fetches, c)
		case webKindLocal:
			cfg := webfetchlocal.Config{
				Proxy:           w.cfg.Web.Fetch.Proxy,
				UserAgent:       w.cfg.Web.Fetch.UserAgent,
				RespectRobots:   w.cfg.Web.Fetch.RespectRobots,
				AllowPrivateIPs: w.cfg.Web.Fetch.AllowPrivateIPs,
			}
			if sec := w.cfg.Web.Fetch.TimeoutSec; sec > 0 {
				cfg.Timeout = time.Duration(sec) * time.Second
			}
			c, err := webfetchlocal.New(cfg)
			if err != nil {
				w.webWarn("抓取", i, p, err)
				continue
			}
			fetches = append(fetches, c)
		}
	}
	var search port.WebSearch
	if len(searches) > 0 {
		search = webchain.NewSearch(searches)
	}
	var fetch port.WebFetch
	if len(fetches) > 0 {
		fetch = webchain.NewFetch(fetches)
	}
	return search, fetch
}

// deepseekWeb 造一个 DeepSeek 联网客户端（D222）：端点与密钥**优先用本条目自己的**，
// 没填的才按 `from` 去 model.providers 借（缺省 = 同名 deepseek）——所以「借用」现在
// 是省事的捷径，而不是唯一形态：用户可以给联网单独配端点与密钥，不动对话 provider。
//
// 两侧都留空时密钥回落按侧派生的名字（`AQUARIUS_DEEPSEEK_SEARCH_API_KEY` /
// `_FETCH_`）：对话 provider 已占着 `AQUARIUS_DEEPSEEK_API_KEY`，共用一格会让
// 「给搜索换个更便宜的钥匙」变成「把对话也换了」。
func (w *wiring) deepseekWeb(ctx context.Context, p webProvider, side webSide) (*webdeepseek.Client, error) {
	fallback := deriveKeyName(webKindDeepSeek + "_" + side.secretSuffix())
	ep, err := w.cfg.resolveEndpoint(p.BaseURL, p.Key, webProviderFrom(p), fallback)
	if err != nil {
		return nil, err
	}
	key, err := resolveAPIKey(ctx, "deepseek web", ep.KeyRef, w.secrets, w.stderr)
	if err != nil {
		return nil, err
	}
	c, err := webdeepseek.New(webdeepseek.Config{BaseURL: ep.BaseURL, APIKey: key})
	if err != nil {
		return nil, err
	}
	return c, nil
}

// webKey 解析一条独立的密钥引用（brave 这类只有联网端点的服务）。
// 空引用 = 回落该服务同名的 secret（secret:<UPPER>），避免「配了 kind 却忘了 key」时
// 静默跳过。
func (w *wiring) webKey(ctx context.Context, name, ref string) (string, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		ref = "secret:AQUARIUS_" + strings.ToUpper(name) + "_API_KEY"
	}
	return resolveAPIKey(ctx, name+" web", ref, w.secrets, w.stderr)
}

// webWarn 一家提供商装不起来：面向用户的警告（不含决策编号，D 规则）。
func (w *wiring) webWarn(side string, i int, p webProvider, err error) {
	if w.stderr == nil {
		return
	}
	fmt.Fprintf(w.stderr, "警告：联网%s的第 %d 家提供商（%s）不可用：%v\n", side, i+1, p.Kind, err)
}
