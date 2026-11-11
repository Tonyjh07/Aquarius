package main

// 决策模型的装配测试（D290）：off 不建客户端、没配全只降级不挡启动、建出来之后确实
// 罩在确认链上并且能放行。

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Tonyjh07/Aquarius/internal/adapter/decorate"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

// onPolicy 一份开了的默认阈值（装配根拿到的就是这个，测试也要照填——零值阈值等于
// 「什么都不放行」）。
func onPolicy() decorate.DecidePolicy {
	return decorate.DecidePolicy{
		Mode: decorate.DecideOn, Timeout: 5 * time.Second,
		AllowRiskBelow:  decorate.DefaultAllowRiskBelow,
		AllowHumanBelow: decorate.DefaultAllowHumanBelow,
		DenyHumanAbove:  decorate.DefaultDenyHumanAbove,
	}
}

// countingConfirmer 记录内层确认器被调用几次。
type countingConfirmer struct {
	calls int
	class port.ConfirmAnswer
}

func (c *countingConfirmer) Confirm(context.Context, port.ConfirmRequest) (port.ConfirmAnswer, error) {
	c.calls++
	return c.class, nil
}

// okEndpoint 一个正常的 System One 假端点：三题都给「明显该批」的分数。
func okEndpoint(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"model":"fake","answers":{
			"should_allow":{"type":"noul","noul":0.92},
			"risk":{"type":"score","score":0.04},
			"needs_human":{"type":"noul","noul":0.03}}}`)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// TestBuildDeciderOffDoesNothing off 档不建客户端，wrapDecide 原样返回。
func TestBuildDeciderOffDoesNothing(t *testing.T) {
	t.Parallel()
	w := &wiring{decide: decideOptions{Policy: decorate.DecidePolicy{Mode: decorate.DecideOff}}}
	w.buildDecider()
	if w.decider != nil {
		t.Fatal("off 档不该建客户端")
	}
	inner := &countingConfirmer{class: port.ConfirmAnswer{Allow: false}}
	if got := w.wrapDecide(inner); got != port.Confirmer(inner) {
		t.Fatal("没建客户端时应原样返回内层确认器")
	}
}

// TestBuildDeciderMissingEndpointDegrades 配了模式但没配端点：不建客户端，留一行错。
func TestBuildDeciderMissingEndpointDegrades(t *testing.T) {
	t.Parallel()
	w := &wiring{decide: decideOptions{Policy: onPolicy()}}
	w.buildDecider()
	if w.decider != nil {
		t.Fatal("端点缺失不该建出客户端")
	}
	if len(w.pendingLog) != 1 {
		t.Fatalf("应留一行降级日志，实际 %d 条", len(w.pendingLog))
	}
	if w.pendingLog[0].event != "log.perm.decide_unconfigured" {
		t.Fatalf("日志事件 = %q", w.pendingLog[0].event)
	}
}

// TestDeciderAllowsWithoutAsking 建好之后：判 allow 的调用不惊动内层确认器。
func TestDeciderAllowsWithoutAsking(t *testing.T) {
	t.Parallel()
	srv := okEndpoint(t)
	w := &wiring{decide: decideOptions{
		Policy:  onPolicy(),
		BaseURL: srv.URL, Model: "fake", TaskChars: 80,
	}}
	w.buildDecider()
	if w.decider == nil {
		t.Fatalf("应建出客户端: %+v", w.pendingLog)
	}
	inner := &countingConfirmer{class: port.ConfirmAnswer{Allow: false, Reason: "asked"}}
	ans, err := w.wrapDecide(inner).Confirm(context.Background(), port.ConfirmRequest{
		Origin: port.OriginTool, Tool: "file_read", HasRisk: true,
	})
	if err != nil {
		t.Fatalf("Confirm: %v", err)
	}
	if !ans.Allow || inner.calls != 0 {
		t.Fatalf("应直接放行且不问用户: %+v, calls=%d", ans, inner.calls)
	}
}

// TestDeciderFailureFallsBackToUser 端点挂了照旧问人——这是整个功能最重要的一条。
func TestDeciderFailureFallsBackToUser(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
	}))
	defer srv.Close()
	w := &wiring{decide: decideOptions{
		Policy:  onPolicy(),
		BaseURL: srv.URL, Model: "fake", TaskChars: 80,
	}}
	w.buildDecider()
	inner := &countingConfirmer{class: port.ConfirmAnswer{Allow: false, Reason: "asked"}}
	ans, err := w.wrapDecide(inner).Confirm(context.Background(), port.ConfirmRequest{
		Origin: port.OriginTool, Tool: "file_write", HasRisk: true,
	})
	if err != nil {
		t.Fatalf("端点报错不该上抛: %v", err)
	}
	if ans.Allow || inner.calls != 1 {
		t.Fatalf("失败应回到确认器: %+v, calls=%d", ans, inner.calls)
	}
}
