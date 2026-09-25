package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/Tonyjh07/Aquarius/internal/adapter/blobfs"
	"github.com/Tonyjh07/Aquarius/internal/adapter/memoryfs"
	"github.com/Tonyjh07/Aquarius/internal/adapter/storejson"
	"github.com/Tonyjh07/Aquarius/internal/domain/conversation"
	"github.com/Tonyjh07/Aquarius/internal/port"
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
	if !strings.Contains(string(data), `"level": "strict"`) {
		t.Fatalf("模板应含权限等级默认值: %s", data)
	}
	if !strings.Contains(string(data), `"system_prompt"`) {
		t.Fatalf("模板应含 system_prompt 键: %s", data)
	}
	if fi, err := os.Stat(filepath.Join(dir, "sandbox")); err != nil || !fi.IsDir() {
		t.Fatalf("特权目录应自动创建: %v", err)
	}
}

// TestRunPermissionSwitchWritesBackConfig /permission 切换写回：其余配置键与数值类型不丢、无半截文件残留（D22）。
func TestRunPermissionSwitchWritesBackConfig(t *testing.T) {
	dir := t.TempDir()
	cfg := `{
  "model": {"name":"m","base_url":"http://127.0.0.1:1","api_key":"secret:X"},
  "ui": {"kind":"repl"},
  "system_prompt": "",
  "memory": {"dir": "~/.aquarius/memory"},
  "permissions": {"level": "strict"},
  "limits": {"max_turns": 8, "max_context_tokens": 64000, "compact_threshold": 0.7}
}`
	cfgPath := filepath.Join(dir, "config.json")
	if err := os.WriteFile(cfgPath, []byte(cfg), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	t.Setenv("X", "k")

	var out, errBuf bytes.Buffer
	code := run([]string{"-data", dir}, strings.NewReader("/permission permissive\n/quit\n"), &out, &errBuf)
	if code != 0 {
		t.Fatalf("code = %d, err = %s", code, errBuf.String())
	}
	if !strings.Contains(out.String(), "permissive") {
		t.Fatalf("stdout = %q", out.String())
	}

	data, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("read back config: %v", err)
	}
	var back map[string]any
	if err := json.Unmarshal(data, &back); err != nil {
		t.Fatalf("config 不是合法 JSON: %v\n%s", err, data)
	}
	perms, _ := back["permissions"].(map[string]any)
	if perms["level"] != "permissive" {
		t.Fatalf("level = %v, want permissive", perms["level"])
	}
	mem, _ := back["memory"].(map[string]any)
	if mem["dir"] != "~/.aquarius/memory" {
		t.Fatalf("memory 键丢失: %v", back["memory"])
	}
	limits, _ := back["limits"].(map[string]any)
	if limits["max_turns"] != float64(8) || limits["compact_threshold"] != 0.7 {
		t.Fatalf("limits 数值类型漂移: %v", limits)
	}
	if m, _ := filepath.Glob(cfgPath + ".*"); len(m) > 0 {
		t.Fatalf("不应残留临时文件: %v", m)
	}
}

// TestRunModelSwitchWritesBackConfig /model <name> 写回 model.name 并保留其余键（D32，同 /permission）。
func TestRunModelSwitchWritesBackConfig(t *testing.T) {
	dir := t.TempDir()
	cfg := `{
  "model": {"provider":"openai-compatible","name":"old-model","base_url":"http://127.0.0.1:1","api_key":"secret:X","tokenizer":""},
  "ui": {"kind":"repl"},
  "system_prompt": "人格不动",
  "permissions": {"level": "strict"},
  "limits": {"max_turns": 8, "max_context_tokens": 64000}
}`
	cfgPath := filepath.Join(dir, "config.json")
	if err := os.WriteFile(cfgPath, []byte(cfg), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	t.Setenv("X", "k")

	var out, errBuf bytes.Buffer
	code := run([]string{"-data", dir}, strings.NewReader("/model new-model\n/quit\n"), &out, &errBuf)
	if code != 0 {
		t.Fatalf("code = %d, err = %s", code, errBuf.String())
	}
	if !strings.Contains(out.String(), "已切换模型 → new-model") {
		t.Fatalf("stdout = %q", out.String())
	}

	data, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("read back config: %v", err)
	}
	var back map[string]any
	if err := json.Unmarshal(data, &back); err != nil {
		t.Fatalf("config 不是合法 JSON: %v\n%s", err, data)
	}
	model, _ := back["model"].(map[string]any)
	if model["name"] != "new-model" {
		t.Fatalf("model.name = %v, want new-model", model["name"])
	}
	if back["system_prompt"] != "人格不动" {
		t.Fatalf("system_prompt 键丢失: %v", back["system_prompt"])
	}
	perms, _ := back["permissions"].(map[string]any)
	if perms["level"] != "strict" {
		t.Fatalf("permissions 键丢失: %v", back["permissions"])
	}
}

// TestRunRejectsInvalidPermissionLevel 非法 permissions.level 启动即报因。
func TestRunRejectsInvalidPermissionLevel(t *testing.T) {
	dir := t.TempDir()
	cfg := `{"model":{"name":"m","base_url":"http://127.0.0.1:1","api_key":"secret:X"},"ui":{"kind":"repl"},"permissions":{"level":"root"}}`
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(cfg), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	t.Setenv("X", "k")
	var out, errBuf bytes.Buffer
	if code := run([]string{"-data", dir}, strings.NewReader(""), &out, &errBuf); code != 1 {
		t.Fatalf("code = %d, want 1", code)
	}
	if !strings.Contains(errBuf.String(), "未知权限等级") {
		t.Fatalf("stderr = %q", errBuf.String())
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

// TestRunAttachmentGC 启动附件 GC：按全量会话引用保活、清扫孤儿；
// 引用收集失败（store 报错）时跳过清扫并告警——宁可漏清不误删。
func TestRunAttachmentGC(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	store, err := storejson.New(filepath.Join(dir, "conversations"))
	if err != nil {
		t.Fatalf("storejson: %v", err)
	}
	blobs, err := blobfs.New(filepath.Join(dir, "attachments"))
	if err != nil {
		t.Fatalf("blobfs: %v", err)
	}
	// 会话带一个图片引用。
	c := conversation.New("conv1", "t")
	ref, err := blobs.Put(ctx, bytes.NewReader([]byte("live")), "image/png", "live.png")
	if err != nil {
		t.Fatalf("put live: %v", err)
	}
	if err := c.AppendCommitted(conversation.Message{
		ID: "m1", Parent: conversation.MessageID("conv1"), Role: conversation.RoleSystem,
		Content:   []conversation.Part{{Kind: conversation.PartImage, Ref: &ref}},
		CreatedAt: time.Now(),
	}); err != nil {
		t.Fatalf("append: %v", err)
	}
	if err := store.Save(ctx, c); err != nil {
		t.Fatalf("save: %v", err)
	}
	orphan, err := blobs.Put(ctx, bytes.NewReader([]byte("dead")), "image/png", "dead.png")
	if err != nil {
		t.Fatalf("put orphan: %v", err)
	}

	keep, err := collectBlobRefs(ctx, store)
	if err != nil || !keep[ref.Hash] {
		t.Fatalf("keep = %v, err = %v", keep, err)
	}
	if keep[orphan.Hash] {
		t.Fatal("孤儿不应进 keep")
	}
	var warn bytes.Buffer
	runAttachmentGC(ctx, store, blobs, &warn)
	if ok, _ := blobs.Stat(ctx, ref); !ok {
		t.Fatal("被引用附件不应被清")
	}
	if ok, _ := blobs.Stat(ctx, orphan); ok {
		t.Fatal("孤儿附件应被清")
	}

	// 收集失败 → 跳过清扫并告警。
	orphan2, err := blobs.Put(ctx, bytes.NewReader([]byte("dead2")), "image/png", "dead2.png")
	if err != nil {
		t.Fatalf("put orphan2: %v", err)
	}
	warn.Reset()
	runAttachmentGC(ctx, failingStore{}, blobs, &warn)
	if !strings.Contains(warn.String(), "已跳过") {
		t.Fatalf("warn = %q, want 已跳过", warn.String())
	}
	if ok, _ := blobs.Stat(ctx, orphan2); !ok {
		t.Fatal("收集失败时不应清扫")
	}
}

// TestRunAttachmentGCQuietOnCancel 启动期 ctx 取消：跳过清扫且不告警（干净收尾，
// ctx 提前到装配段创建后 Ctrl+C 不产生误导性错误输出）。
func TestRunAttachmentGCQuietOnCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	blobs, err := blobfs.New(t.TempDir())
	if err != nil {
		t.Fatalf("blobfs: %v", err)
	}
	var warn bytes.Buffer
	runAttachmentGC(ctx, failingStore{}, blobs, &warn)
	if warn.Len() != 0 {
		t.Fatalf("取消不应告警: %q", warn.String())
	}
}

// failingStore 只让 List 报错的 ConversationStore 替身。
type failingStore struct{ port.ConversationStore }

func (failingStore) List(context.Context) ([]port.ConversationSummary, error) {
	return nil, errors.New("boom")
}

// TestLoadConfigOutput 输出器开关解析：键缺失 = false（保守），显式 true 生效。
func TestLoadConfigOutput(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "config.json")
	if err := os.WriteFile(p, []byte(`{"model":{"name":"m"}}`), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	cfg, err := loadConfig(p)
	if err != nil || cfg.Output.Notify {
		t.Fatalf("缺键 = %+v, err = %v", cfg.Output, err)
	}
	if err := os.WriteFile(p, []byte(`{"output":{"notify":true,"tts":false}}`), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	cfg, err = loadConfig(p)
	if err != nil || !cfg.Output.Notify || cfg.Output.TTS {
		t.Fatalf("output = %+v, err = %v", cfg.Output, err)
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
	if len(c.Nodes) != 4 {
		t.Fatalf("nodes = %d, want 4（root+persona+user+assistant；/quit 不入树）", len(c.Nodes))
	}
	if c.Title != "你好" {
		t.Fatalf("title = %q, want 首条消息摘要", c.Title)
	}

	// 审计装饰器（D14/§8）：这一轮生成应落一行 llm 审计。
	auditData, err := os.ReadFile(filepath.Join(dir, "audit.log"))
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
	if len(c.Nodes) != 6 {
		t.Fatalf("nodes = %d, want 6", len(c.Nodes))
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

// TestRunRmConfirmE2E /rm 二次确认端到端（M1）：REPL 确认读行——
// 拒绝则保留、同意则剪枝落盘、-yes 跳过确认；全程会话树满足不变量。
func TestRunRmConfirmE2E(t *testing.T) {
	srv, reqs := scriptServer(t, []string{"收到一。", "收到二。"})
	dir := t.TempDir()
	writeConfig(t, dir, srv.URL, "m")

	load := func() *conversation.Conversation {
		t.Helper()
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
	if len(*reqs) != 2 {
		t.Fatalf("llm requests = %d, want 2", len(*reqs))
	}
}

// ---------------------------------------------------------------------------
// M2：记忆工具、确认拦截、权限等级生效（DESIGN §12 M2 验收）
// ---------------------------------------------------------------------------

// rawScriptServer 按序回话的 SSE 服务：第 i 次请求逐行回放 dataLines[i]（可含 tool_calls）。
func rawScriptServer(t *testing.T, dataLines [][]string) (*httptest.Server, *[]recordedReq) {
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
		if i >= len(dataLines) {
			t.Errorf("第 %d 次请求超出脚本", i+1)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		for _, line := range dataLines[i] {
			fmt.Fprint(w, line+"\n")
		}
		fmt.Fprint(w, "data: [DONE]\n")
	}))
	t.Cleanup(srv.Close)
	return srv, &reqs
}

// contentData 纯文本回复的 data 行。
func contentData(text string) string {
	b, _ := json.Marshal(map[string]any{
		"choices": []map[string]any{{"delta": map[string]any{"content": text}}},
	})
	return "data: " + string(b)
}

// toolCallData 工具调用回复的 data 行（单分片完整参数）。
func toolCallData(id, name, args string) string {
	b, _ := json.Marshal(map[string]any{
		"choices": []map[string]any{{"delta": map[string]any{
			"tool_calls": []map[string]any{{
				"index": 0, "id": id, "type": "function",
				"function": map[string]any{"name": name, "arguments": args},
			}},
		}}},
	})
	return "data: " + string(b)
}

// TestPickEditor 编辑器选择（D24）：$VISUAL → $EDITOR → 平台默认。
func TestPickEditor(t *testing.T) {
	cases := []struct {
		name                 string
		visual, editor, goos string
		want                 []string
	}{
		{"VISUAL 优先且可带参数", "code -w", "vi", "windows", []string{"code", "-w"}},
		{"EDITOR 兜底", "  ", "emacs -nw", "linux", []string{"emacs", "-nw"}},
		{"windows 默认记事本", "", "", "windows", []string{"notepad"}},
		{"unix 默认 vi", "", "", "linux", []string{"vi"}},
		{"空白 VISUAL 落到 EDITOR", "   ", "nano", "darwin", []string{"nano"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := pickEditor(tc.visual, tc.editor, tc.goos)
			if strings.Join(got, " ") != strings.Join(tc.want, " ") {
				t.Fatalf("pickEditor = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestOpenMemoryEditorCreatesFile 打开前确保文件存在（全局/会话路径，D23 布局）；
// 编辑器启动失败报因但文件已建；非法文档名拒绝。
func TestOpenMemoryEditorCreatesFile(t *testing.T) {
	dir := t.TempDir()
	mem, err := memoryfs.New(filepath.Join(dir, port.GlobalMemoryDoc), filepath.Join(dir, "conversations"))
	if err != nil {
		t.Fatalf("memoryfs: %v", err)
	}
	t.Setenv("VISUAL", "")
	t.Setenv("EDITOR", "aquarius-no-such-editor-xyz") // 必失败的编辑器：断言文件创建与报错
	open := openMemoryEditor(mem, strings.NewReader(""), io.Discard, io.Discard)

	if _, err := open(port.GlobalMemoryDoc); err == nil || !strings.Contains(err.Error(), "运行编辑器") {
		t.Fatalf("err = %v, want 运行编辑器失败", err)
	}
	if _, serr := os.Stat(filepath.Join(dir, port.GlobalMemoryDoc)); serr != nil {
		t.Fatalf("全局记忆文件应已创建: %v", serr)
	}

	sname := port.SessionMemoryDoc("convX")
	if _, err := open(sname); err == nil {
		t.Fatal("会话记忆也应报编辑器失败")
	}
	if _, serr := os.Stat(filepath.Join(dir, "conversations", sname)); serr != nil {
		t.Fatalf("会话记忆文件应已创建: %v", serr)
	}

	if _, err := open("../evil.md"); err == nil {
		t.Fatal("非法文档名应报错")
	}
}

// writeConfigLevel 带指定权限等级的 config。
func writeConfigLevel(t *testing.T, dir, baseURL, name, level string) {
	t.Helper()
	cfg := fmt.Sprintf(`{
  "model": {"provider":"openai-compatible","name":%q,"base_url":%q,"api_key":"secret:AQ_E2E_KEY"},
  "ui": {"kind":"repl"},
  "permissions": {"level": %q},
  "limits": {"max_turns": 8, "max_context_tokens": 64000, "tool_output_chars": 20000, "tool_timeout_sec": 60}
}`, name, baseURL, level)
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(cfg), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	t.Setenv("AQ_E2E_KEY", "test-key")
}

// loadTree 读取唯一会话树并做不变量自检。
func loadTree(t *testing.T, dir string) *conversation.Conversation {
	t.Helper()
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
	return &c
}

// findToolNode 找到首个 tool 角色节点。
func findToolNode(c *conversation.Conversation) (conversation.Message, bool) {
	for _, m := range c.Nodes {
		if m.Role == conversation.RoleTool {
			return m, true
		}
	}
	return conversation.Message{}, false
}

// TestRunMemoryWriteConfirmE2E M2 验收：模型经 memory_write 写记忆；
// Confirm 拦截（strict 答 y 落盘 / 答 n 拒绝不写）；permissive 等级矩阵免确认（矩阵生效）。
func TestRunMemoryWriteConfirmE2E(t *testing.T) {
	toolArgs := `{"name":"memories.md","content":"喜欢绿茶","mode":"overwrite"}`

	t.Run("strict 同意后落盘", func(t *testing.T) {
		srv, reqs := rawScriptServer(t, [][]string{
			{toolCallData("call_mw", "memory_write", toolArgs)},
			{contentData("记下了。")},
		})
		dir := t.TempDir()
		writeConfig(t, dir, srv.URL, "m")

		var out bytes.Buffer
		if code := run([]string{"-data", dir},
			strings.NewReader("记住我喜欢绿茶\ny\n/quit\n"), &out, io.Discard); code != 0 {
			t.Fatalf("code = %d, out = %q", code, out.String())
		}
		got := out.String()
		for _, want := range []string{"[y/N]", "memory_write", "[tool ok]", "记下了。"} {
			if !strings.Contains(got, want) {
				t.Fatalf("stdout 缺 %q: %q", want, got)
			}
		}
		data, err := os.ReadFile(filepath.Join(dir, port.GlobalMemoryDoc))
		if err != nil {
			t.Fatalf("记忆文件未落盘: %v", err)
		}
		if !strings.Contains(string(data), "喜欢绿茶") {
			t.Fatalf("memory = %q", data)
		}
		// 第二次请求带上 tool 结果；首次请求带 tools 声明。
		if !strings.Contains(string((*reqs)[0].body), `"tools"`) {
			t.Fatal("首次请求应带工具声明")
		}
		if !strings.Contains(string((*reqs)[1].body), "已写入") {
			t.Fatalf("第二次请求应带工具结果: %.300s", (*reqs)[1].body)
		}
		c := loadTree(t, dir)
		tn, ok := findToolNode(c)
		if !ok || !tn.ToolResult.OK {
			t.Fatalf("tool 节点 = %+v", tn)
		}
	})

	t.Run("strict 拒绝不落盘", func(t *testing.T) {
		srv, _ := rawScriptServer(t, [][]string{
			{toolCallData("call_mw", "memory_write", toolArgs)},
			{contentData("好的，不写。")},
		})
		dir := t.TempDir()
		writeConfig(t, dir, srv.URL, "m")

		var out bytes.Buffer
		if code := run([]string{"-data", dir},
			strings.NewReader("别记了\nn\n/quit\n"), &out, io.Discard); code != 0 {
			t.Fatalf("code = %d, out = %q", code, out.String())
		}
		got := out.String()
		if !strings.Contains(got, "[y/N]") || !strings.Contains(got, "用户拒绝") {
			t.Fatalf("stdout 缺拒绝痕迹: %q", got)
		}
		if _, err := os.Stat(filepath.Join(dir, port.GlobalMemoryDoc)); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("拒绝后记忆文件不应存在: %v", err)
		}
		c := loadTree(t, dir)
		tn, ok := findToolNode(c)
		if !ok || tn.ToolResult.OK || !strings.Contains(tn.ToolResult.Err, "用户拒绝") {
			t.Fatalf("tool 节点 = %+v", tn)
		}
	})

	t.Run("permissive 矩阵免确认", func(t *testing.T) {
		srv, _ := rawScriptServer(t, [][]string{
			{toolCallData("call_mw", "memory_write", toolArgs)},
			{contentData("已记录。")},
		})
		dir := t.TempDir()
		writeConfigLevel(t, dir, srv.URL, "m", "permissive")

		var out bytes.Buffer
		// 无 y/n 应答行：确认若被触发会吞掉 /quit 并卡在等输入。
		if code := run([]string{"-data", dir},
			strings.NewReader("记住我喜欢绿茶\n/quit\n"), &out, io.Discard); code != 0 {
			t.Fatalf("code = %d, out = %q", code, out.String())
		}
		got := out.String()
		if strings.Contains(got, "[y/N]") {
			t.Fatalf("permissive 不应确认: %q", got)
		}
		if !strings.Contains(got, "[tool ok]") {
			t.Fatalf("工具应直接执行: %q", got)
		}
		if _, err := os.Stat(filepath.Join(dir, port.GlobalMemoryDoc)); err != nil {
			t.Fatalf("记忆文件应落盘: %v", err)
		}
	})
}

// TestEditorHelperProcess 假编辑器（helper 进程模式）：EDITOR 指向本测试二进制时，
// 向命令行末尾的路径写入标记，证明编辑器收到了正确目标（P1-4 e2e 用）。
func TestEditorHelperProcess(t *testing.T) {
	if os.Getenv("AQUARIUS_EDITOR_HELPER") != "1" {
		t.Skip("非 helper 进程运行")
	}
	args := flag.Args() // -test.run 之后的尾随参数 = 记忆文件路径
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "helper: 缺少路径参数")
		os.Exit(2)
	}
	if err := os.WriteFile(args[len(args)-1], []byte("edited-by-helper"), 0o644); err != nil {
		fmt.Fprintln(os.Stderr, "helper:", err)
		os.Exit(2)
	}
	os.Exit(0)
}

// ---------------------------------------------------------------------------
// M3：term_exec / 后台任务（DESIGN §12 M3 验收）
// ---------------------------------------------------------------------------

// jobEchoSpec 平台安全的 echo 后台任务参数（job_start 的 command/args）。
func jobEchoSpec(t *testing.T) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		return `{"command":"cmd","args":["/c","echo","job-e2e"]}`
	}
	return `{"command":"/bin/sh","args":["-c","echo job-e2e"]}`
}

// TestRunTermExecE2E M3 验收①：term_exec 经确认链路执行，
// 输出回填给模型（第二次请求可见）、tool 节点 OK 落树。
// 命令带引号（Windows 回归：cmd /c 的原始命令行不得被 argv 转义破坏）。
func TestRunTermExecE2E(t *testing.T) {
	srv, reqs := rawScriptServer(t, [][]string{
		{toolCallData("call_te", "term_exec", `{"command":"echo \"quoted e2e\""}`)},
		{contentData("执行完成")},
	})
	dir := t.TempDir()
	writeConfig(t, dir, srv.URL, "m")

	var out bytes.Buffer
	if code := run([]string{"-data", dir}, strings.NewReader("跑一下 echo\ny\n/quit\n"), &out, io.Discard); code != 0 {
		t.Fatalf("code = %d, out = %q", code, out.String())
	}
	got := out.String()
	for _, want := range []string{"[y/N]", "term_exec", "[tool ok]", "执行完成"} {
		if !strings.Contains(got, want) {
			t.Fatalf("stdout 缺 %q: %q", want, got)
		}
	}
	// 引号语义（只截结果段；工具参数 JSON 预览本身含 \" 转义，不算破坏）：
	// sh -c 吃掉引号（quoted e2e）；cmd /c 保留引号且结果段不出现 \" 残留。
	idx := strings.Index(got, "[tool ok]")
	if idx < 0 {
		t.Fatalf("stdout 缺 [tool ok]: %q", got)
	}
	okSeg := got[idx:]
	if nl := strings.IndexAny(okSeg, "\r\n"); nl >= 0 {
		okSeg = okSeg[:nl]
	}
	if runtime.GOOS == "windows" {
		if !strings.Contains(okSeg, `"quoted e2e"`) || strings.Contains(okSeg, `\"`) {
			t.Fatalf("cmd /c 引号被 argv 转义破坏: %q", okSeg)
		}
	} else if !strings.Contains(okSeg, "quoted e2e") {
		t.Fatalf("结果段缺命令输出: %q", okSeg)
	}
	if len(*reqs) != 2 {
		t.Fatalf("llm requests = %d, want 2", len(*reqs))
	}
	if !strings.Contains(string((*reqs)[1].body), "quoted e2e") {
		t.Fatalf("第二次请求应回填命令输出: %.400s", (*reqs)[1].body)
	}
	c := loadTree(t, dir)
	tn, ok := findToolNode(c)
	if !ok || !tn.ToolResult.OK {
		t.Fatalf("tool 节点 = %+v", tn)
	}
}

// TestRunTermExecRejectE2E strict 下拒绝确认：不执行、OK=false 回填（工具列 D22）。
func TestRunTermExecRejectE2E(t *testing.T) {
	srv, _ := rawScriptServer(t, [][]string{
		{toolCallData("call_te", "term_exec", `{"command":"echo should-not-run"}`)},
		{contentData("好的，不执行。")},
	})
	dir := t.TempDir()
	writeConfig(t, dir, srv.URL, "m")

	var out bytes.Buffer
	if code := run([]string{"-data", dir}, strings.NewReader("跑一下\nn\n/quit\n"), &out, io.Discard); code != 0 {
		t.Fatalf("code = %d, out = %q", code, out.String())
	}
	if !strings.Contains(out.String(), "用户拒绝") {
		t.Fatalf("stdout 缺拒绝痕迹: %q", out.String())
	}
	c := loadTree(t, dir)
	tn, ok := findToolNode(c)
	if !ok || tn.ToolResult.OK || !strings.Contains(tn.ToolResult.Err, "用户拒绝") {
		t.Fatalf("tool 节点 = %+v", tn)
	}
}

// TestRunJobLifecycleE2E M3 验收②：job_start 后台启动（经确认），
// `/jobs` 列表可见、`/jobs logs` 日志可查（日志落盘 ~/.aquarius/jobs/<id>.log）。
func TestRunJobLifecycleE2E(t *testing.T) {
	srv, reqs := rawScriptServer(t, [][]string{
		{toolCallData("call_js", "job_start", jobEchoSpec(t))},
		{contentData("已启动。")},
	})
	dir := t.TempDir()
	writeConfig(t, dir, srv.URL, "m")

	var out bytes.Buffer
	stdin := "启动后台任务\ny\n/jobs\n/jobs logs j001\n/quit\n"
	if code := run([]string{"-data", dir}, strings.NewReader(stdin), &out, io.Discard); code != 0 {
		t.Fatalf("code = %d, out = %q", code, out.String())
	}
	got := out.String()
	for _, want := range []string{"[y/N]", "job_start", "已启动。", "j001"} {
		if !strings.Contains(got, want) {
			t.Fatalf("stdout 缺 %q: %q", want, got)
		}
	}
	if !strings.Contains(got, "job-e2e") {
		t.Fatalf("日志应回显任务输出: %q", got)
	}
	if len(*reqs) != 2 {
		t.Fatalf("llm requests = %d, want 2", len(*reqs))
	}
	// 任务日志文件落盘（jobs/<id>.log）。
	logPath := filepath.Join(dir, "jobs", "j001.log")
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("读日志文件: %v", err)
	}
	if !strings.Contains(string(data), "job-e2e") {
		t.Fatalf("日志文件 = %q", data)
	}
	// 会话树里 tool 节点 OK（启动成功回填）。
	c := loadTree(t, dir)
	tn, ok := findToolNode(c)
	if !ok || !tn.ToolResult.OK || !strings.Contains(tn.ToolResult.Output, "j001") {
		t.Fatalf("tool 节点 = %+v", tn)
	}
}

// longSleepSpec 平台可靠的长睡眠任务（约 30s；供 kill 测试启动）。
func longSleepSpec(t *testing.T) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		// ping 计时在无交互控制台下可靠等待（timeout 会因 stdin 重定向报错）。
		return `{"command":"ping","args":["-n","30","127.0.0.1"]}`
	}
	return `{"command":"sleep","args":["30"]}`
}

// TestRunJobKillE2E M3：`/jobs kill` 终止后台任务（full-access 免确认启动）。
func TestRunJobKillE2E(t *testing.T) {
	srv, _ := rawScriptServer(t, [][]string{
		{toolCallData("call_js", "job_start", longSleepSpec(t))},
		{contentData("已启动。")},
	})
	dir := t.TempDir()
	writeConfigLevel(t, dir, srv.URL, "m", "full-access")

	var out bytes.Buffer
	stdin := "启动长任务\n/jobs kill j001\n/quit\n"
	if code := run([]string{"-data", dir}, strings.NewReader(stdin), &out, io.Discard); code != 0 {
		t.Fatalf("code = %d, out = %q", code, out.String())
	}
	got := out.String()
	for _, want := range []string{"j001", "已终止 j001"} {
		if !strings.Contains(got, want) {
			t.Fatalf("stdout 缺 %q: %q", want, got)
		}
	}
}

// TestRunMemoryCommandE2E /memory 端到端（M2 验收）：run() 注入的标准流直达编辑器
// 子进程（helper 假编辑器）——收到目标路径、写入标记，stdout 出现打开提示。
func TestRunMemoryCommandE2E(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, dir, "http://127.0.0.1:1", "m") // /memory 不发起生成
	t.Setenv("VISUAL", "")
	t.Setenv("EDITOR", os.Args[0]+" -test.run=TestEditorHelperProcess")
	t.Setenv("AQUARIUS_EDITOR_HELPER", "1")

	var out, errBuf bytes.Buffer
	if code := run([]string{"-data", dir}, strings.NewReader("/memory\n/quit\n"), &out, &errBuf); code != 0 {
		t.Fatalf("code = %d, stderr = %q, stdout = %q", code, errBuf.String(), out.String())
	}
	got := out.String()
	if !strings.Contains(got, "已用系统编辑器打开") || !strings.Contains(got, port.GlobalMemoryDoc) {
		t.Fatalf("stdout 缺打开提示: %q", got)
	}
	data, err := os.ReadFile(filepath.Join(dir, port.GlobalMemoryDoc))
	if err != nil {
		t.Fatalf("记忆文件: %v", err)
	}
	if string(data) != "edited-by-helper" {
		t.Fatalf("假编辑器未收到目标路径: %q", data)
	}
}
