//go:build !windows

// 非 Windows 平台实现（降级，非桩；D111③）：Gio 常规不透明窗渲染 + 系统 API 查询，
// 缺的能力发降级提示而不是静默装死。
//
// 与 Windows 的差别（已知降级，D111 修订④）：
//   - 无窗口句柄（Gio 不暴露原生句柄）→ 位置/尺寸/显隐/置顶交窗口管理器，主窗改常规
//     装饰窗（NativeWindowControl() = false，uigui 据此决定 app.Decorated）；
//   - 无 ULW 像素提交（Present 恒 false）→ 内容由 Gio 自身表面渲染，半透明/羽化/逐像素
//     穿透不可达；位置记忆/停靠/吸附随之失活（WorkArea/MonitorAt 无数据源）；
//   - 托盘与全局热键缺失（S4b 外壳面迁入后由 StartShell 发提示）。
package platform

// NativeWindowControl 非 Windows = false：无原生句柄，窗口位置/尺寸/显隐由窗口管理器
// 与 Gio 自身表面承担（uigui 据此走常规装饰窗 + 跳过自管窗口的机制）。
func (p *Plat) NativeWindowControl() bool { return false }

// MainHandle 非 Windows 无原生句柄（恒 0；uigui 的 u.hwnd 随之恒 0，命中判定只看矩形）。
func (p *Plat) MainHandle() Handle { return 0 }

// AttachMain 非 Windows 无句柄可挂（onHWND 不会触发：Gio 不暴露原生窗口）。
func (p *Plat) AttachMain(Handle) {}

// SetMainRun 非 Windows 无「窗口线程投递」需求（无修改性原生调用经由它下发）。
func (p *Plat) SetMainRun(RunFunc) {}

// WindowRect 无句柄可查（调用方回落自记账坐标）。
func (p *Plat) WindowRect() (Rect, bool) { return Rect{}, false }

// MoveWindow 无原生定位能力（窗口拖动交窗口管理器）。
func (p *Plat) MoveWindow(int32, int32) {}

// ResizeWindow 无原生改尺寸能力（ui.window_width/height 在非 Windows 不生效，属已知降级）。
func (p *Plat) ResizeWindow(int32, int32) {}

// CursorPos 无光标坐标源（依赖光标直采的停靠/悬停 tips 随之失活）。
func (p *Plat) CursorPos() Point { return Point{} }

// WindowFromPoint 无 OS 逐像素命中查询（分层窗语义是 Windows 专有）：恒 0 = 无窗口。
func (p *Plat) WindowFromPoint(Point) Handle { return 0 }

// WorkArea 无显示器工作区源（夹取/吸附/停靠整体不干预）。
func (p *Plat) WorkArea(Point) (Rect, bool) { return Rect{}, false }

// MonitorAt 无显示器拓扑（停靠永不触发）。
func (p *Plat) MonitorAt(Point) bool { return false }

// WindowDPI 无逐窗 DPI 源（恒 1.0 = 100% 口径，D90）；实际缩放由 Gio 的 Metric 承担。
func (p *Plat) WindowDPI(Handle) float64 { return 1.0 }

// MainDPI 同 WindowDPI（恒 1.0）。
func (p *Plat) MainDPI() float64 { return 1.0 }

// HideFromTaskbar 无任务栏/Alt+Tab 屏蔽概念（窗口管理器自行决定）。
func (p *Plat) HideFromTaskbar(Handle) {}

// Visible 主窗恒可见（非 Windows 无我方发起的隐藏路径）。
func (p *Plat) Visible() bool { return true }

// TopMost 无置顶查询（恒真，缺省口径与 Windows 一致）。
func (p *Plat) TopMost() bool { return true }

// SetTopMost 无置顶设置能力。
func (p *Plat) SetTopMost(bool) {}

// EnsureLayered 无分层窗口概念（Gio 常规渲染）。
func (p *Plat) EnsureLayered(Handle) {}

// HideUntilFirstPresent 无隐藏路径（无 Win32ViewEvent、onHWND 不会跑；启动防闪不适用）。
func (p *Plat) HideUntilFirstPresent(Handle) {}

// RevealWindow 无揭示路径（Gio 常规窗口本就由窗口管理器显示）。
func (p *Plat) RevealWindow(Handle) {}

// HideWindow 无隐藏能力（最小化/隐藏交窗口管理器；呼出/隐藏经托盘缺失，属已知降级）。
func (p *Plat) HideWindow(Handle) {}

// HideMain 同 HideWindow（非 Windows 无我方发起的隐藏路径）。
func (p *Plat) HideMain(Handle) {}

// Present 无整窗像素提交（恒 false）：内容由 Gio 自身表面渲染——无 ULW 半透明/羽化/
// 逐像素穿透，alpha 通道不参与合成（uigui 据此跳过离屏合成通道，省一遍全帧重渲）。
func (p *Plat) Present(int32, int32, int32, int32, []byte, byte) bool { return false }
