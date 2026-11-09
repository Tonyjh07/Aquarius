package acpgate

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/Tonyjh07/Aquarius/internal/domain/conversation"
	"github.com/Tonyjh07/Aquarius/internal/domain/perm"
	"github.com/Tonyjh07/Aquarius/internal/i18n"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

// fakeAgent 测试期现编的脚本化 ACP 代理（testdata/fakeagent）；echoCmd 是 terminal
// 回调测试里被代跑的那条命令（testdata/echocmd）。
var (
	fakeAgent string
	echoCmd   string
)

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "acpgate-test")
	if err != nil {
		fmt.Fprintf(os.Stderr, "temp dir: %v\n", err)
		os.Exit(1)
	}
	suffix := ""
	if runtime.GOOS == "windows" {
		suffix = ".exe"
	}
	for _, p := range []struct {
		name, pkg string
		dst       *string
	}{
		{"fakeagent", "./testdata/fakeagent", &fakeAgent},
		{"echocmd", "./testdata/echocmd", &echoCmd},
	} {
		bin := filepath.Join(dir, p.name+suffix)
		if out, err := exec.Command("go", "build", "-o", bin, p.pkg).CombinedOutput(); err != nil {
			fmt.Fprintf(os.Stderr, "build %s: %v\n%s", p.name, err, out)
			os.Exit(1)
		}
		*p.dst = bin
	}
	code := m.Run()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}

// fakeConfirmer 脚本化确认器。
type fakeConfirmer struct {
	allow  bool
	reason string
	calls  int
	prompt string
	req    port.ConfirmRequest
}

func (f *fakeConfirmer) Confirm(_ context.Context, req port.ConfirmRequest) (port.ConfirmAnswer, error) {
	prompt := req.Prompt
	f.calls++
	f.prompt = prompt
	f.req = req
	return port.ConfirmAnswer{Allow: f.allow, Reason: f.reason}, nil
}

// newManager 造一个只配了假代理的管理器（模式经 env 传）。
func newManager(t *testing.T, mode string, tune func(*Options)) *Manager {
	t.Helper()
	opts := Options{
		Agents: []Agent{{
			Name: "fake", Command: fakeAgent, Enabled: true,
			Env: map[string]string{"FAKE_AGENT_MODE": mode},
		}},
		Grace:     300 * time.Millisecond,
		Handshake: 10 * time.Second,
		Now:       time.Now,
	}
	if tune != nil {
		tune(&opts)
	}
	m := New(opts)
	t.Cleanup(m.Close)
	return m
}

func ctxWithSession(id conversation.ID) context.Context {
	return port.WithSessionID(context.Background(), id)
}

// TestDelegateHappyPath 一次正常转交：报告、工具调用数、改动文件（去重）、stopReason、
// agentInfo 回填——全部来自协议事实，不猜。
func TestDelegateHappyPath(t *testing.T) {
	m := newManager(t, "ok", func(o *Options) { o.Home = t.TempDir() })
	task, err := m.Start(ctxWithSession("c1"), port.DelegateSpec{Task: "do the thing"})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if task.ID == "" || task.Agent != "fake" {
		t.Fatalf("task = %+v", task)
	}
	run, err := m.Wait(context.Background(), task.ID)
	if err != nil {
		t.Fatalf("wait: %v", err)
	}
	if run.Report != "Hello world" {
		t.Fatalf("report = %q, want %q", run.Report, "Hello world")
	}
	if run.StopReason != "end_turn" {
		t.Fatalf("stop = %q", run.StopReason)
	}
	if run.ToolCalls != 2 {
		t.Fatalf("tool calls = %d, want 2", run.ToolCalls)
	}
	if got := strings.Join(run.Changed, ","); got != "/tmp/a.go,/tmp/b.go" {
		t.Fatalf("changed = %q（去重后按报过序）", got)
	}
	if run.Session != "sess-1" {
		t.Fatalf("session = %q", run.Session)
	}
	if run.Duration <= 0 {
		t.Fatalf("duration = %v", run.Duration)
	}

	// 第一次转交后清单里才有 title/version（启动不做握手）。
	list, err := m.List(context.Background())
	if err != nil || len(list) != 1 {
		t.Fatalf("list = %+v, err = %v", list, err)
	}
	if list[0].Title != "Fake Agent" || list[0].Version != "9.9" || !list[0].Available {
		t.Fatalf("info = %+v", list[0])
	}
}

// TestDelegateAuthFailure 对方没登录：报因要写成「先在那边的终端里登录」，不把
// -32000 原样抛给用户（§5.18「登录」）。
func TestDelegateAuthFailure(t *testing.T) {
	m := newManager(t, "auth", func(o *Options) { o.Home = t.TempDir() })
	task, err := m.Start(ctxWithSession("c1"), port.DelegateSpec{Task: "x"})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	_, err = m.Wait(context.Background(), task.ID)
	if err == nil {
		t.Fatal("want an error")
	}
	for _, want := range []string{"not signed in", "Log in first", "terminal"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("err = %q, want 含 %q", err, want)
		}
	}
}

// TestDelegateProtocolMismatch 对方只认 v2：明确报版本不兼容，不猜也不降级。
func TestDelegateProtocolMismatch(t *testing.T) {
	m := newManager(t, "v2", func(o *Options) { o.Home = t.TempDir() })
	task, err := m.Start(ctxWithSession("c1"), port.DelegateSpec{Task: "x"})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	_, err = m.Wait(context.Background(), task.ID)
	if err == nil || !strings.Contains(err.Error(), "only supports v1") {
		t.Fatalf("err = %v", err)
	}
}

// TestDelegateCrash 进程起来就崩：报因带 stderr 尾巴（它是「为什么起不来」的唯一线索）。
func TestDelegateCrash(t *testing.T) {
	m := newManager(t, "crash", func(o *Options) { o.Home = t.TempDir() })
	task, err := m.Start(ctxWithSession("c1"), port.DelegateSpec{Task: "x"})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	_, err = m.Wait(context.Background(), task.ID)
	if err == nil || !strings.Contains(err.Error(), "boom on purpose") {
		t.Fatalf("err = %v", err)
	}
}

// TestDelegateCancel 停止键：先发 session/cancel，对方收成 cancelled 就结束——不杀进程。
func TestDelegateCancel(t *testing.T) {
	m := newManager(t, "slow", func(o *Options) { o.Home = t.TempDir() })
	task, err := m.Start(ctxWithSession("c1"), port.DelegateSpec{Task: "long"})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	time.Sleep(200 * time.Millisecond) // 让它进入 prompt
	if err := m.Kill(context.Background(), task.ID); err != nil {
		t.Fatalf("kill: %v", err)
	}
	run, err := m.Wait(context.Background(), task.ID)
	if err != nil {
		t.Fatalf("wait: %v", err)
	}
	if run.StopReason != "cancelled" || !run.Killed {
		t.Fatalf("run = %+v", run)
	}
}

// TestDelegateTimeout 总闸到点：走同一条取消路径；对方不理，则宽限后杀进程组，
// 报告按「已取消」回填而不是报错。
func TestDelegateTimeout(t *testing.T) {
	m := newManager(t, "ignore", func(o *Options) {
		o.Home = t.TempDir()
		o.Timeout = 300 * time.Millisecond
	})
	task, err := m.Start(ctxWithSession("c1"), port.DelegateSpec{Task: "hang"})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	run, err := m.Wait(context.Background(), task.ID)
	if err != nil {
		t.Fatalf("wait: %v", err)
	}
	if run.StopReason != "cancelled" || !run.Killed {
		t.Fatalf("run = %+v", run)
	}
	if !hasNote(run.Notes, "delegate.timeout_sec") {
		t.Fatalf("notes = %v", run.Notes)
	}
}

// TestDelegatePermissionAllow 权限回落：允许 → 选 allow_once；确认句要说清边界。
func TestDelegatePermissionAllow(t *testing.T) {
	conf := &fakeConfirmer{allow: true}
	m := newManager(t, "permission", func(o *Options) {
		o.Home = t.TempDir()
		o.Confirmer = conf
	})
	task, err := m.Start(ctxWithSession("c1"), port.DelegateSpec{Task: "edit"})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	run, err := m.Wait(context.Background(), task.ID)
	if err != nil {
		t.Fatalf("wait: %v", err)
	}
	if conf.calls != 1 {
		t.Fatalf("confirm calls = %d", conf.calls)
	}
	// 权限确认句 = 头部（代理名）+ 工具调用标题与文本 + 边界说明，全部从文案表取词。
	wantPrompt := i18n.Tf("acp.prompt.head", "agent", "fake") +
		"\n  Write src/main.go\n  overwrite src/main.go\n\n" + i18n.T("acp.prompt.foot")
	if conf.prompt != wantPrompt {
		t.Fatalf("prompt = %q, want %q", conf.prompt, wantPrompt)
	}
	// 结构化字段：来源是外部代理，工具与任务摘要都带上，界面与策略表不再猜。
	if conf.req.Origin != port.OriginAgent {
		t.Fatalf("origin = %q, want %q", conf.req.Origin, port.OriginAgent)
	}
	if conf.req.Tool == "" || conf.req.Task == "" {
		t.Fatalf("tool/task 应有值: %+v", conf.req)
	}
	if !strings.Contains(run.Report, `"optionId":"allow"`) {
		t.Fatalf("report = %q（应回 allow_once）", run.Report)
	}
}

// TestDelegatePermissionDenied 拒绝 → reject_once；理由只进我们的提示与报告，不回传。
func TestDelegatePermissionDenied(t *testing.T) {
	conf := &fakeConfirmer{allow: false, reason: "不要动这个文件"}
	m := newManager(t, "permission", func(o *Options) {
		o.Home = t.TempDir()
		o.Confirmer = conf
	})
	task, err := m.Start(ctxWithSession("c1"), port.DelegateSpec{Task: "edit"})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	run, err := m.Wait(context.Background(), task.ID)
	if err != nil {
		t.Fatalf("wait: %v", err)
	}
	if !strings.Contains(run.Report, `"optionId":"deny"`) {
		t.Fatalf("report = %q（应回 reject_once）", run.Report)
	}
	if !hasNote(run.Notes, "不要动这个文件") {
		t.Fatalf("notes = %v", run.Notes)
	}
}

// TestDelegatePermissionAway 用户不在发起会话：当场拒绝并计数，不让转交卡在等确认上。
func TestDelegatePermissionAway(t *testing.T) {
	conf := &fakeConfirmer{allow: true}
	m := newManager(t, "permission", func(o *Options) {
		o.Home = t.TempDir()
		o.Confirmer = conf
		o.CurrentSession = func() conversation.ID { return "other" }
	})
	task, err := m.Start(ctxWithSession("c1"), port.DelegateSpec{Task: "edit"})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	run, err := m.Wait(context.Background(), task.ID)
	if err != nil {
		t.Fatalf("wait: %v", err)
	}
	if conf.calls != 0 {
		t.Fatalf("不该问用户：calls = %d", conf.calls)
	}
	if !strings.Contains(run.Report, `"optionId":"deny"`) {
		t.Fatalf("report = %q", run.Report)
	}
	if !hasNote(run.Notes, "permission denied") {
		t.Fatalf("notes = %v", run.Notes)
	}
}

// TestDelegatePermissionUnknownSession 当前会话未知（事实快照还没发布）不等于「不在」：
// 照常问用户，别把未知当拒绝。
func TestDelegatePermissionUnknownSession(t *testing.T) {
	conf := &fakeConfirmer{allow: true}
	m := newManager(t, "permission", func(o *Options) {
		o.Home = t.TempDir()
		o.Confirmer = conf
		o.CurrentSession = func() conversation.ID { return "" }
	})
	task, err := m.Start(ctxWithSession("c1"), port.DelegateSpec{Task: "edit"})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	run, err := m.Wait(context.Background(), task.ID)
	if err != nil {
		t.Fatalf("wait: %v", err)
	}
	if conf.calls != 1 {
		t.Fatalf("确认次数 = %d, want 1（未知会话不该当场拒绝）", conf.calls)
	}
	if !strings.Contains(run.Report, `"optionId":"allow"`) {
		t.Fatalf("report = %q", run.Report)
	}
}

// TestDelegateStartValidation 参数与并发上限在 Start 处就回绝，不占进程。
func TestDelegateStartValidation(t *testing.T) {
	m := newManager(t, "slow", func(o *Options) {
		o.Home = t.TempDir()
		o.MaxTotal = 1
	})
	ctx := ctxWithSession("c1")
	if _, err := m.Start(ctx, port.DelegateSpec{Task: "  "}); err == nil {
		t.Fatal("空任务应报错")
	}
	if _, err := m.Start(ctx, port.DelegateSpec{Task: "x", Session: "sess-1"}); err == nil ||
		!strings.Contains(err.Error(), "reuse") {
		t.Fatalf("复用会话应报不支持: %v", err)
	}
	if _, err := m.Start(ctx, port.DelegateSpec{Task: "x", WorkDir: "relative/dir"}); err == nil {
		t.Fatal("相对 workdir 应报错")
	}
	if _, err := m.Start(ctx, port.DelegateSpec{Task: strings.Repeat("x", port.MaxDelegateTaskChars+1)}); err == nil {
		t.Fatal("超长任务应报错")
	}
	first, err := m.Start(ctx, port.DelegateSpec{Task: "one"})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if _, err := m.Start(ctx, port.DelegateSpec{Task: "two"}); err == nil ||
		!strings.Contains(err.Error(), "too many delegations") {
		t.Fatalf("超全局上限应报错: %v", err)
	}
	_ = m.Kill(context.Background(), first.ID)
}

// TestDelegateUnknownAgent 名字不认识时把已配置的列出来（可行动）。
func TestDelegateUnknownAgent(t *testing.T) {
	m := newManager(t, "ok", nil)
	_, err := m.Start(context.Background(), port.DelegateSpec{Agent: "nope", Task: "x"})
	if err == nil || !strings.Contains(err.Error(), "fake") {
		t.Fatalf("err = %v", err)
	}
}

// TestListReportsMissingCommand 装了但不在 PATH 上：留在清单里并写明原因。
func TestListReportsMissingCommand(t *testing.T) {
	m := New(Options{Agents: []Agent{
		{Name: "gone", Command: "definitely-not-on-path-9f3a", Enabled: true},
		{Name: "off", Command: "whatever", Enabled: false},
	}})
	list, err := m.List(context.Background())
	if err != nil || len(list) != 2 {
		t.Fatalf("list = %+v, err = %v", list, err)
	}
	if list[0].Available || !strings.Contains(list[0].Note, "PATH") {
		t.Fatalf("gone = %+v", list[0])
	}
	if list[1].Available || list[1].Note != i18n.T("acp.note.disabled") {
		t.Fatalf("off = %+v", list[1])
	}
}

func hasNote(notes []string, sub string) bool {
	for _, n := range notes {
		if strings.Contains(n, sub) {
			return true
		}
	}
	return false
}

// TestDelegateErrorKeepsPartialReport 中途失败也要把已经收到的正文带出去：跑了半天的
// 输出不该因为最后一步报错就整段消失（D268）。
func TestDelegateErrorKeepsPartialReport(t *testing.T) {
	m := newManager(t, "partial", func(o *Options) { o.Home = t.TempDir() })
	task, err := m.Start(ctxWithSession("c1"), port.DelegateSpec{Task: "review"})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	run, err := m.Wait(context.Background(), task.ID)
	if err == nil {
		t.Fatal("want an error（代理中途退出）")
	}
	if !strings.Contains(run.Report, "第一段结论") || !strings.Contains(run.Report, "第二段") {
		t.Fatalf("run.Report = %q, want 两次正文都在", run.Report)
	}
	if run.Agent != "fake" {
		t.Fatalf("run.Agent = %q", run.Agent)
	}
}

// TestDelegatePolicyAllowsWithoutAsking 策略表把 acpgate 来源设成自动放行时，
// 权限反问不再弹给用户（D280）：报告里仍按 allow 回选项。
func TestDelegatePolicyAllowsWithoutAsking(t *testing.T) {
	conf := &fakeConfirmer{allow: true}
	m := newManager(t, "permission", func(o *Options) {
		o.Home = t.TempDir()
		o.Confirmer = conf
		o.Overrides = func() perm.Overrides {
			return perm.Overrides{{Origin: perm.OriginAgent, Mode: perm.ModeAllow}}
		}
	})
	task, err := m.Start(ctxWithSession("c1"), port.DelegateSpec{Task: "edit"})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	run, err := m.Wait(context.Background(), task.ID)
	if err != nil {
		t.Fatalf("wait: %v", err)
	}
	if conf.calls != 0 {
		t.Fatalf("confirm calls = %d, want 0（策略表已放行）", conf.calls)
	}
	if !strings.Contains(run.Report, `"optionId":"allow"`) {
		t.Fatalf("report = %q（应回 allow_once）", run.Report)
	}
}
