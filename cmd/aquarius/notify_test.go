package main

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Tonyjh07/Aquarius/internal/domain/conversation"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

// fakePresenter 记录事件的 Presenter 替身；可注入 Emit 错误。
type fakePresenter struct {
	events []port.Event
	err    error
}

func (f *fakePresenter) Emit(_ context.Context, ev port.Event) error {
	f.events = append(f.events, ev)
	return f.err
}

// fakeOutput 记录 Deliver 的输出器替身；可注入错误。
type fakeOutput struct {
	name string
	err  error
	got  []port.OutputRequest
}

func (f *fakeOutput) Name() string { return f.name }
func (f *fakeOutput) Deliver(_ context.Context, req port.OutputRequest) error {
	f.got = append(f.got, req)
	return f.err
}

func assistantCommit(text string, outcome conversation.Outcome) port.CommittedEvent {
	return port.CommittedEvent{Message: conversation.Message{
		Role:    conversation.RoleAssistant,
		Outcome: outcome,
		Content: []conversation.Part{{Kind: conversation.PartText, Text: text}},
	}}
}

// TestOutputsPresenterDeliversOnCommitted 只对已完成的 assistant 提交扇出；
// 其余事件（增量/工具/错误终态）不 Deliver；内层 Emit 原样透传。
func TestOutputsPresenterDeliversOnCommitted(t *testing.T) {
	inner := &fakePresenter{}
	out := &fakeOutput{name: "notify"}
	p := &outputsPresenter{inner: inner, outs: []port.OutputAdapter{out}}
	ctx := context.Background()

	_ = p.Emit(ctx, port.DeltaEvent{})
	_ = p.Emit(ctx, port.ToolResultEvent{})
	_ = p.Emit(ctx, assistantCommit("被取消的", conversation.OutcomeCancelled))
	if len(out.got) != 0 {
		t.Fatalf("非目标事件不应 Deliver: %d", len(out.got))
	}
	if err := p.Emit(ctx, assistantCommit("回答", conversation.OutcomeDone)); err != nil {
		t.Fatalf("emit: %v", err)
	}
	if len(out.got) != 1 {
		t.Fatalf("Deliver = %d, want 1", len(out.got))
	}
	if len(inner.events) != 4 {
		t.Fatalf("内层收到 %d 个事件, want 4", len(inner.events))
	}
	text := string(out.got[0].Message.Content[0].Text)
	if text != "回答" {
		t.Fatalf("交付文本 = %q", text)
	}
}

// TestOutputsPresenterSwallowsDeliverError Deliver 失败只记日志不打断（§5.7）；
// 内层 Emit 失败则原样上抛。
func TestOutputsPresenterSwallowsDeliverError(t *testing.T) {
	inner := &fakePresenter{}
	var logged []error
	p := &outputsPresenter{
		inner: inner,
		outs:  []port.OutputAdapter{&fakeOutput{name: "notify", err: errors.New("无桌面")}},
		log:   func(err error) { logged = append(logged, err) },
	}
	ctx := context.Background()
	if err := p.Emit(ctx, assistantCommit("hi", conversation.OutcomeDone)); err != nil {
		t.Fatalf("Deliver 错误不应上抛: %v", err)
	}
	if len(logged) != 1 || !strings.Contains(logged[0].Error(), "notify") ||
		!strings.Contains(logged[0].Error(), "无桌面") {
		t.Fatalf("logged = %v", logged)
	}
	inner.err = errors.New("stdout 坏了")
	if err := p.Emit(ctx, assistantCommit("hi", conversation.OutcomeDone)); err == nil {
		t.Fatal("内层错误应上抛")
	}
}

// TestOutputsPresenterNoOutputs 无输出器时装饰器等价透传。
func TestOutputsPresenterNoOutputs(t *testing.T) {
	inner := &fakePresenter{}
	p := &outputsPresenter{inner: inner, log: func(error) { t.Fatal("不应记日志") }}
	if err := p.Emit(context.Background(), assistantCommit("hi", conversation.OutcomeDone)); err != nil {
		t.Fatalf("emit: %v", err)
	}
	if len(inner.events) != 1 {
		t.Fatalf("inner events = %d", len(inner.events))
	}
}

// TestNotifyCmdInjectionSafe 文案只经 argv/环境变量传递，绝不进脚本文本。
func TestNotifyCmdInjectionSafe(t *testing.T) {
	evil := `"; Start-Process calc; "` + "`n`n"
	// Windows：脚本读 $env，标题正文进环境变量。
	path, args, env := notifyCmd("windows", "Aquarius", evil)
	if path != "powershell" || len(args) == 0 {
		t.Fatalf("cmd = %s %v", path, args)
	}
	script := args[len(args)-1]
	if !strings.Contains(script, "$env:AQUARIUS_NOTIFY_BODY") {
		t.Fatalf("脚本应读环境变量: %q", script)
	}
	if strings.Contains(script, evil) {
		t.Fatal("正文不应出现在脚本文本里")
	}
	if len(env) != 2 || !strings.HasSuffix(env[1], evil) {
		t.Fatalf("env = %v", env)
	}
	// Linux：argv 直传（无 shell）。
	path, args, env = notifyCmd("linux", "Aquarius", evil)
	if path != "notify-send" || len(args) != 2 || args[1] != evil || env != nil {
		t.Fatalf("linux cmd = %s %v %v", path, args, env)
	}
	// macOS：osascript 单行文案经转义内嵌（引号/反斜杠转义）。
	_, args, _ = notifyCmd("darwin", "A\"B", `x\y`)
	if len(args) != 2 || !strings.Contains(args[1], `A\"B`) || !strings.Contains(args[1], `x\\y`) {
		t.Fatalf("darwin script = %v", args)
	}
}
