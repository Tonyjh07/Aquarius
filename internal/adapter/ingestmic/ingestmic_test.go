package ingestmic

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/Tonyjh07/Aquarius/internal/adapter/blobfs"
	"github.com/Tonyjh07/Aquarius/internal/adapter/micwin"
	"github.com/Tonyjh07/Aquarius/internal/adapter/vadsilero"
	"github.com/Tonyjh07/Aquarius/internal/domain/conversation"
	"github.com/Tonyjh07/Aquarius/internal/i18n"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

// fakeRecorder 假采集器：把预设的帧按序推给 sink，可指定在推完之后返回的错误。
//
// 它还负责钉住一条语义（D295）：ctx 取消 = 用户主动停止，不是失败——
// 假采集器在取消时照常把已推出的帧留在链路上，由上层决定还要不要。
type fakeRecorder struct {
	frames  [][]byte
	err     error // 推完之后返回的错误
	gotDur  time.Duration
	nFrames int
	// stopAfter 推这么多帧后模拟「用户按了停止」，仍以 nil 收尾。
	stopAfter int
	seenCtx   context.Context
}

func (f *fakeRecorder) Record(ctx context.Context, max time.Duration, sink port.FrameSink) error {
	f.gotDur = max
	f.seenCtx = ctx
	for i, fr := range f.frames {
		if f.stopAfter > 0 && i >= f.stopAfter {
			return nil // 用户停止：已推出的帧照常有效
		}
		more, err := sink(fr)
		if err != nil {
			return err
		}
		f.nFrames++
		if !more {
			return nil
		}
	}
	return f.err
}

type fakeASR struct {
	text  string
	texts []string // 非空时按调用序逐个返回（多段转写）
	err   error
	refs  []conversation.BlobRef
	langs []string
}

func (f *fakeASR) Transcribe(_ context.Context, ref conversation.BlobRef, lang string) (string, error) {
	f.refs = append(f.refs, ref)
	f.langs = append(f.langs, lang)
	if f.err != nil {
		return "", f.err
	}
	if len(f.texts) > 0 {
		i := len(f.refs) - 1
		if i < len(f.texts) {
			return f.texts[i], nil
		}
		return "", nil
	}
	return f.text, nil
}

func newBlobs(t *testing.T) *blobfs.Store {
	t.Helper()
	b, err := blobfs.New(t.TempDir())
	if err != nil {
		t.Fatalf("blobfs: %v", err)
	}
	return b
}

// frames 生成 n 个定长帧，每帧内容各不相同（便于断言顺序）。
func frames(n int) [][]byte {
	out := make([][]byte, n)
	for i := range out {
		fr := make([]byte, port.FrameBytes)
		for j := range fr {
			fr[j] = byte(i)
		}
		out[i] = fr
	}
	return out
}

// whole 整段分段器工厂（关掉分段时的行为）。
func whole() func() (port.SpeechSegmenter, error) {
	return func() (port.SpeechSegmenter, error) { return vadsilero.NewWhole(), nil }
}

// TestIngestRecordsStoresAndTranscribes 录音落库后交给转写，音频不进分片。
func TestIngestRecordsStoresAndTranscribes(t *testing.T) {
	t.Parallel()
	blobs := newBlobs(t)
	rec := &fakeRecorder{frames: frames(8)}
	asr := &fakeASR{text: "  你好世界  "}
	in := New(rec, blobs, asr, "zh", whole())

	if !in.Accepts(port.RawInput{Kind: "mic"}) || in.Accepts(port.RawInput{Kind: "text"}) {
		t.Fatal("Accepts 只应接 mic")
	}
	rep, err := in.Ingest(context.Background(), port.RawInput{Kind: "mic", Seconds: 30})
	if err != nil {
		t.Fatalf("Ingest: %v", err)
	}
	if rec.gotDur != 30*time.Second {
		t.Fatalf("录音时长 = %v, want 30s", rec.gotDur)
	}
	if len(rep.Parts) != 1 || rep.Parts[0].Kind != conversation.PartText || rep.Parts[0].Text != "你好世界" {
		t.Fatalf("分片 = %+v", rep.Parts)
	}
	// 备注 = 文案表模板 + 实际秒数（生产按 %.1f 格式化）。
	wantNote := i18n.Tf("audio.mic.note_transcribed", "seconds", "0.3")
	if rep.Note != wantNote {
		t.Fatalf("备注 = %q, want %q", rep.Note, wantNote)
	}
}

// TestIngestStoresWAVPerSegment 每段各落一个 WAV 附件（D295：分段结果仍是独立 WAV）。
func TestIngestStoresWAVPerSegment(t *testing.T) {
	t.Parallel()
	blobs := newBlobs(t)
	rec := &fakeRecorder{frames: frames(8)}
	asr := &fakeASR{text: "x"}
	in := New(rec, blobs, asr, "auto", whole())

	rep, err := in.Ingest(context.Background(), port.RawInput{Kind: "mic"})
	if err != nil {
		t.Fatalf("Ingest: %v", err)
	}
	if len(asr.refs) != 1 {
		t.Fatalf("整段模式应转写一次，实际 %d 次", len(asr.refs))
	}
	rc, err := blobs.Get(context.Background(), asr.refs[0])
	if err != nil {
		t.Fatalf("取附件: %v", err)
	}
	defer rc.Close()
	back, _ := io.ReadAll(rc)
	if len(back) != 44+8*port.FrameBytes {
		t.Fatalf("落库音频 = %d 字节，want WAV 头 + 8 帧 = %d", len(back), 44+8*port.FrameBytes)
	}
	if string(back[0:4]) != "RIFF" {
		t.Fatalf("落库音频不是 WAV：%q", back[0:4])
	}
	_ = rep
}

// TestMultiSegmentYieldsMultiParts 说几段就几个分片，顺序与段序一致（D295 决策 4）。
func TestMultiSegmentYieldsMultiParts(t *testing.T) {
	t.Parallel()
	blobs := newBlobs(t)
	rec := &fakeRecorder{frames: frames(6)}
	asr := &fakeASR{texts: []string{"第一句", "第二句", "第三句"}}
	// 固定三段的分段器：每喂两帧交出一段（足以验证「多段 → 多分片」的接线）。
	in := New(rec, blobs, asr, "auto", func() (port.SpeechSegmenter, error) {
		return &fixedSegmenter{per: 2, frame: port.FrameBytes, total: 6}, nil
	})

	rep, err := in.Ingest(context.Background(), port.RawInput{Kind: "mic"})
	if err != nil {
		t.Fatalf("Ingest: %v", err)
	}
	if len(rep.Parts) != 3 {
		t.Fatalf("三段录音应得三个分片，得到 %d 个", len(rep.Parts))
	}
	want := []string{"第一句", "第二句", "第三句"}
	for i, w := range want {
		if rep.Parts[i].Text != w {
			t.Fatalf("第 %d 个分片 = %q, want %q", i, rep.Parts[i].Text, w)
		}
	}
	if len(asr.refs) != 3 {
		t.Fatalf("三段应各转写一次，实际 %d 次", len(asr.refs))
	}
}

// TestPureSilenceYieldsNothing 纯静音：不建附件、不转写、不算成功（D295 最实在的收益）。
func TestPureSilenceYieldsNothing(t *testing.T) {
	t.Parallel()
	blobs := newBlobs(t)
	rec := &fakeRecorder{frames: frames(8)}
	asr := &fakeASR{text: "不该被调用"}
	in := New(rec, blobs, asr, "auto", func() (port.SpeechSegmenter, error) {
		return &fixedSegmenter{per: 0, frame: port.FrameBytes, total: 8}, nil // 永不交段
	})

	_, err := in.Ingest(context.Background(), port.RawInput{Kind: "mic"})
	if err == nil || err.Error() != i18n.T("audio.mic.err_silent") {
		t.Fatalf("纯静音应报「没有录到声音」，得到 %v", err)
	}
	if len(asr.refs) != 0 {
		t.Fatalf("纯静音不该发起转写，实际 %d 次", len(asr.refs))
	}
}

// TestIngestWithoutASRReportsReason 语音未配置时给出可操作的报因。
func TestIngestWithoutASRReportsReason(t *testing.T) {
	t.Parallel()
	in := New(&fakeRecorder{}, newBlobs(t), nil, "auto", whole())
	_, err := in.Ingest(context.Background(), port.RawInput{Kind: "mic"})
	if err == nil || err.Error() != i18n.T("audio.mic.err_asr_unset") {
		t.Fatalf("应报未配置，得到 %v", err)
	}
}

// TestIngestRecordingFailure 录音失败不继续转写。
func TestIngestRecordingFailure(t *testing.T) {
	t.Parallel()
	in := New(&fakeRecorder{err: errors.New("没有设备")}, newBlobs(t), &fakeASR{text: "x"}, "auto", whole())
	_, err := in.Ingest(context.Background(), port.RawInput{Kind: "mic"})
	// 报因 = 「录音失败」+ 包装的原始错误（测试喂的「没有设备」原样带回）。
	wantErr := i18n.T("audio.mic.err_record") + ": 没有设备"
	if err == nil || err.Error() != wantErr {
		t.Fatalf("应报录音失败，得到 %v", err)
	}
}

// TestIngestUserStopIsNotFailure 用户按停止（采集器照常返回）不算失败：
// 已录到的音频照常转写。这条语义不能丢（D295）。
func TestIngestUserStopIsNotFailure(t *testing.T) {
	t.Parallel()
	blobs := newBlobs(t)
	rec := &fakeRecorder{frames: frames(8), stopAfter: 4}
	asr := &fakeASR{text: "半句话"}
	in := New(rec, blobs, asr, "auto", whole())

	rep, err := in.Ingest(context.Background(), port.RawInput{Kind: "mic"})
	if err != nil {
		t.Fatalf("用户停止不该报错，得到 %v", err)
	}
	if len(rep.Parts) != 1 || rep.Parts[0].Text != "半句话" {
		t.Fatalf("已录部分应照常转写，分片 = %+v", rep.Parts)
	}
}

// TestSegmenterBuildFailureReported 分段器建不起来时报因，不静默退化。
func TestSegmenterBuildFailureReported(t *testing.T) {
	t.Parallel()
	in := New(&fakeRecorder{frames: frames(2)}, newBlobs(t), &fakeASR{text: "x"}, "auto",
		func() (port.SpeechSegmenter, error) { return nil, errors.New("阈值越界") })
	_, err := in.Ingest(context.Background(), port.RawInput{Kind: "mic"})
	if err == nil || err.Error() != i18n.T("audio.mic.err_segmenter")+": 阈值越界" {
		t.Fatalf("应报分段失败，得到 %v", err)
	}
}

// TestEmptyTranscript 转写为空时明确报因。
func TestEmptyTranscript(t *testing.T) {
	t.Parallel()
	in := New(&fakeRecorder{frames: frames(4)}, newBlobs(t), &fakeASR{text: " "}, "auto", whole())
	_, err := in.Ingest(context.Background(), port.RawInput{Kind: "mic"})
	if err == nil || err.Error() != i18n.T("audio.mic.err_no_text") {
		t.Fatalf("应报没有文字，得到 %v", err)
	}
}

// TestNoDevice 没有录音器/附件库时立刻报因。
func TestNoDevice(t *testing.T) {
	t.Parallel()
	in := New(nil, nil, &fakeASR{text: "x"}, "auto", whole())
	_, err := in.Ingest(context.Background(), port.RawInput{Kind: "mic"})
	if err == nil || err.Error() != i18n.T("audio.mic.err_not_wired") {
		t.Fatalf("应报未接线，得到 %v", err)
	}
}

// TestRecordedDurationFromBytes 采集器不报时长时按字节数换算。
func TestRecordedDurationFromBytes(t *testing.T) {
	t.Parallel()
	in := New(&fakeRecorder{frames: frames(8)}, newBlobs(t), &fakeASR{text: "x"}, "auto", whole())
	rep, err := in.Ingest(context.Background(), port.RawInput{Kind: "mic"})
	if err != nil {
		t.Fatalf("Ingest: %v", err)
	}
	// 8 帧 × 32ms = 256ms；备注里按 %.1f 写。
	want := i18n.Tf("audio.mic.note_transcribed", "seconds", "0.3")
	if rep.Note != want {
		t.Fatalf("备注 = %q, want %q", rep.Note, want)
	}
}

// fixedSegmenter 按固定帧数切段：每 per 帧交出一段（per=0 = 永不交段）。
// 它只把帧原样拼起来，用来验证「多段 → 多分片」的接线，不参与 VAD 判定。
type fixedSegmenter struct {
	per   int
	frame int
	total int
	seen  int
	buf   []byte
	out   []port.SpeechSegment
}

func (f *fixedSegmenter) Feed(frame []byte) ([]port.SpeechSegment, error) {
	f.seen += len(frame)
	f.buf = append(f.buf, frame...)
	if f.per == 0 || len(f.buf) < f.per*f.frame {
		return nil, nil
	}
	seg := port.SpeechSegment{PCM: f.buf, At: time.Duration(f.seen-len(f.buf)) * time.Second / 32000}
	f.buf = nil
	return []port.SpeechSegment{seg}, nil
}

func (f *fixedSegmenter) Flush() ([]port.SpeechSegment, error) {
	out := f.out
	f.out = nil
	return out, nil
}

// 编译期确认整段实现满足端口（也钉住 micwin.WrapWAV 的存在）。
var _ = micwin.WrapWAV
