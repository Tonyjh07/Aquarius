package vadsilero

// 按说话切段（D295）。上游给的是「这一块是不是语音」的概率流与开始/结束事件，
// 而采集侧拿到的是字节流。中间三件事归这里：
//
//  1. 帧 → 概率：一帧 32ms，正好是上游的推理步长，喂进去即可。
//  2. 事件 → 字节区间：上游的 At 是「流起点算起的时间」，且**已经含了首尾补偿**
//     （Segmenter 的 SpeechPad 自己会减/加），所以这里直接换算成字节，不再补第二遍。
//  3. 区间 → PCM：按区间切出来交出去，落库与转写都只认这段 PCM。
//
// 缓冲的规矩只有一条：**只要当前段还没交出，它起点之后的音频一个字节都不能丢**。
// 上游把段首往回补了 SpeechPad，所以起点可能落在几帧之前；而段尾只有在静音判够
// 之后才出现，长段因此可以横跨几分钟——缓冲跟着段一起长，静音时才裁。

import (
	"errors"
	"sync"
	"time"

	"github.com/Tonyjh07/Aquarius/internal/i18n"
	"github.com/Tonyjh07/Aquarius/internal/port"
	"github.com/vithnilica/go-silerovad"
)

// Options 分段参数（`audio.vad` 的四个键，D295）。零值取上游缺省。
type Options struct {
	// Threshold 判为「开始说话」的概率阈值，缺省 0.5。
	Threshold float32
	// NegThreshold 判为「已经说完」的收尾阈值，缺省 Threshold−0.15。
	NegThreshold float32
	// MinSilence 静音多久算这一句说完，缺省 100ms。
	MinSilence time.Duration
	// MinSpeech 短于这个长度的段丢弃，缺省 250ms。上游的 Segmenter 不做这件事
	// （`Segments()` 才做），而短促瞬态恰恰是误触发的主要来源，所以自己判。
	MinSpeech time.Duration
	// SpeechPad 段的两端各留多少余量，缺省 30ms，交给上游的 Segmenter 执行。
	SpeechPad time.Duration
}

// withDefaults 补零值，口径与上游 SegmenterOptions 一致。
func withDefaults(o Options) Options {
	if o.Threshold == 0 {
		o.Threshold = 0.5
	}
	if o.NegThreshold == 0 {
		o.NegThreshold = o.Threshold - 0.15
	}
	if o.MinSilence == 0 {
		o.MinSilence = 100 * time.Millisecond
	}
	if o.MinSpeech == 0 {
		o.MinSpeech = 250 * time.Millisecond
	}
	if o.SpeechPad == 0 {
		o.SpeechPad = 30 * time.Millisecond
	}
	return o
}

// segmenter 按说话切段（port.SpeechSegmenter 的实现）。
type segmenter struct {
	mu     sync.Mutex
	stream *silerovad.Stream
	seg    *silerovad.Segmenter
	opts   Options
	// ring 是 [total-len(ring), total) 这段连续音频；段的 PCM 从这里切。
	ring  []byte
	total int64 // 已喂入的总字节数 = 流位置
	// start 当前段的起点（绝对字节坐标，已含上游的首部补偿）；-1 = 不在段内。
	start int64
}

// errThreshold 阈值越界的报因。
func errThreshold(v float32) error {
	return errors.New(i18n.Tf("audio.vad.err_threshold", "v", v))
}

// New 创建按说话切段的分段器。参数越界时返回错误，由装配根报因。
func New(opts Options) (port.SpeechSegmenter, error) {
	opts = withDefaults(opts)
	if opts.Threshold < 0 || opts.Threshold > 1 {
		return nil, errThreshold(opts.Threshold)
	}
	if opts.NegThreshold >= opts.Threshold {
		return nil, errors.New(i18n.T("audio.vad.err_neg_threshold"))
	}
	stream, err := silerovad.NewStream(SampleRate)
	if err != nil {
		return nil, err
	}
	return &segmenter{
		stream: stream,
		seg: silerovad.NewSegmenter(silerovad.SegmenterOptions{
			Threshold:    opts.Threshold,
			NegThreshold: opts.NegThreshold,
			MinSilence:   opts.MinSilence,
			SpeechPad:    opts.SpeechPad,
		}),
		opts:  opts,
		start: -1,
	}, nil
}

// Feed 喂一帧，返回这一帧之内新结束的语音段（一帧最多结束一段）。
func (s *segmenter) Feed(frame []byte) ([]port.SpeechSegment, error) {
	if len(frame) == 0 {
		return nil, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	s.ring = append(s.ring, frame...)
	s.total += int64(len(frame))

	var out []port.SpeechSegment
	for _, p := range s.stream.PushPCM16(frame) {
		ev, ok := s.seg.Next(p)
		if !ok {
			continue
		}
		if ev.Start {
			s.start = clampStart(bytesOfDuration(ev.At), s.total)
			continue
		}
		if seg, ok := s.finish(bytesOfDuration(ev.At)); ok {
			out = append(out, seg)
		}
	}
	s.trim()
	return out, nil
}

// clampStart 段起点夹到 [0, total]：上游把首部补偿算成负数时按 0 起算。
func clampStart(at, total int64) int64 {
	if at < 0 {
		return 0
	}
	if at > total {
		return total
	}
	return at
}

// finish 收尾一段：end 之后的部分不属于这一段。太短的段丢弃。
func (s *segmenter) finish(end int64) (port.SpeechSegment, bool) {
	start := s.start
	s.start = -1
	if start < 0 {
		// 没登记过起点（理论上不会发生）：从补偿量之前开始，总比丢掉整段好。
		start = clampStart(end-bytesOfDuration(s.opts.SpeechPad), s.total)
	}
	if end > s.total {
		end = s.total
	}
	if end-start < bytesOfDuration(s.opts.MinSpeech) {
		return port.SpeechSegment{}, false // 咳嗽、敲键盘这类瞬态
	}
	pcm, ok := s.slice(start, end)
	if !ok {
		return port.SpeechSegment{}, false
	}
	return port.SpeechSegment{PCM: pcm, At: durationOfBytes(start)}, true
}

// trim 裁掉不再需要的音频：段内留住起点之后（含）的，段外只留够首部补偿的尾巴。
func (s *segmenter) trim() {
	keepFrom := s.total - bytesOfDuration(s.opts.SpeechPad) - 2*chunkBytes
	if s.start >= 0 && s.start < keepFrom {
		keepFrom = s.start
	}
	base := s.total - int64(len(s.ring))
	if keepFrom <= base {
		return
	}
	drop := keepFrom - base
	if drop >= int64(len(s.ring)) {
		return // 全部都要（不该发生：那说明 keepFrom 已越过流尾）
	}
	s.ring = append(s.ring[:0], s.ring[drop:]...)
}

// slice 取 [start,end) 的 PCM 副本。区间已不在缓冲里时返回 false。
func (s *segmenter) slice(start, end int64) ([]byte, bool) {
	base := s.total - int64(len(s.ring))
	if start < base || end > s.total || end <= start {
		return nil, false
	}
	out := make([]byte, end-start)
	copy(out, s.ring[start-base:end-base])
	return out, true
}

// Flush 收尾：仍在进行的那一段照常交出（太短的按 MinSpeech 丢弃）。
func (s *segmenter) Flush() ([]port.SpeechSegment, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	var out []port.SpeechSegment
	for _, p := range s.stream.Flush() {
		ev, ok := s.seg.Next(p)
		if !ok || ev.Start {
			continue
		}
		if seg, ok := s.finish(bytesOfDuration(ev.At)); ok {
			out = append(out, seg)
		}
	}
	// 上游补齐末块后仍可能停在「正在说」：录音就此结束，把剩下的当作这一段的结尾。
	if s.start >= 0 || s.seg.Speaking() {
		if seg, ok := s.finish(s.total); ok {
			out = append(out, seg)
		}
	}
	s.ring = s.ring[:0]
	return out, nil
}
