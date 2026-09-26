//go:build windows

// Win32 补位（§15.6 spike 移植 + D44/§15.1、§15.3）：
//   - 形裁：applyShapesRegion 逐元素圆角矩形并集（无背景"全悬空"，§15.1）
//   - 统一半透明：applyAlpha（LWA_ALPHA 整窗常量，与形裁正交）
//   - 淡出带：overlayPresent/overlaySetVisible —— 独立 WS_EX_LAYERED 窗口 +
//     UpdateLayeredWindow（ULW_ALPHA + AC_SRC_ALPHA 每像素 alpha，D44）
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

	procGetModuleHandleW      = kernel32.NewProc("GetModuleHandleW")
	procGetCursorPos          = user32.NewProc("GetCursorPos")
	procSetWindowPos          = user32.NewProc("SetWindowPos")
	procGetWindowRect         = user32.NewProc("GetWindowRect")
	procSetWindowRgn          = user32.NewProc("SetWindowRgn")
	procCombineRgn            = gdi32.NewProc("CombineRgn") // GDI 函数；声明在 user32 会 panic（LazyProc 找不到入口）
	procGetWindowLongPtrW     = user32.NewProc("GetWindowLongPtrW")
	procSetWindowLongPtrW     = user32.NewProc("SetWindowLongPtrW")
	procSetLayeredWindowAttrs = user32.NewProc("SetLayeredWindowAttributes")
	procMonitorFromPoint      = user32.NewProc("MonitorFromPoint")
	procGetMonitorInfoW       = user32.NewProc("GetMonitorInfoW")
	procRegisterClassW        = user32.NewProc("RegisterClassW")
	procCreateWindowExW       = user32.NewProc("CreateWindowExW")
	procDefWindowProcW        = user32.NewProc("DefWindowProcW")
	procShowWindow            = user32.NewProc("ShowWindow")
	procUpdateLayeredWindow   = user32.NewProc("UpdateLayeredWindow")
	procCreateRoundRectRgn    = gdi32.NewProc("CreateRoundRectRgn")
	procCreateRectRgn         = gdi32.NewProc("CreateRectRgn")
	procCreateCompatibleDC    = gdi32.NewProc("CreateCompatibleDC")
	procCreateDIBSection      = gdi32.NewProc("CreateDIBSection")
	procSelectObject          = gdi32.NewProc("SelectObject")
	procDeleteObject          = gdi32.NewProc("DeleteObject")
	procDeleteDC              = gdi32.NewProc("DeleteDC")
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
	lwaAlpha    = 0x00000002

	rgnOr = 2 // CombineRgn 并集（RGN_AND=1 / RGN_OR=2 / RGN_DIFF=4——写成1会取交集得空区域）

	// overlay 窗口（D44 淡出带）。
	wsExToolWindow  = 0x00000080
	wsExTopMost     = 0x00000008
	wsExTransparent = 0x00000020 // layered 窗口：鼠标穿透
	wsExNoActivate  = 0x08000000
	wsPopup         = 0x80000000 // WS_POPUP（无符号 32 位值，作 uintptr 传参）
	swHide          = 0
	swShowNA        = 8

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

// applyShapesRegion 形裁：逐元素圆角矩形并集（§15.1/D44）——元素间隙与窗口边角
// 完全透明且点击穿透。空集 = 全窗不可见（内容未就绪的兜底，防白色底板闪现）。
// 成功后系统接管最终 HRGN 所有权；中途失败的句柄自行回收。
func applyShapesRegion(shapes []shapePhys) bool {
	hwnd := atomic.LoadUintptr(&mainHWND)
	if hwnd == 0 {
		return false
	}
	ok := false
	onWindowThread(func() {
		var acc uintptr
		del := func(h uintptr) {
			if h != 0 {
				procDeleteObject.Call(h)
			}
		}
		for _, s := range shapes {
			r, _, _ := procCreateRoundRectRgn.Call(
				uintptr(s.x), uintptr(s.y),
				uintptr(s.x+s.w+1), uintptr(s.y+s.h+1),
				uintptr(s.ellipse), uintptr(s.ellipse))
			if r == 0 {
				continue
			}
			if acc == 0 {
				acc = r
				continue
			}
			// 并集：dst 用独立临时区（规避 CombineRgn 源/目标别名的未定义约束）。
			tmp, _, _ := procCreateRectRgn.Call(0, 0, 0, 0)
			if tmp == 0 {
				del(r)
				continue
			}
			procCombineRgn.Call(tmp, acc, r, rgnOr)
			del(acc)
			del(r)
			acc = tmp
		}
		if acc == 0 {
			acc, _, _ = procCreateRectRgn.Call(0, 0, 0, 0) // 空区域
		}
		// SetWindowRgn 成功后系统接管 HRGN 所有权；失败归还调用方回收。
		if r, _, _ := procSetWindowRgn.Call(hwnd, acc, 1); r == 0 {
			del(acc)
			return
		}
		ok = true
	})
	return ok
}

// applyAlpha 统一半透明（LWA_ALPHA 整窗常量；与形裁正交，spike 已验证）。
// 淡出 overlay 是独立窗口走 UpdateLayeredWindow，两者不冲突（MSDN 的
// "SLWA 后同窗 ULW 失效" 仅约束同一 hwnd）。
func applyAlpha(alpha byte) bool {
	h := atomic.LoadUintptr(&mainHWND)
	if h == 0 {
		return false
	}
	ok := false
	onWindowThread(func() {
		ex, _, _ := procGetWindowLongPtrW.Call(h, gwlExStyle)
		procSetWindowLongPtrW.Call(h, gwlExStyle, ex|wsExLayered)
		r, _, _ := procSetLayeredWindowAttrs.Call(h, 0, uintptr(alpha), lwaAlpha)
		ok = r != 0
	})
	return ok
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

// overlayState 淡出带 overlay 窗口（仅窗口线程访问——全部经 onWindowThread）。
type overlayState struct {
	hwnd, hdc, hbm uintptr
	w, h           int32
	visible        bool
}

var ovl overlayState

// fadePresentLogged overlay 首次提交与失败时记日志（窗口线程访问）。
var fadePresentLogged bool

var overlayClassOnce uintptr // RegisterClassW 只做一次

// overlaySyncTopMost 淡出 overlay 与主窗 z 序同步（§15.1），两步：
//  1. 置顶标志对齐——以主窗**实际**置顶位为准（overlay 不带独立 WS_EX_TOPMOST）。
//  2. 相邻锚定（恒做）——overlay 紧贴主窗。只对齐标志不够：同带内激活序列会把别的
//     窗口插到主窗与带之间（实测 bug：切非置顶后带被终端压住/整条消失——主窗被点到
//     带顶、带留在原地沉在终端下）。每次提交把带拉回主窗身后（主窗区域整带挖空，
//     带从洞里透出；紧邻侧在上在下均可见）。
//
// 仅窗口线程调用（ovl 归属该线程）。
func overlaySyncTopMost() {
	main := atomic.LoadUintptr(&mainHWND)
	if main == 0 || ovl.hwnd == 0 {
		return
	}
	want, _, _ := procGetWindowLongPtrW.Call(main, gwlExStyle)
	have, _, _ := procGetWindowLongPtrW.Call(ovl.hwnd, gwlExStyle)
	wantOn := want&wsExTopMost != 0
	if wantOn != (have&wsExTopMost != 0) {
		procSetWindowPos.Call(ovl.hwnd, topMostHandle(wantOn), 0, 0, 0, 0,
			swpNoMove|swpNoSize|swpNoActivate)
	}
	procSetWindowPos.Call(ovl.hwnd, main, 0, 0, 0, 0,
		swpNoMove|swpNoSize|swpNoActivate)
}

// overlayPresent 提交淡出带内容：懒创建 overlay 窗口 + 复用 DIB +
// UpdateLayeredWindow(ULW_ALPHA + AC_SRC_ALPHA)。bits = 预乘 BGRA（顶向、w*h*4）；
// alpha = SourceConstantAlpha（主窗 LWA_ALPHA 常量，与主窗半透明衔接，D44）。
// x/y = 屏幕坐标（物理），w/h = 带尺寸（物理）。
func overlayPresent(x, y, w, h int32, bits []byte, alpha byte) bool {
	if w <= 0 || h <= 0 || int64(w)*int64(h)*4 != int64(len(bits)) {
		return false
	}
	ok := false
	onWindowThread(func() {
		if err := overlayEnsure(x, y, w, h); err != nil {
			return
		}
		overlaySyncTopMost() // 置顶态与主窗实际态对齐（§15.1：带不离主窗独立悬浮）
		if ovl.visible == false {
			procShowWindow.Call(ovl.hwnd, swShowNA)
			ovl.visible = true
		}
		// 写入 DIB（顶向：bits 直接拷贝）。
		dst := unsafe.Slice((*byte)(ovlBits()), len(bits))
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
			ovl.hwnd, 0, // hdcDst：屏幕 DC，可为 NULL（MSDN：指定位置/尺寸时）
			uintptr(unsafe.Pointer(&dstPt)), uintptr(unsafe.Pointer(&sz)),
			ovl.hdc, uintptr(unsafe.Pointer(&srcPt)),
			0, uintptr(unsafe.Pointer(&bf)), ulwAlpha)
		ok = r != 0
		if !fadePresentLogged || !ok {
			fadePresentLogged = true
			fmt.Printf("[fade] UpdateLayeredWindow %dx%d@(%d,%d): %v\n", w, h, x, y, ok)
		}
	})
	return ok
}

// overlaySetVisible 隐藏/显示淡出带（带内无内容时隐藏，省合成）。
func overlaySetVisible(v bool) {
	onWindowThread(func() {
		if ovl.hwnd == 0 || ovl.visible == v {
			return
		}
		if v {
			procShowWindow.Call(ovl.hwnd, swShowNA)
		} else {
			procShowWindow.Call(ovl.hwnd, swHide)
		}
		ovl.visible = v
	})
}

// ovlBits 当前 DIB 的像素指针（无状态查询失败返回 nil——调用方已保证尺寸一致）。
func ovlBits() unsafe.Pointer { return ovlBitsPtr }

var ovlBitsPtr unsafe.Pointer

// overlayEnsure 创建/复用 overlay 窗口与 DIB（窗口线程内）。
func overlayEnsure(x, y, w, h int32) error {
	if ovl.hwnd == 0 {
		hInst, _, _ := procGetModuleHandleW.Call(0)
		if overlayClassOnce == 0 {
			cls, _ := syscall.UTF16PtrFromString("AquariusUiguiFade")
			var wc wndClassW
			wc.lpfnWndProc = syscall.NewCallback(overlayWndProc)
			wc.hInstance = hInst
			wc.lpszClassName = cls
			if r, _, _ := procRegisterClassW.Call(uintptr(unsafe.Pointer(&wc))); r == 0 {
				// 已注册（同进程二次调用）不算错误。
			}
			overlayClassOnce = 1
		}
		name, _ := syscall.UTF16PtrFromString("AquariusUiguiFade")
		// 不带 WS_EX_TOPMOST：置顶态由 overlaySyncTopMost 按主窗实际态逐次对齐（§15.1）。
		hwnd, _, _ := procCreateWindowExW.Call(
			wsExToolWindow|wsExLayered|wsExTransparent|wsExNoActivate,
			uintptr(unsafe.Pointer(name)), 0,
			wsPopup,
			uintptr(x), uintptr(y), uintptr(w), uintptr(h),
			0, 0, hInst, 0)
		if hwnd == 0 {
			return syscall.EINVAL
		}
		ovl.hwnd = hwnd
		ovl.visible = false
	}
	if ovl.w != w || ovl.h != h {
		if ovl.hbm != 0 {
			procDeleteObject.Call(ovl.hbm)
			procDeleteDC.Call(ovl.hdc)
			ovl.hbm, ovl.hdc = 0, 0
		}
		if ovl.hdc == 0 {
			ovl.hdc, _, _ = procCreateCompatibleDC.Call(0)
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
			return syscall.EINVAL
		}
		procSelectObject.Call(ovl.hdc, hbm)
		ovl.hbm, ovlBitsPtr = hbm, bits
		ovl.w, ovl.h = w, h
	}
	return nil
}

// overlayWndProc overlay 无需处理的消息直落 DefWindowProc（窗口线程的
// Gio 消息泵分发到本过程）。
func overlayWndProc(hwnd, uMsg, wParam, lParam uintptr) uintptr {
	r, _, _ := procDefWindowProcW.Call(hwnd, uMsg, wParam, lParam)
	return r
}
