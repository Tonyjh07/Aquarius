// spike/printwin —— 验证 ULW 淡出 overlay 方案的承重假设（DESIGN §15.3）：
//
// PrintWindow(hwnd, hdc, PW_RENDERFULLCONTENT) 读到的内容是否包含
// SetWindowRgn 形裁挖空区的 D3D（Gio swapchain）像素？
//
//   - 成立（挖空区 = backbuffer 原样内容）→ overlay 可从 PrintWindow 拿到
//     "淡出带内的气泡像素"（屏幕上那里已被挖空、桌面直接可见，屏幕捕获取不到），
//     再叠布局掩码 × 垂直渐变 → UpdateLayeredWindow 出真 alpha 淡出。
//   - 不成立（返回被区域裁剪/黑）→ ULW 方案换 fallback（扫描线抖动或换捕获源）。
//
// 场景：窗口整窗画绿色，中心画蓝色矩形；形裁只保留左上角 100x50 条。
// 期望（假设成立）：PrintWindow 结果 = 全窗原样（中心蓝、角落绿），
// 与屏幕上"只有左上条可见"不同。
package main

import (
	"fmt"
	"image"
	"image/color"
	"os"
	"sync/atomic"
	"syscall"
	"unsafe"

	"gioui.org/app"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"
)

var (
	user32 = syscall.NewLazyDLL("user32.dll")
	gdi32  = syscall.NewLazyDLL("gdi32.dll")

	procPrintWindow        = user32.NewProc("PrintWindow")
	procGetWindowRect      = user32.NewProc("GetWindowRect")
	procSetWindowRgn       = user32.NewProc("SetWindowRgn")
	procCreateRectRgn      = gdi32.NewProc("CreateRectRgn")
	procCreateCompatibleDC = gdi32.NewProc("CreateCompatibleDC")
	procCreateDIBSection   = gdi32.NewProc("CreateDIBSection")
	procSelectObject       = gdi32.NewProc("SelectObject")
	procDeleteObject       = gdi32.NewProc("DeleteObject")
	procDeleteDC           = gdi32.NewProc("DeleteDC")
)

const (
	pwRenderFullContent = 2 // Win8.1+：经 DWM 捕获窗口内容
	dibRGBColors        = 0
)

type rect struct{ left, top, right, bottom int32 }

// bitmapInfoHeader BITMAPINFOHEADER（x64 与 C 一致）。
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

var mainHWND uintptr // atomic

func main() {
	go run()
	app.Main()
}

func run() {
	w := new(app.Window)
	w.Option(
		app.Title("printwin-spike"),
		app.Size(unit.Dp(400), unit.Dp(300)),
		app.Decorated(false),
	)
	var ops op.Ops
	frameN := 0
	for {
		switch e := w.Event().(type) {
		case app.FrameEvent:
			frameN++
			gtx := app.NewContext(&ops, e)
			drawFrame(gtx)
			e.Frame(&ops)
			if frameN == 5 {
				verify(w)
				return
			}
		case app.Win32ViewEvent:
			atomic.StoreUintptr(&mainHWND, e.HWND)
			fmt.Printf("[hwnd] %#x\n", e.HWND)
		case app.DestroyEvent:
			os.Exit(1)
		}
	}
}

// drawFrame 整窗绿色 + 中心蓝色矩形（无其他内容）。
func drawFrame(gtx layout.Context) layout.Dimensions {
	return layout.Stack{}.Layout(gtx,
		layout.Expanded(func(gtx layout.Context) layout.Dimensions {
			paint.Fill(gtx.Ops, green)
			return layout.Dimensions{Size: gtx.Constraints.Max}
		}),
		layout.Stacked(func(gtx layout.Context) layout.Dimensions {
			st := clip.Rect{Min: image.Pt(150, 100), Max: image.Pt(250, 200)}.Push(gtx.Ops)
			paint.Fill(gtx.Ops, blue)
			st.Pop()
			return layout.Dimensions{}
		}),
	)
}

var (
	green = color.NRGBA{R: 0x00, G: 0xFF, B: 0x00, A: 0xFF}
	blue  = color.NRGBA{R: 0x00, G: 0x00, B: 0xFF, A: 0xFF}
)

// verify 形裁 → PrintWindow → 读像素断言（全部在窗口线程执行，规避跨线程消息）。
func verify(w *app.Window) {
	w.Run(func() {
		hwnd := atomic.LoadUintptr(&mainHWND)
		if hwnd == 0 {
			fmt.Println("[FAIL] no hwnd")
			os.Exit(1)
		}
		var rc rect
		procGetWindowRect.Call(hwnd, uintptr(unsafe.Pointer(&rc)))
		pw, ph := rc.right-rc.left, rc.bottom-rc.top
		fmt.Printf("[win] physical %dx%d\n", pw, ph)

		// 形裁：只保留左上角 100x50（物理像素）。
		rgn, _, _ := procCreateRectRgn.Call(0, 0, 100, 50)
		r, _, err := procSetWindowRgn.Call(hwnd, rgn, 1)
		fmt.Printf("[rgn] SetWindowRgn(0,0,100,50) ok=%v err=%v\n", r != 0, err)

		px, ok := printWindow(hwnd, pw, ph)
		if !ok {
			fmt.Println("[FAIL] PrintWindow")
			os.Exit(1)
		}
		at := func(x, y int32) [4]uint8 {
			i := (y*int32(pw) + x) * 4
			return [4]uint8{px[i], px[i+1], px[i+2], px[i+3]}
		}
		center := at(pw/2, ph/2)   // 蓝（已挖空——屏幕不可见）
		corner := at(pw-10, ph-10) // 绿（已挖空）
		strip := at(10, 10)        // 绿（区域内，屏幕可见）
		fmt.Printf("[px] center(blue?)=%v corner(green?)=%v strip(green?)=%v\n", center, corner, strip)

		// BGRA：绿 = {0,FF,0,?}，蓝 = {FF,0,0,?}（alpha 位 BI_RGB 下可为 0，不断言）。
		isGreen := func(c [4]uint8) bool { return c[0] == 0x00 && c[1] == 0xFF && c[2] == 0x00 }
		isBlue := func(c [4]uint8) bool { return c[0] == 0xFF && c[1] == 0x00 && c[2] == 0x00 }
		held := isBlue(center) && isGreen(corner) && isGreen(strip)
		fmt.Printf("[RESULT] printwindow_includes_cutout_area=%v\n", held)
		if !held {
			fmt.Println("[RESULT] expected: center=blue corner=green strip=green (backbuffer unclipped)")
			os.Exit(2)
		}
		fmt.Println("[RESULT] OK: ULW fade overlay plan is viable")
		os.Exit(0)
	})
}

// printWindow PW_RENDERFULLCONTENT 捕获窗口为顶向 32bpp BGRA 位图。
func printWindow(hwnd uintptr, w, h int32) ([]byte, bool) {
	var bi bitmapInfoHeader
	bi.biSize = uint32(unsafe.Sizeof(bi))
	bi.biWidth = w
	bi.biHeight = -h // 顶向
	bi.biPlanes = 1
	bi.biBitCount = 32
	bi.biCompression = dibRGBColors
	var bits unsafe.Pointer
	hbm, _, err := procCreateDIBSection.Call(0, uintptr(unsafe.Pointer(&bi)),
		dibRGBColors, uintptr(unsafe.Pointer(&bits)), 0, 0)
	if hbm == 0 || bits == nil {
		fmt.Printf("[FAIL] CreateDIBSection: %v\n", err)
		return nil, false
	}
	defer procDeleteObject.Call(hbm)
	hdc, _, _ := procCreateCompatibleDC.Call(0)
	if hdc == 0 {
		return nil, false
	}
	defer procDeleteDC.Call(hdc)
	procSelectObject.Call(hdc, hbm)
	r, _, err := procPrintWindow.Call(hwnd, hdc, pwRenderFullContent)
	if r == 0 {
		fmt.Printf("[FAIL] PrintWindow: %v\n", err)
		return nil, false
	}
	size := int(w * h * 4)
	out := make([]byte, size)
	copy(out, unsafe.Slice((*byte)(bits), size))
	return out, true
}
