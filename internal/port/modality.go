package port

import (
	"context"
	"io"
	"time"

	"github.com/Tonyjh07/Aquarius/internal/domain/conversation"
)

// Transcriber 语音转写（ASR）：whisper API / 本地引擎 / 系统听写。模型只见转写文本。
// lang 是语种提示（auto | zh | en），由配置 input.language 决定。
type Transcriber interface {
	Transcribe(ctx context.Context, audio conversation.BlobRef, lang string) (string, error)
}

// SpeechRequest 一次合成请求（D179）：正文之外，风格与音色都是配置项，
// 不进模型上下文、不进会话树。字段与 `audio.tts` 一一对应，适配器按模型决定
// 用哪一个（预设音色 / 音色描述 / 样本文件）。
type SpeechRequest struct {
	Text   string // 要播报的正文（进合成请求的 assistant 消息）
	Voice  string // 预置音色名；voiceclone 模式是样本文件绝对路径；voicedesign 模式不用
	Design string // voicedesign 模式的音色描述（该模型必填）
	Style  string // 自然语言风格指令（进 user 消息；空 = 不发）
	Tags   string // 风格标签（如 "温柔 语速稍快"）：非空时拼成 "(标签)" 前缀置于正文开头
}

// Synthesizer 语音合成（TTS）：云 TTS（厂商适配器）/ 系统朗读（后续）。
type Synthesizer interface {
	Synthesize(ctx context.Context, req SpeechRequest) (conversation.BlobRef, error)
}

// FrameBytes 一帧音频的字节数（D295）：16kHz 单声道 16bit 下 32ms = 512 采样 = 1024 字节。
// 与语音活动检测的推理步长取同一个数，分段器因此不必自己再切帧。
const FrameBytes = 1024

// FrameSink 接收采集器推来的音频帧（D295）。pcm 是裸 PCM16 小端、单声道、16kHz，
// 长度恒为 FrameBytes；调用方不得保留切片，需要留存请自行拷贝。
//
// 返回 false = 调用方已不需要后续帧（例如已攒够想要的段），采集器据此提前收工。
// 返回错误 = 中止采集，错误原样上抛给 Record 的调用者。
type FrameSink func(pcm []byte) (bool, error)

// Recorder 麦克风采集（D179/D295）：连续推 16kHz 单声道 16bit 的 PCM 帧。
//
// ctx 取消 = 用户主动停止，此时已推出去的帧照常有效、Record 返回 nil 而不是错误；
// 停止不是失败，这是这条链路上唯一不能丢的语义（用户按停止的前提是他刚说完话）。
type Recorder interface {
	Record(ctx context.Context, max time.Duration, sink FrameSink) error
}

// SpeechSegment 一段语音（D295）：裸 PCM16 小端、单声道、16kHz。
// At 是这段在整次录音里的起点，只用于展示与排错，不进模型上下文。
type SpeechSegment struct {
	PCM []byte
	At  time.Duration
}

// SpeechSegmenter 语音分段（D295）：逐帧喂入，按说话切段。
//
// Feed 交出**在这一帧之内新结束的**语音段（一帧最多结束一段，也可能一段都不结束），
// Flush 收尾交出仍在进行的那一段（太短的段按 MinSpeech 丢弃）。
//
// 一个采集流一个实例：检测器自带跨帧的隐状态（上下文窗口与 LSTM 记忆），
// 两路采集共用一个实例等于让两段音频互相污染。实现不保证并发安全。
type SpeechSegmenter interface {
	Feed(frame []byte) ([]SpeechSegment, error)
	Flush() ([]SpeechSegment, error)
}

// AudioPlayer 播放合成好的语音（D179：整段合成后异步播放；新一条开始前停上一条）。
// audio 只是元信息，字节从 r 读；实现负责落临时文件并异步播放，Play 立即返回。
type AudioPlayer interface {
	Play(ctx context.Context, audio conversation.BlobRef, r io.Reader) error
	Stop()
}
