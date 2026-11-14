package main

// audio 段（D179/D222）：识别与合成两路独立服务配置、协议位表、归一化与必填项校验。
// 端点与密钥引用解析的公用件（serviceEndpoint / resolveEndpoint）在 config.go，联网
// 也走同一条。

import (
	"fmt"
	"strings"
	"time"

	"github.com/Tonyjh07/Aquarius/internal/i18n"
)

// audioConfig 语音配置（D179/D222/D295）：识别与合成各自是一条**完整**的服务配置，
// 端点与密钥可以自己填，也可以借 model.providers 里的一条（不继承 kind、能力位，
// 也不进降级链）。VAD 是采集侧的前处理，不属于任何一路服务。
type audioConfig struct {
	ASR audioChannel `json:"asr"`
	TTS audioChannel `json:"tts"`
	VAD vadConfig    `json:"vad"`
}

// vadConfig 按说话分段（D295）。默认值取上游 Silero VAD 的缺省：
// 阈值 0.5、静音 100ms 判结束、短于 250ms 的段丢弃、两端各补 30ms。
//
// Enabled 用 *bool 区分「键缺失 = 开」与「显式 false = 关」：这一项默认是开的，
// 用 bool 表达不了默认开（零值恒为 false，老配置会被静默关掉）。
type vadConfig struct {
	Enabled      *bool   `json:"enabled,omitempty"`
	Threshold    float64 `json:"threshold,omitempty"`
	MinSilenceMS int     `json:"min_silence_ms,omitempty"`
	MinSpeechMS  int     `json:"min_speech_ms,omitempty"`
}

// 分段参数的缺省值与合法范围。
const (
	vadDefaultThreshold    = 0.5
	vadDefaultMinSilenceMS = 100
	vadDefaultMinSpeechMS  = 250
	vadDefaultPadMS        = 30
	vadMinThreshold        = 0.05
	vadMaxThreshold        = 0.95
	vadMaxSilenceMS        = 5000
	vadMaxSpeechMS         = 5000
)

// enabled 分段是否开启（键缺失 = 开，D295 决策 5）。
func (v vadConfig) enabled() bool { return v.Enabled == nil || *v.Enabled }

// thresholdOrDefault 开始说话的阈值。
func (v vadConfig) thresholdOrDefault() float32 {
	if v.Threshold <= 0 {
		return vadDefaultThreshold
	}
	return float32(v.Threshold)
}

// negThresholdOrDefault 收尾阈值：比开始阈值低 0.15，与上游一致。
//
// 阈值压低到接近下限时那 0.15 会吃掉全部余量（收尾阈值撞上 0.05 的下限就与开始
// 阈值持平），这时按「半个阈值」让步进跟着走——两个阈值相等会让分段器永远判不出
// 「说完了」，段只有在录音结束那一刻才收尾。
func (v vadConfig) negThresholdOrDefault() float32 {
	t := v.thresholdOrDefault()
	step := float32(0.15)
	if t/2 < step {
		step = t / 2
	}
	n := t - step
	if n < vadMinThreshold {
		n = vadMinThreshold
	}
	if n >= t {
		return t / 2
	}
	return n
}

// minSilenceOrDefault 判定一句话说完的静音时长。
func (v vadConfig) minSilenceOrDefault() time.Duration {
	if v.MinSilenceMS <= 0 {
		return vadDefaultMinSilenceMS * time.Millisecond
	}
	return time.Duration(v.MinSilenceMS) * time.Millisecond
}

// minSpeechOrDefault 丢弃过短瞬态的门槛。
func (v vadConfig) minSpeechOrDefault() time.Duration {
	if v.MinSpeechMS <= 0 {
		return vadDefaultMinSpeechMS * time.Millisecond
	}
	return time.Duration(v.MinSpeechMS) * time.Millisecond
}

// padOrDefault 段两端补偿量（不开放配置，沿用上游缺省）。
func (v vadConfig) padOrDefault() time.Duration {
	return vadDefaultPadMS * time.Millisecond
}

// audioChannel 一路语音能力的端点、密钥与参数。
//
// 三种形态（D222）：
//   - 什么都不填 = 该能力不可用；上层据此隐藏入口并报因，而不是启动失败；
//   - 只填 provider = 借它的端点与密钥（老配置的形状，行为不变）；
//   - 填了 base_url / api_key = 独立配置，填了的那项覆盖借来的。
type audioChannel struct {
	Provider string `json:"provider,omitempty"`
	// BaseURL 独立端点（如 https://api.example.com/v1）；空 = 借用 provider 的。
	BaseURL string `json:"base_url,omitempty"`
	// APIKey 独立密钥（`secret:名` 或明文）；空 = 借用 provider 的第一把。
	APIKey string `json:"api_key,omitempty"`
	Model  string `json:"model,omitempty"`
	// Kind 协议位（空 = chat-audio）：识别侧 chat-audio | whisper | openai | funasr，
	// 合成侧 chat-audio | vits | gptsovits。协议决定打哪个端点、字段怎么摆。
	Kind string `json:"kind,omitempty"`
	// ModelPath 本地识别模型文件（ggml 格式）的绝对路径，只对 whisper 有意义。
	// 模型几十 MB 起，不进仓库也不进二进制，用户自己下载后填这里。
	ModelPath string `json:"model_path,omitempty"`
	// Voice 预置音色名 / 音色设计描述之外的模式：`-voiceclone` 时是样本文件绝对路径。
	Voice string `json:"voice,omitempty"`
	// Design 音色描述（`-voicedesign` 模型必填，进 user 消息）。
	Design string `json:"design,omitempty"`
	// Style 自然语言风格指令（进 user 消息；空 = 不发）。
	Style string `json:"style,omitempty"`
	// Tags 风格标签（如 "温柔 语速稍快"）：非空时拼成 "(标签)" 前缀置于正文开头。
	Tags string `json:"tags,omitempty"`
}

// 音频协议位（audio.asr.kind / audio.tts.kind）。
//
// 识别侧四个：chat-audio 走 chat/completions 携带音频（首发形状）；whisper 是进程内
// 本地引擎（读 model_path）；openai 是业界事实标准的多段上传；funasr 是 FunASR 自带
// HTTP 服务的原生形状。合成侧三个：chat-audio 同上；vits 与 gptsovits 都是普通 HTTP
// 返回 WAV，差别在路径、请求方式与音色怎么表达。
//
// 分派靠这张表而不是「模型名猜协议」：同一个模型名可以挂在不同的服务端后面。
const (
	kindChatAudio = "chat-audio"

	kindASRWhisper = "whisper"
	kindASROpenAI  = "openai"
	kindASRFunASR  = "funasr"

	kindTTSVITS      = "vits"
	kindTTSGPTSovits = "gptsovits"
)

// asrKinds / ttsKinds 两侧各自认识的协议位（报因时列出）。
var (
	asrKinds = []string{kindChatAudio, kindASRWhisper, kindASROpenAI, kindASRFunASR}
	ttsKinds = []string{kindChatAudio, kindTTSVITS, kindTTSGPTSovits}
)

// normalizeAudio 语音配置归一与校验（在 model.normalize 之后调用）。
// 引用的 provider **不存在不算启动失败**：语音是可选能力，按「不可用 + 明确报因」
// 处理，用户配错一行不该连对话都开不起来。协议词与语种写错则 fail-fast——那是
// 永远不会生效的配置，留着只会让人以为开了。
//
// 每侧认识的协议位不同（见 asrKinds / ttsKinds），必填项也跟着协议走：只有
// chat-audio 需要「模型名」，本地引擎要的是模型文件路径，两个 HTTP 合成服务既不要
// 模型名也不要端点之外的密钥约定。
func (c *fileConfig) normalizeAudio() error {
	c.Input.Language = strings.ToLower(strings.TrimSpace(c.Input.Language))
	switch c.Input.Language {
	case "":
		c.Input.Language = "auto"
	case "auto", "zh", "en":
	default:
		return fmt.Errorf("%s", i18n.Tf("cmd.config.err.input_language", "lang", c.Input.Language))
	}
	if err := normalizeAudioChannel("audio.asr", &c.Audio.ASR, asrKinds); err != nil {
		return err
	}
	if err := normalizeVAD(&c.Audio.VAD); err != nil {
		return err
	}
	return normalizeAudioChannel("audio.tts", &c.Audio.TTS, ttsKinds)
}

// normalizeVAD 校验按说话分段的参数（D295）。越界即启动报错：分段的阈值调错会让
// 「说三句得三段」变成「一句话被切成十段」，静默跑下去比启动失败更难排查。
func normalizeVAD(v *vadConfig) error {
	if v.Threshold != 0 && (v.Threshold < vadMinThreshold || v.Threshold > vadMaxThreshold) {
		return fmt.Errorf("%s", i18n.Tf("cmd.config.err.vad_threshold",
			"min", vadMinThreshold, "max", vadMaxThreshold, "v", v.Threshold))
	}
	if v.MinSilenceMS < 0 || v.MinSilenceMS > vadMaxSilenceMS {
		return fmt.Errorf("%s", i18n.Tf("cmd.config.err.vad_min_silence",
			"max", vadMaxSilenceMS, "v", v.MinSilenceMS))
	}
	if v.MinSpeechMS < 0 || v.MinSpeechMS > vadMaxSpeechMS {
		return fmt.Errorf("%s", i18n.Tf("cmd.config.err.vad_min_speech",
			"max", vadMaxSpeechMS, "v", v.MinSpeechMS))
	}
	return nil
}

// normalizeAudioChannel 归一一路语音配置并按协议校验必填项（启动期与保存前共用同一
// 口径：落盘的配置必须能过启动校验）。
func normalizeAudioChannel(name string, ch *audioChannel, kinds []string) error {
	ch.Provider = strings.TrimSpace(ch.Provider)
	ch.BaseURL = strings.TrimSpace(ch.BaseURL)
	ch.APIKey = strings.TrimSpace(ch.APIKey)
	ch.Model = strings.TrimSpace(ch.Model)
	ch.Kind = strings.TrimSpace(ch.Kind)
	ch.ModelPath = strings.TrimSpace(ch.ModelPath)
	ch.Voice = strings.TrimSpace(ch.Voice)
	ch.Design = strings.TrimSpace(ch.Design)
	ch.Style = strings.TrimSpace(ch.Style)
	ch.Tags = strings.TrimSpace(ch.Tags)
	// 「明确选了非缺省协议」本身就是意图：即使端点还没填，也要按该协议的必填项校验，
	// 免得存下一个永远不会生效的组合。选回缺省协议则跟「什么都没填」等价——设置窗
	// 保存时循环档恒有值，不能因此把没配语音的机器拦在保存上。
	explicitKind := ch.Kind != "" && ch.Kind != kindChatAudio
	if !explicitKind {
		ch.Kind = kindChatAudio
	}
	if !containsString(kinds, ch.Kind) {
		return fmt.Errorf("%s", i18n.Tf("cmd.config.err.audio_kind",
			"name", name, "kind", ch.Kind, "kinds", strings.Join(kinds, " | ")))
	}
	if !ch.configured() && !explicitKind {
		return nil // 未配置 = 该能力不可用
	}
	if explicitKind && ch.Kind != kindASRWhisper && ch.Provider == "" && ch.BaseURL == "" {
		return fmt.Errorf("%s", i18n.Tf("cmd.config.err.audio_endpoint", "name", name, "kind", ch.Kind))
	}
	switch ch.Kind {
	case kindASRWhisper:
		if ch.ModelPath == "" {
			return fmt.Errorf("%s", i18n.Tf("cmd.config.err.audio_model_path", "name", name))
		}
	case kindASRFunASR, kindTTSVITS, kindTTSGPTSovits:
		// FunASR 的模型由服务端启动参数决定；VITS 与 GPT-SoVITS 的「音色」是
		// 说话人 ID / 参考音频。这三种都没有「模型名」这一层。
	default:
		if ch.Model == "" {
			return fmt.Errorf("%s", i18n.Tf("cmd.config.err.audio_model", "name", name))
		}
	}
	return nil
}

// containsString 列表里有没有这一项（协议白名单用）。
func containsString(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}

// audioProvider 解析一路语音能力的端点与密钥引用（D179/D222）。
// 返回 (零值, 原因) = 该能力不可用；原因面向用户，可直接展示。
func (c *fileConfig) audioProvider(which string) (serviceEndpoint, string) {
	var ch *audioChannel
	switch which {
	case "asr":
		ch = &c.Audio.ASR
	case "tts":
		ch = &c.Audio.TTS
	default:
		return serviceEndpoint{}, i18n.Tf("cmd.config.err.audio_unknown", "which", which)
	}
	if !ch.configured() {
		return serviceEndpoint{}, i18n.T("cmd.config.err.audio_unconfigured")
	}
	ep, err := c.resolveEndpoint(ch.BaseURL, ch.APIKey, ch.Provider, deriveKeyName(which))
	if err != nil {
		return serviceEndpoint{}, err.Error()
	}
	return ep, ""
}

// configured 这一路语音是否配置过（三种形态任一成立，或填了本地模型路径）。
func (ch *audioChannel) configured() bool {
	return strings.TrimSpace(ch.Provider) != "" ||
		strings.TrimSpace(ch.BaseURL) != "" ||
		strings.TrimSpace(ch.APIKey) != "" ||
		strings.TrimSpace(ch.ModelPath) != ""
}
