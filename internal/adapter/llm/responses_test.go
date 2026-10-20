package llm

// openai-responses 适配器测试（S11-5②）：请求体形状、SSE 事件族回放、签名编解码、
// 与 chat 的差异（无剥离重试、429 仍走密钥池口径）。
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

	"github.com/Tonyjh07/Aquarius/internal/domain/conversation"
	"github.com/Tonyjh07/Aquarius/internal/domain/tool"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

// collectResponses 读到 EOF，返回拼接正文、思考文本、签名、全部工具分片与用量。
func collectResponses(t *testing.T, s port.Stream) (text, reasoning, sig string, calls []port.ToolCallDelta, usage *conversation.Usage) {
	t.Helper()
	var tb, rb strings.Builder
	for {
		d, err := s.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("recv: %v", err)
		}
		if d.Reasoning {
			rb.WriteString(d.Text)
		} else {
			tb.WriteString(d.Text)
		}
		if d.ReasoningSignature != "" {
			sig = d.ReasoningSignature
		}
		calls = append(calls, d.ToolCalls...)
		if d.Usage != nil {
			usage = d.Usage
		}
	}
	return tb.String(), rb.String(), sig, calls, usage
}

// responsesSSEScript 录制的 Responses 事件流：思考 item（含加密内容）→ 正文 → 工具调用
// → completed（含 usage）。刻意保留 event: 行（chat 的解析忽略它们，这里也应当忽略）。
const responsesSSEScript = "" +
	"event: response.output_item.added\n" +
	`data: {"type":"response.output_item.added","output_index":0,"item":{"type":"reasoning","id":"rs_1","summary":[],"encrypted_content":"PARTIAL"}}` + "\n" +
	"\n" +
	"event: response.reasoning_summary_text.delta\n" +
	`data: {"type":"response.reasoning_summary_text.delta","output_index":0,"item_id":"rs_1","delta":"think"}` + "\n" +
	`data: {"type":"response.reasoning_summary_text.delta","output_index":0,"item_id":"rs_1","delta":"ing"}` + "\n" +
	"event: response.output_item.done\n" +
	`data: {"type":"response.output_item.done","output_index":0,"item":{"type":"reasoning","id":"rs_1","summary":[{"type":"summary_text","text":"thinking"}],"encrypted_content":"FULL_ENC"}}` + "\n" +
	"\n" +
	`data: {"type":"response.output_text.delta","output_index":1,"delta":"Hel"}` + "\n" +
	`data: {"type":"response.output_text.delta","output_index":1,"delta":"lo"}` + "\n" +
	`data: {"type":"response.output_item.added","output_index":2,"item":{"type":"function_call","id":"fc_1","call_id":"call_9","name":"echo","arguments":""}}` + "\n" +
	`data: {"type":"response.function_call_arguments.delta","output_index":2,"item_id":"fc_1","delta":"{\"m\":"}` + "\n" +
	`data: {"type":"response.function_call_arguments.delta","output_index":2,"item_id":"fc_1","delta":"\"x\"}"}` + "\n" +
	`data: {"type":"response.function_call_arguments.done","output_index":2,"item_id":"fc_1","name":"echo","arguments":"{\"m\":\"x\"}"}` + "\n" +
	`data: {"type":"response.completed","response":{"output":[],"usage":{"input_tokens":12,"output_tokens":34}}}` + "\n"

// newResponsesServer 起一个假 responses 端点：记录path/auth/请求体，回放给定脚本。
func newResponsesServer(t *testing.T, script string) (*httptest.Server, *recorded) {
	t.Helper()
	rec := &recorded{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		rec.add(r.URL.Path, r.Header.Get("Authorization"), string(b))
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, script)
	}))
	t.Cleanup(srv.Close)
	return srv, rec
}

// recorded 假 server 收到的请求快照。
type recorded struct {
	mu     sync.Mutex
	paths  []string
	auths  []string
	bodies []string
}

func (r *recorded) add(path, auth, body string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.paths = append(r.paths, path)
	r.auths = append(r.auths, auth)
	r.bodies = append(r.bodies, body)
}

func (r *recorded) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.bodies)
}

func (r *recorded) body(i int) string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.bodies[i]
}

// responsesClient 造一个 responses 协议的客户端。
func responsesClient(t *testing.T, base string) *Client {
	t.Helper()
	c, err := New(Config{BaseURL: base, APIKey: "sk-test", Protocol: ProtoResponses})
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	return c
}

// TestResponsesGenerateStreams D175：事件族回放——思考摘要（两次分片拼成 thinking）、
// 正文、工具调用（added 给 ID/Name，delta 给参数，done 不重复发参数）、
// 思考签名只在 done 取完整值、completed 给 usage。
func TestResponsesGenerateStreams(t *testing.T) {
	srv, rec := newResponsesServer(t, responsesSSEScript)
	c := responsesClient(t, srv.URL+"/v1/")

	s, err := c.Generate(context.Background(), port.GenerateRequest{
		Model:    "gpt-5",
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
	if reasoning != "thinking" {
		t.Fatalf("reasoning = %q, want thinking", reasoning)
	}
	wantSig := encodeResponsesSignature("rs_1", "FULL_ENC")
	if sig != wantSig {
		t.Fatalf("sig = %q, want %q（必须是 done 的完整值，不是 added 的 PARTIAL）", sig, wantSig)
	}
	if id, enc, ok := decodeResponsesSignature(sig); !ok || id != "rs_1" || enc != "FULL_ENC" {
		t.Fatalf("sig 解码 = (%q,%q,%v)", id, enc, ok)
	}
	// added(ID/Name) + 两个参数 delta + done(仅名字，参数已收过不重发)
	if len(calls) != 4 {
		t.Fatalf("tool deltas = %d, want 4: %+v", len(calls), calls)
	}
	if calls[0].Index != 2 || calls[0].ID != "call_9" || calls[0].Name != "echo" {
		t.Fatalf("call delta 0 = %+v", calls[0])
	}
	args := calls[1].ArgsDelta + calls[2].ArgsDelta
	if args != `{"m":"x"}` {
		t.Fatalf("拼出的参数 = %q", args)
	}
	if calls[3].ArgsDelta != "" {
		t.Fatalf("done 不应重复发参数: %+v", calls[3])
	}
	if usage == nil || usage.InputTokens != 12 || usage.OutputTokens != 34 {
		t.Fatalf("usage = %+v, want {12,34}", usage)
	}
	if rec.paths[0] != "/v1"+responsesPath {
		t.Fatalf("path = %q", rec.paths[0])
	}
	if rec.auths[0] != "Bearer sk-test" {
		t.Fatalf("auth = %q", rec.auths[0])
	}
}

// TestResponsesRequestShape D175：请求体——input item 数组、system 走 message item、
// 图片走 input_image data URL、assistant 的思考→reasoning item（带加密内容+摘要）、
// 工具调用→function_call item、tool→function_call_output、扁平 tools、store:false。
func TestResponsesRequestShape(t *testing.T) {
	srv, rec := newResponsesServer(t, `data: {"type":"response.completed","response":{"output":[],"usage":{"input_tokens":1,"output_tokens":1}}}`+"\n")
	c := responsesClient(t, srv.URL+"/v1/")

	sig := encodeResponsesSignature("rs_9", "ENC9")
	on := true
	s, err := c.Generate(context.Background(), port.GenerateRequest{
		Model: "gpt-5",
		Messages: []port.PromptMessage{
			{Role: "system", Content: []port.PromptPart{{Kind: "text", Text: "sys"}}},
			{Role: "user", Content: []port.PromptPart{
				{Kind: "text", Text: "看图"},
				{Kind: "image", MIME: "image/jpeg", Data: []byte{0x01, 0x02}},
			}},
			{Role: "assistant", Reasoning: "想过", ReasoningSignature: sig,
				Content:   []port.PromptPart{{Kind: "text", Text: "答"}},
				ToolCalls: []tool.Call{{ID: "call_1", Name: "echo", Args: json.RawMessage(`{"m":"x"}`)}}},
			{Role: "tool", CallID: "call_1", Content: []port.PromptPart{{Kind: "text", Text: "pong"}}},
		},
		Tools:  []tool.Spec{{Name: "echo", Description: "回声", Schema: json.RawMessage(`{"type":"object","properties":{}}`)}},
		Params: port.Sampling{ReasoningEffort: "high", Thinking: &on, Temperature: 0.5},
		Budget: port.TokenBudget{MaxOutputTokens: 128},
	})
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	_ = s.Close()

	var body struct {
		Model           string   `json:"model"`
		Stream          bool     `json:"stream"`
		Store           bool     `json:"store"`
		Include         []string `json:"include"`
		MaxOutputTokens int      `json:"max_output_tokens"`
		Temperature     float64  `json:"temperature"`
		Reasoning       *struct {
			Effort  string `json:"effort"`
			Summary string `json:"summary"`
		} `json:"reasoning"`
		Tools []struct {
			Type       string          `json:"type"`
			Name       string          `json:"name"`
			Parameters json.RawMessage `json:"parameters"`
		} `json:"tools"`
		Input []map[string]any `json:"input"`
	}
	raw := rec.body(0)
	if err := json.Unmarshal([]byte(raw), &body); err != nil {
		t.Fatalf("解析请求体: %v: %.600s", err, raw)
	}
	if body.Model != "gpt-5" || !body.Stream || body.Store {
		t.Fatalf("头部字段 = %+v（store 必须 false）", body)
	}
	if body.MaxOutputTokens != 128 || body.Temperature != 0.5 {
		t.Fatalf("预算/采样 = %d / %v", body.MaxOutputTokens, body.Temperature)
	}
	if body.Reasoning == nil || body.Reasoning.Effort != "high" || body.Reasoning.Summary != "auto" {
		t.Fatalf("reasoning = %+v", body.Reasoning)
	}
	if len(body.Include) != 1 || body.Include[0] != "reasoning.encrypted_content" {
		t.Fatalf("include = %v", body.Include)
	}
	if len(body.Tools) != 1 || body.Tools[0].Type != "function" || body.Tools[0].Name != "echo" {
		t.Fatalf("tools = %+v（应为扁平结构）", body.Tools)
	}
	if strings.Contains(raw, `"function":{"name"`) {
		t.Fatalf("tools 不该嵌 function 层: %.600s", raw)
	}

	if len(body.Input) != 6 {
		t.Fatalf("input items = %d, want 6: %.600s", len(body.Input), raw)
	}
	if body.Input[0]["type"] != "message" || body.Input[0]["role"] != "system" {
		t.Fatalf("item0 = %+v", body.Input[0])
	}
	// user：文本 input_text + 图片 input_image data URL
	content, _ := body.Input[1]["content"].([]any)
	if len(content) != 2 {
		t.Fatalf("user content = %+v", body.Input[1]["content"])
	}
	p0, _ := content[0].(map[string]any)
	p1, _ := content[1].(map[string]any)
	if p0["type"] != "input_text" || p0["text"] != "看图" {
		t.Fatalf("user part0 = %+v", p0)
	}
	if p1["type"] != "input_image" || p1["image_url"] != "data:image/jpeg;base64,AQI=" {
		t.Fatalf("user part1 = %+v", p1)
	}
	// assistant：reasoning item → message(output_text) → function_call
	if body.Input[2]["type"] != "reasoning" || body.Input[2]["encrypted_content"] != "ENC9" || body.Input[2]["id"] != "rs_9" {
		t.Fatalf("item2(reasoning) = %+v", body.Input[2])
	}
	if body.Input[3]["type"] != "message" || body.Input[3]["role"] != "assistant" {
		t.Fatalf("item3 = %+v", body.Input[3])
	}
	acontent, _ := body.Input[3]["content"].([]any)
	ap0, _ := acontent[0].(map[string]any)
	if ap0["type"] != "output_text" {
		t.Fatalf("assistant 正文应为 output_text: %+v", ap0)
	}
	if body.Input[4]["type"] != "function_call" || body.Input[4]["call_id"] != "call_1" ||
		body.Input[4]["name"] != "echo" || body.Input[4]["arguments"] != `{"m":"x"}` {
		t.Fatalf("item4(function_call) = %+v", body.Input[4])
	}
	// tool 回填
	if body.Input[5]["type"] != "function_call_output" || body.Input[5]["call_id"] != "call_1" ||
		body.Input[5]["output"] != "pong" {
		t.Fatalf("item5(function_call_output) = %+v", body.Input[5])
	}
}

// TestResponsesNoReasoningParams 未设置思考参数 = 既不发 reasoning 也不发 include
// （缺省零字段变化；不给自己找被兼容端点拒掉的机会）。
func TestResponsesNoReasoningParams(t *testing.T) {
	srv, rec := newResponsesServer(t, `data: {"type":"response.completed","response":{}}`+"\n")
	c := responsesClient(t, srv.URL)

	s, err := c.Generate(context.Background(), port.GenerateRequest{
		Model:    "gpt-4.1",
		Messages: []port.PromptMessage{{Role: "user", Content: []port.PromptPart{{Kind: "text", Text: "hi"}}}},
	})
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	_ = s.Close()

	raw := rec.body(0)
	for _, bad := range []string{"reasoning", "include"} {
		if strings.Contains(raw, `"`+bad+`"`) {
			t.Fatalf("未设置思考参数时不该出现 %s: %.400s", bad, raw)
		}
	}
	// enable_thinking=false 显式关闭：同样不发 reasoning（交服务端默认）。
	off := false
	s2, err := c.Generate(context.Background(), port.GenerateRequest{
		Model:    "gpt-5",
		Messages: []port.PromptMessage{{Role: "user", Content: []port.PromptPart{{Kind: "text", Text: "hi"}}}},
		Params:   port.Sampling{Thinking: &off, ReasoningEffort: "high"},
	})
	if err != nil {
		t.Fatalf("generate2: %v", err)
	}
	_ = s2.Close()
	if raw2 := rec.body(1); strings.Contains(raw2, `"reasoning"`) {
		t.Fatalf("显式关闭思考时不该发 reasoning: %.400s", raw2)
	}
}

// TestResponsesForeignSignatureIgnored D175 口径三：别家前缀的签名一律不回放——
// 跨 provider 复制会话时不能把 A 家的签名发给 B 家。
func TestResponsesForeignSignatureIgnored(t *testing.T) {
	srv, rec := newResponsesServer(t, `data: {"type":"response.completed","response":{}}`+"\n")
	c := responsesClient(t, srv.URL)

	s, err := c.Generate(context.Background(), port.GenerateRequest{
		Model: "gpt-5",
		Messages: []port.PromptMessage{
			{Role: "user", Content: []port.PromptPart{{Kind: "text", Text: "hi"}}},
			{Role: "assistant", Reasoning: "想过", ReasoningSignature: "anthropic:whatever",
				Content: []port.PromptPart{{Kind: "text", Text: "答"}}},
		},
	})
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	_ = s.Close()

	raw := rec.body(0)
	if strings.Contains(raw, `"type":"reasoning"`) {
		t.Fatalf("anthropic 签名不该在 responses 请求里回放: %.600s", raw)
	}
}

// TestResponsesReasoningWithoutSuccessor 思考 item 必须有后继（产出它的消息/工具调用）——
// 孤立思考不发（服务端会拒），空壳思考节点也就随之丢掉。
func TestResponsesReasoningWithoutSuccessor(t *testing.T) {
	srv, rec := newResponsesServer(t, `data: {"type":"response.completed","response":{}}`+"\n")
	c := responsesClient(t, srv.URL)

	s, err := c.Generate(context.Background(), port.GenerateRequest{
		Model: "gpt-5",
		Messages: []port.PromptMessage{
			{Role: "user", Content: []port.PromptPart{{Kind: "text", Text: "hi"}}},
			{Role: "assistant", Reasoning: "只想过没说话", ReasoningSignature: encodeResponsesSignature("rs_1", "E")},
		},
	})
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	_ = s.Close()

	if strings.Contains(rec.body(0), `"type":"reasoning"`) {
		t.Fatalf("无后继的孤立思考不该发: %.600s", rec.body(0))
	}
}

// TestResponsesFallbackFromCompleted 端点不发 output_item.done 就直奔 completed 时，
// 从 response.output 补齐工具参数与思考签名（补不上宁可少回放，也不编）。
func TestResponsesFallbackFromCompleted(t *testing.T) {
	script := `data: {"type":"response.output_text.delta","output_index":0,"delta":"ok"}` + "\n" +
		`data: {"type":"response.completed","response":{"output":[` +
		`{"type":"reasoning","id":"rs_2","encrypted_content":"E2"},` +
		`{"type":"function_call","call_id":"call_7","name":"ping","arguments":"{\"a\":1}"}` +
		`],"usage":{"input_tokens":5,"output_tokens":6}}}` + "\n"
	srv, _ := newResponsesServer(t, script)
	c := responsesClient(t, srv.URL)

	s, err := c.Generate(context.Background(), port.GenerateRequest{
		Model:    "gpt-5",
		Messages: []port.PromptMessage{{Role: "user", Content: []port.PromptPart{{Kind: "text", Text: "hi"}}}},
	})
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	defer s.Close()

	text, _, sig, calls, usage := collectResponses(t, s)
	if text != "ok" {
		t.Fatalf("text = %q", text)
	}
	if _, enc, ok := decodeResponsesSignature(sig); !ok || enc != "E2" {
		t.Fatalf("补齐的签名 = %q ok=%v", sig, ok)
	}
	if len(calls) != 1 || calls[0].Index != 1 || calls[0].ID != "call_7" ||
		calls[0].Name != "ping" || calls[0].ArgsDelta != `{"a":1}` {
		t.Fatalf("补齐的工具调用 = %+v", calls)
	}
	if usage == nil || usage.InputTokens != 5 || usage.OutputTokens != 6 {
		t.Fatalf("usage = %+v", usage)
	}
}

// TestResponsesErrorEvents 流内错误事件 → 报错中止（不是静默收流）。
func TestResponsesErrorEvents(t *testing.T) {
	for _, script := range []string{
		`data: {"type":"error","code":"server_error","message":"boom"}` + "\n",
		`data: {"type":"response.failed","response":{"error":{"code":"x","message":"生成失败"}}}` + "\n",
	} {
		srv, _ := newResponsesServer(t, script)
		c := responsesClient(t, srv.URL)
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
			t.Fatalf("script %.60s: 应报错，得到 %v", script, rerr)
		}
	}
}

// TestResponsesStatusErrors 429 → *port.Quota（同时 Is ErrTransient，走密钥池换钥匙）；
// 5xx → ErrTransient；400 → 普通错误。
func TestResponsesStatusErrors(t *testing.T) {
	cases := []struct {
		code       int
		retryAfter string
	}{
		{http.StatusTooManyRequests, "7"},
		{http.StatusInternalServerError, ""},
		{http.StatusBadRequest, ""},
	}
	for _, tc := range cases {
		code := tc.code
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if tc.retryAfter != "" {
				w.Header().Set("Retry-After", tc.retryAfter)
			}
			w.WriteHeader(code)
			fmt.Fprint(w, `{"error":{"message":"nope"}}`)
		}))
		c := responsesClient(t, srv.URL)
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
			if port.QuotaAfter(err) != 7*time.Second {
				t.Fatalf("QuotaAfter = %v, want 7s", port.QuotaAfter(err))
			}
		case http.StatusInternalServerError:
			if !errors.Is(err, port.ErrTransient) {
				t.Fatalf("5xx 应标瞬时: %v", err)
			}
			if errors.Is(err, port.ErrQuota) {
				t.Fatalf("5xx 不该标成配额: %v", err)
			}
		default:
			if errors.Is(err, port.ErrTransient) || errors.Is(err, port.ErrQuota) {
				t.Fatalf("400 不该标瞬时/配额: %v", err)
			}
		}
	}
}

// TestResponsesNoStripRetry D175 口径四：非 chat 协议不做剥离重试——参数被点名拒绝
// 就照实报错，不静默剥掉（否则配置错误永远浮不上来），且只有一次请求。
func TestResponsesNoStripRetry(t *testing.T) {
	var mu sync.Mutex
	var n int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		n++
		mu.Unlock()
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprint(w, `{"error":{"message":"Unknown parameter: 'reasoning'"}}`)
	}))
	defer srv.Close()

	var noted []string
	c, err := New(Config{
		BaseURL: srv.URL, Protocol: ProtoResponses,
		NoteUnsupported: func(f string) { noted = append(noted, f) },
	})
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	on := true
	_, err = c.Generate(context.Background(), port.GenerateRequest{
		Model:    "m",
		Messages: []port.PromptMessage{{Role: "user", Content: []port.PromptPart{{Kind: "text", Text: "hi"}}}},
		Params:   port.Sampling{ReasoningEffort: "high", Thinking: &on},
	})
	if err == nil {
		t.Fatal("want error")
	}
	mu.Lock()
	defer mu.Unlock()
	if n != 1 {
		t.Fatalf("请求次数 = %d, want 1（不得剥离重试）", n)
	}
	if len(noted) != 0 {
		t.Fatalf("noted = %v, want 空（不记录 unsupported_params）", noted)
	}
}

// TestNewProtocolValidation 协议取值校验：空 = chat、responses 认得、anthropic 未接入、
// 其它词 fail-fast。
func TestNewProtocolValidation(t *testing.T) {
	base := "http://127.0.0.1:1"
	if c, err := New(Config{BaseURL: base}); err != nil || c.proto != ProtoChat {
		t.Fatalf("空协议应落到 chat: %v %q", err, c.proto)
	}
	if c, err := New(Config{BaseURL: base, Protocol: ProtoResponses}); err != nil || c.proto != ProtoResponses {
		t.Fatalf("responses 应被接受: %v %q", err, c.proto)
	}
	if _, err := New(Config{BaseURL: base, Protocol: ProtoAnthropic}); err == nil {
		t.Fatal("anthropic 尚未接入，应报错")
	}
	if _, err := New(Config{BaseURL: base, Protocol: Protocol("grpc")}); err == nil {
		t.Fatal("未知协议应报错")
	}
}

// TestResponsesModels 两套协议共用 /models。
func TestResponsesModels(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1"+modelsPath {
			t.Errorf("path = %q", r.URL.Path)
		}
		fmt.Fprint(w, `{"data":[{"id":"b"},{"id":"a"}]}`)
	}))
	defer srv.Close()

	c := responsesClient(t, srv.URL+"/v1")
	out, err := c.Models(context.Background())
	if err != nil {
		t.Fatalf("models: %v", err)
	}
	if len(out) != 2 || out[0].Name != "a" || out[1].Name != "b" {
		t.Fatalf("models = %+v", out)
	}
}

// TestResponsesSignatureRoundTrip 签名编解码：往返保真、前缀不符/损坏/base64 坏一律丢弃。
func TestResponsesSignatureRoundTrip(t *testing.T) {
	sig := encodeResponsesSignature("rs_abc", "BIG"+strings.Repeat("x", 5000))
	if !strings.HasPrefix(sig, responsesSigPrefix) {
		t.Fatalf("缺少协议前缀: %q", sig)
	}
	id, enc, ok := decodeResponsesSignature(sig)
	if !ok || id != "rs_abc" || enc != "BIG"+strings.Repeat("x", 5000) {
		t.Fatalf("往返失真: ok=%v id=%q len(enc)=%d", ok, id, len(enc))
	}
	if len(sig) > 5000 {
		t.Fatalf("压缩后应明显短于原串: len=%d", len(sig))
	}
	// 无加密内容 = 不留签名。
	if encodeResponsesSignature("rs", "") != "" {
		t.Fatal("无 encrypted_content 应返回空签名")
	}
	for _, bad := range []string{"", "anthropic:abc", "responses:", "responses:!!!not-base64", "responses:aGVsbG8="} {
		if _, _, ok := decodeResponsesSignature(bad); ok {
			t.Fatalf("%q 不该被解出内容", bad)
		}
	}
}

// TestResponsesAssistantImageRejected 图片只从 user 消息进；assistant 带图 = 装配接错线，
// 当场报错而不是把图悄悄丢掉。
func TestResponsesAssistantImageRejected(t *testing.T) {
	srv, _ := newResponsesServer(t, "")
	c := responsesClient(t, srv.URL)
	_, err := c.Generate(context.Background(), port.GenerateRequest{
		Model: "m",
		Messages: []port.PromptMessage{
			{Role: "assistant", Content: []port.PromptPart{{Kind: "image", MIME: "image/png", Data: []byte{1}}}},
		},
	})
	if err == nil {
		t.Fatal("assistant 带图应报错")
	}
}
