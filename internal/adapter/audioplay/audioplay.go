// Package audioplay 实现 port.AudioPlayer（D179）：把附件库里的整段音频写成临时
// 文件，用 PlaySound(SND_ASYNC) 播放；新一条开始前停上一条并清掉上一份临时文件。
// 不做流式解码与设备选择——厂商回的是 WAV，系统播放器就够。
package audioplay

import "sync"

// maxAudioBytes 单条播放的字节上限（4 分钟 16kHz WAV ≈ 7.7MB，留余量）。
const maxAudioBytes = 16 << 20

// Player 异步播放器（同一时刻一条）。
type Player struct {
	mu   sync.Mutex
	file string // 当前（或上一条）临时文件；下一条开始/Stop 时删除
}

// New 创建播放器。
func New() *Player { return &Player{} }
