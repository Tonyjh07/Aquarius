package main

// keys_test.go 输入栏三键的启动校验（§5.19 / ui.keys）：撞键与坏键名在启动时报因，
// 不是等用户按下那个键才发现没反应。

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
)

// TestRunRejectsCollidingKeys 三键里有重复 = 启动失败并说明是哪两个撞了。
func TestRunRejectsCollidingKeys(t *testing.T) {
	dir := t.TempDir()
	cfg := fmt.Sprintf(`{
  "model": {"providers":[{"name":"openai-compatible","base_url":"http://127.0.0.1:1/v1","keys":[{"api_key":"secret:AQ_E2E_KEY"}],"models":[{"name":"m"}]}],"name":"m"},
  "ui": {"kind":"repl", "keys":{"send":"Enter","steer":"Enter","newline":"Ctrl+Enter"}}
}`)
	writeProfileLayout(t, dir, cfg)
	var out, errBuf bytes.Buffer
	code := run([]string{"-data", dir}, strings.NewReader(""), &out, &errBuf)
	if code == 0 {
		t.Fatalf("撞键应启动失败，实际退出 0（out=%s）", out.String())
	}
	if !strings.Contains(errBuf.String(), "撞键") {
		t.Fatalf("stderr = %q, want 含撞键说明", errBuf.String())
	}
}

// TestRunRejectsBadKeyName 不认识的键名同样在启动时报因（并指出是哪一个）。
func TestRunRejectsBadKeyName(t *testing.T) {
	dir := t.TempDir()
	cfg := fmt.Sprintf(`{
  "model": {"providers":[{"name":"openai-compatible","base_url":"http://127.0.0.1:1/v1","keys":[{"api_key":"secret:AQ_E2E_KEY"}],"models":[{"name":"m"}]}],"name":"m"},
  "ui": {"kind":"repl", "keys":{"steer":"Hyper+Enter"}}
}`)
	writeProfileLayout(t, dir, cfg)
	var out, errBuf bytes.Buffer
	code := run([]string{"-data", dir}, strings.NewReader(""), &out, &errBuf)
	if code == 0 {
		t.Fatalf("坏键名应启动失败，实际退出 0（out=%s）", out.String())
	}
	if !strings.Contains(errBuf.String(), "ui.keys.steer") {
		t.Fatalf("stderr = %q, want 指出 ui.keys.steer", errBuf.String())
	}
}
