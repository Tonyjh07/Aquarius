package uigui

// 平台接口（消费方定义，D111 修订②）：`uigui` 主体只经本接口触达平台能力，具体实现见
// `internal/adapter/uigui/platform`（纯 Go、不依赖 gio，三平台可各自交叉编译与单测）。
//
// 接口随 S4b 分批增长：每迁入一面方法，Windows 实现与非 Windows 降级实现必须同时给出
// ——差一个方法 `*platform.Plat` 就满足不了本接口，`platformImpl` 断言即编译失败。
//
// 测试替身：headless 测试可把 `u.plat` 换成假实现（原包级注入槽 presentMain/revealMain/
// visMain 随平台面迁入后退役）。

import (
	"github.com/Tonyjh07/Aquarius/assets"
	"github.com/Tonyjh07/Aquarius/internal/adapter/uigui/platform"
)

// platformImpl 当前实现满足消费方接口的编译期断言。
var _ platformAPI = (*platform.Plat)(nil)

// platformAPI 平台能力（uigui → 平台）。
type platformAPI interface {
	// NativeWindowControl 平台是否自管窗口位置/尺寸与像素提交（Windows = true；非
	// Windows = false → 常规装饰窗 + Gio 自身表面渲染）。
	NativeWindowControl() bool

	// —— 主窗句柄与几何 ——
	MainHandle() platform.Handle
	AttachMain(h platform.Handle)
	SetMainRun(platform.RunFunc)
	WindowRect() (platform.Rect, bool)
	MoveWindow(x, y int32)
	ResizeWindow(w, h int32)
	WindowDPI(h platform.Handle) float64
	MainDPI() float64
	CursorPos() platform.Point
	WindowFromPoint(platform.Point) platform.Handle
	WorkArea(platform.Point) (platform.Rect, bool)
	MonitorAt(platform.Point) bool

	// —— 显隐 / 置顶 / 任务栏 ——
	HideFromTaskbar(h platform.Handle)
	Visible() bool
	TopMost() bool
	SetTopMost(on bool)

	// —— 启动防闪的隐藏与揭示（D78）——
	HideUntilFirstPresent(h platform.Handle)
	EnsureLayered(h platform.Handle)
	RevealWindow(h platform.Handle)
	HideWindow(h platform.Handle)
	HideMain(h platform.Handle) // 任意线程（内部走窗口线程）

	// —— 整窗像素提交（ULW 或降级）——
	Present(x, y, w, h int32, bits []byte, alpha byte) bool

	// —— 系统外观 ——
	DarkMode() bool
	SystemFontCandidates() []string

	// —— 外壳（托盘 / 全局热键 / 原生菜单 / 文件框；非 Windows 为降级实现）——
	SetHost(platform.Host)
	StartShell()
	ShutdownShell()
	DropTray()
	ReloadHotkey()
	PostMenu(kind platform.MenuKind)
	RequestFileDialog()
	SubclassCloseToHide(h platform.Handle)

	// —— 次窗 ——
	CloseWindow(h platform.Handle)
	FocusWindow(h platform.Handle, run platform.RunFunc)
}

// newPlatform 构造平台实现（组装点：`uigui` 内唯一 import 具体实现处）。
// trayIconPx 托盘图标取哪一档（像素）：32 = 16 档的 4 倍像素量。**不是显示尺寸**——16px
// 是 shell 在 100% DPI 下的托盘尺寸；这里给 shell 的是**单张位图**，档位越高、高 DPI 下越
// 少插值（Windows 会自行缩到它需要的尺寸）。48/64 档也在 aquarius.ico 里，取 32 是为
// 「清晰一倍」的最小代价：托盘图标按 16 显示，32 源在下采样时边缘干净；取 256 只会让
// 图标文件与 DIB 变大而无进一步收益（用户 2026-10-13 报障：托盘图标模糊，可再清晰一倍）。
const trayIconPx = 32

func newPlatform(opts Options, window bool) platformAPI {
	cfg := platform.Config{Hotkey: opts.Hotkey}
	if window {
		// 托盘图标：.ico 内嵌资源 → trayIconPx 档（平台无关解析在此完成，平台只负责呈现）。
		// 档位是**交给 shell 的单张位图**：取越小，shell 在 >100% DPI 的任务栏上越要靠
		// 插值放大（用户 2026-10-13 报「托盘图标模糊，可以再清晰一倍」）。取 32px 档 =
		// 16px 档的 4 倍像素量，100% DPI 下 shell 缩一半仍是清晰下采样，125%/150% 下
		// 有原生像素可用；同时不给任务栏造成显存/带宽负担（托盘图标极小）。
		if b, err := icoImage(assets.TrayICO, trayIconPx); err == nil {
			cfg.TrayIcon = b
		}
	}
	return platform.New(cfg)
}
