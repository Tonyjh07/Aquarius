package ingestmic

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/Tonyjh07/Aquarius/internal/adapter/blobfs"
	"github.com/Tonyjh07/Aquarius/internal/domain/conversation"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

type fakeRecorder struct {
	wav []byte
	dur time.Duration
	err error
	got time.Duration
}

func (f *fakeRecorder) Record(_ context.Context, max time.Duration) ([]byte, time.Duration, error) {
	f.got = max
	return f.wav, f.dur, f.err
}

type fakeASR struct {
	text string
	err  error
	ref  conversation.BlobRef
	lang string
}

func (f *fakeASR) Transcribe(_ context.Context, ref conversation.BlobRef, lang string) (string, error) {
	f.ref, f.lang = ref, lang
	return f.text, f.err
}

func newBlobs(t *testing.T) *blobfs.Store {
	t.Helper()
	b, err := blobfs.New(t.TempDir())
	if err != nil {
		t.Fatalf("blobfs: %v", err)
	}
	return b
}

// TestIngestRecordsStoresAndTranscribes 录音落库后交给转写，音频不进分片。
func TestIngestRecordsStoresAndTranscribes(t *testing.T) {
	blobs := newBlobs(t)
	rec := &fakeRecorder{wav: []byte("WAV-BYTES"), dur: 2 * time.Second}
	asr := &fakeASR{text: "  你好世界  "}
	in := New(rec, blobs, asr, "zh")

	if !in.Accepts(port.RawInput{Kind: "mic"}) || in.Accepts(port.RawInput{Kind: "text"}) {
		t.Fatal("Accepts 只应接 mic")
	}
	rep, err := in.Ingest(context.Background(), port.RawInput{Kind: "mic", Seconds: 30})
	if err != nil {
		t.Fatalf("Ingest: %v", err)
	}
	if rec.got != 30*time.Second {
		t.Fatalf("录音时长 = %v, want 30s", rec.got)
	}
	if len(rep.Parts) != 1 || rep.Parts[0].Kind != conversation.PartText || rep.Parts[0].Text != "你好世界" {
		t.Fatalf("分片 = %+v", rep.Parts)
	}
	if !strings.Contains(rep.Note, "已由语音转写") {
		t.Fatalf("备注 = %q", rep.Note)
	}
	if asr.lang != "zh" {
		t.Fatalf("语种 = %q", asr.lang)
	}
	rc, err := blobs.Get(context.Background(), asr.ref)
	if err != nil {
		t.Fatalf("音频未落库: %v", err)
	}
	defer rc.Close()
	back, _ := io.ReadAll(rc)
	if string(back) != "WAV-BYTES" {
		t.Fatalf("落库音频 = %q", back)
	}
}

// TestIngestWithoutASRReportsReason 语音未配置时给出可操作的报因。
func TestIngestWithoutASRReportsReason(t *testing.T) {
	in := New(&fakeRecorder{}, newBlobs(t), nil, "auto")
	_, err := in.Ingest(context.Background(), port.RawInput{Kind: "mic"})
	if err == nil || !strings.Contains(err.Error(), "语音识别未配置") {
		t.Fatalf("应报未配置，得到 %v", err)
	}
}

// TestIngestRecordingFailure 录音失败不继续转写。
func TestIngestRecordingFailure(t *testing.T) {
	in := New(&fakeRecorder{err: errors.New("没有设备")}, newBlobs(t), &fakeASR{text: "x"}, "auto")
	_, err := in.Ingest(context.Background(), port.RawInput{Kind: "mic"})
	if err == nil || !strings.Contains(err.Error(), "录音失败") {
		t.Fatalf("应报录音失败，得到 %v", err)
	}
}

// TestIngestEmptyTranscript 转写为空时明确报因。
func TestIngestEmptyTranscript(t *testing.T) {
	in := New(&fakeRecorder{wav: []byte("x"), dur: time.Second}, newBlobs(t), &fakeASR{text: " "}, "auto")
	_, err := in.Ingest(context.Background(), port.RawInput{Kind: "mic"})
	if err == nil || !strings.Contains(err.Error(), "没有识别出文字") {
		t.Fatalf("应报空转写，得到 %v", err)
	}
}
