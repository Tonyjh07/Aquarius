package uigui

// 假平台实现（headless 测试，S4b）：替代原先的包级注入槽（presentMain/revealMain/
// visMain/mainHWND），并记录平台调用供断言。零值语义对齐「平台未接管的降级档」。
//
// 用法：`u := newFrameUI()` 已装好一个假平台，取 `fp := fakeOf(u)` 配置钩子/读记账。
// 钩子为 nil = 只用可配置状态；记账字段仅供单 goroutine 断言（梯次 goroutine 场景用
// 原子钩子计数，见 window_test.go 的 D115 用例）。

import (
	"sync"

	"github.com/Tonyjh07/Aquarius/internal/adapter/uigui/platform"
)

type fakePlat struct {
	mu sync.Mutex

	// —— 可配置状态 ——
	Native     bool            // NativeWindowControl
	Main       platform.Handle // MainHandle
	MainScore  float64         // MainDPI / WindowDPI 返回值
	Rect       platform.Rect   // WindowRect 返回值
	RectOK     bool
	Cursor     platform.Point
	Work       platform.Rect
	WorkOK     bool
	Monitor    bool
	VisibleNow bool // Visible
	TopNow     bool // TopMost
	PresentOK  bool // Present 返回值
	Dark       bool
	Fonts      []string

	// —— 调用钩子（nil = 仅记账）——
	OnPresent  func(x, y, w, h int32, bits []byte, alpha byte) bool
	OnReveal   func(h platform.Handle)
	OnMove     func(x, y int32)
	OnResize   func(w, h int32)
	OnTopMost  func(on bool)
	OnNotice   func(msg string)
	OnMenuPost func(kind platform.MenuKind)

	// —— 记账 ——
	Presents     int
	Reveals      int
	Hides        int
	Moves        []platform.Point
	Resizes      []platform.Point
	TopMostSets  []bool
	Subclassed   int
	Layered      int
	ShellStarts  int
	ShellStops   int
	Reloads      int
	FileRequests int
	MenuPosts    []platform.MenuKind
	Notices      []string
	Closes       []platform.Handle
	Focuses      []platform.Handle
}

// newFakePlat 构造假平台：缺省对齐「Windows 实测档」的可用语义（自管窗口、可见、置顶、
// 提交成功），个别用例按需覆盖——与旧槽注入的缺省行为一致。
func newFakePlat() *fakePlat {
	return &fakePlat{
		Native:     true,
		MainScore:  1.0,
		VisibleNow: true,
		TopNow:     true,
		PresentOK:  true,
		Fonts:      []string{},
		// 缺省虚拟屏（0,0)-(1920,1080)：夹取/停靠用例要确定性几何，不随开发机显示器变；
		// MonitorAt 缺省 false = 工作区外无屏（与单显示器实机同款：接缝边可停靠）。
		Work:   platform.Rect{Left: 0, Top: 0, Right: 1920, Bottom: 1080},
		WorkOK: true,
	}
}

// fakeOf 取 UI 上的假平台（newFrameUI 已装）。
func fakeOf(u *UI) *fakePlat {
	fp, ok := u.plat.(*fakePlat)
	if !ok {
		panic("测试未装假平台：请用 newFrameUI/newHeadless")
	}
	return fp
}

func (f *fakePlat) NativeWindowControl() bool   { return f.Native }
func (f *fakePlat) MainHandle() platform.Handle { return f.Main }

// AttachMain 记下挂接句柄（与真实现同语义：此后 Present/几何才有目标窗）。
func (f *fakePlat) AttachMain(h platform.Handle) {
	f.mu.Lock()
	f.Main = h
	f.mu.Unlock()
}

func (f *fakePlat) SetMainRun(platform.RunFunc)       {}
func (f *fakePlat) WindowRect() (platform.Rect, bool) { return f.Rect, f.RectOK }
func (f *fakePlat) WindowDPI(platform.Handle) float64 { return f.MainScore }
func (f *fakePlat) MainDPI() float64                  { return f.MainScore }
func (f *fakePlat) CursorPos() platform.Point         { return f.Cursor }
func (f *fakePlat) WindowFromPoint(platform.Point) platform.Handle {
	return f.Main // 缺省「命中的就是本窗」（命中直证用例的常见语义）
}
func (f *fakePlat) WorkArea(platform.Point) (platform.Rect, bool) { return f.Work, f.WorkOK }
func (f *fakePlat) MonitorAt(platform.Point) bool                 { return f.Monitor }
func (f *fakePlat) HideFromTaskbar(platform.Handle)               {}
func (f *fakePlat) Visible() bool                                 { return f.VisibleNow }
func (f *fakePlat) TopMost() bool                                 { return f.TopNow }
func (f *fakePlat) HideUntilFirstPresent(platform.Handle)         {}
func (f *fakePlat) EnsureLayered(platform.Handle)                 { f.Layered++ }
func (f *fakePlat) HideWindow(platform.Handle)                    { f.Hides++ }
func (f *fakePlat) HideMain(platform.Handle)                      { f.Hides++ }
func (f *fakePlat) DarkMode() bool                                { return f.Dark }
func (f *fakePlat) SystemFontCandidates() []string                { return f.Fonts }
func (f *fakePlat) SetHost(platform.Host)                         {}
func (f *fakePlat) DropTray()                                     {}

func (f *fakePlat) MoveWindow(x, y int32) {
	f.mu.Lock()
	f.Moves = append(f.Moves, platform.Point{X: x, Y: y})
	f.mu.Unlock()
	if f.OnMove != nil {
		f.OnMove(x, y)
	}
}

func (f *fakePlat) ResizeWindow(w, h int32) {
	f.mu.Lock()
	f.Resizes = append(f.Resizes, platform.Point{X: w, Y: h})
	f.mu.Unlock()
	if f.OnResize != nil {
		f.OnResize(w, h)
	}
}

func (f *fakePlat) SetTopMost(on bool) {
	f.mu.Lock()
	f.TopNow = on
	f.TopMostSets = append(f.TopMostSets, on)
	f.mu.Unlock()
	if f.OnTopMost != nil {
		f.OnTopMost(on)
	}
}

func (f *fakePlat) RevealWindow(h platform.Handle) {
	f.mu.Lock()
	f.Reveals++
	f.mu.Unlock()
	if f.OnReveal != nil {
		f.OnReveal(h)
	}
}

func (f *fakePlat) Present(x, y, w, h int32, bits []byte, alpha byte) bool {
	f.mu.Lock()
	f.Presents++
	f.mu.Unlock()
	if f.OnPresent != nil {
		return f.OnPresent(x, y, w, h, bits, alpha)
	}
	return f.PresentOK
}

func (f *fakePlat) StartShell()    { f.ShellStarts++ }
func (f *fakePlat) ShutdownShell() { f.ShellStops++ }
func (f *fakePlat) ReloadHotkey()  { f.Reloads++ }

func (f *fakePlat) PostMenu(kind platform.MenuKind) {
	f.MenuPosts = append(f.MenuPosts, kind)
	if f.OnMenuPost != nil {
		f.OnMenuPost(kind)
	}
}

func (f *fakePlat) RequestFileDialog() { f.FileRequests++ }
func (f *fakePlat) SubclassCloseToHide(platform.Handle) {
	f.Subclassed++
}

func (f *fakePlat) CloseWindow(h platform.Handle) {
	f.Closes = append(f.Closes, h)
}

func (f *fakePlat) FocusWindow(h platform.Handle, _ platform.RunFunc) {
	f.Focuses = append(f.Focuses, h)
}
