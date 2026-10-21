package main

import (
	"strings"
	"testing"
)

// audioTestConfig 一份能过 model.normalize 的最小配置 + 可改的语音段。
func audioTestConfig(asr, tts audioChannel) *fileConfig {
	return &fileConfig{
		Model: modelConfig{
			Providers: []providerConfig{{
				Name:    "mimo",
				BaseURL: "https://api.example.com/v1",
				Keys:    []keyConfig{{APIKey: "k"}},
				Models:  []modelEntry{{Name: "mimo-v2.6-flash"}},
			}},
			Primary: "mimo",
			Name:    "mimo-v2.6-flash",
		},
		Audio: audioConfig{ASR: asr, TTS: tts},
	}
}

// TestNormalizeAudioDefaults 缺省语种 = auto；未配置的通道不算错误。
func TestNormalizeAudioDefaults(t *testing.T) {
	cfg := audioTestConfig(audioChannel{}, audioChannel{})
	if err := cfg.Model.normalize(); err != nil {
		t.Fatalf("model.normalize: %v", err)
	}
	if err := cfg.normalizeAudio(); err != nil {
		t.Fatalf("空语音段不应报错: %v", err)
	}
	if cfg.Input.Language != "auto" {
		t.Fatalf("language = %q, want auto", cfg.Input.Language)
	}
	if _, why := cfg.audioProvider("asr"); why == "" {
		t.Fatal("未配置 ASR 应报告不可用")
	}
}

// TestNormalizeAudioTrimsAndFillsKind 空白与缺省协议位归一。
func TestNormalizeAudioTrimsAndFillsKind(t *testing.T) {
	cfg := audioTestConfig(
		audioChannel{Provider: " mimo ", Model: " mimo-v2.5-asr "},
		audioChannel{Provider: "mimo", Model: "mimo-v2.5-tts", Voice: " 冰糖 ", Style: "语速稍快"},
	)
	if err := cfg.Model.normalize(); err != nil {
		t.Fatalf("model.normalize: %v", err)
	}
	if err := cfg.normalizeAudio(); err != nil {
		t.Fatalf("normalizeAudio: %v", err)
	}
	if cfg.Audio.ASR.Provider != "mimo" || cfg.Audio.ASR.Kind != kindChatAudio {
		t.Fatalf("ASR 归一 = %+v", cfg.Audio.ASR)
	}
	if cfg.Audio.TTS.Voice != "冰糖" || cfg.Audio.TTS.Style != "语速稍快" {
		t.Fatalf("TTS 归一 = %+v", cfg.Audio.TTS)
	}
	p, why := cfg.audioProvider("tts")
	if p == nil || why != "" {
		t.Fatalf("TTS 应解析到 provider，得到 %v / %q", p, why)
	}
}

// TestNormalizeAudioRejectsBadKindAndLanguage 协议位与语种写错是永不生效的配置，fail-fast。
func TestNormalizeAudioRejectsBadKindAndLanguage(t *testing.T) {
	cfg := audioTestConfig(audioChannel{Provider: "mimo", Model: "m", Kind: "websocket"}, audioChannel{})
	if err := cfg.Model.normalize(); err != nil {
		t.Fatalf("model.normalize: %v", err)
	}
	if err := cfg.normalizeAudio(); err == nil || !strings.Contains(err.Error(), "audio.asr.kind") {
		t.Fatalf("未知协议位应报错，得到 %v", err)
	}

	cfg = audioTestConfig(audioChannel{}, audioChannel{Provider: "mimo", Model: "m"})
	cfg.Input.Language = "ja"
	if err := cfg.Model.normalize(); err != nil {
		t.Fatalf("model.normalize: %v", err)
	}
	if err := cfg.normalizeAudio(); err == nil || !strings.Contains(err.Error(), "input.language") {
		t.Fatalf("未知语种应报错，得到 %v", err)
	}
}

// TestNormalizeAudioRequiresModel 写了 provider 却没写模型：保存后什么都不会发生，
// 应用时就拦住。
func TestNormalizeAudioRequiresModel(t *testing.T) {
	cfg := audioTestConfig(audioChannel{Provider: "mimo"}, audioChannel{})
	if err := cfg.Model.normalize(); err != nil {
		t.Fatalf("model.normalize: %v", err)
	}
	if err := cfg.normalizeAudio(); err == nil || !strings.Contains(err.Error(), "audio.asr.model") {
		t.Fatalf("缺模型应报错，得到 %v", err)
	}
}

// TestAudioProviderUnknownName 名字写错不阻断启动，但必须说明不可用。
func TestAudioProviderUnknownName(t *testing.T) {
	cfg := audioTestConfig(audioChannel{Provider: "nope", Model: "m"}, audioChannel{})
	if err := cfg.Model.normalize(); err != nil {
		t.Fatalf("model.normalize: %v", err)
	}
	if err := cfg.normalizeAudio(); err != nil {
		t.Fatalf("名字写错不应阻断启动: %v", err)
	}
	if p, why := cfg.audioProvider("asr"); p != nil || why == "" || !strings.Contains(why, "nope") {
		t.Fatalf("应明确报告 provider 不存在，得到 %v / %q", p, why)
	}
}
