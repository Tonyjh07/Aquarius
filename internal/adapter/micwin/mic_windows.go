//go:build windows

package micwin

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"runtime"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"github.com/Tonyjh07/Aquarius/internal/port"
)

var _ port.Recorder = (*Recorder)(nil)

var (
	winmm           = syscall.NewLazyDLL("winmm.dll")
	kernel          = syscall.NewLazyDLL("kernel32.dll")
	procGetNumDevs  = winmm.NewProc("waveInGetNumDevs")
	procOpen        = winmm.NewProc("waveInOpen")
	procClose       = winmm.NewProc("waveInClose")
	procPrepare     = winmm.NewProc("waveInPrepareHeader")
	procUnprepare   = winmm.NewProc("waveInUnprepareHeader")
	procAddBuffer   = winmm.NewProc("waveInAddBuffer")
	procStart       = winmm.NewProc("waveInStart")
	procStop        = winmm.NewProc("waveInStop")
	procReset       = winmm.NewProc("waveInReset")
	procCreateEvent = kernel.NewProc("CreateEventW")
	procCloseHandle = kernel.NewProc("CloseHandle")
	procWait        = kernel.NewProc("WaitForSingleObject")
)

const (
	waveMapper    = 0xFFFFFFFF
	callbackEvent = 0x00050000
	waveFormatPCM = 1
	noError       = 0
	pollInterval  = 30 * time.Millisecond
)

// maxRecordBytes 硬顶对应的裸 PCM 字节数（4 分钟）。
const maxRecordBytes = 4 * 60 * SampleRate * Channels * Bits / 8

// waveFormatEx 与 C 的 WAVEFORMATEX 同布局（18 字节）。
type waveFormatEx struct {
	formatTag      uint16
	channels       uint16
	samplesPerSec  uint32
	avgBytesPerSec uint32
	blockAlign     uint16
	bitsPerSample  uint16
	cbSize         uint16
}

// waveHdr 与 C 的 WAVEHDR 同布局（amd64：48 字节）。
type waveHdr struct {
	data          uintptr
	bufferLength  uint32
	bytesRecorded uint32
	user          uintptr
	flags         uint32
	loops         uint32
	next          uintptr
	reserved      uintptr
}

// Recorder 默认输入设备的录音器（同一时刻只允许一次录音）。
type Recorder struct {
	mu sync.Mutex
}

// New 创建录音器（不打开设备；Record 时才检查有没有设备）。
func New() *Recorder { return &Recorder{} }

// Record 采集一段音频，直到 ctx 取消（用户松手）或到达 max。ctx 取消不当作失败：
// 已录到的部分照常返回，交给上层决定要不要转写。
func (r *Recorder) Record(ctx context.Context, max time.Duration) ([]byte, time.Duration, error) {
	if ctx == nil {
		return nil, 0, errors.New("mic: nil context")
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	if n, _, _ := procGetNumDevs.Call(); n == 0 {
		return nil, 0, errors.New("mic: 没有可用的录音设备")
	}
	max = clampDuration(max)
	want := int(max.Seconds()) * SampleRate * Channels * Bits / 8
	if want <= 0 || want > maxRecordBytes {
		want = maxRecordBytes
	}

	event, _, _ := procCreateEvent.Call(0, 0, 0, 0)
	if event == 0 {
		return nil, 0, errors.New("mic: 创建录音事件失败")
	}
	defer procCloseHandle.Call(event)

	wfx := waveFormatEx{
		formatTag:      waveFormatPCM,
		channels:       Channels,
		samplesPerSec:  SampleRate,
		avgBytesPerSec: SampleRate * Channels * Bits / 8,
		blockAlign:     Channels * Bits / 8,
		bitsPerSample:  Bits,
	}
	var hwi uintptr
	if code, _, _ := procOpen.Call(
		uintptr(unsafe.Pointer(&hwi)), waveMapper,
		uintptr(unsafe.Pointer(&wfx)), event, 0, callbackEvent,
	); code != noError {
		return nil, 0, mmError(code, "打开录音设备")
	}
	defer procClose.Call(hwi)

	buf := make([]byte, want)
	hdr := waveHdr{data: uintptr(unsafe.Pointer(&buf[0])), bufferLength: uint32(len(buf))}
	if code, _, _ := procPrepare.Call(hwi, uintptr(unsafe.Pointer(&hdr)), unsafe.Sizeof(hdr)); code != noError {
		return nil, 0, mmError(code, "准备录音缓冲")
	}
	prepared := true
	defer func() {
		if prepared {
			procUnprepare.Call(hwi, uintptr(unsafe.Pointer(&hdr)), unsafe.Sizeof(hdr))
		}
	}()
	if code, _, _ := procAddBuffer.Call(hwi, uintptr(unsafe.Pointer(&hdr)), unsafe.Sizeof(hdr)); code != noError {
		return nil, 0, mmError(code, "挂载录音缓冲")
	}
	if code, _, _ := procStart.Call(hwi); code != noError {
		return nil, 0, mmError(code, "开始录音")
	}

	started := time.Now()
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()
loop:
	for {
		select {
		case <-ctx.Done():
			break loop
		case <-ticker.C:
		}
		if time.Since(started) >= max {
			break
		}
		// 缓冲录满（WOM_DONE 等价于 WIM_DATA 事件）也收工。
		if res, _, _ := procWait.Call(event, 0); res == 0 {
			break
		}
	}

	procStop.Call(hwi)
	procReset.Call(hwi) // 把缓冲标记为完成并回填 bytesRecorded
	procUnprepare.Call(hwi, uintptr(unsafe.Pointer(&hdr)), unsafe.Sizeof(hdr))
	prepared = false
	runtime.KeepAlive(&hdr)
	runtime.KeepAlive(buf)

	n := int(hdr.bytesRecorded)
	if n <= 0 {
		return nil, 0, errors.New("mic: 没有录到声音（检查麦克风是否被静音）")
	}
	if n > len(buf) {
		n = len(buf)
	}
	dur := time.Duration(n) * time.Second / (SampleRate * Channels * Bits / 8)
	return wrapWAV(buf[:n]), dur, nil
}

// wrapWAV 给裸 PCM 加上 44 字节 WAV 头（16kHz 单声道 16bit）。
func wrapWAV(pcm []byte) []byte {
	out := make([]byte, 44+len(pcm))
	copy(out[0:4], "RIFF")
	binary.LittleEndian.PutUint32(out[4:], uint32(36+len(pcm)))
	copy(out[8:12], "WAVE")
	copy(out[12:16], "fmt ")
	binary.LittleEndian.PutUint32(out[16:], 16)
	binary.LittleEndian.PutUint16(out[20:], waveFormatPCM)
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

// mmError 把 winmm 的 MMRESULT 翻成可读原因。
func mmError(code uintptr, action string) error {
	var why string
	switch code {
	case 2:
		why = "设备 ID 无效"
	case 4:
		why = "设备已被其他程序占用"
	case 5:
		why = "驱动不支持该操作"
	case 6:
		why = "没有可用的录音设备"
	case 7:
		why = "内存不足"
	case 32:
		why = "设备不支持 16kHz 单声道 16bit 采集"
	default:
		why = "未知错误"
	}
	return fmt.Errorf("mic: %s失败：%s（错误码 %d）", action, why, code)
}
