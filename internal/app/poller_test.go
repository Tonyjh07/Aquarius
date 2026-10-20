package app

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Tonyjh07/Aquarius/internal/domain/conversation"
	"github.com/Tonyjh07/Aquarius/internal/domain/tool"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

// pollJobs 可变的 port.JobManager 替身：poller 测试要边跑边改状态。
type pollJobs struct {
	mu       sync.Mutex
	jobs     map[port.JobID]port.Job
	logs     map[port.JobID]string
	lastTail int
	startJob port.Job
	startErr error
	killErr  error
}

func newPollJobs() *pollJobs {
	return &pollJobs{jobs: map[port.JobID]port.Job{}, logs: map[port.JobID]string{}}
}

func (m *pollJobs) set(j port.Job) {
	m.mu.Lock()
	m.jobs[j.ID] = j
	m.mu.Unlock()
}

func (m *pollJobs) setLog(id port.JobID, text string) {
	m.mu.Lock()
	m.logs[id] = text
	m.mu.Unlock()
}

func (m *pollJobs) Start(context.Context, port.JobSpec) (port.Job, error) {
	return m.startJob, m.startErr
}

func (m *pollJobs) Run(context.Context, port.JobSpec) (tool.Result, error) {
	return tool.Result{}, errors.New("pollJobs: Run 未脚本化")
}

func (m *pollJobs) List(context.Context) ([]port.Job, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]port.Job, 0, len(m.jobs))
	for _, j := range m.jobs {
		out = append(out, j)
	}
	return out, nil
}

func (m *pollJobs) Status(_ context.Context, id port.JobID) (port.Job, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	j, ok := m.jobs[id]
	if !ok {
		return port.Job{}, errors.New("pollJobs: 没有任务")
	}
	return j, nil
}

func (m *pollJobs) Logs(_ context.Context, id port.JobID, tail int) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.lastTail = tail
	text, ok := m.logs[id]
	if !ok {
		return "", errors.New("pollJobs: 没有日志")
	}
	return text, nil
}

func (m *pollJobs) Kill(context.Context, port.JobID) error { return m.killErr }

// captureInjector 收集 poller 投递的注入（channel 用于等待，切片用于断言）。
type captureInjector struct {
	mu    sync.Mutex
	items []port.Injection
	ch    chan port.Injection
}

func newCaptureInjector() *captureInjector {
	return &captureInjector{ch: make(chan port.Injection, 8)}
}

func (c *captureInjector) Inject(_ context.Context, in port.Injection) error {
	c.mu.Lock()
	c.items = append(c.items, in)
	c.mu.Unlock()
	c.ch <- in
	return nil
}

func waitInjection(t *testing.T, ch <-chan port.Injection) port.Injection {
	t.Helper()
	select {
	case in := <-ch:
		return in
	case <-time.After(2 * time.Second):
		t.Fatal("等待 poller 注入超时")
		return port.Injection{}
	}
}

// TestPollerReportsTerminal 终态只投一条：正文含状态/退出码/耗时/围栏日志，目标会话与
// 默认触发（Trigger=nil）保持不变，并敲一次唤醒。
func TestPollerReportsTerminal(t *testing.T) {
	jobs := newPollJobs()
	started := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	jobs.set(port.Job{
		ID: "j001", Status: port.JobDone, ExitCode: 0,
		StartedAt: started, EndedAt: started.Add(2*time.Minute + 13*time.Second),
	})
	jobs.setLog("j001", "line one\nline two\n")
	inj := newCaptureInjector()
	wakeCh := make(chan struct{}, 1)
	p := NewPoller(PollerOptions{
		Jobs:        jobs,
		Injector:    func() port.Injector { return inj },
		Wake:        func() { wakeCh <- struct{}{} },
		BackoffBase: time.Millisecond,
		BackoffMax:  2 * time.Millisecond,
	})
	defer p.Close()

	ctx := port.WithSessionID(context.Background(), conversation.ID("c1"))
	p.Watch(ctx, "j001", port.JobSpec{})

	in := waitInjection(t, inj.ch)
	if in.Source != "poller:j001" || in.Session != "c1" {
		t.Fatalf("source/session = %q/%q", in.Source, in.Session)
	}
	if in.Trigger != nil {
		t.Fatalf("Trigger = %v, want nil（按来源默认）", *in.Trigger)
	}
	if len(in.Parts) != 1 || in.Parts[0].Kind != conversation.PartText {
		t.Fatalf("parts = %+v", in.Parts)
	}
	body := in.Parts[0].Text
	for _, want := range []string{
		"Background task j001 finished with status done",
		"exit 0",
		"duration 2m13s",
		"untrusted process output",
		`<injected-context source="poller:j001">`,
		"line one",
		"</injected-context>",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("body = %q, want 含 %q", body, want)
		}
	}
	jobs.mu.Lock()
	tail := jobs.lastTail
	jobs.mu.Unlock()
	if tail != pollerLogTail {
		t.Fatalf("log tail = %d, want %d", tail, pollerLogTail)
	}
	select {
	case <-wakeCh:
	case <-time.After(2 * time.Second):
		t.Fatal("等待 poller 唤醒超时")
	}
	select {
	case extra := <-inj.ch:
		t.Fatalf("重复注入: %+v", extra)
	case <-time.After(20 * time.Millisecond):
	}
}

// TestPollerBackoff 退避纯函数：×1.5、15s 封顶。
func TestPollerBackoff(t *testing.T) {
	d := defaultPollBackoffBase
	want := []time.Duration{
		750 * time.Millisecond,
		1125 * time.Millisecond,
		1687*time.Millisecond + 500*time.Microsecond,
		2531*time.Millisecond + 250*time.Microsecond,
		3796*time.Millisecond + 875*time.Microsecond,
		5695*time.Millisecond + 312*time.Microsecond + 500*time.Nanosecond,
		8542*time.Millisecond + 968*time.Microsecond + 750*time.Nanosecond,
		12814*time.Millisecond + 453*time.Microsecond + 125*time.Nanosecond,
		defaultPollBackoffMax,
		defaultPollBackoffMax,
	}
	for i, w := range want {
		d = nextPollDelay(d, defaultPollBackoffMax)
		if d != w {
			t.Fatalf("step %d: delay = %v, want %v", i, d, w)
		}
	}
}

// TestPollerCancelSession 只撤销该会话的 watcher，别的会话照常观察。
func TestPollerCancelSession(t *testing.T) {
	jobs := newPollJobs()
	jobs.set(port.Job{ID: "j1", Status: port.JobRunning})
	jobs.set(port.Job{ID: "j2", Status: port.JobRunning})
	p := NewPoller(PollerOptions{Jobs: jobs, BackoffBase: time.Hour, BackoffMax: time.Hour})
	defer p.Close()

	p.Watch(port.WithSessionID(context.Background(), "c1"), "j1", port.JobSpec{})
	p.Watch(port.WithSessionID(context.Background(), "c2"), "j2", port.JobSpec{})
	p.CancelSession("c1")

	p.mu.Lock()
	_, has1 := p.watches["j1"]
	_, has2 := p.watches["j2"]
	p.mu.Unlock()
	if has1 || !has2 {
		t.Fatalf("watches: j1=%v j2=%v, want false/true", has1, has2)
	}
}

// TestPollerCloseStops 关停取消所有 watcher，不留下一次注入。
func TestPollerCloseStops(t *testing.T) {
	jobs := newPollJobs()
	jobs.set(port.Job{ID: "j1", Status: port.JobRunning})
	inj := newCaptureInjector()
	p := NewPoller(PollerOptions{
		Jobs: jobs, Injector: func() port.Injector { return inj },
		BackoffBase: time.Hour, BackoffMax: time.Hour,
	})
	p.Watch(port.WithSessionID(context.Background(), "c1"), "j1", port.JobSpec{})
	p.Close()
	p.Close() // 幂等
	p.mu.Lock()
	n := len(p.watches)
	p.mu.Unlock()
	if n != 0 {
		t.Fatalf("watches = %d, want 0", n)
	}
	select {
	case in := <-inj.ch:
		t.Fatalf("关停后仍注入: %+v", in)
	case <-time.After(20 * time.Millisecond):
	}
}

// TestWatchingJobsRegistersAndUnwatches 装饰器覆盖 Start/Kill 两条路径。
func TestWatchingJobsRegistersAndUnwatches(t *testing.T) {
	jobs := newPollJobs()
	jobs.startJob = port.Job{ID: "j9", Status: port.JobRunning}
	p := NewPoller(PollerOptions{Jobs: jobs, BackoffBase: time.Hour, BackoffMax: time.Hour})
	defer p.Close()
	w := NewWatchingJobs(jobs, p)

	ctx := port.WithSessionID(context.Background(), "c1")
	job, err := w.Start(ctx, port.JobSpec{Command: "srv"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	p.mu.Lock()
	watching := p.watches[job.ID]
	p.mu.Unlock()
	if watching == nil {
		t.Fatal("Start 后未登记 watcher")
	}
	if watching.session != "c1" {
		t.Fatalf("watcher session = %q, want c1", watching.session)
	}
	if err := w.Kill(context.Background(), job.ID); err != nil {
		t.Fatalf("Kill: %v", err)
	}
	p.mu.Lock()
	_, stillWatching := p.watches[job.ID]
	p.mu.Unlock()
	if stillWatching {
		t.Fatal("Kill 成功后 watcher 仍在")
	}

	// Kill 失败（例如任务仍在跑/已自然结束）不撤销，留给 watcher 按终态报告。
	jobs.killErr = errors.New("kill failed")
	job, err = w.Start(ctx, port.JobSpec{Command: "srv"})
	if err != nil {
		t.Fatalf("Start 2: %v", err)
	}
	if err := w.Kill(context.Background(), job.ID); err == nil {
		t.Fatal("Kill 应上抛脚本错误")
	}
	p.mu.Lock()
	_, stillWatching = p.watches[job.ID]
	p.mu.Unlock()
	if !stillWatching {
		t.Fatal("Kill 失败时不应撤销 watcher")
	}
}

// TestPollerDeliverRetries 注入队列满（有限重试）后成功；最终失败只记日志。
func TestPollerDeliverRetries(t *testing.T) {
	flaky := &flakyInjector{}
	p := NewPoller(PollerOptions{
		Jobs: &pollJobs{}, Injector: func() port.Injector { return flaky },
		Sleep: func(context.Context, time.Duration) bool { return true },
	})
	in := port.Injection{Source: "poller:j1", Parts: []conversation.Part{{Kind: conversation.PartText, Text: "x"}}}
	if !p.deliver(context.Background(), "j1", in) {
		t.Fatal("第二次投递应成功")
	}
	if flaky.calls != 2 {
		t.Fatalf("calls = %d, want 2", flaky.calls)
	}

	failing := &flakyInjector{alwaysFail: true}
	p.inject = func() port.Injector { return failing }
	if p.deliver(context.Background(), "j1", in) {
		t.Fatal("一直失败时应返回 false")
	}
	if failing.calls != pollerInjectAttempts {
		t.Fatalf("calls = %d, want %d", failing.calls, pollerInjectAttempts)
	}
}

type flakyInjector struct {
	calls      int
	alwaysFail bool
}

func (f *flakyInjector) Inject(context.Context, port.Injection) error {
	f.calls++
	if f.alwaysFail || f.calls == 1 {
		return errors.New("队列已满")
	}
	return nil
}
