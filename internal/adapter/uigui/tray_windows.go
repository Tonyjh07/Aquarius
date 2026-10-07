//go:build windows

// 托盘图标（Windows）：Shell_NotifyIconW 增删、内嵌 ico 解析呈现与托盘右键菜单
// 呈现入口（菜单项构造在 menu_windows.go，共享 runMenu）。
package uigui

import (
	"fmt"
	"syscall"
	"unsafe"

	"github.com/Tonyjh07/Aquarius/assets"
)

var shell32 = syscall.NewLazyDLL("shell32.dll")

var (
	procShellNotifyIconW         = shell32.NewProc("Shell_NotifyIconW")
	procLoadIconW                = user32.NewProc("LoadIconW")
	procCreateIconFromResourceEx = user32.NewProc("CreateIconFromResourceEx")
)

// 托盘常量（Win32 头文件取值）。
const (
	trayCallback = wmApp + 1 // 托盘回调消息

	nimAdd    = 0x00000000
	nimDelete = 0x00000002
	nifIcon   = 0x00000002
	nifTip    = 0x00000004
	nifMsg    = 0x00000001

	idIApplication = 32512 // IDI_APPLICATION（图标解析失败的系统回退）

	iconResVersion = 0x30000 // RT_ICON 资源版本（CreateIconFromResourceEx）
)

// notifyIconData：x64 布局与 C 一致。
type notifyIconData struct {
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

// showTrayMenu 托盘右键菜单呈现（§15.1：显示/隐藏、置顶开关、功能窗入口（§15.7，随各窗
// 步启用）+ 退出）；项集走中性 trayMenuItems，建单/呈现/分发走共享件（D72）。
func showTrayMenu(hwnd uintptr) {
	if u := shellUI.Load(); u != nil {
		menuDispatch(u, runMenu(hwnd, trayMenuItems()))
	}
}
