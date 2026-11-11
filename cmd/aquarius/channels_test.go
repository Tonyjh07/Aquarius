package main

// 确认 / 提问通道装配测试（D185 口径二/六）：无人值守只管模型链、用户面照常问前端、
// 开关热生效（装饰器每次调用现取）。

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

func (s *stubFront) Confirm(_ context.Context, req port.ConfirmRequest) (port.ConfirmAnswer, error) {
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
	t.Parallel()
	front := &stubFront{}
	on := true
	tc := toolChannelsFor(front, func() bool { return on }, nil)

	ans, err := tc.toolConfirmer.Confirm(context.Background(), port.ConfirmRequest{Prompt: "delete?"})
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
	if ans, err := tc.confirmer.Confirm(context.Background(), port.ConfirmRequest{Prompt: "rm?"}); err != nil || !ans.Allow || ans.Reason != "from-ui" {
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
	t.Parallel()
	front := &stubFront{}
	on := false
	tc := toolChannelsFor(front, func() bool { return on }, nil)
	if ans, err := tc.toolConfirmer.Confirm(context.Background(), port.ConfirmRequest{Prompt: "p"}); err != nil || ans.Reason != "from-ui" {
		t.Fatalf("关闭态应透传前端: %+v, %v", ans, err)
	}
	on = true
	if ans, err := tc.toolConfirmer.Confirm(context.Background(), port.ConfirmRequest{Prompt: "p"}); err != nil || ans.Allow {
		t.Fatalf("开启后应即拒绝: %+v, %v", ans, err)
	}
}

// TestToolChannelsNilUI 无前端：用户面留空（nil），模型链关闭态 fail-closed 报错。
func TestToolChannelsNilUI(t *testing.T) {
	t.Parallel()
	tc := toolChannelsFor(nil, func() bool { return false }, nil)
	if tc.confirmer != nil || tc.asker != nil {
		t.Fatal("无前端时用户面应为 nil")
	}
	if _, err := tc.toolConfirmer.Confirm(context.Background(), port.ConfirmRequest{Prompt: "p"}); err == nil {
		t.Fatal("无前端 + 关闭态应 fail-closed 报错")
	}
	if _, err := tc.toolAsker.Ask(context.Background(), port.AskRequest{}); err == nil {
		t.Fatal("无前端 + 关闭态提问应报错")
	}
}

// TestToolChannelsCancelled ctx 取消优先（中断不该被当成一次拒绝结论）。
func TestToolChannelsCancelled(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	tc := toolChannelsFor(&stubFront{}, func() bool { return true }, nil)
	if _, err := tc.toolConfirmer.Confirm(ctx, port.ConfirmRequest{Prompt: "p"}); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}

// TestToolChannelsDeciderInsideUnattended 决策模型罩在无人值守**之内**：无人值守开着
// 时，模型说「可以」也不能放行——那一条说的是「没人可批」，不是「可以自作主张」。
func TestToolChannelsDeciderInsideUnattended(t *testing.T) {
	t.Parallel()
	front := &stubFront{}
	wrapCalls := 0
	wrap := func(inner port.Confirmer) port.Confirmer {
		wrapCalls++
		return autoAllow{inner: inner}
	}
	tc := toolChannelsFor(front, func() bool { return true }, wrap)
	if wrapCalls != 1 || tc.toolConfirmer == nil {
		t.Fatalf("wrap 调用 %d 次", wrapCalls)
	}
	ans, err := tc.toolConfirmer.Confirm(context.Background(), port.ConfirmRequest{
		Origin: port.OriginTool, Prompt: "p",
	})
	if err != nil {
		t.Fatalf("confirm: %v", err)
	}
	if ans.Allow {
		t.Fatal("无人值守必须压过自动放行")
	}
	if front.confirms != 0 {
		t.Fatalf("也不该走到前端（调用 %d 次）", front.confirms)
	}
	// 用户面（`/rm` 之类）不经过决策模型。
	if ans := mustConfirm(t, tc.confirmer); ans.Reason != "from-ui" {
		t.Fatalf("用户面 = %+v", ans)
	}
}

// autoAllow 永远放行的确认器，用来模拟决策模型判了 allow。
type autoAllow struct{ inner port.Confirmer }

func (a autoAllow) Confirm(context.Context, port.ConfirmRequest) (port.ConfirmAnswer, error) {
	return port.ConfirmAnswer{Allow: true}, nil
}

func mustConfirm(t *testing.T, c port.Confirmer) port.ConfirmAnswer {
	t.Helper()
	ans, err := c.Confirm(context.Background(), port.ConfirmRequest{Prompt: "p"})
	if err != nil {
		t.Fatalf("confirm: %v", err)
	}
	return ans
}
