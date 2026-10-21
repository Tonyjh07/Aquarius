package main

import (
	"context"
	"testing"

	"github.com/Tonyjh07/Aquarius/internal/domain/conversation"
)

// fakeTranscriber 假转写器（只用于「装配了什么」的判定，不参与真实转写）。
type fakeTranscriber struct{}

func (fakeTranscriber) Transcribe(context.Context, conversation.BlobRef, string) (string, error) {
	return "", nil
}

// TestVoiceSlotNeedsASR 界面语音键槽的取舍：语音识别没装配就不给槽（右钮保持发送键）。
//
// 这里钉的是一条来自真机的报障：语音压根没配，麦克风图标却亮着，按下去只得到
// 「语音识别未配置」。根因在装配——摄取器**总会**建出来（`/mic` 靠它报因），
// 若直接把它转成 Voice，界面那条 `Voice == nil` 的判空就永远不成立。
func TestVoiceSlotNeedsASR(t *testing.T) {
	w := &wiring{}
	if slot := w.voiceSlot(); slot != nil {
		t.Fatal("语音识别未装配时不应给语音键槽")
	}
	w.asr = fakeTranscriber{}
	if slot := w.voiceSlot(); slot == nil {
		t.Fatal("语音识别装配后应给语音键槽")
	}
}
