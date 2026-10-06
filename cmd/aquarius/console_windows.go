//go:build windows

// 双击静默启动（D108/§15.1）：console 子系统二进制被双击/资源管理器/开始菜单启动时，
// Windows 为本次启动新建控制台窗口，日志随窗直露。运行时判定而非编译期
// -H=windowsgui（单二进制须同时服务终端启动）：GetConsoleProcessList 只挂载本进程
// （=1）即控制台为本次启动而生 → 隐藏；终端启动 shell 同挂载（≥2）不动，日志照常。
package main

import (
	"syscall"
	"unsafe"
)

var (
	kernel32 = syscall.NewLazyDLL("kernel32.dll")
	user32   = syscall.NewLazyDLL("user32.dll")

	procGetConsoleProcessList = kernel32.NewProc("GetConsoleProcessList")
	procGetConsoleWindow      = kernel32.NewProc("GetConsoleWindow")
	procShowWindow            = user32.NewProc("ShowWindow")
)

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
	consoleShow = func(h uintptr, cmdShow int) {
		procShowWindow.Call(h, uintptr(cmdShow))
	}
)

const (
	swHide = 0 // ShowWindow：隐藏窗口
	swShow = 5 // ShowWindow：显示并激活
)

// consoleHidden 自建控制台已隐藏标记（run() 主 goroutine 独占读写，无并发）。
var consoleHidden bool

// hideSpawnedConsole 双击静默启动（D108）：仅当本进程是控制台唯一挂载者时隐藏
// （返回 true）；终端启动（挂载 ≥2）、无控制台（hwnd=0）与已隐藏均 no-op。
func hideSpawnedConsole() bool {
	if consoleHidden || consoleProcCount() > 1 {
		return consoleHidden
	}
	if hwnd := consoleWindow(); hwnd != 0 {
		consoleShow(hwnd, swHide)
		consoleHidden = true
	}
	return consoleHidden
}

// restoreConsole 恢复被 hideSpawnedConsole 隐藏的控制台（repl/tui 前端——终端即
// 界面，隐藏即废）；非自建隐藏态 no-op。
func restoreConsole() {
	if !consoleHidden {
		return
	}
	consoleHidden = false
	if hwnd := consoleWindow(); hwnd != 0 {
		consoleShow(hwnd, swShow)
	}
}
