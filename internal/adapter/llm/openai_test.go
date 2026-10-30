package llm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Tonyjh07/Aquarius/internal/domain/conversation"
	"github.com/Tonyjh07/Aquarius/internal/domain/tool"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

// fptr 采样参数的指针形（D204：nil = 不发送、非 nil 照发，含 0）。
func fptr(v float64) *float64 { return &v }

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
			{Role: port.PromptSystem, Content: []port.PromptPart{{Kind: port.PromptText, Text: "sys"}}},
			{Role: port.PromptUser, Content: []port.PromptPart{{Kind: port.PromptText, Text: "hi"}}},
			{Role: port.PromptAssistant, ToolCalls: []tool.Call{{ID: "call_1", Name: "echo", Args: json.RawMessage(`{"m":"x"}`)}}},
			{Role: port.PromptTool, CallID: "call_1", Content: []port.PromptPart{{Kind: port.PromptText, Text: "pong"}}},
		},
		Tools:  []tool.Spec{{Name: "echo", Description: "回声", Schema: json.RawMessage(`{"type":"object","properties":{}}`)}},
		Params: port.Sampling{Temperature: fptr(0.7), TopP: fptr(0.9), Stop: []string{"\n"}},
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
		Temperature *float64 `json:"temperature"`
		TopP        *float64 `json:"top_p"`
		MaxTokens   int      `json:"max_tokens"`
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
	if body.Temperature == nil || *body.Temperature != 0.7 || body.TopP == nil || *body.TopP != 0.9 ||
		body.MaxTokens != 128 || len(body.Stop) != 1 {
		t.Fatalf("params = temp %v top_p %v max %v stop %v", body.Temperature, body.TopP, body.MaxTokens, body.Stop)
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
		Messages: []port.PromptMessage{{Role: port.PromptUser, Content: []port.PromptPart{{Kind: port.PromptText, Text: "hi"}}}},
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

// TestGenerateFallbackTransportErrorIsTransient stream_options 兼容重试的第二次
// post 遭遇传输层故障：必须原样上抛（含 ErrTransient 标注），不得被吞成首轮 400
// 非瞬时错误（审查修复——否则装饰器层错过可重试窗口）。
func TestGenerateFallbackTransportErrorIsTransient(t *testing.T) {
	var n int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n++
		if n == 1 {
			w.WriteHeader(http.StatusBadRequest)
			fmt.Fprint(w, `{"error":{"message":"Unrecognized request argument supplied: stream_options"}}`)
			return
		}
		panic(http.ErrAbortHandler) // 第二次请求直接断连（模拟传输层故障）
	}))
	defer srv.Close()

	c, _ := New(Config{BaseURL: srv.URL})
	_, err := c.Generate(context.Background(), port.GenerateRequest{
		Model:    "m",
		Messages: []port.PromptMessage{{Role: port.PromptUser, Content: []port.PromptPart{{Kind: port.PromptText, Text: "x"}}}},
	})
	if err == nil {
		t.Fatal("want error")
	}
	if !errors.Is(err, port.ErrTransient) {
		t.Fatalf("err = %v, want 瞬时标注（旧实现会吞成 400 非瞬时）", err)
	}
	if n < 2 {
		t.Fatalf("请求次数 = %d, want 2（应已进入兼容重试）", n)
	}
}

// TestGenerateSendsReasoningParams D34：reasoning_effort / enable_thinking 只在
// Sampling 设置时发送，未设置一律省略（缺省零字段变化）。
func TestGenerateSendsReasoningParams(t *testing.T) {
	var mu sync.Mutex
	var bodies []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		bodies = append(bodies, string(b))
		mu.Unlock()
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, `data: {"choices":[{"delta":{"content":"ok"}}]}`+"\n"+"data: [DONE]\n")
	}))
	defer srv.Close()

	c, err := New(Config{BaseURL: srv.URL})
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	on := true
	s1, err := c.Generate(context.Background(), port.GenerateRequest{
		Model:    "m",
		Messages: []port.PromptMessage{{Role: port.PromptUser, Content: []port.PromptPart{{Kind: port.PromptText, Text: "hi"}}}},
		Params:   port.Sampling{ReasoningEffort: "high", Thinking: &on},
	})
	if err != nil {
		t.Fatalf("generate1: %v", err)
	}
	_ = s1.Close()
	s2, err := c.Generate(context.Background(), port.GenerateRequest{
		Model:    "m",
		Messages: []port.PromptMessage{{Role: port.PromptUser, Content: []port.PromptPart{{Kind: port.PromptText, Text: "hi"}}}},
	})
	if err != nil {
		t.Fatalf("generate2: %v", err)
	}
	_ = s2.Close()

	mu.Lock()
	defer mu.Unlock()
	if len(bodies) != 2 {
		t.Fatalf("requests = %d, want 2", len(bodies))
	}
	for _, want := range []string{`"reasoning_effort":"high"`, `"enable_thinking":true`} {
		if !strings.Contains(bodies[0], want) {
			t.Fatalf("请求1 缺 %s: %.400s", want, bodies[0])
		}
	}
	for _, bad := range []string{"reasoning_effort", "enable_thinking"} {
		if strings.Contains(bodies[1], bad) {
			t.Fatalf("未设置时不应发送 %s: %.400s", bad, bodies[1])
		}
	}
}

// TestGenerateStripsUnsupportedReasoning D34：服务端点名不认 reasoning_effort →
// 同请求剥离重试（enable_thinking 保留），成功后经 NoteUnsupported 记录；
// 记录后同客户端进程内记忆，后续请求首包即省略且不重复记录。
func TestGenerateStripsUnsupportedReasoning(t *testing.T) {
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
			fmt.Fprint(w, `{"error":{"message":"Unknown parameter: 'reasoning_effort'"}}`)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, `data: {"choices":[{"delta":{"content":"ok"}}]}`+"\n"+"data: [DONE]\n")
	}))
	defer srv.Close()

	var noted []string
	c, err := New(Config{
		BaseURL:         srv.URL,
		NoteUnsupported: func(f string) { noted = append(noted, f) },
	})
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	on := true
	gen := func() error {
		s, err := c.Generate(context.Background(), port.GenerateRequest{
			Model:    "m",
			Messages: []port.PromptMessage{{Role: port.PromptUser, Content: []port.PromptPart{{Kind: port.PromptText, Text: "hi"}}}},
			Params:   port.Sampling{ReasoningEffort: "high", Thinking: &on},
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
	if len(bodies) != 3 {
		t.Fatalf("requests = %d, want 3（剥离重试 1 + 记忆后 1...）", len(bodies))
	}
	if !strings.Contains(bodies[0], "reasoning_effort") {
		t.Fatalf("首包应含 reasoning_effort: %.400s", bodies[0])
	}
	if strings.Contains(bodies[1], "reasoning_effort") {
		t.Fatalf("剥离后不应含 reasoning_effort: %.400s", bodies[1])
	}
	if !strings.Contains(bodies[1], "enable_thinking") {
		t.Fatalf("剥离只针对被点名的字段: %.400s", bodies[1])
	}
	if strings.Contains(bodies[2], "reasoning_effort") {
		t.Fatalf("记录后首包即应省略: %.400s", bodies[2])
	}
	mu.Unlock()
	if len(noted) != 1 || noted[0] != "reasoning_effort" {
		t.Fatalf("noted = %v, want [reasoning_effort]（只记一次）", noted)
	}
}

// TestGenerateEchoesReasoningContent D42：assistant 的 PromptMessage.Reasoning 序列化为
// 消息级 reasoning_content 字段（思维链回传）；无思考（装配层已过滤）时该字段不出现。
func TestGenerateEchoesReasoningContent(t *testing.T) {
	var mu sync.Mutex
	var bodies []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		bodies = append(bodies, string(b))
		mu.Unlock()
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, `data: {"choices":[{"delta":{"content":"ok"}}]}`+"\n"+"data: [DONE]\n")
	}))
	defer srv.Close()

	c, err := New(Config{BaseURL: srv.URL})
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	echo := func(reasoning string) error {
		s, err := c.Generate(context.Background(), port.GenerateRequest{
			Model: "m",
			Messages: []port.PromptMessage{
				{Role: port.PromptUser, Content: []port.PromptPart{{Kind: port.PromptText, Text: "hi"}}},
				{Role: port.PromptAssistant, Reasoning: reasoning, Content: []port.PromptPart{{Kind: port.PromptText, Text: "答案"}}},
			},
		})
		if err != nil {
			return err
		}
		return s.Close()
	}
	if err := echo("先想一想"); err != nil {
		t.Fatalf("generate(echo): %v", err)
	}
	if err := echo(""); err != nil {
		t.Fatalf("generate(no-echo): %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(bodies) != 2 {
		t.Fatalf("requests = %d, want 2", len(bodies))
	}
	if !strings.Contains(bodies[0], `"reasoning_content":"先想一想"`) {
		t.Fatalf("回传包缺 reasoning_content: %.500s", bodies[0])
	}
	if strings.Contains(bodies[1], "reasoning_content") {
		t.Fatalf("无思考时不应发 reasoning_content: %.500s", bodies[1])
	}
}

// TestGenerateReasoningWithToolCalls D42 核心形态：推理模型"只调工具不说话"的
// assistant（Reasoning + ToolCalls + 空正文）与纯思考节点都不得被丢——
// reasoning_content 以消息级字段回传、content 字段省略（DeepSeek 带 tools 强制回传的形态）。
func TestGenerateReasoningWithToolCalls(t *testing.T) {
	var body string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		body = string(b)
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, `data: {"choices":[{"delta":{"content":"ok"}}]}`+"\n"+"data: [DONE]\n")
	}))
	defer srv.Close()

	c, err := New(Config{BaseURL: srv.URL})
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	s, err := c.Generate(context.Background(), port.GenerateRequest{
		Model: "m",
		Messages: []port.PromptMessage{
			{Role: port.PromptUser, Content: []port.PromptPart{{Kind: port.PromptText, Text: "hi"}}},
			{Role: port.PromptAssistant, Reasoning: "先想", ToolCalls: []tool.Call{{ID: "c1", Name: "echo", Args: []byte(`{}`)}}},
			{Role: port.PromptAssistant, Reasoning: "独白"},
		},
	})
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	_ = s.Close()

	var payload struct {
		Messages []map[string]any `json:"messages"`
	}
	if err := json.Unmarshal([]byte(body), &payload); err != nil {
		t.Fatalf("parse body: %v (%.400s)", err, body)
	}
	if len(payload.Messages) != 3 {
		t.Fatalf("messages = %d, want 3（只调工具/纯思考的 assistant 不得被丢）: %.400s", len(payload.Messages), body)
	}
	withCalls := payload.Messages[1]
	if withCalls["reasoning_content"] != "先想" {
		t.Fatalf("messages[1].reasoning_content = %v, want 先想", withCalls["reasoning_content"])
	}
	if _, ok := withCalls["tool_calls"]; !ok {
		t.Fatalf("messages[1] 缺 tool_calls: %.400s", body)
	}
	if _, ok := withCalls["content"]; ok {
		t.Fatalf("空正文不应发 content 字段: %.400s", body)
	}
	only := payload.Messages[2]
	if only["reasoning_content"] != "独白" {
		t.Fatalf("messages[2].reasoning_content = %v, want 独白", only["reasoning_content"])
	}
	if _, ok := only["content"]; ok {
		t.Fatalf("纯思考节点不应发 content 字段: %.400s", body)
	}
}

// TestGenerateStripsUnsupportedReasoningContent D42：端点点名不认 reasoning_content
// （未知字段措辞）→ 同请求剥离重试（正文/其余字段保留），成功后 NoteUnsupported
// 记录一次，后续请求首包即省略。
func TestGenerateStripsUnsupportedReasoningContent(t *testing.T) {
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
			fmt.Fprint(w, `{"error":{"message":"Unknown argument: 'messages[1].reasoning_content'"}}`)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, `data: {"choices":[{"delta":{"content":"ok"}}]}`+"\n"+"data: [DONE]\n")
	}))
	defer srv.Close()

	var noted []string
	c, err := New(Config{
		BaseURL:         srv.URL,
		NoteUnsupported: func(f string) { noted = append(noted, f) },
	})
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	gen := func() error {
		s, err := c.Generate(context.Background(), port.GenerateRequest{
			Model: "m",
			Messages: []port.PromptMessage{
				{Role: port.PromptUser, Content: []port.PromptPart{{Kind: port.PromptText, Text: "hi"}}},
				{Role: port.PromptAssistant, Reasoning: "先想", Content: []port.PromptPart{{Kind: port.PromptText, Text: "答案"}}},
			},
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
	if len(bodies) != 3 {
		t.Fatalf("requests = %d, want 3（剥离重试 1 + 记忆后 1）", len(bodies))
	}
	if !strings.Contains(bodies[0], "reasoning_content") {
		t.Fatalf("首包应含 reasoning_content: %.500s", bodies[0])
	}
	if strings.Contains(bodies[1], "reasoning_content") {
		t.Fatalf("剥离后不应含 reasoning_content: %.500s", bodies[1])
	}
	if !strings.Contains(bodies[1], "答案") {
		t.Fatalf("剥离只针对被点名字段，正文须保留: %.500s", bodies[1])
	}
	if strings.Contains(bodies[2], "reasoning_content") {
		t.Fatalf("记录后首包即应省略: %.500s", bodies[2])
	}
	mu.Unlock()
	if len(noted) != 1 || noted[0] != "reasoning_content" {
		t.Fatalf("noted = %v, want [reasoning_content]（只记一次）", noted)
	}
}

// TestGenerateEnumErrorNotStripped D34：枚举值错误不是"未知参数"——不剥离、不记录、
// 照常报 400，不掩盖配置问题。
func TestGenerateEnumErrorNotStripped(t *testing.T) {
	var n int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n++
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprint(w, `{"error":{"message":"Invalid value 'minimal' for parameter 'reasoning_effort'"}}`)
	}))
	defer srv.Close()

	var noted []string
	c, err := New(Config{
		BaseURL:         srv.URL,
		NoteUnsupported: func(f string) { noted = append(noted, f) },
	})
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	_, err = c.Generate(context.Background(), port.GenerateRequest{
		Model:    "m",
		Messages: []port.PromptMessage{{Role: port.PromptUser, Content: []port.PromptPart{{Kind: port.PromptText, Text: "hi"}}}},
		Params:   port.Sampling{ReasoningEffort: "minimal"},
	})
	if err == nil || !strings.Contains(err.Error(), "状态码 400") {
		t.Fatalf("err = %v, want 400 上抛", err)
	}
	if n != 1 {
		t.Fatalf("requests = %d, want 1（枚举错误不重试）", n)
	}
	if len(noted) != 0 {
		t.Fatalf("noted = %v, want 空（枚举错误不记录）", noted)
	}
}

// TestGenerateReasoningContentValueErrorNotStripped D42：reasoning_content 的取值/格式
// 错误不是"未知字段"措辞——不剥离、不记录，照常 400 上抛（剥离管线不掩盖真实故障）。
func TestGenerateReasoningContentValueErrorNotStripped(t *testing.T) {
	var n int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n++
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprint(w, `{"error":{"message":"Invalid value for parameter 'reasoning_content'"}}`)
	}))
	defer srv.Close()

	var noted []string
	c, err := New(Config{
		BaseURL:         srv.URL,
		NoteUnsupported: func(f string) { noted = append(noted, f) },
	})
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	_, err = c.Generate(context.Background(), port.GenerateRequest{
		Model: "m",
		Messages: []port.PromptMessage{
			{Role: port.PromptUser, Content: []port.PromptPart{{Kind: port.PromptText, Text: "hi"}}},
			{Role: port.PromptAssistant, Reasoning: "先想", Content: []port.PromptPart{{Kind: port.PromptText, Text: "答案"}}},
		},
	})
	if err == nil || !strings.Contains(err.Error(), "状态码 400") {
		t.Fatalf("err = %v, want 400 上抛", err)
	}
	if n != 1 {
		t.Fatalf("requests = %d, want 1（取值错误不重试）", n)
	}
	if len(noted) != 0 {
		t.Fatalf("noted = %v, want 空（取值错误不记录）", noted)
	}
}

// TestStreamParsesReasoningDeltas D34：reasoning_content / reasoning 两种键名映射为
// 思维链增量（Reasoning=true）；正文优先，同片时推理丢弃。
func TestStreamParsesReasoningDeltas(t *testing.T) {
	script := "" +
		`data: {"choices":[{"delta":{"reasoning_content":"先想"}}]}` + "\n\n" +
		`data: {"choices":[{"delta":{"reasoning":"一步"}}]}` + "\n\n" +
		`data: {"choices":[{"delta":{"content":"答案"}}]}` + "\n\n" +
		`data: {"choices":[{"delta":{"content":"正文","reasoning_content":"同片丢弃"}}]}` + "\n\n" +
		"data: [DONE]\n"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, script)
	}))
	defer srv.Close()

	c, err := New(Config{BaseURL: srv.URL})
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	s, err := c.Generate(context.Background(), port.GenerateRequest{
		Model:    "m",
		Messages: []port.PromptMessage{{Role: port.PromptUser, Content: []port.PromptPart{{Kind: port.PromptText, Text: "hi"}}}},
	})
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	defer s.Close()

	var got []port.Delta
	for {
		d, err := s.Recv()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatalf("recv: %v", err)
		}
		got = append(got, d)
	}
	want := []struct {
		text      string
		reasoning bool
	}{
		{"先想", true},
		{"一步", true},
		{"答案", false},
		{"正文", false}, // content 优先：同片推理丢弃
	}
	if len(got) != len(want) {
		t.Fatalf("deltas = %d (%+v), want %d", len(got), got, len(want))
	}
	for i, w := range want {
		if got[i].Text != w.text || got[i].Reasoning != w.reasoning {
			t.Fatalf("delta[%d] = %+v, want %q reasoning=%v", i, got[i], w.text, w.reasoning)
		}
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
		Messages: []port.PromptMessage{{Role: port.PromptUser, Content: []port.PromptPart{{Kind: port.PromptText, Text: "x"}}}},
	})
	if err == nil {
		t.Fatal("want error")
	}
	if !strings.Contains(err.Error(), "401") || !strings.Contains(err.Error(), "bad api key") {
		t.Fatalf("err = %v, want 含状态码与服务端信息", err)
	}
}

// TestGenerateTransientStatusTagging §10：429/408/5xx 标注 port.ErrTransient
// （供装配根重试装饰器判据），4xx 业务错误不标注。
func TestGenerateTransientStatusTagging(t *testing.T) {
	cases := []struct {
		code      int
		transient bool
	}{
		{http.StatusTooManyRequests, true},
		{http.StatusRequestTimeout, true},
		{http.StatusInternalServerError, true},
		{http.StatusBadGateway, true},
		{http.StatusUnauthorized, false},
		{http.StatusBadRequest, false},
	}
	for _, tc := range cases {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(tc.code)
			fmt.Fprint(w, `{"error":{"message":"nope"}}`)
		}))
		c, _ := New(Config{BaseURL: srv.URL})
		_, err := c.Generate(context.Background(), port.GenerateRequest{
			Model:    "m",
			Messages: []port.PromptMessage{{Role: port.PromptUser, Content: []port.PromptPart{{Kind: port.PromptText, Text: "x"}}}},
		})
		srv.Close()
		if err == nil {
			t.Fatalf("%d: want error", tc.code)
		}
		if got := errors.Is(err, port.ErrTransient); got != tc.transient {
			t.Fatalf("%d: transient = %v, want %v（err=%v）", tc.code, got, tc.transient, err)
		}
	}
}

// TestGenerateTransportErrorTagging 连接层失败标瞬时；ctx 已取消的不算（§10 不重试）。
func TestGenerateTransportErrorTagging(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}))
	url := srv.URL
	srv.Close() // 此后连接必然失败

	c, _ := New(Config{BaseURL: url})
	req := port.GenerateRequest{
		Model:    "m",
		Messages: []port.PromptMessage{{Role: port.PromptUser, Content: []port.PromptPart{{Kind: port.PromptText, Text: "x"}}}},
	}
	if _, err := c.Generate(context.Background(), req); err == nil || !errors.Is(err, port.ErrTransient) {
		t.Fatalf("err = %v, want 瞬时标注", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := c.Generate(ctx, req)
	if errors.Is(err, port.ErrTransient) {
		t.Fatalf("ctx 取消的错误不应标瞬时: %v", err)
	}
}

// TestGenerateQuotaTagging 429（D173）除瞬时外还标 port.ErrQuota，并带上响应头里的
// 冷却时长；其他瞬时状态（5xx/408）不标配额——那种错误换钥匙没用。
func TestGenerateQuotaTagging(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Retry-After", "42")
		w.WriteHeader(http.StatusTooManyRequests)
		fmt.Fprint(w, `{"error":{"message":"rate limited"}}`)
	}))
	defer srv.Close()

	c, _ := New(Config{BaseURL: srv.URL})
	_, err := c.Generate(context.Background(), port.GenerateRequest{
		Model:    "m",
		Messages: []port.PromptMessage{{Role: port.PromptUser, Content: []port.PromptPart{{Kind: port.PromptText, Text: "x"}}}},
	})
	if err == nil || !errors.Is(err, port.ErrQuota) {
		t.Fatalf("err = %v, want 标 ErrQuota", err)
	}
	if !errors.Is(err, port.ErrTransient) {
		t.Fatalf("配额错误须同时标瞬时（重试层照旧退避）: %v", err)
	}
	if d := port.QuotaAfter(err); d != 42*time.Second {
		t.Fatalf("冷却 = %v, want 42s（读 Retry-After）", d)
	}
}

// TestGenerateNonQuotaTransientNotTagged 5xx/408 不标配额（不该触发换钥匙）。
func TestGenerateNonQuotaTransientNotTagged(t *testing.T) {
	for _, code := range []int{http.StatusInternalServerError, http.StatusRequestTimeout} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(code)
			fmt.Fprint(w, `{"error":{"message":"nope"}}`)
		}))
		c, _ := New(Config{BaseURL: srv.URL})
		_, err := c.Generate(context.Background(), port.GenerateRequest{
			Model:    "m",
			Messages: []port.PromptMessage{{Role: port.PromptUser, Content: []port.PromptPart{{Kind: port.PromptText, Text: "x"}}}},
		})
		srv.Close()
		if err == nil || !errors.Is(err, port.ErrTransient) || errors.Is(err, port.ErrQuota) {
			t.Fatalf("%d: err = %v, want 瞬时但非配额", code, err)
		}
		if d := port.QuotaAfter(err); d != 0 {
			t.Fatalf("%d: QuotaAfter = %v, want 0", code, d)
		}
	}
}

// TestGenerateQuotaRetryAfterVariants Retry-After 的三种写法：秒数、HTTP 日期、
// 缺失（= 0，由密钥池取缺省冷却）。
func TestGenerateQuotaRetryAfterVariants(t *testing.T) {
	cases := []struct {
		header string
		want   time.Duration
	}{
		{"120", 120 * time.Second},
		{"", 0},
		{"0", 0},
		{"-5", 0},
		{"not-a-number", 0},
		{time.Now().Add(90 * time.Second).UTC().Format(http.TimeFormat), 0}, // 日期型在毫秒级断言里不便精确比，交由范围检查
	}
	for _, tc := range cases {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			if tc.header != "" {
				w.Header().Set("Retry-After", tc.header)
			}
			w.WriteHeader(http.StatusTooManyRequests)
			fmt.Fprint(w, `{"error":{"message":"rate limited"}}`)
		}))
		c, _ := New(Config{BaseURL: srv.URL})
		_, err := c.Generate(context.Background(), port.GenerateRequest{
			Model:    "m",
			Messages: []port.PromptMessage{{Role: port.PromptUser, Content: []port.PromptPart{{Kind: port.PromptText, Text: "x"}}}},
		})
		got := port.QuotaAfter(err)
		srv.Close()
		if tc.header == "" || tc.header == "0" || tc.header == "-5" || tc.header == "not-a-number" {
			if got != tc.want {
				t.Fatalf("Retry-After %q: 冷却 = %v, want %v", tc.header, got, tc.want)
			}
			continue
		}
		if tc.want > 0 && got != tc.want {
			t.Fatalf("Retry-After %q: 冷却 = %v, want %v", tc.header, got, tc.want)
		}
		if tc.want == 0 && (got < 80*time.Second || got > 91*time.Second) {
			t.Fatalf("Retry-After %q: 日期型应解出约 90s，实为 %v", tc.header, got)
		}
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
		Messages: []port.PromptMessage{{Role: port.PromptUser, Content: []port.PromptPart{{Kind: port.PromptText, Text: "x"}}}},
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
		Messages: []port.PromptMessage{{Role: port.PromptUser, Content: []port.PromptPart{{Kind: port.PromptText, Text: "x"}}}},
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
		Messages: []port.PromptMessage{{Role: port.PromptUser, Content: []port.PromptPart{{Kind: port.PromptText, Text: "x"}}}},
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

// TestCustomHeadersOnlyOnChat 自定义请求头（D253）：openai-chat 上按声明发送，同名时以
// 声明为准；anthropic 协议不带（那是自家规范）。
func TestCustomHeadersOnlyOnChat(t *testing.T) {
	var gotGateway, gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotGateway, gotAuth = r.Header.Get("X-Gateway"), r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, sseScript)
	}))
	defer srv.Close()

	c, err := New(Config{BaseURL: srv.URL + "/v1", APIKey: "sk-test",
		Headers: map[string]string{"X-Gateway": "route-a", "Authorization": "Token custom"}})
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	if _, err := c.Generate(context.Background(), port.GenerateRequest{Model: "m"}); err != nil {
		t.Fatalf("generate: %v", err)
	}
	if gotGateway != "route-a" {
		t.Fatalf("自定义头未发送: %q", gotGateway)
	}
	if gotAuth != "Token custom" {
		t.Fatalf("同名头应以声明为准: %q", gotAuth)
	}

	gotGateway, gotAuth = "", ""
	ac, err := New(Config{BaseURL: srv.URL, APIKey: "k", Protocol: ProtoAnthropic,
		Headers: map[string]string{"X-Gateway": "route-a"}})
	if err != nil {
		t.Fatalf("new anthropic: %v", err)
	}
	if _, err := ac.Generate(context.Background(), port.GenerateRequest{Model: "m",
		Messages: []port.PromptMessage{{Role: port.PromptUser, Content: []port.PromptPart{{Kind: port.PromptText, Text: "hi"}}}}}); err != nil {
		t.Fatalf("generate anthropic: %v", err)
	}
	if gotGateway != "" {
		t.Fatalf("anthropic 不该带自定义头: %q", gotGateway)
	}
}

// TestDefaultClientHandshakeBounds D137：默认客户端（调用方没注入 HTTPClient 时）必须给
// 握手设界——原来 `&http.Client{}` 两个旋钮都不设，服务端收下连接却永不回首字节节即永久
// 挂起（只有 Ctrl+C 能解；外部审查 2026-10-13 报障）。判据看 Transport 结构：dial 有界 +
// ResponseHeaderTimeout 非零，而**整体 Timeout 必须为 0**（流式 SSE 头之后可能合法跑几十
// 分钟，一刀切会断长流）。
func TestDefaultClientHandshakeBounds(t *testing.T) {
	c, err := New(Config{BaseURL: "http://127.0.0.1:1"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if c.hc == nil {
		t.Fatal("默认客户端未构造")
	}
	if c.hc.Timeout != 0 {
		t.Fatalf("整体 Timeout = %v, want 0（流式不能设整体超时）", c.hc.Timeout)
	}
	tr, ok := c.hc.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("Transport = %T, want *http.Transport（握手超时挂在 Transport 上）", c.hc.Transport)
	}
	if tr.DialContext == nil || tr.ResponseHeaderTimeout <= 0 {
		t.Fatalf("握手无界：DialContext 已设=%v ResponseHeaderTimeout=%v",
			tr.DialContext != nil, tr.ResponseHeaderTimeout)
	}
	// 代理口径不得因自定义 Transport 丢失（否则仅因换了 Transport 就丢掉环境变量代理）。
	if tr.Proxy == nil {
		t.Fatal("Proxy 未设（应保持 http.ProxyFromEnvironment 口径）")
	}

	// 注入的客户端原样使用，不得被改写（D110 的 8s 探测客户端即此路径）。
	own := &http.Client{Timeout: 8 * time.Second}
	c2, err := New(Config{BaseURL: "http://127.0.0.1:1", HTTPClient: own})
	if err != nil {
		t.Fatalf("New(注入侧): %v", err)
	}
	if c2.hc != own {
		t.Fatal("注入的 HTTPClient 被改写（调用方应对自己的客户端有完全控制）")
	}
}

// TestDefaultClientHeaderTimeoutVerdicts 对面挂了（收下连接、永不回首字节节）必须由握手
// 超时切断；ctx 取消仍应即时返回（原报障「仅 Ctrl+C 可解」的另一半）。
func TestDefaultClientHeaderTimeoutVerdicts(t *testing.T) {
	// 挂死服务端：收下连接但永不写响应。用 release 闸门而非长 sleep——srv.Close() 会等
	// 在途 handler 返回，sleep 30s 会把测试拖成 30s。
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		<-release
	}))
	defer func() {
		close(release)
		srv.Close()
	}()

	// 用手动缩短的 Transport 触发同一条路径（10 分钟的真值不必等）。
	tr, ok := defaultClient().Transport.(*http.Transport)
	if !ok {
		t.Fatalf("Transport = %T", tr)
	}
	tr.ResponseHeaderTimeout = 60 * time.Millisecond
	c, err := New(Config{BaseURL: srv.URL, HTTPClient: &http.Client{Transport: tr}})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	start := time.Now()
	if _, err := c.Models(context.Background()); err == nil {
		t.Fatal("挂死服务端应报错（握手超时）")
	}
	if el := time.Since(start); el > 5*time.Second {
		t.Fatalf("等待 %v 才返回——握手没被 ResponseHeaderTimeout 切断", el)
	}

	// ctx 取消优先于任何等待。
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Millisecond)
	defer cancel()
	if _, err := c.Models(ctx); err == nil {
		t.Fatal("ctx 取消后应返回错误")
	}
}
