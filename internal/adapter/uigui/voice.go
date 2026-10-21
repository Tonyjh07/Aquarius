package uigui

import (
	"context"
	"strings"
)

// micButton 右钮此刻是否作语音键（D179）：空输入、无暂存附件、非编辑/确认态，
// 且装配了语音。录音期间恒为真——即使中途开始打字，也要留一个停止入口。
func (u *UI) micButton() bool {
	if u.opts.Voice == nil {
		return false
	}
	if u.recording.Load() {
		return true
	}
	if u.generating.Load() || u.m.confirm != nil || u.m.editTarget != "" || len(u.m.stagedFiles) > 0 {
		return false
	}
	return strings.TrimSpace(u.editor.Text()) == ""
}

// toggleVoice 开始/停止录音。录音在后台跑，完成后经 inbox 回投（不阻塞帧循环）。
func (u *UI) toggleVoice() {
	if u.opts.Voice == nil {
		return
	}
	if u.recording.Load() {
		u.cancelVoice()
		return
	}
	u.voiceMu.Lock()
	if u.voiceCancel != nil { // 已有一条在途，忽略重复点击
		u.voiceMu.Unlock()
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	u.voiceCancel = cancel
	u.voiceMu.Unlock()

	u.recording.Store(true)
	u.focusPending = true
	go func() {
		text, err := u.opts.Voice(ctx)
		u.post(voiceMsg{text: text, err: err})
	}()
}

// cancelVoice 停止录音：录音器收到取消后返回已录到的部分，照常转写。
func (u *UI) cancelVoice() {
	u.voiceMu.Lock()
	defer u.voiceMu.Unlock()
	if u.voiceCancel != nil {
		u.voiceCancel()
	}
}

// finishVoice 收尾：错误进状态行；文本回填输入框（不自动发送——先让用户改错字）。
func (u *UI) finishVoice(text string, err error) {
	u.voiceMu.Lock()
	if u.voiceCancel != nil {
		u.voiceCancel()
		u.voiceCancel = nil
	}
	u.voiceMu.Unlock()
	u.recording.Store(false)
	if err != nil {
		u.m.say("语音输入失败：" + err.Error())
		return
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return
	}
	if cur := u.editor.Text(); strings.TrimSpace(cur) != "" {
		text = strings.TrimRight(cur, "\n") + "\n" + text
	}
	u.editor.SetText(text)
	u.focusPending = true
}
