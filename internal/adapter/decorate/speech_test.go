package decorate

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Tonyjh07/Aquarius/internal/domain/conversation"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

// fakeTranscriber 脚本化回放：每次 Transcribe 从 errs 取一个错误，取尽后成功。
type fakeTranscriber struct {
	errs []error
	n    int
}

func (f *fakeTranscriber) Transcribe(ctx context.Context, _ conversation.BlobRef, _ string) (string, error) {
	if f.n < len(f.errs) {
		err := f.errs[f.n]
		f.n++
		return "", err
	}
	return "transcribed", nil
}

// fakeSynthesizer 脚本化回放：每次 Synthesize 从 errs 取一个错误，取尽后成功。
type fakeSynthesizer struct {
	errs []error
	n    int
}

func (f *fakeSynthesizer) Synthesize(ctx context.Context, _ port.SpeechRequest) (conversation.BlobRef, error) {
	if f.n < len(f.errs) {
		err := f.errs[f.n]
		f.n++
		return conversation.BlobRef{}, err
	}
	return conversation.BlobRef{Name: "speech-1.wav"}, nil
}

// instantSleep 退避不真等：记录每次的时长，测试据此核对指数节奏。
func instantSleep(durs *[]time.Duration) func(context.Context, time.Duration) error {
	return func(_ context.Context, d time.Duration) error {
		*durs = append(*durs, d)
		return nil
	}
}

// TestTranscriberRetry 瞬时错误退避补试至多 2 次；补试用尽后最后一次原样上抛。
func TestTranscriberRetry(t *testing.T) {
	var durs []time.Duration
	r := NewTranscriberRetry(&fakeTranscriber{
		errs: []error{port.ErrTransient, port.ErrTransient, port.ErrTransient},
	})
	r.base = time.Millisecond
	r.sleep = instantSleep(&durs)

	text, err := r.Transcribe(context.Background(), conversation.BlobRef{}, "zh")
	if err == nil || !errors.Is(err, port.ErrTransient) {
		t.Fatalf("补试用尽应原样上抛瞬时错误: %q, %v", text, err)
	}
	if len(durs) != 2 {
		t.Fatalf("补试次数 = %d, want 2", len(durs))
	}
	if durs[0] != time.Millisecond || durs[1] != 2*time.Millisecond {
		t.Fatalf("退避节奏 %v, want [1ms 2ms]（指数 ×2）", durs)
	}

	// 两次瞬时后成功。
	r2 := NewTranscriberRetry(&fakeTranscriber{errs: []error{port.ErrTransient, port.ErrTransient}})
	r2.base = time.Millisecond
	r2.sleep = instantSleep(&durs)
	text, err = r2.Transcribe(context.Background(), conversation.BlobRef{}, "zh")
	if err != nil || text != "transcribed" {
		t.Fatalf("补试内成功: %q, %v", text, err)
	}
}

// TestTranscriberRetryNonTransient 非瞬时错误立即上抛，不退避。
func TestTranscriberRetryNonTransient(t *testing.T) {
	var durs []time.Duration
	boom := errors.New("boom")
	r := NewTranscriberRetry(&fakeTranscriber{errs: []error{boom, boom}})
	r.sleep = instantSleep(&durs)
	if _, err := r.Transcribe(context.Background(), conversation.BlobRef{}, ""); !errors.Is(err, boom) || len(durs) != 0 {
		t.Fatalf("非瞬时错误应立即上抛且不退避: %v, durs=%v", err, durs)
	}
}

// TestTranscriberRetryCtxCancel 退避期间被取消：上抛最后一次识别错误，不再补试。
func TestTranscriberRetryCtxCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	r := NewTranscriberRetry(&fakeTranscriber{errs: []error{port.ErrTransient}})
	r.base = time.Hour
	r.sleep = func(_ context.Context, _ time.Duration) error {
		cancel()
		return context.Canceled
	}
	_, err := r.Transcribe(ctx, conversation.BlobRef{}, "")
	if !errors.Is(err, port.ErrTransient) {
		t.Fatalf("被取消时应上抛最后一次瞬时错误: %v", err)
	}
}

// TestSynthesizerRetry 合成端口同规则：瞬时错误补试至多 2 次，非瞬时立即上抛。
func TestSynthesizerRetry(t *testing.T) {
	var durs []time.Duration
	r := NewSynthesizerRetry(&fakeSynthesizer{errs: []error{port.ErrTransient, port.ErrTransient}})
	r.base = time.Millisecond
	r.sleep = instantSleep(&durs)
	ref, err := r.Synthesize(context.Background(), port.SpeechRequest{Text: "你好"})
	if err != nil || ref.Name != "speech-1.wav" {
		t.Fatalf("补试内成功: %q, %v", ref.Name, err)
	}
	if len(durs) != 2 {
		t.Fatalf("补试次数 = %d, want 2", len(durs))
	}

	boom := errors.New("boom")
	r2 := NewSynthesizerRetry(&fakeSynthesizer{errs: []error{boom}})
	r2.sleep = instantSleep(&durs)
	if _, err := r2.Synthesize(context.Background(), port.SpeechRequest{}); !errors.Is(err, boom) || len(durs) != 2 {
		t.Fatalf("非瞬时错误应立即上抛: %v", err)
	}
}
