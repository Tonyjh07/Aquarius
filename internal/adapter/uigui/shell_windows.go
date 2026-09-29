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
	wmNull          = 0x0000
	wmDestroy       = 0x0002
	wmClose         = 0x0010
	wmSettingChange = 0x001A // 系统设置变化广播（含系统深浅切换，§15.4/D61）
	wmCommand       = 0x0111
	wmLButtonUp     = 0x0202
	wmRButtonUp     = 0x0205
	wmHotkey        = 0x0312
	wmApp           = 0x8000
	trayCallback    = wmApp + 1
	rehotkeyMsg     = wmApp + 2 // 托盘线程内重注册全局快捷键（设置窗改 hotkey 投递，§15.1）
	logoMenuMsg     = wmApp + 3 // 托盘线程呈现 logo 右键菜单（D72：Gio 检出右键后投递）

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
	mfChecked   = 0x00000008 // 勾选（置顶开关当前态）
	mfPopup     = 0x00000010 // 二级菜单：父项 uIDNewItem = 子菜单句柄（D73 权限档）
	tpmRightBtn = 0x0002
	tpmRetCmd   = 0x0100 // 返回命令 ID（不再发 WM_COMMAND）

	cmdToggle   = 101
	cmdExit     = 102
	cmdTopMost  = 103
	cmdHistory  = 104 // 功能窗入口（§15.7/D60：会话历史占位壳）
	cmdWelcome  = 105 // 功能窗入口（§15.7/D60：欢迎/首次运行占位壳）
	cmdSettings = 106 // 功能窗入口（§15.7/D60：设置窗核心档）
	cmdNew      = 107 // 新对话（logo 右键菜单，D72：注入 /new 与键入同路径）
	cmdPermBase = 108 // 权限子菜单四档基值（D73：+i 对齐 settings.go permLevels 序）

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
	case wmDestroy:
		procPostQuitMessage.Call(0)
		return 0
	}
	r, _, _ := procDefWindowProcW.Call(hwnd, uMsg, wParam, lParam)
	return r
}

// menuIt 菜单项（托盘/logo 右键菜单共用，D72/D73）；id 0 = 分隔线，或二级菜单父项
// （sub 非空时 id 不用）。
type menuIt struct {
	id      uintptr
	label   string
	checked bool
	sub     []menuIt // 非空 = 二级菜单（D73：权限四档）
}

// logoMenuItems logo 右键菜单（D72/D73，项序/文案为契约、测试锁定）：置顶勾选随
// topMostQuery（查询类，跨线程直调）。
func logoMenuItems(level string) []menuIt {
	permSub := make([]menuIt, 0, len(permLevels))
	for i, lv := range permLevels {
		permSub = append(permSub, menuIt{id: cmdPermBase + uintptr(i), label: lv, checked: lv == level})
	}
	return []menuIt{
		{id: cmdNew, label: "新对话"},
		{label: "权限", sub: permSub}, // D73：二级菜单四档，当前档打勾
		{id: cmdHistory, label: "消息历史"},
		{id: cmdSettings, label: "设置"},
		{id: cmdTopMost, label: "置顶", checked: topMostQuery()},
		{id: cmdToggle, label: "隐藏"},
		{}, // 分隔线
		{id: cmdExit, label: "退出"},
	}
}

// postLogoMenu 投递 logo 右键菜单请求到托盘线程（D72：呈现归 shell 线程的独立消息
// 泵，TrackPopupMenu 不嵌 Gio 泵）。shell 未就绪（启动微窗/headless）= 静默放弃。
func postLogoMenu() {
	if h := shellHWND.Load(); h != 0 {
		procPostMessageW.Call(h, logoMenuMsg, 0, 0)
	}
}

// showLogoMenu logo 右键菜单呈现（托盘线程，D72/D73）：项清单走共享件，owner = 托盘消息窗
// （与托盘菜单同款 TPM 收尾）。
func showLogoMenu() {
	h := shellHWND.Load()
	if h == 0 {
		return
	}
	if u := shellUI.Load(); u != nil {
		menuDispatch(u, runMenu(h, logoMenuItems(permLevelOf(u))))
	}
}

// permLevelOf 当前权限档（D73 子菜单勾选源）：Status 现取（shell 线程可跨 goroutine
// 读——lvl/agent 访问器均为原子，TUI 状态行同此回调）；未就绪 = ""（不勾，切换后自愈）。
func permLevelOf(u *UI) string {
	if u.opts.Status == nil {
		return ""
	}
	return u.opts.Status().Level
}

// menuDispatch 命令分发（托盘/logo 菜单共用，D72；托盘线程调用——openWin 锁内单
// 实例、修改性调用经 onWindowThread/post，§15.6 铁律 1）。
func menuDispatch(u *UI, r uintptr) {
	// 权限子菜单（D73）：cmdPermBase+i → /permission permLevels[i]（与键入同路径——
	// D22 config 写回 + Agent 热切换，菜单只做呈现与勾选）。
	if r >= cmdPermBase && r < cmdPermBase+uintptr(len(permLevels)) {
		_ = u.post(inputMsg{text: "/permission " + permLevels[r-cmdPermBase]})
		return
	}
	switch r {
	case cmdNew:
		_ = u.post(inputMsg{text: "/new"}) // 新对话：与键入同路径（parseInput → inCh）
	case cmdToggle:
		u.toggleWindow()
	case cmdTopMost:
		u.toggleTopMost()
	case cmdSettings:
		u.wins.openWin(winSettings) // 单实例防重开（§15.7；注册表线程安全）
	case cmdHistory:
		u.wins.openWin(winHistory)
	case cmdWelcome:
		u.wins.openWin(winWelcome)
	case cmdExit:
		u.exitViaShell()
	}
}

// buildMenu 建单（D73：二级菜单父项 MF_POPUP 挂子句柄、递归建单；DestroyMenu 对父单
// 递归销毁子单）。仅建单不显示。
func buildMenu(items []menuIt) uintptr {
	menu, _, _ := procCreatePopupMenu.Call()
	for _, it := range items {
		if len(it.sub) > 0 {
			sub := buildMenu(it.sub)
			label, _ := syscall.UTF16PtrFromString(it.label)
			procAppendMenuW.Call(menu, mfPopup, sub, uintptr(unsafe.Pointer(label)))
			continue
		}
		if it.id == 0 {
			procAppendMenuW.Call(menu, mfSeparator, 0, 0)
			continue
		}
		lab, _ := syscall.UTF16PtrFromString(it.label)
		f := uintptr(mfString)
		if it.checked {
			f |= mfChecked // 勾选 = 当前置顶态 / 当前权限档
		}
		procAppendMenuW.Call(menu, f, it.id, uintptr(unsafe.Pointer(lab)))
	}
	return menu
}

// runMenu 共享呈现件（D72/D73）：建单（buildMenu）→ 光标位 TrackPopupMenu
// （TPM_RETURNCMD 直接返回叶子命令，不发 WM_COMMAND）→ MSDN 收尾 WM_NULL → 销毁。
// 返回 0 = 取消。
func runMenu(hwnd uintptr, items []menuIt) uintptr {
	menu := buildMenu(items)
	var pt point
	procGetCursorPos.Call(uintptr(unsafe.Pointer(&pt)))
	procSetForegroundWindow.Call(hwnd)
	r, _, _ := procTrackPopupMenu.Call(menu, tpmRightBtn|tpmRetCmd,
		uintptr(pt.x), uintptr(pt.y), 0, hwnd, 0)
	procPostMessageW.Call(hwnd, wmNull, 0, 0) // MSDN 要求：收尾防菜单不消失
	procDestroyMenu.Call(menu)
	return r
}

// showTrayMenu 托盘右键菜单（§15.1：显示/隐藏、置顶开关、功能窗入口（§15.7，随各窗
// 步启用）+ 退出）；建单/呈现/分发走共享件（runMenu/menuDispatch，D72）。
func showTrayMenu(hwnd uintptr) {
	items := []menuIt{
		{id: cmdToggle, label: "显示 / 隐藏输入窗"},
		{id: cmdTopMost, label: "窗口置顶", checked: topMostQuery()},
		{},
		{id: cmdSettings, label: "设置"},
		{id: cmdHistory, label: "会话历史"},
		{id: cmdWelcome, label: "欢迎 / 首次运行引导"},
		{},
		{id: cmdExit, label: "退出"},
	}
	if u := shellUI.Load(); u != nil {
		menuDispatch(u, runMenu(hwnd, items))
	}
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
func (u *UI) showMain() {
	h := atomic.LoadUintptr(&mainHWND)
	if h == 0 {
		return
	}
	onWindowThread(func() {
		procShowWindow.Call(h, swRestore)
		procSetForegroundWindow.Call(h)
	})
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
