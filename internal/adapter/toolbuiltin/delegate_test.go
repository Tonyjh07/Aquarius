package toolbuiltin

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/Tonyjh07/Aquarius/internal/domain/tool"
	"github.com/Tonyjh07/Aquarius/internal/i18n"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

// fakeDelegates port.Delegate 替身（工具侧只关心 Start/Wait/Kill 的接线与回执）。
type fakeDelegates struct {
	started []port.DelegateSpec
	startID string
	run     port.DelegateRun
	waitErr error
	killed  []port.DelegateID
	waitDA  []string
}

func (f *fakeDelegates) List(context.Context) ([]port.DelegateInfo, error) {
	return []port.DelegateInfo{{Name: "claude", Available: true}}, nil
}

func (f *fakeDelegates) Start(_ context.Context, spec port.DelegateSpec) (port.DelegateTask, error) {
	f.started = append(f.started, spec)
	id := f.startID
	if id == "" {
		id = "d001"
	}
	return port.DelegateTask{ID: port.DelegateID(id), Agent: "claude"}, nil
}

func (f *fakeDelegates) Wait(ctx context.Context, id port.DelegateID) (port.DelegateRun, error) {
	f.waitDA = append(f.waitDA, string(id))
	if err := ctx.Err(); err != nil {
		return port.DelegateRun{}, err
	}
	return f.run, f.waitErr
}

func (f *fakeDelegates) Kill(_ context.Context, id port.DelegateID) error {
	f.killed = append(f.killed, id)
	return nil
}

func delegateDeps(f *fakeDelegates) Deps {
	return Deps{
		Delegates:      f,
		DelegateAgents: []port.DelegateInfo{{Name: "claude", Available: true}, {Name: "gemini", Available: true}},
	}
}

// TestDelegateToolRegisteredOnlyWithAgents 零可用代理时三个工具都不注册（§5.18）：
// 工具面里不该出现一个必然失败的入口。
func TestDelegateToolRegisteredOnlyWithAgents(t *testing.T) {
	if got := delegateTools(Deps{}); len(got) != 0 {
		t.Fatalf("未配置时注册了 %d 个工具", len(got))
	}
	if got := delegateTools(Deps{Delegates: &fakeDelegates{}}); len(got) != 0 {
		t.Fatalf("零可用代理时注册了 %d 个工具", len(got))
	}
	tools := delegateTools(delegateDeps(&fakeDelegates{}))
	if len(tools) != 3 {
		t.Fatalf("tools = %d, want 3", len(tools))
	}
}

// TestDelegateToolStart 派活即返句柄：回执里带 id 与「报告晚些到」。
func TestDelegateToolStart(t *testing.T) {
	f := &fakeDelegates{}
	tl := pick(t, New(delegateDeps(f)), "delegate")
	if tl.Spec().Risk != tool.Confirm {
		t.Fatalf("risk = %v, want Confirm（批准转交 = 批准那个程序动手）", tl.Spec().Risk)
	}
	if !strings.Contains(tl.Spec().Description, "claude") || !strings.Contains(tl.Spec().Description, "gemini") {
		t.Fatalf("描述里应列出可用代理: %q", tl.Spec().Description)
	}
	res, err := tl.Execute(context.Background(), call(`{"agent":"claude","task":"refactor the parser","workdir":"/tmp"}`))
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if len(f.started) != 1 || f.started[0].Agent != "claude" || f.started[0].Task != "refactor the parser" || f.started[0].WorkDir != "/tmp" {
		t.Fatalf("started = %+v", f.started)
	}
	if !strings.Contains(res.Output, "d001") || !strings.Contains(res.Output, "delegate_wait") {
		t.Fatalf("result = %q", res.Output)
	}

	// 相对 workdir 在工具侧就拒收（绝对路径口径与 job_start 一致）。
	if _, err := tl.Execute(context.Background(), call(`{"task":"x","workdir":"rel/dir"}`)); err == nil {
		t.Fatal("相对 workdir 应报错")
	}
	if _, err := tl.Execute(context.Background(), call(`{"task":"  "}`)); err == nil {
		t.Fatal("空任务应报错")
	}
}

// TestDelegateToolRoots 根集随这次转交走：归一去重后进 spec，相对路径在工具侧就拒收。
func TestDelegateToolRoots(t *testing.T) {
	f := &fakeDelegates{}
	tl := pick(t, New(delegateDeps(f)), "delegate")
	if !strings.Contains(string(tl.Spec().Schema), `"roots"`) {
		t.Fatalf("schema 里应有 roots: %s", tl.Spec().Schema)
	}
	_, err := tl.Execute(context.Background(), call(
		`{"task":"x","workdir":"/tmp","roots":[" /srv/a ","/srv/a",""]}`))
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	got := f.started[0].Roots
	if len(got) != 1 || got[0] != "/srv/a" {
		t.Fatalf("roots = %v（want 去空白去重后一条）", got)
	}

	// 相对路径：不收窄也不放宽，直接拒（它会让越界判定失去参照物）。
	if _, err := tl.Execute(context.Background(), call(`{"task":"x","roots":["rel"]}`)); err == nil {
		t.Fatal("相对根应报错")
	}
	if len(f.started) != 1 {
		t.Fatalf("被拒的调用不该起转交: %d", len(f.started))
	}

	// 不填 = 空（最窄），不是「不限」。
	if _, err := tl.Execute(context.Background(), call(`{"task":"y"}`)); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if len(f.started[1].Roots) != 0 {
		t.Fatalf("不填 roots 应为空，得到 %v", f.started[1].Roots)
	}
}

// TestDelegateDescribe 确认摘要：范围全列不被截断，任务只留开头。
func TestDelegateDescribe(t *testing.T) {
	long := strings.Repeat("把一段很长的任务描述反复粘贴过来。", 30)
	got := delegateDescribe(json.RawMessage(
		`{"agent":"opencode","task":"` + long + `","workdir":"/tmp/w","roots":["/srv/a","/srv/b"]}`))
	for _, want := range []string{"opencode", "/tmp/w", "/srv/a", "/srv/b"} {
		if !strings.Contains(got, want) {
			t.Fatalf("摘要缺 %q: %q", want, got)
		}
	}
	if !strings.HasSuffix(strings.TrimRight(got, " "), "...") {
		t.Fatalf("任务应被截断且留省略号: %q", got)
	}
	if n := len([]rune(got)); n > 160 {
		t.Fatalf("摘要过长（%d 字），范围该是它的主体: %q", n, got)
	}

	// 不填 agent / workdir：给缺省口径，而不是留空白让人猜。
	got = delegateDescribe(json.RawMessage(`{"task":"做点事"}`))
	if !strings.Contains(got, i18n.T("tool.delegate.describe.auto")) ||
		!strings.Contains(got, i18n.T("tool.delegate.describe.scope_default")) {
		t.Fatalf("缺省摘要 = %q", got)
	}

	// 参数读不懂：交回默认提示（返回空串），不编半截话。
	if s := delegateDescribe(json.RawMessage(`not json`)); s != "" {
		t.Fatalf("坏参数应返回空串，得到 %q", s)
	}
}

// TestDelegateWaitText 本轮就要结果：返回报告正文（摘要行 + 原文），并声明 Interactive
// （不吃默认工具超时）。
func TestDelegateWaitText(t *testing.T) {
	f := &fakeDelegates{run: port.DelegateRun{
		Agent: "claude", StopReason: "end_turn", ToolCalls: 3,
		Changed: []string{"/tmp/a.go"}, Report: "all done",
	}}
	tl := pick(t, New(delegateDeps(f)), "delegate_wait")
	inter, ok := tl.(port.Interactive)
	if !ok || !inter.Interactive() {
		t.Fatal("delegate_wait 应声明 Interactive")
	}
	res, err := tl.Execute(context.Background(), call(`{"id":"d001"}`))
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	for _, want := range []string{"Delegation d001 to claude", "tool calls=3", "all done"} {
		if !strings.Contains(res.Output, want) {
			t.Fatalf("result = %q, want 含 %q", res.Output, want)
		}
	}
}

// TestDelegateKillTool 叫停只影响我们自己起的那次。
func TestDelegateKillTool(t *testing.T) {
	f := &fakeDelegates{}
	tl := pick(t, New(delegateDeps(f)), "delegate_kill")
	res, err := tl.Execute(context.Background(), call(`{"id":"d002"}`))
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if len(f.killed) != 1 || f.killed[0] != "d002" || !strings.Contains(res.Output, "stopped") {
		t.Fatalf("killed = %v, result = %q", f.killed, res.Output)
	}
}
