// Package ingestmic 实现麦克风摄取（port.Ingestor，§7.2 / D179 / D295）：
// RawInput{Kind:"mic"} → 连续采集 → 按说话分段 → 每段落成附件并转写 → 文本分片。
//
// 音频本身不随消息进树：模型只见转写文本（§4.2 口径不变）。每段仍落库，
// 便于回放与 GC（也保留了「这段文字是哪一段录音转来的」这一事实）。
//
// 一次采集 = 一条消息（D295 决策 4）：说几句就有几个文本分片，按说话顺序排，
// 而不是发几条消息。一次按键是一次意图，拆成三条等于替用户按了三次回车。
package ingestmic

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/Tonyjh07/Aquarius/internal/adapter/micwin"
	"github.com/Tonyjh07/Aquarius/internal/domain/conversation"
	"github.com/Tonyjh07/Aquarius/internal/i18n"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

var _ port.Ingestor = (*Ingestor)(nil)

// Ingestor 麦克风输入摄取器。
type Ingestor struct {
	rec   port.Recorder
	blobs port.AttachmentStore
	asr   port.Transcriber // nil = 语音识别未配置（执行时报因，入口仍可发现）
	lang  string           // input.language：auto | zh | en
	// newSegmenter 每次采集现开一个分段器（D295：检测器带跨帧隐状态，不能跨采集复用）。
	// nil = 不分段（audio.vad.enabled=false）。
	newSegmenter func() (port.SpeechSegmenter, error)
}

// New 创建摄取器。asr 可为 nil（能力不可用），record/blobs 必须有。
//
// newSegmenter 为 nil 时整段不切，行为与分段能力上线前一致。
func New(rec port.Recorder, blobs port.AttachmentStore, asr port.Transcriber, lang string,
	newSegmenter func() (port.SpeechSegmenter, error)) *Ingestor {
	lang = strings.TrimSpace(lang)
	if lang == "" {
		lang = "auto"
	}
	return &Ingestor{rec: rec, blobs: blobs, asr: asr, lang: lang, newSegmenter: newSegmenter}
}

// Accepts 只接 mic。
func (in *Ingestor) Accepts(raw port.RawInput) bool { return raw.Kind == "mic" }

// Ingest 采 → 切 → 存 → 转写，每段一个文本分片。
func (in *Ingestor) Ingest(ctx context.Context, raw port.RawInput) (port.IngestReport, error) {
	texts, recorded, err := in.Listen(ctx, time.Duration(raw.Seconds)*time.Second)
	if err != nil {
		return port.IngestReport{}, err
	}
	parts := make([]conversation.Part, 0, len(texts))
	for _, t := range texts {
		parts = append(parts, conversation.Part{Kind: conversation.PartText, Text: t})
	}
	return port.IngestReport{
		Parts: parts,
		Note: i18n.Tf("audio.mic.note_transcribed", "seconds",
			fmt.Sprintf("%.1f", recorded.Seconds())),
	}, nil
}

// Listen 采一段并逐段转写（/mic 与界面语音键共用）：不产分片、不提交，
// 只回按说话顺序排好的文本与整次录音时长。dur = 0 时由录音器取缺省值。
func (in *Ingestor) Listen(ctx context.Context, dur time.Duration) ([]string, time.Duration, error) {
	if in.asr == nil {
		return nil, 0, errors.New(i18n.T("audio.mic.err_asr_unset"))
	}
	if in.rec == nil || in.blobs == nil {
		return nil, 0, errors.New(i18n.T("audio.mic.err_not_wired"))
	}
	segs, recorded, err := in.capture(ctx, dur)
	if err != nil {
		return nil, 0, err
	}
	if len(segs) == 0 {
		return nil, 0, errors.New(i18n.T("audio.mic.err_silent"))
	}
	// 用户按停止会把上下文取消（录音器据此收工）——转写照常走完，取消 ≠ 丢弃。
	// 只有窗口关闭等真正的收尾才会让它再被打断（进程已是退出路径）。
	netCtx := context.WithoutCancel(ctx)
	texts := make([]string, 0, len(segs))
	for i, seg := range segs {
		text, err := in.transcribeSegment(netCtx, seg, i)
		if err != nil {
			return nil, 0, err
		}
		// 这一段没识别出字（噪声被切成了段）：跳过它，不因此废掉整条消息。
		if text != "" {
			texts = append(texts, text)
		}
	}
	if len(texts) == 0 {
		return nil, 0, errors.New(i18n.T("audio.mic.err_no_text"))
	}
	return texts, recorded, nil
}

// capture 采集并分段：这一段里没有语音时返回零段（纯静音不产生任何东西）。
//
// 分段与否只由 newSegmenter 决定：不分段时装配根注入 vadsilero.NewWhole，
// 所以这里只有一条采集路径——「关掉分段」在下游只是「这次只交出一段」。
func (in *Ingestor) capture(ctx context.Context, dur time.Duration) ([]port.SpeechSegment, time.Duration, error) {
	var (
		mu   sync.Mutex
		segs []port.SpeechSegment
		fed  int64 // 已喂入分段的字节数，用来算录音时长
	)
	segmenter, err := in.newSegmenter()
	if err != nil {
		return nil, 0, fmt.Errorf("%s: %w", i18n.T("audio.mic.err_segmenter"), err)
	}
	sink := func(frame []byte) (bool, error) {
		mu.Lock()
		defer mu.Unlock()
		fed += int64(len(frame))
		got, err := segmenter.Feed(frame)
		if err != nil {
			return false, err
		}
		segs = append(segs, got...)
		return true, nil
	}
	err = in.rec.Record(ctx, dur, sink)
	// ctx 取消 = 用户主动停止，不是失败；已推出的帧照常有效。
	if err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
		return nil, 0, fmt.Errorf("%s: %w", i18n.T("audio.mic.err_record"), err)
	}
	tail, ferr := segmenter.Flush()
	if ferr != nil {
		return nil, 0, fmt.Errorf("%s: %w", i18n.T("audio.mic.err_segmenter"), ferr)
	}
	mu.Lock()
	defer mu.Unlock()
	segs = append(segs, tail...)
	return coalesce(segs), in.recorded(fed), nil
}

// coalesce 丢掉空段（分段器不该产出空段，但上游行为变化时不该由入库来暴露）。
func coalesce(segs []port.SpeechSegment) []port.SpeechSegment {
	out := segs[:0]
	for _, s := range segs {
		if len(s.PCM) > 0 {
			out = append(out, s)
		}
	}
	return out
}

// recorded 按字节数换算录音时长。
func (in *Ingestor) recorded(n int64) time.Duration {
	return time.Duration(n * int64(time.Second) /
		int64(micwin.SampleRate*micwin.Channels*micwin.Bits/8))
}

// transcribeSegment 把一段录音落库并转写。空文本（这段没识别出字）跳过而不是失败：
// 多段里有一段是噪音时，不该让整条消息失败。
func (in *Ingestor) transcribeSegment(ctx context.Context, seg port.SpeechSegment, idx int) (string, error) {
	wav := micwin.WrapWAV(seg.PCM)
	name := fmt.Sprintf("mic-%d-%d.wav", time.Now().UnixMilli(), idx)
	ref, err := in.blobs.Put(ctx, bytes.NewReader(wav), "audio/wav", name)
	if err != nil {
		return "", fmt.Errorf("%s: %w", i18n.T("audio.mic.err_save"), err)
	}
	text, err := in.asr.Transcribe(ctx, ref, in.lang)
	if err != nil {
		return "", fmt.Errorf("%s: %w", i18n.T("audio.mic.err_transcribe"), err)
	}
	return strings.TrimSpace(text), nil
}
