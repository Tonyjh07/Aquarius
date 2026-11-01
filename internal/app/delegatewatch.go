package app

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/Tonyjh07/Aquarius/internal/domain/conversation"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

// 转交报告的注入重试（与 jobInjectAttempts 同一口径：队列满时最多再来 3 次）。
const (
	delegateInjectAttempts  = 3
	delegateInjectRetryStep = time.Second
)

// DelegateWatcher 转交收尾观察者（D261/§5.18）：阻塞在 DelegateRunner.Wait 上，
// 终态到达就把「报告 + 一行摘要」注入回**发起会话**，并在界面空闲时敲醒会话。
//
// 与 JobWatcher 的差别只有一处语义：报告正文是外部代理写的，所以 Trust = External
// （投影时套数据围栏）；任务的状态/退出码那种宿主事实这里没有。没有轮询、没有退避——
// 转交何时结束是确定事件。
type DelegateWatcher struct {
	delegates port.DelegateRunner
	inject    func() port.Injector
	wake      func()
	logf      func(format string, args ...any)
	sleep     func(ctx context.Context, d time.Duration) bool

	root   context.Context
	cancel context.CancelFunc

	mu      sync.Mutex
	watches map[port.DelegateID]*delegateEntry
	closed  bool
	wg      sync.WaitGroup
}

// claimSettleLimit 等前台 delegate_wait 交回归属的上限（漏掉 release 时的兜底）。
const claimSettleLimit = 2 * time.Second

// delegateEntry 一条转交 watcher 的运行时状态。
type delegateEntry struct {
	session conversation.ID
	agent   string
	cancel  context.CancelFunc

	mu      sync.Mutex
	claimed bool // 前台 delegate_wait 已预领这次终态报告
	taken   bool // 终态归属已定
	// settled 前台把归属交回时关闭。预领之后 watcher 不能直接判「我不报了」：
	// 前台那次 Wait 可能刚好超时失败（比如 delegate_wait 自带 timeout），那一刻
	// 报告两边都不投就彻底没了。所以 watcher 在这里等前台的结论。
	settled     chan struct{}
	settledOnce sync.Once
}

func (e *delegateEntry) claim() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.taken || e.claimed {
		return false
	}
	e.claimed = true
	return true
}

// release 交回归属：delivered=true 表示结果已经交给调用方（watcher 不必再报）；
// false 表示调用方没拿到（超时/报错），报告还给 watcher 照常注入。
func (e *delegateEntry) release(delivered bool) {
	e.mu.Lock()
	if delivered {
		e.taken = true
	}
	e.claimed = false
	e.mu.Unlock()
	e.settledOnce.Do(func() { close(e.settled) })
}

func (e *delegateEntry) takeByWatcher() bool {
	e.mu.Lock()
	if e.taken {
		e.mu.Unlock()
		return false
	}
	if !e.claimed {
		e.taken = true
		e.mu.Unlock()
		return true
	}
	// 前台正拿着它：等结论。前台的 Wait 一返回就会 release，所以这里实际上几乎不等；
	// 上限只是兜底——调用方漏掉 release 不该把收尾观察者永久卡住（关停会一起挂在 wg.Wait）。
	settled := e.settled
	e.mu.Unlock()
	select {
	case <-settled:
	case <-time.After(claimSettleLimit):
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.taken {
		return false
	}
	e.taken = true
	return true
}

// discard 无条件判定「不必再报」（Kill 之后由调用方拿到结果、撤销时）。不等前台。
func (e *delegateEntry) discard() {
	e.mu.Lock()
	e.taken = true
	e.mu.Unlock()
	e.settledOnce.Do(func() { close(e.settled) })
}

// DelegateWatcherOptions 依赖（全部由装配根注入）。
type DelegateWatcherOptions struct {
	Delegates port.DelegateRunner
	// Injector 延迟取注入端口（会话在工具装配之后才建好）；返回 nil = 正在退出，丢弃。
	Injector func() port.Injector
	// Wake 报告落地后的空闲唤醒；nil = 只注入不唤醒。
	Wake func()
	// Logf 运行日志（用户面中文）；nil = 丢弃。
	Logf func(format string, args ...any)
	// Sleep 覆盖注入重试的等待（测试用）。
	Sleep func(ctx context.Context, d time.Duration) bool
}

// NewDelegateWatcher 创建转交收尾观察者。
func NewDelegateWatcher(opts DelegateWatcherOptions) *DelegateWatcher {
	sleep := opts.Sleep
	if sleep == nil {
		sleep = sleepCtx
	}
	logf := opts.Logf
	if logf == nil {
		logf = func(string, ...any) {}
	}
	root, cancel := context.WithCancel(context.Background())
	return &DelegateWatcher{
		delegates: opts.Delegates,
		inject:    opts.Injector,
		wake:      opts.Wake,
		logf:      logf,
		sleep:     sleep,
		root:      root,
		cancel:    cancel,
		watches:   map[port.DelegateID]*delegateEntry{},
	}
}

// Watch 登记并起一条 watcher（重复登记同 id 时覆盖旧条目）。
func (w *DelegateWatcher) Watch(task port.DelegateTask, session conversation.ID) {
	if w == nil || w.delegates == nil {
		return
	}
	ctx, cancel := context.WithCancel(w.root)
	e := &delegateEntry{session: session, agent: task.Agent, cancel: cancel, settled: make(chan struct{})}
	w.mu.Lock()
	if w.closed {
		w.mu.Unlock()
		cancel()
		return
	}
	if old, ok := w.watches[task.ID]; ok {
		old.cancel()
	}
	w.watches[task.ID] = e
	w.wg.Add(1)
	w.mu.Unlock()
	go w.run(ctx, task.ID, e)
}

// Claim 预领终态报告（前台 delegate_wait 用）：返回 false = 归属已定（watcher 报过了）。
// 返回的 release 必须调用，并告诉它结果有没有交出去——交不出去就要还回来。
func (w *DelegateWatcher) Claim(id port.DelegateID) (func(delivered bool), bool) {
	if w == nil {
		return func(bool) {}, true
	}
	w.mu.Lock()
	e, ok := w.watches[id]
	w.mu.Unlock()
	if !ok {
		return func(bool) {}, false // 没有 watcher：前台拿到结果就是全部，不必再报
	}
	if !e.claim() {
		return func(bool) {}, false
	}
	return e.release, true
}

// Unwatch 撤销一条 watcher（delegate_kill 成功后：结果已由调用方拿到，不必再补报告）。
func (w *DelegateWatcher) Unwatch(id port.DelegateID) {
	if w == nil {
		return
	}
	w.mu.Lock()
	e, ok := w.watches[id]
	if ok {
		delete(w.watches, id)
	}
	w.mu.Unlock()
	if ok {
		e.discard() // 定归属：watcher 若已取到终态也不再注入
		e.cancel()
	}
}

// CancelSession 撤销某会话的全部 watcher（删除会话与关停时调用）。切换不撤——
// 报告按归属异地落地，watcher 的生命周期跟任务走（D261）。
func (w *DelegateWatcher) CancelSession(id conversation.ID) {
	if w == nil || id == "" {
		return
	}
	w.mu.Lock()
	for did, e := range w.watches {
		if e.session == id {
			delete(w.watches, did)
			e.cancel()
		}
	}
	w.mu.Unlock()
}

// Close 取消全部 watcher 并等待退出（幂等）。
func (w *DelegateWatcher) Close() {
	if w == nil {
		return
	}
	w.mu.Lock()
	if w.closed {
		w.mu.Unlock()
		return
	}
	w.closed = true
	for id, e := range w.watches {
		delete(w.watches, id)
		e.cancel()
	}
	w.mu.Unlock()
	w.cancel()
	w.wg.Wait()
}

// run 一条 watcher：等终态，到了才投递一条 delegate: 注入。
func (w *DelegateWatcher) run(ctx context.Context, id port.DelegateID, e *delegateEntry) {
	defer w.wg.Done()
	defer w.forget(id, e)
	run, err := w.delegates.Wait(ctx, id)
	if err != nil && ctx.Err() != nil {
		return // 撤销/关停：不再汇报
	}
	if !e.takeByWatcher() {
		return // 前台 delegate_wait 已经把这个结果交给模型了
	}
	var text string
	if err != nil {
		text = delegateFailureText(id, e.agent, run, err)
	} else {
		text = port.DelegateReportText(id, run)
	}
	in := port.Injection{
		Source:  "delegate:" + e.agent,
		Trust:   port.TrustExternal, // 正文是外部代理写的，不是宿主事实（D170）
		Parts:   []conversation.Part{{Kind: conversation.PartText, Text: text}},
		Session: e.session,
	}
	if !w.deliver(ctx, id, in) {
		return
	}
	if w.wake != nil {
		w.wake()
	}
}

// forget 从表里移除自己（只删指针身份相同的项）。
func (w *DelegateWatcher) forget(id port.DelegateID, e *delegateEntry) {
	w.mu.Lock()
	if cur, ok := w.watches[id]; ok && cur == e {
		delete(w.watches, id)
	}
	w.mu.Unlock()
}

// deliver 投递注入，队列满时重试（与 JobWatcher 同一口径）。返回是否落地。
func (w *DelegateWatcher) deliver(ctx context.Context, id port.DelegateID, in port.Injection) bool {
	for attempt := 0; attempt < delegateInjectAttempts; attempt++ {
		if ctx.Err() != nil {
			return false
		}
		inj := w.inject()
		if inj == nil {
			return false // 正在退出
		}
		err := inj.Inject(ctx, in)
		if err == nil {
			return true
		}
		if attempt == delegateInjectAttempts-1 {
			w.logf("转交 %s 的报告注入失败: %v", id, err)
			return false
		}
		if !w.sleep(ctx, delegateInjectRetryStep*(time.Duration(attempt+1))) {
			return false
		}
	}
	return false
}

// delegateFailureText 转交失败的报告正文（英文：进模型上下文）。它必须落地——否则
// 一次失败就是一条静默丢失的活。已经收到的正文一并带上：中途失败不该把十几分钟的
// 输出一起丢掉。
func delegateFailureText(id port.DelegateID, agent string, run port.DelegateRun, err error) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Delegation %s to %s failed: %s", id, agent, strings.TrimSpace(err.Error()))
	if body := strings.TrimSpace(run.Report); body != "" {
		fmt.Fprintf(&b, "\n\nIt had produced this much before failing (partial, tool calls=%d):\n\n%s",
			run.ToolCalls, body)
	}
	if len(run.Notes) > 0 {
		b.WriteString("\n\nNotes:\n")
		for _, n := range run.Notes {
			b.WriteString("- " + n + "\n")
		}
	}
	return b.String()
}

// watchingDelegates port.Delegate 的 watcher 装饰器（与 watchingJobs 同一形状）：
// Start 成功即登记，Kill 成功即撤销，Wait 顺手预领终态报告，其余原样转发。
type watchingDelegates struct {
	port.Delegate
	watcher *DelegateWatcher
}

var _ port.Delegate = (*watchingDelegates)(nil)

// NewWatchingDelegates 给转交管理器套 watcher；任一为空时原样返回。
func NewWatchingDelegates(d port.Delegate, watcher *DelegateWatcher) port.Delegate {
	if d == nil || watcher == nil {
		return d
	}
	return &watchingDelegates{Delegate: d, watcher: watcher}
}

func (w *watchingDelegates) Start(ctx context.Context, spec port.DelegateSpec) (port.DelegateTask, error) {
	task, err := w.Delegate.Start(ctx, spec)
	if err != nil {
		return task, err
	}
	sid, _ := port.SessionIDFrom(ctx)
	w.watcher.Watch(task, sid)
	return task, nil
}

func (w *watchingDelegates) Kill(ctx context.Context, id port.DelegateID) error {
	err := w.Delegate.Kill(ctx, id)
	if err == nil {
		w.watcher.Unwatch(id)
	}
	return err
}

// Wait 等终态并把报告预领给调用方（delegate_wait）：结果就地回填给模型，watcher
// 不再注入一条内容相同的报告。
func (w *watchingDelegates) Wait(ctx context.Context, id port.DelegateID) (port.DelegateRun, error) {
	release, claimed := w.watcher.Claim(id)
	run, err := w.Delegate.Wait(ctx, id)
	if claimed {
		// 拿到结果 = 归属定了（watcher 不再注入）；没拿到（超时/报错）= 还回去，
		// 由 watcher 在终态到达时照常注入，报告不会因为一次超时就凭空消失。
		release(err == nil)
	}
	return run, err
}
