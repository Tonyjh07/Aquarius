//go:build windows

// 托盘 + 全局快捷键 + 关窗拦截 + 菜单呈现线程（§15.1/D43；spike/giospike/win32.go 为
// 验证底本）：自建 HWND_MESSAGE 消息窗口 + 独立消息泵（LockOSThread，窗口消息由创建
// 线程分发），与窗口侧交接同 uigui 旧实现——修改性调用经 p.onWindowThread（§15.6
// 铁律 1），查询类直接调。
//
// 与 uigui 的交接改为 Host 回调（D111 修订②）：菜单项集、显隐/置顶/退出/文件选中/
// 系统主题变化等**语义动作**回宿主，平台层不解析业务。
package platform

import (
	"fmt"
	"runtime"
	"sync/atomic"
	"syscall"
	"unsafe"
)

var (
	procRegisterHotKey   = user32.NewProc("RegisterHotKey")
	procUnregisterHotKey = user32.NewProc("UnregisterHotKey")
	procGetMessageW      = user32.NewProc("GetMessageW")
	procTranslateMessage = user32.NewProc("TranslateMessage")
	procDispatchMessageW = user32.NewProc("DispatchMessageW")
	procPostQuitMessage  = user32.NewProc("PostQuitMessage")
	procPostMessageW     = user32.NewProc("PostMessageW")
	procCallWindowProcW  = user32.NewProc("CallWindowProcW")
)

// 消息泵/快捷键/子类化常量（Win32 头文件取值）。
const (
	wmNull          = 0x0000
	wmDestroy       = 0x0002
	wmClose         = 0x0010
	wmSettingChange = 0x001A // 系统设置变化广播（含系统深浅切换，§15.4/D61）
	wmCommand       = 0x0111
	wmLButtonUp     = 0x0202
	wmRButtonUp     = 0x0205
	wmHotkey        = 0x0312
	wmApp           = 0x8000
	rehotkeyMsg     = wmApp + 2 // 托盘线程内重注册全局快捷键（设置窗改 hotkey 投递，§15.1）

	gwlWndProc = ^uintptr(3) // GWLP_WNDPROC = -4（补码过 uintptr）

	hwndMessage = ^uintptr(2) // HWND_MESSAGE = -3
)

// shellMsg：x64 布局与 C 一致。
type shellMsg struct {
	hwnd    uintptr
	message uint32
	wParam  uintptr
	lParam  uintptr
	time    uint32
	pt      winPoint
}

// 托盘线程与窗口侧的交接状态（进程内单主窗，故为包级）。
var (
	shellHWND atomic.Uintptr // 托盘消息窗口（shell 线程写、事件循环读做 NIM_DELETE）

	// shellPrevProc 子类化前的原窗口过程——仅 Gio 窗口 goroutine 读写
	//（SetWindowLongPtr 闭包与 subClassProc 同线程），裸 uintptr 即可。
	shellPrevProc uintptr
)

// StartShell 启动托盘 + 全局快捷键线程（仅窗口模式调用；失败只记日志，不阻断 GUI）。
func (p *Plat) StartShell() {
	go p.runShell()
}

// runShell 托盘线程：消息窗口 + 快捷键注册 + 托盘图标 + 消息泵。
func (p *Plat) runShell() {
	runtime.LockOSThread()

	hInst, _, _ := procGetModuleHandleW.Call(0)
	cls, _ := syscall.UTF16PtrFromString("AquariusUiguiShell")
	var wc wndClassW
	wc.lpfnWndProc = syscall.NewCallback(p.shellWndProc)
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

	p.registerHotkey(hwnd)
	p.addTrayIcon(hwnd)

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

// registerHotkey 注册全局快捷键：Host.HotkeySetting → 默认 Alt+A；失败回退 Ctrl+Alt+A。
func (p *Plat) registerHotkey(hwnd uintptr) {
	combo, err := parseHotkey(p.hotkeySetting())
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

// ReloadHotkey 请求托盘线程重注册全局快捷键（设置窗改 hotkey 后）。注册归属托盘线程
// ——RegisterHotKey 归调用线程的消息队列，跨线程只能投消息（rehotkeyMsg）；shellHWND
// 未就绪 = 托盘线程未起（headless/非 Windows）→ no-op。
func (p *Plat) ReloadHotkey() {
	if h := shellHWND.Load(); h != 0 {
		procPostMessageW.Call(h, rehotkeyMsg, 0, 0)
	}
}

// ShutdownShell 注销全局快捷键并清托盘图标（菜单退出/前端收尾调用；幂等）。
func (p *Plat) ShutdownShell() {
	if h := shellHWND.Load(); h != 0 {
		procUnregisterHotKey.Call(h, 1)
		procUnregisterHotKey.Call(h, 2)
	}
	p.DropTray()
}

// shellWndProc 托盘消息窗口过程：托盘回调 / 快捷键 / 菜单命令。
func (p *Plat) shellWndProc(hwnd, uMsg, wParam, lParam uintptr) uintptr {
	switch uMsg {
	case trayCallback:
		switch lParam & 0xFFFF {
		case wmLButtonUp:
			if p.host != nil {
				p.host.ToggleShow()
			}
		case wmRButtonUp:
			p.showTrayMenu(hwnd)
		}
		return 0
	case wmHotkey:
		if p.host != nil {
			p.host.HotkeyPressed() // §15.1：隐藏 → 呼出展开；可见 → 展开↔收起互切
		}
		return 0
	case rehotkeyMsg: // 设置窗保存 hotkey → 托盘线程内换绑（失败自动回退 Ctrl+Alt+A）
		procUnregisterHotKey.Call(hwnd, 1)
		procUnregisterHotKey.Call(hwnd, 2)
		p.registerHotkey(hwnd)
		return 0
	case logoMenuMsg: // D72：Gio 侧检出 logo 右键 → 本线程呈现原生菜单
		p.showLogoMenu()
		return 0
	case bubbleMenuMsg: // D92：Gio 侧检出气泡右键 → 本线程呈现原生菜单（项集回调宿主现取）
		p.showBubbleMenu()
		return 0
	case fileDlgMsg: // D104：Gio 侧附件槽点击 → 本线程呈现文件选择框（模态泵不嵌 Gio 泵）
		p.showFileDlg()
		return 0
	case wmDestroy:
		procPostQuitMessage.Call(0)
		return 0
	}
	r, _, _ := procDefWindowProcW.Call(hwnd, uMsg, wParam, lParam)
	return r
}

// SubclassCloseToHide 子类化主窗过程：吞 WM_CLOSE → 隐藏（关窗 = 隐藏，§15.1）。
// Gio 无关闭拦截 API（WM_CLOSE 直落 DefWindowProc → DestroyWindow）。
// 经 onWindowThread 在窗口 goroutine 内执行：原过程指针的写入与 subClassProc
// 读取同 goroutine，无竞态；幂等（重复调用跳过）。
func (p *Plat) SubclassCloseToHide(h Handle) {
	p.onWindowThread(func() {
		if shellPrevProc != 0 {
			return
		}
		proc := syscall.NewCallback(p.subClassProc)
		r, _, _ := procSetWindowLongPtrW.Call(uintptr(h), gwlWndProc, proc)
		if r != 0 {
			shellPrevProc = r
			fmt.Println("[tray] WM_CLOSE 拦截已挂（关窗 = 隐藏）")
		}
	})
}

// subClassProc 子类化窗口过程：WM_CLOSE 隐藏；WM_SETTINGCHANGE（Explorer 广播
// 系统深浅等设置变化）→ 回调宿主重解析 system 档（§15.4/D61）；其余原样转发。
// 本回调在 Gio 窗口 goroutine 上执行，回调线程安全、此处不触修改性调用。
func (p *Plat) subClassProc(hwnd, uMsg, wParam, lParam uintptr) uintptr {
	if uMsg == wmClose {
		p.HideWindow(Handle(hwnd))
		return 0
	}
	if uMsg == wmSettingChange {
		if p.host != nil {
			p.host.SystemThemeChanged()
		}
		// 继续转发：Gio 侧不受影响，不吞广播。
	}
	if prev := shellPrevProc; prev != 0 {
		r, _, _ := procCallWindowProcW.Call(prev, hwnd, uMsg, wParam, lParam)
		return r
	}
	// 原过程尚未存好（微秒窗口）：兜底直落 DefWindowProc。
	r, _, _ := procDefWindowProcW.Call(hwnd, uMsg, wParam, lParam)
	return r
}

// HideMain 从任意线程隐藏主窗（托盘/菜单线程调用）：经窗口线程下发（铁律 1）。
// 窗口过程内的 WM_CLOSE 路径用 HideWindow 直调（同线程，避免 Run 回投自锁）。
func (p *Plat) HideMain(h Handle) {
	if h == 0 {
		return
	}
	p.onWindowThread(func() { p.HideWindow(h) })
}

// PostMenu 投递菜单呈现请求到托盘线程（呈现归 shell 线程的独立消息泵，TrackPopupMenu
// 不嵌 Gio 泵；D72 logo、D92 气泡）。shell 未就绪（启动微窗/headless/非 Windows 降级）
// = 发一次性降级提示并放弃。
func (p *Plat) PostMenu(kind MenuKind) {
	var msg uintptr
	switch kind {
	case MenuLogo:
		msg = logoMenuMsg
	case MenuBubble:
		msg = bubbleMenuMsg
	default:
		return
	}
	h := shellHWND.Load()
	if h == 0 {
		p.Notice(unsupported("原生右键菜单"))
		return
	}
	procPostMessageW.Call(h, msg, 0, 0)
}
