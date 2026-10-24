package uigui

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Tonyjh07/Aquarius/internal/port"
)

// audioSnapshot 语音页快照（provider 列表来自 ProviderMgr 快照）。
func audioSnapshot() SettingsSnapshot {
	s := fillSnapshot()
	s.ASRProvider = "openai"
	s.ASRModel = "mimo-v2.5-asr"
	s.ASRLang = "zh"
	s.TTSProvider = "openai"
	s.TTSModel = "mimo-v2.5-tts"
	s.TTSVoice = "冰糖"
	s.TTSDesign = "温柔女声"
	s.TTSStyle = "语速稍快"
	s.TTSTags = "温柔"
	return s
}

// TestAudioPageFillsSnapshot 开窗填表：provider 循环档选中快照值，模型与音色等
// 文本字段原样落位；语种档按值定位。
func TestAudioPageFillsSnapshot(t *testing.T) {
	u := newHeadless(t, Options{
		Settings:      func() SettingsSnapshot { return audioSnapshot() },
		ApplySettings: func(SettingsPatch) ([]port.Command, error) { return nil, nil },
		ProviderMgr:   &fakeProviderMgr{snap: []ProviderSnapshot{{Name: "openai"}, {Name: "mimo"}}},
	})
	f := newSettingsForm(u)
	if got := f.asrProv.value(); got != "openai" {
		t.Fatalf("识别服务选中 = %q, want openai", got)
	}
	if got := f.ttsProv.value(); got != "openai" {
		t.Fatalf("合成服务选中 = %q, want openai", got)
	}
	if got := f.asrLang.value(); got != "zh" {
		t.Fatalf("语种 = %q, want zh", got)
	}
	if f.asrModel.Text() != "mimo-v2.5-asr" || f.ttsModel.Text() != "mimo-v2.5-tts" {
		t.Fatalf("模型档 = %q / %q", f.asrModel.Text(), f.ttsModel.Text())
	}
	if f.ttsVoice.Text() != "冰糖" || f.ttsDesign.Text() != "温柔女声" ||
		f.ttsStyle.Text() != "语速稍快" || f.ttsTags.Text() != "温柔" {
		t.Fatalf("音色/风格档 = %q/%q/%q/%q",
			f.ttsVoice.Text(), f.ttsDesign.Text(), f.ttsStyle.Text(), f.ttsTags.Text())
	}
}

// TestAudioProviderChoicesKeepUnknown 快照引用了一个已不在 provider 列表里的名字时，
// 选项里补上它——开窗后直接保存不会把它悄悄改成「未配置」。
func TestAudioProviderChoicesKeepUnknown(t *testing.T) {
	s := audioSnapshot()
	s.TTSProvider = "removed"
	u := newHeadless(t, Options{
		Settings:      func() SettingsSnapshot { return s },
		ApplySettings: func(SettingsPatch) ([]port.Command, error) { return nil, nil },
		ProviderMgr:   &fakeProviderMgr{snap: []ProviderSnapshot{{Name: "openai"}}},
	})
	f := newSettingsForm(u)
	if got := f.ttsProv.value(); got != "removed" {
		t.Fatalf("失联的 provider 选中 = %q, want removed", got)
	}
	if !strings.Contains(strings.Join(f.ttsProv.displays, "|"), "removed") {
		t.Fatalf("选项里应补上 removed: %v", f.ttsProv.displays)
	}
}

// TestSaveIncludesAudio 保存提交带上语音页全部字段（草稿值原样）。
func TestSaveIncludesAudio(t *testing.T) {
	var got SettingsPatch
	u := newHeadless(t, Options{
		Settings:      func() SettingsSnapshot { return audioSnapshot() },
		ApplySettings: func(p SettingsPatch) ([]port.Command, error) { got = p; return nil, nil },
		ProviderMgr:   &fakeProviderMgr{snap: []ProviderSnapshot{{Name: "openai"}, {Name: "mimo"}}},
	})
	f := newSettingsForm(u)
	f.page = pageAudio
	f.ttsVoice.SetText("云希")
	f.ttsTags.SetText("温柔 慢速")
	f.asrLang.setValue("en")
	u.saveSettings(f)
	if got.ASRProvider != "openai" || got.ASRModel != "mimo-v2.5-asr" || got.ASRLang != "en" {
		t.Fatalf("ASR patch = %+v", got)
	}
	if got.TTSProvider != "openai" || got.TTSModel != "mimo-v2.5-tts" ||
		got.TTSVoice != "云希" || got.TTSDesign != "温柔女声" ||
		got.TTSStyle != "语速稍快" || got.TTSTags != "温柔 慢速" {
		t.Fatalf("TTS patch = %+v", got)
	}
	if !strings.Contains(f.status, "已保存") {
		t.Fatalf("保存反馈 = %q", f.status)
	}
}

// TestAudioEndpointRows 语音页的端点与密钥行（D222）：端点回填、密钥框恒空（只写不回显），
// 行计划里有这两行；填的值原样进 patch。
func TestAudioEndpointRows(t *testing.T) {
	s := audioSnapshot()
	s.ASRBaseURL = "https://asr.example.com/v1"
	s.ASRKeySaved, s.ASRKeyRef = true, "secret:AQUARIUS_ASR_API_KEY"
	var got SettingsPatch
	u := newHeadless(t, Options{
		Settings:      func() SettingsSnapshot { return s },
		ApplySettings: func(p SettingsPatch) ([]port.Command, error) { got = p; return nil, nil },
		ProviderMgr:   &fakeProviderMgr{snap: []ProviderSnapshot{{Name: "openai"}}},
	})
	f := newSettingsForm(u)
	if f.asrBaseURL.Text() != "https://asr.example.com/v1" {
		t.Fatalf("端点未回填: %q", f.asrBaseURL.Text())
	}
	if f.asrKey.Text() != "" {
		t.Fatalf("密钥框应恒空，实为 %q", f.asrKey.Text())
	}
	if note := f.audioKeyNote("asr"); !strings.Contains(note, "secret:AQUARIUS_ASR_API_KEY") {
		t.Fatalf("密钥形态说明 = %q", note)
	}
	// 行计划里有端点/密钥行，且识别与合成各一组。
	count := map[rowKind]int{}
	for _, r := range f.pageRows(pageAudio) {
		count[r.kind]++
	}
	for _, k := range []rowKind{rowAudioASREndpoint, rowAudioASRKey, rowAudioTTSEndpoint, rowAudioTTSKey} {
		if count[k] != 1 {
			t.Errorf("行型 %v 应恰好一行", k)
		}
	}

	f.asrBaseURL.SetText("https://mirror.example.com/v1")
	f.asrKey.SetText("secret:MY_ASR")
	f.ttsBaseURL.SetText("https://tts.example.com/v1")
	f.ttsKey.SetText("sk-tts-plain")
	u.saveSettings(f)
	if got.ASRBaseURL != "https://mirror.example.com/v1" || got.ASRKey != "secret:MY_ASR" {
		t.Fatalf("ASR 端点/密钥 = %q / %q", got.ASRBaseURL, got.ASRKey)
	}
	if got.TTSBaseURL != "https://tts.example.com/v1" || got.TTSKey != "sk-tts-plain" {
		t.Fatalf("TTS 端点/密钥 = %q / %q", got.TTSBaseURL, got.TTSKey)
	}
	// 回写快照：填了明文后不再标成「明文」（config 里落的是引用）。
	if f.snap.ASRKeySaved != true || f.snap.ASRKeyRef != "secret:MY_ASR" {
		t.Fatalf("ASR 快照形态 = %+v", f.snap.ASRKeySaved)
	}
	if f.snap.TTSKeyPlain {
		t.Fatal("填了新值后不该继续标成明文")
	}
}

// TestFillAudioFromService 「填入」把所选服务的端点与密钥引用抄进该通道（D222）。
func TestFillAudioFromService(t *testing.T) {
	u := newHeadless(t, Options{
		Settings:      func() SettingsSnapshot { return audioSnapshot() },
		ApplySettings: func(SettingsPatch) ([]port.Command, error) { return nil, nil },
		ProviderMgr: &fakeProviderMgr{snap: []ProviderSnapshot{{
			Name:    "mimo",
			BaseURL: "https://api.mimo.ai/v1",
			Keys:    []ProviderKeySnapshot{{Saved: true, Ref: "secret:AQUARIUS_MIMO_API_KEY"}},
			Models:  []ProviderModelSnapshot{{Name: "mimo-v2.5-asr", Enabled: true}},
		}}},
	})
	f := newSettingsForm(u)
	f.asrProv.setValue("mimo")
	f.fillAudioFromService(&f.asrProv, &f.asrBaseURL, &f.asrKey)
	if got := f.asrBaseURL.Text(); got != "https://api.mimo.ai/v1" {
		t.Fatalf("端点未抄入: %q", got)
	}
	if got := f.asrKey.Text(); got != "secret:AQUARIUS_MIMO_API_KEY" {
		t.Fatalf("密钥引用未抄入: %q", got)
	}
	// 「未配置」= 不抄任何东西。
	f.asrBaseURL.SetText("")
	f.asrKey.SetText("")
	f.asrProv.setValue("")
	f.fillAudioFromService(&f.asrProv, &f.asrBaseURL, &f.asrKey)
	if f.asrBaseURL.Text() != "" || f.asrKey.Text() != "" {
		t.Fatalf("未选服务时不该抄入: %q / %q", f.asrBaseURL.Text(), f.asrKey.Text())
	}
}

// TestPreviewVoiceUsesDraft 试听按草稿的音色/描述/风格合成一句（服务与模型来自装配）；
// 结果回灌状态行。
func TestPreviewVoiceUsesDraft(t *testing.T) {
	var got port.SpeechRequest
	u := newHeadless(t, Options{
		Settings:      func() SettingsSnapshot { return audioSnapshot() },
		ApplySettings: func(SettingsPatch) ([]port.Command, error) { return nil, nil },
		PreviewVoice: func(req port.SpeechRequest) error {
			got = req
			return nil
		},
	})
	f := newSettingsForm(u)
	f.ttsVoice.SetText("云希")
	f.ttsDesign.SetText("清亮男声")
	f.ttsStyle.SetText("慢一点")
	f.ttsTags.SetText("沉稳")
	u.previewVoice(f)
	if !f.voiceBusy || f.voiceStatus != "正在合成…" {
		t.Fatalf("试听应进入合成态: busy=%v status=%q", f.voiceBusy, f.voiceStatus)
	}
	select {
	case err := <-f.voiceRes:
		f.voiceBusy = false
		f.voiceErr = err != nil
		if err != nil {
			f.voiceStatus = "试听失败：" + err.Error()
		} else {
			f.voiceStatus = "已开始播放"
		}
	case <-time.After(2 * time.Second):
		t.Fatal("超时：试听没有回结果")
	}
	if got.Text == "" {
		t.Fatal("试听请求没有正文")
	}
	if got.Voice != "云希" || got.Design != "清亮男声" || got.Style != "慢一点" || got.Tags != "沉稳" {
		t.Fatalf("试听参数 = %+v", got)
	}
	if f.voiceStatus != "已开始播放" {
		t.Fatalf("试听反馈 = %q", f.voiceStatus)
	}
}

// TestPreviewVoiceFailureAndGuard 失败写进状态行；未接线时直接报因；合成中重复点击不叠加。
func TestPreviewVoiceFailureAndGuard(t *testing.T) {
	u := newHeadless(t, Options{
		Settings:      func() SettingsSnapshot { return audioSnapshot() },
		ApplySettings: func(SettingsPatch) ([]port.Command, error) { return nil, nil },
		PreviewVoice:  func(port.SpeechRequest) error { return errors.New("服务端返回 400") },
	})
	f := newSettingsForm(u)
	u.previewVoice(f)
	select {
	case err := <-f.voiceRes:
		if err == nil || !strings.Contains(err.Error(), "400") {
			t.Fatalf("试听结果 = %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("超时：失败结果没回来")
	}

	ro := newHeadless(t, Options{
		Settings: func() SettingsSnapshot { return audioSnapshot() },
	})
	rf := newSettingsForm(ro)
	ro.previewVoice(rf)
	if !rf.voiceErr || !strings.Contains(rf.voiceStatus, "不可用") {
		t.Fatalf("未接线试听反馈 = %q (err=%v)", rf.voiceStatus, rf.voiceErr)
	}

	// 在途第二次点击直接忽略（voiceBusy 保护）。
	calls := 0
	started := make(chan struct{}, 1)
	block := make(chan struct{})
	u2 := newHeadless(t, Options{
		Settings:      func() SettingsSnapshot { return audioSnapshot() },
		ApplySettings: func(SettingsPatch) ([]port.Command, error) { return nil, nil },
		PreviewVoice: func(port.SpeechRequest) error {
			calls++
			started <- struct{}{}
			<-block
			return nil
		},
	})
	f2 := newSettingsForm(u2)
	u2.previewVoice(f2)
	<-started // 等第一次合成真的在途
	u2.previewVoice(f2)
	close(block)
	<-f2.voiceRes
	if calls != 1 {
		t.Fatalf("合成中重复点击应被忽略，实际调用 %d 次", calls)
	}
}
