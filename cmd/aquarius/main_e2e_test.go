package main

// main_e2e_test.go 端到端会话：全文对话与回放、TUI 收发与确认、缺密钥时回落
// GUI、未知 UI 类型报因、删除确认、跨家降级、profile 切换重启。

import (
	"bytes"
	"encoding/json"
	"errors"
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
	files, err := filepath.Glob(filepath.Join(dir, "profiles", "default", "conversations", "*.json"))
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
	if len(c.Nodes) != 4 {
		t.Fatalf("nodes = %d, want 4（root+persona+user+assistant；/quit 不入树）", len(c.Nodes))
	}
	if c.Title != "你好" {
		t.Fatalf("title = %q, want 首条消息摘要", c.Title)
	}

	// 审计装饰器（D14/§8）：这一轮生成应落一行 llm 审计。
	auditData, err := os.ReadFile(filepath.Join(dir, "profiles", "default", "audit.log"))
	if err != nil {
		t.Fatalf("audit.log: %v", err)
	}
	var llmOK int
	for _, ln := range strings.Split(strings.TrimSpace(string(auditData)), "\n") {
		if ln == "" {
			continue
		}
		var line struct {
			Kind  string `json:"kind"`
			OK    bool   `json:"ok"`
			Model string `json:"model"`
		}
		if err := json.Unmarshal([]byte(ln), &line); err != nil {
			t.Fatalf("parse audit line %q: %v", ln, err)
		}
		if line.Kind == "llm" && line.OK && line.Model == "script-model" {
			llmOK++
		}
	}
	if llmOK != 1 {
		t.Fatalf("audit llm ok 行 = %d, want 1（内容: %s）", llmOK, auditData)
	}
	if reqs.len() != 1 {
		t.Fatalf("llm requests = %d, want 1", reqs.len())
	}
	var first struct {
		Model    string `json:"model"`
		Stream   bool   `json:"stream"`
		Messages []struct {
			Role string `json:"role"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(reqs.at(0).body, &first); err != nil {
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
	if reqs.len() != 2 {
		t.Fatalf("llm requests = %d, want 2", reqs.len())
	}
	second := string(reqs.at(1).body)
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
	if len(c.Nodes) != 6 {
		t.Fatalf("nodes = %d, want 6", len(c.Nodes))
	}
}

// TestRunTUIAcceptsKind D33：ui.kind=tui 走 bubbletea 前端——
// 管道输入完成一轮对话（流式→提交→glamour 渲染）后 /quit 干净退出。
func TestRunTUIAcceptsKind(t *testing.T) {
	srv, reqs := scriptServer(t, []string{"TUI 好的"})
	dir := t.TempDir()
	cfg := fmt.Sprintf(`{
  "model": {"providers":[{"name":"p","base_url":%q,"keys":[{"api_key":"secret:X"}],"models":[{"name":"m"}]}],"name":"m"},
  "ui": {"kind":"tui"},
  "limits": {"max_turns": 8, "max_context_tokens": 64000}
}`, srv.URL)
	writeProfileLayout(t, dir, cfg)
	t.Setenv("X", "k")

	var out bytes.Buffer
	var errBuf bytes.Buffer
	if code := run([]string{"-data", dir}, strings.NewReader("在吗\n/quit\n"), &out, &errBuf); code != 0 {
		t.Fatalf("code = %d, out = %q, err = %q", code, out.String(), errBuf.String())
	}
	got := out.String()
	t.Logf("llm requests = %d", reqs.len())
	for i, r := range reqs.all() {
		t.Logf("req[%d] = %.120s", i, r.body)
	}
	for _, want := range []string{"在吗", "TUI 好的", "模型 m", "/quit"} {
		if !strings.Contains(got, want) {
			t.Fatalf("TUI 输出缺 %q；stderr=%q", want, errBuf.String())
		}
	}
	if reqs.len() != 1 {
		t.Fatalf("llm requests = %d, want 1", reqs.len())
	}
}

// TestRunTUIConfirmE2E §12 TUI 验收线：TUI 前端完成带工具确认的完整链路——
// 脚本 LLM 发起 file_delete → [y/N] 确认对话 → 键入 y 放行 → 结果回填 → /quit。
func TestRunTUIConfirmE2E(t *testing.T) {
	dir := t.TempDir()
	victim := filepath.Join(dir, "victim.txt")
	if err := os.WriteFile(victim, []byte("bye"), 0o644); err != nil {
		t.Fatal(err)
	}
	srv, reqs := rawScriptServer(t, [][]string{
		{toolCallData("c1", "file_delete", fmt.Sprintf(`{"path":%q}`, filepath.ToSlash(victim)))},
		{contentData("已删除")},
	})
	cfg := fmt.Sprintf(`{
  "model": {"providers":[{"name":"p","base_url":%q,"keys":[{"api_key":"secret:AQ_E2E_KEY"}],"models":[{"name":"m"}]}],"name":"m"},
  "ui": {"kind":"tui"},
  "limits": {"max_turns": 8, "max_context_tokens": 64000, "tool_output_chars": 20000, "tool_timeout_sec": 60}
}`, srv.URL)
	writeProfileLayout(t, dir, cfg)
	t.Setenv("AQ_E2E_KEY", "test-key")

	var out bytes.Buffer
	stdin := "删掉 victim 文件\ny\n/quit\n"
	if code := run([]string{"-data", dir}, strings.NewReader(stdin), &out, io.Discard); code != 0 {
		t.Fatalf("code = %d, out = %q", code, out.String())
	}
	got := out.String()
	for _, want := range []string{"[y/N]", "[tool ok]", "已删除"} {
		if !strings.Contains(got, want) {
			t.Fatalf("TUI 确认链路缺 %q: %q", want, got)
		}
	}
	if _, err := os.Stat(victim); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("y 确认后文件应被删除: %v", err)
	}
	if reqs.len() != 2 {
		t.Fatalf("llm requests = %d, want 2（工具轮 + 回答轮）", reqs.len())
	}
}

// TestRunDefaultsToGUIWhenKeyMissing ui 键缺失与模板同默认 gui（D51；D33 旧默认 tui
// 随单二进制交付目标改判）。只测回落口径、不起 run——GUI 路径会真开窗，测试禁开真窗。
func TestRunDefaultsToGUIWhenKeyMissing(t *testing.T) {
	if got := uiKindDefault(""); got != "gui" {
		t.Fatalf("缺 ui 键应默认 gui: %q", got)
	}
	for _, k := range []string{"tui", "repl", "gui"} {
		if got := uiKindDefault(k); got != k {
			t.Fatalf("显式 ui.kind 不改写: %q → %q", k, got)
		}
	}
}

// TestRunRejectsUnknownUIKind 非法 ui.kind 启动即报因（D33/D43 仅 repl | tui | gui）。
func TestRunRejectsUnknownUIKind(t *testing.T) {
	dir := t.TempDir()
	cfg := `{"model":{"providers":[{"name":"p","base_url":"http://127.0.0.1:1","keys":[{"api_key":"secret:X"}],"models":[{"name":"m"}]}],"name":"m"},"ui":{"kind":"web"}}`
	writeProfileLayout(t, dir, cfg)
	t.Setenv("X", "k")
	var out, errBuf bytes.Buffer
	if code := run([]string{"-data", dir}, strings.NewReader(""), &out, &errBuf); code != 1 {
		t.Fatalf("code = %d, want 1", code)
	}
	if !strings.Contains(errBuf.String(), "web") {
		t.Fatalf("stderr = %q", errBuf.String())
	}
}

// TestRunRmConfirmE2E /rm 二次确认端到端（M1）：REPL 确认读行——
// 拒绝则保留、同意则剪枝落盘、-yes 跳过确认；全程会话树满足不变量。
func TestRunRmConfirmE2E(t *testing.T) {
	srv, reqs := scriptServer(t, []string{"收到一。", "收到二。"})
	dir := t.TempDir()
	writeConfig(t, dir, srv.URL, "m")

	load := func() *conversation.Conversation {
		t.Helper()
		files, err := filepath.Glob(filepath.Join(dir, "profiles", "default", "conversations", "*.json"))
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
		return &c
	}
	runStdin := func(args []string, stdin string) string {
		t.Helper()
		var out, errBuf bytes.Buffer
		if code := run(append([]string{"-data", dir}, args...), strings.NewReader(stdin), &out, &errBuf); code != 0 {
			t.Fatalf("code = %d, want 0; stderr = %q; stdout = %q", code, errBuf.String(), out.String())
		}
		return out.String()
	}

	// 建树：两轮对话 → root, persona, u1, a1, u2, a2。
	runStdin(nil, "hi\nagain\n/quit\n")
	c := load()
	if len(c.Nodes) != 6 {
		t.Fatalf("nodes = %d, want 6", len(c.Nodes))
	}
	path := c.Path()
	u1, a1, u2 := path[2].ID, path[3].ID, path[4].ID

	// 拒绝：确认提示打印在 stdout，回答 n → 节点保留。
	got := runStdin(nil, fmt.Sprintf("/rm %s\nn\n/quit\n", u2))
	for _, want := range []string{"确认删除", "[y/N]", "已取消删除"} {
		if !strings.Contains(got, want) {
			t.Fatalf("stdout 缺 %q: %q", want, got)
		}
	}
	if c = load(); len(c.Nodes) != 6 {
		t.Fatalf("拒绝后 nodes = %d, want 6", len(c.Nodes))
	}

	// 同意：剪掉 {u2, a2}，Head 回退到最近存活祖先 a1，落盘合法。
	got = runStdin(nil, fmt.Sprintf("/rm %s\ny\n/quit\n", u2))
	if !strings.Contains(got, "已删除") {
		t.Fatalf("stdout = %q, want 已删除", got)
	}
	if c = load(); len(c.Nodes) != 4 || c.Head != a1 {
		t.Fatalf("同意后 nodes/head = %d/%s, want 4/%s", len(c.Nodes), c.Head, a1)
	}

	// -yes：跳过确认直接删。
	got = runStdin([]string{"-yes"}, fmt.Sprintf("/rm %s\n/quit\n", u1))
	if strings.Contains(got, "[y/N]") {
		t.Fatalf("-yes 不应出现确认提示: %q", got)
	}
	if !strings.Contains(got, "已删除") {
		t.Fatalf("stdout = %q, want 已删除", got)
	}
	if c = load(); len(c.Nodes) != 2 {
		t.Fatalf("-yes 后 nodes = %d, want 2（root+persona）", len(c.Nodes))
	}
	// 只有首轮对话花生成请求（其余均为命令）。
	if reqs.len() != 2 {
		t.Fatalf("llm requests = %d, want 2", reqs.len())
	}
}

// ---------------------------------------------------------------------------
// M2：记忆工具、确认拦截、权限等级生效（DESIGN §12 M2 验收）
// ---------------------------------------------------------------------------

// TestRunFallbackDegradesE2E D110②/Q5 全链路：primary（bad）持续 5xx → 内层重试
// 耗尽 → 降级到 fallback（good）成功；降级请求改写为其 models[0]，状态行出现一次性
// 降级提示（NoticeEvent → repl 转写）。
func TestRunFallbackDegradesE2E(t *testing.T) {
	var badHits int
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		badHits++
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(bad.Close)
	good, reqs := scriptServer(t, []string{"降级兜底"})
	dir := t.TempDir()
	cfg := fmt.Sprintf(`{
  "model": {"providers":[
      {"name":"bad","base_url":%q,"keys":[{"api_key":"secret:X"}],"models":[{"name":"bm"}]},
      {"name":"good","base_url":%q,"keys":[{"api_key":"secret:X"}],"models":[{"name":"gm"}]}],
    "primary":"bad","fallback":[{"provider":"good"}],"name":"bm"},
  "ui": {"kind":"repl"},
  "limits": {"max_turns": 8, "max_context_tokens": 64000, "tool_output_chars": 20000, "tool_timeout_sec": 60}
}`, bad.URL, good.URL)
	writeProfileLayout(t, dir, cfg)
	t.Setenv("X", "k")

	var out bytes.Buffer
	if code := run([]string{"-data", dir}, strings.NewReader("hi\n/quit\n"), &out, io.Discard); code != 0 {
		t.Fatalf("code = %d, out = %q", code, out.String())
	}
	got := out.String()
	if !strings.Contains(got, "已降级到") || !strings.Contains(got, "good") {
		t.Fatalf("stdout 缺降级提示: %q", got)
	}
	if !strings.Contains(got, "降级兜底") {
		t.Fatalf("降级后应成功生成: %q", got)
	}
	if badHits != 3 {
		t.Fatalf("bad 请求 = %d, want 3（同 provider 重试 3 次在内）", badHits)
	}
	if reqs.len() != 1 {
		t.Fatalf("good 请求 = %d, want 1（降级后成功）", reqs.len())
	}
	if m := string(reqs.at(0).body); !strings.Contains(m, `"model":"gm"`) {
		t.Fatalf("降级请求模型应改写为 good 的 models[0]: %.200s", m)
	}
}

// TestRunProfileSwitchRestartE2E D110③ 切换全链路（GUI 受阻，headless 等价验收）：
// default 起步 → 改写根指针（等价设置窗「切换」）→ 重启 = 换整目录——会话落新
// profile、请求打新 provider 的端点并用其模型名，旧 profile 数据不动。
func TestRunProfileSwitchRestartE2E(t *testing.T) {
	srvA, reqsA := scriptServer(t, []string{"A 的回答"})
	srvB, reqsB := scriptServer(t, []string{"B 的回答"})
	dir := t.TempDir()
	t.Setenv("X", "k")

	// 两个 profile 各自完整配置（A=默认起步，B=切换目标）。
	writeProfileLayout(t, dir, fmt.Sprintf(`{
  "model": {"providers":[{"name":"pa","base_url":%q,"keys":[{"api_key":"secret:X"}],"models":[{"name":"mA"}]}],"name":"mA"},
  "ui": {"kind":"repl"},
  "limits": {"max_turns": 8, "max_context_tokens": 64000, "tool_output_chars": 20000, "tool_timeout_sec": 60}
}`, srvA.URL))
	work := filepath.Join(dir, "profiles", "work")
	if err := os.MkdirAll(work, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(work, "config.json"), []byte(fmt.Sprintf(`{
  "model": {"providers":[{"name":"pb","base_url":%q,"keys":[{"api_key":"secret:X"}],"models":[{"name":"mB"}]}],"name":"mB"},
  "ui": {"kind":"repl"},
  "limits": {"max_turns": 8, "max_context_tokens": 64000, "tool_output_chars": 20000, "tool_timeout_sec": 60}
}`, srvB.URL)), 0o644); err != nil {
		t.Fatal(err)
	}

	// 第一段：跑在 default。
	var out bytes.Buffer
	if code := run([]string{"-data", dir}, strings.NewReader("hi\n/quit\n"), &out, io.Discard); code != 0 {
		t.Fatalf("run1 code = %d, out = %q", code, out.String())
	}
	if files, _ := filepath.Glob(filepath.Join(dir, "profiles", "default", "conversations", "*.json")); len(files) != 1 {
		t.Fatalf("default conversations = %v, want 1", files)
	}

	// 切换 = 原子写根指针（profilesManager.Switch 同口径）。
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(pointerContent("work")), 0o644); err != nil {
		t.Fatal(err)
	}

	// 第二段：重启生效——跑在 work，数据隔离、请求打 B 端点用 mB。
	out.Reset()
	if code := run([]string{"-data", dir}, strings.NewReader("hello\n/quit\n"), &out, io.Discard); code != 0 {
		t.Fatalf("run2 code = %d, out = %q", code, out.String())
	}
	if files, _ := filepath.Glob(filepath.Join(dir, "profiles", "work", "conversations", "*.json")); len(files) != 1 {
		t.Fatalf("work conversations = %v, want 1（换整目录）", files)
	}
	if files, _ := filepath.Glob(filepath.Join(dir, "profiles", "default", "conversations", "*.json")); len(files) != 1 {
		t.Fatalf("default conversations = %v, want 仍为 1（不受切换影响）", files)
	}
	if reqsA.len() != 1 {
		t.Fatalf("A 端点请求 = %d, want 1（仅第一段）", reqsA.len())
	}
	if reqsB.len() != 1 {
		t.Fatalf("B 端点请求 = %d, want 1（仅第二段）", reqsB.len())
	}
	if m := string(reqsB.at(0).body); !strings.Contains(m, `"model":"mB"`) {
		t.Fatalf("重启后应使用 work 的模型 mB: %.200s", m)
	}
	if !strings.Contains(out.String(), "B 的回答") {
		t.Fatalf("重启后应由 B 端点回答: %q", out.String())
	}
}
