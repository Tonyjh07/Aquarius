//go:build windows

// Win32 补位（§15.6 spike 移植，底本 spike/giospike/win32.go）：形裁（SetWindowRgn）、
// 窗口定位（SetWindowPos/GetWindowRect）、多显示器工作区夹取（MonitorFromPoint/
// GetMonitorInfoW）、光标跟踪（GetCursorPos）。修改性调用一律经 onWindowThread
// （Window.Run，§15.6 铁律 1）；查询类直接调用。
// 托盘与全局快捷键在托盘步接入（Shell_NotifyIconW/RegisterHotKey 已在 spike 验证）。
package uigui

import (
	"sync/atomic"
	"syscall"
	"unsafe"

	"gioui.org/app"
	"gioui.org/io/event"
)

var (
	user32 = syscall.NewLazyDLL("user32.dll")
	gdi32  = syscall.NewLazyDLL("gdi32.dll")

	procGetCursorPos       = user32.NewProc("GetCursorPos")
	procSetWindowPos       = user32.NewProc("SetWindowPos")
	procGetWindowRect      = user32.NewProc("GetWindowRect")
	procSetWindowRgn       = user32.NewProc("SetWindowRgn")
	procCreateRoundRectRgn = gdi32.NewProc("CreateRoundRectRgn")
	procMonitorFromPoint   = user32.NewProc("MonitorFromPoint")
	procGetMonitorInfoW    = user32.NewProc("GetMonitorInfoW")
)

// 常量（Win32 头文件取值）。
const (
	swpNoSize           = 0x0001
	swpNoZOrder         = 0x0004
	swpNoActivate       = 0x0010
	monDefaultToNearest = 2
)

// monitorInfo MONITORINFO（x64 布局与 C 一致）。
type monitorInfo struct {
	cbSize    uint32
	rcMonitor rect
	rcWork    rect
	dwFlags   uint32
}

// viewEvent 从 Gio 事件提取窗口句柄（Win32ViewEvent 仅 Windows 定义）。
func (u *UI) viewEvent(ev event.Event) (uintptr, bool) {
	e, ok := ev.(app.Win32ViewEvent)
	if !ok {
		return 0, false
	}
	return e.HWND, true
}

// windowRectPx 取主窗口物理像素矩形（拖动基准与形裁尺寸）。
func windowRectPx() (rect, bool) {
	h := atomic.LoadUintptr(&mainHWND)
	if h == 0 {
		return rect{}, false
	}
	var rc rect
	if r, _, _ := procGetWindowRect.Call(h, uintptr(unsafe.Pointer(&rc))); r == 0 {
		return rect{}, false
	}
	return rc, true
}

// moveWindowTo 移动主窗口（不改尺寸）；经窗口线程执行（§15.6 铁律 1）。
func moveWindowTo(x, y int32) {
	h := atomic.LoadUintptr(&mainHWND)
	if h == 0 {
		return
	}
	onWindowThread(func() {
		procSetWindowPos.Call(h, 0, uintptr(x), uintptr(y), 0, 0,
			swpNoSize|swpNoZOrder|swpNoActivate)
	})
}

// cursorPos 取光标屏幕坐标（拖动增量的绝对基准，§15.6 铁律 2）。
func cursorPos() point {
	var pt point
	procGetCursorPos.Call(uintptr(unsafe.Pointer(&pt)))
	return pt
}

// clampToWorkArea 把目标位置夹进最近显示器工作区（位置记忆恢复时防"记到拔掉的屏幕"）。
func clampToWorkArea(x, y, w, h int32) (int32, int32) {
	pt := point{x: x, y: y}
	hmon, _, _ := procMonitorFromPoint.Call(uintptr(unsafe.Pointer(&pt)), monDefaultToNearest)
	var mi monitorInfo
	mi.cbSize = uint32(unsafe.Sizeof(mi))
	if r, _, _ := procGetMonitorInfoW.Call(hmon, uintptr(unsafe.Pointer(&mi))); r == 0 {
		return x, y
	}
	wr := mi.rcWork
	if x+w > wr.right {
		x = wr.right - w
	}
	if y+h > wr.bottom {
		y = wr.bottom - h
	}
	if x < wr.left {
		x = wr.left
	}
	if y < wr.top {
		y = wr.top
	}
	return x, y
}

// applyRegion 形裁（SetWindowRgn）：窗口可见区 = 圆角矩形（ellipse = 椭圆宽高，
// 取 2×半径 → 两端全圆），区域外完全不可见且点击穿透。【§15.6 spike 实证】Gio 无
// 真透明（gpu.Clear 强制不透明白）、LWA_COLORKEY 与 D3D swapchain 不兼容（白屏），
// 形裁是悬浮形态的实用解；代价：边缘二值掩码无抗锯齿、失去 DWM 阴影、改尺寸需重算。
// 成功后系统接管 HRGN 所有权，无需 DeleteObject。
func applyRegion(x, y, w, h, ellipse int32) bool {
	hwnd := atomic.LoadUintptr(&mainHWND)
	if hwnd == 0 {
		return false
	}
	ok := false
	onWindowThread(func() {
		hrgn, _, _ := procCreateRoundRectRgn.Call(
			uintptr(x), uintptr(y), uintptr(x+w+1), uintptr(y+h+1),
			uintptr(ellipse), uintptr(ellipse))
		if hrgn == 0 {
			return
		}
		r, _, _ := procSetWindowRgn.Call(hwnd, hrgn, 1)
		ok = r != 0
	})
	return ok
}
