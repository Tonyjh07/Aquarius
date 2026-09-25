package plugin

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Tonyjh07/Aquarius/internal/port"
)

// ---------------------------------------------------------------------------
// 测试替身
// ---------------------------------------------------------------------------

// fakeSession 脚本化会话：Wait 由 Close/崩溃信号唤醒。
type fakeSession struct {
	mu     sync.Mutex
	closes int
	waitCh chan error
}

func newFakeSession() *fakeSession { return &fakeSession{waitCh: make(chan error, 1)} }

func (f *fakeSession) Tools(context.Context) ([]port.Tool, error)    { return nil, nil }
func (f *fakeSession) Memory() port.MemoryStore                      { return nil }
func (f *fakeSession) Prompts(context.Context) ([]PromptInfo, error) { return nil, nil }
func (f *fakeSession) RenderPrompt(context.Context, string, []string) (string, error) {
	return "", nil
}
func (f *fakeSession) Stats() Stats { return Stats{} }

func (f *fakeSession) Wait() error { return <-f.waitCh }

func (f *fakeSession) Close() error {
	f.mu.Lock()
	f.closes++
	f.mu.Unlock()
	select { // 唤醒 Wait（主动关闭路径）
	case f.waitCh <- errors.New("closed"):
	default:
	}
	return nil
}

// crash 模拟异常终止（不经 Close）。
func (f *fakeSession) crash() { f.waitCh <- errors.New("boom") }

// fakeDial 记录拨号次数并维护存活会话列表（崩溃测试逐个击落最新会话）。
type fakeDial struct {
	mu      sync.Mutex
	calls   int
	live    []*fakeSession
	dialErr error
}

func (d *fakeDial) dialer() Dialer {
	return func(context.Context, Decl) (Session, error) {
		d.mu.Lock()
		defer d.mu.Unlock()
		d.calls++
		if d.dialErr != nil {
			return nil, d.dialErr
		}
		s := newFakeSession()
		d.live = append(d.live, s)
		return s, nil
	}
}

func (d *fakeDial) callCount() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.calls
}

func (d *fakeDial) latest() *fakeSession {
	d.mu.Lock()
	defer d.mu.Unlock()
	if len(d.live) == 0 {
		return nil
	}
	return d.live[len(d.live)-1]
}

// fakeConfirm 脚本化确认器。
type fakeConfirm struct {
	mu      sync.Mutex
	answers []bool
	prompts []string
}

func (c *fakeConfirm) Confirm(_ context.Context, prompt string) (bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.prompts = append(c.prompts, prompt)
	if len(c.answers) == 0 {
		return false, errors.New("确认脚本耗尽")
	}
	a := c.answers[0]
	c.answers = c.answers[1:]
	return a, nil
}

// waitFor 轮询等待条件成立。
func waitFor(t *testing.T, cond func() bool, msg string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("超时等待: %s", msg)
}

// stdioDecl 无能力的普通声明。
func stdioDecl() MCPServer {
	return MCPServer{MCPConfig: MCPConfig{Transport: "stdio", Command: "srv"}}
}

// statusOf 按名取状态。
func statusOf(h *Host, name string) Status {
	for _, info := range h.List() {
		if info.Name == name {
			return info.Status
		}
	}
	return ""
}

// ---------------------------------------------------------------------------
// 发现与授权
// ---------------------------------------------------------------------------

// TestHostStartGrantAndConnect 授权通过 → 落盘 granted → 连接就绪 → OnChange 触发。
func TestHostStartGrantAndConnect(t *testing.T) {
	dial := &fakeDial{}
	conf := &fakeConfirm{answers: []bool{true}}
	var persisted map[string]State
	var persists int
	changes := 0

	h := New(Deps{
		Dial:    dial.dialer(),
		Confirm: conf,
		ConfigServers: map[string]MCPServer{
			"web": {MCPConfig: MCPConfig{Transport: "stdio", Command: "web-mcp"}, Capabilities: []string{"network"}},
		},
		Persist: func(s map[string]State) error {
			persists++
			persisted = s
			return nil
		},
		OnChange: func() { changes++ },
	})
	h.Start(context.Background())
	defer h.Close()

	if statusOf(h, "web") != StatusReady {
		t.Fatalf("status = %s, want ready", statusOf(h, "web"))
	}
	if dial.callCount() != 1 {
		t.Fatalf("dial = %d, want 1", dial.callCount())
	}
	if len(conf.prompts) != 1 || !strings.Contains(conf.prompts[0], "network") {
		t.Fatalf("prompts = %v, want 含 network 的能力确认", conf.prompts)
	}
	if persists == 0 || !persisted["web"].HasGranted("network") {
		t.Fatalf("persisted = %+v（%d 次），want granted network", persisted, persists)
	}
	if changes == 0 {
		t.Fatal("应触发 OnChange")
	}
	if len(h.Ready()) != 1 {
		t.Fatalf("Ready = %d, want 1", len(h.Ready()))
	}

	// 再次 Start 不重复拨号（幂等：已就绪短路）——直接 Enable 一次验证。
	if err := h.Enable(context.Background(), "web"); err != nil {
		t.Fatalf("enable: %v", err)
	}
	if dial.callCount() != 1 {
		t.Fatalf("dial = %d, want 仍为 1（就绪短路）", dial.callCount())
	}
}

// TestHostGrantRejected 授权拒绝 → revoked、不拨号；无确认器 fail-closed 同效。
func TestHostGrantRejected(t *testing.T) {
	t.Run("用户拒绝", func(t *testing.T) {
		dial := &fakeDial{}
		h := New(Deps{
			Dial:    dial.dialer(),
			Confirm: &fakeConfirm{answers: []bool{false}},
			ConfigServers: map[string]MCPServer{
				"web": {MCPConfig: MCPConfig{Transport: "stdio", Command: "x"}, Capabilities: []string{"network"}},
			},
		})
		h.Start(context.Background())
		defer h.Close()
		if statusOf(h, "web") != StatusRevoked || dial.callCount() != 0 {
			t.Fatalf("status = %s dial = %d, want revoked/0", statusOf(h, "web"), dial.callCount())
		}
	})
	t.Run("无确认器 fail-closed", func(t *testing.T) {
		dial := &fakeDial{}
		h := New(Deps{
			Dial: dial.dialer(),
			ConfigServers: map[string]MCPServer{
				"web": {MCPConfig: MCPConfig{Transport: "stdio", Command: "x"}, Capabilities: []string{"network"}},
			},
		})
		h.Start(context.Background())
		defer h.Close()
		if statusOf(h, "web") != StatusRevoked || dial.callCount() != 0 {
			t.Fatalf("status = %s dial = %d, want revoked/0", statusOf(h, "web"), dial.callCount())
		}
	})
	t.Run("已授权跳过确认", func(t *testing.T) {
		dial := &fakeDial{}
		conf := &fakeConfirm{} // 脚本为空：一旦弹确认即失败
		on := true
		h := New(Deps{
			Dial:    dial.dialer(),
			Confirm: conf,
			ConfigServers: map[string]MCPServer{
				"web": {MCPConfig: MCPConfig{Transport: "stdio", Command: "x"}, Capabilities: []string{"network"}},
			},
			States: map[string]State{"web": {Enabled: &on, Granted: []string{"network"}}},
		})
		h.Start(context.Background())
		defer h.Close()
		if statusOf(h, "web") != StatusReady || len(conf.prompts) != 0 {
			t.Fatalf("status = %s prompts = %v, want ready 且不弹确认", statusOf(h, "web"), conf.prompts)
		}
	})
}

// TestHostDisabledSkips 配置停用的插件不拨号。
func TestHostDisabledSkips(t *testing.T) {
	dial := &fakeDial{}
	off := false
	h := New(Deps{
		Dial:          dial.dialer(),
		ConfigServers: map[string]MCPServer{"web": stdioDecl()},
		States:        map[string]State{"web": {Enabled: &off}},
	})
	h.Start(context.Background())
	defer h.Close()
	if statusOf(h, "web") != StatusDisabled || dial.callCount() != 0 {
		t.Fatalf("status = %s dial = %d, want disabled/0", statusOf(h, "web"), dial.callCount())
	}
}

// TestHostDiscoverMerges plugin.json 与 config 合并（同名 config 优先）、坏 manifest 记 broken。
func TestHostDiscoverMerges(t *testing.T) {
	dir := t.TempDir()
	writeManifest := func(sub, body string) {
		if err := os.MkdirAll(filepath.Join(dir, sub), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, sub, "plugin.json"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	writeManifest("onlyfile", `{"name":"onlyfile","mcp":{"transport":"stdio","command":"x"}}`)
	writeManifest("dup", `{"name":"dup","mcp":{"transport":"stdio","command":"from-file"}}`)
	writeManifest("bad", `{"name":"bad","mcp":{"transport":"stdio","command":"x"},"risk":"wild"}`)
	if err := os.WriteFile(filepath.Join(dir, "stray.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	off := false
	dial := &fakeDial{}
	h := New(Deps{
		Dial:       dial.dialer(),
		PluginsDir: dir,
		ConfigServers: map[string]MCPServer{
			"dup": {MCPConfig: MCPConfig{Transport: "stdio", Command: "from-config"}},
		},
		States: map[string]State{
			"onlyfile": {Enabled: &off},
			"dup":      {Enabled: &off},
		},
	})
	h.Start(context.Background())
	defer h.Close()

	byName := map[string]Info{}
	for _, info := range h.List() {
		byName[info.Name] = info
	}
	if info, ok := byName["onlyfile"]; !ok || info.Source != SourcePlugins {
		t.Fatalf("onlyfile = %+v, want 来自 plugin.json", info)
	}
	if info, ok := byName["dup"]; !ok || info.Source != SourceConfig {
		t.Fatalf("dup = %+v, want config 覆盖 plugin.json（D31）", info)
	}
	bad, ok := byName["bad"]
	if !ok || bad.Status != StatusFailed || !strings.Contains(bad.LastErr, "risk") {
		t.Fatalf("bad = %+v, want broken 记报因", bad)
	}
	if _, ok := byName["stray.txt"]; ok {
		t.Fatal("非目录条目不应成为插件")
	}
	if dial.callCount() != 0 {
		t.Fatalf("全停用不应拨号: %d", dial.callCount())
	}
}

// ---------------------------------------------------------------------------
// 生命周期
// ---------------------------------------------------------------------------

// TestHostCrashRestarts 崩溃限次重启（§6.4 #4）：3 次退避重启后置 crashed。
func TestHostCrashRestarts(t *testing.T) {
	dial := &fakeDial{}
	h := New(Deps{
		Dial:          dial.dialer(),
		ConfigServers: map[string]MCPServer{"web": stdioDecl()},
	})
	h.backoff = func(int) time.Duration { return time.Millisecond } // 测试注入：零等待
	h.Start(context.Background())
	defer h.Close()

	if dial.callCount() != 1 {
		t.Fatalf("dial = %d, want 1", dial.callCount())
	}
	for i := 1; i <= maxRestarts; i++ {
		dial.latest().crash()
		want := i + 1
		waitFor(t, func() bool { return dial.callCount() == want },
			"第 "+itoa(i)+" 次重启拨号")
		waitFor(t, func() bool { return statusOf(h, "web") == StatusReady },
			"重启后回到 ready")
	}
	// 第 maxRestarts+1 次崩溃：超限，不再拨号。
	dial.latest().crash()
	waitFor(t, func() bool { return statusOf(h, "web") == StatusCrashed }, "置 crashed")
	time.Sleep(20 * time.Millisecond)
	if dial.callCount() != maxRestarts+1 {
		t.Fatalf("dial = %d, want %d（限次后不再重启）", dial.callCount(), maxRestarts+1)
	}
	for _, info := range h.List() {
		if info.Name == "web" && (info.Restarts != maxRestarts || !strings.Contains(info.LastErr, "超限")) {
			t.Fatalf("info = %+v, want 重启计满 + 超限报因", info)
		}
	}
}

// TestHostDisableNoRestart 主动停用不计崩溃：Close 唤醒 Wait 后不重启；状态写回。
func TestHostDisableNoRestart(t *testing.T) {
	dial := &fakeDial{}
	var persisted map[string]State
	off := false
	h := New(Deps{
		Dial:          dial.dialer(),
		ConfigServers: map[string]MCPServer{"web": stdioDecl()},
		States:        map[string]State{"web": {Enabled: &off}}, // 先停用
		Persist: func(s map[string]State) error {
			persisted = s
			return nil
		},
	})
	h.backoff = func(int) time.Duration { return time.Millisecond }
	h.Start(context.Background())
	defer h.Close()

	if err := h.Enable(context.Background(), "web"); err != nil {
		t.Fatalf("enable: %v", err)
	}
	waitFor(t, func() bool { return statusOf(h, "web") == StatusReady }, "enable 后就绪")
	if persisted == nil || !persisted["web"].IsEnabled() {
		t.Fatalf("persisted = %+v, want enabled=true", persisted)
	}

	if err := h.Disable("web"); err != nil {
		t.Fatalf("disable: %v", err)
	}
	time.Sleep(30 * time.Millisecond) // 若误判崩溃会在此窗口重启
	if dial.callCount() != 1 {
		t.Fatalf("dial = %d, want 1（主动停用不重启）", dial.callCount())
	}
	if statusOf(h, "web") != StatusDisabled {
		t.Fatalf("status = %s, want disabled", statusOf(h, "web"))
	}
	if persisted == nil || persisted["web"].IsEnabled() {
		t.Fatalf("persisted = %+v, want enabled=false 写回", persisted)
	}
	if len(h.Ready()) != 0 {
		t.Fatal("停用后不应有就绪会话")
	}

	// 未声明的插件启停报错。
	if err := h.Enable(context.Background(), "ghost"); err == nil {
		t.Fatal("enable ghost 应报错")
	}
	if err := h.Disable("ghost"); err == nil {
		t.Fatal("disable ghost 应报错")
	}
}

// TestHostDialFailureIsSoft 首连失败不致命：failed + 报因，其余插件照常。
func TestHostDialFailureIsSoft(t *testing.T) {
	dial := &fakeDial{dialErr: errors.New("spawn failed")}
	h := New(Deps{
		Dial: dial.dialer(),
		ConfigServers: map[string]MCPServer{
			"web": stdioDecl(),
		},
	})
	h.Start(context.Background())
	defer h.Close()
	if statusOf(h, "web") != StatusFailed {
		t.Fatalf("status = %s, want failed", statusOf(h, "web"))
	}
	for _, info := range h.List() {
		if info.Name == "web" && !strings.Contains(info.LastErr, "spawn failed") {
			t.Fatalf("LastErr = %q", info.LastErr)
		}
	}
}

// itoa 小整数转串（测试用，避免额外依赖）。
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}
