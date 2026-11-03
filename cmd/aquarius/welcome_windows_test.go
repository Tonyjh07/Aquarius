//go:build windows

package main

// 欢迎向导取消路径的可见报因（D263，v0.3.0 实测「跳过后静默退出」的回归）：双击启动时
// 控制台已脱离（D114），stderr 不可达——缺密钥的取消必须经系统消息框呈现，窗口不能凭空
// 消失。消息框注入位与 console_windows.go 的其他 Win32 面函数同口径。

import (
	"context"
	"io"
	"strings"
	"testing"

	"github.com/Tonyjh07/Aquarius/internal/adapter/uigui"
)

func TestSetupWizardCancelWithoutKeyShowsMessageBox(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("DEEPSEEK_API_KEY", "") // 模板密钥引用取不到 ⇒ 问题清单非空
	defer swapSetupWindowRunner(func(uigui.SetupRequest) (uigui.SetupOutcome, error) {
		return uigui.SetupCancelled, nil
	})()
	prevDetached, prevBox := consoleDetached, messageBox
	var boxText string
	consoleDetached, messageBox = true, func(_, text string) { boxText = text }
	defer func() { consoleDetached, messageBox = prevDetached, prevBox }()

	w := &wiring{stdout: io.Discard, stderr: io.Discard}
	if err := w.bootstrap(dir, "", "", ""); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	code, done := w.setupWizard(context.Background(), dir, "", "", "")
	if !done || code != 0 {
		t.Fatalf("setupWizard = (%d, %v), want (0, true)", code, done)
	}
	if !strings.Contains(boxText, "DEEPSEEK_API_KEY") {
		t.Fatalf("消息框应指名缺失的环境变量: %q", boxText)
	}
	if !strings.Contains(boxText, "已取消配置引导") {
		t.Fatalf("消息框应说明取消后果: %q", boxText)
	}
}

// 密钥可用时跳过不弹框、不退出（与 welcome_test.go 的结果断言互补，这里盯「无消息框」）。
func TestSetupWizardSkipWithKeyNoMessageBox(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("DEEPSEEK_API_KEY", "sk-from-env")
	defer swapSetupWindowRunner(func(uigui.SetupRequest) (uigui.SetupOutcome, error) {
		return uigui.SetupCancelled, nil
	})()
	prevDetached, prevBox := consoleDetached, messageBox
	boxed := false
	consoleDetached, messageBox = true, func(_, _ string) { boxed = true }
	defer func() { consoleDetached, messageBox = prevDetached, prevBox }()

	w := &wiring{stdout: io.Discard, stderr: io.Discard}
	if err := w.bootstrap(dir, "", "", ""); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	code, done := w.setupWizard(context.Background(), dir, "", "", "")
	if done || code != 0 {
		t.Fatalf("setupWizard = (%d, %v), want (0, false)", code, done)
	}
	if boxed {
		t.Fatal("密钥可用的跳过应继续启动，不该弹消息框")
	}
}
