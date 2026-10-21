//go:build windows

package screencap

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/png"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"unsafe"

	"github.com/Tonyjh07/Aquarius/internal/port"
)

var (
	user32   = syscall.NewLazyDLL("user32.dll")
	gdi32    = syscall.NewLazyDLL("gdi32.dll")
	kernel32 = syscall.NewLazyDLL("kernel32.dll")
	dwmapi   = syscall.NewLazyDLL("dwmapi.dll")

	procEnumWindows            = user32.NewProc("EnumWindows")
	procIsWindow               = user32.NewProc("IsWindow")
	procIsWindowVisible        = user32.NewProc("IsWindowVisible")
	procIsIconic               = user32.NewProc("IsIconic")
	procGetWindowTextLengthW   = user32.NewProc("GetWindowTextLengthW")
	procGetWindowTextW         = user32.NewProc("GetWindowTextW")
	procGetWindowRect          = user32.NewProc("GetWindowRect")
	procGetClientRect          = user32.NewProc("GetClientRect")
	procClientToScreen         = user32.NewProc("ClientToScreen")
	procGetAncestor            = user32.NewProc("GetAncestor")
	procGetLastActivePopup     = user32.NewProc("GetLastActivePopup")
	procGetWindowThreadProcess = user32.NewProc("GetWindowThreadProcessId")
	procGetForegroundWindow    = user32.NewProc("GetForegroundWindow")
	procGetSystemMetrics       = user32.NewProc("GetSystemMetrics")
	procGetDC                  = user32.NewProc("GetDC")
	procReleaseDC              = user32.NewProc("ReleaseDC")
	procPrintWindow            = user32.NewProc("PrintWindow")
	procSetProcessDpiAwareness = user32.NewProc("SetProcessDpiAwarenessContext")
	procCreateCompatibleDC     = gdi32.NewProc("CreateCompatibleDC")
	procCreateDIBSection       = gdi32.NewProc("CreateDIBSection")
	procSelectObject           = gdi32.NewProc("SelectObject")
	procDeleteObject           = gdi32.NewProc("DeleteObject")
	procDeleteDC               = gdi32.NewProc("DeleteDC")
	procBitBlt                 = gdi32.NewProc("BitBlt")
	procGetCurrentProcessID    = kernel32.NewProc("GetCurrentProcessId")
	procOpenProcess            = kernel32.NewProc("OpenProcess")
	procQueryFullProcessImage  = kernel32.NewProc("QueryFullProcessImageNameW")
	procCloseHandle            = kernel32.NewProc("CloseHandle")
	procDwmGetWindowAttribute  = dwmapi.NewProc("DwmGetWindowAttribute")
)

// 常量（Win32 头文件取值）。
const (
	smXVirtualScreen    = 76
	smYVirtualScreen    = 77
	smCXVirtualScreen   = 78
	smCYVirtualScreen   = 79
	srcCopy             = 0x00CC0020
	captureBlt          = 0x40000000
	pwClientOnly        = 0x00000001
	pwRenderFullContent = 0x00000002
	dibRGBColors        = 0

	gaRootOwner = 3

	processQueryLimitedInfo = 0x1000

	dwmwaExtendedFrameBounds = 9
	dwmwaCloaked             = 14
)

// dpiAwareOnce 保证本进程按物理像素取图（D178：不缩放的另一种说法是不虚拟化）。
// DPI 不感知的进程拿到的矩形与屏幕 DC 都是逻辑像素，会出现「清单里窗口超出屏幕」的错位；
// GUI 模式下 Gio 早已设过，这里的调用会失败并忽略；TUI 模式下由这里补上。
var dpiAwareOnce sync.Once

func ensureDPIAware() {
	dpiAwareOnce.Do(func() {
		const perMonitorV2 = ^uintptr(3) // DPI_AWARENESS_CONTEXT_PER_MONITOR_AWARE_V2 = -4
		procSetProcessDpiAwareness.Call(perMonitorV2)
	})
}

// winRect 与 C 布局一致的 RECT。
type winRect struct{ left, top, right, bottom int32 }

// winPoint 与 C 布局一致的 POINT。
type winPoint struct{ x, y int32 }

// bitmapInfoHeader BITMAPINFOHEADER（x64 布局与 C 一致）。
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

// New 返回 Windows 屏幕捕获端口。
func New() port.Capture { return capture{} }

type capture struct{}

// Windows 顶层可见窗口，z 序自顶向下（EnumWindows 的枚举序）。
// 过滤：不可见、被 DWM 隐藏（cloaked）、无标题、零面积、本进程自己的窗口（D178 口径四）。
func (capture) Windows(ctx context.Context) ([]port.WindowInfo, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	ensureDPIAware()
	selfPID, _, _ := procGetCurrentProcessID.Call()
	self := uint32(selfPID)
	fg, _, _ := procGetForegroundWindow.Call()

	var out []port.WindowInfo
	cb := syscall.NewCallback(func(hwnd uintptr, _ uintptr) uintptr {
		if ctx.Err() != nil {
			return 0 // 停止枚举
		}
		if !visible(hwnd) || cloaked(hwnd) {
			return 1
		}
		title := windowTitle(hwnd)
		if strings.TrimSpace(title) == "" {
			return 1
		}
		var pid uint32
		procGetWindowThreadProcess.Call(hwnd, uintptr(unsafe.Pointer(&pid)))
		if pid == self {
			return 1
		}
		r := windowRect(hwnd)
		if r.right <= r.left || r.bottom <= r.top {
			return 1
		}
		out = append(out, port.WindowInfo{
			Handle:     hwnd,
			Title:      title,
			App:        processName(pid),
			Rect:       port.Rect{Left: int(r.left), Top: int(r.top), Right: int(r.right), Bottom: int(r.bottom)},
			Foreground: hwnd == fg,
		})
		return 1
	})
	if r, _, err := procEnumWindows.Call(cb, 0); r == 0 {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, ctxErr
		}
		return nil, fmt.Errorf("enumerate windows: %w", err)
	}
	return out, nil
}

// Screen 整个虚拟桌面（多显示器合成），物理像素。
func (capture) Screen(ctx context.Context) (port.Image, error) {
	if err := ctx.Err(); err != nil {
		return port.Image{}, err
	}
	ensureDPIAware()
	xm, _, _ := procGetSystemMetrics.Call(smXVirtualScreen)
	ym, _, _ := procGetSystemMetrics.Call(smYVirtualScreen)
	wm, _, _ := procGetSystemMetrics.Call(smCXVirtualScreen)
	hm, _, _ := procGetSystemMetrics.Call(smCYVirtualScreen)
	x, y := int(int32(xm)), int(int32(ym))
	w, h := int(int32(wm)), int(int32(hm))
	if w <= 0 || h <= 0 {
		return port.Image{}, fmt.Errorf("screen capture: the desktop reports a %dx%d virtual screen", w, h)
	}

	screenDC, _, _ := procGetDC.Call(0)
	if screenDC == 0 {
		return port.Image{}, fmt.Errorf("screen capture: cannot get the screen device context")
	}
	defer procReleaseDC.Call(0, screenDC)

	pix, err := bitblt(screenDC, int(x), int(y), w, h)
	if err != nil {
		return port.Image{}, fmt.Errorf("screen capture: %w", err)
	}
	return encode(pix, w, h)
}

// Window 抓指定窗口的客户区：PrintWindow 优先（被遮挡/非顶层也抓得到），
// 失败且窗口未最小化时回退到屏幕上的客户区 BitBlt；两者都拿不到就明确报因（D178 口径二）。
// 活动弹窗（菜单/下拉/owned 对话框）是独立顶层窗口，按屏幕偏移合成进主图。
func (capture) Window(ctx context.Context, h uintptr) (port.Image, error) {
	if err := ctx.Err(); err != nil {
		return port.Image{}, err
	}
	ensureDPIAware()
	if h == 0 {
		return port.Image{}, fmt.Errorf("window capture: missing window handle; get one from window_list")
	}
	if ok, _, _ := procIsWindow.Call(h); ok == 0 {
		return port.Image{}, fmt.Errorf("window capture: %#x is not a window; get a current handle from window_list", h)
	}
	w, ht, ok := clientSize(h)
	if !ok || w <= 0 || ht <= 0 {
		return port.Image{}, fmt.Errorf("window capture: the window has an empty client area (%dx%d)", w, ht)
	}
	ox, oy, ok := clientOrigin(h)
	if !ok {
		return port.Image{}, fmt.Errorf("window capture: cannot locate the window on screen")
	}

	screenDC, _, _ := procGetDC.Call(0)
	if screenDC == 0 {
		return port.Image{}, fmt.Errorf("window capture: cannot get the screen device context")
	}
	defer procReleaseDC.Call(0, screenDC)

	// 一、让窗口自己画进 DIB（不依赖遮挡状态）。
	pix, err := printWindow(h, screenDC, w, ht, pwClientOnly|pwRenderFullContent)
	if err == nil && !blank(pix, w, ht) {
		mergeActivePopup(pix, w, ht, h, ox, oy, screenDC)
		return encode(pix, w, ht)
	}

	// 二、未最小化时抓屏幕上的客户区。最小化窗口的屏幕位置被别的窗口占着，
	// 抓到的不是它自己——这种「看起来成功」比报错更糟（D178 口径二）。
	if iconic(h) {
		return port.Image{}, fmt.Errorf("window capture: the window is minimized and did not render itself; restore it, or capture target \"screen\"")
	}
	pix, err = bitblt(screenDC, ox, oy, w, ht)
	if err != nil {
		return port.Image{}, fmt.Errorf("window capture: %w", err)
	}
	if blank(pix, w, ht) {
		return port.Image{}, fmt.Errorf("window capture: the window did not render anything (it may be GPU-composited); try target \"screen\" instead")
	}
	return encode(pix, w, ht)
}

// mergeActivePopup 把主窗的活动弹窗（右键菜单、下拉、owned 对话框）按各自客户区在屏幕上的
// 偏移合成进主图。弹窗通常是独立顶层窗口，单抓主窗会把它漏掉；合成失败只丢这一层，不整次失败。
func mergeActivePopup(main []byte, w, h int, hwnd uintptr, ox, oy int, screenDC uintptr) {
	root, _, _ := procGetAncestor.Call(hwnd, gaRootOwner)
	if root == 0 {
		root = hwnd
	}
	popup, _, _ := procGetLastActivePopup.Call(root)
	if popup == 0 || popup == hwnd || popup == root || !visible(popup) {
		return
	}
	pw, ph, ok := clientSize(popup)
	if !ok || pw <= 0 || ph <= 0 {
		return
	}
	px, py, ok := clientOrigin(popup)
	if !ok {
		return
	}
	shot, err := printWindow(popup, screenDC, pw, ph, pwClientOnly|pwRenderFullContent)
	if err != nil || blank(shot, pw, ph) {
		return
	}
	copyOverlap(main, w, h, shot, pw, ph, px-ox, py-oy)
}

// copyOverlap 把 src（sw×sh）按 (dx,dy) 偏移叠到 dst（dw×dh）上，只拷贝两者相交的矩形。
func copyOverlap(dst []byte, dw, dh int, src []byte, sw, sh, dx, dy int) {
	x0, y0 := max(0, dx), max(0, dy)
	x1, y1 := min(dw, dx+sw), min(dh, dy+sh)
	if x0 >= x1 || y0 >= y1 {
		return
	}
	rowBytes := (x1 - x0) * 4
	for y := y0; y < y1; y++ {
		di := (y*dw + x0) * 4
		si := ((y-dy)*sw + (x0 - dx)) * 4
		copy(dst[di:di+rowBytes], src[si:si+rowBytes])
	}
}

// printWindow 用 PrintWindow 把窗口画进内存位图（flags 决定客户区/整窗、是否渲染完整内容）。
func printWindow(hwnd, screenDC uintptr, w, h int, flags uintptr) ([]byte, error) {
	dc := createMemDC(screenDC)
	if dc == 0 {
		return nil, fmt.Errorf("create device context")
	}
	defer procDeleteDC.Call(dc)
	bmp, bits, old := createDIB(dc, w, h)
	if bmp == 0 {
		return nil, fmt.Errorf("create DIB section")
	}
	defer func() {
		procSelectObject.Call(dc, old)
		procDeleteObject.Call(bmp)
	}()

	if ok, _, err := procPrintWindow.Call(hwnd, dc, flags); ok == 0 {
		return nil, fmt.Errorf("PrintWindow: %w", err)
	}
	return rgbaFromDIB(bits, w, h), nil
}

// bitblt 从源 DC 的 (x,y) 处拷 w×h 到内存位图并取回像素。
func bitblt(src uintptr, x, y, w, h int) ([]byte, error) {
	dc := createMemDC(src)
	if dc == 0 {
		return nil, fmt.Errorf("create device context")
	}
	defer procDeleteDC.Call(dc)
	bmp, bits, old := createDIB(dc, w, h)
	if bmp == 0 {
		return nil, fmt.Errorf("create DIB section")
	}
	defer func() {
		procSelectObject.Call(dc, old)
		procDeleteObject.Call(bmp)
	}()

	if ok, _, err := procBitBlt.Call(dc, 0, 0, uintptr(w), uintptr(h), src,
		uintptr(x), uintptr(y), srcCopy|captureBlt); ok == 0 {
		return nil, fmt.Errorf("BitBlt: %w", err)
	}
	return rgbaFromDIB(bits, w, h), nil
}

func createMemDC(src uintptr) uintptr {
	dc, _, _ := procCreateCompatibleDC.Call(src)
	return dc
}

// createDIB 建 32bpp 自顶向下的 DIB section 并选进 DC，返回位图、像素指针与旧对象。
// 用 DIB section 而不是「兼容位图 + GetDIBits」：位图像素由系统直接映射到内存，
// 少一次格式转换，也避开 GetDIBits 对「位图不得选进 DC」的隐式要求。
func createDIB(dc uintptr, w, h int) (uintptr, unsafe.Pointer, uintptr) {
	bi := bitmapInfoHeader{
		biSize:        uint32(unsafe.Sizeof(bitmapInfoHeader{})),
		biWidth:       int32(w),
		biHeight:      int32(-h), // 负高度 = 自顶向下
		biPlanes:      1,
		biBitCount:    32,
		biCompression: dibRGBColors,
	}
	var bits unsafe.Pointer
	bmp, _, _ := procCreateDIBSection.Call(dc, uintptr(unsafe.Pointer(&bi)), dibRGBColors,
		uintptr(unsafe.Pointer(&bits)), 0, 0)
	if bmp == 0 || bits == nil {
		return 0, nil, 0
	}
	old, _, _ := procSelectObject.Call(dc, bmp)
	return bmp, bits, old
}

// rgbaFromDIB 把 BGRX 像素拷成 RGBA（alpha 置不透明）。
func rgbaFromDIB(bits unsafe.Pointer, w, h int) []byte {
	src := unsafe.Slice((*byte)(bits), w*h*4)
	rgba := make([]byte, len(src))
	for i := 0; i+3 < len(src); i += 4 {
		rgba[i+0] = src[i+2]
		rgba[i+1] = src[i+1]
		rgba[i+2] = src[i+0]
		rgba[i+3] = 0xff
	}
	return rgba
}

// blank 判定「没画出来」：整张图纯黑（DIB 初值 / PrintWindow 失败会返回全零）。
// 不能按「单色」判——白页、纯色面板、暗色编辑器的空白区都是合法内容，
// 按单色判会把用户正要看的画面拒掉（D178 口径二）。
func blank(rgba []byte, w, h int) bool {
	if w <= 0 || h <= 0 || len(rgba) < w*h*4 {
		return true
	}
	for i := 0; i+3 < len(rgba); i += 4 {
		if rgba[i]|rgba[i+1]|rgba[i+2] != 0 {
			return false
		}
	}
	return true
}

// encode 把 RGBA 像素编码成 PNG。
func encode(pix []byte, w, h int) (port.Image, error) {
	img := &image.RGBA{Pix: pix, Stride: w * 4, Rect: image.Rect(0, 0, w, h)}
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		return port.Image{}, fmt.Errorf("encode png: %w", err)
	}
	return port.Image{Data: b.Bytes(), Width: w, Height: h}, nil
}

func visible(hwnd uintptr) bool {
	ok, _, _ := procIsWindowVisible.Call(hwnd)
	return ok != 0
}

// iconic 判断窗口是否最小化（IsWindowVisible 对最小化窗口仍为真，不能拿它当可见性判据）。
func iconic(hwnd uintptr) bool {
	ok, _, _ := procIsIconic.Call(hwnd)
	return ok != 0
}

// clientSize 客户区尺寸（物理 px）。
func clientSize(hwnd uintptr) (w, h int, ok bool) {
	var r winRect
	if a, _, _ := procGetClientRect.Call(hwnd, uintptr(unsafe.Pointer(&r))); a == 0 {
		return 0, 0, false
	}
	return int(r.right - r.left), int(r.bottom - r.top), true
}

// clientOrigin 客户区左上角在屏幕上的坐标（物理 px）。
func clientOrigin(hwnd uintptr) (x, y int, ok bool) {
	var p winPoint
	if a, _, _ := procClientToScreen.Call(hwnd, uintptr(unsafe.Pointer(&p))); a == 0 {
		return 0, 0, false
	}
	return int(p.x), int(p.y), true
}

// cloaked 判断窗口是否被 DWM 隐藏（UWP 后台窗口会保留 IsWindowVisible=true）。
func cloaked(hwnd uintptr) bool {
	var v uint32
	hr, _, _ := procDwmGetWindowAttribute.Call(hwnd, dwmwaCloaked, uintptr(unsafe.Pointer(&v)), unsafe.Sizeof(v))
	if int32(hr) != 0 {
		return false // 查询失败不代表隐藏
	}
	return v != 0
}

func windowTitle(hwnd uintptr) string {
	n, _, _ := procGetWindowTextLengthW.Call(hwnd)
	if n == 0 {
		return ""
	}
	buf := make([]uint16, int(n)+1)
	procGetWindowTextW.Call(hwnd, uintptr(unsafe.Pointer(&buf[0])), n+1)
	return syscall.UTF16ToString(buf)
}

// windowRect 优先用 DWM 扩展边框（不含不可见阴影），失败回退 GetWindowRect。
func windowRect(hwnd uintptr) winRect {
	var r winRect
	hr, _, _ := procDwmGetWindowAttribute.Call(hwnd, dwmwaExtendedFrameBounds, uintptr(unsafe.Pointer(&r)), unsafe.Sizeof(r))
	if int32(hr) == 0 && r.right > r.left && r.bottom > r.top {
		return r
	}
	procGetWindowRect.Call(hwnd, uintptr(unsafe.Pointer(&r)))
	return r
}

// processName 取进程可执行文件名；进程已退出或无权限时为空。
func processName(pid uint32) string {
	if pid == 0 {
		return ""
	}
	h, _, _ := procOpenProcess.Call(processQueryLimitedInfo, 0, uintptr(pid))
	if h == 0 {
		return ""
	}
	defer procCloseHandle.Call(h)
	buf := make([]uint16, 1024)
	size := uint32(len(buf))
	ok, _, _ := procQueryFullProcessImage.Call(h, 0, uintptr(unsafe.Pointer(&buf[0])), uintptr(unsafe.Pointer(&size)))
	if ok == 0 {
		return ""
	}
	return filepath.Base(syscall.UTF16ToString(buf[:size]))
}
