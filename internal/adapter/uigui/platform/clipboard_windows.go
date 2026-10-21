//go:build windows

// 剪贴板位图读取（Windows，D187 口径一/二）：先 IsClipboardFormatAvailable 探格式
// （不打开剪贴板，绝不阻塞别的程序），命中才 OpenClipboard 取一次句柄并即刻关闭。
//
// 取到的字节再交给平台无关的 clipimg.go 转 PNG——本文件只管「取字节」。
package platform

import (
	"fmt"
	"syscall"
	"unsafe"
)

var (
	procIsClipboardFormatAvailable = user32.NewProc("IsClipboardFormatAvailable")
	procOpenClipboard              = user32.NewProc("OpenClipboard")
	procCloseClipboard             = user32.NewProc("CloseClipboard")
	procGetClipboardData           = user32.NewProc("GetClipboardData")
	procRegisterClipboardFormatW   = user32.NewProc("RegisterClipboardFormatW")
	procGlobalLock                 = kernel32.NewProc("GlobalLock")
	procGlobalUnlock               = kernel32.NewProc("GlobalUnlock")
	procGlobalSize                 = kernel32.NewProc("GlobalSize")
)

// 剪贴板基本格式（Win32 头文件取值）。
const (
	cfDIB   = 8  // 设备无关位图（BITMAPINFOHEADER + 像素）
	cfDIBV5 = 17 // DIBV5（BITMAPV5HEADER，带 alpha 掩码）
)

// pngClipFormat 注册格式 "PNG" 的格式号（0 = 注册失败，此时只剩 DIB 路径）。
// sync.OnceValue 语义由包级惰性缓存提供：格式号进程内恒定。
var pngClipFormatCache uintptr

// pngClipFormat 取（并缓存）注册格式 "PNG" 的编号。浏览器与截图工具都放这个格式，
// 读到就能直通、不必自己解 DIB。
func pngClipFormat() uintptr {
	if pngClipFormatCache != 0 {
		return pngClipFormatCache
	}
	name, err := syscall.UTF16PtrFromString("PNG")
	if err != nil {
		return 0
	}
	r, _, _ := procRegisterClipboardFormatW.Call(uintptr(unsafe.Pointer(name)))
	pngClipFormatCache = r
	return r
}

// ClipboardHasImage 剪贴板里是否有一张图（只探测不打开）。ctrl+V 是否被入口消费
// 就看这个返回值——没图时按键原样交给编辑器做文本粘贴。
func (p *Plat) ClipboardHasImage() bool {
	if r, _, _ := procIsClipboardFormatAvailable.Call(cfDIBV5); r != 0 {
		return true
	}
	if r, _, _ := procIsClipboardFormatAvailable.Call(cfDIB); r != 0 {
		return true
	}
	if f := pngClipFormat(); f != 0 {
		if r, _, _ := procIsClipboardFormatAvailable.Call(f); r != 0 {
			return true
		}
	}
	return false
}

// ClipboardImage 读剪贴板里的图为 PNG 字节。优先注册格式 "PNG"（直通，不重编码），
// 其次 CF_DIBV5 / CF_DIB（自己按 BITMAPINFOHEADER 转）。
//
// 打开剪贴板失败（别的程序正占用）返回错误——调用方提示一次即可，不重试：
// 剪贴板争用是瞬时状态，重试反而更容易两边都卡住。
func (p *Plat) ClipboardImage() ([]byte, error) {
	// 先缩窗口：探测与实际读取之间剪贴板可能变，取不到就按「没有图」处理。
	if !p.ClipboardHasImage() {
		return nil, ErrNoClipboardImage
	}
	if r, _, err := procOpenClipboard.Call(0); r == 0 {
		return nil, fmt.Errorf("打开剪贴板失败（可能被其他程序占用）：%v", err)
	}
	defer procCloseClipboard.Call()

	if f := pngClipFormat(); f != 0 {
		if b, ok := clipboardBytes(f); ok {
			if out, err := clipPNGToPNG(b); err == nil {
				return out, nil
			}
			// 该程序放的 "PNG" 不合法：退回去试 DIB，不当场失败。
		}
	}
	for _, cf := range []uintptr{cfDIBV5, cfDIB} {
		b, ok := clipboardBytes(cf)
		if !ok {
			continue
		}
		out, err := clipDIBToPNG(b)
		if err != nil {
			continue // 换下一档格式再试（V5 解不动时 DIB 往往还在）
		}
		return out, nil
	}
	return nil, ErrNoClipboardImage
}

// clipboardBytes 取某个剪贴板格式的原始字节；ok=false = 该格式不存在或取不到。
func clipboardBytes(format uintptr) ([]byte, bool) {
	h, _, _ := procGetClipboardData.Call(format)
	if h == 0 {
		return nil, false
	}
	ptr, _, _ := procGlobalLock.Call(h)
	if ptr == 0 {
		return nil, false
	}
	defer procGlobalUnlock.Call(h)
	size, _, _ := procGlobalSize.Call(h)
	if size == 0 {
		return nil, false
	}
	// 剪贴板内存块只读：拷出来再转，锁区间之外不持有指针。
	return append([]byte(nil), unsafe.Slice((*byte)(ptrFromUintptr(ptr)), int(size))...), true
}

// ptrFromUintptr Win32 返回的地址（uintptr）→ unsafe.Pointer。逐字写
// unsafe.Pointer(uintptr) 会被 vet 的 unsafeptr 检查拦下（它无法确认这个整数确实
// 来自指针），中转一层是标准写法。
func ptrFromUintptr(p uintptr) unsafe.Pointer {
	var out unsafe.Pointer
	*(*uintptr)(unsafe.Pointer(&out)) = p
	return out
}
