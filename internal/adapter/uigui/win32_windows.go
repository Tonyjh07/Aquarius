//go:build windows

// Win32 补位（§15.6 spike 移植 + D44/§15.1、§15.3、D62）：
//   - 整窗 ULW：mainPresent——全帧合成位图直接提主窗（位图 alpha 即形状/命中/穿透，
//     单通道；形裁 / LWA_ALPHA / overlay 独立窗三机制随 D62 退役）
//   - 定位/夹取/光标跟踪（位置记忆、拖动，§15.6 铁律 2）
//
// 修改性调用一律经 onWindowThread（Window.Run，§15.6 铁律 1）；查询类直接调用。
// 托盘与全局快捷键在托盘步接入（spike/giospike/win32.go 为验证底本）。
package uigui

import (
	"fmt"
	"sync/atomic"
	"syscall"
	"unsafe"

	"gioui.org/app"
	"gioui.org/io/event"
)

var (
	user32   = syscall.NewLazyDLL("user32.dll")
	kernel32 = syscall.NewLazyDLL("kernel32.dll")
	gdi32    = syscall.NewLazyDLL("gdi32.dll")

	procGetModuleHandleW    = kernel32.NewProc("GetModuleHandleW")
	procGetCursorPos        = user32.NewProc("GetCursorPos")
	procSetWindowPos        = user32.NewProc("SetWindowPos")
	procGetWindowRect       = user32.NewProc("GetWindowRect")
	procGetWindowLongPtrW   = user32.NewProc("GetWindowLongPtrW")
	procSetWindowLongPtrW   = user32.NewProc("SetWindowLongPtrW")
	procMonitorFromPoint    = user32.NewProc("MonitorFromPoint")
	procGetMonitorInfoW     = user32.NewProc("GetMonitorInfoW")
	procRegisterClassW      = user32.NewProc("RegisterClassW") // 托盘消息窗口用（shell_windows.go）
	procCreateWindowExW     = user32.NewProc("CreateWindowExW")
	procDefWindowProcW      = user32.NewProc("DefWindowProcW")
	procShowWindow          = user32.NewProc("ShowWindow")
	procUpdateLayeredWindow = user32.NewProc("UpdateLayeredWindow")
	procCreateCompatibleDC  = gdi32.NewProc("CreateCompatibleDC")
	procCreateDIBSection    = gdi32.NewProc("CreateDIBSection")
	procSelectObject        = gdi32.NewProc("SelectObject")
	procDeleteObject        = gdi32.NewProc("DeleteObject")
	procDeleteDC            = gdi32.NewProc("DeleteDC")
)

// 常量（Win32 头文件取值）。
const (
	swpNoSize           = 0x0001
	swpNoMove           = 0x0002
	swpNoZOrder         = 0x0004
	swpNoActivate       = 0x0010
	monDefaultToNearest = 2

	gwlExStyle  = ^uintptr(19) // GWL_EXSTYLE = -20（补码形式过 uintptr 参数）
	wsExLayered = 0x00080000
	// 主窗任务栏屏蔽（D51）：TOOLWINDOW + 清 APPWINDOW。
	wsExAppWindow = 0x00040000 // 顶层窗强制上任务栏（与 TOOLWINDOW 相斥）

	wsExToolWindow = 0x00000080
	wsExTopMost    = 0x00000008
	swHide         = 0

	ulwAlpha   = 0x00000002
	acSrcOver  = 0
	acSrcAlpha = 1

	dibRGBColors = 0
)

// monitorInfo MONITORINFO（x64 布局与 C 一致）。
type monitorInfo struct {
	cbSize    uint32
	rcMonitor rect
	rcWork    rect
	dwFlags   uint32
}

// wndClassW / bitmapInfoHeader / blendFunc / sizeXY：x64 与 C 布局一致。
type (
	wndClassW struct {
		style         uint32
		lpfnWndProc   uintptr
		cbClsExtra    int32
		cbWndExtra    int32
		hInstance     uintptr
		hIcon         uintptr
		hCursor       uintptr
		hbrBackground uintptr
		lpszMenuName  *uint16
		lpszClassName *uint16
	}

	bitmapInfoHeader struct {
		biSize          uint32
		biWidth         int32
		biHeight        int32
		biPlanes        uint16
		biBitCount      uint16
		biCompression   uint32
		biSizeImage     uint32
		biXPelsPerMeter int32
		biYPelsPerMeter int32
		biClrUsed       uint32
		biClrImportant  uint32
	}

	blendFunc struct {
		blendOp             uint8
		blendFlags          uint8
		sourceConstantAlpha uint8
		alphaFormat         uint8
	}

	sizeXY struct{ cx, cy int32 }
)

// viewEvent 从 Gio 事件提取窗口句柄（Win32ViewEvent 仅 Windows 定义）。
func (u *UI) viewEvent(ev event.Event) (uintptr, bool) {
	e, ok := ev.(app.Win32ViewEvent)
	if !ok {
		return 0, false
	}
	return e.HWND, true
}

// windowRectPx 取主窗口物理像素矩形（拖动基准与 ULW 定位）。
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

// moveWindowTo 移动主窗口（不改尺寸）；经窗口线程执行（铁律 1）。
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

// cursorPos 取光标屏幕坐标（拖动增量的绝对基准，铁律 2）。
func cursorPos() point {
	var pt point
	procGetCursorPos.Call(uintptr(unsafe.Pointer(&pt)))
	return pt
}

// platformWorkArea 最近显示器工作区（查询类直接调）——锚点夹取/吸附/停靠共用口径
// （D50：点取锚点中心，含多显示器）。
func platformWorkArea(p point) (rect, bool) {
	hmon, _, _ := procMonitorFromPoint.Call(uintptr(unsafe.Pointer(&p)), monDefaultToNearest)
	var mi monitorInfo
	mi.cbSize = uint32(unsafe.Sizeof(mi))
	if r, _, _ := procGetMonitorInfoW.Call(hmon, uintptr(unsafe.Pointer(&mi))); r == 0 {
		return rect{}, false
	}
	return mi.rcWork, true
}

// platformMonitorAt 点上是否有显示器（MONITOR_DEFAULTTONULL = 0：域外返回 0）——
// D50 停靠外侧边判定（接缝边外还有屏 → 不停靠）。
func platformMonitorAt(p point) bool {
	hmon, _, _ := procMonitorFromPoint.Call(uintptr(unsafe.Pointer(&p)), 0)
	return hmon != 0
}

// hideFromTaskbar 主窗不进任务栏与 Alt+Tab（D51）：置 WS_EX_TOOLWINDOW、清
// WS_EX_APPWINDOW——悬浮球托盘常驻、关窗即隐藏，任务栏条目与形态相斥。
// onHWND 挂接时一次性设置，经 onWindowThread（§15.6 铁律 1）。
func hideFromTaskbar(h uintptr) {
	onWindowThread(func() {
		ex, _, _ := procGetWindowLongPtrW.Call(h, gwlExStyle)
		ne := (ex | wsExToolWindow) &^ wsExAppWindow
		procSetWindowLongPtrW.Call(h, gwlExStyle, ne)
	})
}

// mainVisible 主窗可见性（查询类直接调；无句柄 = 不可见）——fadePresent 兜底：
// 主窗隐藏时不再提交 ULW（hideMain 已藏，防隐藏后仍有帧把位图唤回）。
func mainVisible() bool {
	h := atomic.LoadUintptr(&mainHWND)
	if h == 0 {
		return false
	}
	v, _, _ := procIsWindowVisible.Call(h)
	return v != 0
}

// topMostQuery 主窗置顶态**实际值**（查询类直接调，铁律 1 不限；无句柄 = 缺省置顶）。
func topMostQuery() bool {
	h := atomic.LoadUintptr(&mainHWND)
	if h == 0 {
		return true
	}
	ex, _, _ := procGetWindowLongPtrW.Call(h, gwlExStyle)
	return ex&wsExTopMost != 0
}

// topMostHandle HWND_TOPMOST(-1)/HWND_NOTOPMOST(-2) 的补码 uintptr。
func topMostHandle(on bool) uintptr {
	if on {
		return ^uintptr(0)
	}
	return ^uintptr(1)
}

// platformSetTopMost 显式设置主窗置顶（§15.1 置顶开关）——本端 SetWindowPos 断言，
// 不依赖 Gio 的 TopMost 应用路径（实测会意外丢失、原因未明）；经 onWindowThread（铁律 1）。
func platformSetTopMost(on bool) {
	h := atomic.LoadUintptr(&mainHWND)
	if h == 0 {
		return
	}
	after := topMostHandle(on)
	onWindowThread(func() {
		procSetWindowPos.Call(h, after, 0, 0, 0, 0, swpNoMove|swpNoSize|swpNoActivate)
	})
}

// mainBits 主窗 ULW 位图状态（仅窗口线程访问——全部经 onWindowThread，D62 单通道）。
type mainBits struct {
	hdc, hbm uintptr
	w, h     int32
}

var (
	mbits    mainBits
	mbitsPtr unsafe.Pointer // 当前 DIB 像素（窗口线程访问）

	mainPresentN  int  // 首次/失败日志节流（窗口线程访问）
	mainULWLayerd bool // WS_EX_LAYERED 已挂（窗口线程访问）
)

// ensureMainDIB 分配/复用主窗 DIB（窗口线程内；尺寸变化时重建）。
func ensureMainDIB(w, h int32) error {
	if mbits.hdc != 0 && mbits.w == w && mbits.h == h {
		return nil
	}
	if mbits.hbm != 0 {
		procDeleteObject.Call(mbits.hbm)
		procDeleteDC.Call(mbits.hdc)
		mbits.hbm, mbits.hdc, mbitsPtr = 0, 0, nil
	}
	hdc, _, _ := procCreateCompatibleDC.Call(0)
	if hdc == 0 {
		return syscall.EINVAL
	}
	var bi bitmapInfoHeader
	bi.biSize = uint32(unsafe.Sizeof(bi))
	bi.biWidth = w
	bi.biHeight = -h // 顶向
	bi.biPlanes = 1
	bi.biBitCount = 32
	bi.biCompression = dibRGBColors
	var bits unsafe.Pointer
	hbm, _, _ := procCreateDIBSection.Call(0, uintptr(unsafe.Pointer(&bi)),
		dibRGBColors, uintptr(unsafe.Pointer(&bits)), 0, 0)
	if hbm == 0 || bits == nil {
		procDeleteDC.Call(hdc)
		return syscall.EINVAL
	}
	procSelectObject.Call(hdc, hbm)
	mbits.hdc, mbits.hbm, mbits.w, mbits.h, mbitsPtr = hdc, hbm, w, h, bits
	return nil
}

// ensureLayeredStyle 主窗挂 WS_EX_LAYERED（onHWND 一次性，D62）：ULW 承载形状/命中/
// 穿透的前提；ULW 接管后 SLWA 同窗互斥，LWA_ALPHA 已随 D62 退役。启动防闪的**显式**
// 机制归 D78（挂接即 SW_HIDE、首帧 ULW 成功才揭示）——分层样式只作双保险，不再依赖
// 「首 ULW 前不显示」语义（那只在建窗时挂样式才成立）。
func ensureLayeredStyle(h uintptr) {
	onWindowThread(func() {
		mainULWLayerd = applyLayeredStyle(h)
	})
}

// applyLayeredStyle 置 WS_EX_LAYERED（幂等）；窗口线程调用。
func applyLayeredStyle(h uintptr) bool {
	ex, _, _ := procGetWindowLongPtrW.Call(h, gwlExStyle)
	if ex&wsExLayered == 0 {
		procSetWindowLongPtrW.Call(h, gwlExStyle, ex|wsExLayered)
	}
	return true
}

// hideUntilFirstPresent 挂接即隐藏（D78 启动防闪）：Gio `Configure(ShowWindow)` 早于
// Win32ViewEvent 投递——onHWND 是最早可接管点，此处 SW_HIDE 直到首帧 ULW 提交成功经
// revealMainWindow 揭示。窗口线程调用（onHWND 即在其中）。
func hideUntilFirstPresent(h uintptr) {
	procShowWindow.Call(h, swHide)
}

// revealMainWindow 揭示（D78）：显示 + 激活前台（与现状启动聚焦一致，D78 拍板）——
// 首帧揭示（fadePresent）与呼出（showMain）共用，经 revealMain 槽注入可测。
// 经窗口线程（铁律 1）。
func revealMainWindow(h uintptr) {
	onWindowThread(func() {
		procShowWindow.Call(h, swRestore)
		procSetForegroundWindow.Call(h)
	})
}

// mainPresent 整窗 ULW 提交（D62 单通道）：全帧合成位图直接写主窗——位图 alpha 即
// 形状（自带抗锯齿）也即命中（分层窗逐像素命中：alpha=0 穿透到下层窗口），
// SourceConstantAlpha = 统一半透明（D50 停靠淡化同帧跟随；旧 LWA_ALPHA 退役——
// 同窗 SLWA 与 ULW 互斥，ULW 接管即生效）。bits = 预乘 BGRA（顶向、w*h*4）；
// x/y = 屏幕坐标（物理，实测窗口矩形）、w/h = 位图尺寸（物理）。经窗口线程（铁律 1）。
func mainPresent(x, y, w, h int32, bits []byte, alpha byte) bool {
	if w <= 0 || h <= 0 || int64(w)*int64(h)*4 != int64(len(bits)) {
		return false
	}
	ok := false
	onWindowThread(func() {
		hwnd := atomic.LoadUintptr(&mainHWND)
		if hwnd == 0 {
			return
		}
		if err := ensureMainDIB(w, h); err != nil {
			return
		}
		if !mainULWLayerd {
			mainULWLayerd = applyLayeredStyle(hwnd) // 兜底：onHWND 未跑到的防御
		}
		// 写入 DIB（顶向：bits 直接拷贝）。
		dst := unsafe.Slice((*byte)(mbitsPtr), len(bits))
		copy(dst, bits)
		dstPt := point{x: x, y: y}
		sz := sizeXY{cx: w, cy: h}
		srcPt := point{}
		bf := blendFunc{
			blendOp:             acSrcOver,
			blendFlags:          0,
			sourceConstantAlpha: alpha,
			alphaFormat:         acSrcAlpha,
		}
		r, _, _ := procUpdateLayeredWindow.Call(
			hwnd, 0, // hdcDst：屏幕 DC，可为 NULL（MSDN：指定位置/尺寸时）
			uintptr(unsafe.Pointer(&dstPt)), uintptr(unsafe.Pointer(&sz)),
			mbits.hdc, uintptr(unsafe.Pointer(&srcPt)),
			0, uintptr(unsafe.Pointer(&bf)), ulwAlpha)
		ok = r != 0
		mainPresentN++
		if mainPresentN == 1 || (!ok && mainPresentN <= 8) {
			fmt.Printf("[ulw] UpdateLayeredWindow %dx%d@(%d,%d): %v\n", w, h, x, y, ok)
		}
	})
	return ok
}
