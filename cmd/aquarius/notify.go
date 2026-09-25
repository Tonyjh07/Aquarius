package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/Tonyjh07/Aquarius/internal/domain/conversation"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

// outputsPresenter 输出扇出装饰器（D28/D14）：包住实际 UI，Emit 透传后对
// 已提交的 assistant 回答逐个调 OutputAdapter.Deliver——失败只记日志、不打断
// 主流程（DESIGN §5.7/§7.2）。app 与 UI 适配器均不感知输出器。
type outputsPresenter struct {
	inner port.Presenter
	outs  []port.OutputAdapter
	log   func(error) // 记日志（nil = 丢弃）
}

func (p *outputsPresenter) Emit(ctx context.Context, ev port.Event) error {
	if err := p.inner.Emit(ctx, ev); err != nil {
		return err
	}
	if len(p.outs) == 0 {
		return nil
	}
	ce, ok := ev.(port.CommittedEvent)
	if !ok || ce.Message.Role != conversation.RoleAssistant || ce.Message.Outcome != conversation.OutcomeDone {
		return nil
	}
	req := port.OutputRequest{Message: ce.Message, Parts: ce.Message.Content}
	for _, out := range p.outs {
		if err := out.Deliver(ctx, req); err != nil && p.log != nil {
			p.log(fmt.Errorf("输出器 %s: %w", out.Name(), err))
		}
	}
	return nil
}

// notifySenderImpl 装配用发送器入口（测试可替换，避免 e2e 真弹系统通知）。
var notifySenderImpl = notifySender

// notifyTimeout 非 Windows 分支的发送超时：通知守护缺失/挂起时以超时收场，
// 不卡主循环（§5.7"失败只记日志、不打断主流程"——挂起不是失败）。
const notifyTimeout = 5 * time.Second

// notifySender 平台通知发送（外部命令尽力而为；失败由装饰器记日志）。
// 正文/标题经 argv 或环境变量传递，绝不拼进脚本——内容来自模型（不可信数据，§9）。
func notifySender(goos string) func(title, body string) error {
	return notifyWith(goos, startDetached, func(cmd *exec.Cmd) error { return cmd.Run() })
}

// notifyWith 组装发送函数（执行方式可注入，便于测试断言分支与命令构造）：
// windows 分离启动（气泡需驻留数秒），其余平台带超时同步执行。
func notifyWith(goos string, detached, waiting func(*exec.Cmd) error) func(title, body string) error {
	return func(title, body string) error {
		if strings.TrimSpace(body) == "" {
			return nil
		}
		path, args, env := notifyCmd(goos, title, body)
		if goos == "windows" {
			cmd := exec.Command(path, args...)
			if len(env) > 0 {
				cmd.Env = append(os.Environ(), env...)
			}
			return detached(cmd)
		}
		ctx, cancel := context.WithTimeout(context.Background(), notifyTimeout)
		defer cancel()
		cmd := exec.CommandContext(ctx, path, args...)
		if len(env) > 0 {
			cmd.Env = append(os.Environ(), env...)
		}
		return waiting(cmd)
	}
}

// startDetached 分离启动（进程不随调用返回退出），由后台 goroutine 回收句柄。
func startDetached(cmd *exec.Cmd) error {
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("启动 %s: %w", cmd.Path, err)
	}
	go func() { _ = cmd.Wait() }()
	return nil
}

// notifyCmd 构造平台通知命令（argv/env 与脚本分离，便于测试断言注入安全）。
func notifyCmd(goos, title, body string) (path string, args, env []string) {
	switch goos {
	case "windows":
		return "powershell",
			[]string{"-NoProfile", "-NonInteractive", "-Command", windowsNotifyScript},
			[]string{
				"AQUARIUS_NOTIFY_TITLE=" + title,
				"AQUARIUS_NOTIFY_BODY=" + body,
			}
	case "darwin":
		// 正文已是单行（Adapter.Preview 压平）；此处再防御换行，
		// %q 负责引号/反斜杠转义（AppleScript 支持这两种转义）。
		script := fmt.Sprintf("display notification %q with title %q",
			oneLine(body), oneLine(title))
		return "osascript", []string{"-e", script}, nil
	default:
		return "notify-send", []string{title, body}, nil
	}
}

// oneLine 把换行/制表压成空格（osascript 单行字面量用）。
func oneLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// windowsNotifyScript PowerShell 气泡通知：从环境变量取文案（不拼接正文），
// NotifyIcon 需要消息循环存活片刻再释放。
const windowsNotifyScript = `Add-Type -AssemblyName System.Windows.Forms
Add-Type -AssemblyName System.Drawing
$n = New-Object System.Windows.Forms.NotifyIcon
$n.Icon = [System.Drawing.SystemIcons]::Information
$n.Visible = $true
$n.ShowBalloonTip(5000, $env:AQUARIUS_NOTIFY_TITLE, $env:AQUARIUS_NOTIFY_BODY, [System.Windows.Forms.ToolTipIcon]::Info)
Start-Sleep -Seconds 6
$n.Dispose()`
