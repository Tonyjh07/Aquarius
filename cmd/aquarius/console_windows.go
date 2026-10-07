//go:build windows

// 双击静默启动（D108/§15.1，D114 修订）：console 子系统二进制被双击/资源管理器/
// 开始菜单启动时，Windows 为本次启动新建控制台窗口，日志随窗直露。运行时判定而非
// 编译期 -H=windowsgui（单二进制须同时服务终端启动）：GetConsoleProcessList 只挂载
// 本进程（=1）即控制台为本次启动而生 → FreeConsole 脱离——客户端脱离即控制台会话
// 销毁，可见终端（WT 标签页/经典 conhost 窗）与任务栏条目随之关闭。【D114】原
// SW_HIDE 机制作废：WT 作默认终端时 GetConsoleWindow 返回 conhost 不可见伪窗
// （visible=false，实测），可见终端是 WT 窗口，句柄不可达、隐藏视觉上是 no-op。
// 终端启动 shell 同挂载（≥2）不动，日志照常。
package main

import (
	"os"
	"syscall"
	"unsafe"
)

var (
	kernel32 = syscall.NewLazyDLL("kernel32.dll")
	user32   = syscall.NewLazyDLL("user32.dll")

	procGetConsoleProcessList = kernel32.NewProc("GetConsoleProcessList")
	procGetConsoleWindow      = kernel32.NewProc("GetConsoleWindow")
	procFreeConsole           = kernel32.NewProc("FreeConsole")
	procAllocConsole          = kernel32.NewProc("AllocConsole")
	procAttachConsole         = kernel32.NewProc("AttachConsole")
	procMessageBoxW           = user32.NewProc("MessageBoxW")
)

// attachParent ATTACH_PARENT_PROCESS = (DWORD)-1（x64 传参零扩展 32 位值）。
const attachParent = 0xFFFFFFFF

// Win32 面函数变量：测试注入假实现用（生产恒为真实 syscall 薄封装）。
var (
	consoleProcCount = func() int {
		var pids [16]uint32
		n, _, _ := procGetConsoleProcessList.Call(
			uintptr(unsafe.Pointer(&pids[0])), uintptr(len(pids)))
		return int(n)
	}
	consoleWindow = func() uintptr {
		h, _, _ := procGetConsoleWindow.Call()
		return h
	}
	consoleDetach = func() bool {
		r, _, _ := procFreeConsole.Call()
		return r != 0
	}
	consoleAttach = func() bool {
		r, _, _ := procAttachConsole.Call(attachParent)
		return r != 0
	}
	consoleAlloc = func() bool {
		r, _, _ := procAllocConsole.Call()
		return r != 0
	}
	consoleReopenStd = func() {
		if f, err := os.OpenFile(`\\.\CONIN$`, os.O_RDWR, 0); err == nil {
			os.Stdin = f
		}
		if f, err := os.OpenFile(`\\.\CONOUT$`, os.O_RDWR, 0); err == nil {
			os.Stdout = f
			os.Stderr = f
		}
	}
	messageBox = func(title, text string) {
		tp, _ := syscall.UTF16PtrFromString(title)
		mp, _ := syscall.UTF16PtrFromString(text)
		procMessageBoxW.Call(0, uintptr(unsafe.Pointer(mp)),
			uintptr(unsafe.Pointer(tp)), mbIconError)
	}
)

const mbIconError = 0x10 // MessageBoxW：错误图标

// consoleDetached 控制台已由本进程脱离标记（run() 主 goroutine 独占读写，无并发）。
var consoleDetached bool

// hideSpawnedConsole 双击静默启动（D108，D114 修订为 FreeConsole）：仅当本进程是
// 控制台唯一挂载者时脱离（返回 true）；终端启动（挂载 ≥2）、无控制台（hwnd=0）与
// 已脱离均 no-op。
func hideSpawnedConsole() bool {
	if consoleDetached || consoleProcCount() > 1 {
		return consoleDetached
	}
	if hwnd := consoleWindow(); hwnd != 0 {
		if consoleDetach() {
			consoleDetached = true
		}
	}
	return consoleDetached
}

// restoreConsole repl/tui 控制台兜底（D114）：脱离后终端即界面而控制台已销毁——先试
// AttachConsole(parent)（挂回宿主终端），失败 AllocConsole 新建；随后重挂标准流
// （原句柄随脱离失效，重挂后 bootstrap 经 wiring 下发给前端）。返回是否执行了恢复；
// 未脱离态 no-op（终端启动从未脱离，流保持原样）。
func restoreConsole() bool {
	if !consoleDetached {
		return false
	}
	consoleDetached = false
	if !consoleAttach() {
		consoleAlloc()
	}
	consoleReopenStd()
	return true
}

// notifyFatal 启动期致命错误的兜底呈现（D110 修订④）：stderr 对双击启动的用户不可用
// （D114 已脱离控制台，写入静默丢弃），此时经系统消息框呈现；控制台可用（终端启动或
// repl/tui 已恢复重挂）时 stderr 即达，no-op。
func notifyFatal(msg string) {
	if consoleDetached {
		messageBox("Aquarius", msg)
	}
}
