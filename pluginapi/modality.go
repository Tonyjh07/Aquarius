package pluginapi

import "context"

// modality.go 语音两端口（plugin-guide.md §4）：转写与合成，外加分段器形态。
// 模型只见转写文本；音频字节始终走附件库，不在参数里传路径。

// Transcriber 语音转写（ASR）。lang 是语种提示（"auto" / "zh" / "en"），
// 由内核配置决定，插件只当提示用。
// 兼容承诺：改签名会破坏所有声明 provides: transcribe 的插件与语音输入链。
type Transcriber interface {
	Transcribe(ctx context.Context, audio BlobRef, lang string) (string, error)
}

// SpeechRequest 一次合成请求。音色与风格都是配置项，不进模型上下文、不进会话树。
// 兼容承诺：改字段会破坏所有声明 provides: synthesize 的插件。
type SpeechRequest struct {
	Text   string // 要播报的正文
	Voice  string // 预置音色名；样本克隆模式下是样本文件路径；音色设计模式下不用
	Design string // 音色描述（音色设计模式必填）
	Style  string // 自然语言风格指令；空 = 不发
	Tags   string // 风格标签（如 "温柔 语速稍快"）：非空时拼成前缀置于正文开头
}

// Synthesizer 语音合成（TTS）。返回的音频落附件库，插件交回引用。
// 兼容承诺：改签名会破坏所有声明 provides: synthesize 的插件与播放链。
type Synthesizer interface {
	Synthesize(ctx context.Context, req SpeechRequest) (BlobRef, error)
}

// SpeechSegmenter 可选扩展：逐帧喂入、按说话切段（内核按 type assertion 探测）。
// 帧长固定（32ms 的 PCM16 单声道 16kHz），所以分段器不必自己再切帧。
// 兼容承诺：改签名会破坏实现了分段能力的插件与采集链。
type SpeechSegmenter interface {
	// Feed 喂一帧，返回这帧之后切出的段（可能为空）。
	Feed(frame []byte) ([]SpeechSegment, error)
	// Flush 收尾：把缓冲里剩下的段交出来。
	Flush() ([]SpeechSegment, error)
}
