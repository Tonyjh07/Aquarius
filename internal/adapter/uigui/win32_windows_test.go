//go:build windows

package uigui

import (
	"runtime"
	"syscall"
	"testing"
	"unsafe"
)

var procDestroyWindow = user32.NewProc("DestroyWindow")

// TestHideUntilFirstPresentRoutesWindowThread D78 挂接即隐藏必须经窗口线程下发
// （§15.6 铁律 1）：onHWND 跑在事件循环客户端协程（runWindow 读 w.Event()），此际
// 窗口线程常停在 deliverEvent 的 select 把事件发给客户端、不泵消息——跨线程 ShowWindow
// 内部回投窗口过程会永久互锁，实机表现为启动即「未响应」（日志断在 logo、WM_CLOSE
// 钩子与 [ulw] 皆未打出）。经 win32Run 槽注入断言：hide 走 Window.Run，不得在调用方
// 协程直调 ShowWindow。
func TestHideUntilFirstPresentRoutesWindowThread(t *testing.T) {
	if mainHWND != 0 {
		t.Skip("测试进程内已有主窗句柄")
	}
	oldRun := win32Run.Load()
	defer func() { win32Run.Store(oldRun) }()
	routed := false
	fakeRun := func(f func()) { routed = true; f() } // 假槽：记录后执行（假句柄 ShowWindow 落空无副作用）
	win32Run.Store(&fakeRun)

	hideUntilFirstPresent(0x1234)

	if !routed {
		t.Fatal("hideUntilFirstPresent 未走窗口线程槽（直调 ShowWindow 违反铁律 1）")
	}
}

// TestOnHWNDOrderKeepsFirstULW D78 启动时序回归（实机「窗口永不出现」）：`onHWND` 必须
// **先 SW_HIDE、后挂 WS_EX_LAYERED**。层样式在**可见态**挂接、随后首帧 ULW 之前被隐藏，
// UpdateLayeredWindow 将永久失败（实机 errno=87 ERROR_INVALID_PARAMETER，重新显示也不恢复）
// → `revealPending` 永不清零 → 窗口永不揭示，且托盘/快捷键呼出被 revealPending 门死，
// 表现为「打开后找不到窗口、点了也没反应」。D78 前顺序为「挂样式（可见态）→ 不隐藏」故正常。
// 复刻实机时序：建真窗 → ShowWindow（Gio Configure 早于 Win32ViewEvent）→ onHWND → 首帧 ULW。
func TestOnHWNDOrderKeepsFirstULW(t *testing.T) {
	if mainHWND != 0 {
		t.Skip("测试进程内已有主窗句柄")
	}
	runtime.LockOSThread() // 窗口归属本线程，Show/ULW 同线程直调不跨线程回投
	defer runtime.UnlockOSThread()

	cls, _ := syscall.UTF16PtrFromString("STATIC")
	title, _ := syscall.UTF16PtrFromString("aquarius-uigui-test")
	const wsPopup = 0x80000000
	const swShow = 5
	// 位置放屏外：本测试只验时序，避免测试窗在屏幕上闪现。
	h, _, _ := procCreateWindowExW.Call(0, uintptr(unsafe.Pointer(cls)),
		uintptr(unsafe.Pointer(title)), wsPopup, 0xFFFFF000, 0xFFFFF000, 8, 8, 0, 0, 0, 0)
	if h == 0 {
		t.Fatal("CreateWindowEx 失败")
	}
	procShowWindow.Call(h, swShow) // Gio Configure(ShowWindow) 早于 Win32ViewEvent

	u := &UI{}
	u.onHWND(h)
	defer func() {
		mainHWND = 0
		shellPrevProc = 0 // subclassCloseToHide 挂的钩子随窗销毁；复位防污染后续测试
		procDestroyWindow.Call(h)
	}()

	bits := make([]byte, 8*8*4)
	if !mainPresent(0, 0, 8, 8, bits, 255) {
		t.Fatal("onHWND 后首帧 ULW 失败（errno=87 = 层样式在可见态挂接后被隐藏，须先隐藏再挂样式）")
	}
}
