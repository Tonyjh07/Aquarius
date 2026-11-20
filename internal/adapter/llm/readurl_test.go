package llm

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Tonyjh07/Aquarius/internal/port"
)

// readURLSSE 一轮「读链接 → 回答」的最小流：调用块与结果块各一，正文跟在后面。
const readURLSSE = `data: {"type":"message_start","message":{"id":"m1","usage":{"input_tokens":10}}}` + "\n" +
	`data: {"type":"content_block_start","index":0,"content_block":{"type":"server_tool_use","id":"srvtoolu_rl_00_a","name":"deepseek_read_url","input":{"action":"read_link","url":"https://example.com/a"},"caller":{"type":"direct"},"kind":"read_link"}}` + "\n" +
	`data: {"type":"content_block_stop","index":0}` + "\n" +
	`data: {"type":"content_block_start","index":1,"content_block":{"type":"web_fetch_tool_result","tool_use_id":"srvtoolu_rl_00_a","content":{"type":"web_fetch_result","url":"https://example.com/a","content":{"type":"document","source":{"type":"text","media_type":"text/plain","data":""},"title":"Example"},"published_at":"2026-01-01"},"kind":"read_link","signature":"SIGabc"}}` + "\n" +
	`data: {"type":"content_block_stop","index":1}` + "\n" +
	`data: {"type":"content_block_start","index":2,"content_block":{"type":"text","text":""}}` + "\n" +
	`data: {"type":"content_block_delta","index":2,"delta":{"type":"text_delta","text":"read"}}` + "\n" +
	`data: {"type":"content_block_stop","index":2}` + "\n" +
	`data: {"type":"message_stop"}` + "\n"

// TestAnthropicReadURLDeclared 声明只出现在服务端工具这一项，且形状是 {type,name}。
func TestAnthropicReadURLDeclared(t *testing.T) {
	srv, rec := newAnthropicServer(t, `data: {"type":"message_stop"}`+"\n")
	c := anthropicClient(t, srv.URL+"/v1", "ak")

	s, err := c.Generate(context.Background(), port.GenerateRequest{
		Model:       "deepseek-flash",
		Messages:    []port.PromptMessage{{Role: port.PromptUser, Content: []port.PromptPart{{Kind: port.PromptText, Text: "读一下 https://example.com/a"}}}},
		ServerTools: []string{port.ServerCapReadURL},
	})
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	_ = s.Close()

	var body struct {
		Tools []map[string]any `json:"tools"`
	}
	if err := json.Unmarshal([]byte(rec.body(0)), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if len(body.Tools) != 1 {
		t.Fatalf("tools = %d 项，want 1", len(body.Tools))
	}
	if body.Tools[0]["type"] != "deepseek_read_url" || body.Tools[0]["name"] != "deepseek_read_url" {
		t.Fatalf("声明形状不对：%v", body.Tools[0])
	}
	if _, ok := body.Tools[0]["input_schema"]; ok {
		t.Fatal("服务端工具不该带 input_schema")
	}
}

// TestAnthropicReadURLStreamed 两个块都要以原样原文交出去，正文照常流出。
func TestAnthropicReadURLStreamed(t *testing.T) {
	srv, _ := newAnthropicServer(t, readURLSSE)
	c := anthropicClient(t, srv.URL+"/v1", "ak")

	s, err := c.Generate(context.Background(), port.GenerateRequest{
		Model:       "deepseek-flash",
		Messages:    []port.PromptMessage{{Role: port.PromptUser, Content: []port.PromptPart{{Kind: port.PromptText, Text: "读 https://example.com/a"}}}},
		ServerTools: []string{port.ServerCapReadURL},
	})
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	defer s.Close()

	var blocks []port.ServerBlock
	var text strings.Builder
	for {
		d, err := s.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("recv: %v", err)
		}
		if d.Server != nil {
			blocks = append(blocks, *d.Server)
		}
		text.WriteString(d.Text)
	}
	if text.String() != "read" {
		t.Fatalf("正文 = %q", text.String())
	}
	if len(blocks) != 2 {
		t.Fatalf("服务端块 = %d 个，want 2", len(blocks))
	}
	if blocks[0].URL != "https://example.com/a" || blocks[0].Kind != "read_link" || blocks[0].Title != "" {
		t.Fatalf("调用块字段不对：%+v", blocks[0])
	}
	if blocks[1].URL != "https://example.com/a" || blocks[1].Title != "Example" {
		t.Fatalf("结果块字段不对：%+v", blocks[1])
	}
	// signature 必须完整留在结果块的原文里——那是网页正文本身。
	if !strings.Contains(string(blocks[1].Raw), `"signature":"SIGabc"`) {
		t.Fatalf("结果块丢了 signature：%s", blocks[1].Raw)
	}
}

// TestAnthropicReadURLEchoed 回传：assistant 的块排在思考之后、正文之前，且原样。
func TestAnthropicReadURLEchoed(t *testing.T) {
	rawResult := json.RawMessage(`{"type":"web_fetch_tool_result","tool_use_id":"srvtoolu_rl_00_a","kind":"read_link","signature":"SIGabc","content":{"type":"web_fetch_result","url":"https://example.com/a"}}`)
	body, err := encodeAnthropicRequest(port.GenerateRequest{
		Model: "deepseek-flash",
		Messages: []port.PromptMessage{
			{Role: port.PromptUser, Content: []port.PromptPart{{Kind: port.PromptText, Text: "读 https://example.com/a"}}},
			{Role: port.PromptAssistant,
				Reasoning:          "想过",
				ReasoningSignature: anthropicSigPrefix + "SIG9",
				ServerBlocks:       []json.RawMessage{rawResult},
				Content:            []port.PromptPart{{Kind: port.PromptText, Text: "read"}}},
		},
	})
	if err != nil {
		t.Fatalf("encode: %v", err)
	}

	var out struct {
		Messages []struct {
			Role    string            `json:"role"`
			Content []json.RawMessage `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	var kinds []string
	for _, b := range out.Messages[1].Content {
		var head struct {
			Type string `json:"type"`
		}
		_ = json.Unmarshal(b, &head)
		kinds = append(kinds, head.Type)
	}
	want := []string{"thinking", "web_fetch_tool_result", "text"}
	if strings.Join(kinds, ",") != strings.Join(want, ",") {
		t.Fatalf("块序 = %v，want %v", kinds, want)
	}
	if !strings.Contains(string(out.Messages[1].Content[1]), `"signature":"SIGabc"`) {
		t.Fatalf("回传的块丢了 signature：%s", out.Messages[1].Content[1])
	}
}

// TestAnthropicReadURLRejected 400 点名工具名 → 记进 unsupported_server_caps。
func TestAnthropicReadURLRejected(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte("{\"error\":{\"message\":\"tools: other tools cannot be used together with deepseek_read_url yet\"}}"))
	}))
	defer srv.Close()

	var noted []string
	c := anthropicClient(t, srv.URL+"/v1", "ak")
	c.noteCap = func(cap string) { noted = append(noted, cap) }

	if _, err := c.Generate(context.Background(), port.GenerateRequest{
		Model:       "deepseek-flash",
		Messages:    []port.PromptMessage{{Role: port.PromptUser, Content: []port.PromptPart{{Kind: port.PromptText, Text: "hi"}}}},
		ServerTools: []string{port.ServerCapReadURL},
	}); err == nil {
		t.Fatal("want error")
	}
	if len(noted) != 1 || noted[0] != port.ServerCapReadURL {
		t.Fatalf("记录 = %v，want [read_url]", noted)
	}
}
