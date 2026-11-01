package port

import (
	"strings"
	"testing"
	"time"
)

// TestDelegateReportTextShape 报告是「宿主的摘要行 + 外部代理的正文」两段：
// 摘要行必须能被逐行读懂（Notes 换行、被拒的权限、没列出的改动都要说出来），
// 正文前要有一句围栏提示——那段是外部程序写的，不是我们的结论。
func TestDelegateReportTextShape(t *testing.T) {
	run := DelegateRun{
		Agent:       "claude",
		StopReason:  "max_tokens",
		ToolCalls:   7,
		Changed:     []string{"a.go", "b.go"},
		ChangedOver: 3,
		Denied:      2,
		Duration:    90 * time.Second,
		Notes:       []string{"permission denied: 用户拒绝"},
		Report:      "正文第一行\n正文第二行",
	}
	got := DelegateReportText("d001", run)

	for _, want := range []string{
		"Delegation d001 to claude",
		"max_tokens",
		"tool calls=7",
		"a.go, b.go (and 3 more not listed)",
		"unanswered: 2",
		"Notes:\n- permission denied", // 换行不能是字面的 /n
		"written by the external agent",
		"正文第二行",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("报告缺 %q:/n%s", want, got)
		}
	}
	if strings.Contains(got, "/n") {
		t.Fatalf("报告里有字面的 /n:/n%s", got)
	}
}

// TestDelegateReportTextNoBody 代理没交正文时明说，别留一段空白让模型猜。
func TestDelegateReportTextNoBody(t *testing.T) {
	got := DelegateReportText("d002", DelegateRun{Agent: "x", StopReason: "end_turn"})
	if !strings.Contains(got, "no final text") {
		t.Fatalf("报告 = %q", got)
	}
}
