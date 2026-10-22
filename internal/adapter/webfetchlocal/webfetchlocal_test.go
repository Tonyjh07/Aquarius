package webfetchlocal

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Tonyjh07/Aquarius/internal/port"
)

// newLocal 造一个放开私网限制的抓取器（httptest 监听 127.0.0.1，测试必须放行）。
func newLocal(t *testing.T, mods ...func(*Config)) *Client {
	t.Helper()
	cfg := Config{AllowPrivateIPs: true}
	for _, m := range mods {
		m(&cfg)
	}
	c, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return c
}

const articleHTML = `<!doctype html><html><head><meta charset="utf-8"><title>示例文章</title></head>
<body><nav>导航</nav><article><h1>标题</h1>
<p>第一段正文，包含 <b>粗体</b> 与 <a href="/next">链接</a>。</p>
<h2>小节</h2><ul><li>一</li><li>二</li></ul>
<p>` + `长正文` + `</p></article><script>alert(1)</script></body></html>`

func TestFetchConvertsArticleToMarkdown(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/robots.txt" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		io.WriteString(w, articleHTML)
	}))
	defer srv.Close()
	c := newLocal(t, func(c *Config) { c.RespectRobots = boolPtr(false) })
	page, err := c.Fetch(context.Background(), srv.URL+"/article")
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if page.Title != "示例文章" {
		t.Fatalf("title = %q", page.Title)
	}
	if page.Provider != "local" || page.URL != srv.URL+"/article" {
		t.Fatalf("page 元信息 = %+v", page)
	}
	for _, want := range []string{"标题", "第一段正文", "**粗体**", "小节", "- 一"} {
		if !strings.Contains(page.Markdown, want) {
			t.Fatalf("Markdown 缺 %q：\n%s", want, page.Markdown)
		}
	}
	if strings.Contains(page.Markdown, "alert(1)") {
		t.Fatalf("脚本不该进正文：\n%s", page.Markdown)
	}
}

func TestFetchPlainText(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		io.WriteString(w, "第一行标题\n\n正文内容")
	}))
	defer srv.Close()
	c := newLocal(t, func(c *Config) { c.RespectRobots = boolPtr(false) })
	page, err := c.Fetch(context.Background(), srv.URL+"/notes.txt")
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if page.Title != "第一行标题" || !strings.Contains(page.Markdown, "正文内容") {
		t.Fatalf("page = %+v", page)
	}
}

func TestFetchRejectsBadURLs(t *testing.T) {
	c := newLocal(t)
	for _, u := range []string{"", "file:///C:/Windows/win.ini", "ftp://e.com/a", "https://user:pw@e.com/a"} {
		if _, err := c.Fetch(context.Background(), u); !errors.Is(err, port.ErrWebBadInput) {
			t.Fatalf("%q = %v，应为 ErrWebBadInput", u, err)
		}
	}
}

func TestFetchBlocksPrivateTargetsByDefault(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "<html><body>secret</body></html>")
	}))
	defer srv.Close()
	c, err := New(Config{RespectRobots: boolPtr(false)})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	_, err = c.Fetch(context.Background(), srv.URL+"/")
	if err == nil {
		t.Fatal("缺省应拒绝回环/私网目标")
	}
	if !strings.Contains(err.Error(), "private or local address") {
		t.Fatalf("错误应说明原因：%v", err)
	}
}

func TestFetchBlocksRedirectToNonHTTP(t *testing.T) {
	// 重定向的每一跳都重新校验：跳到非 http(s) 必须拒绝。
	c := newLocal(t, func(c *Config) { c.RespectRobots = boolPtr(false) })
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "file:///C:/Windows/win.ini", http.StatusFound)
	}))
	defer srv.Close()
	if _, err := c.Fetch(context.Background(), srv.URL+"/"); err == nil {
		t.Fatal("重定向到非 http(s) 应被拒绝")
	}
}

func TestFetchRedirectsToBlockedHostWhenPrivateDisabled(t *testing.T) {
	// 私网禁用的抓取器连第一跳（127.0.0.1）都进不去——顺带证明拨号层校验生效。
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "<html><body>x</body></html>")
	}))
	defer srv.Close()
	c, err := New(Config{RespectRobots: boolPtr(false)})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := c.Fetch(context.Background(), srv.URL+"/"); err == nil {
		t.Fatal("私网目标应被拒绝")
	}
}

func TestFetchHonorsRobots(t *testing.T) {
	var fetched bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/robots.txt" {
			io.WriteString(w, "User-agent: *\nDisallow: /private\n")
			return
		}
		fetched = true
		io.WriteString(w, articleHTML)
	}))
	defer srv.Close()
	c := newLocal(t)
	if _, err := c.Fetch(context.Background(), srv.URL+"/private/page"); err == nil {
		t.Fatal("robots.txt 禁止的路径应拒绝")
	}
	if fetched {
		t.Fatal("被禁止的路径不该真的去取")
	}
	if _, err := c.Fetch(context.Background(), srv.URL+"/public/page"); err != nil {
		t.Fatalf("允许的路径应正常抓取：%v", err)
	}
}

func TestFetchIgnoresUnavailableRobots(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/robots.txt" {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		io.WriteString(w, articleHTML)
	}))
	defer srv.Close()
	c := newLocal(t)
	if _, err := c.Fetch(context.Background(), srv.URL+"/page"); err != nil {
		t.Fatalf("robots.txt 取不到不该阻断：%v", err)
	}
}

func TestFetchRejectsNonTextContentType(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/pdf")
		io.WriteString(w, "%PDF-1.7")
	}))
	defer srv.Close()
	c := newLocal(t, func(c *Config) { c.RespectRobots = boolPtr(false) })
	_, err := c.Fetch(context.Background(), srv.URL+"/doc.pdf")
	if err == nil || !strings.Contains(err.Error(), "unsupported content type") {
		t.Fatalf("二进制类型应明确拒绝：%v", err)
	}
}

func TestFetchTruncatesOversizedBody(t *testing.T) {
	// 超上限的 HTML 会被截断，但仍应尽量给出正文（而不是整篇失败）。
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		io.WriteString(w, "<html><head><title>Big</title></head><body><article><p>")
		io.WriteString(w, strings.Repeat("很长的正文。", 400))
	}))
	defer srv.Close()
	c := newLocal(t, func(c *Config) {
		c.RespectRobots = boolPtr(false)
		c.MaxBytes = 200
	})
	page, err := c.Fetch(context.Background(), srv.URL+"/big")
	if err != nil {
		t.Fatalf("截断不应失败：%v", err)
	}
	if page.Markdown == "" {
		t.Fatal("截断后仍应有正文")
	}
}

func TestFetchNon2xxIsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "gone", http.StatusNotFound)
	}))
	defer srv.Close()
	c := newLocal(t, func(c *Config) { c.RespectRobots = boolPtr(false) })
	if _, err := c.Fetch(context.Background(), srv.URL+"/missing"); err == nil {
		t.Fatal("404 应报错")
	}
}

func TestFetchThroughProxyAllowsLoopbackProxy(t *testing.T) {
	// 假代理：明文 http 目标时 Transport 会把绝对 URL 发给代理（不走 CONNECT）。
	var gotURI string
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotURI = r.RequestURI
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		io.WriteString(w, articleHTML)
	}))
	defer proxy.Close()

	// 代理装在本机（127.0.0.1）——拨号层必须给代理自身开口子，否则「配了代理」= 全拒绝。
	// 注意这里**不开** AllowPrivateIPs：放行的只能是代理地址，不能顺带放开目标。
	c, err := New(Config{Proxy: proxy.URL, RespectRobots: boolPtr(false)})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	page, err := c.Fetch(context.Background(), "http://example.invalid/article")
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if !strings.Contains(page.Markdown, "第一段正文") {
		t.Fatalf("代理返回的正文没被提取：%+v", page)
	}
	if !strings.Contains(gotURI, "example.invalid") {
		t.Fatalf("请求应经代理转发到目标，RequestURI = %q", gotURI)
	}
}

func TestFetchThroughProxyStillBlocksPrivateTarget(t *testing.T) {
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("私网目标不该被转发")
	}))
	defer proxy.Close()
	c, err := New(Config{Proxy: proxy.URL, RespectRobots: boolPtr(false)})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := c.Fetch(context.Background(), "http://169.254.169.254/latest/meta-data/"); err == nil {
		t.Fatal("云元数据地址应被拒绝")
	}
}

func TestBlockedIP(t *testing.T) {
	for _, s := range []string{"127.0.0.1", "10.1.2.3", "192.168.1.1", "172.16.0.1", "169.254.169.254", "::1", "fd00::1", "0.0.0.0"} {
		ip := parseIP(t, s)
		if !blockedIP(ip) {
			t.Fatalf("%s 应被判为内网/本地", s)
		}
	}
	for _, s := range []string{"8.8.8.8", "1.1.1.1", "2606:4700::1111"} {
		if blockedIP(parseIP(t, s)) {
			t.Fatalf("%s 不该被判为内网", s)
		}
	}
}

func boolPtr(b bool) *bool { return &b }

// parseIP 测试辅助：把字面量翻成 net.IP。
func parseIP(t *testing.T, s string) net.IP {
	t.Helper()
	ip := net.ParseIP(s)
	if ip == nil {
		t.Fatalf("bad IP %q", s)
	}
	return ip
}
