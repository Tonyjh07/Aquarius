//go:build windows

package screencap

import (
	"context"
	"fmt"
	"runtime"
	"sync"
	"syscall"
	"time"
	"unsafe"
)

// 最小化窗口的截图通道：DWM 缩略图。
//
// 最小化窗口不接收 WM_PAINT，PrintWindow（本质是 WM_PRINT）只能拿到占位框或标题栏残留，
// 而且照样返回成功——原样回填就是一张「看起来成功」的空图。系统里现成的那帧画面由 DWM
// 持有（任务栏悬停预览就是这么来的），所以这里反过来做：让 DWM 把源的缩略图合成到本进程
// 的一个离屏窗口上，再用 PrintWindow(PW_RENDERFULLCONTENT) 读那个窗口。
//
// 对用户完全无感：源窗口不动、不还原、不抢焦点，辅助窗口放在虚拟桌面最右侧之外。
// 代价是缩略图按整窗渲染（标题栏一并入图）——最小化时客户区尺寸无从可靠取得
// （GetClientRect 塌成占位框，按窗口样式反推对自绘边框的窗口不准），不做会猜错的裁剪。

const (
	thumbSettle     = 40 * time.Millisecond
	thumbPoll       = 25 * time.Millisecond
	thumbWaitBudget = 1500 * time.Millisecond
	thumbIdleSize   = 64
	// thumbOffscreenGap 辅助窗口距虚拟桌面右缘的距离：足够远，任何布局下都不会露出来。
	thumbOffscreenGap = 2000
	// thumbSentinelColor 辅助窗口的底色（COLORREF = 0x00BBGGRR，洋红）。它只回答
	// 「缩略图到了没」：判定是与基线帧做差，不靠颜色本身，所以真实内容是纯黑、纯白、
	// 还是单色面板都不影响。合成器合成时会给边缘做抗锯齿混合，故识别靠 sentinelLike
	// 的阈值而不是精确相等。
	thumbSentinelColor = 0x00FF00FF
	// 识别哨兵底色的阈值（含混合后）：红蓝都高、绿明显低、红蓝相近。见 sentinelLike。
	sentinelMinRB = 140
	sentinelMaxG  = 110
	sentinelMaxRB = 40
)

// thumbResult 一次缩略图抓取的结果。
type thumbResult struct {
	pix  []byte
	w, h int
	err  error
}

// thumbRequest 一次抓取请求；out 由专用线程回填。
type thumbRequest struct {
	ctx  context.Context
	hwnd uintptr
	out  chan thumbResult
}

// thumbService 常驻的离屏缩略图窗口。窗口操作全部发生在一个 LockOSThread 钉住的
// goroutine 里：Win32 窗口有线程亲和性，跨线程销毁会失败或死锁（实测），因此辅助窗口
// 建一次就不再重建，只改尺寸与缩略图属性。
type thumbService struct {
	reqs chan thumbRequest
}

var (
	thumbOnce   sync.Once
	thumbShared *thumbService
)

// captureMinimized 用 DWM 缩略图抓一个最小化窗口。
func captureMinimized(ctx context.Context, hwnd uintptr) ([]byte, int, int, error) {
	s := thumbServiceFor()
	res, err := s.capture(ctx, hwnd)
	if err != nil {
		return nil, 0, 0, err
	}
	return res.pix, res.w, res.h, nil
}

func thumbServiceFor() *thumbService {
	thumbOnce.Do(func() {
		thumbShared = &thumbService{reqs: make(chan thumbRequest)}
		go thumbShared.run()
	})
	return thumbShared
}

func (s *thumbService) capture(ctx context.Context, hwnd uintptr) (thumbResult, error) {
	out := make(chan thumbResult, 1)
	select {
	case s.reqs <- thumbRequest{ctx: ctx, hwnd: hwnd, out: out}:
	case <-ctx.Done():
		return thumbResult{}, ctx.Err()
	}
	select {
	case res := <-out:
		return res, res.err
	case <-ctx.Done():
		// 抓取已在专用线程里开始：让它自己收尾（注销缩略图、复位窗口），结果丢弃。
		go func() { <-out }()
		return thumbResult{}, ctx.Err()
	}
}

// run 专用线程：建一次离屏辅助窗口，然后循环服务抓取请求。
func (s *thumbService) run() {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	hwnd, anchorX, err := newThumbWindow()
	if err != nil {
		// 建不出窗口时仍继续服务，让每次请求立刻拿到同一个原因。
		for req := range s.reqs {
			req.out <- thumbResult{err: err}
		}
		return
	}
	for {
		select {
		case req := <-s.reqs:
			req.out <- grabViaThumbnail(req.ctx, hwnd, anchorX, req.hwnd)
		default:
			pumpMessages()
			time.Sleep(4 * time.Millisecond)
		}
	}
}

// newThumbWindow 建立离屏辅助窗口：无边框（整窗即客户区，读到什么就是什么）、
// 无标题（不进 window_list 的清单，那份清单只为用户可见窗口服务）、工具窗口
// （不进任务栏与 Alt+Tab）、不激活。位置在虚拟桌面最右侧之外，用户看不见。
//
// 背景取**哨兵色**（洋红）而不是黑或系统窗口色：合成器还没把缩略图放上来时，读到的就是
// 这个底色。它既不会被当成「纯黑 = 没画出来」（暗色面板本身就可能整张近黑，那样会被误
// 判成失败），也不可能被当作真实内容——判定靠「与注册缩略图前的基线帧做差」，见
// grabViaThumbnail。这样单色窗口（纯黑、纯白、纯色面板）也能正常取到。
func newThumbWindow() (hwnd uintptr, anchorX int32, err error) {
	hInst, _, _ := procGetModuleHandleW.Call(0)
	name, _ := syscall.UTF16PtrFromString("AquariusCaptureThumbnail")
	brush, _, _ := procCreateSolidBrush.Call(thumbSentinelColor)
	if brush == 0 {
		return 0, 0, fmt.Errorf("create the preview window background brush")
	}
	procRegisterClassExW.Call(uintptr(unsafe.Pointer(&wndClassExW{
		cbSize:        uint32(unsafe.Sizeof(wndClassExW{})),
		style:         csHRedraw | csVRedraw,
		lpfnWndProc:   defWindowProc,
		hInstance:     hInst,
		hbrBackground: brush,
		lpszClassName: name,
	})))

	vx, _, _ := procGetSystemMetrics.Call(smXVirtualScreen)
	vw, _, _ := procGetSystemMetrics.Call(smCXVirtualScreen)
	anchorX = offscreenAnchorX(int32(vx), int32(vw))
	hwnd, _, callErr := procCreateWindowExW.Call(
		wsExToolWindow|wsExNoActivate,
		uintptr(unsafe.Pointer(name)), 0, // 无标题：不进 window_list
		wsPopup, uintptr(anchorX), 0, thumbIdleSize, thumbIdleSize,
		0, 0, hInst, 0)
	if hwnd == 0 {
		return 0, 0, fmt.Errorf("create the off-screen preview window: %w", callErr)
	}
	procShowWindow.Call(hwnd, swShowNoActivate)
	waitFrames(60 * time.Millisecond)
	return hwnd, anchorX, nil
}

// grabViaThumbnail 在一个最小化窗口上跑一次「注册缩略图 → 等首帧 → 读窗口 → 注销」。
func grabViaThumbnail(ctx context.Context, dst uintptr, anchorX int32, src uintptr) thumbResult {
	var thumb uintptr
	if hr, _, _ := procDwmRegisterThumbnail.Call(dst, src, uintptr(unsafe.Pointer(&thumb))); int32(hr) != 0 {
		return thumbResult{err: fmt.Errorf("the window is minimized and the desktop compositor refused its preview (0x%x); try target \"screen\"", uint32(hr))}
	}
	// 无论走哪条返回路径都要注销：否则辅助窗口会把这张缩略图一直挂在身上。
	defer procDwmUnregisterThumbnail.Call(thumb)
	defer procSetWindowPos.Call(dst, 0, uintptr(anchorX), 0, thumbIdleSize, thumbIdleSize, swpNoActivate|swpNoZOrder)

	var sz struct{ cx, cy int32 }
	if hr, _, _ := procDwmQueryThumbnailSourceSize.Call(thumb, uintptr(unsafe.Pointer(&sz))); int32(hr) != 0 || sz.cx <= 0 || sz.cy <= 0 {
		return thumbResult{err: fmt.Errorf("the minimized window reports no preview size (%dx%d)", sz.cx, sz.cy)}
	}
	w, h := int(sz.cx), int(sz.cy)

	// 辅助窗口与缩略图同尺寸：1:1，不缩放。
	procSetWindowPos.Call(dst, 0, uintptr(anchorX), 0, uintptr(w), uintptr(h), swpNoActivate|swpNoZOrder)
	waitFrames(thumbSettle)

	sdc, _, _ := procGetDC.Call(0)
	if sdc == 0 {
		return thumbResult{err: fmt.Errorf("cannot get the screen device context")}
	}
	defer procReleaseDC.Call(0, sdc)

	// 基线帧：此刻缩略图还**不可见**（DWM 缩略图注册后 fVisible 默认为 FALSE），所以读到的是
	// 辅助窗口自己那副样子。之后「与基线不同」就等于缩略图已经盖上来。基线要读到稳定态，
	// 否则它自己还在变（放大后擦背景、未初始化区域补画）就会被当成内容。
	baseline := stableFrame(dst, sdc, w, h)

	props := thumbnailProperties{
		flags:   dtpRectDestination | dtpVisible | dtpOpacity,
		dest:    winRect{right: int32(w), bottom: int32(h)},
		opacity: 255,
		visible: 1,
	}
	if hr, _, _ := procDwmUpdateThumbnailProperties.Call(thumb, uintptr(unsafe.Pointer(&props))); int32(hr) != 0 {
		return thumbResult{err: fmt.Errorf("the desktop compositor refused the preview properties (0x%x)", uint32(hr))}
	}

	var (
		pix      []byte
		deadline = time.Now().Add(thumbWaitBudget)
	)
	for {
		if err := ctx.Err(); err != nil {
			return thumbResult{err: err}
		}
		waitFrames(thumbPoll)
		shot, err := printWindow(dst, sdc, w, h, pwRenderFullContent)
		if err == nil && composited(shot, baseline, w, h) {
			pix = shot
			break
		}
		if time.Now().After(deadline) {
			break
		}
	}
	if pix == nil {
		return thumbResult{err: fmt.Errorf("the minimized window composed no preview within %v; try target \"screen\"", thumbWaitBudget)}
	}
	pix, w, h = trimSentinelBorder(pix, w, h)
	return thumbResult{pix: pix, w: w, h: h}
}

// trimSentinelBorder 裁掉整列/整行都还是哨兵底色的边缘。
//
// 合成器把缩略图画进目标矩形后，边缘常常留下 1–2 像素的底色（实测右下各一条），
// 而且是被抗锯齿混合过的洋红。那点粉色对模型没有信息量，却足够突兀到被当成
// 「界面里有条粉线」。只裁**整条边**都还是底色的行列，内容里偶然出现的同色像素不会被削掉。
func trimSentinelBorder(pix []byte, w, h int) ([]byte, int, int) {
	if w <= 0 || h <= 0 || len(pix) < w*h*4 {
		return pix, w, h
	}
	colOnlySentinel := func(x, top, bottom int) bool {
		for y := top; y < bottom; y++ {
			if !sentinelLike(pix, (y*w+x)*4) {
				return false
			}
		}
		return true
	}
	rowOnlySentinel := func(y, left, right int) bool {
		for x := left; x < right; x++ {
			if !sentinelLike(pix, (y*w+x)*4) {
				return false
			}
		}
		return true
	}
	left, top, right, bottom := 0, 0, w, h
	for left < right-1 && colOnlySentinel(left, top, bottom) {
		left++
	}
	for right > left+1 && colOnlySentinel(right-1, top, bottom) {
		right--
	}
	for top < bottom-1 && rowOnlySentinel(top, left, right) {
		top++
	}
	for bottom > top+1 && rowOnlySentinel(bottom-1, left, right) {
		bottom--
	}
	if left == 0 && top == 0 && right == w && bottom == h {
		return pix, w, h
	}
	nw, nh := right-left, bottom-top
	out := make([]byte, nw*nh*4)
	for y := 0; y < nh; y++ {
		src := ((y+top)*w + left) * 4
		copy(out[y*nw*4:(y+1)*nw*4], pix[src:src+nw*4])
	}
	return out, nw, nh
}

// sentinelLike 判断某个像素是不是哨兵底色或它被混合后的样子。
//
// 合成器把缩略图画进目标矩形时，边缘那一两个像素是抗锯齿混合的结果：实测纯洋红
// (255,0,255) 会变成 (181,47,181)、(166,47,166) 一类。精确比对判不出来，所以按
// 「红蓝都高、绿明显低、红蓝相近」认它。这是**底色记号**的判据，不是内容的判据。
func sentinelLike(pix []byte, i int) bool {
	r, g, b := int(pix[i]), int(pix[i+1]), int(pix[i+2])
	if r < sentinelMinRB || b < sentinelMinRB || g > sentinelMaxG {
		return false
	}
	diff := r - b
	if diff < 0 {
		diff = -diff
	}
	return diff <= sentinelMaxRB
}

// composited 判断这一帧是否已经盖上缩略图内容：与基线帧比较，只要有一个像素不同就算到了。
// 不按颜色本身判内容——纯黑、纯白、单色面板都是合法内容，按单色判会把用户正要看的画面
// 拒掉（与 blank 的口径一致）。基线读不到时退回「不全为哨兵底色」。
func composited(shot, baseline []byte, w, h int) bool {
	if w <= 0 || h <= 0 || len(shot) < w*h*4 {
		return false
	}
	if len(baseline) < w*h*4 {
		return notAllSentinel(shot, w, h)
	}
	for i := 0; i+3 < len(shot); i += 4 {
		if shot[i] != baseline[i] || shot[i+1] != baseline[i+1] || shot[i+2] != baseline[i+2] {
			return true
		}
	}
	return false
}

// notAllSentinel 整张图没有任何一个像素偏离哨兵色时返回 false。
func notAllSentinel(shot []byte, w, h int) bool {
	if w <= 0 || h <= 0 || len(shot) < w*h*4 {
		return false
	}
	for i := 0; i+3 < len(shot); i += 4 {
		if !sentinelLike(shot, i) {
			return true
		}
	}
	return false
}

// stableFrame 读一张「稳定下来」的辅助窗口画面作为基线（专用线程调用）。
//
// 窗口刚放大时新露出的区域是未初始化的，系统擦背景也可能滞后一帧：只读一张容易把
// 「正在擦」的中间态当成基线，之后它自己变稳就会被当成「缩略图来了」。这里读到相邻两帧
// 完全相同为止，次数用尽就返回最后一张。
func stableFrame(dst, sdc uintptr, w, h int) []byte {
	var prev []byte
	for attempt := 0; attempt < 5; attempt++ {
		waitFrames(thumbPoll)
		cur, err := printWindow(dst, sdc, w, h, pwRenderFullContent)
		if err != nil || len(cur) < w*h*4 {
			continue
		}
		if prev != nil && sameFrame(prev, cur, w, h) {
			return cur
		}
		prev = cur
	}
	return prev
}

// sameFrame 两张图逐像素相同（只看颜色，不看 alpha）。
func sameFrame(a, b []byte, w, h int) bool {
	if len(a) < w*h*4 || len(b) < w*h*4 {
		return false
	}
	for i := 0; i+3 < len(a); i += 4 {
		if a[i] != b[i] || a[i+1] != b[i+1] || a[i+2] != b[i+2] {
			return false
		}
	}
	return true
}
