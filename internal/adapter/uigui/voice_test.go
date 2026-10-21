package uigui

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

// TestMicButtonOnlyWhenEmpty 空输入且装配了语音 → 右钮是语音键；有输入/暂存/编辑态
// 都是发送键（D179）。
func TestMicButtonOnlyWhenEmpty(t *testing.T) {
	u := newHeadless(t, Options{Voice: func(context.Context) (string, error) { return "", nil }})
	if !u.micButton() {
		t.Fatal("空输入应为语音键")
	}
	u.editor.SetText("hi")
	if u.micButton() {
		t.Fatal("有输入应为发送键")
	}
	u.editor.SetText("   ")
	if !u.micButton() {
		t.Fatal("纯空白应为语音键")
	}
	u.m.stagedFile = "C:/x.png"
	if u.micButton() {
		t.Fatal("有暂存附件应为发送键")
	}
	u.m.stagedFile = ""
	u.m.editTarget = "m1"
	if u.micButton() {
		t.Fatal("编辑态应为发送键")
	}
}

// TestMicButtonNeedsVoice 没装配语音：空输入仍保持发送键（不承诺做不到的事）。
func TestMicButtonNeedsVoice(t *testing.T) {
	u := newHeadless(t, Options{})
	if u.micButton() {
		t.Fatal("未装配语音时不应显示录音键")
	}
}

// TestFinishVoiceFillsEditor 转写成功：文本回填输入框（不自动发送），录音态结束。
func TestFinishVoiceFillsEditor(t *testing.T) {
	u := newHeadless(t, Options{Voice: func(context.Context) (string, error) { return "", nil }})
	u.recording.Store(true)
	u.finishVoice("  你好，世界  ", nil)
	if u.recording.Load() {
		t.Fatal("录音态应结束")
	}
	if got := u.editor.Text(); got != "你好，世界" {
		t.Fatalf("输入框 = %q", got)
	}
}

// TestFinishVoiceAppendsToDraft 录音期间已打了字：转写接在后面，不覆盖草稿。
func TestFinishVoiceAppendsToDraft(t *testing.T) {
	u := newHeadless(t, Options{Voice: func(context.Context) (string, error) { return "", nil }})
	u.editor.SetText("补充：")
	u.finishVoice("明天下午三点", nil)
	if got := u.editor.Text(); !strings.Contains(got, "补充：") || !strings.Contains(got, "明天下午三点") {
		t.Fatalf("输入框 = %q", got)
	}
}

// TestFinishVoiceErrorKeepsDraft 失败只提示，不动草稿。
func TestFinishVoiceErrorKeepsDraft(t *testing.T) {
	u := newHeadless(t, Options{Voice: func(context.Context) (string, error) { return "", nil }})
	u.editor.SetText("草稿")
	u.finishVoice("", errors.New("没有可用的录音设备"))
	if got := u.editor.Text(); got != "草稿" {
		t.Fatalf("输入框 = %q", got)
	}
	if u.recording.Load() {
		t.Fatal("录音态应结束")
	}
}

// TestToggleVoiceRoundTrip 点一下开始录音、再点一下停止；结果经开箱回投回填输入框。
func TestToggleVoiceRoundTrip(t *testing.T) {
	started := make(chan struct{})
	u := newHeadless(t, Options{Voice: func(ctx context.Context) (string, error) {
		close(started)
		<-ctx.Done() // 等第二次点击（停止）
		return "半句话", nil
	}})
	u.toggleVoice()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("录音没有启动")
	}
	if !u.recording.Load() {
		t.Fatal("应处于录音态")
	}
	u.toggleVoice() // 停止
	// 事件循环在另一 goroutine：用 drain 屏障等录音结果应用进状态机，之后才读输入框。
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		drainSync(t, u)
		if !u.recording.Load() {
			break
		}
	}
	if u.recording.Load() {
		t.Fatal("录音态应结束")
	}
	if got := u.editor.Text(); got != "半句话" {
		t.Fatalf("输入框 = %q", got)
	}
}
