package port

import "testing"

// TestInjectSourceLabel 来源友好化（D150/D166）：前缀翻成用户面措辞，未知回落原文。
func TestInjectSourceLabel(t *testing.T) {
	cases := map[string]string{
		"user":         "手动",
		"job:j1":       "任务 j1",
		"poller:p1":    "观察 p1",
		"tool:think":   "工具 think",
		"plugin:demo":  "插件 demo",
		"weird":        "weird",
		"other:a:b":    "other:a:b",
		"":             "未知来源",
		"tool:mcpx:cc": "工具 mcpx:cc",
		"user:extra":   "手动 extra",
	}
	for in, want := range cases {
		if got := InjectSourceLabel(in); got != want {
			t.Fatalf("label(%q) = %q, want %q", in, got, want)
		}
	}
}
