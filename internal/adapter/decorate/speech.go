package decorate

import (
	"context"
	"errors"
	"time"

	"github.com/Tonyjh07/Aquarius/internal/domain/conversation"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

// 语音链路（识别/合成）的默认重试参数：单次请求一次响应，补试上限比 LLM 少一档
// （识别合成卡着用户在等，多等一轮不如早报）；退避与 LLM 同起点。
const (
	speechMaxRetries = 2                      // 首次之外的补试上限
	speechBaseWait   = 200 * time.Millisecond // 首次退避，之后指数 ×2
)

// TranscriberRetry 识别端口的瞬时错误重试装饰器：只重试错误链含 port.ErrTransient
// 的 Transcribe 失败（5xx/断连等，audiohttp 统一标注），限次 + 指数退避；
// ctx 取消与非瞬时错误立即上抛。
type TranscriberRetry struct {
	inner    port.Transcriber
	attempts int
	base     time.Duration
	sleep    func(ctx context.Context, d time.Duration) error
}

// NewTranscriberRetry 默认参数构造（首次 + 至多 2 次补试、200ms 起指数退避）。
func NewTranscriberRetry(inner port.Transcriber) *TranscriberRetry {
	return &TranscriberRetry{
		inner:    inner,
		attempts: speechMaxRetries + 1,
		base:     speechBaseWait,
		sleep:    sleepCtx,
	}
}

// Transcribe 见类型注释；最后一次瞬时错误原样上抛（保留错误链供上层判因）。
func (r *TranscriberRetry) Transcribe(ctx context.Context, audio conversation.BlobRef, lang string) (string, error) {
	for attempt := 1; ; attempt++ {
		text, err := r.inner.Transcribe(ctx, audio, lang)
		if err == nil {
			return text, nil
		}
		if !errors.Is(err, port.ErrTransient) || attempt >= r.attempts || ctx.Err() != nil {
			return "", err
		}
		if serr := r.sleep(ctx, r.base<<uint(attempt-1)); serr != nil {
			return "", err // 退避期间被取消：上抛最后一次识别错误
		}
	}
}

// SynthesizerRetry 合成端口的瞬时错误重试装饰器，规则与 TranscriberRetry 相同。
type SynthesizerRetry struct {
	inner    port.Synthesizer
	attempts int
	base     time.Duration
	sleep    func(ctx context.Context, d time.Duration) error
}

// NewSynthesizerRetry 默认参数构造（首次 + 至多 2 次补试、200ms 起指数退避）。
func NewSynthesizerRetry(inner port.Synthesizer) *SynthesizerRetry {
	return &SynthesizerRetry{
		inner:    inner,
		attempts: speechMaxRetries + 1,
		base:     speechBaseWait,
		sleep:    sleepCtx,
	}
}

// Synthesize 见类型注释；最后一次瞬时错误原样上抛（保留错误链供上层判因）。
func (r *SynthesizerRetry) Synthesize(ctx context.Context, req port.SpeechRequest) (conversation.BlobRef, error) {
	for attempt := 1; ; attempt++ {
		ref, err := r.inner.Synthesize(ctx, req)
		if err == nil {
			return ref, nil
		}
		if !errors.Is(err, port.ErrTransient) || attempt >= r.attempts || ctx.Err() != nil {
			return conversation.BlobRef{}, err
		}
		if serr := r.sleep(ctx, r.base<<uint(attempt-1)); serr != nil {
			return conversation.BlobRef{}, err // 退避期间被取消：上抛最后一次合成错误
		}
	}
}
