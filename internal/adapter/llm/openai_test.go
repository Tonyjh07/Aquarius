package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Tonyjh07/Aquarius/internal/domain/conversation"
	"github.com/Tonyjh07/Aquarius/internal/domain/tool"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

// sseScript 覆盖：文本分片、碎片化工具调用参数（Index 聚合）、finish 分片、usage 分片、[DONE]。
const sseScript = "" +
	": keep-alive\n" +
	"\n" +
	`data: {"choices":[{"delta":{"content":"Hello"}}]}` + "\n" +
	"\n" +
	`data: {"choices":[{"delta":{"content":", world"}}]}` + "\n" +
	`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"echo","arguments":""}}]}}]}` + "\n" +
	`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"m\":"}}]}}]}` + "\n" +
	`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"\"x\"}"}}]}}]}` + "\n" +
	`data: {"choices":[{"delta":{"tool_calls":[{"index":1,"id":"call_2","type":"function","function":{"name":"ping","arguments":"{}"}}]}}]}` + "\n" +
	`data: {"choices":[{"delta":{},"finish_reason":"tool_calls"}]}` + "\n" +
	`data: {"choices":[],"usage":{"prompt_tokens":12,"completion_tokens":34}}` + "\n" +
	"data: [DONE]\n" +
	"\n"

// collect 读到 EOF，返回拼接文本、全部工具分片与流末尾用量。
func collect(t *testing.T, s port.Stream) (string, []port.ToolCallDelta, *conversation.Usage) {
	t.Helper()
	var text strings.Builder
	var calls []port.ToolCallDelta
	var usage *conversation.Usage
	for {
		d, err := s.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("recv: %v", err)
		}
		text.WriteString(d.Text)
		calls = append(calls, d.ToolCalls...)
		if d.Usage != nil {
			usage = d.Usage
		}
	}
	return text.String(), calls, usage
}

func TestGenerateStreamsTextAndToolCalls(t *testing.T) {
	var gotPath, gotAuth string
	var gotBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		gotBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, sseScript)
	}))
	defer srv.Close()

	c, err := New(Config{BaseURL: srv.URL + "/v1/", APIKey: "sk-test"})
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	s, err := c.Generate(context.Background(), port.GenerateRequest{
		Model: "gpt-4o-mini",
		Messages: []port.PromptMessage{
			{Role: "system", Content: []port.PromptPart{{Kind: "text", Text: "sys"}}},
			{Role: "user", Content: []port.PromptPart{{Kind: "text", Text: "hi"}}},
			{Role: "assistant", ToolCalls: []tool.Call{{ID: "call_1", Name: "echo", Args: json.RawMessage(`{"m":"x"}`)}}},
			{Role: "tool", CallID: "call_1", Content: []port.PromptPart{{Kind: "text", Text: "pong"}}},
		},
		Tools:  []tool.Spec{{Name: "echo", Description: "回声", Schema: json.RawMessage(`{"type":"object","properties":{}}`)}},
		Params: port.Sampling{Temperature: 0.7, Stop: []string{"\n"}},
		Budget: port.TokenBudget{MaxOutputTokens: 128},
	})
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	defer s.Close()

	text, calls, usage := collect(t, s)
	if text != "Hello, world" {
		t.Fatalf("text = %q, want %q", text, "Hello, world")
	}
	if len(calls) != 4 {
		t.Fatalf("tool deltas = %d, want 4", len(calls))
	}
	if calls[0].Index != 0 || calls[0].ID != "call_1" || calls[0].Name != "echo" {
		t.Fatalf("call delta 0 = %+v", calls[0])
	}
	if calls[1].Index != 0 || calls[1].ArgsDelta != `{"m":` || calls[1].ID != "" {
		t.Fatalf("call delta 1 = %+v", calls[1])
	}
	if calls[3].Index != 1 || calls[3].ID != "call_2" || calls[3].Name != "ping" {
		t.Fatalf("call delta 3 = %+v", calls[3])
	}
	if usage == nil || usage.InputTokens != 12 || usage.OutputTokens != 34 {
		t.Fatalf("usage = %+v, want {in:12 out:34}", usage)
	}

	if gotPath != "/v1"+chatPath {
		t.Fatalf("path = %q", gotPath)
	}
	if gotAuth != "Bearer sk-test" {
		t.Fatalf("auth = %q", gotAuth)
	}
	var body struct {
		Model         string `json:"model"`
		Stream        bool   `json:"stream"`
		StreamOptions *struct {
			IncludeUsage bool `json:"include_usage"`
		} `json:"stream_options"`
		Temperature float64 `json:"temperature"`
		MaxTokens   int     `json:"max_tokens"`
		Stop        []string
		Messages    []struct {
			Role      string `json:"role"`
			Content   any    `json:"content"`
			ToolCalls []struct {
				ID       string `json:"id"`
				Function struct {
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				} `json:"function"`
			} `json:"tool_calls"`
			ToolCallID string `json:"tool_call_id"`
		} `json:"messages"`
		Tools []struct {
			Type     string `json:"type"`
			Function struct {
				Name       string          `json:"name"`
				Parameters json.RawMessage `json:"parameters"`
			} `json:"function"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(gotBody, &body); err != nil {
		t.Fatalf("parse request body: %v", err)
	}
	if body.Model != "gpt-4o-mini" || !body.Stream {
		t.Fatalf("body model/stream = %q/%v", body.Model, body.Stream)
	}
	if body.StreamOptions == nil || !body.StreamOptions.IncludeUsage {
		t.Fatal("stream_options.include_usage 应为 true")
	}
	if body.Temperature != 0.7 || body.MaxTokens != 128 || len(body.Stop) != 1 {
		t.Fatalf("params = temp %v max %v stop %v", body.Temperature, body.MaxTokens, body.Stop)
	}
	if len(body.Messages) != 4 {
		t.Fatalf("messages = %d, want 4", len(body.Messages))
	}
	if body.Messages[0].Role != "system" || body.Messages[1].Role != "user" {
		t.Fatalf("msg roles = %s/%s", body.Messages[0].Role, body.Messages[1].Role)
	}
	asst := body.Messages[2]
	if asst.Role != "assistant" || len(asst.ToolCalls) != 1 || asst.ToolCalls[0].ID != "call_1" ||
		asst.ToolCalls[0].Function.Name != "echo" || asst.ToolCalls[0].Function.Arguments != `{"m":"x"}` {
		t.Fatalf("assistant msg = %+v", asst)
	}
	tm := body.Messages[3]
	if tm.Role != "tool" || tm.ToolCallID != "call_1" || tm.Content != "pong" {
		t.Fatalf("tool msg = %+v", tm)
	}
	if len(body.Tools) != 1 || body.Tools[0].Type != "function" || body.Tools[0].Function.Name != "echo" {
		t.Fatalf("tools = %+v", body.Tools)
	}
	if len(body.Tools[0].Function.Parameters) == 0 {
		t.Fatal("parameters 不应为空")
	}
}

func TestGenerateFallsBackWithoutStreamOptions(t *testing.T) {
	var bodies []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		bodies = append(bodies, string(b))
		if strings.Contains(string(b), "stream_options") {
			w.WriteHeader(http.StatusBadRequest)
			fmt.Fprint(w, `{"error":{"message":"Unrecognized request argument supplied: stream_options"}}`)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, `data: {"choices":[{"delta":{"content":"ok"}}]}`+"\n"+"data: [DONE]\n")
	}))
	defer srv.Close()

	c, err := New(Config{BaseURL: srv.URL})
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	s, err := c.Generate(context.Background(), port.GenerateRequest{
		Model:    "m",
		Messages: []port.PromptMessage{{Role: "user", Content: []port.PromptPart{{Kind: "text", Text: "hi"}}}},
	})
	if err != nil {
		t.Fatalf("generate should succeed via fallback: %v", err)
	}
	defer s.Close()
	d, err := s.Recv()
	if err != nil || d.Text != "ok" {
		t.Fatalf("first recv = %+v, %v", d, err)
	}
	if len(bodies) != 2 {
		t.Fatalf("requests = %d, want 2（含 stream_options 失败后重试）", len(bodies))
	}
	if strings.Contains(bodies[1], "stream_options") {
		t.Fatal("第二次请求不应包含 stream_options")
	}
}

func TestGenerateHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		fmt.Fprint(w, `{"error":{"message":"bad api key"}}`)
	}))
	defer srv.Close()

	c, _ := New(Config{BaseURL: srv.URL, APIKey: "bad"})
	_, err := c.Generate(context.Background(), port.GenerateRequest{
		Model:    "m",
		Messages: []port.PromptMessage{{Role: "user", Content: []port.PromptPart{{Kind: "text", Text: "x"}}}},
	})
	if err == nil {
		t.Fatal("want error")
	}
	if !strings.Contains(err.Error(), "401") || !strings.Contains(err.Error(), "bad api key") {
		t.Fatalf("err = %v, want 含状态码与服务端信息", err)
	}
}

func TestGenerateMidStreamErrorPayload(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, `data: {"choices":[{"delta":{"content":"par"}}]}`+"\n")
		fmt.Fprint(w, `data: {"error":{"message":"quota exceeded"}}`+"\n")
	}))
	defer srv.Close()

	c, _ := New(Config{BaseURL: srv.URL})
	s, err := c.Generate(context.Background(), port.GenerateRequest{
		Model:    "m",
		Messages: []port.PromptMessage{{Role: "user", Content: []port.PromptPart{{Kind: "text", Text: "x"}}}},
	})
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	defer s.Close()
	d, err := s.Recv()
	if err != nil || d.Text != "par" {
		t.Fatalf("first recv = %+v, %v", d, err)
	}
	_, err = s.Recv()
	if err == nil || err == io.EOF || !strings.Contains(err.Error(), "quota exceeded") {
		t.Fatalf("second recv err = %v, want 服务端流错误", err)
	}
}

func TestGenerateCanceledContext(t *testing.T) {
	c, _ := New(Config{BaseURL: "http://127.0.0.1:1"}) // 端口不可达也无所谓：ctx 已取消
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := c.Generate(ctx, port.GenerateRequest{Model: "m"})
	if err == nil {
		t.Fatal("want error on canceled ctx")
	}
}

func TestGenerateRejectsBadInput(t *testing.T) {
	if _, err := New(Config{BaseURL: ""}); err == nil {
		t.Fatal("empty base_url should fail")
	}
	if _, err := New(Config{BaseURL: "://bad"}); err == nil {
		t.Fatal("bad base_url should fail")
	}
	c, _ := New(Config{BaseURL: "http://localhost:1234/v1"})
	if _, err := c.Generate(context.Background(), port.GenerateRequest{}); err == nil {
		t.Fatal("empty model should fail")
	}
}

func TestStreamCloseIsIdempotentEOF(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, `data: {"choices":[{"delta":{"content":"a"}}]}`+"\n"+"data: [DONE]\n")
	}))
	defer srv.Close()

	c, _ := New(Config{BaseURL: srv.URL})
	s, err := c.Generate(context.Background(), port.GenerateRequest{
		Model:    "m",
		Messages: []port.PromptMessage{{Role: "user", Content: []port.PromptPart{{Kind: "text", Text: "x"}}}},
	})
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	if _, err := s.Recv(); err != io.EOF {
		t.Fatalf("recv after close = %v, want io.EOF", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("second close: %v", err)
	}
}

func TestModels(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/models" {
			t.Errorf("path = %s", r.URL.Path)
		}
		fmt.Fprint(w, `{"data":[{"id":"gpt-4o"},{"id":"claude-x"},{"id":""}]}`)
	}))
	defer srv.Close()

	c, _ := New(Config{BaseURL: srv.URL})
	models, err := c.Models(context.Background())
	if err != nil {
		t.Fatalf("models: %v", err)
	}
	if len(models) != 2 || models[0].Name != "claude-x" || models[1].Name != "gpt-4o" {
		t.Fatalf("models = %+v", models)
	}
}

func TestEncodeOmitsDefaultParams(t *testing.T) {
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: [DONE]\n")
	}))
	defer srv.Close()

	c, _ := New(Config{BaseURL: srv.URL})
	s, err := c.Generate(context.Background(), port.GenerateRequest{
		Model:    "m",
		Messages: []port.PromptMessage{{Role: "user", Content: []port.PromptPart{{Kind: "text", Text: "x"}}}},
	})
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	s.Close()
	if _, hasTemp := body["temperature"]; hasTemp {
		t.Fatal("temperature=0 不应发送")
	}
	if _, hasMax := body["max_tokens"]; hasMax {
		t.Fatal("max_tokens=0 不应发送")
	}
	if _, hasTools := body["tools"]; hasTools {
		t.Fatal("无工具不应发送 tools")
	}
}

// TestCountTokens TokenCounter（三级计数链②，D26）：未配置报错回落估算；
// 配置 fixture 后精确计数；配置了坏路径 fail-fast。
func TestCountTokens(t *testing.T) {
	c, err := New(Config{BaseURL: "http://127.0.0.1:1/v1"})
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	if _, err := c.CountTokens(context.Background(), "hello"); err == nil ||
		!strings.Contains(err.Error(), "未配置") {
		t.Fatalf("未配置 err = %v", err)
	}

	const fixture = "../llm/tokenizer/testdata/fixture_tokenizer.json"
	c2, err := New(Config{BaseURL: "http://127.0.0.1:1/v1", Tokenizer: fixture})
	if err != nil {
		t.Fatalf("new with tokenizer: %v", err)
	}
	if n, err := c2.CountTokens(context.Background(), "hello world"); err != nil || n != 3 {
		t.Fatalf("count = %d, %v, want 3（fixture 官方期望值）", n, err)
	}
	if n, err := c2.CountTokens(context.Background(), ""); err != nil || n != 0 {
		t.Fatalf("empty = %d, %v", n, err)
	}

	if _, err := New(Config{BaseURL: "http://127.0.0.1:1/v1", Tokenizer: "no/such/tokenizer.json"}); err == nil {
		t.Fatal("坏路径应 fail-fast")
	}
}
