//go:build windows

// 托盘 + 全局快捷键 + 关窗拦截（§15.1/D43；spike/giospike/win32.go 为验证底本）：
//   - 托盘：Shell_NotifyIconW，左键显隐、右键菜单（MVP：显示/隐藏 + 退出）；
//     图标内嵌 assets.TrayICO → 纯 Go 目录解析 → CreateIconFromResourceEx（单二进制）。
//   - 全局快捷键：RegisterHotKey 默认 Alt+A（ui.hotkey 可配），失败回退 Ctrl+Alt+A。
//   - 关窗（Alt+F4）= 隐藏：子类化主窗过程吞 WM_CLOSE（Gio 无关闭拦截 API）。
//
// 托盘线程自建 HWND_MESSAGE 消息窗口 + 独立消息泵（LockOSThread，窗口消息由创建
// 线程分发）。与窗口侧交接：修改性调用经 onWindowThread（§15.6 铁律 1）；查询类
// （IsWindowVisible 等）直接调。主窗过程回调与 onWindowThread 闭包同在 Gio 窗口
// goroutine 上执行（os_windows.go 消息泵 → ProcessEvent → deliverEvent），故
// hideMain/subClassProc 可直读 ovl 与 shellPrevProc，无竞争。
package uigui

import (
	"fmt"
	"runtime"
	"sync/atomic"
	"syscall"
	"unsafe"

	"github.com/Tonyjh07/Aquarius/assets"
)

var shell32 = syscall.NewLazyDLL("shell32.dll")

var (
	procShellNotifyIconW         = shell32.NewProc("Shell_NotifyIconW")
	procRegisterHotKey           = user32.NewProc("RegisterHotKey")
	procUnregisterHotKey         = user32.NewProc("UnregisterHotKey")
	procGetMessageW              = user32.NewProc("GetMessageW")
	procTranslateMessage         = user32.NewProc("TranslateMessage")
	procDispatchMessageW         = user32.NewProc("DispatchMessageW")
	procPostQuitMessage          = user32.NewProc("PostQuitMessage")
	procPostMessageW             = user32.NewProc("PostMessageW")
	procIsWindowVisible          = user32.NewProc("IsWindowVisible")
	procSetForegroundWindow      = user32.NewProc("SetForegroundWindow")
	procCreatePopupMenu          = user32.NewProc("CreatePopupMenu")
	procAppendMenuW              = user32.NewProc("AppendMenuW")
	procTrackPopupMenu           = user32.NewProc("TrackPopupMenu")
	procDestroyMenu              = user32.NewProc("DestroyMenu")
	procLoadIconW                = user32.NewProc("LoadIconW")
	procCallWindowProcW          = user32.NewProc("CallWindowProcW")
	procCreateIconFromResourceEx = user32.NewProc("CreateIconFromResourceEx")
)

// 托盘/快捷键/子类化常量（Win32 头文件取值）。
const (
	wmNull       = 0x0000
	wmDestroy    = 0x0002
	wmClose      = 0x0010
	wmCommand    = 0x0111
	wmLButtonUp  = 0x0202
	wmRButtonUp  = 0x0205
	wmHotkey     = 0x0312
	wmApp        = 0x8000
	trayCallback = wmApp + 1

	swRestore = 9

	gwlWndProc = ^uintptr(3) // GWLP_WNDPROC = -4（补码过 uintptr）

	hwndMessage = ^uintptr(2) // HWND_MESSAGE = -3

	nimAdd    = 0x00000000
	nimDelete = 0x00000002
	nifIcon   = 0x00000002
	nifTip    = 0x00000004
	nifMsg    = 0x00000001

	mfString    = 0x00000000
	mfSeparator = 0x00000800
	tpmRightBtn = 0x0002
	tpmRetCmd   = 0x0100 // 返回命令 ID（不再发 WM_COMMAND）

	cmdToggle = 101
	cmdExit   = 102

	idIApplication = 32512 // IDI_APPLICATION（图标解析失败的系统回退）

	iconResVersion = 0x30000 // RT_ICON 资源版本（CreateIconFromResourceEx）
)

// shellMsg / notifyIconData：x64 布局与 C 一致。
type (
	shellMsg struct {
		hwnd    uintptr
		message uint32
		wParam  uintptr
		lParam  uintptr
		time    uint32
		pt      point
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

// 托盘线程与窗口侧的交接状态。
var (
	shellUI   atomic.Pointer[UI] // 托盘/快捷键回调目标（startShell 一次写入）
	shellHWND atomic.Uintptr     // 托盘消息窗口（shell 线程写、事件循环读做 NIM_DELETE）

	// shellPrevProc 子类化前的原窗口过程——仅 Gio 窗口 goroutine 读写
	//（SetWindowLongPtr 闭包与 subClassProc 同线程），裸 uintptr 即可。
	shellPrevProc uintptr
)

// startShell 启动托盘 + 全局快捷键线程（仅窗口模式调用；失败只记日志，不阻断 GUI）。
func startShell(u *UI) {
	shellUI.Store(u)
	go runShell()
}

// runShell 托盘线程：消息窗口 + 快捷键注册 + 托盘图标 + 消息泵。
func runShell() {
	runtime.LockOSThread()

	hInst, _, _ := procGetModuleHandleW.Call(0)
	cls, _ := syscall.UTF16PtrFromString("AquariusUiguiShell")
	var wc wndClassW
	wc.lpfnWndProc = syscall.NewCallback(shellWndProc)
	wc.hInstance = hInst
	wc.lpszClassName = cls
	if r, _, _ := procRegisterClassW.Call(uintptr(unsafe.Pointer(&wc))); r == 0 {
		// 同进程二次注册（理论不发生）不算错误，继续用。
	}
	hwnd, _, err := procCreateWindowExW.Call(0, uintptr(unsafe.Pointer(cls)), 0,
		0, 0, 0, 0, 0, hwndMessage, 0, hInst, 0)
	if hwnd == 0 {
		fmt.Printf("[tray] 消息窗口创建失败: %v\n", err)
		return
	}
	shellHWND.Store(hwnd)

	registerHotkey(hwnd)
	addTrayIcon(hwnd)

	var m shellMsg
	for {
		r, _, _ := procGetMessageW.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0)
		if int32(r) <= 0 {
			return
		}
		procTranslateMessage.Call(uintptr(unsafe.Pointer(&m)))
		procDispatchMessageW.Call(uintptr(unsafe.Pointer(&m)))
	}
}

// registerHotkey 注册全局快捷键：opts.Hotkey → 默认 Alt+A；失败回退 Ctrl+Alt+A。
func registerHotkey(hwnd uintptr) {
	combo, err := parseHotkey(hotkeySetting())
	if err != nil {
		fmt.Printf("[hotkey] 配置非法(%v)，用默认 %s\n", err, defaultHotkey)
		combo, _ = parseHotkey(defaultHotkey)
	}
	if r, _, _ := procRegisterHotKey.Call(hwnd, 1, combo.mods, combo.vk); r != 0 {
		fmt.Printf("[hotkey] RegisterHotKey(%s) = 成功\n", combo.label)
		return
	}
	fb := fallbackHotkey()
	fmt.Printf("[hotkey] RegisterHotKey(%s) = 失败，回退 %s\n", combo.label, fb.label)
	if r, _, _ := procRegisterHotKey.Call(hwnd, 2, fb.mods, fb.vk); r != 0 {
		fmt.Printf("[hotkey] 回退 %s = 成功\n", fb.label)
		return
	}
	fmt.Println("[hotkey] 两档均失败，呼出仅剩托盘")
}

// hotkeySetting 取装配注入的快捷键配置（空 = 默认）。
func hotkeySetting() string {
	if u := shellUI.Load(); u != nil {
		return u.opts.Hotkey
	}
	return ""
}

// addTrayIcon 托盘图标 + 提示（v3 回调：legacy WM_LBUTTONUP/WM_RBUTTONUP）。
func addTrayIcon(hwnd uintptr) {
	icon := trayIcon()
	var nid notifyIconData
	nid.cbSize = uint32(unsafe.Sizeof(nid))
	nid.hWnd = hwnd
	nid.uID = 1
	nid.uFlags = nifIcon | nifTip | nifMsg
	nid.uCallbackMessage = trayCallback
	nid.hIcon = icon
	copy(nid.szTip[:], syscall.StringToUTF16("Aquarius"))
	if r, _, err := procShellNotifyIconW.Call(nimAdd, uintptr(unsafe.Pointer(&nid))); r != 0 {
		fmt.Println("[tray] Shell_NotifyIconW(NIM_ADD) = 成功（左键显隐，右键菜单）")
	} else {
		fmt.Printf("[tray] Shell_NotifyIconW(NIM_ADD) = 失败: %v\n", err)
	}
}

// trayIcon 托盘图标：内嵌 ico → 16px 档 → CreateIconFromResourceEx；
// 失败回落系统默认图标（spike 口径）。
func trayIcon() uintptr {
	if b, err := icoImage(assets.TrayICO, 16); err == nil && len(b) > 0 {
		if h, _, _ := procCreateIconFromResourceEx.Call(
			uintptr(unsafe.Pointer(&b[0])), uintptr(len(b)), 1, iconResVersion, 0, 0); h != 0 {
			return h
		}
	}
	h, _, _ := procLoadIconW.Call(0, idIApplication)
	return h
}

// trayDelete 移除托盘图标（NIM_DELETE 自带消息投递，线程无关；幂等）。
// 关窗销毁与退出清理两处调用，防悬浮区残留。
func trayDelete() {
	h := shellHWND.Load()
	if h == 0 {
		return
	}
	var nid notifyIconData
	nid.cbSize = uint32(unsafe.Sizeof(nid))
	nid.hWnd = h
	nid.uID = 1
	procShellNotifyIconW.Call(nimDelete, uintptr(unsafe.Pointer(&nid)))
	shellHWND.Store(0)
}

// shellWndProc 托盘消息窗口过程：托盘回调 / 快捷键 / 菜单命令。
func shellWndProc(hwnd, uMsg, wParam, lParam uintptr) uintptr {
	switch uMsg {
	case trayCallback:
		switch lParam & 0xFFFF {
		case wmLButtonUp:
			if u := shellUI.Load(); u != nil {
				u.toggleWindow()
			}
		case wmRButtonUp:
			showTrayMenu(hwnd)
		}
		return 0
	case wmHotkey:
		if u := shellUI.Load(); u != nil {
			u.toggleWindow()
		}
		return 0
	case wmDestroy:
		procPostQuitMessage.Call(0)
		return 0
	}
	r, _, _ := procDefWindowProcW.Call(hwnd, uMsg, wParam, lParam)
	return r
}

// showTrayMenu 右键菜单（§15.1 MVP：显示/隐藏 + 退出；会话/主题项随菜单步补全）。
// TPM_RETURNCMD：TrackPopupMenu 直接返回命令（不发 WM_COMMAND）。
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
	r, _, _ := procTrackPopupMenu.Call(menu, tpmRightBtn|tpmRetCmd,
		uintptr(pt.x), uintptr(pt.y), 0, hwnd, 0)
	procPostMessageW.Call(hwnd, wmNull, 0, 0) // MSDN 要求：收尾防菜单不消失
	procDestroyMenu.Call(menu)
	switch r {
	case cmdToggle:
		if u := shellUI.Load(); u != nil {
			u.toggleWindow()
		}
	case cmdExit:
		if u := shellUI.Load(); u != nil {
			u.exitViaShell()
		}
	}
}

// toggleWindow 呼出/收起主窗（托盘左键、快捷键、菜单共用，§15.1）。
// 显示时抢前台 + 投 focusMsg（下帧焦点进输入栏）；隐藏走窗口线程直落 hideMain。
func (u *UI) toggleWindow() {
	h := atomic.LoadUintptr(&mainHWND)
	if h == 0 {
		return // 主窗未就绪（Win32ViewEvent 晚到）：忽略本次触发
	}
	visible, _, _ := procIsWindowVisible.Call(h) // 查询类：直接调（铁律 1 不限）
	if visible != 0 {
		onWindowThread(func() { hideMain(h) })
		return
	}
	onWindowThread(func() {
		procShowWindow.Call(h, swRestore)
		procSetForegroundWindow.Call(h)
	})
	u.post(focusMsg{})
}

// hideMain 主窗与淡出 overlay 一并隐藏——必须在 Gio 窗口 goroutine 执行
// （overlay 归属该线程；帧循环隐藏路径与 WM_CLOSE 拦截共用）。
func hideMain(h uintptr) {
	procShowWindow.Call(h, swHide)
	if ovl.hwnd != 0 && ovl.visible {
		procShowWindow.Call(ovl.hwnd, swHide)
		ovl.visible = false
	}
}

// exitViaShell 托盘菜单退出（§15.1：退出只经菜单）——注销快捷键、清托盘图标、
// 隐藏主窗，走 EOF 收尾（装配根 Next → io.EOF → Close → 事件循环退出，退出码 0）。
func (u *UI) exitViaShell() {
	if h := shellHWND.Load(); h != 0 {
		procUnregisterHotKey.Call(h, 1)
		procUnregisterHotKey.Call(h, 2)
	}
	trayDelete()
	if h := atomic.LoadUintptr(&mainHWND); h != 0 {
		onWindowThread(func() { hideMain(h) })
	}
	fmt.Println("[tray] 菜单退出")
	u.signalEOF(nil)
}

// subclassCloseToHide 子类化主窗过程：吞 WM_CLOSE → 隐藏（关窗 = 隐藏，§15.1）。
// Gio 无关闭拦截 API（WM_CLOSE 直落 DefWindowProc → DestroyWindow）。
// 经 onWindowThread 在窗口 goroutine 内执行：原过程指针的写入与 subClassProc
// 读取同 goroutine，无竞态；幂等（重复调用跳过）。
func subclassCloseToHide(h uintptr) {
	onWindowThread(func() {
		if shellPrevProc != 0 {
			return
		}
		proc := syscall.NewCallback(subClassProc)
		r, _, _ := procSetWindowLongPtrW.Call(h, gwlWndProc, proc)
		if r != 0 {
			shellPrevProc = r
			fmt.Println("[tray] WM_CLOSE 拦截已挂（关窗 = 隐藏）")
		}
	})
}

// subClassProc 子类化窗口过程：WM_CLOSE 隐藏，其余原样转发。
func subClassProc(hwnd, uMsg, wParam, lParam uintptr) uintptr {
	if uMsg == wmClose {
		hideMain(hwnd)
		return 0
	}
	if p := shellPrevProc; p != 0 {
		r, _, _ := procCallWindowProcW.Call(p, hwnd, uMsg, wParam, lParam)
		return r
	}
	// 原过程尚未存好（微秒窗口）：兜底直落 DefWindowProc。
	r, _, _ := procDefWindowProcW.Call(hwnd, uMsg, wParam, lParam)
	return r
}
