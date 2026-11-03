// Package acpgate 实现 port.Delegate（§5.18/D244）：把一件复杂任务**整个**交给一个
// ACP 代理进程去跑，我们只收回它的报告。
//
// 我们是客户端，不是代理：只做 ACP v1 的六个方法（initialize / session/new /
// session/prompt / session/update / session/cancel / session/request_permission），
// `fs/*` 与 `terminal/*` 一个都不申报——申报等于承诺替它读写文件、替它管终端，
// 那是一整条新的能力面（D260 的「能力扩张」已排后续）。
//
// 一次转交 = 一个进程、一个 ACP 会话（D261：后台任务模型，派活即返句柄）。进程跑完即关，
// sessionId 不跨次使用——攒两份互相矛盾的历史比丢上下文更糟。
package acpgate

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Tonyjh07/Aquarius/internal/domain/conversation"
	"github.com/Tonyjh07/Aquarius/internal/i18n"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

// Agent 配置里的一个外部代理（delegate.agents 的一项，§5.18）。
type Agent struct {
	Name    string            `json:"name"`
	Command string            `json:"command"`
	Args    []string          `json:"args,omitempty"`
	Env     map[string]string `json:"env,omitempty"` // 值支持 secret:<名>（起进程时经 Secrets 解析）
	WorkDir string            `json:"workdir,omitempty"`
	Enabled bool              `json:"enabled"`
	Default bool              `json:"default,omitempty"`
}

// Options 装配依赖（全部由装配根注入）。
type Options struct {
	Agents []Agent
	// Secrets 解析 env 里的 `secret:` 引用（§9：真实值不落配置/日志）。
	Secrets port.Secrets
	// Confirmer 权限回落（§9）：子代理动手前反问，答不上来就不动手。
	Confirmer port.Confirmer
	// CurrentSession 当前会话（D261 无人应答判定）：转交绑发起会话，用户不在该会话时
	// 确认无人应答——当场拒绝并写进报告，不让转交卡在等确认上。nil = 不判（测试/无 UI）。
	CurrentSession func() conversation.ID
	// Timeout 单次转交总闸（0 = 不限）：防的是进程挂住一直占着并发名额。
	Timeout time.Duration
	// MaxConcurrent 每会话同时进行的转交上限；MaxTotal 全局上限（0 = 用端口缺省）。
	MaxConcurrent int
	MaxTotal      int
	// Logf 运行日志（用户面中文）；nil = 丢弃。
	Logf func(format string, args ...any)
	// LookPath 可执行探测（测试可覆盖）；nil = exec.LookPath。
	LookPath func(string) (string, error)
	// Grace 取消后等它收成 cancelled 的宽限（缺省 2s；测试缩短）。
	Grace time.Duration
	// Handshake 单次启动+握手+建会话的上限（缺省 60s）。
	Handshake time.Duration
	// Now 时钟（测试用）。
	Now func() time.Time
	// Home 缺省工作目录（缺省 os.UserHomeDir）。
	Home string
	// LedgerPath 转交台账文件路径（空 = 不记；D269）。由装配根给到 `<profile>/jobs/delegates.json`
	// ——与任务台账同目录：它们回答的是同一类问题「上次退出时还有什么没收尾」。
	LedgerPath string
}

// Manager port.Delegate 实现。
type Manager struct {
	opts Options

	mu     sync.Mutex
	runs   map[port.DelegateID]*run
	seq    int
	closed bool // Close 之后不再接受新转交（否则关停期间会起一个没人管的进程）
	// learned initialize 读回的 agentInfo（Title/Version）：只在第一次转交后才有，
	// 启动时只探「能不能执行」（不拿用户时间换一张清单）。
	learned map[string]learnedInfo
	// ledgerMu 台账读写串行（见 ledger.go；与 runs 无关，不必同锁）。
	ledgerMu sync.Mutex
}

type learnedInfo struct{ title, version string }

var _ port.Delegate = (*Manager)(nil)

// New 创建转交管理器。零代理是合法配置（工具不注册、/delegate 给配置指引）。
func New(opts Options) *Manager {
	if opts.LookPath == nil {
		opts.LookPath = exec.LookPath
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.Grace <= 0 {
		opts.Grace = 2 * time.Second
	}
	if opts.Handshake <= 0 {
		opts.Handshake = 60 * time.Second
	}
	if opts.MaxConcurrent <= 0 {
		opts.MaxConcurrent = port.DefaultDelegateMaxConcurrent
	}
	if opts.MaxTotal <= 0 {
		opts.MaxTotal = port.DefaultDelegateMaxTotal
	}
	if opts.Home == "" {
		if home, err := os.UserHomeDir(); err == nil {
			opts.Home = home
		}
	}
	if opts.Logf == nil {
		opts.Logf = func(string, ...any) {}
	}
	return &Manager{opts: opts, runs: map[port.DelegateID]*run{}, learned: map[string]learnedInfo{}}
}

// List 清单：只探「命令在不在 PATH 上」，不握手（§5.18）。
func (m *Manager) List(context.Context) ([]port.DelegateInfo, error) {
	out := make([]port.DelegateInfo, 0, len(m.opts.Agents))
	for _, a := range m.opts.Agents {
		info := port.DelegateInfo{Name: a.Name, Command: commandLine(a)}
		m.mu.Lock()
		li, ok := m.learned[a.Name]
		m.mu.Unlock()
		if ok {
			info.Title, info.Version = li.title, li.version
		}
		switch {
		case !a.Enabled:
			info.Note = i18n.T("acp.note.disabled")
		case strings.TrimSpace(a.Command) == "":
			info.Note = i18n.T("acp.note.no_command")
		default:
			if _, err := m.opts.LookPath(a.Command); err != nil {
				info.Note = i18n.Tf("acp.note.not_on_path", "command", a.Command)
			} else {
				info.Available = true
			}
		}
		out = append(out, info)
	}
	return out, nil
}

// Available 可用的代理（装配根据此决定是否注册 delegate 工具）。
func (m *Manager) Available(ctx context.Context) []port.DelegateInfo {
	list, err := m.List(ctx)
	if err != nil {
		return nil
	}
	out := list[:0]
	for _, a := range list {
		if a.Available {
			out = append(out, a)
		}
	}
	return out
}

// Start 派活：校验 → 占一个并发名额 → 起进程 → **立刻**返回句柄（D261）。
// 报告晚些经收尾观察者注入回发起会话。
func (m *Manager) Start(ctx context.Context, spec port.DelegateSpec) (port.DelegateTask, error) {
	if err := ctx.Err(); err != nil {
		return port.DelegateTask{}, err
	}
	agent, err := m.pick(spec.Agent)
	if err != nil {
		return port.DelegateTask{}, err
	}
	task := strings.TrimSpace(spec.Task)
	if task == "" {
		return port.DelegateTask{}, fmt.Errorf("missing task")
	}
	if n := len([]rune(task)); n > port.MaxDelegateTaskChars {
		return port.DelegateTask{}, fmt.Errorf("task description is too long (%d chars, at most %d)", n, port.MaxDelegateTaskChars)
	}
	if strings.TrimSpace(spec.Session) != "" {
		return port.DelegateTask{}, fmt.Errorf("session reuse is not supported in this build")
	}
	if wd := strings.TrimSpace(spec.WorkDir); wd != "" && !filepath.IsAbs(wd) {
		return port.DelegateTask{}, fmt.Errorf("workdir must be an absolute path, got %q", spec.WorkDir)
	}
	sid, _ := port.SessionIDFrom(ctx)

	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return port.DelegateTask{}, fmt.Errorf("the application is shutting down; no new delegation can start")
	}
	var total, mine int
	for _, r := range m.runs {
		if r.finished() {
			continue
		}
		total++
		if sid != "" && r.session == sid {
			mine++
		}
	}
	if total >= m.opts.MaxTotal {
		m.mu.Unlock()
		return port.DelegateTask{}, fmt.Errorf("too many delegations are running (%d, at most %d); wait for one to finish or stop one first", total, m.opts.MaxTotal)
	}
	if sid != "" && mine >= m.opts.MaxConcurrent {
		m.mu.Unlock()
		return port.DelegateTask{}, fmt.Errorf("this session already has %d delegations running (at most %d)", mine, m.opts.MaxConcurrent)
	}
	m.seq++
	id := port.DelegateID(fmt.Sprintf("d%03d", m.seq))
	r := newRun(id, agent, spec, sid)
	m.runs[id] = r
	m.trimFinishedLocked()
	m.mu.Unlock()

	m.ledgerAdd(r) // 先落台账再起进程：进程一起来就可能失控，记不下就无从对账
	go m.run(r)
	return port.DelegateTask{ID: id, Agent: agent.Name}, nil
}

// Wait 阻塞到终态。取消 ctx 只是不再等，**不会**停掉转交（那要走 Kill）。
func (m *Manager) Wait(ctx context.Context, id port.DelegateID) (port.DelegateRun, error) {
	r, err := m.lookup(id)
	if err != nil {
		return port.DelegateRun{}, err
	}
	select {
	case <-r.done:
		return r.result()
	case <-ctx.Done():
		return port.DelegateRun{}, ctx.Err()
	}
}

// Kill 终止一次转交：先发 session/cancel 通知，等它把 prompt 收成 cancelled，
// 宽限后仍不返回就杀进程组（§5.18）。
func (m *Manager) Kill(ctx context.Context, id port.DelegateID) error {
	r, err := m.lookup(id)
	if err != nil {
		return err
	}
	if r.finished() {
		// 停止键恰好落在「刚好跑完」那一瞬是正常事：按幂等成功回，不要报错让人以为没停掉。
		return nil
	}
	r.requestStop(stopKilled)
	select {
	case <-r.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Close 关停：终止全部进行中的转交（D262：ACP 会话不能恢复，留着只有一个后果——
// 一路失去监督的子代理继续按自己的判断改文件）。
func (m *Manager) Close() {
	m.mu.Lock()
	m.closed = true
	list := make([]*run, 0, len(m.runs))
	for _, r := range m.runs {
		list = append(list, r)
	}
	m.mu.Unlock()
	for _, r := range list {
		if !r.finished() {
			r.requestStop(stopShutdown)
		}
	}
	for _, r := range list {
		select {
		case <-r.done:
		case <-time.After(m.opts.Grace + 3*time.Second):
		}
	}
}

// keepFinished 表里最多留多少个已结束的转交（给迟到的 Wait 取报告）。
const keepFinished = 32

// trimFinishedLocked 裁掉最旧的已结束记录（调用方持有 m.mu）。
func (m *Manager) trimFinishedLocked() {
	var done []port.DelegateID
	for id, r := range m.runs {
		if r.finished() {
			done = append(done, id)
		}
	}
	if len(done) <= keepFinished {
		return
	}
	sort.Slice(done, func(i, j int) bool { return m.runs[done[i]].started.Before(m.runs[done[j]].started) })
	for _, id := range done[:len(done)-keepFinished] {
		delete(m.runs, id)
	}
}

// lookup 按 id 取运行记录；不存在时报「没有这次转交」。
func (m *Manager) lookup(id port.DelegateID) (*run, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.runs[id]
	if !ok {
		return nil, fmt.Errorf("no delegation %s", id)
	}
	return r, nil
}

// pick 选代理：名字精确匹配；空 = 配置里 default 的那条，没有 default 则唯一启用的那条。
func (m *Manager) pick(name string) (Agent, error) {
	name = strings.TrimSpace(name)
	if name != "" {
		for _, a := range m.opts.Agents {
			if a.Name == name {
				if !a.Enabled {
					return Agent{}, fmt.Errorf("agent %q is disabled", name)
				}
				return a, nil
			}
		}
		if len(m.opts.Agents) == 0 {
			return Agent{}, fmt.Errorf("no external agents are configured (delegate.agents)")
		}
		return Agent{}, fmt.Errorf("unknown agent %q (configured: %s)", name, strings.Join(m.agentNames(), ", "))
	}
	var def *Agent
	enabled := 0
	for i := range m.opts.Agents {
		a := &m.opts.Agents[i]
		if !a.Enabled {
			continue
		}
		enabled++
		if a.Default {
			def = a
		}
	}
	if def != nil {
		return *def, nil
	}
	if enabled == 1 {
		for _, a := range m.opts.Agents {
			if a.Enabled {
				return a, nil
			}
		}
	}
	if enabled == 0 {
		return Agent{}, fmt.Errorf("no enabled external agents (delegate.agents)")
	}
	return Agent{}, fmt.Errorf("agent is required when several are configured (%s)", strings.Join(m.agentNames(), ", "))
}

func (m *Manager) agentNames() []string {
	out := make([]string, 0, len(m.opts.Agents))
	for _, a := range m.opts.Agents {
		out = append(out, a.Name)
	}
	return out
}

// workDir 子进程 cwd：工具参数 → 配置 workdir → 家目录（与 JobSpec.WorkDir 同一口径）。
// 两者都不是围栏，只是起点（§5.18）。
func (m *Manager) workDir(spec port.DelegateSpec, a Agent) string {
	for _, wd := range []string{strings.TrimSpace(spec.WorkDir), strings.TrimSpace(a.WorkDir), m.opts.Home} {
		if wd == "" {
			continue
		}
		if abs, err := filepath.Abs(wd); err == nil {
			return abs
		}
		return wd
	}
	return "."
}

// commandLine 配置原样的命令行（清单展示用；不执行）。
func commandLine(a Agent) string {
	parts := append([]string{a.Command}, a.Args...)
	return strings.Join(parts, " ")
}

// noteLearned 记下 initialize 的 agentInfo（Title/Version），供 /delegate list 展示。
func (m *Manager) noteLearned(name, title, version string) {
	if title == "" && version == "" {
		return
	}
	m.mu.Lock()
	m.learned[name] = learnedInfo{title: title, version: version}
	m.mu.Unlock()
}
