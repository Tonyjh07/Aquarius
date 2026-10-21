package decorate

// 无人值守装饰器单测（D185 口径一/三/六）：开 = 不调内层且原因稳定、关 = 透传、
// ctx 取消优先——这几条是「不阻塞轮次」与「权限边界不变」的全部可执行口径。

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Tonyjh07/Aquarius/internal/port"
)

// countingConf 记录被调用次数的确认器替身。
type countingConf struct {
	calls  int
	answer port.ConfirmAnswer
	err    error
}

func (c *countingConf) Confirm(_ context.Context, _ string) (port.ConfirmAnswer, error) {
	c.calls++
	return c.answer, c.err
}

// countingAsker 记录被调用次数的提问器替身。
type countingAsker struct {
	calls  int
	answer port.AskAnswer
	err    error
}

func (a *countingAsker) Ask(_ context.Context, _ port.AskRequest) (port.AskAnswer, error) {
	a.calls++
	return a.answer, a.err
}

// TestUnattendedDeniesWithoutInner 开启时不调内层、返回拒绝 + 稳定原因。
func TestUnattendedDeniesWithoutInner(t *testing.T) {
	inner := &countingConf{answer: port.ConfirmAnswer{Allow: true}}
	c := Unattended(inner, func() bool { return true })
	ans, err := c.Confirm(context.Background(), "delete 3 files?")
	if err != nil {
		t.Fatalf("Confirm: %v", err)
	}
	if ans.Allow {
		t.Fatal("无人值守必须拒绝（口径一：拒绝而非放行）")
	}
	if ans.Reason != UnattendedReason {
		t.Fatalf("Reason = %q, want %q", ans.Reason, UnattendedReason)
	}
	if inner.calls != 0 {
		t.Fatalf("内层被调用 %d 次，应为 0", inner.calls)
	}
	// 原因要覆盖三件事，否则模型会反复撞同一面墙（口径一）。
	for _, want := range []string{"unattended mode", "Do not retry", "permission level allows", "needs approval"} {
		if !strings.Contains(UnattendedReason, want) {
			t.Fatalf("原因缺 %q: %q", want, UnattendedReason)
		}
	}
}

// TestUnattendedPassThrough 关闭时原样透传（含内层错误）。
func TestUnattendedPassThrough(t *testing.T) {
	inner := &countingConf{answer: port.ConfirmAnswer{Allow: true}}
	c := Unattended(inner, func() bool { return false })
	ans, err := c.Confirm(context.Background(), "p")
	if err != nil || !ans.Allow {
		t.Fatalf("透传 = %+v, %v", ans, err)
	}
	if inner.calls != 1 {
		t.Fatalf("内层调用次数 = %d, want 1", inner.calls)
	}
	// on == nil 视为关闭（装配根未接线时不静默拒绝）。
	inner2 := &countingConf{answer: port.ConfirmAnswer{Allow: false, Reason: "不"}}
	ans, err = Unattended(inner2, nil).Confirm(context.Background(), "p")
	if err != nil || ans.Allow || ans.Reason != "不" {
		t.Fatalf("on=nil 透传 = %+v, %v", ans, err)
	}
	// 内层错误原样上抛。
	boom := errors.New("boom")
	_, err = Unattended(&countingConf{err: boom}, func() bool { return false }).Confirm(context.Background(), "p")
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, want %v", err, boom)
	}
}

// TestUnattendedContextFirst ctx 取消优先于开关：中断的轮次归为取消，不是「拒绝」结论。
func TestUnattendedContextFirst(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	ans, err := Unattended(&countingConf{}, func() bool { return true }).Confirm(ctx, "p")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if ans.Allow || ans.Reason != "" {
		t.Fatalf("取消时不应带拒绝结论: %+v", ans)
	}
	// 关闭态同款：取消也不该落到内层。
	inner := &countingConf{}
	_, err = Unattended(inner, func() bool { return false }).Confirm(ctx, "p")
	if !errors.Is(err, context.Canceled) || inner.calls != 0 {
		t.Fatalf("err = %v, calls = %d", err, inner.calls)
	}
}

// TestUnattendedNoInnerFailClosed 无内层且关闭：报错而非静默放行。
func TestUnattendedNoInnerFailClosed(t *testing.T) {
	if _, err := Unattended(nil, func() bool { return false }).Confirm(context.Background(), "p"); err == nil {
		t.Fatal("无内层确认器应报错（fail-closed）")
	}
}

// TestUnattendedAskerAway 开启时回填 Away（不是 Dismissed——口径六）。
func TestUnattendedAskerAway(t *testing.T) {
	inner := &countingAsker{answer: port.AskAnswer{Answers: [][]string{{"A"}}}}
	a := UnattendedAsker(inner, func() bool { return true })
	ans, err := a.Ask(context.Background(), port.AskRequest{})
	if err != nil {
		t.Fatalf("Ask: %v", err)
	}
	if !ans.Away {
		t.Fatal("无人值守应回填 Away")
	}
	if ans.Dismissed || len(ans.Answers) != 0 {
		t.Fatalf("应是「用户不在」，不是「忽略」或编造答案: %+v", ans)
	}
	if inner.calls != 0 {
		t.Fatalf("内层被调用 %d 次，应为 0", inner.calls)
	}
}

// TestUnattendedAskerPassThrough 关闭/取消/无内层三条旁路。
func TestUnattendedAskerPassThrough(t *testing.T) {
	inner := &countingAsker{answer: port.AskAnswer{Answers: [][]string{{"A"}}}}
	ans, err := UnattendedAsker(inner, func() bool { return false }).Ask(context.Background(), port.AskRequest{})
	if err != nil || len(ans.Answers) != 1 || inner.calls != 1 {
		t.Fatalf("透传 = %+v, %v, calls=%d", ans, err, inner.calls)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := UnattendedAsker(&countingAsker{}, func() bool { return true }).Ask(ctx, port.AskRequest{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if _, err := UnattendedAsker(nil, func() bool { return false }).Ask(context.Background(), port.AskRequest{}); err == nil {
		t.Fatal("无内层提问器应报错（fail-closed）")
	}
}
