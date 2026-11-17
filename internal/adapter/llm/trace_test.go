package llm

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Tonyjh07/Aquarius/internal/port"
)

// traceClient 起一个返回给定响应体的假服务端，并收集留档回调。
func traceClient(t *testing.T, h http.HandlerFunc) (*Client, *[]port.WireTrace, func()) {
	t.Helper()
	srv := httptest.NewServer(h)
	var got []port.WireTrace
	c, err := New(Config{
		BaseURL:    srv.URL,
		HTTPClient: srv.Client(),
		Trace:      func(tr port.WireTrace) { got = append(got, tr) },
	})
	if err != nil {
		srv.Close()
		t.Fatalf("new client: %v", err)
	}
	return c, &got, srv.Close
}

// traceReq 最小生成请求（一条 user 文本）。
func traceReq() port.GenerateRequest {
	return port.GenerateRequest{
		Model: "m",
		Messages: []port.PromptMessage{{
			Role:    port.PromptUser,
			Content: []port.PromptPart{{Kind: port.PromptText, Text: "hi"}},
		}},
	}
}

// TestTraceCapturesRawStream 留档就是响应体字节本身：注释行、空行、[DONE] 一个不少，
// 逐字节等于服务端写出的内容。
func TestTraceCapturesRawStream(t *testing.T) {
	c, got, closeSrv := traceClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("X-Request-Id", "req-1")
		// 敏感头：白名单之外的一律不留档，这是「响应头也进视图」这条的口子。
		w.Header().Set("Authorization", "Bearer sk-secret")
		w.Header().Set("Set-Cookie", "sid=abc")
		fmt.Fprint(w, sseScript)
	})
	defer closeSrv()

	stream, err := c.Generate(context.Background(), traceReq())
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	collect(t, stream)
	_ = stream.Close() // 正常收尾后的 Close 不得改写结束原因

	if len(*got) != 1 {
		t.Fatalf("trace callbacks = %d, want 1", len(*got))
	}
	tr := (*got)[0]
	if string(tr.Raw) != sseScript {
		t.Fatalf("raw mismatch:\n got %q\nwant %q", tr.Raw, sseScript)
	}
	if tr.Meta.Bytes != len(sseScript) {
		t.Fatalf("bytes = %d, want %d", tr.Meta.Bytes, len(sseScript))
	}
	if tr.Meta.Status != http.StatusOK || tr.Meta.End != port.WireEndDone {
		t.Fatalf("meta = %+v, want 200/done", tr.Meta)
	}
	if tr.Meta.Proto != string(ProtoChat) {
		t.Fatalf("proto = %q", tr.Meta.Proto)
	}
	if len(tr.Meta.Headers) == 0 || !strings.Contains(strings.Join(tr.Meta.Headers, "|"), "X-Request-Id: req-1") {
		t.Fatalf("headers = %v, want x-request-id", tr.Meta.Headers)
	}
	for _, h := range tr.Meta.Headers {
		if strings.Contains(h, "sk-secret") || strings.Contains(h, "sid=abc") {
			t.Fatalf("留档带出了敏感响应头: %v", tr.Meta.Headers)
		}
	}
	if tr.Meta.Started.IsZero() {
		t.Fatal("started is zero")
	}
}

// TestTraceStatusErrorKeepsWholeBody 状态码拒绝时留整份错误体：错误行里的摘要只有前
// 500 字符，而真实错误体常常更长。
func TestTraceStatusErrorKeepsWholeBody(t *testing.T) {
	body := strings.Repeat("x", 800) + "TAIL-MARKER"
	c, got, closeSrv := traceClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprint(w, body)
	})
	defer closeSrv()

	_, err := c.Generate(context.Background(), traceReq())
	if err == nil {
		t.Fatal("generate succeeded, want status error")
	}
	if strings.Contains(err.Error(), "TAIL-MARKER") {
		t.Fatal("error text should carry only a truncated excerpt")
	}
	if len(*got) != 1 {
		t.Fatalf("trace callbacks = %d, want 1", len(*got))
	}
	tr := (*got)[0]
	if string(tr.Raw) != body {
		t.Fatalf("raw = %d bytes, want the whole %d-byte body", len(tr.Raw), len(body))
	}
	if tr.Meta.Status != http.StatusBadRequest || tr.Meta.End != port.WireEndStatus {
		t.Fatalf("meta = %+v, want 400/status", tr.Meta)
	}
}

// TestTraceTruncatesAtLimit 超过留存上限后只留前一段并标记；收流照常到底。
func TestTraceTruncatesAtLimit(t *testing.T) {
	c, got, closeSrv := traceClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		line := ": " + strings.Repeat("x", 1000) + "\n"
		for i := 0; i < traceLimit/len(line)+4; i++ {
			fmt.Fprint(w, line)
		}
		fmt.Fprint(w, "data: [DONE]\n\n")
	})
	defer closeSrv()

	stream, err := c.Generate(context.Background(), traceReq())
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	collect(t, stream)

	tr := (*got)[0]
	if !tr.Meta.Truncated {
		t.Fatalf("truncated = false, bytes = %d", tr.Meta.Bytes)
	}
	if len(tr.Raw) != traceLimit {
		t.Fatalf("raw len = %d, want %d", len(tr.Raw), traceLimit)
	}
	if tr.Meta.Bytes <= traceLimit {
		t.Fatalf("bytes = %d, want more than the limit", tr.Meta.Bytes)
	}
	if tr.Meta.End != port.WireEndDone {
		t.Fatalf("end = %q, want done", tr.Meta.End)
	}
}

// TestTraceCancelledKeepsPartial 中断也留档：半截流正是最需要看的形态。
func TestTraceCancelledKeepsPartial(t *testing.T) {
	c, got, closeSrv := traceClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, `data: {"choices":[{"delta":{"content":"half"}}]}`+"\n\n")
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	})
	defer closeSrv()

	stream, err := c.Generate(context.Background(), traceReq())
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if _, err := stream.Recv(); err != nil {
		t.Fatalf("recv: %v", err)
	}
	if err := stream.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	tr := (*got)[0]
	if tr.Meta.End != port.WireEndCancelled {
		t.Fatalf("end = %q, want cancelled", tr.Meta.End)
	}
	if !strings.Contains(string(tr.Raw), "half") {
		t.Fatalf("raw lost the partial frame: %q", tr.Raw)
	}
}

// TestTraceNotesStrippedRetry 剥离重试是适配器内部的决定：留档里得有一句话说明
// 「为什么这一次通了」，而留档正文只保留真正用了的那份响应。
func TestTraceNotesStrippedRetry(t *testing.T) {
	calls := 0
	c, got, closeSrv := traceClient(t, func(w http.ResponseWriter, _ *http.Request) {
		calls++
		if calls == 1 {
			w.WriteHeader(http.StatusBadRequest)
			fmt.Fprint(w, `{"error":{"message":"Unrecognized request argument: stream_options"}}`)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, sseScript)
	})
	defer closeSrv()

	stream, err := c.Generate(context.Background(), traceReq())
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	collect(t, stream)

	if len(*got) != 1 {
		t.Fatalf("trace callbacks = %d, want 1 (only the response actually used)", len(*got))
	}
	tr := (*got)[0]
	if tr.Meta.Status != http.StatusOK || tr.Meta.End != port.WireEndDone {
		t.Fatalf("meta = %+v, want the successful retry", tr.Meta)
	}
	if len(tr.Meta.Notes) != 1 || !strings.Contains(tr.Meta.Notes[0], "stream_options") {
		t.Fatalf("notes = %v, want one mentioning stream_options", tr.Meta.Notes)
	}
}

// TestRedactedURL 查询串只留键名：`?key=` 型端点不该把密钥抄进视图。
func TestRedactedURL(t *testing.T) {
	cases := map[string]string{
		"https://api.example.com/v1/chat/completions":              "https://api.example.com/v1/chat/completions",
		"https://gw.example.com/v1/chat/completions?key=sk-secret": "https://gw.example.com/v1/chat/completions?key=…",
		"https://gw.example.com/x?a=1&b=2":                         "https://gw.example.com/x?a=…&b=…",
		"https://gw.example.com/x?flag":                            "https://gw.example.com/x?flag",
	}
	for in, want := range cases {
		if got := redactedURL(in); got != want {
			t.Fatalf("redactedURL(%q) = %q, want %q", in, got, want)
		}
	}
}
