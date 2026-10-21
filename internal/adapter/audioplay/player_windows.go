//go:build windows

package audioplay

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"sync"
	"syscall"
	"unsafe"

	"github.com/Tonyjh07/Aquarius/internal/domain/conversation"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

var _ port.AudioPlayer = (*Player)(nil)

var (
	procPlaySound = syscall.NewLazyDLL("winmm.dll").NewProc("PlaySoundW")
	// playMu 串行化 PlaySound 调用：它操作的是进程级播放状态，多个 Player 实例
	// 并发调用会互相打断。
	playMu sync.Mutex
)

const (
	sndAsync     = 0x0001
	sndNoDefault = 0x0002
	sndFilename  = 0x00020000
)

// Play 把 r 写成临时文件后异步播放，立即返回；先停掉上一条。
func (p *Player) Play(ctx context.Context, audio conversation.BlobRef, r io.Reader) error {
	if ctx == nil {
		return errors.New("audio: nil context")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if r == nil {
		return errors.New("audio: 没有可播放的数据")
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.stopLocked()

	tmp, err := os.CreateTemp("", "aquarius-tts-*.wav")
	if err != nil {
		return fmt.Errorf("audio: 创建临时文件: %w", err)
	}
	name := tmp.Name()
	n, cerr := io.Copy(tmp, io.LimitReader(r, maxAudioBytes+1))
	if err := tmp.Close(); err != nil && cerr == nil {
		cerr = err
	}
	if cerr != nil {
		os.Remove(name)
		return fmt.Errorf("audio: 写临时文件: %w", cerr)
	}
	if n == 0 {
		os.Remove(name)
		return errors.New("audio: 音频为空")
	}
	if n > maxAudioBytes {
		os.Remove(name)
		return fmt.Errorf("audio: 音频超过 %d MB", maxAudioBytes>>20)
	}

	ptr, err := syscall.UTF16PtrFromString(name)
	if err != nil {
		os.Remove(name)
		return fmt.Errorf("audio: 文件名: %w", err)
	}
	playMu.Lock()
	ok, _, _ := procPlaySound.Call(uintptr(unsafe.Pointer(ptr)), 0, sndFilename|sndAsync|sndNoDefault)
	playMu.Unlock()
	if ok == 0 {
		os.Remove(name)
		return errors.New("audio: 系统播放器拒绝了该音频")
	}
	p.file = name
	return nil
}

// Stop 停止播放并删掉临时文件。
func (p *Player) Stop() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.stopLocked()
}

func (p *Player) stopLocked() {
	playMu.Lock()
	procPlaySound.Call(0, 0, 0) // pszSound = NULL 停止
	playMu.Unlock()
	if p.file != "" {
		os.Remove(p.file)
		p.file = ""
	}
}
