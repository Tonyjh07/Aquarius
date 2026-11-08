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

// TestDelegateReportTextCommands 命令清单排在文件之前（动作比结果更该先看到）：
// 每条按字面量渲染，失败的附输出末尾一行；命令里的换行不能把一行拆成两行。
func TestDelegateReportTextCommands(t *testing.T) {
	code0, code2 := 0, 2
	run := DelegateRun{
		Agent:      "kimi",
		StopReason: "end_turn",
		Duration:   time.Second,
		Wrote:      []string{"/repo/a.go"},
		Commands: []CommandRun{
			{Line: "bash -c cd '/repo' && git status", ExitCode: &code0},
			{Line: "bash -c make", ExitCode: &code2, Tail: "error: undefined: x"},
			{Line: "bash -c sleep 600", Signal: "killed"},
			{Line: "bash -c ls\npwd"},
		},
	}
	got := DelegateReportText("d003", run)
	want := `Commands run through this session: "bash -c cd '/repo' && git status" (exit 0), ` +
		`"bash -c make" (exit 2); tail: "error: undefined: x", ` +
		`"bash -c sleep 600" (stopped by killed), "bash -c ls\npwd"`
	if !strings.Contains(got, want) {
		t.Fatalf("命令清单不对：\n got %s\nwant %s", got, want)
	}
	if strings.Index(got, "Commands run") > strings.Index(got, "Files written") {
		t.Fatalf("命令应排在文件之前：\n%s", got)
	}
	if got := DelegateReportText("d004", DelegateRun{Agent: "x"}); strings.Contains(got, "Commands run") {
		t.Fatalf("没有命令时不该出现这一行：\n%s", got)
	}
}

// TestNormalizeRoots 根集归一：去空白、去空项、去重（保序），相对路径拒收。
//
// 「多一行空行不算错、重复一条也不算错」是刻意的：手写或模型产出里出现这两样很常见，
// 为它们报错只会让人反复试错；只有相对路径必须拒——它会让越界判定失去参照物。
func TestNormalizeRoots(t *testing.T) {
	t.Parallel()
	got, err := NormalizeRoots([]string{" /a ", "", "/b", "/a", "   "})
	if err != nil {
		t.Fatalf("normalize: %v", err)
	}
	if len(got) != 2 || got[0] != "/a" || got[1] != "/b" {
		t.Fatalf("roots = %v（want 去空去重保序）", got)
	}
	if got, err := NormalizeRoots(nil); err != nil || got != nil {
		t.Fatalf("空输入应回 nil: %v %v", got, err)
	}
	if got, err := NormalizeRoots([]string{"", " "}); err != nil || got != nil {
		t.Fatalf("全空应回 nil（不是含空串的一条）: %v %v", got, err)
	}
	if _, err := NormalizeRoots([]string{"rel/dir"}); err == nil {
		t.Fatal("相对路径应报错")
	}
}
