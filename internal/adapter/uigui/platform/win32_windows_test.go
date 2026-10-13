//go:build windows

package platform

import (
	"runtime"
	"syscall"
	"testing"
	"unsafe"
)

var (
	procDestroyWindow = user32.NewProc("DestroyWindow")
	psapi             = syscall.NewLazyDLL("psapi.dll")
	// GetProcessMemoryInfo 在 psapi（GDI 堆计入进程 private commit，故可作泄漏尺）。
	procGetProcessMemoryInfo = psapi.NewProc("GetProcessMemoryInfo")
)

// procMemoryCounters PROCESS_MEMORY_COUNTERS（仅取 cb 与 PrivateUsage 字段）。
type procMemoryCounters struct {
	cb, pageFaultCount               uint32
	peakWorkingSetSize               uintptr
	workingSetSize                   uintptr
	quotaPeakPagedPoolUsage          uintptr
	quotaPagedPoolUsage              uintptr
	quotaPeakNonPagedPoolUsage       uintptr
	quotaNonPagedPoolUsage           uintptr
	pagefileUsage, peakPagefileUsage uintptr
	privateUsage                     uintptr
}

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

// TestEnsureDIBResizeReleases D134：窗口尺寸变化时重建 DIB，**旧位图必须真被释放**。
//
// 外部审查报此处「每次变尺寸泄漏一个 w×h×4 像素缓冲」——本测试连同规模实测（见
// ensureDIB 注释）把该结论钉死为**不成立**：DeleteObject 对选入内存 DC 的 DIB 有效。
// 判据用两把尺：① 释放返回值查证计数器（dibReleaseFailN）必须恒 0——真失败立即报；
// ② 进程 private bytes 在多轮变尺寸后不得增长一张位图的量（真泄漏则线性增长，
// 阳性对照实测 40 轮 × 1200×800 故意漏删 = +146.9MB）。
//
// 不用 GetGuiResources 数 GDI 对象（实测本会话恒返回 0，不可作判据）；也不用
// GetObjectType 探句柄（GDI 句柄槽位会被立即复用，删完探仍是 OBJ_BITMAP = 假阳性）。
func TestEnsureDIBResizeReleases(t *testing.T) {
	defer func() {
		if mbits.hbm != 0 {
			procDeleteObject.Call(mbits.hbm)
			procDeleteDC.Call(mbits.hdc)
			mbits.hbm, mbits.hdc, mbits.ptr = 0, 0, nil
		}
	}()

	const (
		w, h    = 1200, 800 // ≈3.7MB/张，泄漏时增长显著
		rounds  = 12
		slackMB = 8 // 允许的噪声（一张位图 + 运行时杂项）
	)
	before := privateMB(t)
	for i := 0; i < rounds; i++ {
		// 变尺寸 ⇒ 走重建分支（尺寸相同则 ensureDIB 直接复用，不释放）。
		if err := ensureDIB(int32(w), int32(h+i)); err != nil {
			t.Fatalf("ensureDIB = %v", err)
		}
	}
	if dibReleaseFailN != 0 {
		t.Fatalf("释放异常 %d 次：DeleteObject/DeleteDC 有失败（旧位图泄漏）", dibReleaseFailN)
	}
	if before < 0 {
		t.Skip("GetProcessMemoryInfo 不可用，跳过内存增长判据")
	}
	if grew := privateMB(t) - before; grew > slackMB {
		t.Fatalf("变尺寸 %d 轮后进程 private +%.1fMB（判据 %dMB）= 每轮泄漏一张 DIB",
			rounds, grew, slackMB)
	}
}

// privateMB 进程 private bytes（GetProcessMemoryInfo）；不可用返回 -1。
func privateMB(t *testing.T) float64 {
	t.Helper()
	self, _ := syscall.GetCurrentProcess()
	var c procMemoryCounters
	c.cb = uint32(unsafe.Sizeof(c))
	if r, _, _ := procGetProcessMemoryInfo.Call(uintptr(self),
		uintptr(unsafe.Pointer(&c)), uintptr(c.cb)); r == 0 {
		return -1
	}
	return float64(c.privateUsage) / (1 << 20)
}
