// Package webfetchlocal 本地抓网页并转 Markdown（port.WebFetch，§5.14）。
//
// 管线：HTTP GET → 字符集探测 → 正文提取（readability）→ HTML 转 Markdown。
// 不依赖任何第三方服务，因此**不需要密钥**：远端提供商不可用时由它兜底。
//
// 安全默认（本地发起请求意味着能打到内网）：
//   - 只允许 http/https，URL 不得带用户名密码；
//   - DNS 解析后校验目标地址：回环、私网、链路本地（含云元数据 169.254.169.254）、
//     组播、未指定地址一律拒绝；重定向逐跳复核（防「公网 302 到内网」）；
//   - 默认遵守 robots.txt；
//   - 响应体积上限 5 MB，超出按截断处理而不是读进内存再说。
package webfetchlocal

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	readability "codeberg.org/readeck/go-readability/v2"
	htmltomarkdown "github.com/JohannesKaufmann/html-to-markdown/v2"
	"github.com/temoto/robotstxt"
	"golang.org/x/net/html"
	"golang.org/x/net/html/charset"

	"github.com/Tonyjh07/Aquarius/internal/port"
)

// 缺省值：体积上限与超时都按「个人助手读一篇文章」量级定，不是爬虫。
const (
	DefaultMaxBytes  int64 = 5 << 20
	defaultUserAgent       = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 " +
		"(KHTML, like Gecko) Chrome/126.0 Safari/537.36 Aquarius/1.0"
	robotsMaxBytes = 512 << 10
	robotsTTL      = 10 * time.Minute
	maxRedirects   = 10
)

// Config 构造参数。
type Config struct {
	// Proxy 代理地址（如 http://127.0.0.1:7897）；空 = 读 HTTP(S)_PROXY 环境变量。
	Proxy string
	// UserAgent 请求头；空 = 浏览器形态缺省值（很多站点直接拒绝非浏览器 UA）。
	UserAgent string
	// RespectRobots 是否遵守 robots.txt；指针为 nil = 遵守。
	RespectRobots *bool
	// AllowPrivateIPs 放开私网/回环目标（调试本机服务用；缺省关闭）。
	AllowPrivateIPs bool
	// MaxBytes 响应体上限；<=0 = DefaultMaxBytes。
	MaxBytes int64
	// Timeout 单次抓取超时；<=0 = 30s。
	Timeout time.Duration
	// Now 时间源（robots 缓存过期判定；测试注入）。
	Now func() time.Time
}

// Client 实现 port.WebFetch。
type Client struct {
	client   *http.Client
	proxyFor func(*http.Request) (*url.URL, error)
	ua       string
	robots   bool
	private  bool
	maxByte  int64
	now      func() time.Time

	mu        sync.Mutex
	robotsTxt map[string]robotsEntry
}

type robotsEntry struct {
	data    *robotstxt.RobotsData
	expires time.Time
}

// New 构造本地抓取器。
func New(cfg Config) (*Client, error) {
	ua := strings.TrimSpace(cfg.UserAgent)
	if ua == "" {
		ua = defaultUserAgent
	}
	maxBytes := cfg.MaxBytes
	if maxBytes <= 0 {
		maxBytes = DefaultMaxBytes
	}
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	now := cfg.Now
	if now == nil {
		now = time.Now
	}
	c := &Client{
		ua:        ua,
		robots:    cfg.RespectRobots == nil || *cfg.RespectRobots,
		private:   cfg.AllowPrivateIPs,
		maxByte:   maxBytes,
		now:       now,
		robotsTxt: make(map[string]robotsEntry),
	}
	c.proxyFor = proxyFunc(cfg.Proxy)
	transport := &http.Transport{
		Proxy:                 c.proxyFor,
		DialContext:           c.dialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          8,
		IdleConnTimeout:       60 * time.Second,
		TLSHandshakeTimeout:   15 * time.Second,
		ResponseHeaderTimeout: timeout,
	}
	c.client = &http.Client{
		Timeout:   timeout,
		Transport: transport,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= maxRedirects {
				return fmt.Errorf("stopped after %d redirects", maxRedirects)
			}
			if err := c.checkURL(req.URL); err != nil {
				return err
			}
			// 每一跳都复核目标地址：「公网 302 到内网」是绕过 URL 校验最常见的方式。
			return c.checkTarget(req.Context(), req.URL)
		},
	}
	return c, nil
}

// Provider 实现 port.WebFetch。
func (c *Client) Provider() string { return "local" }

// proxyFunc 代理选择：显式配置优先，其次环境变量（缺省 Transport 的行为）。
func proxyFunc(raw string) func(*http.Request) (*url.URL, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return http.ProxyFromEnvironment
	}
	u, err := url.Parse(raw)
	if err != nil {
		// 配错了不静默直连（那会把本该走代理的请求打到目标站），改成永远失败的代理。
		return func(*http.Request) (*url.URL, error) {
			return nil, fmt.Errorf("invalid proxy %q: %w", raw, err)
		}
	}
	return http.ProxyURL(u)
}

// proxyHost 代理自身的 host:port（大小写归一）；空 = 不走代理。
// 拨号校验要给它开个口子：代理常常就装在本机（127.0.0.1:7897），
// 不放行的话「配了代理」和「全拒绝」是同一个结果。
func (c *Client) proxyHost() string {
	req, err := http.NewRequest(http.MethodGet, "https://proxy-probe.invalid/", nil)
	if err != nil {
		return ""
	}
	u, err := c.proxyFor(req)
	if err != nil || u == nil {
		return ""
	}
	host := u.Hostname()
	if host == "" {
		return ""
	}
	port := u.Port()
	if port == "" {
		switch u.Scheme {
		case "http":
			port = "80"
		case "https":
			port = "443"
		}
	}
	return strings.ToLower(net.JoinHostPort(host, port))
}

// dialContext 在真正建连前校验解析出来的 IP——在 Dial 层做而不是只查 URL，
// 才能挡住「域名解析到内网」与 DNS rebinding（重定向后的每一跳都会重新拨号）。
//
// 走代理时目标地址由代理去解析，这道门禁只对**直连**生效（代理自己就在本机时尤其明显）。
func (c *Client) dialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, err
	}
	allowLocal := c.private || strings.EqualFold(net.JoinHostPort(host, port), c.proxyHost())
	ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return nil, err
	}
	var lastErr error
	for _, ip := range ips {
		if !allowLocal && blockedIP(ip.IP) {
			lastErr = fmt.Errorf("refusing to connect to %s (%s): private or local address", host, ip.IP)
			continue
		}
		d := net.Dialer{}
		conn, err := d.DialContext(ctx, network, net.JoinHostPort(ip.IP.String(), port))
		if err == nil {
			return conn, nil
		}
		lastErr = err
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("no address for %s", host)
	}
	return nil, lastErr
}

// checkURL URL 形态校验（协议、凭据、主机）。IP 校验在拨号层，这里只挡明显的。
func (c *Client) checkURL(u *url.URL) error {
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("only http and https URLs are supported, got %q", u.Scheme)
	}
	if u.Host == "" {
		return fmt.Errorf("URL has no host")
	}
	if u.User != nil {
		return fmt.Errorf("URL must not carry credentials")
	}
	return nil
}

// checkTarget 目标地址的内网校验（拨号层之外的第二道，给走代理的请求用）。
//
// 走代理时目标由代理解析，拨号层只看得见代理地址——所以这里先自己解析一遍目标域名。
// 本地解析失败不算拒绝（可能是代理侧才有的域名），交给拨号层与代理去试；
// 但字面量 IP 不依赖 DNS，私网/回环一律当场拒绝。
func (c *Client) checkTarget(ctx context.Context, u *url.URL) error {
	if c.private {
		return nil
	}
	host := u.Hostname()
	if ip := net.ParseIP(host); ip != nil {
		if blockedIP(ip) {
			return fmt.Errorf("refusing to fetch %s: private or local address", host)
		}
		return nil
	}
	ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return nil
	}
	for _, ip := range ips {
		if blockedIP(ip.IP) {
			return fmt.Errorf("refusing to fetch %s: resolves to private or local address %s", host, ip.IP)
		}
	}
	return nil
}

// blockedIP 私网/回环/链路本地/组播/未指定地址判定。
func blockedIP(ip net.IP) bool {
	if ip == nil {
		return true
	}
	return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() || ip.IsInterfaceLocalMulticast() ||
		ip.IsMulticast() || ip.IsUnspecified()
}

// Fetch 实现 port.WebFetch。
func (c *Client) Fetch(ctx context.Context, rawURL string) (port.WebPage, error) {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return port.WebPage{}, fmt.Errorf("%w: URL is empty", port.ErrWebBadInput)
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		return port.WebPage{}, fmt.Errorf("%w: %v", port.ErrWebBadInput, err)
	}
	if err := c.checkURL(u); err != nil {
		return port.WebPage{}, fmt.Errorf("%w: %v", port.ErrWebBadInput, err)
	}
	// 目标地址的内网校验放在最前面：连 robots.txt 都不必去取。
	if err := c.checkTarget(ctx, u); err != nil {
		return port.WebPage{}, err
	}
	if c.robots {
		ok, err := c.allowedByRobots(ctx, u)
		if err != nil {
			// robots 取不到（网络错误/非 2xx）不阻断抓取：与主流抓取器同口径，
			// 只有明确的 Disallow 才拒绝。
			ok = true
		}
		if !ok {
			return port.WebPage{}, fmt.Errorf("robots.txt of %s disallows fetching %s", u.Host, u.Path)
		}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return port.WebPage{}, fmt.Errorf("%w: %v", port.ErrWebBadInput, err)
	}
	req.Header.Set("User-Agent", c.ua)
	req.Header.Set("Accept", "text/html,application/xhtml+xml,text/plain;q=0.9,*/*;q=0.5")
	req.Header.Set("Accept-Language", "zh-CN,zh;q=0.9,en;q=0.8")
	resp, err := c.client.Do(req)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return port.WebPage{}, err
		}
		return port.WebPage{}, fmt.Errorf("fetch %s: %w", u.Redacted(), err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return port.WebPage{}, fmt.Errorf("fetch %s: HTTP %d", u.Redacted(), resp.StatusCode)
	}
	ctype := strings.ToLower(resp.Header.Get("Content-Type"))
	if !fetchableType(ctype) {
		return port.WebPage{}, fmt.Errorf("fetch %s: unsupported content type %q (only HTML and plain text are converted)", u.Redacted(), ctype)
	}
	raw, truncated, err := readLimited(resp.Body, c.maxByte)
	if err != nil {
		return port.WebPage{}, fmt.Errorf("fetch %s: %w", u.Redacted(), err)
	}
	if truncated {
		// 截断的 HTML 会让正文提取器丢掉收尾标签：按「读到了但只有前半篇」处理，
		// 仍然尽量给出正文，而不是整篇失败。
		raw = append(raw, []byte("</body></html>")...)
	}
	page, err := c.extract(u, ctype, raw)
	if err != nil {
		return port.WebPage{}, err
	}
	page.URL = u.String()
	page.Provider = c.Provider()
	return page, nil
}

// fetchableType 能转成 Markdown 的类型白名单。
func fetchableType(ctype string) bool {
	ctype = strings.TrimSpace(strings.SplitN(ctype, ";", 2)[0])
	switch ctype {
	case "", "text/html", "application/xhtml+xml", "text/plain", "text/markdown":
		return true
	default:
		return false
	}
}

// readLimited 读到上限为止；truncated = 还有剩余没读。
func readLimited(r io.Reader, max int64) ([]byte, bool, error) {
	data, err := io.ReadAll(io.LimitReader(r, max+1))
	if err != nil {
		return nil, false, err
	}
	if int64(len(data)) > max {
		return data[:max], true, nil
	}
	return data, false, nil
}

// extract 字符集归一 + 正文提取 + Markdown 转换。
func (c *Client) extract(u *url.URL, ctype string, raw []byte) (port.WebPage, error) {
	if strings.HasPrefix(ctype, "text/plain") || strings.HasPrefix(ctype, "text/markdown") {
		text, err := decodeToUTF8(bytes.NewReader(raw), ctype)
		if err != nil {
			return port.WebPage{}, fmt.Errorf("fetch %s: %w", u.Redacted(), err)
		}
		body := strings.TrimSpace(string(text))
		if body == "" {
			return port.WebPage{}, fmt.Errorf("fetch %s: page has no text", u.Redacted())
		}
		return port.WebPage{Title: firstLine(body), Markdown: body}, nil
	}
	utf8HTML, err := decodeToUTF8(bytes.NewReader(raw), ctype)
	if err != nil {
		return port.WebPage{}, fmt.Errorf("fetch %s: %w", u.Redacted(), err)
	}
	title := ""
	markdown := ""
	if art, err := readability.FromReader(bytes.NewReader(utf8HTML), u); err == nil {
		title = strings.TrimSpace(art.Title())
		var buf bytes.Buffer
		if err := art.RenderHTML(&buf); err == nil && buf.Len() > 0 {
			markdown = convert(buf.Bytes())
		}
	}
	if strings.TrimSpace(markdown) == "" {
		// 不是「文章形态」的页面（列表页、单页应用壳、文档首页）：退化成整页转换。
		// 噪声多，但总比「没有正文」有用；调用方会看到标题缺失。
		body, t := wholePageHTML(utf8HTML)
		if title == "" {
			title = t
		}
		markdown = convert(body)
	}
	markdown = strings.TrimSpace(markdown)
	if markdown == "" {
		return port.WebPage{}, fmt.Errorf("fetch %s: no readable content", u.Redacted())
	}
	return port.WebPage{Title: title, Markdown: markdown}, nil
}

// convert HTML → Markdown（失败时退化为纯文本，绝不返回空）。
func convert(htmlBytes []byte) string {
	md, err := htmltomarkdown.ConvertString(string(htmlBytes))
	if err == nil {
		return md
	}
	if text := plainText(htmlBytes); text != "" {
		return text
	}
	return ""
}

// decodeToUTF8 按 Content-Type / <meta charset> 归一成 UTF-8。
// x/net/html/charset 自己处理 BOM、meta 与 GBK/Shift-JIS 等常见编码。
func decodeToUTF8(r io.Reader, ctype string) ([]byte, error) {
	rd, err := charset.NewReader(r, ctype)
	if err != nil {
		return nil, err
	}
	return io.ReadAll(rd)
}

// wholePageHTML 去掉脚本/样式/导航噪音后的整页正文 + <title>。
func wholePageHTML(doc []byte) ([]byte, string) {
	root, err := html.Parse(bytes.NewReader(doc))
	if err != nil {
		return doc, ""
	}
	title := ""
	var strip func(*html.Node)
	strip = func(n *html.Node) {
		if n.Type == html.ElementNode {
			switch n.Data {
			case "script", "style", "noscript", "template", "svg", "iframe":
				n.Parent.RemoveChild(n)
				return
			case "title":
				if title == "" && n.FirstChild != nil {
					title = strings.TrimSpace(n.FirstChild.Data)
				}
			}
		}
		for child := n.FirstChild; child != nil; {
			next := child.NextSibling
			strip(child)
			child = next
		}
	}
	strip(root)
	var buf bytes.Buffer
	if err := html.Render(&buf, root); err != nil {
		return doc, title
	}
	return buf.Bytes(), title
}

// plainText 兜底：把 HTML 标签去掉只剩文本。
func plainText(doc []byte) string {
	root, err := html.Parse(bytes.NewReader(doc))
	if err != nil {
		return ""
	}
	var sb strings.Builder
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.TextNode {
			if s := strings.TrimSpace(n.Data); s != "" {
				sb.WriteString(s)
				sb.WriteString("\n")
			}
		}
		for child := n.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(root)
	return strings.TrimSpace(sb.String())
}

// firstLine 纯文本页面的标题：第一行非空文本。
func firstLine(s string) string {
	for _, line := range strings.Split(s, "\n") {
		if t := strings.TrimSpace(line); t != "" {
			if n := len([]rune(t)); n > 120 {
				return string([]rune(t)[:120])
			}
			return t
		}
	}
	return ""
}

// allowedByRobots 查 robots.txt（按 host 缓存 10 分钟）。
// 返回 (true, nil) = 允许；错误 = 取不到 robots（调用方按允许处理）。
func (c *Client) allowedByRobots(ctx context.Context, u *url.URL) (bool, error) {
	origin := u.Scheme + "://" + u.Host
	c.mu.Lock()
	entry, ok := c.robotsTxt[origin]
	c.mu.Unlock()
	if ok && c.now().Before(entry.expires) {
		return entry.data.TestAgent(u.Path, "Aquarius"), nil
	}
	data, err := c.fetchRobots(ctx, origin)
	if err != nil {
		return true, err
	}
	c.mu.Lock()
	c.robotsTxt[origin] = robotsEntry{data: data, expires: c.now().Add(robotsTTL)}
	c.mu.Unlock()
	return data.TestAgent(u.Path, "Aquarius"), nil
}

// fetchRobots 取一份 robots.txt；非 2xx 视为「没有限制」。
func (c *Client) fetchRobots(ctx context.Context, origin string) (*robotstxt.RobotsData, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, origin+"/robots.txt", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", c.ua)
	resp, err := c.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _, err := readLimited(resp.Body, robotsMaxBytes)
	if err != nil {
		return nil, err
	}
	return robotstxt.FromStatusAndBytes(resp.StatusCode, body)
}

var _ port.WebFetch = (*Client)(nil)
