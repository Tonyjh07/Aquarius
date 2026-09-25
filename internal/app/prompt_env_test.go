package app

import (
	"strings"
	"testing"
)

// TestSystemWithEnv D37：环境块逐字段拼装；全空不附加；非绝对沙盒不附提示（139d585 口径）。
func TestSystemWithEnv(t *testing.T) {
	base := defaultSystem

	if got := systemWithEnv(base, RuntimeEnv{}); got != base {
		t.Fatalf("zero env = %q, want base unchanged", got)
	}

	full := RuntimeEnv{
		Platform:   "windows/amd64",
		Terminal:   "tui",
		TERM:       "xterm-256color",
		SandboxDir: `C:\data\sandbox`,
	}
	got := systemWithEnv(base, full)
	for _, want := range []string{
		"Runtime environment:",
		"- Platform: windows/amd64",
		"- Terminal: tui (TERM=xterm-256color)",
		`- Privileged sandbox directory: "C:\\data\\sandbox"`,
		"skip confirmation from the strict permission level upward",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("env block missing %q:\n%s", want, got)
		}
	}
	if !strings.HasPrefix(got, base) {
		t.Fatalf("base prompt not kept:\n%s", got)
	}

	// TERM 缺省显示 unset；非绝对沙盒不附提示。
	got = systemWithEnv(base, RuntimeEnv{Platform: "linux/amd64", Terminal: "repl", SandboxDir: "relative/dir"})
	if !strings.Contains(got, "- Terminal: repl (TERM unset)") {
		t.Fatalf("TERM unset missing:\n%s", got)
	}
	if strings.Contains(got, "sandbox directory") {
		t.Fatalf("relative sandbox must not be hinted:\n%s", got)
	}

	// 超时说明行（D38 发现性）：缺省秒数与保留参数口径；0 不附。
	got = systemWithEnv(base, RuntimeEnv{Platform: "linux/amd64", ToolTimeoutSec: 60})
	if !strings.Contains(got, "- Tool timeout: default 60s per call") ||
		!strings.Contains(got, `"timeout_sec" argument (1-3600)`) {
		t.Fatalf("tool timeout line missing:\n%s", got)
	}
	if got := systemWithEnv(base, RuntimeEnv{Platform: "linux/amd64"}); strings.Contains(got, "Tool timeout") {
		t.Fatalf("zero ToolTimeoutSec must not add the line:\n%s", got)
	}

	// 自定义人格 + 环境块：基础提示在前、环境块在后。
	custom := "Custom persona."
	got = systemWithEnv(custom, RuntimeEnv{Platform: "darwin/arm64"})
	if !strings.HasPrefix(got, custom) || !strings.Contains(got, "darwin/arm64") {
		t.Fatalf("custom persona + env = %q", got)
	}
}
