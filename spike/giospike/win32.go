// spike/giospike —— Win32 补位能力实测（DESIGN §15.6 / D43 前置验证）。
//
// Gio v0.10.2 没有窗口透明与窗口定位 Option，但会投递 Win32ViewEvent{HWND}——
// 本文件用纯 Go syscall（无 cgo）验证三件补位能力：
//
//	① 托盘图标 + 右键菜单（Shell_NotifyIconW + 消息窗口）
//	② 全局快捷键（RegisterHotKey，默认 Alt+Space，失败回退 Ctrl+Alt+A）
//	③ 多显示器枚举 + 窗口定位/位置记忆（MonitorFromPoint + SetWindowPos）
//
// 另在 main.go 中验证：形裁（SetWindowRgn，胶囊裁切 = 悬浮形态的实现方案）、
// 半透明（LWA_ALPHA）。【已排除】LWA_COLORKEY 色键与 D3D swapchain 不兼容（白屏，
// WinUI3#8469/SDL#15751 同类）；DWM 圆角被形裁完全取代且会多一圈窗口描边。
package main

import (
	"fmt"
	"os"
	"runtime"
	"sync/atomic"
	"syscall"
	"unsafe"
)

var (
	user32   = syscall.NewLazyDLL("user32.dll")
	kernel32 = syscall.NewLazyDLL("kernel32.dll")
	shell32  = syscall.NewLazyDLL("shell32.dll")
	gdi32    = syscall.NewLazyDLL("gdi32.dll")

	procGetModuleHandleW      = kernel32.NewProc("GetModuleHandleW")
	procRegisterClassW        = user32.NewProc("RegisterClassW")
	procCreateWindowExW       = user32.NewProc("CreateWindowExW")
	procDefWindowProcW        = user32.NewProc("DefWindowProcW")
	procGetMessageW           = user32.NewProc("GetMessageW")
	procTranslateMessage      = user32.NewProc("TranslateMessage")
	procDispatchMessageW      = user32.NewProc("DispatchMessageW")
	procPostQuitMessage       = user32.NewProc("PostQuitMessage")
	procPostMessageW          = user32.NewProc("PostMessageW")
	procShowWindow            = user32.NewProc("ShowWindow")
	procIsWindowVisible       = user32.NewProc("IsWindowVisible")
	procSetForegroundWindow   = user32.NewProc("SetForegroundWindow")
	procGetCursorPos          = user32.NewProc("GetCursorPos")
	procLoadImageW            = user32.NewProc("LoadImageW")
	procLoadIconW             = user32.NewProc("LoadIconW")
	procRegisterHotKey        = user32.NewProc("RegisterHotKey")
	procUnregisterHotKey      = user32.NewProc("UnregisterHotKey")
	procCreatePopupMenu       = user32.NewProc("CreatePopupMenu")
	procAppendMenuW           = user32.NewProc("AppendMenuW")
	procTrackPopupMenu        = user32.NewProc("TrackPopupMenu")
	procDestroyMenu           = user32.NewProc("DestroyMenu")
	procSetWindowPos          = user32.NewProc("SetWindowPos")
	procGetWindowRect         = user32.NewProc("GetWindowRect")
	procGetWindowLongPtrW     = user32.NewProc("GetWindowLongPtrW")
	procSetWindowLongPtrW     = user32.NewProc("SetWindowLongPtrW")
	procSetLayeredWindowAttrs = user32.NewProc("SetLayeredWindowAttributes")
	procMonitorFromPoint      = user32.NewProc("MonitorFromPoint")
	procMonitorFromWindow     = user32.NewProc("MonitorFromWindow")
	procGetMonitorInfoW       = user32.NewProc("GetMonitorInfoW")
	procEnumDisplayMonitors   = user32.NewProc("EnumDisplayMonitors")
	procShellNotifyIconW      = shell32.NewProc("Shell_NotifyIconW")
	procSetWindowRgn          = user32.NewProc("SetWindowRgn")
	procCreateRoundRectRgn    = gdi32.NewProc("CreateRoundRectRgn")
)

// 常量（Win32 头文件取值）。
const (
	wmDestroy    = 0x0002
	wmCommand    = 0x0111
	wmNull       = 0x0000
	wmHotkey     = 0x0312
	wmLButtonUp  = 0x0202
	wmRButtonUp  = 0x0205
	wmApp        = 0x8000
	trayCallback = wmApp + 1

	modAlt     = 0x0001
	modControl = 0x0002
	vkSpace    = 0x20
	vkA        = 0x41

	hwndMessage = ^uintptr(2) // HWND_MESSAGE = -3（补码：~2）

	swHide    = 0
	swRestore = 9

	swpNoSize     = 0x0001
	swpNoZOrder   = 0x0004
	swpNoActivate = 0x0010

	gwlExStyle  = ^uintptr(19) // GWLP_EXSTYLE = -20（补码形式过 uintptr 参数）
	wsExLayered = 0x00080000
	lwaAlpha    = 0x00000002

	nimAdd    = 0x00000000
	nimDelete = 0x00000002
	nifIcon   = 0x00000002
	nifTip    = 0x00000004
	nifMsg    = 0x00000001

	mfString    = 0x00000000
	mfSeparator = 0x00000800
	tpmRightBtn = 0x0002
	tpmRetCmd   = 0x0100

	cmdToggle = 101
	cmdExit   = 102

	monDefaultToNearest = 2
)

// Win32 结构体（x64 布局与 C 一致，Go 对齐规则自动补 padding）。
type (
	point struct{ x, y int32 }
	rect  struct{ left, top, right, bottom int32 }

	winMsg struct {
		hwnd    uintptr
		message uint32
		wParam  uintptr
		lParam  uintptr
		time    uint32
		pt      point
	}

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

	monitorInfo struct {
		cbSize    uint32
		rcMonitor rect
		rcWork    rect
		dwFlags   uint32
	}

	notifyIconData struct {
		cbSize           uint32
		hWnd             uintptr
		uID              uint32
		uFlags           uint32
		uCallbackMessage uint32
		hIcon            uintptr
		szTip            [128]uint16
		dwState          uint32
		dwStateMask      uint32
		szInfo           [256]uint16
		uVersion         uint32
		szInfoTitle      [64]uint16
		dwInfoFlags      uint32
		guidItem         [16]byte
		hBalloonIcon     uintptr
	}
)

// 全局句柄：Gio 窗口 HWND（Win32ViewEvent 投递）与托盘消息窗口。
var (
	mainHWND   uintptr // atomic 存取：Gio 事件线程写、托盘线程读
	shellHWND  uintptr
	trayAdded  bool
	hotkeyUsed string
	onShowHook atomic.Pointer[func()] // 快捷键呼出后触发 Gio 重绘
	win32Run   atomic.Pointer[func(func())]
)

// onWindowThread 把修改性 Win32 调用送到 Gio 窗口线程执行（Window.Run）并等待完成。
// 【死锁实证】Gio runLoop 线程在 deliverEvent 的 select 中服务 driverFuncs（os.go），
// 但从客户端协程跨线程 SendMessage（SetWindowPos/SetWindowLongPtr 等内部回投窗口过程）
// 会永久阻塞——runLoop 停在 select、不泵消息（spike 堆栈 goroutine 22/50 实证）。
func onWindowThread(f func()) {
	if r := win32Run.Load(); r != nil {
		(*r)(f)
		return
	}
	f()
}

// runShell 托盘 + 全局快捷键消息线程（自建消息窗口 + 独立消息泵）。
// 窗口消息必须由创建线程分发，故整体 LockOSThread。
func runShell() {
	runtime.LockOSThread()

	hInst, _, _ := procGetModuleHandleW.Call(0)
	cls, _ := syscall.UTF16PtrFromString("AquariusGioSpikeShell")
	wndProcCB := syscall.NewCallback(shellWndProc)
	var wc wndClassW
	wc.lpfnWndProc = wndProcCB
	wc.hInstance = hInst
	wc.lpszClassName = cls
	if r, _, err := procRegisterClassW.Call(uintptr(unsafe.Pointer(&wc))); r == 0 {
		fmt.Printf("[tray] RegisterClassW 失败: %v\n", err)
		return
	}
	hwnd, _, err := procCreateWindowExW.Call(0, uintptr(unsafe.Pointer(cls)), 0,
		0, 0, 0, 0, 0, hwndMessage, 0, hInst, 0)
	if hwnd == 0 {
		fmt.Printf("[tray] 消息窗口创建失败: %v\n", err)
		return
	}
	shellHWND = hwnd

	// ① 全局快捷键：默认 Alt+Space（设计默认值），失败回退 Ctrl+Alt+A。
	if r, _, err := procRegisterHotKey.Call(hwnd, 1, modAlt, vkSpace); r != 0 {
		hotkeyUsed = "Alt+Space"
		fmt.Println("[hotkey] RegisterHotKey(Alt+Space) = 成功")
	} else if r2, _, err2 := procRegisterHotKey.Call(hwnd, 2, modAlt|modControl, vkA); r2 != 0 {
		hotkeyUsed = "Ctrl+Alt+A"
		fmt.Printf("[hotkey] RegisterHotKey(Alt+Space) = 失败 %v；回退 Ctrl+Alt+A = 成功\n", err)
		_ = err2
	} else {
		fmt.Printf("[hotkey] RegisterHotKey 两档均失败: %v\n", err)
	}

	// ② 托盘图标 + 提示。
	icon := loadTrayIcon()
	var nid notifyIconData
	nid.cbSize = uint32(unsafe.Sizeof(nid))
	nid.hWnd = hwnd
	nid.uID = 1
	nid.uFlags = nifIcon | nifTip | nifMsg
	nid.uCallbackMessage = trayCallback
	nid.hIcon = icon
	copy(nid.szTip[:], syscall.StringToUTF16("Aquarius GUI spike"))
	if r, _, err := procShellNotifyIconW.Call(nimAdd, uintptr(unsafe.Pointer(&nid))); r != 0 {
		trayAdded = true
		fmt.Println("[tray] Shell_NotifyIconW(NIM_ADD) = 成功（左键显隐，右键菜单）")
	} else {
		fmt.Printf("[tray] Shell_NotifyIconW(NIM_ADD) = 失败: %v\n", err)
	}

	// ③ 多显示器枚举（一次性探测）。
	probeMonitors()

	var m winMsg
	for {
		r, _, err := procGetMessageW.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0)
		if int32(r) <= 0 {
			if int32(r) == -1 {
				fmt.Printf("[tray] 消息泵错误: %v\n", err)
			}
			return
		}
		procTranslateMessage.Call(uintptr(unsafe.Pointer(&m)))
		procDispatchMessageW.Call(uintptr(unsafe.Pointer(&m)))
	}
}

// shellWndProc 消息窗口过程：托盘回调 / 快捷键 / 菜单命令。
func shellWndProc(hwnd, uMsg, wParam, lParam uintptr) uintptr {
	switch uMsg {
	case trayCallback:
		switch lParam & 0xFFFF {
		case wmLButtonUp:
			toggleMainWindow()
		case wmRButtonUp:
			showTrayMenu(hwnd)
		}
		return 0
	case wmHotkey:
		toggleMainWindow()
		return 0
	case wmCommand:
		switch wParam & 0xFFFF {
		case cmdToggle:
			toggleMainWindow()
		case cmdExit:
			fmt.Println("[exit] 托盘菜单退出")
			os.Exit(0)
		}
		return 0
	case wmDestroy:
		procPostQuitMessage.Call(0)
		return 0
	}
	r, _, _ := procDefWindowProcW.Call(hwnd, uMsg, wParam, lParam)
	return r
}

// showTrayMenu 右键菜单（与托盘菜单同内容，§15.1）：显示/隐藏 · 退出。
func showTrayMenu(hwnd uintptr) {
	menu, _, _ := procCreatePopupMenu.Call()
	toggleText, _ := syscall.UTF16PtrFromString("显示 / 隐藏输入窗")
	exitText, _ := syscall.UTF16PtrFromString("退出")
	procAppendMenuW.Call(menu, mfString, cmdToggle, uintptr(unsafe.Pointer(toggleText)))
	procAppendMenuW.Call(menu, mfSeparator, 0, 0)
	procAppendMenuW.Call(menu, mfString, cmdExit, uintptr(unsafe.Pointer(exitText)))
	var pt point
	procGetCursorPos.Call(uintptr(unsafe.Pointer(&pt)))
	procSetForegroundWindow.Call(hwnd)
	procTrackPopupMenu.Call(menu, tpmRightBtn|tpmRetCmd, uintptr(pt.x), uintptr(pt.y), 0, hwnd, 0)
	procPostMessageW.Call(hwnd, wmNull, 0, 0) // MSDN 要求：收尾防菜单不消失
	procDestroyMenu.Call(menu)
}

// toggleMainWindow 主窗口显隐（托盘/快捷键共用）。
func toggleMainWindow() {
	h := atomic.LoadUintptr(&mainHWND)
	if h == 0 {
		return
	}
	visible, _, _ := procIsWindowVisible.Call(h)
	if visible != 0 {
		procShowWindow.Call(h, swHide)
	} else {
		procShowWindow.Call(h, swRestore)
		procSetForegroundWindow.Call(h)
		if fn := onShowHook.Load(); fn != nil {
			(*fn)()
		}
	}
}

// loadTrayIcon 托盘图标：优先项目 icon，找不到用系统默认。
func loadTrayIcon() uintptr {
	exe, _ := os.Executable()
	var candidates []string
	if exe != "" {
		candidates = append(candidates,
			exe+`\..\..\..\assets\icon\aquarius.ico`,
			exe+`\..\..\assets\icon\aquarius.ico`)
	}
	candidates = append(candidates, `assets\icon\aquarius.ico`, `..\assets\icon\aquarius.ico`,
		`E:\Aquarius\assets\icon\aquarius.ico`)
	const imageIcon, lrLoadFromFile, lrDefaultSize = 1, 0x0010, 0x0040
	for _, p := range candidates {
		if _, err := os.Stat(p); err != nil {
			continue
		}
		ptr, _ := syscall.UTF16PtrFromString(p)
		if h, _, _ := procLoadImageW.Call(0, uintptr(unsafe.Pointer(ptr)), imageIcon,
			0, 0, lrLoadFromFile|lrDefaultSize); h != 0 {
			return h
		}
	}
	const idIApplication = 32512
	h, _, _ := procLoadIconW.Call(0, idIApplication)
	return h
}

// probeMonitors 枚举显示器（多显示器定位验证）。
func probeMonitors() {
	cb := syscall.NewCallback(func(hmon, hdc, lprc, lparam uintptr) uintptr {
		var mi monitorInfo
		mi.cbSize = uint32(unsafe.Sizeof(mi))
		if r, _, _ := procGetMonitorInfoW.Call(hmon, uintptr(unsafe.Pointer(&mi))); r == 0 {
			return 1
		}
		fmt.Printf("[mon] 屏幕(%d,%d)-(%d,%d) 工作区(%d,%d)-(%d,%d)\n",
			mi.rcMonitor.left, mi.rcMonitor.top, mi.rcMonitor.right, mi.rcMonitor.bottom,
			mi.rcWork.left, mi.rcWork.top, mi.rcWork.right, mi.rcWork.bottom)
		return 1
	})
	var count int32
	procEnumDisplayMonitors.Call(0, 0, cb, 0)
	_ = count
	fmt.Println("[mon] 显示器枚举完成（上列工作区即定位/位置记忆的坐标空间）")
}

// clampToWorkArea 把目标位置夹进最近显示器工作区（位置记忆恢复时防"记到拔掉的屏幕"）。
func clampToWorkArea(x, y int32, w, h int32) (int32, int32) {
	var pt point
	pt.x, pt.y = x, y
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

// windowRectPx 取主窗口物理像素矩形（拖动增量 1:1 对应屏幕像素）。
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

// moveWindowTo 移动主窗口（不改尺寸）；经窗口线程执行（onWindowThread）。
func moveWindowTo(x, y int32) {
	h := atomic.LoadUintptr(&mainHWND)
	if h == 0 {
		return
	}
	onWindowThread(func() {
		procSetWindowPos.Call(h, 0, uintptr(x), uintptr(y), 0, 0, swpNoSize|swpNoZOrder|swpNoActivate)
	})
}

// applyPillRegion 把窗口形裁为胶囊（SetWindowRgn）：窗口可见区 = 胶囊圆角矩形，
// 区域外完全不可见且点击穿透。【调研结论】Gio 无真透明（gpu.Clear 强制不透明白）、
// LWA_COLORKEY 与 D3D swapchain 不兼容（WinUI3#8469/SDL#15751 同类，白屏），
// 形裁是"悬浮胶囊"的实用解。代价：边缘二值掩码无抗锯齿、失去 DWM 阴影、改尺寸需重算。
func applyPillRegion(x, y, w, h int32) bool {
	hwnd := atomic.LoadUintptr(&mainHWND)
	if hwnd == 0 {
		return false
	}
	ok := false
	onWindowThread(func() {
		// 椭圆宽高 = 胶囊高度 → 两端全圆。
		hrgn, _, err := procCreateRoundRectRgn.Call(
			uintptr(x), uintptr(y), uintptr(x+w+1), uintptr(y+h+1), uintptr(h), uintptr(h))
		if hrgn == 0 {
			fmt.Printf("[形裁] CreateRoundRectRgn 失败: %v\n", err)
			return
		}
		// SetWindowRgn 成功后系统接管 HRGN 所有权，无需 DeleteObject。
		r, _, err := procSetWindowRgn.Call(hwnd, hrgn, 1)
		ok = r != 0
		fmt.Printf("[形裁] SetWindowRgn(%d,%d %dx%d): %v (err=%v)\n", x, y, w, h, ok, err)
	})
	return ok
}

// clearPillRegion 取消形裁（SetWindowRgn(NULL)）。
func clearPillRegion() bool {
	hwnd := atomic.LoadUintptr(&mainHWND)
	if hwnd == 0 {
		return false
	}
	ok := false
	onWindowThread(func() {
		r, _, err := procSetWindowRgn.Call(hwnd, 0, 1)
		ok = r != 0
		fmt.Printf("[形裁] 取消: %v (err=%v)\n", ok, err)
	})
	return ok
}

// cursorPos 取光标屏幕坐标（拖动增量的绝对基准，修"回弹"）。
func cursorPos() point {
	var pt point
	procGetCursorPos.Call(uintptr(unsafe.Pointer(&pt)))
	return pt
}

// applyAlpha 半透明（常量 alpha，非每像素）；经窗口线程执行。
func applyAlpha(on bool) bool {
	h := atomic.LoadUintptr(&mainHWND)
	if h == 0 {
		return false
	}
	ok := false
	onWindowThread(func() {
		ex, _, _ := procGetWindowLongPtrW.Call(h, gwlExStyle)
		procSetWindowLongPtrW.Call(h, gwlExStyle, ex|wsExLayered)
		var r uintptr
		var err error
		if on {
			r, _, err = procSetLayeredWindowAttrs.Call(h, 0, 210, lwaAlpha)
		} else {
			r, _, err = procSetLayeredWindowAttrs.Call(h, 0, 255, lwaAlpha)
		}
		ok = r != 0
		fmt.Printf("[半透明] LWA_ALPHA(%v): %v (err=%v)\n", on, ok, err)
	})
	return ok
}
