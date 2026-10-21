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

// Recorder 麦克风采集（D179）：录一段 16kHz 单声道 16bit 的 WAV。
// ctx 取消 = 用户主动停止，已录到的音频照常返回（不当作失败）。
type Recorder interface {
	Record(ctx context.Context, max time.Duration) (wav []byte, dur time.Duration, err error)
}

// AudioPlayer 播放合成好的语音（D179：整段合成后异步播放；新一条开始前停上一条）。
// audio 只是元信息，字节从 r 读；实现负责落临时文件并异步播放，Play 立即返回。
type AudioPlayer interface {
	Play(ctx context.Context, audio conversation.BlobRef, r io.Reader) error
	Stop()
}
