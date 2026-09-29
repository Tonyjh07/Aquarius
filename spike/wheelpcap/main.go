// spike/wheelpcap —— 滚轮路由实证（D71 设计依据，§15.6 spike 纪律）：
// 分层窗在光标下时 WM_MOUSEWHEEL 的路由由什么决定？四段观察：
//
//	A0 不透明窗（注入可达性 + 前台态下是否收得到滚轮）
//	A1 全透明窗（ULW alpha=0，前台不变）——「逐像素命中穿透 → 滚轮流失」模型
//	B  全透明窗 + SetCapture ——「捕获期间滚轮路由到本窗」（D71 修复假设）
//	C  ReleaseCapture 后复归 A1 口径
//
// 判读：位置路由模型 = A0 收 / A1 不收 / B 收 / C 不收；焦点路由模型 = 全收。
// 输入用 SendInput(MOUSEEVENTF_WHEEL) 注入，光标全程不动（GetCursorPos 仅查询）。
package main

import (
	"fmt"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unsafe"
)

const (
	wsPopup        = 0x80000000
	wsVisible      = 0x10000000
	wsExLayered    = 0x00080000
	wsExTopMost    = 0x00000008
	wsExToolWindow = 0x00000080

	wmMouseWheel = 0x020A
	wmQuit       = 0x0012
	pmRemove     = 0x0001

	inputMouse       = 0
	mouseEventFWheel = 0x0800
	wheelDelta       = 120

	ulwAlpha   = 0x00000002
	acSrcAlpha = 1

	swRestore = 9
	idcArrow  = 32512
)

type point struct{ x, y int32 }
type rect struct{ left, top, right, bottom int32 }

type wndClassW struct {
	style         uint32
	lpfnWndProc   uintptr
	cbClsExtra    int32
	cbWndExtra    int32
	hInstance     uintptr
	hIcon         uintptr
	hCursor       uintptr
	hbrBackground uintptr
	lpszMenuName  *uint16
	lpszClassName *uint16
}

type bitmapInfoHeader struct {
	biSize          uint32
	biWidth         int32
	biHeight        int32
	biPlanes        uint16
	biBitCount      uint16
	biCompression   uint32
	biSizeImage     uint32
	biXPelsPerMeter int32
	biYPelsPerMeter int32
	biClrUsed       uint32
	biClrImportant  uint32
}

type blendFunc struct {
	blendOp             uint8
	blendFlags          uint8
	sourceConstantAlpha uint8
	alphaFormat         uint8
}

type sizeXY struct{ cx, cy int32 }

type msg struct {
	hwnd    uintptr
	message uint32
	wParam  uintptr
	lParam  uintptr
	time    uint32
	pt      point
	private uint32
}

type mouseInput struct {
	dx, dy      int32
	mouseData   uint32
	dwFlags     uint32
	time        uint32
	dwExtraInfo uintptr
}

type input struct {
	typ uint32
	mi  mouseInput
}

var (
	kernel32 = syscall.NewLazyDLL("kernel32.dll")
	user32   = syscall.NewLazyDLL("user32.dll")
	gdi32    = syscall.NewLazyDLL("gdi32.dll")

	procGetModuleHandleW              = kernel32.NewProc("GetModuleHandleW")
	procRegisterClassW                = user32.NewProc("RegisterClassW")
	procCreateWindowExW               = user32.NewProc("CreateWindowExW")
	procDefWindowProcW                = user32.NewProc("DefWindowProcW")
	procShowWindow                    = user32.NewProc("ShowWindow")
	procSetForegroundWindow           = user32.NewProc("SetForegroundWindow")
	procGetCursorPos                  = user32.NewProc("GetCursorPos")
	procUpdateLayeredWindow           = user32.NewProc("UpdateLayeredWindow")
	procSetCapture                    = user32.NewProc("SetCapture")
	procGetCapture                    = user32.NewProc("GetCapture")
	procReleaseCapture                = user32.NewProc("ReleaseCapture")
	procSendInput                     = user32.NewProc("SendInput")
	procPeekMessageW                  = user32.NewProc("PeekMessageW")
	procTranslateMessage              = user32.NewProc("TranslateMessage")
	procDispatchMessageW              = user32.NewProc("DispatchMessageW")
	procDestroyWindow                 = user32.NewProc("DestroyWindow")
	procLoadCursorW                   = user32.NewProc("LoadCursorW")
	procSetProcessDpiAwarenessContext = user32.NewProc("SetProcessDpiAwarenessContext")
	procSetProcessDPIAware            = user32.NewProc("SetProcessDPIAware")

	procCreateCompatibleDC = gdi32.NewProc("CreateCompatibleDC")
	procCreateDIBSection   = gdi32.NewProc("CreateDIBSection")
	procSelectObject       = gdi32.NewProc("SelectObject")
)

var wheelCount int

// wndProc 记录滚轮到达，其余交 DefWindowProc。
func wndProc(hwnd, uMsg, wParam, lParam uintptr) uintptr {
	if uMsg == wmMouseWheel {
		wheelCount++
		fmt.Printf("    [wndproc] WM_MOUSEWHEEL #%d\n", wheelCount)
	}
	r, _, _ := procDefWindowProcW.Call(hwnd, uMsg, wParam, lParam)
	return r
}

func main() {
	// 线程亲和：窗口创建 / SetCapture / PeekMessage 必须同一线程（Windows 按线程
	// 归属窗口；Go goroutine 在 time.Sleep 后可能换 M）——等价 Gio 窗口线程模型。
	runtime.LockOSThread()

	// DPI 感知（对齐 Gio 主程序口径）：不设则 GetCursorPos/ULW 坐标虚拟化，
	// 钉点像素可能落错物理位置——先置 PER_MONITOR_AWARE_V2，失败退 SetProcessDPIAware。
	if r, _, _ := procSetProcessDpiAwarenessContext.Call(^uintptr(3)); r == 0 { // -4 = PMv2
		procSetProcessDPIAware.Call()
	}

	fmt.Println("wheelpcap spike —— 滚轮路由四段实证")

	var cur point
	procGetCursorPos.Call(uintptr(unsafe.Pointer(&cur)))
	fmt.Printf("cursor = (%d,%d)（光标全程不动）\n", cur.x, cur.y)

	hInst, _, _ := procGetModuleHandleW.Call(0)
	cls, _ := syscall.UTF16PtrFromString("SpikeWheelCapClass")
	title, _ := syscall.UTF16PtrFromString("SpikeWheelCap")
	hCursor, _, _ := procLoadCursorW.Call(0, idcArrow)
	wc := wndClassW{
		lpfnWndProc:   syscall.NewCallback(wndProc),
		hInstance:     hInst,
		hCursor:       hCursor,
		lpszClassName: cls,
	}
	if r, _, e := procRegisterClassW.Call(uintptr(unsafe.Pointer(&wc))); r == 0 {
		fmt.Println("RegisterClassW:", e)
	}

	const w, h = 160, 160
	hwnd, _, err := procCreateWindowExW.Call(
		wsExLayered|wsExTopMost|wsExToolWindow,
		uintptr(unsafe.Pointer(cls)), uintptr(unsafe.Pointer(title)),
		wsPopup|wsVisible,
		uintptr(uint32(cur.x-w/2)), uintptr(uint32(cur.y-h/2)), w, h,
		0, 0, hInst, 0)
	if hwnd == 0 {
		fmt.Println("CreateWindowExW:", err)
		return
	}
	procShowWindow.Call(hwnd, swRestore)
	fg, _, _ := procSetForegroundWindow.Call(hwnd)
	fmt.Printf("SetForegroundWindow = %v\n", fg != 0)

	// DIB（顶向 32bpp，与 uigui 主程序同法）。
	hdc, _, _ := procCreateCompatibleDC.Call(0)
	var bi bitmapInfoHeader
	bi.biSize = uint32(unsafe.Sizeof(bitmapInfoHeader{}))
	bi.biWidth = w
	bi.biHeight = -h
	bi.biPlanes = 1
	bi.biBitCount = 32
	var bits unsafe.Pointer
	hbm, _, _ := procCreateDIBSection.Call(0, uintptr(unsafe.Pointer(&bi)), 0,
		uintptr(unsafe.Pointer(&bits)), 0, 0)
	if hbm == 0 || bits == nil {
		fmt.Println("CreateDIBSection 失败")
		return
	}
	procSelectObject.Call(hdc, hbm)

	// paint 提交 ULW：mode ∈ opaque / transparent / pinN（N = 光标像素 alpha 档）。
	paint := func(mode string) {
		px := unsafe.Slice((*byte)(bits), int(w*h*4))
		var pinA byte
		for i := 0; i < len(px); i += 4 {
			px[i+0], px[i+1], px[i+2], px[i+3] = 0x30, 0x40, 0x50, 0x00
		}
		switch {
		case mode == "opaque":
			for i := 0; i < len(px); i += 4 {
				px[i+3] = 0xFF
			}
		case strings.HasPrefix(mode, "pin"):
			a, _ := strconv.Atoi(mode[3:]) // pin 后跟十进制 alpha（pin1 / pin8 / pin128 ...）
			pinA = byte(a)
			// 光标居中（窗口以光标为中心创建）→ 帧像素 (w/2, h/2)。
			off := ((h/2)*w + w/2) * 4
			px[off+3] = pinA
		}
		dst := point{x: cur.x - w/2, y: cur.y - h/2}
		sz := sizeXY{cx: w, cy: h}
		src := point{}
		bf := blendFunc{sourceConstantAlpha: 0xFF, alphaFormat: acSrcAlpha}
		r, _, _ := procUpdateLayeredWindow.Call(hwnd, 0,
			uintptr(unsafe.Pointer(&dst)), uintptr(unsafe.Pointer(&sz)),
			hdc, uintptr(unsafe.Pointer(&src)), 0,
			uintptr(unsafe.Pointer(&bf)), ulwAlpha)
		fmt.Printf("  ULW mode=%-7s → %v\n", mode, r != 0)
	}

	// pump 泵消息 dur 时长（滚轮随 SendInput 异步路由，400ms 足够落窗）。
	pump := func(d time.Duration) {
		deadline := time.Now().Add(d)
		for time.Now().Before(deadline) {
			for {
				var m msg
				r, _, _ := procPeekMessageW.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0, pmRemove)
				if r == 0 {
					break
				}
				if m.message == wmQuit {
					return
				}
				procTranslateMessage.Call(uintptr(unsafe.Pointer(&m)))
				procDispatchMessageW.Call(uintptr(unsafe.Pointer(&m)))
			}
			time.Sleep(5 * time.Millisecond)
		}
	}

	// probe 注入 n 格滚轮（逐格注入 + 逐格泵，排除合流）并观察到达数。
	probe := func(label string, n int) {
		fmt.Printf("  %-30s", label)
		for i := 0; i < n; i++ {
			before := wheelCount
			in := input{typ: inputMouse, mi: mouseInput{mouseData: wheelDelta, dwFlags: mouseEventFWheel}}
			r, _, _ := procSendInput.Call(1, uintptr(unsafe.Pointer(&in)), unsafe.Sizeof(input{}))
			if r != 1 {
				fmt.Printf(" [SendInput=%d]", r)
			}
			pump(150 * time.Millisecond)
			fmt.Printf(" %d/%d", wheelCount-before, 1)
		}
		fmt.Println()
	}

	capture := func(on bool) {
		var ret uintptr
		var e error
		if on {
			ret, _, e = procSetCapture.Call(hwnd)
		} else {
			ret, _, e = procReleaseCapture.Call()
		}
		cur, _, _ := procGetCapture.Call()
		fmt.Printf("  SetCapture(%v) ret=%#x err=%v GetCapture=%#x（本窗 %#x）\n", on, ret, e, cur, hwnd)
	}

	fmt.Println("A0 不透明窗:")
	paint("opaque")
	probe("wheel×3 (opaque)", 3)

	fmt.Println("A0c 不透明窗 + 捕获（捕获本身可用性）:")
	capture(true)
	probe("wheel×3 (opaque+cap)", 3)
	capture(false)

	fmt.Println("A1 全透明窗（焦点不变）:")
	paint("transparent")
	probe("wheel×3 (transparent)", 3)

	fmt.Println("B 全透明窗 + SetCapture（D71 旧假设）:")
	capture(true)
	probe("wheel×3 (transparent+cap)", 3)

	fmt.Println("C ReleaseCapture:")
	capture(false)
	probe("wheel×1 (after release)", 1)

	fmt.Println("D alpha 档扫描：整幅透明 + 光标像素 alpha=N（D71 钉点假设）:")
	for _, n := range []string{"1", "8", "32", "64", "128", "255"} {
		paint("pin" + n)
		probe("wheel×1 (transparent+pin"+n+")", 1)
	}

	procDestroyWindow.Call(hwnd)
	fmt.Println("done")
}
