package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
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

// TestNotifyWithBranches 发送分支：windows 分离启动（env 带文案、正文不进脚本）、
// 其余平台带超时上下文同步执行；空正文不打扰；执行错误原样上抛。
func TestNotifyWithBranches(t *testing.T) {
	t.Run("windows 分离启动", func(t *testing.T) {
		var got *exec.Cmd
		detachedCalled, waitingCalled := false, false
		send := notifyWith("windows",
			func(c *exec.Cmd) error { detachedCalled, got = true, c; return nil },
			func(*exec.Cmd) error { waitingCalled = true; return nil })
		if err := send("Aquarius", "你好"); err != nil {
			t.Fatalf("send: %v", err)
		}
		if !detachedCalled || waitingCalled {
			t.Fatalf("detached=%v waiting=%v, want 分离启动", detachedCalled, waitingCalled)
		}
		if len(got.Args) == 0 || !strings.HasSuffix(strings.ToLower(got.Args[0]), "powershell") {
			t.Fatalf("args = %v, want powershell", got.Args)
		}
		if strings.Contains(strings.Join(got.Args, " "), "你好") {
			t.Fatal("正文不应出现在命令行参数里")
		}
		env := got.Env
		if len(env) < 2 || !strings.HasSuffix(env[len(env)-2], "Aquarius") ||
			!strings.HasSuffix(env[len(env)-1], "你好") {
			t.Fatalf("文案应经环境变量（末尾两项）: %v", env[max(0, len(env)-2):])
		}
	})
	t.Run("其余平台带超时同步执行", func(t *testing.T) {
		var got *exec.Cmd
		send := notifyWith("linux",
			func(*exec.Cmd) error { t.Fatal("不应分离启动"); return nil },
			func(c *exec.Cmd) error { got = c; return nil })
		if err := send("Aquarius", "hi"); err != nil {
			t.Fatalf("send: %v", err)
		}
		if got.Args[0] != "notify-send" || got.Args[1] != "Aquarius" || got.Args[2] != "hi" {
			t.Fatalf("args = %v", got.Args)
		}
		if got.Cancel == nil {
			t.Fatal("应为带超时上下文的 CommandContext（防通知守护挂起卡主循环）")
		}
	})
	t.Run("空正文不发送", func(t *testing.T) {
		called := false
		send := notifyWith("linux",
			func(*exec.Cmd) error { called = true; return nil },
			func(*exec.Cmd) error { called = true; return nil })
		if err := send("Aquarius", "   \n "); err != nil {
			t.Fatalf("send: %v", err)
		}
		if called {
			t.Fatal("空正文不应执行外部命令")
		}
	})
	t.Run("执行错误上抛", func(t *testing.T) {
		want := errors.New("无通知守护")
		if err := notifyWith("linux", nil, func(*exec.Cmd) error { return want })("t", "b"); err != want {
			t.Fatalf("err = %v, want %v", err, want)
		}
		if err := notifyWith("windows", func(*exec.Cmd) error { return want }, nil)("t", "b"); err != want {
			t.Fatalf("err = %v, want %v", err, want)
		}
	})
}

// writeConfigNotify 写带 output.notify 开关的可运行配置。
func writeConfigNotify(t *testing.T, dir, baseURL, name string, notify bool) {
	t.Helper()
	cfg := fmt.Sprintf(`{
  "model": {"provider":"openai-compatible","name":%q,"base_url":%q,"api_key":"secret:AQ_E2E_KEY"},
  "ui": {"kind":"repl"},
  "output": {"notify": %t, "tts": false},
  "limits": {"max_turns": 8, "max_context_tokens": 64000, "tool_output_chars": 20000, "tool_timeout_sec": 60}
}`, name, baseURL, notify)
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(cfg), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	t.Setenv("AQ_E2E_KEY", "test-key")
}

// TestRunNotifyWiring M3 验收④：output.notify=true 装配输出器——
// 提交回答触发发送；发送失败只记日志不打断；键缺失不装配（不触碰发送器）。
func TestRunNotifyWiring(t *testing.T) {
	// stubNotify 临时替换发送器工厂，返回记录函数；返回恢复函数。
	stubNotify := func(fn func(goos string) func(title, body string) error) func() {
		old := notifySenderImpl
		notifySenderImpl = fn
		return func() { notifySenderImpl = old }
	}

	t.Run("提交回答触发发送", func(t *testing.T) {
		srv, _ := scriptServer(t, []string{"通知这句"})
		dir := t.TempDir()
		writeConfigNotify(t, dir, srv.URL, "m", true)
		calls := 0
		var gotTitle, gotBody string
		restore := stubNotify(func(string) func(string, string) error {
			return func(title, body string) error {
				calls++
				gotTitle, gotBody = title, body
				return nil
			}
		})
		t.Cleanup(restore)

		var out, errBuf bytes.Buffer
		if code := run([]string{"-data", dir}, strings.NewReader("hi\n/quit\n"), &out, &errBuf); code != 0 {
			t.Fatalf("code = %d, stderr = %q", code, errBuf.String())
		}
		if calls != 1 || gotTitle != "Aquarius" || !strings.Contains(gotBody, "通知这句") {
			t.Fatalf("calls=%d title=%q body=%q", calls, gotTitle, gotBody)
		}
		if !strings.Contains(out.String(), "通知这句") {
			t.Fatalf("回答仍应正常呈现: %q", out.String())
		}
	})

	t.Run("发送失败只记日志不打断", func(t *testing.T) {
		srv, _ := scriptServer(t, []string{"第二句"})
		dir := t.TempDir()
		writeConfigNotify(t, dir, srv.URL, "m", true)
		restore := stubNotify(func(string) func(string, string) error {
			return func(string, string) error { return errors.New("无桌面会话") }
		})
		t.Cleanup(restore)

		var out, errBuf bytes.Buffer
		if code := run([]string{"-data", dir}, strings.NewReader("hi\n/quit\n"), &out, &errBuf); code != 0 {
			t.Fatalf("发送失败不应影响退出码: %d, stderr = %q", code, errBuf.String())
		}
		if !strings.Contains(errBuf.String(), "[输出器]") ||
			!strings.Contains(errBuf.String(), "无桌面会话") {
			t.Fatalf("stderr 缺输出器日志: %q", errBuf.String())
		}
		if !strings.Contains(out.String(), "第二句") {
			t.Fatalf("回答仍应正常呈现: %q", out.String())
		}
	})

	t.Run("键缺失不装配", func(t *testing.T) {
		srv, _ := scriptServer(t, []string{"无通知"})
		dir := t.TempDir()
		writeConfig(t, dir, srv.URL, "m") // 无 output 键 → 不装配
		factoryCalls := 0
		restore := stubNotify(func(string) func(string, string) error {
			factoryCalls++
			return func(string, string) error { return nil }
		})
		t.Cleanup(restore)

		var out, errBuf bytes.Buffer
		if code := run([]string{"-data", dir}, strings.NewReader("hi\n/quit\n"), &out, &errBuf); code != 0 {
			t.Fatalf("code = %d, stderr = %q", code, errBuf.String())
		}
		if factoryCalls != 0 {
			t.Fatalf("notify 关闭时不应构造发送器（factoryCalls=%d）", factoryCalls)
		}
	})
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
