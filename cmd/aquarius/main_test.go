package main

// main_test.go 组装根的共享测试夹具（脚本 LLM server、profile 布局、配置写入、
// 设置窗替身）与启动路径：版本旗标、首次运行生成配置、旧格式拒绝、profile
// 切换与校验。模型配置见 main_model_test.go，运行期写回见 main_run_test.go，
// 协议与密钥池见 main_llm_test.go，端到端会话见 main_e2e_test.go，附件见
// main_attach_test.go，记忆与编辑器见 main_memory_test.go，后台任务见
// main_job_test.go。

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/Tonyjh07/Aquarius/internal/adapter/uigui"
)

// recordedReq 脚本服务记录的一次请求。
type recordedReq struct {
	body []byte
}

// scriptServer 造一个按序回话的 OpenAI 兼容 SSE 服务。
// replies[i] 是第 i+1 次请求的回复文本。
// reqLog 脚本服务的请求收集器：handler goroutine 写、测试体读——
// 裸切片无同步边，-race 会判为无序访问（审查修复）。
type reqLog struct {
	mu    sync.Mutex
	items []recordedReq
}

// add 记录一次请求，返回其下标。
func (l *reqLog) add(r recordedReq) int {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.items = append(l.items, r)
	return len(l.items) - 1
}

// len 请求总数。
func (l *reqLog) len() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.items)
}

// at 第 i 条（越界即 panic——测试尽早失败）。
func (l *reqLog) at(i int) recordedReq {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.items[i]
}

// all 全量快照（range 遍历用）。
func (l *reqLog) all() []recordedReq {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]recordedReq(nil), l.items...)
}

func scriptServer(t *testing.T, replies []string) (*httptest.Server, *reqLog) {
	t.Helper()
	reqs := &reqLog{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			http.NotFound(w, r)
			return
		}
		body, _ := io.ReadAll(r.Body)
		i := reqs.add(recordedReq{body: body})
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
	return srv, reqs
}

// writeProfileLayout 在 <data> 落一套可运行 profile 布局（D110）：根指针 +
// profiles/default/config.json。返回 profile 目录（conversations/ 等数据面断言用）。
func writeProfileLayout(t *testing.T, dir, cfgJSON string) string {
	t.Helper()
	prof := filepath.Join(dir, "profiles", "default")
	if err := os.MkdirAll(prof, 0o755); err != nil {
		t.Fatalf("mkdir profile: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(pointerTemplate), 0o644); err != nil {
		t.Fatalf("write pointer: %v", err)
	}
	if err := os.WriteFile(filepath.Join(prof, "config.json"), []byte(cfgJSON), 0o644); err != nil {
		t.Fatalf("write profile config: %v", err)
	}
	return prof
}

// writeConfig 写入可运行的 profile 配置（密钥走 secret 引用）。
func writeConfig(t *testing.T, dir, baseURL, name string) {
	t.Helper()
	cfg := fmt.Sprintf(`{
  "model": {"providers":[{"name":"openai-compatible","base_url":%q,"keys":[{"api_key":"secret:AQ_E2E_KEY"}],"models":[{"name":%q}]}],"name":%q},
  "ui": {"kind":"repl"},
  "limits": {"max_turns": 8, "max_context_tokens": 64000, "tool_output_chars": 20000, "tool_timeout_sec": 60}
}`, baseURL, name, name)
	writeProfileLayout(t, dir, cfg)
	t.Setenv("AQ_E2E_KEY", "test-key")
}

// swapSetupWindowRunner 换向导窗宿主并返回还原函数（D141）：首跑/缺密钥路径的测试
// 不得真开窗（真宿主 = uigui.RunSetupWindow，会阻塞在事件循环上）。仓库先例：
// messageBox / notifySenderImpl 的包级可注入实现。
func swapSetupWindowRunner(fake func(uigui.SetupRequest) (uigui.SetupOutcome, error)) func() {
	prev := setupWindowRunner
	setupWindowRunner = fake
	return func() { setupWindowRunner = prev }
}

// TestVersionFlag -version 打印版本并退出 0：不读配置、不装配端口（发版冒烟用）。
func TestVersionFlag(t *testing.T) {
	var out, errBuf bytes.Buffer
	if code := run([]string{"-version"}, strings.NewReader(""), &out, &errBuf); code != 0 {
		t.Fatalf("code = %d, stderr = %s", code, errBuf.String())
	}
	line := strings.TrimSpace(out.String())
	if !strings.HasPrefix(line, "Aquarius ") || !strings.Contains(line, version) {
		t.Fatalf("版本输出 = %q", out.String())
	}
	if !strings.Contains(line, runtime.GOOS+"/"+runtime.GOARCH) || !strings.Contains(line, runtime.Version()) {
		t.Fatalf("版本输出缺平台信息 = %q", out.String())
	}
}

// TestRunFirstTimeGeneratesConfig 首次运行（D110 + D141）：生成根指针 + default profile
// 模板配置。模板 ui.kind=gui ⇒ **不再「写模板即退出」**：改开欢迎向导窗（此处换假宿主），
// 用户取消 ⇒ 退出 0、不写 welcomed 标记（配置仍不完整，下次再来）。
func TestRunFirstTimeGeneratesConfig(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("AQUARIUS_OPENAI_KEY", "") // 模板密钥引用取不到 ⇒ 判「配置不完整」，必走向导
	var got []uigui.SetupRequest
	defer swapSetupWindowRunner(func(req uigui.SetupRequest) (uigui.SetupOutcome, error) {
		got = append(got, req)
		return uigui.SetupCancelled, nil // 取消：不写盘
	})()
	var out, errBuf bytes.Buffer
	if code := run([]string{"-data", dir}, strings.NewReader(""), &out, &errBuf); code != 0 {
		t.Fatalf("code = %d, stderr = %s", code, errBuf.String())
	}
	if !strings.Contains(out.String(), "已生成配置") {
		t.Fatalf("stdout = %q", out.String())
	}
	pointer, err := os.ReadFile(filepath.Join(dir, "config.json"))
	if err != nil {
		t.Fatalf("指针未生成: %v", err)
	}
	if !strings.Contains(string(pointer), `"profile"`) {
		t.Fatalf("根 config.json 应为指针: %s", pointer)
	}
	profCfg, err := os.ReadFile(filepath.Join(dir, "profiles", "default", "config.json"))
	if err != nil {
		t.Fatalf("profile 配置未生成: %v", err)
	}
	if !strings.Contains(string(profCfg), `"secret:AQUARIUS_OPENAI_KEY"`) {
		t.Fatalf("模板应使用 secret 引用: %s", profCfg)
	}
	if !strings.Contains(string(profCfg), `"level": "strict"`) {
		t.Fatalf("模板应含权限等级默认值: %s", profCfg)
	}
	if !strings.Contains(string(profCfg), `"system_prompt"`) {
		t.Fatalf("模板应含 system_prompt 键: %s", profCfg)
	}
	if !strings.Contains(string(profCfg), `"prompts"`) {
		t.Fatalf("模板应含 prompts 段（S7/D144）: %s", profCfg)
	}
	if !strings.Contains(string(profCfg), `"kind": "gui"`) {
		t.Fatalf("模板默认 ui.kind 应为 gui（D51）: %s", profCfg)
	}
	if fi, err := os.Stat(filepath.Join(dir, "profiles", "default", "sandbox")); err != nil || !fi.IsDir() {
		t.Fatalf("特权目录应自动创建（profile 内）: %v", err)
	}
	// D141：向导窗被调用一次，问题清单指出缺哪个环境变量；取消 ⇒ 标记未置位。
	if len(got) != 1 {
		t.Fatalf("向导窗调用 %d 次, want 1（reqs=%+v）", len(got), got)
	}
	if joined := strings.Join(got[0].Issues, "；"); !strings.Contains(joined, "AQUARIUS_OPENAI_KEY") {
		t.Fatalf("向导窗问题清单应指出缺失的密钥: %+v", got[0].Issues)
	}
	if !strings.Contains(errBuf.String(), "已取消配置引导") {
		t.Fatalf("stderr 应记取消: %q", errBuf.String())
	}
	if !strings.Contains(string(profCfg), `"welcomed": false`) {
		t.Fatalf("取消不得写 welcomed 标记: %s", profCfg)
	}
}

// TestRunLegacyRootConfigRejected 旧单文件 config（D110 前布局）启动即显式报因
// 并给出手工迁移步骤——绝不静默当零值指针解析（D95 同口径，D110④）。
func TestRunLegacyRootConfigRejected(t *testing.T) {
	dir := t.TempDir()
	legacy := `{"model":{"name":"m","base_url":"http://127.0.0.1:1"},"ui":{"kind":"repl"}}`
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(legacy), 0o644); err != nil {
		t.Fatalf("write legacy config: %v", err)
	}
	var out, errBuf bytes.Buffer
	if code := run([]string{"-data", dir}, strings.NewReader(""), &out, &errBuf); code != 1 {
		t.Fatalf("code = %d, want 1", code)
	}
	msg := errBuf.String()
	for _, want := range []string{"旧格式", "profiles", "手工"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("stderr 缺 %q: %q", want, msg)
		}
	}
}

// TestRunProfileFlagSwitchesData --profile 选择已存在的 profile：数据面整体落在
// profiles/<name>/（会话、审计随 profile 隔离，D110①）。
func TestRunProfileFlagSwitchesData(t *testing.T) {
	srv, _ := scriptServer(t, []string{"好的"})
	dir := t.TempDir()
	writeProfileLayout(t, dir, fmt.Sprintf(`{
  "model": {"providers":[{"name":"p","base_url":%q,"keys":[{"api_key":"secret:X"}],"models":[{"name":"m"}]}],"name":"m"},
  "ui": {"kind":"repl"},
  "limits": {"max_turns": 8, "max_context_tokens": 64000, "tool_output_chars": 20000, "tool_timeout_sec": 60}
}`, srv.URL))
	// 第二个 profile work：配置同构（密钥引用同名环境变量）。
	work := filepath.Join(dir, "profiles", "work")
	if err := os.MkdirAll(work, 0o755); err != nil {
		t.Fatalf("mkdir work: %v", err)
	}
	if err := os.WriteFile(filepath.Join(work, "config.json"),
		[]byte(fmt.Sprintf(`{"model":{"providers":[{"name":"p","base_url":%q,"keys":[{"api_key":"secret:X"}],"models":[{"name":"m"}]}],"name":"m"},"ui":{"kind":"repl"}}`, srv.URL)), 0o644); err != nil {
		t.Fatalf("write work config: %v", err)
	}
	t.Setenv("X", "k")

	var out, errBuf bytes.Buffer
	if code := run([]string{"-data", dir, "-profile", "work"},
		strings.NewReader("hi\n/quit\n"), &out, &errBuf); code != 0 {
		t.Fatalf("code = %d, stderr = %q", code, errBuf.String())
	}
	// 会话树落在 work profile，default 不受影响。
	if files, _ := filepath.Glob(filepath.Join(work, "conversations", "*.json")); len(files) != 1 {
		t.Fatalf("work conversations = %v, want 1", files)
	}
	if files, _ := filepath.Glob(filepath.Join(dir, "profiles", "default", "conversations", "*.json")); len(files) != 0 {
		t.Fatalf("default conversations = %v, want 0（整目录隔离）", files)
	}
}

// TestRunProfileFlagMissingRejected --profile 指向不存在的 profile：报因并列出
// 可用名，不自动创建（D110 修订②）。
func TestRunProfileFlagMissingRejected(t *testing.T) {
	dir := t.TempDir()
	writeProfileLayout(t, dir, `{"model":{"providers":[{"name":"p","base_url":"http://127.0.0.1:1","keys":[{"api_key":"secret:X"}],"models":[{"name":"m"}]}],"name":"m"},"ui":{"kind":"repl"}}`)
	t.Setenv("X", "k")
	var out, errBuf bytes.Buffer
	if code := run([]string{"-data", dir, "-profile", "nope"}, strings.NewReader(""), &out, &errBuf); code != 1 {
		t.Fatalf("code = %d, want 1", code)
	}
	if !strings.Contains(errBuf.String(), "nope") || !strings.Contains(errBuf.String(), "default") {
		t.Fatalf("stderr = %q, want 报因含可用 profile 名", errBuf.String())
	}
	if _, err := os.Stat(filepath.Join(dir, "profiles", "nope")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("不存在的 profile 不应被自动创建: %v", err)
	}
}

// TestParsePointerFile 指针解析：合法指针、缺 profile 键、非法 profile 名、
// 旧格式键判别（ErrLegacyConfig）。
func TestParsePointerFile(t *testing.T) {
	if name, err := parsePointerFile("p", []byte(`{"profile":"work"}`)); err != nil || name != "work" {
		t.Fatalf("name = %q, err = %v", name, err)
	}
	if _, err := parsePointerFile("p", []byte(`{"other":1}`)); err == nil ||
		!strings.Contains(err.Error(), "profile") {
		t.Fatalf("缺 profile 键 err = %v", err)
	}
	if _, err := parsePointerFile("p", []byte(`{"profile":"../evil"}`)); err == nil {
		t.Fatal("路径分隔 profile 名应被拒")
	}
	for _, k := range []string{"model", "ui", "mcpServers"} {
		data := fmt.Sprintf(`{"profile":"default",%q:{}}`, k)
		_, err := parsePointerFile("p", []byte(data))
		if !errors.Is(err, ErrLegacyConfig) {
			t.Fatalf("键 %q: err = %v, want ErrLegacyConfig", k, err)
		}
	}
	if _, err := parsePointerFile("p", []byte(`{"model":{"name":"m"}}`)); !errors.Is(err, ErrLegacyConfig) {
		t.Fatalf("纯旧格式 err = %v, want ErrLegacyConfig", err)
	}
}

// TestValidProfileName profile 名目录安全子集。
func TestValidProfileName(t *testing.T) {
	for _, ok := range []string{"default", "work-1", "测试", "a.b"} {
		if err := validProfileName(ok); err != nil {
			t.Fatalf("%q 应合法: %v", ok, err)
		}
	}
	for _, bad := range []string{"", " x", "x ", ".", "..", "a/b", `a\b`, "a:b", strings.Repeat("x", 65)} {
		if err := validProfileName(bad); err == nil {
			t.Fatalf("%q 应被拒", bad)
		}
	}
}
