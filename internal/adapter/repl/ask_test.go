package repl

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/Tonyjh07/Aquarius/internal/port"
)

// askReq 两题：第一题两个选项（单选），第二题纯自由输入。
func askReq() port.AskRequest {
	return port.AskRequest{Questions: []port.AskQuestion{
		{Header: "Target", Question: "Which platform?", Options: []port.AskOption{
			{Label: "Windows", Description: "desktop"},
			{Label: "Linux"},
		}},
		{Header: "Scope", Question: "What scope?"},
	}}
}

// TestAskByNumbers 编号选择：回填选项标签（不是编号），按题序对齐。
func TestAskByNumbers(t *testing.T) {
	var buf bytes.Buffer
	ui := New(strings.NewReader("1\nonly this file\n"), &buf)
	ans, err := ui.Ask(context.Background(), askReq())
	if err != nil {
		t.Fatalf("Ask: %v", err)
	}
	if ans.Dismissed {
		t.Fatal("不应是放弃")
	}
	if len(ans.Answers) != 2 {
		t.Fatalf("answers = %+v", ans.Answers)
	}
	if got := ans.Answers[0]; len(got) != 1 || got[0] != "Windows" {
		t.Fatalf("第 1 题 = %v, want [Windows]", got)
	}
	if got := ans.Answers[1]; len(got) != 1 || got[0] != "only this file" {
		t.Fatalf("第 2 题 = %v, want 自由答案", got)
	}
	// 题干、选项与说明都要呈现给用户（编号来自同一序列）。
	out := buf.String()
	for _, want := range []string{"Which platform?", "1) Windows", "desktop", "What scope?", "第 1/2 题"} {
		if !strings.Contains(out, want) {
			t.Fatalf("输出缺 %q：%q", want, out)
		}
	}
}

// TestAskMultipleAndDedup 多选：逗号/空格分隔、重复编号去重。
func TestAskMultipleAndDedup(t *testing.T) {
	req := port.AskRequest{Questions: []port.AskQuestion{{
		Header: "Files", Question: "Which files?", Multiple: true,
		Options: []port.AskOption{{Label: "a.go"}, {Label: "b.go"}, {Label: "c.go"}},
	}}}
	ui := New(strings.NewReader("1, 3,3\n"), io.Discard)
	ans, err := ui.Ask(context.Background(), req)
	if err != nil {
		t.Fatalf("Ask: %v", err)
	}
	got := ans.Answers[0]
	if len(got) != 2 || got[0] != "a.go" || got[1] != "c.go" {
		t.Fatalf("answers = %v, want [a.go c.go]", got)
	}
}

// TestAskSingleTrimsExtra 单选给了多个编号：只取第一个，并明说。
func TestAskSingleTrimsExtra(t *testing.T) {
	var buf bytes.Buffer
	req := port.AskRequest{Questions: []port.AskQuestion{{
		Header: "One", Question: "Pick one",
		Options: []port.AskOption{{Label: "a"}, {Label: "b"}},
	}}}
	ui := New(strings.NewReader("1 2\n"), &buf)
	ans, err := ui.Ask(context.Background(), req)
	if err != nil {
		t.Fatalf("Ask: %v", err)
	}
	if got := ans.Answers[0]; len(got) != 1 || got[0] != "a" {
		t.Fatalf("answers = %v, want [a]", got)
	}
	if !strings.Contains(buf.String(), "单选") {
		t.Fatalf("未提示只取第一项：%q", buf.String())
	}
}

// TestAskFreeTextAndEmpty 有选项时输入非编号 = 自由答案；空行 = 本题不答。
func TestAskFreeTextAndEmpty(t *testing.T) {
	ui := New(strings.NewReader("something else\n\n"), io.Discard)
	ans, err := ui.Ask(context.Background(), askReq())
	if err != nil {
		t.Fatalf("Ask: %v", err)
	}
	if got := ans.Answers[0]; len(got) != 1 || got[0] != "something else" {
		t.Fatalf("第 1 题 = %v", got)
	}
	if got := ans.Answers[1]; len(got) != 0 {
		t.Fatalf("第 2 题 = %v, want 空", got)
	}
}

// TestAskDismiss 放弃全部：立即返回 Dismissed，后续题不再问。
func TestAskDismiss(t *testing.T) {
	var buf bytes.Buffer
	ui := New(strings.NewReader("/dismiss\n"), &buf)
	ans, err := ui.Ask(context.Background(), askReq())
	if err != nil {
		t.Fatalf("Ask: %v", err)
	}
	if !ans.Dismissed {
		t.Fatalf("ans = %+v, want Dismissed", ans)
	}
	if strings.Contains(buf.String(), "What scope?") {
		t.Fatalf("放弃后不该再问下一题：%q", buf.String())
	}
}

// TestAskEOFIsDismiss 输入流结束不替用户编答案：按放弃全部收场。
func TestAskEOFIsDismiss(t *testing.T) {
	ui := New(strings.NewReader(""), io.Discard)
	ans, err := ui.Ask(context.Background(), askReq())
	if err != nil {
		t.Fatalf("Ask: %v", err)
	}
	if !ans.Dismissed {
		t.Fatalf("ans = %+v, want Dismissed", ans)
	}
}

// TestAskContextCancel 取消本轮上抛 ctx 错误（与「忽略」区分）。
func TestAskContextCancel(t *testing.T) {
	ui := New(strings.NewReader(""), io.Discard)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := ui.Ask(ctx, askReq()); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}

// TestParseAskChoice 编号解析：越界/混文本整体按自由答案处理（不当成编号）。
func TestParseAskChoice(t *testing.T) {
	opts := []port.AskOption{{Label: "a"}, {Label: "b"}}
	cases := []struct {
		line string
		want []string
		ok   bool
	}{
		{"1", []string{"a"}, true},
		{"2,1", []string{"b", "a"}, true},
		{"1 2", []string{"a", "b"}, true},
		{"1)", []string{"a"}, true},
		{"3", nil, false},
		{"1 and 2", nil, false},
		{"version 2", nil, false},
		{"", nil, false},
	}
	for _, tc := range cases {
		got, ok := parseAskChoice(tc.line, opts)
		if ok != tc.ok {
			t.Fatalf("%q ok = %v, want %v", tc.line, ok, tc.ok)
		}
		if ok && strings.Join(got, ",") != strings.Join(tc.want, ",") {
			t.Fatalf("%q = %v, want %v", tc.line, got, tc.want)
		}
	}
}
