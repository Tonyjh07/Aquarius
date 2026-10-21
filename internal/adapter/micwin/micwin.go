// Package micwin 实现 port.Recorder（D179）：waveIn 系列 syscall 采集默认输入设备，
// 输出 16kHz 单声道 16bit WAV。纯 Go、无 cgo；不选设备、不做 VAD/回声消除——
// 首发要的是「按一下能说话」。
package micwin

import "time"

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
