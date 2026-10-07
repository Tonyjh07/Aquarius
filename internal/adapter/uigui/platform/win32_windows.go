//go:build windows

// Win32 平台实现（§15.6 spike 移植 + D44/§15.1、§15.3、D62）：整窗 ULW 像素提交、
// 窗口定位/夹取/光标跟踪、显示器与 DPI 查询、置顶与任务栏屏蔽、启动防闪的隐藏/揭示。
//
// 修改性调用一律经 p.onWindowThread（Gio `Window.Run`，§15.6 铁律 1）；查询类直接调用。
package platform

import (
	"fmt"
	"syscall"
	"unsafe"
)

var (
	user32   = syscall.NewLazyDLL("user32.dll")
	kernel32 = syscall.NewLazyDLL("kernel32.dll")
	gdi32    = syscall.NewLazyDLL("gdi32.dll")

	procGetModuleHandleW    = kernel32.NewProc("GetModuleHandleW")
	procGetCursorPos        = user32.NewProc("GetCursorPos")
	procWindowFromPoint     = user32.NewProc("WindowFromPoint")
	procSetWindowPos        = user32.NewProc("SetWindowPos")
	procGetWindowRect       = user32.NewProc("GetWindowRect")
	procGetWindowLongPtrW   = user32.NewProc("GetWindowLongPtrW")
	procSetWindowLongPtrW   = user32.NewProc("SetWindowLongPtrW")
	procMonitorFromPoint    = user32.NewProc("MonitorFromPoint")
	procGetMonitorInfoW     = user32.NewProc("GetMonitorInfoW")
	procGetDpiForWindow     = user32.NewProc("GetDpiForWindow")
	procRegisterClassW      = user32.NewProc("RegisterClassW") // 托盘消息窗口用（shell_windows.go）
	procCreateWindowExW     = user32.NewProc("CreateWindowExW")
	procDefWindowProcW      = user32.NewProc("DefWindowProcW")
	procShowWindow          = user32.NewProc("ShowWindow")
	procIsWindowVisible     = user32.NewProc("IsWindowVisible")
	procSetForegroundWindow = user32.NewProc("SetForegroundWindow")
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
	swRestore      = 9

	ulwAlpha   = 0x00000002
	acSrcOver  = 0
	acSrcAlpha = 1

	dibRGBColors = 0
)

// winRect/winPoint：x64 与 C 布局一致的私有 POD（导出 DTO Rect/Point 字段名不同，
// 平台调用一律用这两个，边界处转换）。
type winRect struct{ left, top, right, bottom int32 }
type winPoint struct{ x, y int32 }

// monitorInfo MONITORINFO（x64 布局与 C 一致）。
type monitorInfo struct {
	cbSize    uint32
	rcMonitor winRect
	rcWork    winRect
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

// toRect/fromPoint：私有 POD ↔ 导出 DTO。
func toRect(r winRect) Rect {
	return Rect{Left: r.left, Top: r.top, Right: r.right, Bottom: r.bottom}
}

func toWinPoint(p Point) winPoint { return winPoint{x: p.X, y: p.Y} }

// NativeWindowControl Windows = true：平台自管窗口位置/尺寸/显隐（SetWindowPos）与像素
// 提交（ULW 整窗位图），uigui 据此走无边框悬浮球形态 + 自管窗口的机制（停靠/吸附/拖动）。
func (p *Plat) NativeWindowControl() bool { return true }

// MainHandle 主窗句柄（0 = 未挂接）；跨 goroutine 安全（原子读）。
func (p *Plat) MainHandle() Handle { return Handle(p.mainHWND.Load()) }

// AttachMain 挂接主窗句柄（uigui 的 onHWND 调用）：此后显隐/几何/像素提交才有目标窗。
func (p *Plat) AttachMain(h Handle) { p.mainHWND.Store(uintptr(h)) }

// SetMainRun 注册主窗窗口线程投递（Gio `Window.Run`；runWindow 起手调用）。
func (p *Plat) SetMainRun(run RunFunc) { p.mainRun.Store(&run) }

// WindowRect 主窗物理像素矩形（拖动基准与 ULW 定位）。
func (p *Plat) WindowRect() (Rect, bool) {
	h := p.mainHWND.Load()
	if h == 0 {
		return Rect{}, false
	}
	var rc winRect
	if r, _, _ := procGetWindowRect.Call(h, uintptr(unsafe.Pointer(&rc))); r == 0 {
		return Rect{}, false
	}
	return toRect(rc), true
}

// MoveWindow 移动主窗（不改尺寸）；经窗口线程执行（铁律 1）。
func (p *Plat) MoveWindow(x, y int32) {
	h := p.mainHWND.Load()
	if h == 0 {
		return
	}
	p.onWindowThread(func() {
		procSetWindowPos.Call(h, 0, uintptr(x), uintptr(y), 0, 0,
			swpNoSize|swpNoZOrder|swpNoActivate)
	})
}

// ResizeWindow 改主窗尺寸（保位，D90）；经窗口线程执行（铁律 1）。Gio 收 WM_SIZE
// 后下帧以新 Constraints 重排；fadePresent 的尺寸竞态守卫（实测矩形 ≠ frameSize
// 跳帧）兜底对齐一帧。
func (p *Plat) ResizeWindow(w, h int32) {
	hh := p.mainHWND.Load()
	if hh == 0 {
		return
	}
	p.onWindowThread(func() {
		procSetWindowPos.Call(hh, 0, 0, 0, uintptr(w), uintptr(h),
			swpNoMove|swpNoZOrder|swpNoActivate)
	})
}

// CursorPos 取光标屏幕坐标（拖动增量的绝对基准，铁律 2）。
func (p *Plat) CursorPos() Point {
	var pt winPoint
	procGetCursorPos.Call(uintptr(unsafe.Pointer(&pt)))
	return Point{X: pt.x, Y: pt.y}
}

// WindowFromPoint 光标处的顶层窗口（OS 命中判定的同款查询，D85）：返回 0 = 无窗口。
// WindowFromPoint 的 POINT 参数按 **值** 传递（x64 单寄存器：低 32 位 = x、高 32 位 = y，
// 两成员均为原始 32 位，负坐标不符号扩展）。
func (p *Plat) WindowFromPoint(pt Point) Handle {
	arg := uintptr(uint32(pt.X)) | uintptr(uint32(pt.Y))<<32
	h, _, _ := procWindowFromPoint.Call(arg)
	return Handle(h)
}

// WorkArea 最近显示器工作区（查询类直接调）——锚点夹取/吸附/停靠共用口径
// （D50：点取锚点中心，含多显示器）。
func (p *Plat) WorkArea(pt Point) (Rect, bool) {
	wp := toWinPoint(pt)
	hmon, _, _ := procMonitorFromPoint.Call(uintptr(unsafe.Pointer(&wp)), monDefaultToNearest)
	var mi monitorInfo
	mi.cbSize = uint32(unsafe.Sizeof(mi))
	if r, _, _ := procGetMonitorInfoW.Call(hmon, uintptr(unsafe.Pointer(&mi))); r == 0 {
		return Rect{}, false
	}
	return toRect(mi.rcWork), true
}

// MonitorAt 点上是否有显示器（MONITOR_DEFAULTTONULL = 0：域外返回 0）——
// D50 停靠外侧边判定（接缝边外还有屏 → 不停靠）。
func (p *Plat) MonitorAt(pt Point) bool {
	wp := toWinPoint(pt)
	hmon, _, _ := procMonitorFromPoint.Call(uintptr(unsafe.Pointer(&wp)), 0)
	return hmon != 0
}

// WindowDPI 指定窗口 DPI 比例（D90：GetDpiForWindow 直查，恢复期 dp→px 换算用——
// 废除旧「窗高÷默认高」反推，窗口尺寸可配后该假设必错）。查询类直接调（铁律 1 不限）；
// 句柄无效或老系统无此 API → 0，回落 1.0 = 100% 口径（与旧默认行为一致）。
func (p *Plat) WindowDPI(h Handle) float64 {
	v, _, _ := procGetDpiForWindow.Call(uintptr(h))
	if v == 0 {
		return 1.0
	}
	return float64(v) / 96.0
}

// MainDPI 主窗 DPI 比例（未挂接 = 1.0）。
func (p *Plat) MainDPI() float64 { return p.WindowDPI(p.MainHandle()) }

// HideFromTaskbar 主窗不进任务栏与 Alt+Tab（D51）：置 WS_EX_TOOLWINDOW、清
// WS_EX_APPWINDOW——悬浮球托盘常驻、关窗即隐藏，任务栏条目与形态相斥。
// onHWND 挂接时一次性设置，经窗口线程（§15.6 铁律 1）。
func (p *Plat) HideFromTaskbar(h Handle) {
	p.onWindowThread(func() {
		ex, _, _ := procGetWindowLongPtrW.Call(uintptr(h), gwlExStyle)
		ne := (ex | wsExToolWindow) &^ wsExAppWindow
		procSetWindowLongPtrW.Call(uintptr(h), gwlExStyle, ne)
	})
}

// Visible 主窗可见性（查询类直接调；无句柄 = 不可见）——fadePresent 兜底：
// 主窗隐藏时不再提交 ULW（HideWindow 已藏，防隐藏后仍有帧把位图唤回）。
func (p *Plat) Visible() bool {
	h := p.mainHWND.Load()
	if h == 0 {
		return false
	}
	v, _, _ := procIsWindowVisible.Call(h)
	return v != 0
}

// TopMost 主窗置顶态**实际值**（查询类直接调，铁律 1 不限；无句柄 = 缺省置顶）。
func (p *Plat) TopMost() bool {
	h := p.mainHWND.Load()
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

// SetTopMost 显式设置主窗置顶（§15.1 置顶开关）——本端 SetWindowPos 断言，
// 不依赖 Gio 的 TopMost 应用路径（实测会意外丢失、原因未明）；经窗口线程（铁律 1）。
func (p *Plat) SetTopMost(on bool) {
	h := p.mainHWND.Load()
	if h == 0 {
		return
	}
	after := topMostHandle(on)
	p.onWindowThread(func() {
		procSetWindowPos.Call(h, after, 0, 0, 0, 0, swpNoMove|swpNoSize|swpNoActivate)
	})
}

// dibState 主窗 ULW 位图状态（仅窗口线程访问——全部经 onWindowThread，D62 单通道）。
type dibState struct {
	hdc, hbm uintptr
	w, h     int32
	ptr      unsafe.Pointer // 当前 DIB 像素（窗口线程访问）
}

// 主窗像素管线状态（进程内单主窗，故为包级；仅窗口线程访问）。
var (
	mbits         dibState
	mainPresentN  int  // 首次/失败日志节流（窗口线程访问）
	mainULWLayerd bool // WS_EX_LAYERED 已挂（窗口线程访问）
)

// ensureDIB 分配/复用主窗 DIB（窗口线程内；尺寸变化时重建）。
func ensureDIB(w, h int32) error {
	d := &mbits
	if d.hdc != 0 && d.w == w && d.h == h {
		return nil
	}
	if d.hbm != 0 {
		procDeleteObject.Call(d.hbm)
		procDeleteDC.Call(d.hdc)
		d.hbm, d.hdc, d.ptr = 0, 0, nil
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
	d.hdc, d.hbm, d.w, d.h, d.ptr = hdc, hbm, w, h, bits
	return nil
}

// EnsureLayered 主窗挂 WS_EX_LAYERED（onHWND 一次性，D62）：ULW 承载形状/命中/
// 穿透的前提；ULW 接管后 SLWA 同窗互斥，LWA_ALPHA 已随 D62 退役。启动防闪的**显式**
// 机制归 D78（挂接即 SW_HIDE、首帧 ULW 成功才揭示）——分层样式只作双保险，不再依赖
// 「首 ULW 前不显示」语义（那只在建窗时挂样式才成立）。
func (p *Plat) EnsureLayered(h Handle) {
	p.onWindowThread(func() {
		mainULWLayerd = applyLayeredStyle(h)
	})
}

// applyLayeredStyle 置 WS_EX_LAYERED（幂等）；窗口线程调用。
func applyLayeredStyle(h Handle) bool {
	ex, _, _ := procGetWindowLongPtrW.Call(uintptr(h), gwlExStyle)
	if ex&wsExLayered == 0 {
		procSetWindowLongPtrW.Call(uintptr(h), gwlExStyle, ex|wsExLayered)
	}
	return true
}

// HideUntilFirstPresent 挂接即隐藏（D78 启动防闪）：Gio `Configure(ShowWindow)` 早于
// Win32ViewEvent 投递——onHWND 是最早可接管点，此处 SW_HIDE 直到首帧 ULW 提交成功经
// RevealWindow 揭示。经窗口线程下发（铁律 1）：onHWND 在事件循环客户端协程，
// 此际窗口线程常停在 deliverEvent 的 select 发事件、不泵消息，直调 ShowWindow 永久互锁
// （实机启动即「未响应」）。
func (p *Plat) HideUntilFirstPresent(h Handle) {
	p.onWindowThread(func() {
		procShowWindow.Call(uintptr(h), swHide)
	})
}

// RevealWindow 揭示（D78）：显示 + 激活前台（与现状启动聚焦一致，D78 拍板）——
// 首帧揭示（fadePresent）与呼出（showMain）共用。经窗口线程（铁律 1）。
func (p *Plat) RevealWindow(h Handle) {
	p.onWindowThread(func() {
		procShowWindow.Call(uintptr(h), swRestore)
		procSetForegroundWindow.Call(uintptr(h))
	})
}

// HideWindow 主窗隐藏（像素层随窗口一并消失，D62 后无独立 overlay）——**必须在 Gio
// 窗口 goroutine 执行**（帧循环隐藏路径与 WM_CLOSE 拦截共用）。
func (p *Plat) HideWindow(h Handle) {
	procShowWindow.Call(uintptr(h), swHide)
}

// Present 整窗 ULW 提交（D62 单通道）：全帧合成位图直接写主窗——位图 alpha 即
// 形状（自带抗锯齿）也即命中（分层窗逐像素命中：alpha=0 穿透到下层窗口），
// SourceConstantAlpha = 统一半透明（D50 停靠淡化同帧跟随；旧 LWA_ALPHA 退役——
// 同窗 SLWA 与 ULW 互斥，ULW 接管即生效）。bits = 预乘 BGRA（顶向、w*h*4）；
// x/y = 屏幕坐标（物理，实测窗口矩形）、w/h = 位图尺寸（物理）。经窗口线程（铁律 1）。
func (p *Plat) Present(x, y, w, h int32, bits []byte, alpha byte) bool {
	if w <= 0 || h <= 0 || int64(w)*int64(h)*4 != int64(len(bits)) {
		return false
	}
	ok := false
	p.onWindowThread(func() {
		hwnd := p.mainHWND.Load()
		if hwnd == 0 {
			return
		}
		if err := ensureDIB(w, h); err != nil {
			return
		}
		if !mainULWLayerd {
			mainULWLayerd = applyLayeredStyle(Handle(hwnd)) // 兜底：onHWND 未跑到的防御
		}
		// 写入 DIB（顶向：bits 直接拷贝）。
		dst := unsafe.Slice((*byte)(mbits.ptr), len(bits))
		copy(dst, bits)
		dstPt := winPoint{x: x, y: y}
		sz := sizeXY{cx: w, cy: h}
		srcPt := winPoint{}
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
