package app

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Tonyjh07/Aquarius/internal/domain/conversation"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

// fakeProber 脚本化探针：按序返回预设结果，用完后重复最后一项。
type fakeProber struct {
	mu    sync.Mutex
	steps []probeStep
	calls int
}

type probeStep struct {
	out string
	err error
}

func (p *fakeProber) Probe(context.Context, port.WatchProbe) (port.WatchProbeResult, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	i := p.calls
	p.calls++
	if len(p.steps) == 0 {
		return port.WatchProbeResult{}, nil
	}
	if i >= len(p.steps) {
		i = len(p.steps) - 1
	}
	st := p.steps[i]
	if st.err != nil {
		return port.WatchProbeResult{}, st.err
	}
	return port.WatchProbeResult{Output: st.out}, nil
}

func (p *fakeProber) count() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls
}

// instantSleep 立刻返回（观察器测试不等真时间）；cancelAfter 次调用后返回 false 模拟取消。
func instantSleep(cancelAfter int) func(context.Context, time.Duration) bool {
	var mu sync.Mutex
	n := 0
	return func(context.Context, time.Duration) bool {
		mu.Lock()
		defer mu.Unlock()
		n++
		return cancelAfter <= 0 || n < cancelAfter
	}
}

// newTestWatcher 建观察器 + 收集器（不接唤醒，重试等待立即返回）。
func newTestWatcher(p port.WatchProber, inj *captureInjector) *Watcher {
	return NewWatcher(WatcherOptions{
		Prober: p,
		Injector: func() port.Injector {
			return inj
		},
		Sleep: instantSleep(0),
	})
}

func matchSpec() port.WatchSpec {
	return port.WatchSpec{
		Kind:      port.WatchCommand,
		Target:    "ci status",
		Match:     `passed`,
		Interval:  5 * time.Second,
		MaxChecks: 10,
		Deadline:  time.Hour,
	}
}

// TestWatcherReportsOnMatchOnly 命中才注入一条：来源 poller:<id>、宿主信任、
// 正文围栏包住探针输出；报完即结束（notify=match）。
func TestWatcherReportsOnMatchOnly(t *testing.T) {
	prober := &fakeProber{steps: []probeStep{{out: "pending"}, {out: "passed"}, {out: "passed"}}}
	inj := newCaptureInjector()
	w := newTestWatcher(prober, inj)
	defer w.Close()

	st, err := w.Start(context.Background(), matchSpec())
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if st.ID == "" {
		t.Fatal("watch id 为空")
	}
	in := waitInjection(t, inj.ch)
	if in.Source != "poller:"+string(st.ID) {
		t.Fatalf("source = %q", in.Source)
	}
	if in.Trust != port.TrustHost {
		t.Fatalf("trust = %v, want TrustHost", in.Trust)
	}
	if in.Trigger != nil {
		t.Fatal("命中报告应走来源默认触发（nil）")
	}
	body := in.Parts[0].Text
	for _, want := range []string{"matched after 2 check", `matches "passed"`, "untrusted data", "passed"} {
		if !strings.Contains(body, want) {
			t.Fatalf("body = %q, want 含 %q", body, want)
		}
	}
	// 报完即结束：列表里不该再留着。
	waitForWatch(t, "观察器结束", func() bool {
		list, _ := w.List(context.Background())
		return len(list) == 0
	})
	// 只报一次（不会因为后续取样再投）。
	if n := len(inj.items); n != 1 {
		t.Fatalf("注入 %d 条, want 1", n)
	}
	if got := prober.count(); got != 2 {
		t.Fatalf("取样 %d 次, want 2", got)
	}
}

// TestWatcherSilentUntilMatch 未命中不投递；次数用完报「观察结束，未命中」。
func TestWatcherSilentUntilMatch(t *testing.T) {
	prober := &fakeProber{steps: []probeStep{{out: "pending"}}}
	inj := newCaptureInjector()
	w := newTestWatcher(prober, inj)
	defer w.Close()

	spec := matchSpec()
	spec.MaxChecks = 3
	if _, err := w.Start(context.Background(), spec); err != nil {
		t.Fatalf("start: %v", err)
	}
	in := waitInjection(t, inj.ch) // 只有一条：结束报告
	if len(inj.items) != 1 {
		t.Fatalf("未命中期间不该注入，实收 %d 条", len(inj.items))
	}
	if !strings.Contains(in.Parts[0].Text, "ended after 3 check(s) without a match") {
		t.Fatalf("body = %q", in.Parts[0].Text)
	}
}

// TestWatcherChangeCriterion 无正则 = 输出变化：首轮只建基线，第二轮相同不算，
// 第三轮变了才算命中。
func TestWatcherChangeCriterion(t *testing.T) {
	prober := &fakeProber{steps: []probeStep{{out: "v1"}, {out: "v1"}, {out: "v2"}}}
	inj := newCaptureInjector()
	w := newTestWatcher(prober, inj)
	defer w.Close()

	spec := matchSpec()
	spec.Match = ""
	if _, err := w.Start(context.Background(), spec); err != nil {
		t.Fatalf("start: %v", err)
	}
	in := waitInjection(t, inj.ch)
	body := in.Parts[0].Text
	if !strings.Contains(body, "changed since the previous check") {
		t.Fatalf("body = %q", body)
	}
	if !strings.Contains(body, "matched after 3 check") {
		t.Fatalf("body = %q", body)
	}
}

// TestWatcherChangeNotifyKeepsWatching notify=change 每次**翻转**到命中都报，
// 直到被叫停；一直命中着不重复报。
func TestWatcherChangeNotifyKeepsWatching(t *testing.T) {
	prober := &fakeProber{steps: []probeStep{
		{out: "ci passed"}, {out: "ci pending"}, {out: "ci passed"}, {out: "ci pending"},
	}}
	inj := newCaptureInjector()
	w := NewWatcher(WatcherOptions{
		Prober:   prober,
		Injector: func() port.Injector { return inj },
		Sleep:    instantSleep(3), // 3 次取样后让出，避免无限跑
	})
	defer w.Close()

	spec := matchSpec()
	spec.Notify = port.WatchNotifyChange
	if _, err := w.Start(context.Background(), spec); err != nil {
		t.Fatalf("start: %v", err)
	}
	waitForWatch(t, "多条命中报告", func() bool {
		inj.mu.Lock()
		defer inj.mu.Unlock()
		return len(inj.items) >= 2
	})
	waitForWatch(t, "观察器停下", func() bool {
		list, _ := w.List(context.Background())
		return len(list) == 0
	})
}

// TestWatcherProbeFailureFirstAndRecovery 探针连续失败：首次报一条、中间静默、
// 恢复再报一条；两条都不触发轮次（不是命中）。
func TestWatcherProbeFailureFirstAndRecovery(t *testing.T) {
	boom := errors.New("connection refused")
	prober := &fakeProber{steps: []probeStep{
		{err: boom}, {err: boom}, {err: boom}, {out: "pending"},
	}}
	inj := newCaptureInjector()
	w := newTestWatcher(prober, inj)
	defer w.Close()

	spec := matchSpec()
	spec.MaxChecks = 4
	spec.Interval = 5 * time.Second
	if _, err := w.Start(context.Background(), spec); err != nil {
		t.Fatalf("start: %v", err)
	}
	first := waitInjection(t, inj.ch)
	if !strings.Contains(first.Parts[0].Text, "probe failed") {
		t.Fatalf("first = %q", first.Parts[0].Text)
	}
	if first.Trigger == nil || *first.Trigger {
		t.Fatal("失败报告不该触发轮次")
	}
	second := waitInjection(t, inj.ch)
	if !strings.Contains(second.Parts[0].Text, "recovered") {
		t.Fatalf("second = %q", second.Parts[0].Text)
	}
	if second.Trigger == nil || *second.Trigger {
		t.Fatal("恢复报告不该触发轮次")
	}
	// 中间那两次失败静默：只剩结束报告。
	third := waitInjection(t, inj.ch)
	if !strings.Contains(third.Parts[0].Text, "without a match") {
		t.Fatalf("third = %q", third.Parts[0].Text)
	}
	if n := len(inj.items); n != 3 {
		t.Fatalf("注入 %d 条, want 3", n)
	}
}

// TestWatcherStopAndCancelSession 叫停后不再投递；会话切换撤销该会话的观察器。
func TestWatcherStopAndCancelSession(t *testing.T) {
	prober := &fakeProber{steps: []probeStep{{out: "pending"}}}
	inj := newCaptureInjector()
	blocked := make(chan struct{})
	w := NewWatcher(WatcherOptions{
		Prober:   prober,
		Injector: func() port.Injector { return inj },
		Sleep: func(ctx context.Context, _ time.Duration) bool {
			select {
			case <-blocked:
				return false
			case <-ctx.Done():
				return false
			}
		},
	})
	defer w.Close()

	spec := matchSpec()
	ctx := port.WithSessionID(context.Background(), conversation.ID("c1"))
	st, err := w.Start(ctx, spec)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if err := w.Stop(context.Background(), st.ID); err != nil {
		t.Fatalf("stop: %v", err)
	}
	if err := w.Stop(context.Background(), st.ID); err == nil {
		t.Fatal("重复停止应报错")
	}
	waitForWatch(t, "停止后列表清空", func() bool {
		list, _ := w.List(context.Background())
		return len(list) == 0
	})

	other, err := w.Start(port.WithSessionID(context.Background(), conversation.ID("c2")), spec)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	w.CancelSession("c2")
	waitForWatch(t, "会话撤销", func() bool {
		list, _ := w.List(context.Background())
		return len(list) == 0
	})
	_ = other
	close(blocked)
	if len(inj.items) != 0 {
		t.Fatalf("叫停/撤销后不该注入，实收 %d 条", len(inj.items))
	}
}

// TestWatcherValidatesSpec 参数边界与正则校验（其他调用方也不许绕过）。
func TestWatcherValidatesSpec(t *testing.T) {
	w := newTestWatcher(&fakeProber{}, newCaptureInjector())
	defer w.Close()
	cases := []struct {
		name string
		spec port.WatchSpec
		want string
	}{
		{"kind", port.WatchSpec{Kind: "watch", Target: "x"}, "kind must be"},
		{"target", port.WatchSpec{Kind: port.WatchFile}, "missing target"},
		{"interval", port.WatchSpec{Kind: port.WatchFile, Target: "x", Interval: time.Second}, "interval must be"},
		{"regex", port.WatchSpec{Kind: port.WatchFile, Target: "x", Match: "("}, "not a valid regular expression"},
		{"notify", port.WatchSpec{Kind: port.WatchFile, Target: "x", Notify: "always"}, "notify must be"},
	}
	for _, c := range cases {
		if _, err := w.Start(context.Background(), c.spec); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Fatalf("%s: err = %v, want 含 %q", c.name, err, c.want)
		}
	}
}

// TestWatcherInjectionRetry 注入队列满时有限重试（首次失败、第二次成功仍算送达）。
func TestWatcherInjectionRetry(t *testing.T) {
	target := newCaptureInjector()
	retry := &waitingInjector{fail: 1, target: target}
	prober := &fakeProber{steps: []probeStep{{out: "passed"}}}
	w := NewWatcher(WatcherOptions{
		Prober:   prober,
		Injector: func() port.Injector { return retry },
		Sleep:    instantSleep(0),
	})
	defer w.Close()
	if _, err := w.Start(context.Background(), matchSpec()); err != nil {
		t.Fatalf("start: %v", err)
	}
	waitInjection(t, target.ch)
	retry.mu.Lock()
	calls := retry.calls
	retry.mu.Unlock()
	if calls != 2 {
		t.Fatalf("注入调用 %d 次, want 2", calls)
	}
}

// TestWatcherCloseStopsAll 关停后列表清空、不再投递。
func TestWatcherCloseStopsAll(t *testing.T) {
	prober := &fakeProber{steps: []probeStep{{out: "pending"}}}
	inj := newCaptureInjector()
	w := NewWatcher(WatcherOptions{
		Prober:   prober,
		Injector: func() port.Injector { return inj },
		Sleep: func(ctx context.Context, _ time.Duration) bool {
			<-ctx.Done()
			return false
		},
	})
	if _, err := w.Start(context.Background(), matchSpec()); err != nil {
		t.Fatalf("start: %v", err)
	}
	w.Close()
	w.Close() // 幂等
	if list, _ := w.List(context.Background()); len(list) != 0 {
		t.Fatalf("关停后仍有 %d 条", len(list))
	}
	if _, err := w.Start(context.Background(), matchSpec()); err == nil {
		t.Fatal("关停后不应再接受新观察器")
	}
}

// waitForWatch 轮询条件成立（不用硬 sleep）。
func waitForWatch(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("等待 %s 超时", what)
}
