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
	ep, why := cfg.audioProvider("tts")
	if why != "" || ep.BaseURL == "" || ep.Borrowed != "mimo" {
		t.Fatalf("TTS 应解析到借来的端点，得到 %+v / %q", ep, why)
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
	if _, why := cfg.audioProvider("asr"); why == "" || !strings.Contains(why, "nope") {
		t.Fatalf("应明确报告 provider 不存在，得到 %q", why)
	}
}

// TestAudioEndpointIndependent 独立端点与密钥（D222）：不引用任何 provider 也能用；
// 只填端点时密钥按能力回落 secret:AQUARIUS_<ASR|TTS>_API_KEY。
func TestAudioEndpointIndependent(t *testing.T) {
	cfg := audioTestConfig(
		audioChannel{BaseURL: "https://asr.example.com/v1", APIKey: "secret:MY_ASR", Model: "m1"},
		audioChannel{BaseURL: "https://tts.example.com/v1", Model: "m2"},
	)
	if err := cfg.Model.normalize(); err != nil {
		t.Fatalf("model.normalize: %v", err)
	}
	if err := cfg.normalizeAudio(); err != nil {
		t.Fatalf("独立配置应通过校验: %v", err)
	}
	asr, why := cfg.audioProvider("asr")
	if why != "" || asr.BaseURL != "https://asr.example.com/v1" || asr.KeyRef != "secret:MY_ASR" {
		t.Fatalf("ASR = %+v / %q", asr, why)
	}
	tts, why := cfg.audioProvider("tts")
	if why != "" || tts.BaseURL != "https://tts.example.com/v1" {
		t.Fatalf("TTS = %+v / %q", tts, why)
	}
	if tts.KeyRef != "secret:AQUARIUS_TTS_API_KEY" {
		t.Fatalf("只填端点时密钥应回落按能力派生的引用，得 %q", tts.KeyRef)
	}
	if asr.Borrowed != "" || tts.Borrowed != "" {
		t.Fatalf("独立配置不该记为借用: %q / %q", asr.Borrowed, tts.Borrowed)
	}
}

// TestAudioEndpointOverridesBorrow 显式字段只覆盖缺的那一项：填了端点仍借密钥，
// 填了密钥仍借端点（D222）。
func TestAudioEndpointOverridesBorrow(t *testing.T) {
	cfg := audioTestConfig(
		audioChannel{Provider: "mimo", BaseURL: "https://mirror.example.com/v1", Model: "m1"},
		audioChannel{Provider: "mimo", APIKey: "secret:MY_TTS", Model: "m2"},
	)
	if err := cfg.Model.normalize(); err != nil {
		t.Fatalf("model.normalize: %v", err)
	}
	if err := cfg.normalizeAudio(); err != nil {
		t.Fatalf("normalizeAudio: %v", err)
	}
	asr, _ := cfg.audioProvider("asr")
	if asr.BaseURL != "https://mirror.example.com/v1" {
		t.Fatalf("自己的端点应生效，得 %q", asr.BaseURL)
	}
	if asr.KeyRef != "k" { // 借用 mimo 的第一把（引用原文，可能是明文）
		t.Fatalf("没填密钥时应借 mimo 的，得 %q", asr.KeyRef)
	}
	tts, _ := cfg.audioProvider("tts")
	if tts.BaseURL != "https://api.example.com/v1" {
		t.Fatalf("没填端点时应借 mimo 的，得 %q", tts.BaseURL)
	}
	if tts.KeyRef != "secret:MY_TTS" {
		t.Fatalf("自己的密钥应生效，得 %q", tts.KeyRef)
	}
}

// TestAudioEndpointBorrowMissingNeedsOwnURL 借的条目不存在时：只填了密钥还不够（端点
// 无从定位），必须自己填端点。
func TestAudioEndpointBorrowMissingNeedsOwnURL(t *testing.T) {
	cfg := audioTestConfig(audioChannel{Provider: "gone", APIKey: "secret:K", Model: "m"}, audioChannel{})
	if err := cfg.Model.normalize(); err != nil {
		t.Fatalf("model.normalize: %v", err)
	}
	if _, why := cfg.audioProvider("asr"); why == "" || !strings.Contains(why, "端点") {
		t.Fatalf("端点无着落应明确报因，得到 %q", why)
	}

	cfg = audioTestConfig(audioChannel{
		Provider: "gone", BaseURL: "https://asr.example.com/v1", Model: "m",
	}, audioChannel{})
	if err := cfg.Model.normalize(); err != nil {
		t.Fatalf("model.normalize: %v", err)
	}
	if ep, why := cfg.audioProvider("asr"); why != "" || ep.BaseURL == "" {
		t.Fatalf("自己填了端点就该可用，得到 %+v / %q", ep, why)
	}
}

// TestNormalizeAudioKinds 两侧各认自己的协议位：识别认 whisper / openai / funasr，
// 合成认 vits / gptsovits；写错的一律 fail-fast（永远不会生效的配置）。
func TestNormalizeAudioKinds(t *testing.T) {
	ok := []struct {
		name string
		asr  audioChannel
		tts  audioChannel
	}{
		{"本地 whisper", audioChannel{Kind: kindASRWhisper, ModelPath: "/m/ggml-base.bin"}, audioChannel{}},
		{"OpenAI 转写", audioChannel{Kind: kindASROpenAI, BaseURL: "https://x/v1", Model: "whisper-1"}, audioChannel{}},
		{"FunASR 原生", audioChannel{Kind: kindASRFunASR, BaseURL: "http://127.0.0.1:10095"}, audioChannel{}},
		{"VITS", audioChannel{}, audioChannel{Kind: kindTTSVITS, BaseURL: "http://127.0.0.1:23456"}},
		{"GPT-SoVITS", audioChannel{}, audioChannel{Kind: kindTTSGPTSovits, BaseURL: "http://127.0.0.1:9880"}},
	}
	for _, c := range ok {
		t.Run(c.name, func(t *testing.T) {
			cfg := audioTestConfig(c.asr, c.tts)
			if err := cfg.Model.normalize(); err != nil {
				t.Fatalf("model.normalize: %v", err)
			}
			if err := cfg.normalizeAudio(); err != nil {
				t.Fatalf("normalizeAudio: %v", err)
			}
		})
	}

	bad := []struct {
		name string
		asr  audioChannel
		tts  audioChannel
		want string
	}{
		{"识别侧不认 vits", audioChannel{Kind: kindTTSVITS, BaseURL: "https://x", Model: "m"}, audioChannel{}, "audio.asr.kind"},
		{"合成侧不认 funasr", audioChannel{}, audioChannel{Kind: kindASRFunASR, BaseURL: "https://x"}, "audio.tts.kind"},
		{"whisper 缺模型路径", audioChannel{Kind: kindASRWhisper}, audioChannel{}, "model_path"},
	}
	for _, c := range bad {
		t.Run(c.name, func(t *testing.T) {
			cfg := audioTestConfig(c.asr, c.tts)
			if err := cfg.Model.normalize(); err != nil {
				t.Fatalf("model.normalize: %v", err)
			}
			if err := cfg.normalizeAudio(); err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("err = %v, want 含 %q", err, c.want)
			}
		})
	}
}

// TestNormalizeAudioWhisperNeedsNoEndpoint 本地引擎没有端点也没有密钥：只填模型路径
// 就算配置好了，不该因为「端点未配置」被拒。
func TestNormalizeAudioWhisperNeedsNoEndpoint(t *testing.T) {
	cfg := audioTestConfig(audioChannel{Kind: kindASRWhisper, ModelPath: " /m/ggml-base.bin "}, audioChannel{})
	if err := cfg.Model.normalize(); err != nil {
		t.Fatalf("model.normalize: %v", err)
	}
	if err := cfg.normalizeAudio(); err != nil {
		t.Fatalf("normalizeAudio: %v", err)
	}
	if !cfg.Audio.ASR.configured() {
		t.Fatal("填了模型路径应算已配置")
	}
	if cfg.Audio.ASR.ModelPath != "/m/ggml-base.bin" {
		t.Fatalf("模型路径未归一: %q", cfg.Audio.ASR.ModelPath)
	}
	// 装配路径不走 audioProvider（本地引擎不连网），这里确认它确实无端点可用。
	if _, why := cfg.audioProvider("asr"); why == "" {
		t.Fatal("本地引擎不该解析出端点")
	}
}

// TestNormalizeAudioEmptyKindDefaults 协议位留空 = chat-audio（老配置的形状，行为不变）。
func TestNormalizeAudioEmptyKindDefaults(t *testing.T) {
	cfg := audioTestConfig(audioChannel{Provider: "mimo", Model: "m"}, audioChannel{Provider: "mimo", Model: "m"})
	if err := cfg.Model.normalize(); err != nil {
		t.Fatalf("model.normalize: %v", err)
	}
	if err := cfg.normalizeAudio(); err != nil {
		t.Fatalf("normalizeAudio: %v", err)
	}
	if cfg.Audio.ASR.Kind != kindChatAudio || cfg.Audio.TTS.Kind != kindChatAudio {
		t.Fatalf("协议位 = %q / %q", cfg.Audio.ASR.Kind, cfg.Audio.TTS.Kind)
	}
}
