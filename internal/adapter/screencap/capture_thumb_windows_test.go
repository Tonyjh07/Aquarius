//go:build windows

package screencap

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/png"
	"runtime"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
	"unsafe"
)

// TestOffscreenAnchorX 辅助窗口落在虚拟桌面最右侧之外：任何显示器布局都框不到它，
// 全屏截图也不会把它拍进来。
func TestOffscreenAnchorX(t *testing.T) {
	cases := []struct {
		left, width int32
		want        int32
	}{
		{0, 2048, 2048 + thumbOffscreenGap},     // 单屏
		{0, 5120, 5120 + thumbOffscreenGap},     // 超宽屏
		{-1920, 3840, 1920 + thumbOffscreenGap}, // 主屏偏右、左副屏
		{-3840, 6400, 2560 + thumbOffscreenGap}, // 双屏负坐标
	}
	for _, c := range cases {
		if got := offscreenAnchorX(c.left, c.width); got != c.want {
			t.Fatalf("offscreenAnchorX(%d,%d) = %d, want %d", c.left, c.width, got, c.want)
		}
	}
}

var (
	procTestRegisterClassExW = user32.NewProc("RegisterClassExW")
	procTestCreateWindowExW  = user32.NewProc("CreateWindowExW")
	procTestDefWindowProcW   = user32.NewProc("DefWindowProcW")
	procTestBeginPaint       = user32.NewProc("BeginPaint")
	procTestEndPaint         = user32.NewProc("EndPaint")
	procTestFillRect         = user32.NewProc("FillRect")
	procTestGetClientRect    = user32.NewProc("GetClientRect")
	procTestShowWindow       = user32.NewProc("ShowWindow")
	procTestDestroyWindow    = user32.NewProc("DestroyWindow")
	procTestPeekMessageW     = user32.NewProc("PeekMessageW")
	procTestTranslateMessage = user32.NewProc("TranslateMessage")
	procTestDispatchMessageW = user32.NewProc("DispatchMessageW")
	procTestGetModuleHandleW = kernel32.NewProc("GetModuleHandleW")
	procTestCreateSolidBrush = gdi32.NewProc("CreateSolidBrush")
	procTestDeleteObject     = gdi32.NewProc("DeleteObject")

	procTestGetWindowPlacement = user32.NewProc("GetWindowPlacement")
)

const (
	wmPaint      = 0x000F
	wmEraseBkgnd = 0x0014
	wsVisible    = 0x10000000
	swMinimize   = 6
)

// testPattern 测试窗口要画的内容：多个单色块（依次是红/绿/蓝/黄），或整片同一个颜色。
// 单色那一档专门覆盖「暗色单色面板不能被当成没画出来」。
var testPatternFor atomic.Pointer[uintptr]

func setTestPattern(solid uintptr) { testPatternFor.Store(&solid) }

// testPaintProc 把客户区画成左上红、右上绿、左下蓝、右下黄四块：
// 抓回来只数颜色就能确认「确实是这个窗口的内容」，不必比对像素位置。
func testPaintProc(hwnd, msg, wParam, lParam uintptr) uintptr {
	if msg != wmPaint && msg != wmEraseBkgnd {
		r, _, _ := procTestDefWindowProcW.Call(hwnd, msg, wParam, lParam)
		return r
	}
	var (
		r     winRect
		paint [72]byte // PAINTSTRUCT（x64 为 72 字节）
		hdc   = wParam
	)
	procTestGetClientRect.Call(hwnd, uintptr(unsafe.Pointer(&r)))
	if msg == wmPaint {
		hdc, _, _ = procTestBeginPaint.Call(hwnd, uintptr(unsafe.Pointer(&paint[0])))
		defer procTestEndPaint.Call(hwnd, uintptr(unsafe.Pointer(&paint[0])))
	}
	if hdc == 0 {
		return 0
	}
	halfW := (r.right - r.left) / 2
	halfH := (r.bottom - r.top) / 2
	quads := []struct {
		rc       winRect
		colorRef uintptr
	}{
		{winRect{0, 0, halfW, halfH}, 0x000000FF},              // 红
		{winRect{halfW, 0, r.right, halfH}, 0x0000FF00},        // 绿
		{winRect{0, halfH, halfW, r.bottom}, 0x00FF0000},       // 蓝
		{winRect{halfW, halfH, r.right, r.bottom}, 0x0000FFFF}, // 黄
	}
	if solid := testPatternFor.Load(); solid != nil && *solid != 0 {
		quads = []struct {
			rc       winRect
			colorRef uintptr
		}{{r, *solid}}
	}
	for _, q := range quads {
		rc := q.rc
		brush, _, _ := procTestCreateSolidBrush.Call(q.colorRef)
		if brush == 0 {
			continue
		}
		procTestFillRect.Call(hdc, uintptr(unsafe.Pointer(&rc)), brush)
		procTestDeleteObject.Call(brush)
	}
	return 0
}

// startTestWindow 在钉住的线程上开一个会自绘的窗口并进入消息循环，返回句柄与停止函数。
// 窗口必须在同一个线程上建、收消息、被最小化，否则收不到 WM_PAINT，也不会被合成。
// 停止信号是每扇窗自己的：几个测试先后各开一扇，共用一个标志会让上一扇窗销毁不了。
func startTestWindow(t *testing.T) (uintptr, func()) {
	t.Helper()
	ready := make(chan uintptr, 1)
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		hInst, _, _ := procTestGetModuleHandleW.Call(0)
		name, _ := syscall.UTF16PtrFromString("AquariusScreencapTestWindow")
		procTestRegisterClassExW.Call(uintptr(unsafe.Pointer(&wndClassExW{
			cbSize:        uint32(unsafe.Sizeof(wndClassExW{})),
			style:         csHRedraw | csVRedraw,
			lpfnWndProc:   syscall.NewCallback(testPaintProc),
			hInstance:     hInst,
			hbrBackground: 6,
			lpszClassName: name,
		})))
		hwnd, _, _ := procTestCreateWindowExW.Call(0,
			uintptr(unsafe.Pointer(name)), 0, wsPopup|wsVisible,
			40, 40, 640, 420, 0, 0, hInst, 0)
		ready <- hwnd
		if hwnd == 0 {
			return
		}
		var m winMsg
		for {
			select {
			case <-stop:
				procTestDestroyWindow.Call(hwnd)
				return
			default:
			}
			for {
				if r, _, _ := procTestPeekMessageW.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0, pmRemove); r == 0 {
					break
				}
				procTestTranslateMessage.Call(uintptr(unsafe.Pointer(&m)))
				procTestDispatchMessageW.Call(uintptr(unsafe.Pointer(&m)))
			}
			time.Sleep(3 * time.Millisecond)
		}
	}()
	hwnd := <-ready
	if hwnd == 0 {
		t.Skip("cannot create a test window in this session")
	}
	return hwnd, func() {
		close(stop)
		<-done
	}
}

// minimizeTestWindow 等首帧画出来后最小化，返回最小化态的 placement 作为基线。
// 最小化不成就 skip：远程会话/无桌面环境里这个测试没有意义。
func minimizeTestWindow(t *testing.T, hwnd uintptr) [6]int32 {
	t.Helper()
	// 等首帧画出来，再最小化。
	time.Sleep(250 * time.Millisecond)
	procTestShowWindow.Call(hwnd, swMinimize)
	for deadline := time.Now().Add(3 * time.Second); time.Now().Before(deadline); {
		if iconic(hwnd) {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !iconic(hwnd) {
		t.Skip("the test window did not minimize in this session")
	}
	// 最小化态作为基线：抓取必须不改动它。
	return testPlacement(hwnd)
}

// grabMinimizedWindow 走生产的 Window() 路径抓一个最小化窗口并解码。
func grabMinimizedWindow(t *testing.T, hwnd uintptr) image.Image {
	t.Helper()
	img, err := (capture{}).Window(context.Background(), hwnd)
	if err != nil {
		t.Skipf("the desktop compositor does not provide window previews in this session: %v", err)
	}
	dec, err := png.Decode(bytes.NewReader(img.Data))
	if err != nil {
		t.Fatalf("decode capture: %v", err)
	}
	return dec
}

// TestWindowCapturesMinimizedWindow 最小化窗口经 DWM 缩略图抓回真实内容。
//
// 这是本适配器唯一能证明「最小化窗口截得到内容」的测试：它不依赖桌面上恰好有某个窗口，
// 而是自己开一个画着四色块的窗口、最小化它，再走生产的 Window() 路径抓回来，
// 断言图里认得出那几种颜色，且窗口在抓取前后仍是最小化、位置未变。
func TestWindowCapturesMinimizedWindow(t *testing.T) {
	hwnd, stop := startTestWindow(t)
	defer stop()
	before := minimizeTestWindow(t, hwnd)

	dec := grabMinimizedWindow(t, hwnd)
	b := dec.Bounds()
	if b.Dx() < 300 || b.Dy() < 200 {
		t.Fatalf("captured image is too small to be the real window: %v", b)
	}
	seen := make(map[[3]uint32]bool)
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			r, g, bl, _ := dec.At(x, y).RGBA()
			seen[[3]uint32{r >> 8, g >> 8, bl >> 8}] = true
		}
	}
	quads := [][3]uint32{{255, 0, 0}, {0, 255, 0}, {0, 0, 255}, {255, 255, 0}}
	found := 0
	for _, q := range quads {
		if seen[q] {
			found++
		}
	}
	if found < 2 {
		t.Fatalf("the capture does not contain the test window's colours (found %d of 4, %d distinct colours overall)", found, len(seen))
	}

	// 抓取不能改动窗口：仍是最小化，位置与状态未变。
	if !iconic(hwnd) {
		t.Fatal("capturing a minimized window left it restored")
	}
	if after := testPlacement(hwnd); after != before {
		t.Fatalf("capturing a minimized window changed its placement: %v -> %v", before, after)
	}
}

// testSentinelPix 把像素写成哨兵底色（DIB 是 BGRA 字节序）。
func testSentinelPix(pix []byte, i int) {
	pix[i], pix[i+1], pix[i+2] = 0xFF, 0x00, 0xFF
}

// TestSentinelLike 哨兵底色的识别：纯洋红与它被抗锯齿混合后的样子都算，真实内容不算。
func TestSentinelLike(t *testing.T) {
	cases := []struct {
		rgb  [3]byte
		want bool
	}{
		{[3]byte{255, 0, 255}, true},    // 纯哨兵色
		{[3]byte{181, 47, 181}, true},   // 混合后的边条（真机实测）
		{[3]byte{166, 47, 166}, true},   // 另一种混合比例
		{[3]byte{189, 47, 189}, true},   // 顶边实测
		{[3]byte{0, 0, 0}, false},       // 纯黑内容
		{[3]byte{255, 255, 255}, false}, // 纯白内容
		{[3]byte{32, 32, 32}, false},    // 暗色面板
		{[3]byte{255, 153, 51}, false},  // 橙色按钮
	}
	for _, c := range cases {
		p := []byte{c.rgb[0], c.rgb[1], c.rgb[2], 255}
		if got := sentinelLike(p, 0); got != c.want {
			t.Fatalf("sentinelLike(%v) = %v, want %v", c.rgb, got, c.want)
		}
	}
}

// TestTrimSentinelBorder 合成器留下的底色边条要裁掉，内容里的同色像素不能裁。
func TestTrimSentinelBorder(t *testing.T) {
	// 8x6：四周 1px 哨兵色，内部全黑。
	const w, h = 8, 6
	pix := make([]byte, w*h*4)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			i := (y*w + x) * 4
			edge := x == 0 || y == 0 || x == w-1 || y == h-1
			if edge {
				testSentinelPix(pix, i)
			}
			pix[i+3] = 255
		}
	}
	got, gw, gh := trimSentinelBorder(pix, w, h)
	if gw != w-2 || gh != h-2 {
		t.Fatalf("trimSentinelBorder kept %dx%d, want %dx%d", gw, gh, w-2, h-2)
	}
	for i := 0; i+3 < len(got); i += 4 {
		if sentinelLike(got, i) {
			t.Fatalf("sentinel colour survived the trim at byte %d", i)
		}
	}

	// 边框被抗锯齿混合过（实测 181,47,181）：同样要裁。
	pix1b := make([]byte, w*h*4)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			i := (y*w + x) * 4
			if x == w-1 {
				pix1b[i], pix1b[i+1], pix1b[i+2] = 181, 47, 181
			}
			pix1b[i+3] = 255
		}
	}
	if _, gw1b, _ := trimSentinelBorder(pix1b, w, h); gw1b != w-1 {
		t.Fatalf("trimSentinelBorder kept the blended sentinel column: width %d, want %d", gw1b, w-1)
	}

	// 内容里有一条同色的竖线但不是整条边：不能裁。
	pix2 := make([]byte, w*h*4)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			i := (y*w + x) * 4
			switch {
			case x == 0:
				testSentinelPix(pix2, i)
			case x == 3 && y > 1:
				testSentinelPix(pix2, i)
			}
			pix2[i+3] = 255
		}
	}
	_, gw2, gh2 := trimSentinelBorder(pix2, w, h)
	if gw2 != w-1 || gh2 != h {
		t.Fatalf("trimSentinelBorder removed content: got %dx%d, want %dx%d", gw2, gh2, w-1, h)
	}
}

// TestCaptureMinimizedHonoursContext 调用方取消时立刻返回、不留悬挂 goroutine。
func TestCaptureMinimizedHonoursContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	start := time.Now()
	img, err := (capture{}).Window(ctx, 0x1234)
	if err == nil {
		t.Fatalf("cancelled context returned an image: %dx%d", img.Width, img.Height)
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled context error = %v, want context.Canceled", err)
	}
	if d := time.Since(start); d > 500*time.Millisecond {
		t.Fatalf("cancelled capture took %v", d)
	}
}

// TestCaptureMinimizedSolidColourPanel 整张同色的面板（例如暗色小工具、纯白页）也必须
// 抓得到。这是「不能按单色判内容」的回归：早先按「全图纯哨兵色 = 没画出来」判定时，
// 这类窗口会被误判成失败或回填一张只剩哨兵边角的假图。
func TestCaptureMinimizedSolidColourPanel(t *testing.T) {
	// 0x00202020：COLORREF 里的深灰，接近暗色面板。
	const solidDark = 0x00202020
	setTestPattern(solidDark)
	defer setTestPattern(0)

	hwnd, stop := startTestWindow(t)
	defer stop()
	before := minimizeTestWindow(t, hwnd)

	dec := grabMinimizedWindow(t, hwnd)
	b := dec.Bounds()
	if b.Dx() < 300 || b.Dy() < 200 {
		t.Fatalf("captured image is too small to be the real window: %v", b)
	}
	// 客户区应当是那片深灰：允许合成器/色彩空间带来少量偏差，数灰阶范围内的像素。
	dark := 0
	total := 0
	for y := b.Min.Y; y < b.Max.Y; y += 4 {
		for x := b.Min.X; x < b.Max.X; x += 4 {
			r, g, bl, _ := dec.At(x, y).RGBA()
			total++
			if r>>8 == 0x20 && g>>8 == 0x20 && bl>>8 == 0x20 {
				dark++
			}
		}
	}
	if dark*2 < total {
		t.Fatalf("the capture does not show the solid panel colour: %d of %d sampled pixels are the panel colour", dark, total)
	}

	if !iconic(hwnd) {
		t.Fatal("capturing a minimized window left it restored")
	}
	if after := testPlacement(hwnd); after != before {
		t.Fatalf("capturing a minimized window changed its placement: %v -> %v", before, after)
	}
}

// TestMinimizedServiceIsReusable 同一个服务连续服务多次抓取：辅助窗口复用后
// 每次仍拿得到内容（缩略图注销与窗口复位没有串味）。
func TestMinimizedServiceIsReusable(t *testing.T) {
	hwnd, stop := startTestWindow(t)
	defer stop()
	minimizeTestWindow(t, hwnd)

	var sizes [][2]int
	for i := 0; i < 3; i++ {
		img, err := (capture{}).Window(context.Background(), hwnd)
		if err != nil {
			t.Skipf("the desktop compositor does not provide window previews in this session: %v", err)
		}
		sizes = append(sizes, [2]int{img.Width, img.Height})
		if img.Width < 300 || img.Height < 200 {
			t.Fatalf("capture %d is too small to be the real window: %dx%d", i, img.Width, img.Height)
		}
	}
	for i := 1; i < len(sizes); i++ {
		if sizes[i] != sizes[0] {
			t.Fatalf("repeated captures returned different sizes: %v", sizes)
		}
	}
}

// testPlacement 只需比较前后是否一致，故摊平成可比较的数组。
func testPlacement(hwnd uintptr) [6]int32 {
	var p struct {
		length   uint32
		flags    uint32
		showCmd  uint32
		ptMin    winPoint
		ptMax    winPoint
		rcNormal winRect
	}
	p.length = uint32(unsafe.Sizeof(p))
	procTestGetWindowPlacement.Call(hwnd, uintptr(unsafe.Pointer(&p)))
	return [6]int32{
		int32(p.showCmd),
		p.rcNormal.left, p.rcNormal.top, p.rcNormal.right, p.rcNormal.bottom,
		int32(p.flags),
	}
}
