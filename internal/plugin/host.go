package plugin

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/Tonyjh07/Aquarius/internal/port"
)

// 重启退避与上限（§6.4 #4"崩溃自动重启（限次 + 退避）"；DESIGN 未给配置键，取常量）。
const (
	maxRestarts     = 3
	baseRestartWait = time.Second
)

// Status 单个插件的运行状态（/plugin list 展示）。
type Status string

const (
	StatusDisabled   Status = "disabled"   // 配置停用
	StatusReady      Status = "ready"      // 已连接
	StatusConnecting Status = "connecting" // 连接中
	StatusFailed     Status = "failed"     // 首连失败（报因保留，可 /plugin enable 重试）
	StatusRestarting Status = "restarting" // 崩溃退避中
	StatusRevoked    Status = "revoked"    // 授权被拒/无确认器（fail-closed）
	StatusCrashed    Status = "crashed"    // 崩溃重启超限（§6.4 限次）
)

// Info /plugin list 的一行快照（§6.4 #5 可观测）。
type Info struct {
	Name         string
	Source       string // SourceConfig | SourcePlugins
	Transport    string
	Capabilities []string
	Granted      []string
	Risk         string
	Enabled      bool
	Status       Status
	Restarts     int
	LastErr      string
	Stats        Stats // 就绪时为会话实时统计，否则零值
}

// Deps 宿主依赖（装配根注入；Secrets 属适配器细节、由 Dialer 闭包持有）。
type Deps struct {
	// Dial 连接器（mcpgate.NewDialer 产出）。
	Dial Dialer
	// Confirm capability 首用确认（§6.4 #3）；nil 且声明了能力 = fail-closed 不启动。
	Confirm port.Confirmer
	// ConfigServers config `mcpServers` 声明（发现源之一，D31）。
	ConfigServers map[string]MCPServer
	// PluginsDir `~/.aquarius/plugins`（发现源之二；目录不存在 = 跳过）。
	PluginsDir string
	// States config `plugins` 初始状态（D31；nil = 全部默认）。
	States map[string]State
	// Persist 状态变更落盘（装配根原子写回 config 的 plugins 段）；nil = 不落盘。
	Persist func(map[string]State) error
	// Logf 可观测日志（装配根接到 stderr）；nil = 丢弃。
	Logf func(format string, args ...any)
	// OnChange 启停/状态变化回调（装配根刷新工具与动态命令面）；锁外触发。
	OnChange func()
}

// Host 插件宿主（DESIGN §6.4）：发现 → 校验 → 授权 → 生命周期 → 可观测。
// 只编排 plugin.Session（消费方接口），不 import 任何适配器（同 D13 边界）。
type Host struct {
	dial     Dialer
	confirm  port.Confirmer
	dir      string
	persist  func(map[string]State) error
	logf     func(string, ...any)
	onChange func()
	closed   chan struct{}

	mu      sync.Mutex
	decls   map[string]Decl     // 发现合并结果（config 覆盖同名 plugin.json，D31）
	broken  map[string]string   // 解析失败的 plugin.json（目录名 → 报因）
	states  map[string]State    // 运行时状态（与 config 同步）
	managed map[string]*managed // name → 运行时记录
	ctx     context.Context     // Start 的 ctx（重启连接用）
	closing bool
	backoff func(restart int) time.Duration // 退避（测试注入）
}

// managed 单个 server 的运行时记录。
type managed struct {
	session  Session
	status   Status
	restarts int
	lastErr  string
	stopping bool // 主动关闭（区分崩溃）
}

// New 构造宿主（发现前状态为空；调 Start 后可用）。
func New(d Deps) *Host {
	h := &Host{
		dial:     d.Dial,
		confirm:  d.Confirm,
		dir:      d.PluginsDir,
		persist:  d.Persist,
		logf:     d.Logf,
		onChange: d.OnChange,
		closed:   make(chan struct{}),
		decls:    map[string]Decl{},
		broken:   map[string]string{},
		states:   map[string]State{},
		managed:  map[string]*managed{},
		backoff: func(restart int) time.Duration {
			return baseRestartWait << (restart - 1) // 1s, 2s, 4s
		},
	}
	for name, st := range d.States {
		h.states[name] = st
	}
	if h.logf == nil {
		h.logf = func(string, ...any) {}
	}
	// config 声明（启动前已过 Validate）。
	for name, srv := range d.ConfigServers {
		h.decls[name] = Decl{
			Name: name, MCPConfig: srv.MCPConfig,
			Capabilities: srv.Capabilities, Risk: srv.Risk, Source: SourceConfig,
		}
	}
	return h
}

// Start 扫描 plugin.json 合并声明，随后按状态启动全部已启用插件（§6.4 #1/#3/#4）。
// 单个插件失败不致命：记状态继续（与 §10 同哲学——会话比插件重要）。
func (h *Host) Start(ctx context.Context) {
	h.mu.Lock()
	h.ctx = ctx
	h.mu.Unlock()
	h.discover()

	for _, name := range h.sortedNames() {
		h.mu.Lock()
		d := h.decls[name]
		st := h.states[name]
		h.mu.Unlock()
		if !st.IsEnabled() {
			h.setStatus(name, StatusDisabled, "")
			continue
		}
		if h.grant(ctx, d) {
			h.connect(ctx, name)
		} else {
			h.setStatus(name, StatusRevoked, "授权被拒绝或缺少确认器（fail-closed）")
		}
	}
	h.changed()
}

// discover 扫描 <PluginsDir>/*/plugin.json 合并声明（config 同名条目优先，D31）；
// 解析失败的条目记 broken（/plugin list 可见报因），不致命。
func (h *Host) discover() {
	entries, err := os.ReadDir(h.dir)
	if err != nil {
		return // 目录不存在 = 纯 config 模式
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		data, err := os.ReadFile(filepath.Join(h.dir, e.Name(), "plugin.json"))
		if err != nil {
			if !os.IsNotExist(err) {
				h.logf("[plugin] 读取 %s/plugin.json: %v", e.Name(), err)
			}
			continue
		}
		m, err := ParseManifest(data)
		if err != nil {
			h.logf("[plugin] 拒载 %s: %v", e.Name(), err)
			h.mu.Lock()
			h.broken[e.Name()] = err.Error()
			h.mu.Unlock()
			continue
		}
		h.mu.Lock()
		_, exists := h.decls[m.Name]
		if !exists { // config 同名条目优先（D31：声明与状态分离，config 是本机权威）
			h.decls[m.Name] = Decl{
				Name: m.Name, MCPConfig: m.MCP,
				Capabilities: m.Capabilities, Risk: m.Risk, Source: SourcePlugins,
			}
		}
		h.mu.Unlock()
		if exists {
			h.logf("[plugin] %s: config 条目覆盖 plugin.json 声明（D31）", m.Name)
		}
	}
}

// grant capability 首用逐项确认（§6.4 #3）：通过则写入 State.Granted 并落盘。
// 返回 false = 拒绝（或无确认器 fail-closed），调用方置 revoked 不启动。
func (h *Host) grant(ctx context.Context, d Decl) bool {
	if len(d.Capabilities) == 0 {
		return true
	}
	h.mu.Lock()
	st := h.states[d.Name]
	var pending []string
	for _, c := range d.Capabilities {
		if !st.HasGranted(c) {
			pending = append(pending, c)
		}
	}
	h.mu.Unlock()
	if len(pending) == 0 {
		return true
	}
	if h.confirm == nil {
		h.logf("[plugin] %s: 请求能力 %v，但无 Confirmer（fail-closed）", d.Name, pending)
		return false
	}
	changed := false
	for _, c := range pending {
		yes, err := h.confirm.Confirm(ctx, fmt.Sprintf("插件 %q 请求能力 %q，允许？", d.Name, c))
		if err != nil {
			h.logf("[plugin] %s: 确认 %s 失败: %v", d.Name, c, err)
			return false
		}
		if !yes {
			h.logf("[plugin] %s: 用户拒绝能力 %s", d.Name, c)
			return false
		}
		h.mu.Lock()
		cur := h.states[d.Name]
		if !cur.HasGranted(c) {
			cur.Granted = append(cur.Granted, c)
			h.states[d.Name] = cur
			changed = true
		}
		h.mu.Unlock()
	}
	if changed {
		h.saveStates()
	}
	return true
}

// connect 拉起一个 server 并挂崩溃观察（§6.4 #4）；已就绪则短路（幂等）。
func (h *Host) connect(ctx context.Context, name string) {
	h.mu.Lock()
	d, ok := h.decls[name]
	if !ok {
		h.mu.Unlock()
		return
	}
	if m := h.managed[name]; m != nil && m.session != nil && m.status == StatusReady {
		h.mu.Unlock()
		return // 已连接
	}
	h.mu.Unlock()

	h.setStatus(name, StatusConnecting, "")
	s, err := h.dial(ctx, d)
	if err != nil {
		h.logf("[plugin] %s 启动失败: %v", name, err)
		h.setStatus(name, StatusFailed, err.Error())
		return
	}
	h.mu.Lock()
	m := h.managed[name]
	if m == nil {
		m = &managed{}
		h.managed[name] = m
	}
	if m.stopping || h.closing { // 等待期间被停用
		h.mu.Unlock()
		_ = s.Close()
		return
	}
	m.session = s
	m.status = StatusReady
	m.lastErr = ""
	base := h.ctx
	h.mu.Unlock()

	go h.watch(name, s, base)
	h.changed()
}

// watch 阻塞至会话终止；非主动关闭则按限次 + 退避重启（§6.4 #4）。
func (h *Host) watch(name string, s Session, base context.Context) {
	werr := s.Wait()
	h.mu.Lock()
	m := h.managed[name]
	if h.closing || m == nil || m.stopping || m.session != s {
		// 主动关闭或已被替换：不计崩溃。
		if m != nil && m.session == s {
			m.session = nil
		}
		h.mu.Unlock()
		_ = s.Close()
		return
	}
	m.session = nil
	if m.restarts >= maxRestarts {
		msg := fmt.Sprintf("崩溃重启超限（%d 次）: %v", maxRestarts, werr)
		m.status = StatusCrashed
		m.lastErr = msg
		h.mu.Unlock()
		h.logf("[plugin] %s: %s", name, msg)
		h.changed()
		return
	}
	m.restarts++
	restart := m.restarts
	m.status = StatusRestarting
	m.lastErr = fmt.Sprintf("%v", werr)
	wait := h.backoff(restart)
	h.mu.Unlock()
	h.logf("[plugin] %s 连接终止（%v），%s 后重启（第 %d/%d 次）",
		name, werr, wait, restart, maxRestarts)

	select {
	case <-time.After(wait):
	case <-h.closed:
		return
	}
	if base == nil {
		base = context.Background()
	}
	if base.Err() != nil || h.isClosing() {
		return
	}
	h.connect(base, name)
}

// isClosing 宿主是否在关闭（锁内读）。
func (h *Host) isClosing() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.closing
}

// Enable 启用并立即连接（写回 state，§6.4 #1 config 启停）。
func (h *Host) Enable(ctx context.Context, name string) error {
	h.mu.Lock()
	d, ok := h.decls[name]
	if !ok {
		h.mu.Unlock()
		return fmt.Errorf("未声明的插件 %q（既不在 config mcpServers 也无 plugin.json）", name)
	}
	prev := h.states[name]
	on := true
	h.states[name] = State{Enabled: &on, Granted: prev.Granted}
	h.mu.Unlock()
	h.saveStates()
	if h.grant(ctx, d) {
		h.connect(ctx, name)
	} else {
		h.setStatus(name, StatusRevoked, "授权被拒绝或缺少确认器（fail-closed）")
	}
	h.changed()
	return nil
}

// Disable 停用并关闭连接（写回 state）。
func (h *Host) Disable(name string) error {
	h.mu.Lock()
	_, ok := h.decls[name]
	if !ok {
		h.mu.Unlock()
		return fmt.Errorf("未声明的插件 %q（既不在 config mcpServers 也无 plugin.json）", name)
	}
	prev := h.states[name]
	off := false
	h.states[name] = State{Enabled: &off, Granted: prev.Granted}
	h.mu.Unlock()
	h.saveStates()
	h.stop(name)
	h.setStatus(name, StatusDisabled, "")
	h.changed()
	return nil
}

// stop 主动关闭一个 server 的连接（stopping 标记抑制崩溃重启）。
func (h *Host) stop(name string) {
	h.mu.Lock()
	m := h.managed[name]
	if m == nil || m.session == nil {
		h.mu.Unlock()
		return
	}
	m.stopping = true
	s := m.session
	m.session = nil
	h.mu.Unlock()
	_ = s.Close()
}

// Close 关闭全部连接（§6.4 #4 优雅关机）。
func (h *Host) Close() {
	h.mu.Lock()
	if !h.closing {
		h.closing = true
		close(h.closed)
	}
	sessions := make([]Session, 0, len(h.managed))
	for _, m := range h.managed {
		m.stopping = true
		if m.session != nil {
			sessions = append(sessions, m.session)
			m.session = nil
		}
	}
	h.mu.Unlock()
	for _, s := range sessions {
		_ = s.Close()
	}
}

// Ready 就绪会话快照（name → Session），装配根据此刷新工具与动态命令面。
func (h *Host) Ready() map[string]Session {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make(map[string]Session, len(h.managed))
	for name, m := range h.managed {
		if m.status == StatusReady && m.session != nil {
			out[name] = m.session
		}
	}
	return out
}

// List 全部插件快照（按名排序，/plugin list 用）；含解析失败的 broken 条目。
func (h *Host) List() []Info {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]Info, 0, len(h.decls)+len(h.broken))
	for name, d := range h.decls {
		st := h.states[name]
		info := Info{
			Name: name, Source: d.Source, Transport: d.Transport,
			Capabilities: d.Capabilities, Granted: st.Granted, Risk: d.Risk,
			Enabled: st.IsEnabled(), Status: StatusDisabled,
		}
		if m := h.managed[name]; m != nil {
			info.Status = m.status
			info.Restarts = m.restarts
			info.LastErr = m.lastErr
			if m.session != nil {
				info.Stats = m.session.Stats()
			}
		}
		out = append(out, info)
	}
	for name, errMsg := range h.broken {
		out = append(out, Info{Name: name, Source: SourcePlugins, Status: StatusFailed, LastErr: errMsg})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// setStatus 更新状态（锁内）；不触发 OnChange（调用方按需 changed）。
func (h *Host) setStatus(name string, st Status, lastErr string) {
	h.mu.Lock()
	m := h.managed[name]
	if m == nil {
		m = &managed{}
		h.managed[name] = m
	}
	m.status = st
	m.lastErr = lastErr
	h.mu.Unlock()
}

// saveStates 落盘当前状态（拷贝隔离；Persist 失败只记日志——不阻断运行态）。
func (h *Host) saveStates() {
	if h.persist == nil {
		return
	}
	h.mu.Lock()
	cp := make(map[string]State, len(h.states))
	for k, v := range h.states {
		cp[k] = v
	}
	h.mu.Unlock()
	if err := h.persist(cp); err != nil {
		h.logf("[plugin] 状态落盘失败: %v", err)
	}
}

// changed 触发 OnChange 回调（锁外）。
func (h *Host) changed() {
	if h.onChange != nil {
		h.onChange()
	}
}

// sortedNames 声明名有序遍历（Start 确定性）。
func (h *Host) sortedNames() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	names := make([]string, 0, len(h.decls))
	for n := range h.decls {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}
