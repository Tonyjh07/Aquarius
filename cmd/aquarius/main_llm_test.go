package main

// main_llm_test.go 协议与密钥池：429 轮换与冷却、非配额错误不换钥匙、
// responses/anthropic 两种协议、服务端不认的参数被剔除、TUI 思考块回放。

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// TestRunRecordsUnsupportedParams D34 全链路：服务端 400 点名不认 reasoning_effort
// → 同请求剥离重试成功 → 自动写回 config model.unsupported_params（重启后不再发送）。
// TestRunKeyPoolRotatesOn429 密钥池端到端（D173/S11-4）：第一把钥匙 429（带 Retry-After）
// 时立刻换第二把钥匙重试（不退避），并把被拒的那把拉进冷却。
func TestRunKeyPoolRotatesOn429(t *testing.T) {
	var mu sync.Mutex
	var keys []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			http.NotFound(w, r)
			return
		}
		mu.Lock()
		keys = append(keys, strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
		mu.Unlock()
		if len(keys) == 1 {
			w.Header().Set("Retry-After", "30")
			w.WriteHeader(http.StatusTooManyRequests)
			fmt.Fprint(w, `{"error":{"message":"rate limited"}}`)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, `data: {"choices":[{"delta":{"content":"ok"}}]}`+"\n"+"data: [DONE]\n")
	}))
	defer srv.Close()

	dir := t.TempDir()
	cfg := fmt.Sprintf(`{"model":{"providers":[{"name":"p","base_url":%q,"keys":[
      {"label":"主账号","api_key":"secret:KEY_A"},{"label":"备用","api_key":"secret:KEY_B"}],
      "models":[{"name":"m"}]}],"name":"m"},"ui":{"kind":"repl"},
  "limits":{"max_turns":8,"max_context_tokens":64000,"tool_output_chars":20000,"tool_timeout_sec":60}}`, srv.URL)
	writeProfileLayout(t, dir, cfg)
	t.Setenv("KEY_A", "key-a")
	t.Setenv("KEY_B", "key-b")

	var out, errBuf bytes.Buffer
	if code := run([]string{"-data", dir}, strings.NewReader("hi\n/quit\n"), &out, io.Discard); code != 0 {
		t.Fatalf("code = %d, stderr = %q", code, errBuf.String())
	}
	mu.Lock()
	snapshot := append([]string(nil), keys...)
	mu.Unlock()
	if len(snapshot) != 2 || snapshot[0] != "key-a" || snapshot[1] != "key-b" {
		t.Fatalf("请求用的密钥 = %v, want [key-a key-b]（先第一把，429 后换第二把）", snapshot)
	}
	if !strings.Contains(out.String(), "ok") {
		t.Fatalf("第二轮应正常出结果：%q", out.String())
	}
	if strings.Contains(out.String(), "key-b") || strings.Contains(errBuf.String(), "key-b") {
		t.Fatal("用户面不得出现密钥值")
	}
}

// TestRunKeyPoolKeepsKeyOnNonQuota 5xx 不换钥匙（端点故障换钥匙没用，交重试/降级）。
func TestRunKeyPoolKeepsKeyOnNonQuota(t *testing.T) {
	var mu sync.Mutex
	var keys []string
	count := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			http.NotFound(w, r)
			return
		}
		mu.Lock()
		keys = append(keys, strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
		count++
		n := count
		mu.Unlock()
		if n == 1 {
			w.WriteHeader(http.StatusInternalServerError)
			fmt.Fprint(w, `{"error":{"message":"boom"}}`)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, `data: {"choices":[{"delta":{"content":"ok"}}]}`+"\n"+"data: [DONE]\n")
	}))
	defer srv.Close()

	dir := t.TempDir()
	cfg := fmt.Sprintf(`{"model":{"providers":[{"name":"p","base_url":%q,"keys":[
      {"api_key":"secret:KEY_A"},{"api_key":"secret:KEY_B"}],"models":[{"name":"m"}]}],"name":"m"},
  "ui":{"kind":"repl"},"limits":{"max_turns":8,"max_context_tokens":64000,"tool_output_chars":20000,"tool_timeout_sec":60}}`, srv.URL)
	writeProfileLayout(t, dir, cfg)
	t.Setenv("KEY_A", "key-a")
	t.Setenv("KEY_B", "key-b")

	var out bytes.Buffer
	if code := run([]string{"-data", dir}, strings.NewReader("hi\n/quit\n"), &out, io.Discard); code != 0 {
		t.Fatalf("code = %d", code)
	}
	mu.Lock()
	snapshot := append([]string(nil), keys...)
	mu.Unlock()
	// 重试层（3 次）都该是第一把；不该出现第二把。
	for _, k := range snapshot {
		if k == "key-b" {
			t.Fatalf("5xx 不该换钥匙：keys = %v", snapshot)
		}
	}
	if len(snapshot) < 2 {
		t.Fatalf("应经重试层再试一次：keys = %v", snapshot)
	}
	if !strings.Contains(out.String(), "ok") {
		t.Fatalf("重试后应给出结果：%q", out.String())
	}
}

// TestRunResponsesProtocol D175 端到端：provider kind=openai-responses 时请求打到
// `/responses`（不是 `/chat/completions`），并按 Responses 的事件族解析出正文与用量。
func TestRunResponsesProtocol(t *testing.T) {
	var mu sync.Mutex
	var paths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		paths = append(paths, r.URL.Path)
		mu.Unlock()
		if r.URL.Path != "/responses" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, `data: {"type":"response.output_text.delta","output_index":0,"delta":"hello-responses"}`+"\n"+
			`data: {"type":"response.completed","response":{"output":[],"usage":{"input_tokens":1,"output_tokens":2}}}`+"\n")
	}))
	defer srv.Close()

	dir := t.TempDir()
	cfg := fmt.Sprintf(`{"model":{"providers":[{"name":"p","kind":"openai-responses","base_url":%q,"keys":[{"api_key":"secret:X"}],"models":[{"name":"m"}]}],"name":"m"},"ui":{"kind":"repl"},
  "limits":{"max_turns":8,"max_context_tokens":64000,"tool_output_chars":20000,"tool_timeout_sec":60}}`, srv.URL)
	writeProfileLayout(t, dir, cfg)
	t.Setenv("X", "k")

	var out bytes.Buffer
	if code := run([]string{"-data", dir}, strings.NewReader("hi\n/quit\n"), &out, io.Discard); code != 0 {
		t.Fatalf("code = %d, out = %q", code, out.String())
	}
	mu.Lock()
	snapshot := append([]string(nil), paths...)
	mu.Unlock()
	if len(snapshot) == 0 || snapshot[0] != "/responses" {
		t.Fatalf("请求路径 = %v, want 首个为 /responses", snapshot)
	}
	if !strings.Contains(out.String(), "hello-responses") {
		t.Fatalf("正文未显示：%q", out.String())
	}
}

// TestRunAnthropicProtocol 端到端：kind = anthropic 的 provider 由装配根造出 Messages
// 客户端——请求发到 /messages（不是 /chat/completions），正文照常显示。
func TestRunAnthropicProtocol(t *testing.T) {
	var mu sync.Mutex
	var paths []string
	var apiKeys []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		paths = append(paths, r.URL.Path)
		apiKeys = append(apiKeys, r.Header.Get("x-api-key"))
		mu.Unlock()
		if r.URL.Path != "/messages" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, `data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"hello-anthropic"}}`+"\n"+
			`data: {"type":"message_stop"}`+"\n")
	}))
	defer srv.Close()

	dir := t.TempDir()
	cfg := fmt.Sprintf(`{"model":{"providers":[{"name":"p","kind":"anthropic","base_url":%q,"keys":[{"api_key":"secret:X"}],"models":[{"name":"m"}]}],"name":"m"},"ui":{"kind":"repl"},
  "limits":{"max_turns":8,"max_context_tokens":64000,"tool_output_chars":20000,"tool_timeout_sec":60}}`, srv.URL)
	writeProfileLayout(t, dir, cfg)
	t.Setenv("X", "k")

	var out bytes.Buffer
	if code := run([]string{"-data", dir}, strings.NewReader("hi\n/quit\n"), &out, io.Discard); code != 0 {
		t.Fatalf("code = %d, out = %q", code, out.String())
	}
	mu.Lock()
	snapshot := append([]string(nil), paths...)
	keys := append([]string(nil), apiKeys...)
	mu.Unlock()
	if len(snapshot) == 0 || snapshot[0] != "/messages" {
		t.Fatalf("请求路径 = %v, want 首个为 /messages", snapshot)
	}
	// 密钥经 x-api-key（不是 Bearer）——协议选对了鉴权头才跟着对。
	if len(keys) == 0 || keys[0] != "k" {
		t.Fatalf("x-api-key = %v, want k", keys)
	}
	if !strings.Contains(out.String(), "hello-anthropic") {
		t.Fatalf("正文未显示：%q", out.String())
	}
}

// TestRunRecordsUnsupportedParams 服务端点名拒绝某参数：本请求剥离重试成功后写回
// config providers[0].unsupported_params，且不改动原档位（只记不改）。
func TestRunRecordsUnsupportedParams(t *testing.T) {
	var mu sync.Mutex
	var n int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		n++
		first := n == 1 && strings.Contains(string(b), "reasoning_effort")
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

	dir := t.TempDir()
	cfg := fmt.Sprintf(`{"model":{"providers":[{"name":"p","base_url":%q,"keys":[{"api_key":"secret:X"}],"models":[{"name":"m"}]}],"name":"m","think":true,"reasoning_effort":"high"},"ui":{"kind":"repl"},
  "limits":{"max_turns":8,"max_context_tokens":64000,"tool_output_chars":20000,"tool_timeout_sec":60}}`, srv.URL)
	writeProfileLayout(t, dir, cfg)
	t.Setenv("X", "k")

	var out bytes.Buffer
	if code := run([]string{"-data", dir}, strings.NewReader("hi\n/quit\n"), &out, io.Discard); code != 0 {
		t.Fatalf("code = %d, out = %q", code, out.String())
	}
	mu.Lock()
	if n < 2 {
		t.Fatalf("requests = %d, want >=2（400 后剥离重试）", n)
	}
	mu.Unlock()

	data, err := os.ReadFile(filepath.Join(dir, "profiles", "default", "config.json"))
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	var parsed struct {
		Model struct {
			Providers []struct {
				UnsupportedParams []string `json:"unsupported_params"`
			} `json:"providers"`
			ReasoningEffort string `json:"reasoning_effort"`
		} `json:"model"`
	}
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("parse config: %v", err)
	}
	if len(parsed.Model.Providers) != 1 || len(parsed.Model.Providers[0].UnsupportedParams) != 1 ||
		parsed.Model.Providers[0].UnsupportedParams[0] != "reasoning_effort" {
		t.Fatalf("providers[0].unsupported_params = %v, want [reasoning_effort]", parsed.Model.Providers)
	}
	if parsed.Model.ReasoningEffort != "high" {
		t.Fatalf("reasoning_effort = %q, want 保留原档位（只记不改）", parsed.Model.ReasoningEffort)
	}
}

// TestRunTUIReasoningE2E D34/D42：思维链全链路——SSE reasoning_content 分片 → TUI
// [thinking] 暗块展示，正文照常提交（思考随节点入树见 app 层测试与 TestRunEchoThinkingE2E）。
func TestRunTUIReasoningE2E(t *testing.T) {
	reasonLine := `data: {"choices":[{"delta":{"reasoning_content":"先想一想"}}]}`
	srv, reqs := rawScriptServer(t, [][]string{{reasonLine, contentData("答案")}})
	dir := t.TempDir()
	cfg := fmt.Sprintf(`{"model":{"providers":[{"name":"p","base_url":%q,"keys":[{"api_key":"secret:X"}],"models":[{"name":"m"}]}],"name":"m"},"ui":{"kind":"tui"},
  "limits":{"max_turns":8,"max_context_tokens":64000,"tool_output_chars":20000,"tool_timeout_sec":60}}`, srv.URL)
	writeProfileLayout(t, dir, cfg)
	t.Setenv("X", "k")

	var out bytes.Buffer
	if code := run([]string{"-data", dir}, strings.NewReader("hi\n/quit\n"), &out, io.Discard); code != 0 {
		t.Fatalf("code = %d, out = %q", code, out.String())
	}
	got := out.String()
	for _, want := range []string{"[thinking]", "先想一想", "答案"} {
		if !strings.Contains(got, want) {
			t.Fatalf("TUI 思维链链路缺 %q: %.500s", want, got)
		}
	}
	if reqs.len() != 1 {
		t.Fatalf("llm requests = %d, want 1", reqs.len())
	}
}

// TestRunEchoThinkingE2E D42 链路验收：第一轮 SSE 思考分片随节点入树；
// 第二轮请求把上一轮思考以消息级 reasoning_content 回传——config 键缺失 = 回传（缺省），
// 显式 echo_thinking:false = 装配时过滤不发。
func TestRunEchoThinkingE2E(t *testing.T) {
	for _, tc := range []struct {
		name       string
		modelExtra string // 追加进 model 段的配置（"" = 键缺失）
		wantEcho   bool
	}{
		{"键缺失默认回传", "", true},
		{"显式关闭不回传", `, "echo_thinking": false`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reasonLine := `data: {"choices":[{"delta":{"reasoning_content":"先想一想"}}]}`
			srv, reqs := rawScriptServer(t, [][]string{
				{reasonLine, contentData("答案")},
				{contentData("第二答")},
			})
			dir := t.TempDir()
			cfg := fmt.Sprintf(`{"model":{"providers":[{"name":"p","base_url":%q,"keys":[{"api_key":"secret:X"}],"models":[{"name":"m"}]}],"name":"m"%s},"ui":{"kind":"repl"},
  "limits":{"max_turns":8,"max_context_tokens":64000,"tool_output_chars":20000,"tool_timeout_sec":60}}`,
				srv.URL, tc.modelExtra)
			writeProfileLayout(t, dir, cfg)
			t.Setenv("X", "k")

			var out bytes.Buffer
			in := strings.NewReader("hi\nagain\n/quit\n")
			if code := run([]string{"-data", dir}, in, &out, io.Discard); code != 0 {
				t.Fatalf("code = %d, out = %q", code, out.String())
			}
			if reqs.len() != 2 {
				t.Fatalf("llm requests = %d, want 2（两轮对话）", reqs.len())
			}
			second := string(reqs.at(1).body)
			if tc.wantEcho {
				if !strings.Contains(second, `"reasoning_content":"先想一想"`) {
					t.Fatalf("第二轮应回传 reasoning_content: %.600s", second)
				}
			} else if strings.Contains(second, "reasoning_content") {
				t.Fatalf("echo_thinking=false 不应含 reasoning_content: %.600s", second)
			}
			// 正文与思考分离：历史正文照常回传，思考不混入 content 字段。
			if !strings.Contains(second, "答案") {
				t.Fatalf("第二轮缺历史正文: %.600s", second)
			}
		})
	}
}
