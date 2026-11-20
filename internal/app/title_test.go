package app

// title_test.go 会话标题由模型生成（D309）
//
// 三段逻辑各守一条：什么时候问（首轮、链上认、标题没被手动改过）、拿到什么算数
// （引号与换行收拾干净、过长截断、空串当没拿到）、写回时谁优先（手动的永不覆盖）。

import (
	"context"
	"strings"
	"testing"

	"github.com/Tonyjh07/Aquarius/internal/domain/conversation"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

// TestTitleEditable 只有「还是默认标题」或「还是首条消息的摘录」才允许被模型标题改写。
func TestTitleEditable(t *testing.T) {
	c := newConv(t) // Root + 一条 user "hi"
	if c.Title = "hi"; !titleEditable(c) {
		t.Fatal("首条消息的摘录应可改写")
	}
	c.Title = ""
	if !titleEditable(c) {
		t.Fatal("空标题应可改写")
	}
	c.Title = "我自己起的"
	if titleEditable(c) {
		t.Fatal("/title 起过的名字不该被改写")
	}
}

// TestFirstUserTurn 判据是「路径上一条用户消息」，不是路径长度（persona 节点占位）。
func TestFirstUserTurn(t *testing.T) {
	c := newConv(t)
	if !firstUserTurn(c) {
		t.Fatal("只有一条用户消息时应算首轮")
	}
	if _, err := c.Append(conversation.RoleAssistant, []conversation.Part{{Kind: conversation.PartText, Text: "ok"}}); err != nil {
		t.Fatalf("append: %v", err)
	}
	if _, err := c.Append(conversation.RoleUser, []conversation.Part{{Kind: conversation.PartText, Text: "再来"}}); err != nil {
		t.Fatalf("append: %v", err)
	}
	if firstUserTurn(c) {
		t.Fatal("第二条用户消息之后不算首轮")
	}
}

// TestNormalizeModelTitle 收拾服务端给的标题：引号、换行、超长、空串。
func TestNormalizeModelTitle(t *testing.T) {
	cases := []struct{ in, want string }{
		{"  北京时间  ", "北京时间"},
		{"「调试报错」", "调试报错"},
		{`"quoted"`, "quoted"},
		{"第一行\n第二行", "第一行 第二行"},
		{"", ""},
		{"   ", ""},
	}
	for _, c := range cases {
		if got := normalizeModelTitle(c.in); got != c.want {
			t.Fatalf("normalizeModelTitle(%q) = %q, want %q", c.in, got, c.want)
		}
	}
	long := strings.Repeat("长", titleRunes+5)
	got := normalizeModelTitle(long)
	if r := []rune(got); len(r) != titleRunes+1 || !strings.HasSuffix(got, "…") {
		t.Fatalf("超长标题应截断到 %d 字加省略号，实际 %q", titleRunes, got)
	}
}

// TestBuildRequestTitleGate 请求里带不带 title 的四道闸门。
func TestBuildRequestTitleGate(t *testing.T) {
	ctx := context.Background()
	build := func(t *testing.T, cfg Config) port.GenerateRequest {
		t.Helper()
		a := newAgent(t, &scriptLLM{t: t}, &recorder{}, Deps{}, cfg)
		c := newConv(t)
		c.Title = autoTitleFrom(c) // maybeSetTitle 之后的状态
		req, err := a.buildRequest(ctx, c, nil)
		if err != nil {
			t.Fatalf("buildRequest: %v", err)
		}
		return req
	}

	on := Config{TitleEnabled: true, ServerCaps: port.ServerCaps{Title: true}}
	if !build(t, on).Title {
		t.Fatal("首轮、开关开、端点认：应带 title")
	}
	if build(t, Config{TitleEnabled: false, ServerCaps: port.ServerCaps{Title: true}}).Title {
		t.Fatal("开关关着：不该带 title")
	}
	if build(t, Config{TitleEnabled: true}).Title {
		t.Fatal("端点不认：不该带 title")
	}

	// 第二条用户消息之后不再问。
	a := newAgent(t, &scriptLLM{t: t}, &recorder{}, Deps{}, on)
	c := newConv(t)
	c.Title = autoTitleFrom(c)
	if _, err := c.Append(conversation.RoleAssistant, []conversation.Part{{Kind: conversation.PartText, Text: "ok"}}); err != nil {
		t.Fatalf("append: %v", err)
	}
	if _, err := c.Append(conversation.RoleUser, []conversation.Part{{Kind: conversation.PartText, Text: "再来"}}); err != nil {
		t.Fatalf("append: %v", err)
	}
	if req, err := a.buildRequest(ctx, c, nil); err != nil || req.Title {
		t.Fatalf("第二轮不该带 title：title=%v err=%v", req.Title, err)
	}
}

// TestApplyModelTitle 写回：自动标题被换掉，手动命名的不动。
func TestApplyModelTitle(t *testing.T) {
	s, _, _ := newTestSession(t, newMemStore())
	appendUserText(t, s, "帮我看看这个报错")
	s.cur.Title = truncateTitle("帮我看看这个报错") // maybeSetTitle 的效果

	s.applyModelTitle("「调试报错」")
	if s.cur.Title != "调试报错" {
		t.Fatalf("自动标题应被改写，实际 %q", s.cur.Title)
	}

	s.cur.Title = "我自己起的"
	s.applyModelTitle("别的标题")
	if s.cur.Title != "我自己起的" {
		t.Fatalf("手动标题被覆盖了：%q", s.cur.Title)
	}

	s.cur.Title = truncateTitle("帮我看看这个报错")
	s.applyModelTitle("   ")
	if s.cur.Title != truncateTitle("帮我看看这个报错") {
		t.Fatalf("空标题应回落原摘录，实际 %q", s.cur.Title)
	}
}

// TestTakeTitle 取走即清空：同一轮的标题不会落到下一轮。
func TestTakeTitle(t *testing.T) {
	a := newAgent(t, &scriptLLM{t: t}, &recorder{}, Deps{}, Config{})
	a.genTitle.Store("北京天气")
	if got := a.TakeTitle(); got != "北京天气" {
		t.Fatalf("TakeTitle = %q", got)
	}
	if got := a.TakeTitle(); got != "" {
		t.Fatalf("第二次应为空，实际 %q", got)
	}
}
