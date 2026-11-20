package llm

// title_test.go 会话标题（D309）在三套协议上的收发与 400 收敛（D307）
//
// 三处要点：请求里只在 req.Title 为真时才出现 `title.enabled`；响应里的标题在三套
// 协议各有位置（chat 的 delta.title / Messages 的 message_delta / Responses 的
// response.title）；端点不认时记的是 unsupported_server_caps，不是 unsupported_params。

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/Tonyjh07/Aquarius/internal/port"
)

// TestChatRequestCarriesTitle chat 请求体：开了才发，关了一字不多。
func TestChatRequestCarriesTitle(t *testing.T) {
	req := port.GenerateRequest{Model: "deepseek-flash"}
	off, err := encodeChatRequest(req, true, nil)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	if bytes.Contains(off, []byte(`"title"`)) {
		t.Fatalf("未开启时不该出现 title：%s", off)
	}
	req.Title = true
	on, err := encodeChatRequest(req, true, nil)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	if !bytes.Contains(on, []byte(`"title":{"enabled":true}`)) {
		t.Fatalf("开启时应发 title.enabled：%s", on)
	}
}

// TestChatDeltaTitle chat 的标题在最后一个 chunk 的 delta 里，一次给全。
func TestChatDeltaTitle(t *testing.T) {
	d, ok, err := decodeChatPayload(`{"choices":[{"delta":{"title":"北京明天天气"}}]}`)
	if err != nil || !ok {
		t.Fatalf("decode: ok=%v err=%v", ok, err)
	}
	if d.Title != "北京明天天气" {
		t.Fatalf("title = %q", d.Title)
	}
	if d.Text != "" {
		t.Fatalf("标题不该混进正文：%q", d.Text)
	}
}

// TestAnthropicDeltaTitle Messages 协议：标题在 message_delta 的 delta 里。
func TestAnthropicDeltaTitle(t *testing.T) {
	s := newRawStream(context.Background(), func() {}, io.NopCloser(strings.NewReader("")), nil)
	d, ok, err := decodeAnthropicEvent(s, &anthropicStreamState{},
		`{"type":"message_delta","delta":{"stop_reason":"end_turn","title":"北京天气"},"usage":{"output_tokens":12}}`)
	if err != nil || !ok {
		t.Fatalf("decode: ok=%v err=%v", ok, err)
	}
	if d.Title != "北京天气" {
		t.Fatalf("title = %q", d.Title)
	}
}

// TestResponsesDeltaTitle Responses：标题在结束事件的 response.title 上。
func TestResponsesDeltaTitle(t *testing.T) {
	s := newRawStream(context.Background(), func() {}, io.NopCloser(strings.NewReader("")), nil)
	d, ok, err := s.decodeResponses(`{"type":"response.completed","response":{"title":"北京天气"}}`)
	if err != nil || !ok {
		t.Fatalf("decode: ok=%v err=%v", ok, err)
	}
	if d.Title != "北京天气" {
		t.Fatalf("title = %q", d.Title)
	}
}

// TestTitleCapRejectedOnce 端点 400 点名 title 后：本次运行不再发，回调只记一次，
// 且记的是服务端能力那一本账。
func TestTitleCapRejectedOnce(t *testing.T) {
	var noted []string
	c := &Client{
		omit: map[string]bool{},
		caps: map[string]bool{},
		noteCap: func(n string) {
			noted = append(noted, n)
		},
	}
	req := port.GenerateRequest{Title: true}
	c.noteCapFrom400(400, `{"error":{"message":"unknown parameter: title"}}`, req)
	if len(noted) != 1 || noted[0] != port.ServerCapTitle {
		t.Fatalf("应记一次 title，实际 %v", noted)
	}
	if c.wantTitle(true) {
		t.Fatal("记过之后本次运行不该再发 title")
	}
	// 重复的 400 不再触发第二次记录。
	c.noteCapFrom400(400, `{"error":{"message":"unknown parameter: title"}}`, req)
	if len(noted) != 1 {
		t.Fatalf("重复记录：%v", noted)
	}
}

// TestTitleCapNotRejectedOnOtherErrors 报错没点名 title，或状态码不是 400/422 时不记。
func TestTitleCapNotRejectedOnOtherErrors(t *testing.T) {
	var noted []string
	c := &Client{
		omit: map[string]bool{},
		caps: map[string]bool{},
		noteCap: func(n string) {
			noted = append(noted, n)
		},
	}
	req := port.GenerateRequest{Title: true}
	c.noteCapFrom400(400, `{"error":{"message":"unknown parameter: safety_check"}}`, req)
	c.noteCapFrom400(429, `{"error":{"message":"unknown parameter: title"}}`, req)
	if len(noted) != 0 {
		t.Fatalf("不该记录：%v", noted)
	}
	if !c.wantTitle(true) {
		t.Fatal("没记过就该照发")
	}
}

// TestChatStripTitleRecordsCap 完整回归：chat 400 提到 title → 剥离重试成功 →
// 记进服务端能力清单，而不是 unsupported_params。
func TestChatStripTitleRecordsCap(t *testing.T) {
	req := port.GenerateRequest{Model: "m", Title: true}
	stripped := unsupportedFields(`{"error":{"message":"unknown parameter: title"}}`, req, true)
	if len(stripped) != 1 || stripped[0] != port.ServerCapTitle {
		t.Fatalf("stripped = %v", stripped)
	}
}

// TestGenerateStripsUnsupportedTitle 走完 HTTP 一遍：首包带 title → 400 点名 title →
// 剥离重试不带 → 记进服务端能力清单 → 下一轮首包直接省略（D307 那半条验收）。
func TestGenerateStripsUnsupportedTitle(t *testing.T) {
	var mu sync.Mutex
	var bodies []string
	var n int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		bodies = append(bodies, string(b))
		n++
		first := n == 1
		mu.Unlock()
		if first {
			w.WriteHeader(http.StatusBadRequest)
			fmt.Fprint(w, `{"error":{"message":"Unknown parameter: 'title'"}}`)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, `data: {"choices":[{"delta":{"content":"ok"}}]}`+"\n"+"data: [DONE]\n")
	}))
	defer srv.Close()

	var notedParam, notedCap []string
	c, err := New(Config{
		BaseURL:         srv.URL,
		NoteUnsupported: func(f string) { notedParam = append(notedParam, f) },
		NoteServerCap:   func(n string) { notedCap = append(notedCap, n) },
	})
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	gen := func() error {
		s, err := c.Generate(context.Background(), port.GenerateRequest{
			Model:    "m",
			Messages: []port.PromptMessage{{Role: port.PromptUser, Content: []port.PromptPart{{Kind: port.PromptText, Text: "hi"}}}},
			Title:    true,
		})
		if err != nil {
			return err
		}
		return s.Close()
	}
	if err := gen(); err != nil {
		t.Fatalf("剥离重试后应成功: %v", err)
	}
	if err := gen(); err != nil {
		t.Fatalf("进程内记忆后应直接成功: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(bodies) != 3 {
		t.Fatalf("requests = %d, want 3（剥离重试 1 + 记忆后 1）", len(bodies))
	}
	if !strings.Contains(bodies[0], `"title":{"enabled":true}`) {
		t.Fatalf("首包应带 title: %.400s", bodies[0])
	}
	if strings.Contains(bodies[1], `"title"`) {
		t.Fatalf("剥离后不应带 title: %.400s", bodies[1])
	}
	if strings.Contains(bodies[2], `"title"`) {
		t.Fatalf("记录后首包即应省略: %.400s", bodies[2])
	}
	if len(notedCap) != 1 || notedCap[0] != port.ServerCapTitle {
		t.Fatalf("notedCap = %v, want [title]", notedCap)
	}
	if len(notedParam) != 0 {
		t.Fatalf("title 不该记进 unsupported_params: %v", notedParam)
	}
}
