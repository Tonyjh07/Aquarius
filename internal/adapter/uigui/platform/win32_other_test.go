//go:build !windows

package platform

import "testing"

// TestDegradedSurface 非 Windows 降级面的契约（D111 修订④）：能力缺失必须**可判定**——
// 调用方据此走常规窗 + 跳过自管窗口机制，而不是等运行时静默失效。
func TestDegradedSurface(t *testing.T) {
	p := New(Config{})

	if p.NativeWindowControl() {
		t.Error("非 Windows 不能声称自管窗口（无原生句柄）")
	}
	if p.MainHandle() != 0 {
		t.Errorf("MainHandle = %d, want 0", p.MainHandle())
	}
	if got := p.MainDPI(); got != 1.0 {
		t.Errorf("MainDPI = %v, want 1.0（DPI 交 Gio Metric）", got)
	}
	if _, ok := p.WindowRect(); ok {
		t.Error("非 Windows 无窗口矩形可查（调用方回退自记账坐标）")
	}
	if _, ok := p.WorkArea(Point{}); ok {
		t.Error("非 Windows 无显示器工作区（停靠/夹取整体失活）")
	}
	if p.MonitorAt(Point{}) {
		t.Error("非 Windows 无显示器拓扑（停靠永不触发）")
	}
	if !p.Visible() {
		t.Error("非 Windows 主窗恒可见（无我方隐藏路径）")
	}
	if !p.TopMost() {
		t.Error("非 Windows 置顶恒真（缺省口径与 Windows 一致）")
	}
	if p.Present(0, 0, 4, 4, make([]byte, 4*4*4), 255) {
		t.Error("非 Windows 无 ULW 像素提交（内容由 Gio 自身表面渲染）")
	}
}

// TestShellServicesDegradeQuietly 外壳面（托盘/热键/菜单/文件框）在非 Windows 不 panic
// 且不发请求：缺失能力由 StartShell/PostMenu 发降级提示（提示经 Host 回传，宿主为 nil
// 时静默——headless 口径）。
func TestShellServicesDegradeQuietly(t *testing.T) {
	p := New(Config{})
	p.SetHost(nil)
	p.StartShell() // 无 Host：提示丢弃，不 panic
	p.ReloadHotkey()
	p.ShutdownShell()
	p.DropTray()
	p.PostMenu(MenuLogo)
	p.PostMenu(MenuBubble)
	p.CloseWindow(0)
	p.FocusWindow(0, nil)
	p.SubclassCloseToHide(0)
}

// TestDarkModeFallback 深色检测在无桌面环境命令时保守回落浅色（false），不 panic。
func TestDarkModeFallback(t *testing.T) {
	p := New(Config{})
	_ = p.DarkMode() // 结果取决于宿主桌面环境；此处只验不 panic 且为布尔
}

// TestFontCandidatesNonEmpty 非 Windows 也给出中文字体候选（缺失由上层回落 gofont）。
func TestFontCandidatesNonEmpty(t *testing.T) {
	p := New(Config{})
	if len(p.SystemFontCandidates()) == 0 {
		t.Error("非 Windows 应给出常见 CJK 字体候选路径")
	}
}
