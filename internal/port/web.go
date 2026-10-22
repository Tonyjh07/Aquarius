package port

import (
	"context"
	"errors"
)

// ErrWebBadInput 调用方输入有误（查询为空/超长、URL 不合法）。
// 换一个提供商也是同样的结果，所以失败切换链**不**因它降级。
var ErrWebBadInput = errors.New("invalid web request")

// WebQuery 一次网页搜索的输入（与具体提供商无关）。
type WebQuery struct {
	// Query 搜索词；提供商会把它原样交给搜索引擎，站点/时间等算符可直接写在里面。
	Query string
	// Count 期望条数（1-20）；0 = 适配器缺省。
	Count int
	// Freshness 时效过滤："" | day | week | month | year。
	Freshness string
}

// WebResult 一条搜索结果。字段按「模型先读什么」排序：标题 → 地址 → 摘要。
type WebResult struct {
	Title    string
	URL      string
	Snippet  string // 短摘要（约 100-200 字）
	Summary  string // 长摘要（约 600-800 字），为空时用 Snippet
	SiteName string
	Date     string // 发布时间（RFC3339）；未知为空
	// Score 相关性排序分（各家量纲不同，只用于同一批结果内比较）。
	Score float64
	// Authority 权威度 0-4；-1 = 未知。
	Authority int
}

// WebResults 一次搜索的产出，附带「谁答的」与降级痕迹。
type WebResults struct {
	Items []WebResult
	// Provider 实际应答的提供商名（deepseek / brave …）。
	Provider string
	// Fallbacks 应答之前失败的提供商名（按尝试顺序）。非空 = 这次是降级结果，
	// 工具层会把它写进回填文本，模型才能如实说明「换了一家」。
	Fallbacks []string
}

// WebPage 抓取到的一篇网页正文。
type WebPage struct {
	URL       string
	Title     string
	Markdown  string // 正文（已转 Markdown，未截断）
	Provider  string
	Fallbacks []string
}

// WebSearch 搜索提供商端口（§5.14）：DeepSeek / Brave / 自建 SearXNG 各自实现。
//
// 实现只做「一次搜索」，不处理重试、冷却与降级——那些是 webchain 的事
// （横切能力不进提供商，DESIGN §9 铁律同口径）。
type WebSearch interface {
	// Search 执行一次搜索。输入非法时返回包装了 ErrWebBadInput 的错误；
	// 其余错误（网络、鉴权、限额、上游 5xx）由调用方决定是否换一家。
	Search(ctx context.Context, q WebQuery) (WebResults, error)
	// Provider 提供商名（写进降级痕迹与日志，不进工具声明）。
	Provider() string
}

// WebFetch 网页抓取端口（§5.14）：远端（DeepSeek）或本地（HTTP + 正文提取）。
//
// 实现返回**未截断**的正文；按预算截断由工具层统一做（tool_output_chars 之外
// 还有 max_chars，两者取小）。
type WebFetch interface {
	Fetch(ctx context.Context, rawURL string) (WebPage, error)
	Provider() string
}
