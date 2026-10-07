//go:build windows

package platform

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
// 钩子与 [ulw] 皆未打出）。经 SetMainRun 注入断言：hide 走窗口线程，不得在调用方
// 协程直调 ShowWindow。
func TestHideUntilFirstPresentRoutesWindowThread(t *testing.T) {
	p := New(Config{})
	routed := false
	p.SetMainRun(func(f func()) { routed = true; f() }) // 假槽：记录后执行（假句柄 ShowWindow 落空无副作用）

	p.HideUntilFirstPresent(0x1234)

	if !routed {
		t.Fatal("HideUntilFirstPresent 未走窗口线程投递（直调 ShowWindow 违反铁律 1）")
	}
}

// TestAttachOrderKeepsFirstULW D78 启动时序回归（实机「窗口永不出现」）：挂接序列必须
// **先 SW_HIDE、后挂 WS_EX_LAYERED**（uigui.onHWND 即此序，本测在平台层钉住同一不变量）。
// 层样式在**可见态**挂接、随后首帧 ULW 之前被隐藏，UpdateLayeredWindow 将永久失败
// （实机 errno=87 ERROR_INVALID_PARAMETER，重新显示也不恢复）→ 揭示待定永不清零 → 窗口
// 永不揭示。复刻实机时序：建真窗 → ShowWindow（Gio Configure 早于 Win32ViewEvent）→
// 隐藏 → 挂分层样式 → 首帧 ULW。
func TestAttachOrderKeepsFirstULW(t *testing.T) {
	runtime.LockOSThread() // 窗口归属本线程，Show/ULW 同线程直调不跨线程回投
	defer runtime.UnlockOSThread()

	p := New(Config{})
	cls, _ := syscall.UTF16PtrFromString("STATIC")
	title, _ := syscall.UTF16PtrFromString("aquarius-uigui-platform-test")
	const wsPopup = 0x80000000
	const swShow = 5
	// 位置放屏外：本测试只验时序，避免测试窗在屏幕上闪现。
	h, _, _ := procCreateWindowExW.Call(0, uintptr(unsafe.Pointer(cls)),
		uintptr(unsafe.Pointer(title)), wsPopup, 0xFFFFF000, 0xFFFFF000, 8, 8, 0, 0, 0, 0)
	if h == 0 {
		t.Fatal("CreateWindowEx 失败")
	}
	procShowWindow.Call(h, swShow) // Gio Configure(ShowWindow) 早于 Win32ViewEvent
	defer procDestroyWindow.Call(h)

	p.AttachMain(Handle(h))
	p.HideUntilFirstPresent(Handle(h))
	p.EnsureLayered(Handle(h))
	p.SubclassCloseToHide(Handle(h))
	defer func() { shellPrevProc = 0 }() // 钩子随窗销毁；复位防污染后续测试

	bits := make([]byte, 8*8*4)
	if !p.Present(0, 0, 8, 8, bits, 255) {
		t.Fatal("挂接序（先隐藏、后挂样式）后首帧 ULW 失败——errno=87 = 层样式在可见态挂接后被隐藏")
	}
	if p.MainHandle() != Handle(h) {
		t.Fatalf("MainHandle = %#x, want %#x（AttachMain 未落库）", p.MainHandle(), h)
	}
}

// TestTopMostHandle HWND_TOPMOST(-1)/HWND_NOTOPMOST(-2) 的补码取值（置顶开关，§15.1）。
func TestTopMostHandle(t *testing.T) {
	if got := topMostHandle(true); got != ^uintptr(0) {
		t.Fatalf("topMostHandle(true) = %d, want -1", int64(got))
	}
	if got := topMostHandle(false); got != ^uintptr(1) {
		t.Fatalf("topMostHandle(false) = %d, want -2", int64(got))
	}
}

// TestTopMostDefaultNoHandle 无句柄（未挂接/headless）= 缺省置顶（口径与旧实现一致）。
func TestTopMostDefaultNoHandle(t *testing.T) {
	p := New(Config{})
	if !p.TopMost() {
		t.Fatal("未挂接主窗时应缺省置顶（缺省口径）")
	}
	if p.Visible() {
		t.Fatal("未挂接主窗时不可见（防隐藏后仍有帧提交）")
	}
}

// TestWindowDPIInvalidHandle 句柄无效 → 回落 1.0（100% 口径，D90）。
func TestWindowDPIInvalidHandle(t *testing.T) {
	p := New(Config{})
	if got := p.WindowDPI(0); got != 1.0 {
		t.Fatalf("WindowDPI(0) = %v, want 1.0（无 DPI 源回落）", got)
	}
	if got := p.MainDPI(); got != 1.0 {
		t.Fatalf("MainDPI()（未挂接）= %v, want 1.0", got)
	}
}
