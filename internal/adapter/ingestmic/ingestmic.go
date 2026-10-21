// Package ingestmic 实现麦克风摄取（port.Ingestor，DESIGN §7.2 / D179）：
// RawInput{Kind:"mic"} → 录音（WAV）→ 附件库 → 转写 → 文本分片。
//
// 音频本身不随消息进树：模型只见转写文本（§4.2 口径不变）。附件仍落库，
// 便于回放与 GC（也保留了「这段文字是哪段录音转来的」这一事实）。
package ingestmic

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Tonyjh07/Aquarius/internal/adapter/micwin"
	"github.com/Tonyjh07/Aquarius/internal/domain/conversation"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

var _ port.Ingestor = (*Ingestor)(nil)

// Ingestor 麦克风输入摄取器。
type Ingestor struct {
	rec   port.Recorder
	blobs port.AttachmentStore
	asr   port.Transcriber // nil = 语音识别未配置（执行时报因，入口仍可发现）
	lang  string           // input.language：auto | zh | en
}

// New 创建摄取器。asr 可为 nil（能力不可用），record/blobs 必须有。
func New(rec port.Recorder, blobs port.AttachmentStore, asr port.Transcriber, lang string) *Ingestor {
	lang = strings.TrimSpace(lang)
	if lang == "" {
		lang = "auto"
	}
	return &Ingestor{rec: rec, blobs: blobs, asr: asr, lang: lang}
}

// Accepts 只接 mic。
func (in *Ingestor) Accepts(raw port.RawInput) bool { return raw.Kind == "mic" }

// Ingest 录 → 存 → 转写。
func (in *Ingestor) Ingest(ctx context.Context, raw port.RawInput) (port.IngestReport, error) {
	text, recorded, err := in.Listen(ctx, time.Duration(raw.Seconds)*time.Second)
	if err != nil {
		return port.IngestReport{}, err
	}
	return port.IngestReport{
		Parts: []conversation.Part{{Kind: conversation.PartText, Text: text}},
		Note:  fmt.Sprintf("已由语音转写（%.1f 秒录音）", recorded.Seconds()),
	}, nil
}

// Listen 录一段并转写（/mic 与界面语音键共用）：不产分片、不提交，只回文本与
// 实际录音时长。dur = 0 时由录音器取缺省值。
func (in *Ingestor) Listen(ctx context.Context, dur time.Duration) (string, time.Duration, error) {
	if in.asr == nil {
		return "", 0, errors.New("语音识别未配置：请在设置「语音」页填写语音服务")
	}
	if in.rec == nil || in.blobs == nil {
		return "", 0, errors.New("语音输入未装配（缺少录音器或附件库）")
	}
	wav, recorded, err := in.rec.Record(ctx, dur)
	if err != nil {
		return "", 0, fmt.Errorf("录音失败: %w", err)
	}
	if len(wav) == 0 {
		return "", 0, errors.New("没有录到声音")
	}
	// 用户按停止会把上下文取消（录音器据此收工）——转写照常走完，取消 ≠ 丢弃。
	// 只有窗口关闭等真正的收尾才会让它再被打断（进程已是退出路径）。
	netCtx := context.WithoutCancel(ctx)
	name := fmt.Sprintf("mic-%d.wav", time.Now().UnixMilli())
	ref, err := in.blobs.Put(netCtx, bytes.NewReader(wav), "audio/wav", name)
	if err != nil {
		return "", 0, fmt.Errorf("保存录音: %w", err)
	}
	text, err := in.asr.Transcribe(netCtx, ref, in.lang)
	if err != nil {
		return "", 0, fmt.Errorf("语音转写失败: %w", err)
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return "", 0, errors.New("没有识别出文字，请再说一次")
	}
	if recorded <= 0 {
		recorded = time.Duration(len(wav)) * time.Second / (micwin.SampleRate * micwin.Channels * micwin.Bits / 8)
	}
	return text, recorded, nil
}
