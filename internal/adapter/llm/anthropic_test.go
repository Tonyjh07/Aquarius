package llm

// anthropic 适配器测试（S11-6）：请求体形状（system 顶层、tool_use/tool_result 内容块）、
// SSE 事件族回放、思考签名、鉴权头，以及与既有协议共用的错误口径。
// 全部用 httptest 假 server 回放录制的分片，不起真实网络。

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

	"github.com/Tonyjh07/Aquarius/internal/domain/tool"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

// anthropicSSEScript 录制的 Messages 事件流：思考（含签名）→ 正文 → 工具调用 →
// message_delta（输出用量）→ message_stop。
const anthropicSSEScript = "" +
	"event: message_start\n" +
	`data: {"type":"message_start","message":{"id":"msg_1","type":"message","role":"assistant","content":[],"usage":{"input_tokens":11,"output_tokens":1}}}` + "\n" +
	"\n" +
	"event: content_block_start\n" +
	`data: {"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":""}}` + "\n" +
	`data: {"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"pon"}}` + "\n" +
	`data: {"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"der"}}` + "\n" +
	`data: {"type":"content_block_delta","index":0,"delta":{"type":"signature_delta","signature":"SIG-ABC"}}` + "\n" +
	`data: {"type":"content_block_stop","index":0}` + "\n" +
	"\n" +
	`data: {"type":"content_block_start","index":1,"content_block":{"type":"text","text":""}}` + "\n" +
	`data: {"type":"content_block_delta","index":1,"delta":{"type":"text_delta","text":"Hel"}}` + "\n" +
	`data: {"type":"content_block_delta","index":1,"delta":{"type":"text_delta","text":"lo"}}` + "\n" +
	`data: {"type":"content_block_stop","index":1}` + "\n" +
	"\n" +
	`data: {"type":"content_block_start","index":2,"content_block":{"type":"tool_use","id":"toolu_9","name":"echo","input":{}}}` + "\n" +
	`data: {"type":"content_block_delta","index":2,"delta":{"type":"input_json_delta","partial_json":"{\"m\":"}}` + "\n" +
	`data: {"type":"content_block_delta","index":2,"delta":{"type":"input_json_delta","partial_json":"\"x\"}"}}` + "\n" +
	`data: {"type":"content_block_stop","index":2}` + "\n" +
	"\n" +
	`data: {"type":"message_delta","delta":{"stop_reason":"tool_use"},"usage":{"output_tokens":42}}` + "\n" +
	"event: message_stop\n" +
	`data: {"type":"message_stop"}` + "\n"

// newAnthropicServer 起一个假 Messages 端点：记录 path/头部/请求体，回放给定脚本。
func newAnthropicServer(t *testing.T, script string) (*httptest.Server, *anthropicRecorder) {
	t.Helper()
	rec := &anthropicRecorder{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		rec.add(r.URL.Path, r.Header.Get("x-api-key"), r.Header.Get("anthropic-version"),
			r.Header.Get("Authorization"), string(b))
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, script)
	}))
	t.Cleanup(srv.Close)
	return srv, rec
}

type anthropicRecorder struct {
	mu       sync.Mutex
	paths    []string
	apiKeys  []string
	versions []string
	auths    []string
	bodies   []string
}

func (r *anthropicRecorder) add(path, apiKey, version, auth, body string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.paths = append(r.paths, path)
	r.apiKeys = append(r.apiKeys, apiKey)
	r.versions = append(r.versions, version)
	r.auths = append(r.auths, auth)
	r.bodies = append(r.bodies, body)
}

func (r *anthropicRecorder) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.bodies)
}

func (r *anthropicRecorder) body(i int) string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.bodies[i]
}

// anthropicClient 造一个 anthropic 协议的客户端。
func anthropicClient(t *testing.T, base, key string) *Client {
	t.Helper()
	c, err := New(Config{BaseURL: base, APIKey: key, Protocol: ProtoAnthropic})
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	return c
}

// TestAnthropicGenerateStreams D175/S11-6：事件族回放——思考分片、单独到达的签名、
// 正文、工具调用的 ID/名字（content_block_start）与参数增量、合并后的 usage。
func TestAnthropicGenerateStreams(t *testing.T) {
	srv, rec := newAnthropicServer(t, anthropicSSEScript)
	c := anthropicClient(t, srv.URL+"/v1", "ak-test")

	s, err := c.Generate(context.Background(), port.GenerateRequest{
		Model:    "claude-sonnet-4-6",
		Messages: []port.PromptMessage{{Role: "user", Content: []port.PromptPart{{Kind: "text", Text: "hi"}}}},
	})
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	defer s.Close()

	text, reasoning, sig, calls, usage := collectResponses(t, s)
	if text != "Hello" {
		t.Fatalf("text = %q, want Hello", text)
	}
	if reasoning != "ponder" {
		t.Fatalf("reasoning = %q, want ponder", reasoning)
	}
	if sig != anthropicSigPrefix+"SIG-ABC" {
		t.Fatalf("sig = %q, want %q", sig, anthropicSigPrefix+"SIG-ABC")
	}
	if len(calls) != 3 {
		t.Fatalf("tool deltas = %d, want 3（ID/名字 + 两段参数）: %+v", len(calls), calls)
	}
	if calls[0].Index != 2 || calls[0].ID != "toolu_9" || calls[0].Name != "echo" {
		t.Fatalf("call delta 0 = %+v", calls[0])
	}
	if args := calls[1].ArgsDelta + calls[2].ArgsDelta; args != `{"m":"x"}` {
		t.Fatalf("拼出的参数 = %q", args)
	}
	if usage == nil || usage.InputTokens != 11 || usage.OutputTokens != 42 {
		t.Fatalf("usage = %+v, want {11,42}", usage)
	}
	if rec.paths[0] != "/v1"+anthropicPath {
		t.Fatalf("path = %q", rec.paths[0])
	}
	// 鉴权：anthropic 走 x-api-key + anthropic-version，不发 Bearer。
	if rec.apiKeys[0] != "ak-test" {
		t.Fatalf("x-api-key = %q", rec.apiKeys[0])
	}
	if rec.versions[0] != anthropicVersion {
		t.Fatalf("anthropic-version = %q", rec.versions[0])
	}
	if rec.auths[0] != "" {
		t.Fatalf("不该发 Authorization: %q", rec.auths[0])
	}
}

// TestAnthropicRequestShape S11-6：请求形状——system 走顶层、图片走 image/base64 块、
// assistant 的思考→thinking 块（含签名）、工具调用→tool_use（input 是对象）、
// tool 应答→user 里的 tool_result 块、工具声明用 input_schema。
func TestAnthropicRequestShape(t *testing.T) {
	srv, rec := newAnthropicServer(t, `data: {"type":"message_stop"}`+"\n")
	c := anthropicClient(t, srv.URL+"/v1", "ak")

	on := true
	s, err := c.Generate(context.Background(), port.GenerateRequest{
		Model: "claude-sonnet-4-6",
		Messages: []port.PromptMessage{
			{Role: "system", Content: []port.PromptPart{{Kind: "text", Text: "sys"}}},
			{Role: "user", Content: []port.PromptPart{
				{Kind: "text", Text: "看图"},
				{Kind: "image", MIME: "image/jpeg", Data: []byte{0x01, 0x02}},
			}},
			{Role: "assistant", Reasoning: "想过", ReasoningSignature: anthropicSigPrefix + "SIG9",
				Content:   []port.PromptPart{{Kind: "text", Text: "答"}},
				ToolCalls: []tool.Call{{ID: "toolu_1", Name: "echo", Args: json.RawMessage(`{"m":"x"}`)}}},
			{Role: "tool", CallID: "toolu_1", Content: []port.PromptPart{{Kind: "text", Text: "pong"}}},
		},
		Tools:  []tool.Spec{{Name: "echo", Description: "回声", Schema: json.RawMessage(`{"type":"object","properties":{}}`)}},
		Params: port.Sampling{Thinking: &on, ReasoningEffort: "low", Temperature: 0.5, Stop: []string{"\n"}},
	})
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	_ = s.Close()

	var body struct {
		Model       string   `json:"model"`
		MaxTokens   int      `json:"max_tokens"`
		Stream      bool     `json:"stream"`
		Temperature *float64 `json:"temperature"`
		StopSeqs    []string `json:"stop_sequences"`
		System      []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"system"`
		Thinking *struct {
			Type         string `json:"type"`
			BudgetTokens int    `json:"budget_tokens"`
		} `json:"thinking"`
		Tools []struct {
			Name        string          `json:"name"`
			InputSchema json.RawMessage `json:"input_schema"`
		} `json:"tools"`
		Messages []struct {
			Role    string `json:"role"`
			Content []struct {
				Type     string `json:"type"`
				Text     string `json:"text"`
				Thinking string `json:"thinking"`
				Source   *struct {
					MediaType string `json:"media_type"`
					Data      string `json:"data"`
				} `json:"source"`
				Signature string          `json:"signature"`
				ID        string          `json:"id"`
				Name      string          `json:"name"`
				Input     json.RawMessage `json:"input"`
				ToolUseID string          `json:"tool_use_id"`
				Content   string          `json:"content"`
			} `json:"content"`
		} `json:"messages"`
	}
	raw := rec.body(0)
	if err := json.Unmarshal([]byte(raw), &body); err != nil {
		t.Fatalf("解析请求体: %v: %.600s", err, raw)
	}
	if body.Model != "claude-sonnet-4-6" || !body.Stream {
		t.Fatalf("头部字段 = %+v", body)
	}
	// system 顶层（persona 类消息不进 messages）。
	if len(body.System) != 1 || body.System[0].Type != "text" || body.System[0].Text != "sys" {
		t.Fatalf("system = %+v", body.System)
	}
	// 思考：enabled + 档位映射；开启思考时 temperature 必须是 1（省略 = 服务端默认）。
	if body.Thinking == nil || body.Thinking.Type != "enabled" || body.Thinking.BudgetTokens != 2048 {
		t.Fatalf("thinking = %+v", body.Thinking)
	}
	if body.Temperature != nil {
		t.Fatalf("开启思考时不该发 temperature（只能是 1）: %+v", *body.Temperature)
	}
	// 输出预算要装得下思考预算（budget < max_tokens 是硬约束）。
	if body.MaxTokens <= body.Thinking.BudgetTokens {
		t.Fatalf("max_tokens = %d，必须大于 thinking.budget_tokens = %d", body.MaxTokens, body.Thinking.BudgetTokens)
	}
	// 工具声明用 input_schema（不是 parameters）。
	if len(body.Tools) != 1 || body.Tools[0].Name != "echo" || len(body.Tools[0].InputSchema) == 0 {
		t.Fatalf("tools = %+v", body.Tools)
	}
	if strings.Contains(raw, `"parameters"`) {
		t.Fatalf("工具声明不该出现 parameters: %.600s", raw)
	}
	if len(body.StopSeqs) != 1 || body.StopSeqs[0] != "\n" {
		t.Fatalf("stop_sequences = %v", body.StopSeqs)
	}

	// 消息序列：user(text+image) → assistant(thinking+text+tool_use) → user(tool_result)
	if len(body.Messages) != 3 {
		t.Fatalf("messages = %d, want 3: %.700s", len(body.Messages), raw)
	}
	if body.Messages[0].Role != "user" || len(body.Messages[0].Content) != 2 {
		t.Fatalf("msg0 = %+v", body.Messages[0])
	}
	if body.Messages[0].Content[1].Type != "image" || body.Messages[0].Content[1].Source == nil ||
		body.Messages[0].Content[1].Source.MediaType != "image/jpeg" ||
		body.Messages[0].Content[1].Source.Data != "AQI=" {
		t.Fatalf("图片块 = %+v", body.Messages[0].Content[1])
	}
	asst := body.Messages[1]
	if asst.Role != "assistant" || len(asst.Content) != 3 {
		t.Fatalf("assistant = %+v", asst)
	}
	if asst.Content[0].Type != "thinking" || asst.Content[0].Thinking != "想过" || asst.Content[0].Signature != "SIG9" {
		t.Fatalf("thinking 块 = %+v", asst.Content[0])
	}
	if asst.Content[1].Type != "text" {
		t.Fatalf("正文块 = %+v", asst.Content[1])
	}
	if asst.Content[2].Type != "tool_use" || asst.Content[2].ID != "toolu_1" || asst.Content[2].Name != "echo" {
		t.Fatalf("tool_use 块 = %+v", asst.Content[2])
	}
	// input 是 JSON 对象（不是 chat 那样的字符串）。
	var inp map[string]any
	if err := json.Unmarshal(asst.Content[2].Input, &inp); err != nil || inp["m"] != "x" {
		t.Fatalf("tool_use.input = %s (%v)", asst.Content[2].Input, err)
	}
	if body.Messages[2].Role != "user" || body.Messages[2].Content[0].Type != "tool_result" ||
		body.Messages[2].Content[0].ToolUseID != "toolu_1" || body.Messages[2].Content[0].Content != "pong" {
		t.Fatalf("tool_result 块 = %+v", body.Messages[2])
	}
}

// TestAnthropicThinkingDisabled 显式关思考 → {"type":"disabled"}；键缺失 → 不发该字段。
func TestAnthropicThinkingDisabled(t *testing.T) {
	srv, rec := newAnthropicServer(t, `data: {"type":"message_stop"}`+"\n")
	c := anthropicClient(t, srv.URL, "")
	off := false

	// 键缺失：零字段变化。
	s1, err := c.Generate(context.Background(), port.GenerateRequest{
		Model:    "m",
		Messages: []port.PromptMessage{{Role: "user", Content: []port.PromptPart{{Kind: "text", Text: "hi"}}}},
		Params:   port.Sampling{Temperature: 0.3},
	})
	if err != nil {
		t.Fatalf("generate1: %v", err)
	}
	_ = s1.Close()
	if strings.Contains(rec.body(0), `"thinking"`) {
		t.Fatalf("键缺失时不该发 thinking: %.400s", rec.body(0))
	}

	s2, err := c.Generate(context.Background(), port.GenerateRequest{
		Model:    "m",
		Messages: []port.PromptMessage{{Role: "user", Content: []port.PromptPart{{Kind: "text", Text: "hi"}}}},
		Params:   port.Sampling{Thinking: &off},
	})
	if err != nil {
		t.Fatalf("generate2: %v", err)
	}
	_ = s2.Close()
	if !strings.Contains(rec.body(1), `"thinking":{"type":"disabled"}`) {
		t.Fatalf("显式关思考应为 disabled: %.400s", rec.body(1))
	}
	// 只给档位（键缺失）= 要思考、强度多少：这边只有 thinking 对象一个字段，
	// 档位必须能自己把思考打开（不能对档位装没看见）。
	s3, err := c.Generate(context.Background(), port.GenerateRequest{
		Model:    "m",
		Messages: []port.PromptMessage{{Role: "user", Content: []port.PromptPart{{Kind: "text", Text: "hi"}}}},
		Params:   port.Sampling{ReasoningEffort: "high"},
	})
	if err != nil {
		t.Fatalf("generate3: %v", err)
	}
	_ = s3.Close()
	var want3 = `"thinking":{"type":"enabled","budget_tokens":4096}`
	if !strings.Contains(rec.body(2), want3) {
		t.Fatalf("只给档位应打开思考（%s）: %.400s", want3, rec.body(2))
	}
	// 未开思考时 max_tokens 走兜底（不必为思考留位）。
	if !strings.Contains(rec.body(0), fmt.Sprintf(`"max_tokens":%d`, anthropicDefaultMaxTokens)) {
		t.Fatalf("max_tokens 兜底缺失: %.300s", rec.body(0))
	}
}

// TestAnthropicRoleMerging API 要求角色交替：相邻同角色消息必须合并（工具结果 +
// 紧随的工具图片正好合成一条 user）。相邻两条 user 也合成一条。
func TestAnthropicRoleMerging(t *testing.T) {
	srv, rec := newAnthropicServer(t, `data: {"type":"message_stop"}`+"\n")
	c := anthropicClient(t, srv.URL, "")

	s, err := c.Generate(context.Background(), port.GenerateRequest{
		Model: "m",
		Messages: []port.PromptMessage{
			{Role: "user", Content: []port.PromptPart{{Kind: "text", Text: "A"}}},
			{Role: "user", Content: []port.PromptPart{{Kind: "text", Text: "B"}}},
			{Role: "tool", CallID: "toolu_1", Content: []port.PromptPart{{Kind: "text", Text: "r1"}}},
			{Role: "tool", CallID: "toolu_2", Content: []port.PromptPart{{Kind: "text", Text: "r2"}}},
		},
	})
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	_ = s.Close()

	var body struct {
		Messages []struct {
			Role    string `json:"role"`
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal([]byte(rec.body(0)), &body); err != nil {
		t.Fatalf("解析: %v", err)
	}
	if len(body.Messages) != 1 {
		t.Fatalf("同角色应合并为一条: %+v", body.Messages)
	}
	if body.Messages[0].Role != "user" || len(body.Messages[0].Content) != 4 {
		t.Fatalf("合并结果 = %+v", body.Messages[0])
	}
}

// TestAnthropicForeignSignatureIgnored D175 口径三：别家前缀的签名不回放。
func TestAnthropicForeignSignatureIgnored(t *testing.T) {
	srv, rec := newAnthropicServer(t, `data: {"type":"message_stop"}`+"\n")
	c := anthropicClient(t, srv.URL, "")

	s, err := c.Generate(context.Background(), port.GenerateRequest{
		Model: "m",
		Messages: []port.PromptMessage{
			{Role: "user", Content: []port.PromptPart{{Kind: "text", Text: "hi"}}},
			{Role: "assistant", Reasoning: "想过", ReasoningSignature: "responses:whatever",
				Content: []port.PromptPart{{Kind: "text", Text: "答"}}},
		},
	})
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	_ = s.Close()

	if strings.Contains(rec.body(0), `"type":"thinking"`) {
		t.Fatalf("responses 签名不该在 anthropic 请求里回放: %.600s", rec.body(0))
	}
}

// TestAnthropicLeadingAssistantRejected API 要求首条消息是 user：装配出以 assistant
// 开头的上下文时当场报错（而不是发出去等 400）。
func TestAnthropicLeadingAssistantRejected(t *testing.T) {
	srv, _ := newAnthropicServer(t, "")
	c := anthropicClient(t, srv.URL, "")
	_, err := c.Generate(context.Background(), port.GenerateRequest{
		Model: "m",
		Messages: []port.PromptMessage{
			{Role: "assistant", Content: []port.PromptPart{{Kind: "text", Text: "我先说"}}},
		},
	})
	if err == nil || !strings.Contains(err.Error(), "user") {
		t.Fatalf("err = %v, want 指名首条须为 user", err)
	}
}

// TestAnthropicErrorEvents 流内 error 事件 → 报错中止。
func TestAnthropicErrorEvents(t *testing.T) {
	srv, _ := newAnthropicServer(t, `data: {"type":"error","error":{"type":"overloaded_error","message":"Overloaded"}}`+"\n")
	c := anthropicClient(t, srv.URL, "")
	s, err := c.Generate(context.Background(), port.GenerateRequest{
		Model:    "m",
		Messages: []port.PromptMessage{{Role: "user", Content: []port.PromptPart{{Kind: "text", Text: "hi"}}}},
	})
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	_, rerr := s.Recv()
	_ = s.Close()
	if rerr == nil || rerr == io.EOF {
		t.Fatalf("应报错，得到 %v", rerr)
	}
	if !strings.Contains(rerr.Error(), "Overloaded") {
		t.Fatalf("err = %v, want 服务端原话", rerr)
	}
}

// TestAnthropicStatusErrors 429 → *port.Quota（含 Retry-After 冷却）；5xx → 瞬时；
// 鉴权错误不换钥匙口径由装饰器判——这里只验分类。且**只有一次请求**（无剥离重试）。
func TestAnthropicStatusErrors(t *testing.T) {
	cases := []struct {
		code       int
		retryAfter string
	}{
		{http.StatusTooManyRequests, "5"},
		{http.StatusInternalServerError, ""},
		{http.StatusBadRequest, ""},
	}
	for _, tc := range cases {
		code := tc.code
		var mu sync.Mutex
		var n int
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			n++
			mu.Unlock()
			if tc.retryAfter != "" {
				w.Header().Set("Retry-After", tc.retryAfter)
			}
			w.WriteHeader(code)
			fmt.Fprint(w, `{"type":"error","error":{"type":"invalid_request_error","message":"nope"}}`)
		}))
		c := anthropicClient(t, srv.URL, "k")
		_, err := c.Generate(context.Background(), port.GenerateRequest{
			Model:    "m",
			Messages: []port.PromptMessage{{Role: "user", Content: []port.PromptPart{{Kind: "text", Text: "hi"}}}},
			Params:   port.Sampling{ReasoningEffort: "high"},
		})
		srv.Close()
		if err == nil {
			t.Fatalf("code %d: want error", code)
		}
		switch code {
		case http.StatusTooManyRequests:
			if !errors.Is(err, port.ErrQuota) || !errors.Is(err, port.ErrTransient) {
				t.Fatalf("429 应同时命中 ErrQuota 与 ErrTransient: %v", err)
			}
			if port.QuotaAfter(err) != 5*time.Second {
				t.Fatalf("QuotaAfter = %v, want 5s", port.QuotaAfter(err))
			}
		case http.StatusInternalServerError:
			if !errors.Is(err, port.ErrTransient) {
				t.Fatalf("5xx 应标瞬时: %v", err)
			}
		default:
			if errors.Is(err, port.ErrTransient) {
				t.Fatalf("400 不该标瞬时: %v", err)
			}
		}
		mu.Lock()
		got := n
		mu.Unlock()
		if got != 1 {
			t.Fatalf("code %d: 请求次数 = %d, want 1（无剥离重试）", code, got)
		}
	}
}

// TestAnthropicSignatureRoundTrip 签名往返：前缀 + 原样；前缀不符/空一律丢弃。
func TestAnthropicSignatureRoundTrip(t *testing.T) {
	sig := encodeAnthropicSignature("SIG+abc/def=")
	if sig != anthropicSigPrefix+"SIG+abc/def=" {
		t.Fatalf("编码 = %q", sig)
	}
	got, ok := decodeAnthropicSignature(sig)
	if !ok || got != "SIG+abc/def=" {
		t.Fatalf("往返失真: (%q,%v)", got, ok)
	}
	if encodeAnthropicSignature("") != "" {
		t.Fatal("空签名应返回空串")
	}
	for _, bad := range []string{"", "responses:xyz", "anthropic:"} {
		if _, ok := decodeAnthropicSignature(bad); ok {
			t.Fatalf("%q 不该被解出内容", bad)
		}
	}
}

// TestAnthropicModels 两套协议共用 /models。
func TestAnthropicModels(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1"+modelsPath {
			t.Errorf("path = %q", r.URL.Path)
		}
		if r.Header.Get("x-api-key") != "ak" || r.Header.Get("anthropic-version") != anthropicVersion {
			t.Errorf("鉴权头缺失")
		}
		fmt.Fprint(w, `{"data":[{"id":"m2"},{"id":"m1"}]}`)
	}))
	defer srv.Close()

	c := anthropicClient(t, srv.URL+"/v1", "ak")
	out, err := c.Models(context.Background())
	if err != nil {
		t.Fatalf("models: %v", err)
	}
	if len(out) != 2 || out[0].Name != "m1" || out[1].Name != "m2" {
		t.Fatalf("models = %+v", out)
	}
}

// TestAnthropicProtocolValidation 协议取值校验：空 = chat、三种已知协议都收、
// 其它词 fail-fast（anthropic 到此已接入，不再是「未接入」）。
func TestAnthropicProtocolValidation(t *testing.T) {
	base := "http://127.0.0.1:1"
	if c, err := New(Config{BaseURL: base}); err != nil || c.proto != ProtoChat {
		t.Fatalf("空协议应落到 chat: %v %q", err, c.proto)
	}
	for _, p := range []Protocol{ProtoChat, ProtoResponses, ProtoAnthropic} {
		if c, err := New(Config{BaseURL: base, Protocol: p}); err != nil || c.proto != p {
			t.Fatalf("%s 应被接受: %v %q", p, err, c.proto)
		}
	}
	if _, err := New(Config{BaseURL: base, Protocol: Protocol("grpc")}); err == nil {
		t.Fatal("未知协议应报错")
	}
}
