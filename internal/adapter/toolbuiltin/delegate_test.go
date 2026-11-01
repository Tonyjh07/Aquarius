package toolbuiltin

import (
	"context"
	"strings"
	"testing"

	"github.com/Tonyjh07/Aquarius/internal/domain/tool"
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
