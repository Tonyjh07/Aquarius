//go:build windows

package micwin

import (
	"context"
	"errors"
	"runtime"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"github.com/Tonyjh07/Aquarius/internal/i18n"
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

// bufferCount 同时在飞的缓冲块数。waveIn 是环形队列：一块满了发事件、自动接上下一块，
// 采集器在事件到达时把这一块读出来推给调用方。四块 = 约 128ms 的余量，
// 足够盖住一次 GC 停顿而不丢音频。
const bufferCount = 4

// bufferBytes 单块长度：一帧（32ms）的整数倍，帧长与 VAD 的推理步长同值。
const bufferBytes = port.FrameBytes * 4

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

// Record 连续采集，把音频按 port.FrameBytes 一帧推给 sink，直到 ctx 取消（用户松手）、
// 到达 max、或 sink 表示不再需要。
//
// ctx 取消不当作失败：已推出去的帧照常有效，返回 nil。停止是用户的正常操作，
// 而他把停止按下去的前提正是刚说完话——这条语义丢了，按停止就等于丢半句话。
func (r *Recorder) Record(ctx context.Context, max time.Duration, sink port.FrameSink) error {
	if ctx == nil {
		return errors.New(i18n.T("audio.rec.err_nil_context"))
	}
	if sink == nil {
		return errors.New(i18n.T("audio.rec.err_nil_sink"))
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	if n, _, _ := procGetNumDevs.Call(); n == 0 {
		return errors.New(i18n.T("audio.rec.err_no_device"))
	}
	max = clampDuration(max)

	event, _, _ := procCreateEvent.Call(0, 0, 0, 0)
	if event == 0 {
		return errors.New(i18n.T("audio.rec.err_create_event"))
	}
	defer procCloseHandle.Call(event)

	hwi, bufs, hdrs, err := openSession(event)
	if err != nil {
		return err
	}
	defer procClose.Call(hwi)
	defer unprepareAll(hwi, hdrs)
	defer runtime.KeepAlive(bufs)

	if code, _, _ := procStart.Call(hwi); code != noError {
		return mmError(code, i18n.T("audio.rec.action.start"))
	}

	started := time.Now()
	written := 0
	ended := false
	// pending 攒够一帧就推：waveIn 的块长度是帧的整数倍，但块与块之间可能只录到一半，
	// 由这里补齐再推，调用方因此永远拿到定长帧。
	pending := make([]byte, 0, port.FrameBytes+bufferBytes)
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			goto done
		case <-ticker.C:
		}
		if time.Since(started) >= max || written >= maxRecordBytes {
			break
		}
		// 事件到达 = 至少有一块录满了（WIM_DATA）；没到达就继续等。
		if res, _, _ := procWait.Call(event, 0); res != 0 {
			continue
		}
		// 把已完成、还没取走的块全部取走并重新挂回队列。一次事件只报一块，
		// 但被推迟处理时队列里可能已经积了好几块。
		pending = drainBuffers(hwi, hdrs, bufs, pending)
		var err error
		pending, written, ended, err = pushFrames(pending, written, sink)
		if err != nil {
			procStop.Call(hwi)
			return err
		}
		if ended {
			procStop.Call(hwi)
			goto done
		}
	}
done:
	procStop.Call(hwi)
	procReset.Call(hwi)
	// 停止时不足一帧的尾巴补零推出去：丢掉它等于截断最后一个字。
	if len(pending) > 0 && written < maxRecordBytes {
		frame := make([]byte, port.FrameBytes)
		copy(frame, pending)
		_, _ = sink(frame)
	}
	return nil
}

// openSession 打开默认输入设备并挂上全部缓冲块。返回设备句柄、缓冲与已挂上的块头；
// 调用方负责 procClose 与 unprepareAll（出错时这里已经把自己挂上的那些收回去了）。
func openSession(event uintptr) (uintptr, [][]byte, []waveHdr, error) {
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
		return 0, nil, nil, mmError(code, i18n.T("audio.rec.action.open"))
	}
	bufs := make([][]byte, bufferCount)
	hdrs := make([]waveHdr, bufferCount)
	for i := range bufs {
		bufs[i] = make([]byte, bufferBytes)
		hdrs[i].data = uintptr(unsafe.Pointer(&bufs[i][0]))
		hdrs[i].bufferLength = uint32(len(bufs[i]))
		if code, _, _ := procPrepare.Call(hwi, uintptr(unsafe.Pointer(&hdrs[i])), unsafe.Sizeof(hdrs[i])); code != noError {
			unprepareAll(hwi, hdrs[:i])
			procClose.Call(hwi)
			return 0, nil, nil, mmError(code, i18n.T("audio.rec.action.prepare"))
		}
		if code, _, _ := procAddBuffer.Call(hwi, uintptr(unsafe.Pointer(&hdrs[i])), unsafe.Sizeof(hdrs[i])); code != noError {
			unprepareAll(hwi, hdrs[:i+1])
			procClose.Call(hwi)
			return 0, nil, nil, mmError(code, i18n.T("audio.rec.action.mount"))
		}
	}
	return hwi, bufs, hdrs, nil
}

// drainBuffers 把已完成、还没取走的缓冲块全部取走，并重新挂回队列接着录。
// 返回把取到的音频接在 pending 之后的新切片。
//
// 一次 WIM_DATA 只报一块，但处理被推迟时队列里可能已经积了好几块，所以这里整圈扫一遍。
func drainBuffers(hwi uintptr, hdrs []waveHdr, bufs [][]byte, pending []byte) []byte {
	for i := 0; i < bufferCount; i++ {
		h, buf := &hdrs[i], bufs[i]
		if h.flags&flagDone == 0 {
			continue
		}
		n := int(h.bytesRecorded)
		if n > len(buf) {
			n = len(buf)
		}
		pending = append(pending, buf[:n]...)
		// 这一块回收，重新挂回队列。
		procUnprepare.Call(hwi, uintptr(unsafe.Pointer(h)), unsafe.Sizeof(*h))
		h.bytesRecorded, h.flags = 0, 0
		if code, _, _ := procPrepare.Call(hwi, uintptr(unsafe.Pointer(h)), unsafe.Sizeof(*h)); code != noError {
			// 这里没法返回错误（调用方是一次循环体）：把块标记成「未就绪」，
			// 下一轮自然不再取它；真正的失败由后续 procAddBuffer 的返回码暴露。
			continue
		}
		procAddBuffer.Call(hwi, uintptr(unsafe.Pointer(h)), unsafe.Sizeof(*h))
	}
	return pending
}

// pushFrames 把攒够的整帧推给 sink。返回剩下的不足一帧的尾巴、已推字节数与
// 「调用方不要了」的标志。
func pushFrames(pending []byte, written int, sink port.FrameSink) ([]byte, int, bool, error) {
	for len(pending) >= port.FrameBytes {
		more, err := sink(pending[:port.FrameBytes])
		if err != nil {
			return pending, written, false, err
		}
		written += port.FrameBytes
		pending = pending[port.FrameBytes:]
		if !more {
			return pending, written, true, nil
		}
	}
	return pending, written, false, nil
}

// flagDone WAVEHDR.dwFlags 的 WHDR_DONE（0x00000001）：这一块已经录满、可以取走。
const flagDone = 0x00000001

// unprepareAll 给已挂上的块收尾（出错路径与正常退出共用）。
func unprepareAll(hwi uintptr, hdrs []waveHdr) {
	for i := range hdrs {
		procUnprepare.Call(hwi, uintptr(unsafe.Pointer(&hdrs[i])), unsafe.Sizeof(hdrs[i]))
	}
}

// mmError 把 winmm 的 MMRESULT 翻成可读原因。
func mmError(code uintptr, action string) error {
	var why string
	switch code {
	case 2:
		why = i18n.T("audio.rec.why.bad_id")
	case 4:
		why = i18n.T("audio.rec.why.busy")
	case 5:
		why = i18n.T("audio.rec.why.unsupported")
	case 6:
		why = i18n.T("audio.rec.why.no_device")
	case 7:
		why = i18n.T("audio.rec.why.out_of_memory")
	case 32:
		why = i18n.T("audio.rec.why.format")
	default:
		why = i18n.T("audio.rec.why.unknown")
	}
	return errors.New(i18n.Tf("audio.rec.err_mm", "action", action, "why", why, "code", code))
}
