package acpgate

import (
	"context"
	"strings"
	"testing"
	"time"

	acp "github.com/coder/acp-go-sdk"

	"github.com/Tonyjh07/Aquarius/internal/port"
)

// chunk 造一条 agent_message_chunk 通知，模拟 SDK 通知 goroutine 交回的正文。
func chunk(text string) acp.SessionNotification {
	return acp.SessionNotification{
		Update: acp.SessionUpdate{
			AgentMessageChunk: &acp.SessionUpdateAgentMessageChunk{Content: acp.TextBlock(text)},
		},
	}
}

// TestWaitNotifyDrainKeepsLateChunks 锁住 D268 的失败时序：Prompt 已经拿到断连 error，
// 两条正文还在 SDK 的通知队列里没交付。收尾必须等排空完成再读报告，否则报告为空。
func TestWaitNotifyDrainKeepsLateChunks(t *testing.T) {
	r := newRun("d1", Agent{Name: "fake"}, port.DelegateSpec{}, "s1")
	r.armNotifyDrain()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan struct{})
	go func() {
		r.waitNotifyDrain(2 * time.Second)
		close(done)
	}()

	// 排空信号没到之前，收尾不该往下走。
	select {
	case <-done:
		t.Fatal("通知尚未交付，收尾就继续读报告了")
	case <-time.After(30 * time.Millisecond):
	}

	cl := &client{r: r}
	for _, text := range []string{"first chunk", "second chunk"} {
		if err := cl.SessionUpdate(ctx, chunk(text)); err != nil {
			t.Fatalf("session/update: %v", err)
		}
	}
	cancel() // SDK 排空完队列之后才 cancel inboundCtx

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("通知排空之后收尾没有继续")
	}
	if got := r.reportText(); !strings.Contains(got, "first chunk") || !strings.Contains(got, "second chunk") {
		t.Fatalf("report = %q, want 两段正文都在", got)
	}
}

// TestWaitNotifyDrainSkipsBeforePrompt 起不来、握手就报错的路径没有正文可保，
// 不该为它付等待成本。
func TestWaitNotifyDrainSkipsBeforePrompt(t *testing.T) {
	r := newRun("d2", Agent{}, port.DelegateSpec{}, "s1")
	start := time.Now()
	r.waitNotifyDrain(2 * time.Second)
	if elapsed := time.Since(start); elapsed > 100*time.Millisecond {
		t.Fatalf("未进入 prompt 阶段却等了 %v", elapsed)
	}
}

// TestWaitNotifyDrainBudgetBoundsTheWait 队列本来是空的失败最多等 budget，
// SDK 语义变化也不能挂住 Wait。
func TestWaitNotifyDrainBudgetBoundsTheWait(t *testing.T) {
	r := newRun("d3", Agent{}, port.DelegateSpec{}, "s1")
	r.armNotifyDrain()
	start := time.Now()
	r.waitNotifyDrain(20 * time.Millisecond)
	if elapsed := time.Since(start); elapsed > 500*time.Millisecond {
		t.Fatalf("没有通知交付却等了 %v，超过 budget", elapsed)
	}
}
