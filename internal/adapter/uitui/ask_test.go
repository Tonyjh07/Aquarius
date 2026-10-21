package uitui

import (
	"context"
	"errors"
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

// TestAskByNumbers 编号选择：回填选项标签、按题序对齐；题干与选项进转写区。
func TestAskByNumbers(t *testing.T) {
	_, u := newTestModel(t)
	u.inCh <- port.UserInput{Text: "1"}
	u.inCh <- port.UserInput{Text: "only this file"}

	ans, err := u.Ask(context.Background(), askReq())
	if err != nil {
		t.Fatalf("Ask: %v", err)
	}
	if ans.Dismissed {
		t.Fatal("不应是放弃")
	}
	if got := ans.Answers[0]; len(got) != 1 || got[0] != "Windows" {
		t.Fatalf("第 1 题 = %v", got)
	}
	if got := ans.Answers[1]; len(got) != 1 || got[0] != "only this file" {
		t.Fatalf("第 2 题 = %v", got)
	}
	// 转写文本与 repl 同口径（无 Program 时不投递，这里直接验文本生成）。
	text := askPromptText(0, 2, askReq().Questions[0])
	for _, want := range []string{"第 1/2 题", "Which platform?", "1) Windows", "desktop"} {
		if !strings.Contains(text, want) {
			t.Fatalf("提问文本缺 %q：%q", want, text)
		}
	}
}

// TestAskMultiple 多选编号（逗号/空格分隔）与去重。
func TestAskMultiple(t *testing.T) {
	_, u := newTestModel(t)
	u.inCh <- port.UserInput{Text: "1, 2,2"}
	req := port.AskRequest{Questions: []port.AskQuestion{{
		Header: "Files", Question: "Which files?", Multiple: true,
		Options: []port.AskOption{{Label: "a.go"}, {Label: "b.go"}},
	}}}
	ans, err := u.Ask(context.Background(), req)
	if err != nil {
		t.Fatalf("Ask: %v", err)
	}
	if got := ans.Answers[0]; len(got) != 2 || got[0] != "a.go" || got[1] != "b.go" {
		t.Fatalf("answers = %v", got)
	}
}

// TestAskDismissCommand /dismiss（输入行解析成命令）与裸文本 dismiss 都算放弃全部。
func TestAskDismissCommand(t *testing.T) {
	_, u := newTestModel(t)
	u.inCh <- port.UserInput{Command: &port.Command{Name: "dismiss"}}
	ans, err := u.Ask(context.Background(), askReq())
	if err != nil {
		t.Fatalf("Ask: %v", err)
	}
	if !ans.Dismissed {
		t.Fatalf("ans = %+v, want Dismissed", ans)
	}
}

// TestAskEmptyMeansUnanswered 空行 = 本题不答（后续题照问）。
func TestAskEmptyMeansUnanswered(t *testing.T) {
	_, u := newTestModel(t)
	u.inCh <- port.UserInput{}
	u.inCh <- port.UserInput{Text: "x"}
	ans, err := u.Ask(context.Background(), askReq())
	if err != nil {
		t.Fatalf("Ask: %v", err)
	}
	if len(ans.Answers[0]) != 0 {
		t.Fatalf("第 1 题 = %v, want 空", ans.Answers[0])
	}
	if got := ans.Answers[1]; len(got) != 1 || got[0] != "x" {
		t.Fatalf("第 2 题 = %v", got)
	}
}

// TestAskEOFIsDismiss 输入流结束不替用户编答案：放弃全部。
func TestAskEOFIsDismiss(t *testing.T) {
	_, u := newTestModel(t)
	close(u.eofCh)
	ans, err := u.Ask(context.Background(), askReq())
	if err != nil {
		t.Fatalf("Ask: %v", err)
	}
	if !ans.Dismissed {
		t.Fatalf("ans = %+v, want Dismissed", ans)
	}
}

// TestAskContextCancel 取消本轮上抛 ctx 错误（与「忽略」区分）。
func TestAskContextCancel(t *testing.T) {
	_, u := newTestModel(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := u.Ask(ctx, askReq()); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}

// TestAskSingleTrimsExtra 单选给多个编号：只取第一个。
func TestAskSingleTrimsExtra(t *testing.T) {
	_, u := newTestModel(t)
	u.inCh <- port.UserInput{Text: "2 1"}
	req := port.AskRequest{Questions: []port.AskQuestion{{
		Header: "One", Question: "Pick one",
		Options: []port.AskOption{{Label: "a"}, {Label: "b"}},
	}}}
	ans, err := u.Ask(context.Background(), req)
	if err != nil {
		t.Fatalf("Ask: %v", err)
	}
	if got := ans.Answers[0]; len(got) != 1 || got[0] != "b" {
		t.Fatalf("answers = %v, want [b]", got)
	}
}
