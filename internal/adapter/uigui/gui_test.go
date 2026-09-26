package uigui

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/Tonyjh07/Aquarius/internal/domain/conversation"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

// newHeadless 无窗口前端（§15.5：桥接层 headless 逻辑测试，无帧循环/无窗口）。
func newHeadless(t *testing.T, opts Options) *UI {
	t.Helper()
	u := newUI(opts, false)
	t.Cleanup(func() { _ = u.Close() })
	return u
}

// drainSync 等待此前所有 post 应用进状态机（drain → close 提供 happens-before，
// 之后读 u.m 与帧循环无竞争）。
func drainSync(t *testing.T, u *UI) {
	t.Helper()
	done := make(chan struct{})
	if !u.post(drainMsg{done: done}) {
		t.Fatal("事件循环已退出")
	}
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("drain 超时：事件循环未推进")
	}
}

// ctx5 带超时的上下文（防桥接 bug 把测试挂死）。
func ctx5(t *testing.T) (context.Context, context.CancelFunc) {
	t.Helper()
	return context.WithTimeout(context.Background(), 5*time.Second)
}

// TestEmitAndSayApplied Emit/Say 从外部 goroutine 投递 → 状态机可见
// （流式草稿、committed 定稿替换、纯文本行）。
func TestEmitAndSayApplied(t *testing.T) {
	u := newHeadless(t, Options{})
	ctx := context.Background()

	if err := u.Emit(ctx, port.DeltaEvent{Delta: port.Delta{Text: "流式中"}}); err != nil {
		t.Fatalf("Emit: %v", err)
	}
	if err := u.Emit(ctx, port.CommittedEvent{Message: conversation.Message{
		Role:    conversation.RoleAssistant,
		Outcome: conversation.OutcomeDone,
		Content: []conversation.Part{{Kind: conversation.PartText, Text: "最终答案"}},
	}}); err != nil {
		t.Fatalf("Emit: %v", err)
	}
	u.Say("启动提示")
	drainSync(t, u)

	if u.m.draft.Len() != 0 || u.m.drafting {
		t.Fatalf("committed 后草稿应清: %q", u.m.draft.String())
	}
	if len(u.m.blocks) != 2 {
		t.Fatalf("blocks = %+v, want 定稿 + plain", u.m.blocks)
	}
	if u.m.blocks[0].kind != blockAssistant || !strings.Contains(u.m.blocks[0].text, "最终答案") {
		t.Fatalf("blocks[0] = %+v", u.m.blocks[0])
	}
	if u.m.blocks[1].kind != blockPlain || !strings.Contains(u.m.blocks[1].text, "启动提示") {
		t.Fatalf("blocks[1] = %+v", u.m.blocks[1])
	}
}

// TestSayEmptyIgnored 空文本不入转写（与 uitui 同口径）。
func TestSayEmptyIgnored(t *testing.T) {
	u := newHeadless(t, Options{})
	u.Say("")
	drainSync(t, u)
	if len(u.m.blocks) != 0 {
		t.Fatalf("blocks = %+v, want 空", u.m.blocks)
	}
}

// TestNextDrainsQueuedInputBeforeEOF 排队输入必须先于 EOF 交付——EOF 是 close
// 广播（粘滞+同时可见），旧一次性标记会被随机 select 吞掉（uitui 审查修复同款）；
// 此后 Next 持续返回 EOF。
func TestNextDrainsQueuedInputBeforeEOF(t *testing.T) {
	u := newHeadless(t, Options{})
	u.post(inputMsg{text: "第一行"})
	u.post(inputMsg{text: "/quit"})
	u.post(eofMsg{})
	drainSync(t, u)

	ctx, cancel := ctx5(t)
	defer cancel()
	in, err := u.Next(ctx)
	if err != nil || in.Text != "第一行" {
		t.Fatalf("first = %+v, %v, want 第一行", in, err)
	}
	in, err = u.Next(ctx)
	if err != nil || in.Command == nil || in.Command.Name != "quit" {
		t.Fatalf("second = %+v, %v, want /quit", in, err)
	}
	for i := 0; i < 3; i++ {
		if _, err := u.Next(ctx); !errors.Is(err, io.EOF) {
			t.Fatalf("第 %d 次 Next = %v, want 持续 EOF", i+3, err)
		}
	}
}

// TestNextSurfacesError eofMsg 携带的底层错误原样上抛（不静默当 EOF 掩盖故障）。
func TestNextSurfacesError(t *testing.T) {
	u := newHeadless(t, Options{})
	boom := errors.New("读取输入故障")
	u.post(eofMsg{err: boom})
	drainSync(t, u)

	ctx, cancel := ctx5(t)
	defer cancel()
	if _, err := u.Next(ctx); !errors.Is(err, boom) {
		t.Fatalf("err = %v, want 扫描错误原样上抛", err)
	}
}

// TestConfirmRejectsOnEOF 输入流结束时确认不得永久挂起——返回拒绝（对齐 repl
// "不替用户做破坏性决定"），并同步收尾确认态；随后 Next 仍见 EOF。
func TestConfirmRejectsOnEOF(t *testing.T) {
	u := newHeadless(t, Options{})
	u.post(eofMsg{})
	drainSync(t, u)

	ctx, cancel := ctx5(t)
	defer cancel()
	yes, err := u.Confirm(ctx, "危险操作？")
	if err != nil || yes {
		t.Fatalf("confirm = %v, %v, want (false, nil)", yes, err)
	}
	drainSync(t, u) // confirmResultMsg 收尾已应用
	if u.m.confirm != nil {
		t.Fatal("EOF 应答后确认态应收尾")
	}
	if _, err := u.Next(ctx); !errors.Is(err, io.EOF) {
		t.Fatalf("Next = %v, want EOF", err)
	}
}

// TestConfirmDrainsQueuedAnswer 排队输入在确认打开前已入队——Confirm 必须把它
// 当应答取走（否则与已关闭的 eofCh 同时就绪时随机选中会把 y 误拒），后续行保留给 Next。
func TestConfirmDrainsQueuedAnswer(t *testing.T) {
	u := newHeadless(t, Options{})
	u.post(inputMsg{text: "y"})
	u.post(inputMsg{text: "/quit"})
	drainSync(t, u)

	ctx, cancel := ctx5(t)
	defer cancel()
	yes, err := u.Confirm(ctx, "确认删除？")
	if err != nil || !yes {
		t.Fatalf("confirm = %v, %v, want 排队输入 y 作答为真", yes, err)
	}
	drainSync(t, u)
	if u.m.confirm != nil {
		t.Fatal("确认态应收尾")
	}
	in, err := u.Next(ctx)
	if err != nil || in.Command == nil || in.Command.Name != "quit" {
		t.Fatalf("next = %+v, %v, want /quit", in, err)
	}
}

// TestConfirmFromModelReply 模型侧应答（输入栏允许/拒绝按钮 → replyConfirm 经
// 事件循环应用）是 Confirm 的主路径。
func TestConfirmFromModelReply(t *testing.T) {
	u := newHeadless(t, Options{})
	ctx, cancel := ctx5(t)
	defer cancel()

	type result struct {
		yes bool
		err error
	}
	resCh := make(chan result, 1)
	go func() {
		yes, err := u.Confirm(ctx, "允许执行？")
		resCh <- result{yes, err}
	}()
	// Confirm 构造后立即投递 confirmMsg：墙钟余量 + 屏障（FIFO——屏障处理到即代表
	// 此前投递全部应用），保证应答不越过确认请求。
	time.Sleep(100 * time.Millisecond)
	drainSync(t, u)
	u.post(confirmResultMsg{yes: true}) // 按钮同路：事件循环上应用 replyConfirm
	drainSync(t, u)

	r := <-resCh
	if r.err != nil || !r.yes {
		t.Fatalf("confirm = %v, %v, want (true, nil)", r.yes, r.err)
	}
	if u.m.confirm != nil {
		t.Fatal("应答后确认态应清除")
	}
	if !strings.Contains(allText(u.m), "允许执行？ → true") {
		t.Fatalf("缺应答记录: %q", allText(u.m))
	}
}

// TestInterruptGenerating 生成中标志驱动停止键（§15.2）：SetInterrupt 非空 = 一轮
// Turn 进行中；nil 收轮；interruptNow 触发取消。
func TestInterruptGenerating(t *testing.T) {
	u := newHeadless(t, Options{})
	if u.generating.Load() {
		t.Fatal("初始不应为生成中")
	}
	var n int
	u.SetInterrupt(func() { n++ })
	if !u.generating.Load() {
		t.Fatal("SetInterrupt 后应为生成中")
	}
	u.interruptNow()
	if n != 1 {
		t.Fatalf("interruptNow 次数 = %d, want 1", n)
	}
	u.SetInterrupt(nil)
	if u.generating.Load() {
		t.Fatal("SetInterrupt(nil) 后应停止生成中")
	}
	u.interruptNow()
	if n != 1 {
		t.Fatalf("无轮进行时 interruptNow 应 no-op, n = %d", n)
	}
}

// TestCloseIdempotentAndPostSafe Close 幂等；循环退出后的 post 静默丢弃不阻塞。
func TestCloseIdempotentAndPostSafe(t *testing.T) {
	u := newHeadless(t, Options{})
	u.Say("在循环退出前")
	if err := u.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := u.Close(); err != nil {
		t.Fatalf("二次 Close: %v", err)
	}
	// 循环已退出：post 走 done 分支返回 false，不阻塞。
	done := make(chan struct{})
	go func() {
		u.Say("循环退出后")
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("循环退出后 post 阻塞")
	}
}

// TestStatusTextPhase 状态行（§15.1）：仅生成时显示，思考阶段显示"思考中"。
// 本测试不向 inbox 投递消息，u.m 无并发写者（t.Cleanup 的 drain 不触碰状态机字段）。
func TestStatusTextPhase(t *testing.T) {
	u := newHeadless(t, Options{
		Status: func() Status { return Status{Model: "m", Level: "strict"} },
	})
	if got := u.statusText(); got != "" {
		t.Fatalf("非生成态 statusText = %q, want 空", got)
	}
	u.SetInterrupt(func() {})
	u.m.think.WriteString("想")
	if got := u.statusText(); !strings.Contains(got, "思考中") || !strings.Contains(got, "m") || !strings.Contains(got, "strict") {
		t.Fatalf("思考阶段 statusText = %q", got)
	}
	u.m.think.Reset()
	if got := u.statusText(); !strings.Contains(got, "生成中") {
		t.Fatalf("生成阶段 statusText = %q", got)
	}
}
