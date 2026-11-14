// Package micwin 实现 port.Recorder（D179/D295）：waveIn 系列 syscall 采集默认输入设备，
// 按 32ms 一帧连续推出 16kHz 单声道 16bit 的裸 PCM。纯 Go、无 cgo；不选设备、不做回声消除。
//
// 分段不在这里：采集只负责「把麦克风里的东西原样推出去」，切不切、怎么切由
// port.SpeechSegmenter 的实现决定（D295）。这样换分段器不必碰平台代码，
// 而平台代码也不必理解语音。
package micwin

import (
	"encoding/binary"
	"time"
)

// SampleRate / Channels / Bits 采集格式（D179 口径七）。
const (
	SampleRate = 16000
	Channels   = 1
	Bits       = 16
)

// MaxDuration 产品侧硬顶：MiMo 的 Base64 体积上限 10MB 反推约 4 分钟
// （16kHz 单声道 16bit ≈ 32KB/s）。命令/界面最多录到这里。
const MaxDuration = 4 * time.Minute

// DefaultDuration /mic 与语音键的缺省录音时长。
const DefaultDuration = 60 * time.Second

// clampDuration 把请求时长夹到 [1s, MaxDuration]；0 = 缺省。
func clampDuration(d time.Duration) time.Duration {
	if d <= 0 {
		return DefaultDuration
	}
	if d > MaxDuration {
		return MaxDuration
	}
	return d
}

// WrapWAV 给裸 PCM 加上 44 字节 WAV 头（16kHz 单声道 16bit）。
//
// 分段结果要落成独立附件，而附件与转写都按 WAV 走（D179 不动），所以封装这一步
// 从采集器搬到这里，由摄取侧对每一段各调一次。
func WrapWAV(pcm []byte) []byte {
	out := make([]byte, 44+len(pcm))
	copy(out[0:4], "RIFF")
	binary.LittleEndian.PutUint32(out[4:], uint32(36+len(pcm)))
	copy(out[8:12], "WAVE")
	copy(out[12:16], "fmt ")
	binary.LittleEndian.PutUint32(out[16:], 16)
	binary.LittleEndian.PutUint16(out[20:], 1) // PCM
	binary.LittleEndian.PutUint16(out[22:], Channels)
	binary.LittleEndian.PutUint32(out[24:], SampleRate)
	binary.LittleEndian.PutUint32(out[28:], SampleRate*Channels*Bits/8)
	binary.LittleEndian.PutUint16(out[32:], Channels*Bits/8)
	binary.LittleEndian.PutUint16(out[34:], Bits)
	copy(out[36:40], "data")
	binary.LittleEndian.PutUint32(out[40:], uint32(len(pcm)))
	copy(out[44:], pcm)
	return out
}
