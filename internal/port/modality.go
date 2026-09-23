package port

import (
	"context"

	"github.com/Tonyjh07/Aquarius/internal/domain/conversation"
)

// Transcriber 语音转写（ASR）：whisper API / 本地引擎 / 系统听写。模型只见转写文本。
type Transcriber interface {
	Transcribe(ctx context.Context, audio conversation.BlobRef, lang string) (string, error)
}

// Synthesizer 语音合成（TTS）：云 TTS / 系统朗读。
type Synthesizer interface {
	Synthesize(ctx context.Context, text, voice string) (conversation.BlobRef, error)
}
