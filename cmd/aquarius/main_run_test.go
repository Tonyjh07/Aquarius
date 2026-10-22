package main

// main_run_test.go 运行期写回与密钥：权限/模型切换落盘、非法权限与 effort、
// 明文密钥告警、数据目录转绝对路径、空密钥回落默认 secret、缺失密钥报因、
// think 工具可见性、人格环境注入。

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Tonyjh07/Aquarius/internal/adapter/uigui"
	"github.com/Tonyjh07/Aquarius/internal/domain/conversation"
)

// TestRunPermissionSwitchWritesBackConfig /permission 切换写回：其余配置键与数值类型不丢、无半截文件残留（D22）。
func TestRunPermissionSwitchWritesBackConfig(t *testing.T) {
	dir := t.TempDir()
	cfg := `{
  "model": {"providers":[{"name":"p","base_url":"http://127.0.0.1:1","keys":[{"api_key":"secret:X"}],"models":[{"name":"m"}]}],"name":"m"},
  "ui": {"kind":"repl"},
  "system_prompt": "",
  "memory": {"dir": "~/.aquarius/memory"},
  "permissions": {"level": "strict"},
  "limits": {"max_turns": 8, "max_context_tokens": 64000, "compact_threshold": 0.7}
}`
	prof := writeProfileLayout(t, dir, cfg)
	t.Setenv("X", "k")

	var out, errBuf bytes.Buffer
	code := run([]string{"-data", dir}, strings.NewReader("/permission permissive\n/quit\n"), &out, &errBuf)
	if code != 0 {
		t.Fatalf("code = %d, err = %s", code, errBuf.String())
	}
	if !strings.Contains(out.String(), "permissive") {
		t.Fatalf("stdout = %q", out.String())
	}

	data, err := os.ReadFile(filepath.Join(prof, "config.json"))
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
	if m, _ := filepath.Glob(filepath.Join(prof, "config.json") + ".*"); len(m) > 0 {
		t.Fatalf("不应残留临时文件: %v", m)
	}
}

// TestRunModelSwitchWritesBackConfig /model <name> 写回 model.name 并保留其余键（D32，同 /permission）。
func TestRunModelSwitchWritesBackConfig(t *testing.T) {
	dir := t.TempDir()
	cfg := `{
  "model": {"providers":[{"name":"openai-compatible","base_url":"http://127.0.0.1:1","keys":[{"api_key":"secret:X"}],"models":[{"name":"old-model"}]}],"name":"old-model"},
  "ui": {"kind":"repl"},
  "system_prompt": "人格不动",
  "permissions": {"level": "strict"},
  "limits": {"max_turns": 8, "max_context_tokens": 64000}
}`
	prof := writeProfileLayout(t, dir, cfg)
	t.Setenv("X", "k")

	var out, errBuf bytes.Buffer
	code := run([]string{"-data", dir}, strings.NewReader("/model new-model\n/quit\n"), &out, &errBuf)
	if code != 0 {
		t.Fatalf("code = %d, err = %s", code, errBuf.String())
	}
	if !strings.Contains(out.String(), "已切换模型 → new-model") {
		t.Fatalf("stdout = %q", out.String())
	}

	data, err := os.ReadFile(filepath.Join(prof, "config.json"))
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
	cfg := `{"model":{"providers":[{"name":"p","base_url":"http://127.0.0.1:1","keys":[{"api_key":"secret:X"}],"models":[{"name":"m"}]}],"name":"m"},"ui":{"kind":"repl"},"permissions":{"level":"root"}}`
	writeProfileLayout(t, dir, cfg)
	t.Setenv("X", "k")
	var out, errBuf bytes.Buffer
	if code := run([]string{"-data", dir}, strings.NewReader(""), &out, &errBuf); code != 1 {
		t.Fatalf("code = %d, want 1", code)
	}
	if !strings.Contains(errBuf.String(), "未知权限等级") {
		t.Fatalf("stderr = %q", errBuf.String())
	}
}

// TestRunPlaintextKeyWarns D35：明文 api_key 允许启动，但必须打印警告、
// 给出 secret: 建议，且不回显密钥值。
func TestRunPlaintextKeyWarns(t *testing.T) {
	dir := t.TempDir()
	cfg := `{"model":{"providers":[{"name":"p","base_url":"http://127.0.0.1:1","keys":[{"api_key":"sk-plain-abc"}],"models":[{"name":"m"}]}],"name":"m"},"ui":{"kind":"repl"}}`
	writeProfileLayout(t, dir, cfg)
	var out, errBuf bytes.Buffer
	if code := run([]string{"-data", dir}, strings.NewReader("/quit\n"), &out, &errBuf); code != 0 {
		t.Fatalf("code = %d, stderr = %q", code, errBuf.String())
	}
	stderr := errBuf.String()
	if !strings.Contains(stderr, "明文") || !strings.Contains(stderr, "secret:") {
		t.Fatalf("stderr 缺明文警告/建议: %q", stderr)
	}
	if strings.Contains(stderr, "sk-plain-abc") {
		t.Fatalf("警告不得回显密钥值: %q", stderr)
	}
}

// TestRunEmptyAPIKeyFallsBackToDefaultSecret D35：api_key 留空回落默认引用
// secret:AQUARIUS_OPENAI_KEY（文件内值优先于默认引用；环境变量缺失才报错）。
// TestRunRelativeDataDirBecomesAbsolute -data 相对路径启动即锚定为绝对：
// 首跑输出的配置路径可直接复制使用，沙盒提示也因此恒为绝对路径。
func TestRunRelativeDataDirBecomesAbsolute(t *testing.T) {
	tmp := t.TempDir()
	t.Chdir(tmp) // 测试内改 cwd，结束自动还原
	t.Setenv("AQUARIUS_OPENAI_KEY", "")
	// D141：模板 ui.kind=gui ⇒ 首跑会开向导窗；这里换假宿主并取消（配置仍不完整）。
	defer swapSetupWindowRunner(func(uigui.SetupRequest) (uigui.SetupOutcome, error) {
		return uigui.SetupCancelled, nil
	})()
	var out, errBuf bytes.Buffer
	if code := run([]string{"-data", "data"}, strings.NewReader(""), &out, &errBuf); code != 0 {
		t.Fatalf("code = %d, stderr = %q", code, errBuf.String())
	}
	want := filepath.Join(tmp, "data", "config.json") // tmp 为绝对 TempDir
	if !strings.Contains(out.String(), want) {
		t.Fatalf("first-run 输出 = %q, want 绝对路径 %q", out.String(), want)
	}
}

func TestRunEmptyAPIKeyFallsBackToDefaultSecret(t *testing.T) {
	dir := t.TempDir()
	cfg := `{"model":{"providers":[{"name":"p","base_url":"http://127.0.0.1:1","keys":[{"api_key":""}],"models":[{"name":"m"}]}],"name":"m"},"ui":{"kind":"repl"}}`
	writeProfileLayout(t, dir, cfg)
	t.Setenv("AQUARIUS_OPENAI_KEY", "k") // 环境变量就位 → 正常启动
	var out, errBuf bytes.Buffer
	if code := run([]string{"-data", dir}, strings.NewReader("/quit\n"), &out, &errBuf); code != 0 {
		t.Fatalf("code = %d, stderr = %q", code, errBuf.String())
	}

	t.Setenv("AQUARIUS_OPENAI_KEY", "") // 缺失（envSecrets 对空值报错）→ 指名报因
	out.Reset()
	errBuf.Reset()
	if code := run([]string{"-data", dir}, strings.NewReader(""), &out, &errBuf); code != 1 {
		t.Fatalf("缺环境变量 code = %d, want 1", code)
	}
	if !strings.Contains(errBuf.String(), "AQUARIUS_OPENAI_KEY") {
		t.Fatalf("stderr 应指名默认环境变量: %q", errBuf.String())
	}
}

// TestRunRejectsBadEffort D34：reasoning_effort 非法值启动即报因（与 /effort 同口径）。
func TestRunRejectsBadEffort(t *testing.T) {
	dir := t.TempDir()
	cfg := `{"model":{"providers":[{"name":"p","base_url":"http://127.0.0.1:1","keys":[{"api_key":"secret:X"}],"models":[{"name":"m"}]}],"name":"m","reasoning_effort":"extreme"},"ui":{"kind":"repl"}}`
	writeProfileLayout(t, dir, cfg)
	t.Setenv("X", "k")
	var out, errBuf bytes.Buffer
	if code := run([]string{"-data", dir}, strings.NewReader(""), &out, &errBuf); code != 1 {
		t.Fatalf("code = %d, want 1", code)
	}
	if !strings.Contains(errBuf.String(), "minimal|low|medium|high|off") {
		t.Fatalf("stderr = %q, want 档位口径", errBuf.String())
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

// TestRunThinkToolVisibility D34：think 草稿工具默认**不出现**在请求工具清单；
// config model.think_tool=true 时列给模型（装配期决定，重启生效）。
func TestRunThinkToolVisibility(t *testing.T) {
	cases := []struct {
		name      string
		thinkTool string // model.think_tool 字段原文（"" = 键缺失）
		wantThink bool
	}{
		{"默认隐藏", "", false},
		{"配置启用", `"think_tool": true`, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv, reqs := scriptServer(t, []string{"好的"})
			dir := t.TempDir()
			model := `"providers":[{"name":"p","base_url":` + fmt.Sprintf("%q", srv.URL) + `,"keys":[{"api_key":"secret:X"}],"models":[{"name":"m"}]}]`
			if tc.thinkTool != "" {
				model += "," + tc.thinkTool
			}
			cfg := `{"model":{` + model + `,"name":"m"},"ui":{"kind":"repl"},
  "limits":{"max_turns":8,"max_context_tokens":64000,"tool_output_chars":20000,"tool_timeout_sec":60}}`
			writeProfileLayout(t, dir, cfg)
			t.Setenv("X", "k")
			var out bytes.Buffer
			if code := run([]string{"-data", dir}, strings.NewReader("hi\n/quit\n"), &out, io.Discard); code != 0 {
				t.Fatalf("code = %d, out = %q", code, out.String())
			}
			body := string(reqs.at(0).body)
			got := strings.Contains(body, `"name":"think"`)
			if got != tc.wantThink {
				t.Fatalf("think 工具在请求清单中 = %v, want %v（body 摘要: %.300s）", got, tc.wantThink, body)
			}
		})
	}
}

// TestRunPersonaEnvironmentE2E D37：persona 首节点携带运行环境块（main 装配 → 树 → 请求体）。
func TestRunPersonaEnvironmentE2E(t *testing.T) {
	srv, reqs := scriptServer(t, []string{"好的"})
	dir := t.TempDir()
	cfg := fmt.Sprintf(`{"model":{"providers":[{"name":"p","base_url":%q,"keys":[{"api_key":"secret:X"}],"models":[{"name":"m"}]}],"name":"m"},"ui":{"kind":"repl"},
  "limits":{"max_turns":8,"max_context_tokens":64000,"tool_output_chars":20000,"tool_timeout_sec":60}}`, srv.URL)
	writeProfileLayout(t, dir, cfg)
	t.Setenv("X", "k")

	var out bytes.Buffer
	if code := run([]string{"-data", dir}, strings.NewReader("hi\n/quit\n"), &out, io.Discard); code != 0 {
		t.Fatalf("code = %d, out = %q", code, out.String())
	}
	for _, want := range []string{"Runtime environment:", "Platform: ", "Terminal: repl", "sandbox",
		"Tool timeout: default 60s", `"timeout_sec"`} {
		if !strings.Contains(string(reqs.at(0).body), want) {
			t.Fatalf("request body 缺 %q: %.400s", want, reqs.at(0).body)
		}
	}
	c := loadTree(t, dir)
	persona := c.Path()[1]
	if persona.Role != conversation.RoleSystem ||
		!strings.Contains(persona.Content[0].Text, "Runtime environment:") {
		t.Fatalf("persona = %+v, want 环境块随快照入树", persona)
	}
}
