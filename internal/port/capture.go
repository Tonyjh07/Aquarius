package port

import (
	"context"
	"errors"
)

// ErrCaptureUnsupported 本平台不提供屏幕捕获（DESIGN §5.11）。
// 工具层据此翻成给模型的可行动提示，而不是静默返回空清单。
var ErrCaptureUnsupported = errors.New("screen capture is not supported on this platform")

// Rect 屏幕矩形（物理 px，语义同 image.Rectangle：含 Left/Top、不含 Right/Bottom）。
type Rect struct{ Left, Top, Right, Bottom int }

// WindowInfo 一个可抓取的顶层窗口。
type WindowInfo struct {
	Handle     uintptr // 平台句柄（不透明；仅回传给 Capture 用）
	Title      string
	App        string // 可执行文件名（进程已退出/无权限时为空）
	Rect       Rect   // 屏幕坐标（物理 px，含 Min 不含 Max）
	Foreground bool
}

// Image 已编码的图片字节（PNG）与像素尺寸。
type Image struct {
	Data          []byte
	Width, Height int
}

// Capture 屏幕捕获端口（D178）：列窗口、抓整屏、抓指定窗口。
//
// 不做 DPI 缩放：调用方按物理像素拿图，需要缩小时自己缩（适配器只负责「抓得到」）。
type Capture interface {
	// Windows 顶层可见窗口，z 序自顶向下；不含本进程自己的窗口。
	Windows(ctx context.Context) ([]WindowInfo, error)
	// Screen 整个虚拟桌面（多显示器合成）。
	Screen(ctx context.Context) (Image, error)
	// Window 指定窗口，含被遮挡、非顶层、最小化。
	Window(ctx context.Context, h uintptr) (Image, error)
}
