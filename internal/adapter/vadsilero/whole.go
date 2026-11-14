package vadsilero

// 整段模式（关掉分段时的退路，D295）：喂进来的帧原样攒着，Flush 交出唯一一段。
//
// 做成同一个端口而不是让上层分支，是因为「关掉分段」在下游应当只是一次录音产出一段——
// 入库、转写、分片、回执全都只认「这次采集交出了几段」，不认走了哪条路。
//
// 为什么仍然限长：整段模式没有静音可判，采集时长就是缓冲上限。这条不是新口径，
// 而是沿用采集侧的硬顶（D179 的 4 分钟、约 7.7MB），超出部分丢弃并报错由采集侧先行拦。

import (
	"sync"

	"github.com/Tonyjh07/Aquarius/internal/port"
)

// wholeSegmenter 不分段：整次录音攒成一段。
type wholeSegmenter struct {
	mu   sync.Mutex
	pcm  []byte
	seen int64 // 已攒入的字节总数（换算 At 用；清零后仍从流起点算）
}

// NewWhole 创建不分段的分段器。
func NewWhole() port.SpeechSegmenter { return &wholeSegmenter{} }

// Feed 攒帧。整段模式下没有可提前结束的段，恒返回空。
func (w *wholeSegmenter) Feed(frame []byte) ([]port.SpeechSegment, error) {
	if len(frame) == 0 {
		return nil, nil
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	w.pcm = append(w.pcm, frame...)
	w.seen += int64(len(frame))
	return nil, nil
}

// Flush 交出攒到的全部音频（一段）。空流交出零段：纯静音不该产出一个空附件。
func (w *wholeSegmenter) Flush() ([]port.SpeechSegment, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if len(w.pcm) == 0 {
		return nil, nil
	}
	out := []port.SpeechSegment{{PCM: w.pcm, At: durationOfBytes(w.seen - int64(len(w.pcm)))}}
	w.pcm = nil
	return out, nil
}
