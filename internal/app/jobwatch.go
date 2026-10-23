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

// 任务完成报告参数（D189）：终态取最后 50 行日志，注入队列满时最多重试 3 次。
const (
	jobReportLogTail   = 50
	jobInjectAttempts  = 3
	jobInjectRetryStep = time.Second
)

// JobWatcher 后台任务收尾观察者（D150/D189）：把「任务结束」变成一条 job: 注入，
// 并在界面空闲时敲醒会话。
//
// 这里**没有轮询、没有退避**：任务何时结束是确定事件，JobManager.Wait 由进程收割者
// 在写终态时唤醒（D189）。需要「定时看某个外部状态」的是观察器，不是这里。
//
// 并发（§7.5）：Watch/Unwatch/Claim/CancelSession 可在会话 goroutine 调用；每条
// watcher 在自己的 goroutine 里只等 Wait 并读 Status/Logs，**不碰会话树**——落地仍经
// port.Injector 回到会话 goroutine。Close 取消全部并等待，避免测试/长驻场景泄漏。
type JobWatcher struct {
	jobs   port.JobReader
	inject func() port.Injector
	wake   func()
	logf   func(format string, args ...any)

	sleep func(ctx context.Context, d time.Duration) bool

	root   context.Context
	cancel context.CancelFunc

	mu      sync.Mutex
	watches map[port.JobID]*jobEntry
	closed  bool
	wg      sync.WaitGroup
}

// jobEntry 一条 watcher 的运行时状态。
type jobEntry struct {
	session conversation.ID // 启动任务的会话；空 = 不绑定（测试/直接装配）
	cancel  context.CancelFunc

	mu      sync.Mutex
	claimed bool // 前台 job_wait 已预领这次终态报告
	taken   bool // 终态归属已定（watcher 报 or 前台领走），只判一次
}

// claim 预领终态报告（前台 job_wait 用）：返回 false = 归属已定（watcher 已经
// 报过了），前台照常拿结果即可。
func (e *jobEntry) claim() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.taken {
		return false
	}
	e.claimed = true
	return true
}

// release 归还预领（前台没等到终态就放弃了，报告仍该由 watcher 发）。
func (e *jobEntry) release() {
	e.mu.Lock()
	defer e.mu.Unlock()
	if !e.taken {
		e.claimed = false
	}
}

// takeByWatcher 判 watcher 是否该发报告：被前台预领或已被别人定过归属时不发。
func (e *jobEntry) takeByWatcher() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.taken || e.claimed {
		e.taken = true
		return false
	}
	e.taken = true
	return true
}

// JobWatcherOptions 依赖（全部由装配根注入）。
type JobWatcherOptions struct {
	Jobs port.JobReader
	// Injector 延迟取注入端口（会话在工具装配之后才建好）；返回 nil = 正在退出，丢弃。
	Injector func() port.Injector
	// Wake 完成报告注入后的空闲唤醒（D167）；nil = 只注入不唤醒（测试/无 UI）。
	Wake func()
	// Logf watcher 失败、报告未送达等运行日志（用户面中文）；nil = 丢弃。
	Logf func(format string, args ...any)
	// Sleep 覆盖注入重试的等待（测试用；nil = 可取消的 time.Sleep）。
	Sleep func(ctx context.Context, d time.Duration) bool
}

// NewJobWatcher 创建任务收尾观察者。
func NewJobWatcher(opts JobWatcherOptions) *JobWatcher {
	sleep := opts.Sleep
	if sleep == nil {
		sleep = sleepCtx
	}
	logf := opts.Logf
	if logf == nil {
		logf = func(string, ...any) {}
	}
	root, cancel := context.WithCancel(context.Background())
	return &JobWatcher{
		jobs:    opts.Jobs,
		inject:  opts.Injector,
		wake:    opts.Wake,
		logf:    logf,
		sleep:   sleep,
		root:    root,
		cancel:  cancel,
		watches: map[port.JobID]*jobEntry{},
	}
}

// Watch 登记一条后台任务（D189）：ctx 只用来取启动会话，watcher 自身用脱离 Turn
// 的 context——job_start 的 ctx 在轮末会被取消，直接沿用会立刻停摆。
func (p *JobWatcher) Watch(ctx context.Context, id port.JobID, _ port.JobSpec) {
	if p == nil || p.jobs == nil || id == "" {
		return
	}
	sid, _ := port.SessionIDFrom(ctx)
	wctx, cancel := context.WithCancel(p.root)
	e := &jobEntry{session: sid, cancel: cancel}
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		cancel()
		return
	}
	if old, ok := p.watches[id]; ok {
		old.cancel() // 重复登记：旧 watcher 让位（正常路径不会发生）
	}
	p.watches[id] = e
	p.wg.Add(1)
	p.mu.Unlock()
	go p.run(wctx, id, e)
}

// Unwatch 撤销一条 watcher；未知任务静默。job_kill 成功后由 JobManager 装饰器调用。
func (p *JobWatcher) Unwatch(id port.JobID) {
	if p == nil {
		return
	}
	p.mu.Lock()
	if w, ok := p.watches[id]; ok {
		delete(p.watches, id)
		w.cancel()
	}
	p.mu.Unlock()
}

// Claim 预领某任务的终态报告（D189，前台 job_wait 用）：前台自己会把这个结果交给
// 模型，watcher 就不必再补一条重复的完成报告。返回值是归还函数——前台没等到终态
// （超时/取消）时应调用它，让 watcher 照常汇报。
func (p *JobWatcher) Claim(id port.JobID) (release func()) {
	if p == nil {
		return func() {}
	}
	p.mu.Lock()
	e, ok := p.watches[id]
	p.mu.Unlock()
	if !ok || !e.claim() {
		return func() {}
	}
	return e.release
}

// CancelSession 撤销某会话的全部 watcher（D167）：切走/新建/删除当前会话时调用。
// 空 id 不动作（避免误伤全部）。
func (p *JobWatcher) CancelSession(id conversation.ID) {
	if p == nil || id == "" {
		return
	}
	p.mu.Lock()
	for jid, w := range p.watches {
		if w.session == id {
			delete(p.watches, jid)
			w.cancel()
		}
	}
	p.mu.Unlock()
}

// Close 取消全部 watcher 并等待退出（幂等）。装配根在 UI 关闭之前 defer 调用。
func (p *JobWatcher) Close() {
	if p == nil {
		return
	}
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return
	}
	p.closed = true
	for jid, w := range p.watches {
		delete(p.watches, jid)
		w.cancel()
	}
	p.mu.Unlock()
	p.cancel()
	p.wg.Wait()
}

// run 一条 watcher：阻塞等终态，到了才投递一条 job: 注入——不轮询、不猜节奏。
func (p *JobWatcher) run(ctx context.Context, id port.JobID, e *jobEntry) {
	defer p.wg.Done()
	defer p.forget(id, e)
	j, err := p.jobs.Wait(ctx, id)
	if err != nil {
		if ctx.Err() != nil {
			return // 撤销/关停：不再汇报
		}
		p.logf("等待任务 %s 结束失败: %v", id, err)
		return
	}
	if !e.takeByWatcher() {
		return // 前台 job_wait 已经把这个结果交给模型了
	}
	p.report(ctx, id, j, e.session)
}

// forget 从表里移除自己（只删指针身份相同的项，防旧 watcher 误删新登记）。
func (p *JobWatcher) forget(id port.JobID, e *jobEntry) {
	p.mu.Lock()
	if cur, ok := p.watches[id]; ok && cur == e {
		delete(p.watches, id)
	}
	p.mu.Unlock()
}

// report 组装并投递终态报告。正文进模型上下文，按硬规则用英文；日志段是不可信
// 进程输出，在正文里单独围栏（D167，投影层仍按 job: → system）。
func (p *JobWatcher) report(ctx context.Context, id port.JobID, j port.Job, session conversation.ID) {
	source := "job:" + string(id)
	ended := j.EndedAt
	if ended.IsZero() {
		ended = time.Now()
	}
	dur := time.Duration(0)
	if !j.StartedAt.IsZero() {
		dur = ended.Sub(j.StartedAt)
		if dur < 0 {
			dur = 0
		}
	}
	var b strings.Builder
	fmt.Fprintf(&b, jobReportFmt, id, j.Status, j.ExitCode, dur.Round(time.Second))
	logs, err := p.jobs.Logs(ctx, id, jobReportLogTail)
	if err != nil {
		if ctx.Err() != nil {
			return
		}
		p.logf("读取任务 %s 日志失败: %v", id, err)
		logs = ""
	}
	logs = strings.TrimRight(logs, "\n")
	if strings.TrimSpace(logs) == "" {
		b.WriteString(jobLogEmpty)
	} else {
		b.WriteString(jobLogNote)
		fmt.Fprintf(&b, "<injected-context source=\"%s\">\n", escapeFenceAttr(source))
		// 日志是外部进程写的：正文里的闭合标签必须中和，否则一行日志就能提前收尾，
		// 把后面伪装成宿主说明（D170）。报告整体是宿主事实，装配层不会再包一层。
		b.WriteString(neutralizeFence(logs))
		b.WriteString("\n</injected-context>")
	}
	if ctx.Err() != nil {
		return // 关停/切换已撤销 watcher：报告不再落地
	}
	in := port.Injection{
		Source:  source,
		Trust:   port.TrustHost, // 状态/退出码/耗时是宿主事实（D170）；日志段已在正文里单独围栏
		Parts:   []conversation.Part{{Kind: conversation.PartText, Text: b.String()}},
		Session: session,
	}
	if !p.deliver(ctx, id, in) {
		return
	}
	if p.wake != nil {
		p.wake()
	}
}

// deliver 投递报告；队列满时做有限重试（任务已结束，丢一条就没人再报）。
func (p *JobWatcher) deliver(ctx context.Context, id port.JobID, in port.Injection) bool {
	if p.inject == nil {
		p.logf("后台任务 %s 的完成报告未送达: 注入端口未就绪", id)
		return false
	}
	inj := p.inject()
	if inj == nil {
		p.logf("后台任务 %s 的完成报告未送达: 会话已关闭", id)
		return false
	}
	var err error
	for attempt := 0; attempt < jobInjectAttempts; attempt++ {
		if ctx.Err() != nil {
			return false
		}
		if err = inj.Inject(ctx, in); err == nil {
			return true
		}
		if ctx.Err() != nil {
			return false
		}
		if attempt+1 < jobInjectAttempts && !p.sleep(ctx, time.Duration(attempt+1)*jobInjectRetryStep) {
			return false
		}
	}
	p.logf("后台任务 %s 的完成报告未送达: %v", id, err)
	return false
}

// sleepCtx 可被 ctx 提前唤醒的等待。
func sleepCtx(ctx context.Context, d time.Duration) bool {
	if err := ctx.Err(); err != nil {
		return false
	}
	if d <= 0 {
		return true
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}
