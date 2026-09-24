package main

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
	"testing"

	"github.com/Tonyjh07/Aquarius/internal/domain/conversation"
)

// recordedReq 脚本服务记录的一次请求。
type recordedReq struct {
	body []byte
}

// scriptServer 造一个按序回话的 OpenAI 兼容 SSE 服务。
// replies[i] 是第 i+1 次请求的回复文本。
func scriptServer(t *testing.T, replies []string) (*httptest.Server, *[]recordedReq) {
	t.Helper()
	var reqs []recordedReq
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			http.NotFound(w, r)
			return
		}
		body, _ := io.ReadAll(r.Body)
		reqs = append(reqs, recordedReq{body: body})
		i := len(reqs) - 1
		if i >= len(replies) {
			t.Errorf("第 %d 次请求超出脚本", i+1)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprintf(w, "data: {\"choices\":[{\"delta\":{\"content\":%q}}]}\n", replies[i])
		fmt.Fprint(w, "data: [DONE]\n")
	}))
	t.Cleanup(srv.Close)
	return srv, &reqs
}

// writeConfig 写入可运行的 config.json（密钥走 secret 引用）。
func writeConfig(t *testing.T, dir, baseURL, name string) {
	t.Helper()
	cfg := fmt.Sprintf(`{
  "model": {"provider":"openai-compatible","name":%q,"base_url":%q,"api_key":"secret:AQ_E2E_KEY"},
  "ui": {"kind":"repl"},
  "limits": {"max_turns": 8, "max_context_tokens": 64000, "tool_output_chars": 20000, "tool_timeout_sec": 60}
}`, name, baseURL)
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(cfg), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	t.Setenv("AQ_E2E_KEY", "test-key")
}

// TestRunFirstTimeGeneratesConfig 首次运行：生成模板配置并退出（README 快速开始语义）。
func TestRunFirstTimeGeneratesConfig(t *testing.T) {
	dir := t.TempDir()
	var out, errBuf bytes.Buffer
	if code := run([]string{"-data", dir}, strings.NewReader(""), &out, &errBuf); code != 0 {
		t.Fatalf("code = %d, stderr = %s", code, errBuf.String())
	}
	if !strings.Contains(out.String(), "已生成配置") {
		t.Fatalf("stdout = %q", out.String())
	}
	data, err := os.ReadFile(filepath.Join(dir, "config.json"))
	if err != nil {
		t.Fatalf("config 未生成: %v", err)
	}
	if !strings.Contains(string(data), `"secret:AQUARIUS_OPENAI_KEY"`) {
		t.Fatalf("模板应使用 secret 引用: %s", data)
	}
}

// TestRunRejectsPlaintextKey 明文密钥与缺失环境变量都要在启动时报因（硬性规则 8）。
func TestRunRejectsPlaintextKey(t *testing.T) {
	dir := t.TempDir()
	cfg := `{"model":{"name":"m","base_url":"http://127.0.0.1:1","api_key":"sk-plain"},"ui":{"kind":"repl"}}`
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(cfg), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	var out, errBuf bytes.Buffer
	if code := run([]string{"-data", dir}, strings.NewReader(""), &out, &errBuf); code != 1 {
		t.Fatalf("code = %d, want 1", code)
	}
	if !strings.Contains(errBuf.String(), "secret:") {
		t.Fatalf("stderr = %q, want 提示 secret 引用", errBuf.String())
	}
}

func TestRunMissingSecretEnv(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, dir, "http://127.0.0.1:1", "m")
	if err := os.Unsetenv("AQ_E2E_KEY"); err != nil {
		t.Fatalf("unsetenv: %v", err)
	}
	var out, errBuf bytes.Buffer
	if code := run([]string{"-data", dir}, strings.NewReader(""), &out, &errBuf); code != 1 {
		t.Fatalf("code = %d, want 1", code)
	}
	if !strings.Contains(errBuf.String(), "AQ_E2E_KEY") {
		t.Fatalf("stderr = %q, want 指名环境变量", errBuf.String())
	}
}

// TestRunFullTextConversation M0 验收：二进制装配跑通一轮纯文本对话，
// 会话树落盘且不变量成立；再次启动恢复同一会话，历史进入上下文。
func TestRunFullTextConversation(t *testing.T) {
	srv, reqs := scriptServer(t, []string{
		"你好，我是脚本模型。",
		"好的，我们继续。",
	})
	dir := t.TempDir()
	writeConfig(t, dir, srv.URL, "script-model")

	// 第一轮：一句对话 + /quit。
	var out1 bytes.Buffer
	if code := run([]string{"-data", dir}, strings.NewReader("你好\n/quit\n"), &out1, io.Discard); code != 0 {
		t.Fatalf("run1 code = %d, out = %q", code, out1.String())
	}
	got := out1.String()
	for _, want := range []string{"> ", "你好，我是脚本模型。"} {
		if !strings.Contains(got, want) {
			t.Fatalf("run1 stdout 缺 %q: %q", want, got)
		}
	}

	// 会话树已落盘且合法。
	files, err := filepath.Glob(filepath.Join(dir, "conversations", "*.json"))
	if err != nil || len(files) != 1 {
		t.Fatalf("conversation files = %v, %v", files, err)
	}
	data, err := os.ReadFile(files[0])
	if err != nil {
		t.Fatalf("read tree: %v", err)
	}
	var c conversation.Conversation
	if err := json.Unmarshal(data, &c); err != nil {
		t.Fatalf("parse tree: %v", err)
	}
	if err := c.Validate(); err != nil {
		t.Fatalf("落盘会话破坏不变量: %v", err)
	}
	if len(c.Nodes) != 3 {
		t.Fatalf("nodes = %d, want 3（root+user+assistant；/quit 不入树）", len(c.Nodes))
	}
	if c.Title != "你好" {
		t.Fatalf("title = %q, want 首条消息摘要", c.Title)
	}
	if len(*reqs) != 1 {
		t.Fatalf("llm requests = %d, want 1", len(*reqs))
	}
	var first struct {
		Model    string `json:"model"`
		Stream   bool   `json:"stream"`
		Messages []struct {
			Role string `json:"role"`
		} `json:"messages"`
	}
	if err := json.Unmarshal((*reqs)[0].body, &first); err != nil {
		t.Fatalf("parse request: %v", err)
	}
	if first.Model != "script-model" || !first.Stream {
		t.Fatalf("request = %+v", first)
	}
	if len(first.Messages) != 2 || first.Messages[0].Role != "system" || first.Messages[1].Role != "user" {
		t.Fatalf("messages = %+v, want system+user", first.Messages)
	}

	// 第二轮：恢复同一会话，历史进上下文；/list 可见标题。
	var out2 bytes.Buffer
	if code := run([]string{"-data", dir}, strings.NewReader("继续聊\n/list\n/quit\n"), &out2, io.Discard); code != 0 {
		t.Fatalf("run2 code = %d, out = %q", code, out2.String())
	}
	if !strings.Contains(out2.String(), "好的，我们继续。") {
		t.Fatalf("run2 stdout = %q", out2.String())
	}
	if !strings.Contains(out2.String(), c.Title) {
		t.Fatalf("run2 /list 应含标题 %q: %q", c.Title, out2.String())
	}
	if len(*reqs) != 2 {
		t.Fatalf("llm requests = %d, want 2", len(*reqs))
	}
	second := string((*reqs)[1].body)
	for _, want := range []string{"你好", "继续聊", "你好，我是脚本模型。"} {
		if !strings.Contains(second, want) {
			t.Fatalf("第二次请求应携带历史 %q: %s", want, second)
		}
	}

	// 恢复后追加到同一棵树：4 节点。
	data, err = os.ReadFile(files[0])
	if err != nil {
		t.Fatalf("read tree: %v", err)
	}
	if err := json.Unmarshal(data, &c); err != nil {
		t.Fatalf("parse tree: %v", err)
	}
	if err := c.Validate(); err != nil {
		t.Fatalf("第二轮后不变量: %v", err)
	}
	if len(c.Nodes) != 5 {
		t.Fatalf("nodes = %d, want 5", len(c.Nodes))
	}
}

// TestRunRejectsTUIConfig M0 只支持 repl。
func TestRunRejectsTUIConfig(t *testing.T) {
	dir := t.TempDir()
	cfg := `{"model":{"name":"m","base_url":"http://127.0.0.1:1","api_key":"secret:X"},"ui":{"kind":"tui"}}`
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(cfg), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	t.Setenv("X", "k")
	var out, errBuf bytes.Buffer
	if code := run([]string{"-data", dir}, strings.NewReader(""), &out, &errBuf); code != 1 {
		t.Fatalf("code = %d, want 1", code)
	}
	if !strings.Contains(errBuf.String(), "tui") {
		t.Fatalf("stderr = %q", errBuf.String())
	}
}
