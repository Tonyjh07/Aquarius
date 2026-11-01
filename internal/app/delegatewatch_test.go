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

// scriptedDelegate port.Delegate 替身：Wait 阻塞在 release 上，测试决定何时收尾。
type scriptedDelegate struct {
	mu      sync.Mutex
	waits   map[port.DelegateID]chan struct{}
	runs    map[port.DelegateID]port.DelegateRun
	errs    map[port.DelegateID]error
	started []port.DelegateSpec
	killed  []port.DelegateID
	killErr error
}

func newScriptedDelegate() *scriptedDelegate {
	return &scriptedDelegate{
		waits: map[port.DelegateID]chan struct{}{},
		runs:  map[port.DelegateID]port.DelegateRun{},
		errs:  map[port.DelegateID]error{},
	}
}

func (d *scriptedDelegate) List(context.Context) ([]port.DelegateInfo, error) {
	return []port.DelegateInfo{{Name: "fake", Available: true}}, nil
}

func (d *scriptedDelegate) Start(_ context.Context, spec port.DelegateSpec) (port.DelegateTask, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	id := port.DelegateID("d" + string(rune('0'+len(d.started)+1)))
	d.started = append(d.started, spec)
	d.waits[id] = make(chan struct{})
	return port.DelegateTask{ID: id, Agent: "fake"}, nil
}

// finish 让一次转交进入终态并唤醒 Wait。
func (d *scriptedDelegate) finish(id port.DelegateID, run port.DelegateRun, err error) {
	d.mu.Lock()
	d.runs[id] = run
	d.errs[id] = err
	ch, ok := d.waits[id]
	if !ok {
		ch = make(chan struct{})
		d.waits[id] = ch
	}
	close(ch)
	d.mu.Unlock()
}

func (d *scriptedDelegate) Wait(ctx context.Context, id port.DelegateID) (port.DelegateRun, error) {
	d.mu.Lock()
	ch, ok := d.waits[id]
	if !ok {
		ch = make(chan struct{})
		d.waits[id] = ch
	}
	d.mu.Unlock()
	select {
	case <-ch:
	case <-ctx.Done():
		return port.DelegateRun{}, ctx.Err()
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.runs[id], d.errs[id]
}

func (d *scriptedDelegate) Kill(_ context.Context, id port.DelegateID) error {
	if d.killErr != nil {
		return d.killErr
	}
	d.mu.Lock()
	d.killed = append(d.killed, id)
	d.mu.Unlock()
	return nil
}

// TestDelegateWatcherReportsTerminal 报告按归属落在发起会话：来源 delegate:<名>、
// Trust=External（正文是外部代理写的）、摘要行 + 原文，投递后敲一次唤醒。
func TestDelegateWatcherReportsTerminal(t *testing.T) {
	d := newScriptedDelegate()
	ci := newCaptureInjector()
	woken := make(chan struct{}, 1)
	w := NewDelegateWatcher(DelegateWatcherOptions{
		Delegates: d,
		Injector:  func() port.Injector { return ci },
		Wake:      func() { woken <- struct{}{} },
	})
	t.Cleanup(w.Close)

	w.Watch(port.DelegateTask{ID: "d1", Agent: "claude"}, "c1")
	d.finish("d1", port.DelegateRun{
		Agent: "claude", StopReason: "end_turn", ToolCalls: 7,
		Changed: []string{"/tmp/a.go"}, Report: "done: refactored the parser",
		Notes: []string{"permission denied: 用户拒绝"}, Duration: 3 * time.Minute,
	}, nil)

	in := waitInjection(t, ci.ch)
	if in.Source != "delegate:claude" {
		t.Fatalf("source = %q", in.Source)
	}
	if in.Trust != port.TrustExternal {
		t.Fatalf("trust = %v, want External（正文是外部代理写的）", in.Trust)
	}
	if in.Session != "c1" {
		t.Fatalf("session = %q, want 发起会话", in.Session)
	}
	text := in.Parts[0].Text
	for _, want := range []string{"Delegation d1 to claude", "tool calls=7", "files changed=1", "permission denied", "done: refactored the parser"} {
		if !strings.Contains(text, want) {
			t.Fatalf("text = %q, want 含 %q", text, want)
		}
	}
	select {
	case <-woken:
	case <-time.After(2 * time.Second):
		t.Fatal("报告落地后应唤醒会话")
	}
}

// TestDelegateWatcherReportsFailure 转交失败也要落地：否则一次失败就是一条静默丢失的活。
func TestDelegateWatcherReportsFailure(t *testing.T) {
	d := newScriptedDelegate()
	ci := newCaptureInjector()
	w := NewDelegateWatcher(DelegateWatcherOptions{Delegates: d, Injector: func() port.Injector { return ci }})
	t.Cleanup(w.Close)

	w.Watch(port.DelegateTask{ID: "d1", Agent: "claude"}, "c1")
	d.finish("d1", port.DelegateRun{}, errors.New("initialize: the agent exited"))

	in := waitInjection(t, ci.ch)
	if !strings.Contains(in.Parts[0].Text, "failed") || !strings.Contains(in.Parts[0].Text, "initialize") {
		t.Fatalf("text = %q", in.Parts[0].Text)
	}
}

// TestDelegateWaitClaimsReport 前台 delegate_wait 预领后 watcher 不再注入（模型不该看到两遍）。
func TestDelegateWaitClaimsReport(t *testing.T) {
	d := newScriptedDelegate()
	ci := newCaptureInjector()
	w := NewDelegateWatcher(DelegateWatcherOptions{Delegates: d, Injector: func() port.Injector { return ci }})
	t.Cleanup(w.Close)

	w.Watch(port.DelegateTask{ID: "d1", Agent: "claude"}, "c1")
	_, claimed := w.Claim("d1")
	if !claimed {
		t.Fatal("watcher 在场时应能预领")
	}
	d.finish("d1", port.DelegateRun{Agent: "claude", Report: "r"}, nil)
	select {
	case in := <-ci.ch:
		t.Fatalf("预领后不该再注入: %+v", in)
	case <-time.After(300 * time.Millisecond):
	}
}

// TestWatchingDelegatesKillUnwatches 叫停成功后不再补一条报告（结果已由调用方拿到）。
func TestWatchingDelegatesKillUnwatches(t *testing.T) {
	d := newScriptedDelegate()
	ci := newCaptureInjector()
	w := NewDelegateWatcher(DelegateWatcherOptions{Delegates: d, Injector: func() port.Injector { return ci }})
	t.Cleanup(w.Close)
	wd := NewWatchingDelegates(d, w)

	ctx := port.WithSessionID(context.Background(), "c1")
	task, err := wd.Start(ctx, port.DelegateSpec{Task: "x"})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if len(d.started) != 1 {
		t.Fatalf("started = %d", len(d.started))
	}
	if err := wd.Kill(context.Background(), task.ID); err != nil {
		t.Fatalf("kill: %v", err)
	}
	d.finish(task.ID, port.DelegateRun{Agent: "fake", Report: "r"}, nil)
	select {
	case in := <-ci.ch:
		t.Fatalf("叫停后不该再注入: %+v", in)
	case <-time.After(300 * time.Millisecond):
	}
}

// TestDelegateCancelSessionDropsReport 删会话时撤 watcher：报告不再尝试落地。
func TestDelegateCancelSessionDropsReport(t *testing.T) {
	d := newScriptedDelegate()
	ci := newCaptureInjector()
	w := NewDelegateWatcher(DelegateWatcherOptions{Delegates: d, Injector: func() port.Injector { return ci }})
	t.Cleanup(w.Close)

	w.Watch(port.DelegateTask{ID: "d1", Agent: "fake"}, conversation.ID("c1"))
	w.CancelSession("c1")
	d.finish("d1", port.DelegateRun{Agent: "fake", Report: "r"}, nil)
	select {
	case in := <-ci.ch:
		t.Fatalf("会话已删，不该注入: %+v", in)
	case <-time.After(300 * time.Millisecond):
	}
}
