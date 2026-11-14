// Package vadsilero 实现 port.SpeechSegmenter（D295）：按说话把采集流切成独立的语音段。
//
// 底层是 Silero VAD v6 的纯 Go 实现（权重内嵌、无 cgo、无 onnxruntime），
// 这里只负责三件上层的事：把 16kHz 的定长帧喂进去、把「开始/结束」事件换算成
// 采集流里的字节位置、把补偿要用的那段已经流过去的音频留住。
//
// 两个实现共用这一个端口：`New` 按说话切段，`NewWhole` 整段不切（关掉分段时的退路）。
// 上层只认「这次采集交出了几段」，不认走了哪条。
package vadsilero

import "time"

// SampleRate / Channels / Bits 采集格式（D179 口径七）：与 micwin 同一套数。
// 检测器只吃 16kHz 单声道，换格式要连检测器一起换。
const (
	SampleRate = 16000
	Channels   = 1
	Bits       = 16
)

// bytesPerSecond 裸 PCM16 单声道 16kHz 每秒字节数（32000）。
const bytesPerSecond = SampleRate * Channels * Bits / 8

// chunkBytes 一个推理步长对应的字节数：32ms × 32000 B/s = 1024，与 port.FrameBytes 同值。
// 不能写成 bytesPerSecond/32——一秒里有 31.25 个 32ms，取整会少 24 字节、样本数对不上 512。
const chunkBytes = bytesPerSecond * 32 / 1000

// durationOfBytes 裸 PCM16 单声道 16kHz 的字节数 → 时长。
func durationOfBytes(n int64) time.Duration {
	return time.Duration(n * int64(time.Second) / int64(bytesPerSecond))
}

// bytesOfDuration 时长 → 字节数（向下取整，宁可少切不可越界）。
func bytesOfDuration(d time.Duration) int64 {
	return int64(d) * int64(bytesPerSecond) / int64(time.Second)
}
