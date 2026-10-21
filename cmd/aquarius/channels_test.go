package main

// 确认 / 提问通道装配测试（D185 口径二/三/六）：无人值守只管模型链、-yes 优先且不装
// 装饰器、开关热生效（装饰器每次调用现取）。

import (
	"context"
	"errors"
	"testing"

	"github.com/Tonyjh07/Aquarius/internal/port"
)

// stubFront 前端交互面的最小替身：确认与提问都回一个可辨识的结论，便于断言「走到了
// 哪条链」。
type stubFront struct {
	uiFrontend
	confirms int
	asks     int
}

func (s *stubFront) Confirm(context.Context, string) (port.ConfirmAnswer, error) {
	s.confirms++
	return port.ConfirmAnswer{Allow: true, Reason: "from-ui"}, nil
}

func (s *stubFront) Ask(context.Context, port.AskRequest) (port.AskAnswer, error) {
	s.asks++
	return port.AskAnswer{Answers: [][]string{{"from-ui"}}}, nil
}

// TestToolChannelsUnattendedScopedToTools 开关打开：模型链当场拒绝、不调前端；用户面
// 仍问前端（口径二：`/rm` 这类命令确认照常询问）。
func TestToolChannelsUnattendedScopedToTools(t *testing.T) {
	front := &stubFront{}
	on := true
	tc := toolChannelsFor(front, false, func() bool { return on })

	ans, err := tc.toolConfirmer.Confirm(context.Background(), "delete?")
	if err != nil {
		t.Fatalf("tool confirm: %v", err)
	}
	if ans.Allow {
		t.Fatal("无人值守应拒绝模型发起的越界调用")
	}
	if front.confirms != 0 {
		t.Fatalf("无人值守不应调前端（调用 %d 次）", front.confirms)
	}
	ask, err := tc.toolAsker.Ask(context.Background(), port.AskRequest{})
	if err != nil || !ask.Away {
		t.Fatalf("tool ask = %+v, %v；应回 Away", ask, err)
	}
	if front.asks != 0 {
		t.Fatalf("无人值守不应调前端提问（调用 %d 次）", front.asks)
	}

	// 用户面照常询问。
	if ans, err := tc.confirmer.Confirm(context.Background(), "rm?"); err != nil || !ans.Allow || ans.Reason != "from-ui" {
		t.Fatalf("用户面确认 = %+v, %v", ans, err)
	}
	if got, err := tc.asker.Ask(context.Background(), port.AskRequest{}); err != nil || len(got.Answers) != 1 {
		t.Fatalf("用户面提问 = %+v, %v", got, err)
	}
	if front.confirms != 1 || front.asks != 1 {
		t.Fatalf("前端调用 = %d/%d, want 1/1", front.confirms, front.asks)
	}
}

// TestToolChannelsUnattendedHot 关闭时两条链都透传前端；开关一翻即生效（不重新装配）。
func TestToolChannelsUnattendedHot(t *testing.T) {
	front := &stubFront{}
	on := false
	tc := toolChannelsFor(front, false, func() bool { return on })
	if ans, err := tc.toolConfirmer.Confirm(context.Background(), "p"); err != nil || ans.Reason != "from-ui" {
		t.Fatalf("关闭态应透传前端: %+v, %v", ans, err)
	}
	on = true
	if ans, err := tc.toolConfirmer.Confirm(context.Background(), "p"); err != nil || ans.Allow {
		t.Fatalf("开启后应即拒绝: %+v, %v", ans, err)
	}
}

// TestToolChannelsAutoYes 口径三：-yes 走自动允许，**不装**装饰器（开关即便开着也放行）。
func TestToolChannelsAutoYes(t *testing.T) {
	front := &stubFront{}
	tc := toolChannelsFor(front, true, func() bool { return true })
	for name, ans := range map[string]port.ConfirmAnswer{
		"tool": mustConfirm(t, tc.toolConfirmer),
		"user": mustConfirm(t, tc.confirmer),
	} {
		if !ans.Allow {
			t.Fatalf("%s 链在 -yes 下应自动允许: %+v", name, ans)
		}
	}
	if front.confirms != 0 {
		t.Fatal("-yes 不应打扰前端")
	}
	// 提问同款：无人应答（Away），不阻塞。
	for name, ask := range map[string]port.AskAnswer{
		"tool": mustAsk(t, tc.toolAsker),
		"user": mustAsk(t, tc.asker),
	} {
		if !ask.Away || ask.Dismissed {
			t.Fatalf("%s 链在 -yes 下应回 Away: %+v", name, ask)
		}
	}
	if front.asks != 0 {
		t.Fatal("-yes 不应打扰前端")
	}
}

// TestToolChannelsNilUI 无前端：用户面留空（nil），模型链关闭态 fail-closed 报错。
func TestToolChannelsNilUI(t *testing.T) {
	tc := toolChannelsFor(nil, false, func() bool { return false })
	if tc.confirmer != nil || tc.asker != nil {
		t.Fatal("无前端时用户面应为 nil")
	}
	if _, err := tc.toolConfirmer.Confirm(context.Background(), "p"); err == nil {
		t.Fatal("无前端 + 关闭态应 fail-closed 报错")
	}
	if _, err := tc.toolAsker.Ask(context.Background(), port.AskRequest{}); err == nil {
		t.Fatal("无前端 + 关闭态提问应报错")
	}
}

// TestToolChannelsCancelled ctx 取消优先（中断不该被当成一次拒绝结论）。
func TestToolChannelsCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	tc := toolChannelsFor(&stubFront{}, false, func() bool { return true })
	if _, err := tc.toolConfirmer.Confirm(ctx, "p"); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}

func mustConfirm(t *testing.T, c port.Confirmer) port.ConfirmAnswer {
	t.Helper()
	ans, err := c.Confirm(context.Background(), "p")
	if err != nil {
		t.Fatalf("confirm: %v", err)
	}
	return ans
}

func mustAsk(t *testing.T, a port.Asker) port.AskAnswer {
	t.Helper()
	ans, err := a.Ask(context.Background(), port.AskRequest{})
	if err != nil {
		t.Fatalf("ask: %v", err)
	}
	return ans
}
