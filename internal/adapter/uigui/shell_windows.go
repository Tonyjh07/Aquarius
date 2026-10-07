//go:build windows

// 托盘 + 全局快捷键 + 关窗拦截（§15.1/D43；spike/giospike/win32.go 为验证底本）：
//   - 托盘：Shell_NotifyIconW，左键显隐、右键菜单（显示/隐藏 + 置顶 + 功能窗入口
//   - 退出；功能窗项随各窗步启用，§15.7）；logo 右键菜单经 PostMessage 投本线程
//     呈现（项清单含权限二级菜单，D72/D73；与托盘共享 runMenu/menuDispatch）；
//     图标内嵌 assets.TrayICO → 纯 Go 目录解析 → CreateIconFromResourceEx（单二进制）。
//   - 全局快捷键：RegisterHotKey 默认 Alt+A（ui.hotkey 可配），失败回退 Ctrl+Alt+A。
//   - 关窗（Alt+F4）= 隐藏：子类化主窗过程吞 WM_CLOSE（Gio 无关闭拦截 API）。
//
// 托盘线程自建 HWND_MESSAGE 消息窗口 + 独立消息泵（LockOSThread，窗口消息由创建
// 线程分发）。与窗口侧交接：修改性调用经 onWindowThread（§15.6 铁律 1）；查询类
// （IsWindowVisible 等）直接调。主窗过程回调与 onWindowThread 闭包同在 Gio 窗口
// goroutine 上执行（os_windows.go 消息泵 → ProcessEvent → deliverEvent），故
// hideMain/subClassProc 同 goroutine，无竞争。
package uigui

import (
	"fmt"
	"runtime"
	"sync/atomic"
	"syscall"
	"unsafe"
)

var (
	procRegisterHotKey      = user32.NewProc("RegisterHotKey")
	procUnregisterHotKey    = user32.NewProc("UnregisterHotKey")
	procGetMessageW         = user32.NewProc("GetMessageW")
	procTranslateMessage    = user32.NewProc("TranslateMessage")
	procDispatchMessageW    = user32.NewProc("DispatchMessageW")
	procPostQuitMessage     = user32.NewProc("PostQuitMessage")
	procPostMessageW        = user32.NewProc("PostMessageW")
	procIsWindowVisible     = user32.NewProc("IsWindowVisible")
	procSetForegroundWindow = user32.NewProc("SetForegroundWindow")
	procCallWindowProcW     = user32.NewProc("CallWindowProcW")
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

	swRestore = 9

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
	pt      point
}

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

// hotkeySetting 取快捷键配置（装配注入初值 + 设置窗热更新经原子槽；空 = 默认）。
func hotkeySetting() string {
	if u := shellUI.Load(); u != nil {
		if hk, ok := u.hotkeyCfg.Load().(string); ok {
			return hk
		}
		return u.opts.Hotkey // 兜底（newUI 预存之前的理论窗口）
	}
	return ""
}

// reRegisterHotkey 请求托盘线程重注册全局快捷键（设置窗改 hotkey 后）。注册归属
// 托盘线程——RegisterHotKey 归调用线程的消息队列，跨线程只能投消息（rehotkeyMsg）；
// shellHWND 未就绪 = 托盘线程未起（headless）→ no-op。
func reRegisterHotkey() {
	if h := shellHWND.Load(); h != 0 {
		procPostMessageW.Call(h, rehotkeyMsg, 0, 0)
	}
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
			u.hotkeyToggle() // §15.1：隐藏 → 呼出展开；可见 → 展开↔收起互切
		}
		return 0
	case rehotkeyMsg: // 设置窗保存 hotkey → 托盘线程内换绑（失败自动回退 Ctrl+Alt+A）
		procUnregisterHotKey.Call(hwnd, 1)
		procUnregisterHotKey.Call(hwnd, 2)
		registerHotkey(hwnd)
		return 0
	case logoMenuMsg: // D72：Gio 侧检出 logo 右键 → 本线程呈现原生菜单
		showLogoMenu()
		return 0
	case bubbleMenuMsg: // D92：Gio 侧检出气泡右键 → 本线程呈现原生菜单（上下文在原子槽）
		showBubbleMenu()
		return 0
	case fileDlgMsg: // D104：Gio 侧附件槽点击 → 本线程呈现文件选择框（模态泵不嵌 Gio 泵）
		if u := shellUI.Load(); u != nil {
			u.showFileDlg()
		}
		return 0
	case wmDestroy:
		procPostQuitMessage.Call(0)
		return 0
	}
	r, _, _ := procDefWindowProcW.Call(hwnd, uMsg, wParam, lParam)
	return r
}

// toggleTopMost 置顶开关（托盘/logo 菜单，§15.1/D72）：切换主窗 HWND_TOPMOST、持久化（与位置
// 记忆同文件）。D62：单窗单像素层，无 overlay 跟随步。
func (u *UI) toggleTopMost() {
	on := !topMostQuery()
	platformSetTopMost(on) // 主窗断言（内部经 onWindowThread，铁律 1）
	if u.opts.PosFile != "" {
		if rc, ok := windowRectPx(); ok { // 查询类：跨线程直接调
			tm := on
			var docked string // D50：停靠边随记忆保存（dockHint 原子镜像，跨线程读）
			if c := u.dockHint.Load(); c != dockNoneInt {
				docked = edgeName(c)
			}
			savePos(u.opts.PosFile, posRec{X: rc.left, Y: rc.top, TopMost: &tm, Docked: docked})
		}
	}
	fmt.Printf("[tray] 窗口置顶 → %v\n", on)
}

// toggleWindow 托盘显隐（§15.1：隐藏只经托盘；呼出 = 显示 + 展开 + 焦点入栏）。
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
	u.showMain()
}

// hotkeyToggle 快捷键（§15.1）：隐藏 → 呼出（showMain）；可见 → 展开 ↔ 收起互切。
func (u *UI) hotkeyToggle() {
	h := atomic.LoadUintptr(&mainHWND)
	if h == 0 {
		return
	}
	visible, _, _ := procIsWindowVisible.Call(h)
	if visible == 0 {
		u.showMain()
		return
	}
	u.post(toggleExpandMsg{})
}

// showMain 呼出：显示 + 前台 + 展开输入栏 + 焦点入栏（§15.1 呼出 = 展开）。
// D78：首帧 ULW 未提交前（revealPending）**不显示窗口**——只投展开态，窗口由首帧
// 揭示（防提前 ShowWindow 闪现未定制窗口）；就绪后经 revealMain 揭示（显示 + 激活前台）。
func (u *UI) showMain() {
	h := atomic.LoadUintptr(&mainHWND)
	if h == 0 {
		return
	}
	if u.revealPending.Load() {
		u.post(showExpandMsg{}) // 只置展开/焦点态（拍板：首帧前呼出忽略显示）
		return
	}
	revealMain(h)
	u.representAfterShow() // D88：呼出重显后补提交（隐藏期间无 ULW，防裸 Gio 表面）
	u.post(showExpandMsg{})
}

// hideMain 主窗隐藏（像素层随窗口一并消失，D62 后无独立 overlay）——必须在 Gio
// 窗口 goroutine 执行（帧循环隐藏路径与 WM_CLOSE 拦截共用）。
func hideMain(h uintptr) {
	procShowWindow.Call(h, swHide)
}

// exitViaShell 菜单退出（托盘/logo 右键菜单共用，§15.1/D72：退出只经菜单）——注销快捷键、清托盘图标、
// 隐藏主窗，**中断进行中轮次**（D64：退出即终止，不等待 Agent 完成——装配根阻塞在
// Turn 内时 EOF 不可见，先经停止键同款取消通道解卷）再走 EOF 收尾（装配根 Next →
// io.EOF → Close → 事件循环退出，退出码 0）。
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
	u.interruptNow() // D64：进行中的 Turn 立即取消（无轮进行 = nop，空闲退出不受影响）
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

// subClassProc 子类化窗口过程：WM_CLOSE 隐藏；WM_SETTINGCHANGE（Explorer 广播
// 系统深浅等设置变化）→ sysThemeMsg 重解析 system 档（§15.4/D61）；其余原样转发。
// 本回调在 Gio 窗口 goroutine 上执行，post 线程安全、此处不触修改性调用。
func subClassProc(hwnd, uMsg, wParam, lParam uintptr) uintptr {
	if uMsg == wmClose {
		hideMain(hwnd)
		return 0
	}
	if uMsg == wmSettingChange {
		if u := shellUI.Load(); u != nil {
			u.post(sysThemeMsg{})
		}
		// 继续转发：Gio 侧不受影响，不吞广播。
	}
	if p := shellPrevProc; p != 0 {
		r, _, _ := procCallWindowProcW.Call(p, hwnd, uMsg, wParam, lParam)
		return r
	}
	// 原过程尚未存好（微秒窗口）：兜底直落 DefWindowProc。
	r, _, _ := procDefWindowProcW.Call(hwnd, uMsg, wParam, lParam)
	return r
}
