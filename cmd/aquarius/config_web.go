package main

// web 段（D197）：搜索与抓取两组有序提供商，以及启动期只拦「写错了」的校验——未知
// kind、抓取侧写了只做搜索的 kind、借用的条目不存在且端点也没自己填。

import (
	"fmt"
	"strings"

	"github.com/Tonyjh07/Aquarius/internal/port"
)

// webConfig 联网搜索与抓取（D197）：两组**有序**提供商列表，顺序即优先级。
//
// 空列表 = 该能力不可用：对应工具照常注册（工具清单与设置窗保持稳定），执行时
// 明确报「未配置提供商」，而不是静默返回空结果。
type webConfig struct {
	Search webSearchConfig `json:"search"`
	Fetch  webFetchConfig  `json:"fetch"`
}

// webSearchConfig 搜索侧。
type webSearchConfig struct {
	// Providers 有序提供商：先试第一个，失败换下一个（D197）。
	Providers []webProvider `json:"providers"`
	// Count 缺省返回条数（工具参数可逐次覆盖）；0 = 8。
	Count int `json:"count,omitempty"`
}

// webFetchConfig 抓取侧 + 本地抓取的安全/网络参数。
type webFetchConfig struct {
	// Providers 有序提供商：deepseek（远端）/ local（本地纯 Go）。
	Providers []webProvider `json:"providers"`
	// MaxChars 单次回填正文的缺省上限（字符）；0 = 16000，硬顶 20000。
	MaxChars int `json:"max_chars,omitempty"`
	// Proxy 本地抓取用的代理（如 http://127.0.0.1:7897）；空 = 读 HTTP(S)_PROXY。
	Proxy string `json:"proxy,omitempty"`
	// UserAgent 本地抓取的 UA；空 = 浏览器形态缺省值（很多站点拒绝非浏览器 UA）。
	UserAgent string `json:"user_agent,omitempty"`
	// RespectRobots 本地抓取是否遵守 robots.txt；键缺失 = 遵守。
	RespectRobots *bool `json:"respect_robots,omitempty"`
	// AllowPrivateIPs 放开回环/私网目标（调试本机服务用）；缺省关闭。
	AllowPrivateIPs bool `json:"allow_private_ips,omitempty"`
	// TimeoutSec 本地抓取超时秒；0 = 30。
	TimeoutSec int `json:"timeout_sec,omitempty"`
}

// webProvider 一家提供商（kind 决定怎么连，其余字段按 kind 取用）。
type webProvider struct {
	// Kind 提供商类型：搜索侧 deepseek | brave，抓取侧 deepseek | local。
	Kind string `json:"kind"`
	// From 借哪个 model.providers 条目（缺省 = 同 kind 名）。只补本条目没填的端点与
	// 密钥；两项都自己填了就不必写它。老配置只写 kind 照样能用。
	From string `json:"from,omitempty"`
	// Key 自己的密钥引用（secret:名 或明文）；空 = 借 From 那一条的第一把，
	// 连 From 也没有则按侧回落（deepseek 走 _SEARCH_/_FETCH_，brave 走 _BRAVE_）。
	Key string `json:"key,omitempty"`
	// BaseURL 自己的端点前缀（自建网关/镜像）；空 = 借 From 那一条的。
	BaseURL string `json:"base_url,omitempty"`
	// Count 这一家的缺省条数（覆盖 web.search.count）。
	Count int `json:"count,omitempty"`
	// Country / Lang 结果国家与语言（仅 brave；空 = 服务端缺省）。
	Country string `json:"country,omitempty"`
	Lang    string `json:"lang,omitempty"`
}

// 联网提供商类型标识。
const (
	webKindDeepSeek = "deepseek"
	webKindBrave    = "brave"
	webKindLocal    = "local"
)

// normalizeWeb 校验联网配置（启动期一次）：只拦「写错了」的声明——未知 kind、
// 抓取侧写了只做搜索的 kind、借用的条目不存在且端点也没自己填。
// 密钥缺失不在这里报错（装配根警告并跳过该家，其余提供商继续可用）。
func (c *fileConfig) normalizeWeb() error {
	for i, p := range c.Web.Search.Providers {
		switch p.Kind {
		case webKindDeepSeek:
			if err := c.checkWebEndpoint(p); err != nil {
				return fmt.Errorf("web.search.providers[%d]: %w", i, err)
			}
		case webKindBrave:
			// 只借 key（或 base_url），不引用对话 provider。
		default:
			return fmt.Errorf("web.search.providers[%d].kind=%q 不认识（搜索侧支持 %s / %s）",
				i, p.Kind, webKindDeepSeek, webKindBrave)
		}
	}
	for i, p := range c.Web.Fetch.Providers {
		switch p.Kind {
		case webKindDeepSeek:
			if err := c.checkWebEndpoint(p); err != nil {
				return fmt.Errorf("web.fetch.providers[%d]: %w", i, err)
			}
		case webKindLocal:
		default:
			return fmt.Errorf("web.fetch.providers[%d].kind=%q 不认识（抓取侧支持 %s / %s）",
				i, p.Kind, webKindDeepSeek, webKindLocal)
		}
	}
	if n := c.Web.Search.Count; n < 0 || n > port.SettingsSearchCountMax {
		return fmt.Errorf("web.search.count=%d 超出范围（%d-%d；0 = 缺省 8）",
			n, port.SettingsSearchCountMin, port.SettingsSearchCountMax)
	}
	if n := c.Web.Fetch.MaxChars; n < 0 || n > port.SettingsFetchCharsMax {
		return fmt.Errorf("web.fetch.max_chars=%d 超出范围（%d-%d；0 = 缺省 16000）",
			n, port.SettingsFetchCharsMin, port.SettingsFetchCharsMax)
	}
	return nil
}

// checkWebEndpoint deepseek 联网条目要能定位到端点：自己填了 base_url，或借的条目在。
func (c *fileConfig) checkWebEndpoint(p webProvider) error {
	from := webProviderFrom(p)
	if strings.TrimSpace(p.BaseURL) != "" {
		return nil // 端点自己填了：借不到密钥只让这家不可用，不阻断启动
	}
	if c.Model.byName(from) == nil {
		return fmt.Errorf("from=%q 不在 model.providers 内"+
			"（填 base_url 独立配置，或引用一个已有的条目）", from)
	}
	return nil
}

// webProviderFrom 该提供商引用的对话 provider 名（deepseek 缺省按 kind 同名找）。
func webProviderFrom(p webProvider) string {
	if s := strings.TrimSpace(p.From); s != "" {
		return s
	}
	return p.Kind
}
