package llm

import (
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/Tonyjh07/Aquarius/internal/i18n"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

// trace.go 原始响应留档（§7.1，`/raw`）：在响应体外面套一层有界 tee，字节流原样留一份。
//
// 为什么在这里做、而不是在 Scan 循环里逐帧记录：解析路径一行都不用改，而「原始」就是
// 真的原始——空行、`event:` 行、注释行、`[DONE]` 全都在，不会因为我对协议的理解决定了
// 那份留档的样子。三套协议共用这条通道（三套协议都以 c.post 起步）。
//
// 留档是**尽力而为**的旁路：写失败不报错、超上限截断不中断收流、没有回调就整个跳过。

// traceLimit 单次留存的字节上限。够装下十万级 token 的 SSE（含 JSON 冗余），
// 超出只记 Truncated，收流照常。按最近 4 次留存，最坏 8 MB 常驻。
const traceLimit = 2 << 20

// traceHeaders 留档的响应头白名单。**不放 Authorization / Cookie**：这些不是排查对象，
// 出现在视图里只会制造泄露机会。
var traceHeaders = []string{"Content-Type", "X-Request-Id", "Request-Id", "Retry-After", "Server"}

// traceRec 一次交换的留档缓冲。除 cb 外的字段都在 mu 之下——init 之后仍会被
// 收流的 goroutine 写入。
type traceRec struct {
	cb func(port.WireTrace)

	mu    sync.Mutex
	meta  port.WireMeta
	notes []string
	buf   []byte
	total int
	trunc bool
	first time.Time
	once  sync.Once
}

// startTrace 建一份留档并填元信息。resp 必须非 nil。
func (c *Client) startTrace(path, model string, resp *http.Response) *traceRec {
	return &traceRec{
		cb: c.trace,
		meta: port.WireMeta{
			Proto:   string(c.proto),
			URL:     redactedURL(c.base + path),
			Model:   model,
			Status:  resp.StatusCode,
			Headers: pickHeaders(resp.Header),
			Started: time.Now(),
		},
	}
}

// write 追加一段实收字节（超上限后只记总数）。
func (t *traceRec) write(p []byte) {
	t.mu.Lock()
	t.total += len(p)
	if !t.trunc {
		room := traceLimit - len(t.buf)
		switch {
		case room <= 0:
			t.trunc = true
		case len(p) > room:
			t.buf = append(t.buf, p[:room]...)
			t.trunc = true
		default:
			t.buf = append(t.buf, p...)
		}
	}
	t.mu.Unlock()
}

// markFirst 记下首字节到达时刻（只记第一次）。
func (t *traceRec) markFirst() {
	t.mu.Lock()
	if t.first.IsZero() {
		t.first = time.Now()
	}
	t.mu.Unlock()
}

// note 追加一条适配器侧事实（剥离重试之类）。nil 安全。
func (t *traceRec) note(format string, args ...any) {
	if t == nil {
		return
	}
	t.mu.Lock()
	t.notes = append(t.notes, i18n.Tf(format, args...))
	t.mu.Unlock()
}

// readBody 读完一份错误响应体并返回给调用方的摘要。
//
// 原来是「读 4KB 就关」，于是错误体全文永远看不到——而长错误体（带字段清单的 400、
// 网关的 HTML 错误页）恰恰是排查要看的东西。这里读到留存上限为止，摘要另取前 500 字符。
func (t *traceRec) readBody(r io.Reader) string {
	b, _ := io.ReadAll(io.LimitReader(r, traceLimit))
	t.write(b)
	return snippet(string(b), 500)
}

// finish 交出留档（恰一次：正常收流、出错、中断三选一）。
func (t *traceRec) finish(end string) {
	if t == nil {
		return
	}
	t.once.Do(func() {
		t.mu.Lock()
		m := t.meta
		m.Bytes = t.total
		m.Truncated = t.trunc
		m.Elapsed = time.Since(m.Started)
		if !t.first.IsZero() {
			m.FirstByte = t.first.Sub(m.Started)
		}
		m.Notes = append([]string(nil), t.notes...)
		m.End = end
		raw := append([]byte(nil), t.buf...)
		t.mu.Unlock()
		if t.cb != nil {
			t.cb(port.WireTrace{Meta: m, Raw: raw})
		}
	})
}

// tracedBody 包一层读：收到的每一段都写进留档。tee 不改 Close——关连接仍是原 body 的事。
type tracedBody struct {
	io.ReadCloser
	tr *traceRec
}

func (b *tracedBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	if n > 0 {
		b.tr.markFirst()
		b.tr.write(p[:n])
	}
	return n, err
}

// pickHeaders 按白名单挑响应头（保持固定顺序，便于两次交换对照）。
func pickHeaders(h http.Header) []string {
	var out []string
	for _, name := range traceHeaders {
		if v := strings.TrimSpace(h.Get(name)); v != "" {
			out = append(out, name+": "+v)
		}
	}
	return out
}

// redactedURL 抹掉查询串的值，只留键名。有些兼容端点用 `?key=` 传密钥，
// 原样写进视图等于把密钥抄到导出文件里。
func redactedURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return strings.SplitN(raw, "?", 2)[0]
	}
	base := u.Scheme + "://" + u.Host + u.Path
	if u.RawQuery == "" {
		return base
	}
	pairs := strings.Split(u.RawQuery, "&")
	for i, kv := range pairs {
		if k, _, ok := strings.Cut(kv, "="); ok {
			pairs[i] = k + "=…"
		}
	}
	return base + "?" + strings.Join(pairs, "&")
}
