package acpgate

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync"
	"time"

	acp "github.com/coder/acp-go-sdk"

	"github.com/Tonyjh07/Aquarius/internal/adapter/pathres"
	"github.com/Tonyjh07/Aquarius/internal/domain/conversation"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

// stderrCap 子进程 stderr 的有界缓冲（§5.18 进程卫生）：只用于「它为什么起不来 /
// 为什么退出」这类报因，不进上下文。
const stderrCap = 8 << 10

// stderrDrainBudget 对端断连后等 exec 的 stderr 拷贝排空的预算。进程已退出时 cmd.Wait
// 立刻返回；这个值只给慢机器留兜底，不让报因卡住。
const stderrDrainBudget = 500 * time.Millisecond

// notifyDrainBudgetDefault 失败收尾等 SDK 通知排空的兜底上限。正常断连的排空是微秒级，
// 这个值只在 SDK 语义变化或通知 handler 卡住时起作用：到期就退回现状，不挂住 Wait。
const notifyDrainBudgetDefault = 2 * time.Second

// notifyDrainQuietWindow 失败收尾等第一条通知交付的静默窗口。只有「断连时队列里还有通知、
// handler 尚未被调度」才用得到；队列本来是空的失败会等满这个窗口，所以它不宜长，
// 但要盖得住 -race 满载下的 goroutine 调度延迟。
const notifyDrainQuietWindow = 500 * time.Millisecond

// stopKind 停止原因（第一次请求胜出：停止键、总闸、关停三处可能同时到）。
type stopKind int

const (
	stopNone stopKind = iota
	stopKilled
	stopTimeout
	stopShutdown
)

// run 一次转交的运行时状态。
//
// 收集字段只由 run goroutine 写；停止请求来自别的 goroutine（Kill/定时器/关停），
// 因此 stopKind 与 sessID 走 mu。ctx 取消用于**打断阻塞中的确认框**（取消要显式发
// session/cancel 通知，不能用 ctx 取消代替——SDK 的 Prompt 见 ctx 被取消会自己补一条
// session/cancel 然后立刻返回错误，stopReason 和正文一起丢，§5.18）。
type run struct {
	id      port.DelegateID
	agent   Agent
	spec    port.DelegateSpec
	session conversation.ID
	started time.Time

	mu     sync.Mutex
	stop   stopKind
	stopCh chan struct{}
	sessID string
	proc   *process
	// roots 本次转交的会话根集（cwd + 调用时批准过的额外目录），已解析：fs 闸门用它
	// 判定「在不在范围里」。只在 execute 里写一次，之后只读；但读方是各自独立的 fs
	// 请求 goroutine，所以同样走 mu。
	roots pathres.RootSet

	cancelCtx context.Context
	cancelFn  context.CancelFunc

	// 收集面。**不是 run goroutine 独占**：session/update 跑在 SDK 的通知 goroutine 上，
	// request_permission 每个请求各开一条 goroutine（三者可以同时写）——所以全部经 cmu。
	// 曾经按「独占」写，属于靠顺序侥幸不炸的类型。
	cmu         sync.Mutex
	report      strings.Builder
	headLen     int    // report 里已有的字符数（增量累加，别每次 String() 再数一遍）
	tailRunes   []rune // 头段满了之后的尾部窗口
	dropped     int
	toolCalls   int
	changed     []string
	changedSeen map[string]bool
	changedOver int
	// wrote 经 Aquarius 写下的文件（事实，不是它自称的）：与 changed 并列进报告，
	// 两份来源分开列（D283）。
	wrote     []string
	wroteSeen map[string]bool
	wroteOver int
	// 越界读的账（D284）：放行过的进报告清单，拒过的记住原因（同一路径不再打扰用户），
	// 额度按「问过几次」算。
	outsideRead       []string
	outsideReadOK     map[string]bool
	outsideReadNo     map[string]string
	outsideReadOver   int
	outsideReadsAsked int
	notes             []string
	denied            int
	truncated         bool
	peerGone          bool

	// 通知排空信号。SDK 在断连时先关 Done() 让 Prompt 拿到 error，之后才在另一条
	// goroutine 里把已入队的通知交回 SessionUpdate；排空完成才 cancel 它传给 handler 的
	// inboundCtx。notifySeen 等第一条通知交付，drainDone 等那个 ctx 结束。
	notifyOnce  sync.Once
	notifySeen  chan struct{}
	drainDone   chan struct{}
	notifyArmed bool // 只由 run goroutine 读写：进入 prompt 阶段后才有正文要保住

	done  chan struct{}
	out   port.DelegateRun
	err   error
	ended bool
}

func newRun(id port.DelegateID, a Agent, spec port.DelegateSpec, session conversation.ID) *run {
	ctx, cancel := context.WithCancel(context.Background())
	return &run{
		id: id, agent: a, spec: spec, session: session,
		started:   time.Now(),
		stopCh:    make(chan struct{}),
		cancelCtx: ctx, cancelFn: cancel,
		changedSeen:   map[string]bool{},
		wroteSeen:     map[string]bool{},
		outsideReadOK: map[string]bool{},
		outsideReadNo: map[string]string{},
		notifySeen:    make(chan struct{}),
		drainDone:     make(chan struct{}),
		done:          make(chan struct{}),
	}
}

// finished 是否已进入终态（done 关闭）。
func (r *run) finished() bool {
	select {
	case <-r.done:
		return true
	default:
		return false
	}
}

// requestStop 请求停止（第一次胜出）：关闭 stopCh 并取消确认 ctx，让阻塞中的调用返回。
func (r *run) requestStop(kind stopKind) {
	r.mu.Lock()
	if r.stop != stopNone || r.ended {
		r.mu.Unlock()
		return
	}
	r.stop = kind
	close(r.stopCh)
	cancel := r.cancelFn
	r.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

// stopKind 当前停止原因（未停止 = stopNone）。
func (r *run) stopKind() stopKind {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.stop
}

func (r *run) setSession(id string) {
	r.mu.Lock()
	r.sessID = id
	r.mu.Unlock()
}

// setRoots 记下本次转交的会话根集（只写一次）。
func (r *run) setRoots(s pathres.RootSet) {
	r.mu.Lock()
	r.roots = s
	r.mu.Unlock()
}

// rootSet 当前会话的根集（未设时为空集：什么都不许，fail closed）。
func (r *run) rootSet() pathres.RootSet {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.roots
}

func (r *run) sessionID() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.sessID
}

func (r *run) setProc(p *process) {
	r.mu.Lock()
	r.proc = p
	r.mu.Unlock()
}

func (r *run) process() *process {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.proc
}

// note 记一条用户面事实（能力协商、跳过的东西、被拒的权限请求）。
func (r *run) note(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	r.cmu.Lock()
	defer r.cmu.Unlock()
	for _, n := range r.notes {
		if n == msg {
			return
		}
	}
	r.notes = append(r.notes, msg)
}

// addDenied 记一次被拒的权限请求（计数与备注一起，免得两处各写一次锁）。
func (r *run) addDenied(reason string) {
	r.cmu.Lock()
	r.denied++
	r.cmu.Unlock()
	r.note("permission denied: %s", reason)
}

// deniedCount 收尾时读被拒次数。写方 addDenied 持 cmu，这里不能裸读 r.denied：
// 权限请求跑在独立 goroutine 上，与收尾可以重叠。
func (r *run) deniedCount() int {
	r.cmu.Lock()
	defer r.cmu.Unlock()
	return r.denied
}

// setPeerGone 记「对端断开了」（写方是 peer 监听 goroutine）。
func (r *run) setPeerGone() {
	r.cmu.Lock()
	r.peerGone = true
	r.cmu.Unlock()
}

// peerGoneNow 对端是否已经断开。写读都走同一把收集面锁——这个字段不在 run goroutine
// 的独占面里，用错锁就是数据竞争。
func (r *run) peerGoneNow() bool {
	r.cmu.Lock()
	defer r.cmu.Unlock()
	return r.peerGone
}

// markPeerIfClosed 由 run goroutine 亲自看一眼连接是否已关闭，并在是的时候置 peerGone。
//
// 只靠 watchdog 那条 goroutine 置位是不够的：SDK 的 waitForResponse 与 watchdog 都等
// 同一个 Done()，谁先被唤醒不确定。Prompt 已经因断连返回错误时，watchdog 可能还没跑，
// abort 就会以为「对端还在」而跳过 stderr 排空，崩溃报因退化成一句没有线索的关闭。
// 这里把「已关闭」变成本 goroutine 直接观察到的事实，与 Prompt 的返回值之间也就有了
// happens-before（同一个 channel 的接收）。
func (r *run) markPeerIfClosed(conn *acp.ClientSideConnection) {
	if conn == nil {
		return
	}
	select {
	case <-conn.Done():
		r.setPeerGone()
	default:
	}
}

// armNotifyDrain 标记「转交已进入 prompt 阶段」。只有这个阶段之后失败才有正文要保住：
// 起不来、握手/建会话就报错的路径一条 session/update 都收不到，不必等。
// 只由 run goroutine 调用，与 waitNotifyDrain 同 goroutine，无需加锁。
func (r *run) armNotifyDrain() { r.notifyArmed = true }

// observeNotifyCtx 记下 SDK 交给 session/update 的 ctx。每条通知都会走到这里，只认第一次：
// 同一个 Connection 里所有通知共用同一个 inboundCtx，它的 Done() 才是排空完成的信号。
func (r *run) observeNotifyCtx(ctx context.Context) {
	r.notifyOnce.Do(func() {
		close(r.notifySeen)
		go func() {
			<-ctx.Done()
			close(r.drainDone)
		}()
	})
}

// waitNotifyDrain 失败收尾前等「断连前入队的通知已全部交回 handler」。
//
// SDK 在 EOF 时先 cancel 连接 ctx（Done 关闭，Prompt 立刻拿到 error），之后才在另一条
// goroutine 里排空通知队列，排空完成才 cancel 上面那个 inboundCtx。不等就取报告会读到
// 还没写入的正文，D268「中途失败也要带出已收正文」便间歇性落空。
//
// 两段式：先等第一条通知交付——队列里真有通知，SDK 的排空就一定会调到 handler；
// 静默窗口里一条都没来，说明队列本来是空的，报告也不会有正文，直接返回。看到通知后
// 再等 inboundCtx 结束，这一步是 SDK 保证的严格 happens-before，不是时间猜测。
// budget 是总上限：SDK 语义若变，最多退回「可能少一点正文」，绝不挂住 Wait。
func (r *run) waitNotifyDrain(budget time.Duration) {
	if !r.notifyArmed || budget <= 0 {
		return
	}
	timer := time.NewTimer(budget)
	defer timer.Stop()
	quiet := notifyDrainQuietWindow
	if budget < quiet {
		quiet = budget
	}
	quietTimer := time.NewTimer(quiet)
	defer quietTimer.Stop()
	select {
	case <-r.notifySeen:
	case <-quietTimer.C:
		return
	case <-timer.C:
		return
	}
	select {
	case <-r.drainDone:
	case <-timer.C:
	}
}

// collected 收尾时一次性取走的收集结果（拷贝，不与活切片共享底层数组）。
type collected struct {
	toolCalls                                       int
	changed, wrote, readOutside, notes              []string
	denied, changedOver, wroteOver, readOutsideOver int
}

func (r *run) snapshot() collected {
	r.cmu.Lock()
	defer r.cmu.Unlock()
	return collected{
		toolCalls:       r.toolCalls,
		changed:         append([]string(nil), r.changed...),
		wrote:           append([]string(nil), r.wrote...),
		readOutside:     append([]string(nil), r.outsideRead...),
		notes:           append([]string(nil), r.notes...),
		denied:          r.denied,
		changedOver:     r.changedOver,
		wroteOver:       r.wroteOver,
		readOutsideOver: r.outsideReadOver,
	}
}

// complete 写终态并放行 Wait（只调一次）。
func (r *run) complete(out port.DelegateRun, err error) {
	r.mu.Lock()
	r.out, r.err, r.ended = out, err, true
	r.mu.Unlock()
	close(r.done)
}

func (r *run) result() (port.DelegateRun, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.out, r.err
}

// run 一次转交的整条生命周期：起进程 → 握手 → 开会话 → 提示 → 收报告 → 关进程。
func (m *Manager) run(r *run) {
	res, err := m.execute(r)
	res.Duration = m.opts.Now().Sub(r.started)
	if res.Duration < 0 {
		res.Duration = 0
	}
	// 不管成功还是失败，已经收到的正文都要带出去：跑了二十分钟、最后一步报错时，
	// 丢掉全部输出是「转交」最不能接受的结果（失败报告里会标明「可能不完整」）。
	res.Agent = r.agent.Name
	res.Session = r.sessionID()
	if err != nil {
		// 成功路径由 SDK 的响应 barrier 保证正文到齐；失败路径要自己等通知排空。
		r.waitNotifyDrain(m.opts.NotifyDrainBudget)
	}
	got := r.snapshot()
	res.ToolCalls, res.Changed, res.Wrote, res.ReadOutside = got.toolCalls, got.changed, got.wrote, got.readOutside
	res.Notes, res.Denied = got.notes, got.denied
	res.ChangedOver, res.WroteOver, res.ReadOutsideOver = got.changedOver, got.wroteOver, got.readOutsideOver
	res.Report = r.reportText()
	m.ledgerFinish(r, err) // 先标终态再放行 Wait：关停时 Close 等到 done 就返回，慢一步就丢
	r.complete(res, err)
	// 终态记录留着给 Wait 取（Kill 之后再来 Wait、或先 Kill 再 Wait 都要能拿到报告）；
	// 由下一次 Start 按序裁掉旧记录，不让表无限长。
}

// execute 一次转交的协议流程。
func (m *Manager) execute(r *run) (port.DelegateRun, error) {
	wd := m.workDir(r.spec, r.agent)
	// 根集在这里算定：cwd + 这次调用批准过的额外目录（D279）。解析失败就整次失败——
	// 一个解析不出的根会让「在不在范围里」变成猜，而猜错的两边都是坏结果。
	roots, err := pathres.NewRootSet(append([]string{wd}, r.spec.Roots...))
	if err != nil {
		return port.DelegateRun{}, fmt.Errorf("session root set: %w", err)
	}
	r.setRoots(roots)
	proc, err := m.spawn(r, wd)
	if err != nil {
		return port.DelegateRun{}, err
	}
	defer proc.terminate(m.opts.Grace)

	conn := acp.NewClientSideConnection(&client{m: m, r: r}, proc.stdin, proc.stdout)
	// SDK 的诊断日志按字节刷到 stderr（正常收尾也会打一条 "connection closed"）；
	// 用户面的报因我们自己在 abort 里带 stderr 尾巴，这里不需要再吵一次。
	conn.SetLogger(slog.New(slog.NewTextHandler(io.Discard, nil)))

	// 对端断连（崩溃、退出）：让阻塞中的调用尽快返回，报因里带 stderr 尾部。
	peerDone := make(chan struct{})
	go func() {
		select {
		case <-conn.Done():
			r.setPeerGone()
			r.cancelFn()
		case <-r.done:
		}
		close(peerDone)
	}()

	hctx, cancel := context.WithTimeout(r.cancelCtx, m.opts.Handshake)
	init, err := conn.Initialize(hctx, acp.InitializeRequest{
		ProtocolVersion: 1,
		// 申报 fs（A2）：读写都经我们过手，`Changed` 才有事实可依（D283）。
		// 这是**经手**不是围栏——代理自己起 shell 的路看不见（与根集同一条口径）。
		ClientCapabilities: acp.ClientCapabilities{
			Fs: acp.FileSystemCapabilities{ReadTextFile: true, WriteTextFile: true},
		},
	})
	cancel()
	if err != nil {
		return m.abort(r, proc, fmt.Errorf("initialize: %w", err))
	}
	if init.ProtocolVersion != 1 {
		return m.abort(r, proc, fmt.Errorf("the agent speaks ACP v%d and this build only supports v1; pick an agent that supports v1", int(init.ProtocolVersion)))
	}
	if init.AgentInfo != nil {
		m.noteLearned(r.agent.Name, deref(init.AgentInfo.Title), init.AgentInfo.Version)
	}
	// authMethods 是「这个代理支持哪些登录方式」，不是「你现在没登录」——已登录的代理
	// 也照报（gemini-cli 这类）。所以这里只记下清单，真需要登录时 session/new 会回
	// -32000（auth required），那时再给出可执行的指引。

	nctx, cancelNew := context.WithTimeout(r.cancelCtx, m.opts.Handshake)
	ns, err := conn.NewSession(nctx, acp.NewSessionRequest{
		Cwd:        wd,
		McpServers: []acp.McpServer{}, // 不转发我们的 MCP server（§5.18：两套授权链不搅在一起）
	})
	cancelNew()
	if err != nil {
		return m.abort(r, proc, m.sessionError(err, init))
	}
	sid := ns.SessionId
	r.setSession(string(sid))

	// 总闸：到点走与停止键同一条取消路径（而不是杀进程走人）——先把机会留给
	// 「收成 cancelled、保住已交付的正文」。
	if m.opts.Timeout > 0 {
		timer := time.AfterFunc(m.opts.Timeout, func() { r.requestStop(stopTimeout) })
		defer timer.Stop()
	}

	// Prompt 用**独立 ctx**：取消必须显式发通知（§5.18 第 7 步）。
	// 从这里起失败要保住已交付的正文（D268），失败收尾据此等 SDK 通知排空。
	r.armNotifyDrain()
	resCh := make(chan promptOutcome, 1)
	go func() {
		resp, err := conn.Prompt(context.Background(), acp.PromptRequest{
			SessionId: sid,
			Prompt:    []acp.ContentBlock{acp.TextBlock(r.spec.Task)},
		})
		resCh <- promptOutcome{resp: resp, err: err}
	}()

	out := port.DelegateRun{Agent: r.agent.Name}
	var stopped stopKind
	select {
	case res := <-resCh:
		if res.err != nil {
			r.markPeerIfClosed(conn)
			return m.abort(r, proc, fmt.Errorf("session/prompt: %w", res.err))
		}
		out.StopReason = string(res.resp.StopReason)
	case <-r.stopCh:
		stopped = r.stopKind()
		// 协议明文：取消时要把待应答的权限请求全部回 cancelled、并显式发 session/cancel。
		_ = conn.Cancel(context.Background(), acp.CancelNotification{SessionId: sid})
		select {
		case res := <-resCh:
			if res.err != nil {
				r.markPeerIfClosed(conn)
				return m.abort(r, proc, fmt.Errorf("session/prompt: %w", res.err))
			}
			out.StopReason = string(res.resp.StopReason)
			if out.StopReason == "" {
				out.StopReason = string(acp.StopReasonCancelled)
			}
		case <-time.After(m.opts.Grace):
			// 宽限用完仍不返回：杀进程组，报告按「已取消」回填而不是报错。
			_ = proc.kill()
			select {
			case <-resCh:
			case <-time.After(2 * time.Second):
			}
			out.StopReason = string(acp.StopReasonCancelled)
			r.note("the agent did not acknowledge the cancel within %s; its process group was terminated", m.opts.Grace)
		}
	}

	switch stopped {
	case stopKilled, stopShutdown:
		out.Killed = true
		r.note("the delegation was stopped on request; the report below may be incomplete")
	case stopTimeout:
		out.Killed = true
		r.note("the delegation exceeded delegate.timeout_sec (%s) and was stopped; the report below may be incomplete", m.opts.Timeout)
	}
	if out.StopReason == string(acp.StopReasonCancelled) && stopped == stopNone {
		r.note("the agent cancelled the turn on its own")
	}
	if denied := r.deniedCount(); denied > 0 {
		r.note("%d permission request(s) were rejected (unattended, or you were not in the session that started the delegation)", denied)
	}
	return out, nil
}

// deref 取可选字符串的值（agentInfo 的 title/version 是指针）。
func deref(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

// promptOutcome Prompt 的返回（送进 channel，避免阻塞）。
type promptOutcome struct {
	resp acp.PromptResponse
	err  error
}

// abort 失败收尾：停止请求优先（停止键/关停不是错误），否则把 stderr 尾部带进报因。
func (m *Manager) abort(r *run, proc *process, err error) (port.DelegateRun, error) {
	if kind := r.stopKind(); kind != stopNone {
		out := port.DelegateRun{
			Agent:      r.agent.Name,
			StopReason: string(acp.StopReasonCancelled),
			Killed:     kind != stopNone,
		}
		// 「会话还没建起来」只在真是那个阶段时这么说：建好之后被停也照原样念，
		// 用户会以为它一步都没跑。
		early := ""
		if r.sessionID() == "" {
			early = " before the session was established"
		}
		switch kind {
		case stopTimeout:
			r.note("the delegation exceeded delegate.timeout_sec (%s) and was stopped%s", m.opts.Timeout, early)
		default:
			r.note("the delegation was stopped on request%s", early)
		}
		cur := r.snapshot()
		if cur.denied > 0 {
			r.note("%d permission request(s) were rejected", cur.denied)
			cur.notes = r.snapshot().notes
		}
		out.Notes = cur.notes
		return out, nil
	}
	// 进程已经结束同样要等：这才是「崩了」的实锤，watchdog 的标志只是旁证（它可能与
	// Prompt 的返回抢同一个 Done()，慢一步）。两条都看，避免报因丢掉 stderr。
	if r.peerGoneNow() || proc.exited() {
		// conn.Done() 只保证 stdout 到了 EOF，stderr 的拷贝 goroutine 可能还没排空。
		proc.waitStderr(stderrDrainBudget)
		if tail := proc.stderrTail(); tail != "" {
			return port.DelegateRun{}, fmt.Errorf("%w; the agent exited (stderr: %s)", err, tail)
		}
		return port.DelegateRun{}, fmt.Errorf("%w; the agent exited", err)
	}
	if tail := proc.stderrTail(); tail != "" {
		return port.DelegateRun{}, fmt.Errorf("%w (stderr: %s)", err, tail)
	}
	return port.DelegateRun{}, err
}

// authError 把 authMethods 拼成可执行指引：本版不实现 authenticate（§5.18「登录」）。
func (m *Manager) authError(a Agent, methods []acp.AuthMethod) error {
	var names []string
	for _, am := range methods {
		switch {
		case am.Agent != nil && am.Agent.Name != "":
			names = append(names, am.Agent.Name)
		case am.Terminal != nil && am.Terminal.Name != "":
			names = append(names, am.Terminal.Name)
		case am.EnvVar != nil && am.EnvVar.Name != "":
			names = append(names, am.EnvVar.Name)
		}
	}
	list := strings.Join(names, ", ")
	if list == "" {
		list = "the agent's own login"
	}
	return fmt.Errorf("agent %q is not signed in yet (auth methods: %s); "+
		"run %q once in its own terminal to log in, then retry the delegation",
		a.Name, list, a.Command)
}

// sessionError 建会话失败：认不出来时把「需要登录」这个最常见原因写清。
func (m *Manager) sessionError(err error, init acp.InitializeResponse) error {
	var re *acp.RequestError
	if errors.As(err, &re) {
		// -32000 = auth required：这才是「真没登录」，此时 authMethods 那份清单才有用。
		if re.Code == -32000 {
			return m.authError(Agent{Command: ""}, init.AuthMethods)
		}
		return fmt.Errorf("session/new was rejected (code %d: %s)", re.Code, re.Message)
	}
	return fmt.Errorf("session/new: %w", err)
}

// spawn 起子进程：环境剔除 AQUARIUS_*、CREATE_NO_WINDOW、独立进程组（§5.18 进程卫生）。
func (m *Manager) spawn(r *run, wd string) (*process, error) {
	env, err := m.resolveEnv(r.agent.Env)
	if err != nil {
		return nil, fmt.Errorf("environment for %s: %w", r.agent.Name, err)
	}
	p, err := startProcess(r.agent.Command, r.agent.Args, wd, env)
	if err != nil {
		return nil, fmt.Errorf("start %s: %w", r.agent.Command, err)
	}
	r.setProc(p)
	m.ledgerPID(r.id, p.cmd.Process.Pid)
	return p, nil
}

// resolveEnv 解析 env 里的 `secret:` 引用（§9：真实值只经 port.Secrets 按名取用）。
func (m *Manager) resolveEnv(declared map[string]string) (map[string]string, error) {
	if len(declared) == 0 {
		return nil, nil
	}
	out := make(map[string]string, len(declared))
	for k, v := range declared {
		if !strings.HasPrefix(v, "secret:") {
			out[k] = v
			continue
		}
		name := strings.TrimPrefix(v, "secret:")
		if name == "" {
			return nil, fmt.Errorf("%s: secret: reference is missing a name", k)
		}
		if m.opts.Secrets == nil {
			return nil, fmt.Errorf("%s: no secret store is configured", k)
		}
		val, err := m.opts.Secrets.Get(context.Background(), name)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", k, err)
		}
		out[k] = val
	}
	return out, nil
}

// childEnv 子进程环境：父环境剔除 AQUARIUS_*（宿主密钥不随继承外泄），再加声明项。
func childEnv(declared map[string]string) []string {
	base := environ()
	out := make([]string, 0, len(base)+len(declared))
	for _, kv := range base {
		if strings.HasPrefix(kv, "AQUARIUS_") {
			continue
		}
		out = append(out, kv)
	}
	for k, v := range declared {
		out = append(out, k+"="+v)
	}
	return out
}
