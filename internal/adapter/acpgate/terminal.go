package acpgate

// 申报 terminal 之后的五个 handler（A3）：命令由 Aquarius 起进程，输出由 Aquarius 收。
//
// 为什么接：走这条路，我们才知道代理要跑什么、跑完退出码是多少；更重要的是**能拒**、
// 能停。代理自己起 shell 的那条路（opencode 就走那条）永远看不见——两个实例并存的
// 事实要跟用户讲清（guide）。
//
// 这不是沙箱。根集对命令只用来拒绝越界的起点，命令一起来就能 cd 出去；协议自己也写着
// 「It confines shell commands only when a sandbox enforces it」，而 Aquarius 没有沙箱。

import (
	"context"
	"fmt"
	"os/exec"
	"sort"
	"strings"
	"sync"
	"unicode/utf8"

	acp "github.com/coder/acp-go-sdk"

	"github.com/Tonyjh07/Aquarius/internal/i18n"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

const (
	// maxLiveTerminals 一次转交里同时活着的终端上限：代理并行跑几条命令是常态，
	// 但它不是终端复用器——四条以上同时在跑，更像它没在等结果。
	maxLiveTerminals = 4
	// maxTerminalCreates 一次转交里创建过的终端总数（含已 release 的）。代理通常
	// create / wait / release 一条走完一条，32 条已经够一整轮排查。
	maxTerminalCreates = 32
	// defaultTerminalByteLimit 代理没给 outputByteLimit 时的单条缓冲上限。
	defaultTerminalByteLimit = 1 << 20
	// maxTerminalByteLimit 代理给的上限能有多大。超了就拒，不静默降到 8 MiB：
	// 悄悄少给会让代理以为拿全了。
	maxTerminalByteLimit = 8 << 20
	// terminalBufferBudget 本次转交里**当前占用**的输出缓冲合计（release 归还）。
	terminalBufferBudget = 16 << 20
)

// headBuffer 有界输出缓冲：超过上限时**从头**丢，并且丢在字符边界上。
//
// 方向不能反。协议要求代理看到的是「累计全文（被截断时是它的最长后缀）」，而主流实现
// 按**字符串长度差**取增量（kimi 每 250 ms 取一次）。留头丢尾会让长度忽增忽减，增量
// 就错位；丢头则长度只增不减，取到的永远是新增的那一段。
type headBuffer struct {
	mu        sync.Mutex
	max       int
	buf       []byte
	truncated bool
}

func newHeadBuffer(max int) *headBuffer { return &headBuffer{max: max} }

// Write 追加（满足 io.Writer：命令的 stdout 与 stderr 都写到这里）。
func (b *headBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.buf = append(b.buf, p...)
	if len(b.buf) <= b.max {
		return len(p), nil
	}
	b.truncated = true
	cut := len(b.buf) - b.max
	for cut < len(b.buf) && !utf8.RuneStart(b.buf[cut]) {
		// 越过半个字符再丢：截断本身不能产出非法 UTF-8。协议允许保留量略少于上限。
		cut++
	}
	b.buf = append(b.buf[:0], b.buf[cut:]...)
	return len(p), nil
}

// Visible 当前可见的输出，以及是否被截断过。
//
// 命令输出可以不是 UTF-8（Windows 上的本地代码页、二进制残留）：非法字节换成 U+FFFD。
// 末尾那个还没写完的字符先不显示——显示成替换字符会让按长度取增量的对端错位，
// 而它下一轮就会被补全。
func (b *headBuffer) Visible() (string, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	s := string(b.buf)
	if utf8.ValidString(s) {
		return s, b.truncated
	}
	return strings.ToValidUTF8(trimIncompleteTail(s), "\uFFFD"), b.truncated
}

// trimIncompleteTail 去掉末尾那段「起了头但没写完」的 UTF-8 字节。
func trimIncompleteTail(s string) string {
	for i := len(s) - 1; i >= 0 && i >= len(s)-utf8.UTFMax; i-- {
		if !utf8.RuneStart(s[i]) {
			continue
		}
		if !utf8.FullRuneInString(s[i:]) {
			return s[:i]
		}
		break
	}
	return s
}

// terminal 一条被我们代跑的命令。
type terminal struct {
	id   string
	cmd  *exec.Cmd
	out  *headBuffer
	done chan struct{} // 进程退出（并回填退出信息）之后关闭
	// code/sig 只由等退出的那条 goroutine 在关闭 done 之前写，读方一律先等 done：
	// channel 关闭是 happens-before，所以这里不需要再加锁。
	code *int
	sig  string
}

// exited 进程是否已结束；结束则同时给出退出码与信号。
func (t *terminal) exited() (*int, string, bool) {
	select {
	case <-t.done:
		return t.code, t.sig, true
	default:
		return nil, "", false
	}
}

// kill 杀掉进程树。幂等：已经退出的终端什么也不做，退出码保持原样——
// 协议要求 kill 之后 wait_for_exit 仍能给出它。
func (t *terminal) kill() {
	if t == nil {
		return
	}
	select {
	case <-t.done:
		return
	default:
	}
	_ = killTree(t.cmd)
}

// terminalTable 一次转交的终端表。账目口径与越界读（D284）一致：上限是**本次转交**的，
// 不跨次累积。
type terminalTable struct {
	mu    sync.Mutex
	byID  map[string]*terminal
	next  int // 已分配过的 id 序号（不回收：id 复用会让迟到的 output 打到另一条命令上）
	total int // 累计创建数
	live  int // 活着（含正在起）的条数
	inuse int // 当前占用的输出缓冲字节
	asks  int // 已问过用户几次
}

func (t *terminalTable) init() {
	t.mu.Lock()
	if t.byID == nil {
		t.byID = map[string]*terminal{}
	}
	t.mu.Unlock()
}

// reserve 先占一个名额与一块缓冲额度，再起进程：顺序反过来的话，起进程期间
// 并发的 create 都会认为「还有空位」。
func (t *terminalTable) reserve(bytes int) (string, error) {
	t.init()
	t.mu.Lock()
	defer t.mu.Unlock()
	switch {
	case t.total >= maxTerminalCreates:
		return "", fmt.Errorf("this delegation already created %d terminals (at most %d)", t.total, maxTerminalCreates)
	case t.live >= maxLiveTerminals:
		return "", fmt.Errorf("%d terminals are already running (at most %d)", t.live, maxLiveTerminals)
	case t.inuse+bytes > terminalBufferBudget:
		return "", fmt.Errorf("%d bytes of output are already held (at most %d); release a terminal first", t.inuse, terminalBufferBudget)
	}
	t.next++
	t.total++
	t.live++
	t.inuse += bytes
	return fmt.Sprintf("t%02d", t.next), nil
}

// attach 把起好的进程挂到预留的 id 上。
func (t *terminalTable) attach(term *terminal) {
	t.mu.Lock()
	t.byID[term.id] = term
	t.mu.Unlock()
}

// unreserve 起进程失败、或用户拒了：把预留的名额与额度还回去。
func (t *terminalTable) unreserve(id string, bytes int) {
	t.mu.Lock()
	t.live--
	t.total--
	t.inuse -= bytes
	t.mu.Unlock()
}

// get 按 id 取终端（release 之后就取不到了）。
func (t *terminalTable) get(id string) (*terminal, bool) {
	t.init()
	t.mu.Lock()
	defer t.mu.Unlock()
	term, ok := t.byID[id]
	return term, ok
}

// drop 释放一个终端并归还它的缓冲额度。
func (t *terminalTable) drop(id string) (*terminal, bool) {
	t.init()
	t.mu.Lock()
	defer t.mu.Unlock()
	term, ok := t.byID[id]
	if !ok {
		return nil, false
	}
	delete(t.byID, id)
	t.live--
	if term != nil {
		t.inuse -= term.out.max
	}
	return term, true
}

// killAll 转交收尾：取出全部还活着的终端（调用方逐个 kill）。
func (t *terminalTable) killAll() []*terminal {
	t.init()
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make([]*terminal, 0, len(t.byID))
	for _, term := range t.byID {
		out = append(out, term)
	}
	return out
}

// askUsed 已问过几次（额度按「问过」算，不按放行算）。
func (t *terminalTable) askUsed() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.asks
}

// countAsk 记一次询问。
func (t *terminalTable) countAsk() {
	t.mu.Lock()
	t.asks++
	t.mu.Unlock()
}

// CreateTerminal 替代理起一条命令（`terminal/create`）。
//
// 顺序是 校验 → 占额度 → 确认 → 起进程：确认放在起进程之前，拒了就什么都没发生。
func (c *client) CreateTerminal(ctx context.Context, p acp.CreateTerminalRequest) (acp.CreateTerminalResponse, error) {
	if err := c.termGate(ctx, p.SessionId); err != nil {
		return acp.CreateTerminalResponse{}, err
	}
	wd, err := c.terminalDir(p.Cwd)
	if err != nil {
		return acp.CreateTerminalResponse{}, err
	}
	limit := defaultTerminalByteLimit
	if p.OutputByteLimit != nil {
		limit = *p.OutputByteLimit
		if limit <= 0 {
			return acp.CreateTerminalResponse{}, terminalDenied("outputByteLimit must be positive")
		}
		if limit > maxTerminalByteLimit {
			return acp.CreateTerminalResponse{}, terminalDenied(
				fmt.Sprintf("outputByteLimit is too large (%d bytes, at most %d)", limit, maxTerminalByteLimit))
		}
	}
	env := terminalEnv(p.Env)
	id, err := c.r.terms.reserve(limit)
	if err != nil {
		// 上限不是参数错，也不是用户拒的：写清楚是哪一条，代理才有得选（少开一条、
		// 或者先 release）。
		c.r.note("terminal/create was refused: %s", err)
		return acp.CreateTerminalResponse{}, terminalDenied(err.Error())
	}
	line := commandText(p.Command, p.Args)
	if ok, why := c.allowTerminal(ctx, terminalPrompt(c.r.agent.Name, line, wd, env)); !ok {
		c.r.terms.unreserve(id, limit)
		c.r.note("command was refused (%s): %s", line, why)
		return acp.CreateTerminalResponse{}, terminalDenied(why)
	}
	term, err := startTerminal(id, p.Command, p.Args, wd, env, limit)
	if err != nil {
		c.r.terms.unreserve(id, limit)
		c.r.note("command could not start (%s): %v", line, err)
		return acp.CreateTerminalResponse{}, terminalDenied(fmt.Sprintf("cannot start the command: %v", err))
	}
	c.r.terms.attach(term)
	return acp.CreateTerminalResponse{TerminalId: id}, nil
}

// TerminalOutput 当前输出与（已结束时的）退出状态。
func (c *client) TerminalOutput(ctx context.Context, p acp.TerminalOutputRequest) (acp.TerminalOutputResponse, error) {
	if err := c.termGate(ctx, p.SessionId); err != nil {
		return acp.TerminalOutputResponse{}, err
	}
	term, ok := c.r.terms.get(p.TerminalId)
	if !ok {
		return acp.TerminalOutputResponse{}, unknownTerminal(p.TerminalId)
	}
	out, truncated := term.out.Visible()
	// Output 为空是合法的：命令还没打印任何东西。协议只要求这个字段在场（schema 是
	// "type": "string"），Go SDK 的 Validate 只在入站请求上跑，出站不校验。
	resp := acp.TerminalOutputResponse{Output: out, Truncated: truncated}
	if code, sig, done := term.exited(); done {
		resp.ExitStatus = exitStatusOf(code, sig)
	}
	return resp, nil
}

// WaitForTerminalExit 等命令结束。三个出口：进程退出、请求 ctx 取消、本次转交被停——
// 少了最后一个，代理不发 cancel 时我们会永远挂着。
func (c *client) WaitForTerminalExit(ctx context.Context, p acp.WaitForTerminalExitRequest) (acp.WaitForTerminalExitResponse, error) {
	if err := c.termGate(ctx, p.SessionId); err != nil {
		return acp.WaitForTerminalExitResponse{}, err
	}
	term, ok := c.r.terms.get(p.TerminalId)
	if !ok {
		return acp.WaitForTerminalExitResponse{}, unknownTerminal(p.TerminalId)
	}
	select {
	case <-term.done:
	case <-ctx.Done():
		return acp.WaitForTerminalExitResponse{}, acp.NewRequestCancelled(
			map[string]any{"error": "the request was cancelled"})
	case <-c.r.stopCh:
		return acp.WaitForTerminalExitResponse{}, acp.NewRequestCancelled(
			map[string]any{"error": "the delegation is stopping"})
	}
	code, sig, _ := term.exited()
	resp := acp.WaitForTerminalExitResponse{ExitCode: code}
	if sig != "" {
		resp.Signal = &sig
	}
	return resp, nil
}

// KillTerminal 杀进程树，但**不释放**：之后仍要能取输出与退出码（kimi 就是这么用的）。
func (c *client) KillTerminal(ctx context.Context, p acp.KillTerminalRequest) (acp.KillTerminalResponse, error) {
	if err := c.termGate(ctx, p.SessionId); err != nil {
		return acp.KillTerminalResponse{}, err
	}
	term, ok := c.r.terms.get(p.TerminalId)
	if !ok {
		return acp.KillTerminalResponse{}, unknownTerminal(p.TerminalId)
	}
	term.kill()
	return acp.KillTerminalResponse{}, nil
}

// ReleaseTerminal 释放：活着的先杀，再清掉缓冲；之后这个 id 一律「不认识」。
func (c *client) ReleaseTerminal(ctx context.Context, p acp.ReleaseTerminalRequest) (acp.ReleaseTerminalResponse, error) {
	if err := c.termGate(ctx, p.SessionId); err != nil {
		return acp.ReleaseTerminalResponse{}, err
	}
	term, ok := c.r.terms.drop(p.TerminalId)
	if !ok {
		return acp.ReleaseTerminalResponse{}, unknownTerminal(p.TerminalId)
	}
	term.kill()
	return acp.ReleaseTerminalResponse{}, nil
}

// termGate terminal 请求的共同前置（与 fsGate 同口径，只是没有路径参数）。
func (c *client) termGate(ctx context.Context, sid acp.SessionId) error {
	if c.r.stopKind() != stopNone || ctx.Err() != nil {
		return acp.NewRequestCancelled(map[string]any{"error": "the delegation is stopping"})
	}
	if cur := c.r.sessionID(); cur == "" || string(sid) != cur {
		return acp.NewInvalidParams(map[string]any{"error": "sessionId does not belong to this delegation"})
	}
	return nil
}

// terminalDir 命令的起点：缺省 = 会话 cwd，给了就必须是绝对路径且落在根集之内。
//
// 越界**直接拒、不问**：cwd 只是「从哪儿开始」，命令起来随时能 cd 出去，为它弹一个
// 确认框拦不住任何东西，只会让用户以为被拦住了。
func (c *client) terminalDir(cwd *string) (string, error) {
	dir := strings.TrimSpace(deref(cwd))
	if dir == "" {
		dir = c.r.workDir()
	}
	if !port.AbsPath(dir) {
		return "", acp.NewInvalidParams(map[string]any{"error": "cwd must be an absolute path", "cwd": dir})
	}
	resolved, root, err := c.r.rootSet().Locate(dir)
	if err != nil {
		return "", terminalDenied("cannot resolve the working directory: " + err.Error())
	}
	if root == "" {
		c.r.note("a command was refused: its working directory is outside the session root set: %s", resolved)
		return "", terminalDenied(reasonOutside + ": the working directory " + resolved)
	}
	return resolved, nil
}

// unknownTerminal id 不认识（打错，或者已经 release）：参数错，不是我们的判断。
func unknownTerminal(id string) *acp.RequestError {
	return acp.NewInvalidParams(map[string]any{"error": "unknown terminalId", "terminalId": id})
}

// terminalDenied 我们自己拒掉的 terminal 请求。与 fsDenied 同一个码、同一个理由：
// -32603 既不是参数错也不是方法不存在，原因写在 message 里，代理读得懂也换得了做法。
func terminalDenied(reason string) *acp.RequestError {
	return &acp.RequestError{Code: -32603, Message: reason}
}

// reasonTerminalLimit 确认额度用完时的原因：说清是「额度」而不是「范围」。
const reasonTerminalLimit = "the command-approval limit for this delegation is used up"

// allowTerminal 一条命令的处置：额度内问一次，额度用完（或配置成 0）直接拒并留档。
//
// 不自造命令白/黑名单：四态策略表是 v0.7.0 的事，这里先塞一套，等于把同一套字段口径
// 写两遍（plan.md A3）。
func (c *client) allowTerminal(ctx context.Context, prompt string) (bool, string) {
	limit := c.m.terminalAsks()
	switch {
	case limit == 0:
		reason := reasonTerminalLimit + ": delegate.terminal_ask_max is 0"
		return false, reason
	case limit > 0 && c.r.terms.askUsed() >= limit:
		return false, fmt.Sprintf("%s (%d)", reasonTerminalLimit, limit)
	}
	c.r.terms.countAsk()
	return c.askUser(ctx, prompt)
}

// terminalPrompt 命令确认的用户面文案：命令、目录、环境变量三者都出现（策略匹配的是
// 三者，不是命令名），环境变量按名排序并截断——它是这一屏里最不重要的那一行。
func terminalPrompt(agent, line, wd string, env map[string]string) string {
	s := i18n.Tf("acp.confirm.terminal", "agent", agent, "command", clip(line, 500), "cwd", wd)
	if text := envText(env); text != "" {
		s += "\n" + i18n.Tf("acp.confirm.terminal_env", "env", text)
	}
	return s
}

// envText 环境变量的一行描述（按名排序；值截断）。
func envText(env map[string]string) string {
	if len(env) == 0 {
		return ""
	}
	keys := make([]string, 0, len(env))
	for k := range env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, k+"="+clip(env[k], 60))
	}
	return clip(strings.Join(parts, " "), 300)
}

// clip 截到 max 个字符（确认框放不下长命令；截了就带省略号，别让人以为命令就这么短）。
func clip(s string, max int) string {
	rs := []rune(s)
	if len(rs) <= max {
		return s
	}
	return string(rs[:max]) + "…"
}

// commandText 命令行的一行写法（确认框与备注里用）。换行保留：脚本里的换行是它的一部分，
// 抹掉会让人看成另一条命令。
func commandText(command string, args []string) string {
	if len(args) == 0 {
		return command
	}
	return command + " " + strings.Join(args, " ")
}

// terminalEnv 代理给的命令环境。剔除 AQUARIUS_*：宿主密钥不跟着命令走（与起子代理
// 那一侧同一条口径，§5.18 进程卫生）。
func terminalEnv(list []acp.EnvVariable) map[string]string {
	if len(list) == 0 {
		return nil
	}
	out := make(map[string]string, len(list))
	for _, kv := range list {
		if kv.Name == "" || strings.HasPrefix(kv.Name, "AQUARIUS_") {
			continue
		}
		out[kv.Name] = kv.Value
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// startTerminal 起命令：不给 shell，直接 exec（`command` + `args` 原样）。
//
// stdout 与 stderr 写同一个 writer：exec 认得出同一个 Writer，合成一根管道，两条流的
// 先后顺序也就保住了（分成两根，输出交错时读不出真实顺序）。
//
// Windows 上 `.cmd` / `.bat`（`npm` 会解析到 `npm.cmd`）不能直接 exec，起不来就照实
// 把错误回给代理，让它换写法——我们不替它拼一层 `cmd.exe /c`，那等于绕开「不给 shell」。
func startTerminal(id, command string, args []string, wd string, env map[string]string, limit int) (*terminal, error) {
	cmd := exec.Command(command, args...)
	cmd.Dir = wd
	cmd.Env = childEnv(env)
	setupProc(cmd)
	buf := newHeadBuffer(limit)
	cmd.Stdout = buf
	cmd.Stderr = buf
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	t := &terminal{id: id, cmd: cmd, out: buf, done: make(chan struct{})}
	go func() {
		_ = cmd.Wait()
		t.recordExit()
		close(t.done)
	}()
	return t, nil
}

// recordExit 回填退出信息（在关闭 done 之前调用一次）。
func (t *terminal) recordExit() {
	st := t.cmd.ProcessState
	if st == nil {
		return
	}
	if code := st.ExitCode(); code >= 0 {
		t.code = &code
	}
	t.sig = exitSignal(st)
}

// exitStatusOf 组装协议里的退出状态。
func exitStatusOf(code *int, sig string) *acp.TerminalExitStatus {
	st := &acp.TerminalExitStatus{ExitCode: code}
	if sig != "" {
		st.Signal = &sig
	}
	return st
}

// closeTerminals 转交收尾：杀掉还活着的终端，不等代理 release——它可能永远不发。
func (r *run) closeTerminals() {
	for _, term := range r.terms.killAll() {
		term.kill()
	}
}
