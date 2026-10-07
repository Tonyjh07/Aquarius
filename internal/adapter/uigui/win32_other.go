//go:build !windows

// 非 Windows 平台实现（降级，非桩；D111/S4b 前的过渡形态）：GUI 窗口壳仅 Windows
// 实测（§15.6），其余平台经本文件补齐构建并给出可用降级——ULW 像素管线/半透明为
// no-op（Gio 常规不透明窗照常渲染内容），窗口句柄/显示器/DPI 查询缺失项回落中性值，
// 托盘/全局热键/原生菜单不发（S4b 迁入平台层后由 platform 包统一承担）。
package uigui

import "gioui.org/io/event"

// viewEvent 非 Windows 无 Win32 视图事件。
func (u *UI) viewEvent(event.Event) (uintptr, bool) { return 0, false }

// windowRectPx 无窗口句柄可查。
func windowRectPx() (rect, bool) { return rect{}, false }

// moveWindowTo 无窗口可移。
func moveWindowTo(int32, int32) {}

// resizeWindowTo 窗口尺寸热改未适配（Gio 常规窗口，D90）。
func resizeWindowTo(int32, int32) {}

// cursorPos 无光标跟踪。
func cursorPos() point { return point{} }

// windowFromPoint 非 Windows 无 OS 逐像素命中查询（分层窗语义是 Windows 专有）：
// 恒 0 = 无窗口。调用点（cursorHitsRect）在 hwnd==0 时已短路，非 Windows 下 u.hwnd
// 恒 0（Win32ViewEvent 不投递），故此实现不可达——降级语义 = 命中只看矩形。
func windowFromPoint(point) uintptr { return 0 }

// platformWorkArea 非 Windows 无显示器信息源（夹取/吸附/停靠整体不干预）。
func platformWorkArea(point) (rect, bool) { return rect{}, false }

// platformMonitorAt 无显示器拓扑（停靠永不触发）。
func platformMonitorAt(point) bool { return false }

// platformWindowDPI 无 DPI 源（恒 1.0 = 100% 口径，D90）。
func platformWindowDPI(uintptr) float64 { return 1.0 }

// mainPresent 整窗 ULW 未适配（Gio 常规渲染路径照常显示）。
func mainPresent(int32, int32, int32, int32, []byte, byte) bool { return false }

// ensureLayeredStyle 非 Windows 无分层窗口概念（Gio 常规渲染）。
func ensureLayeredStyle(uintptr) {}

// mainVisible 主窗可见性未适配（恒真——非 Windows 无隐藏路径）。
func mainVisible() bool { return true }

// topMostQuery 非 Windows 无置顶概念（恒真，缺省口径与 Windows 一致）。
func topMostQuery() bool { return true }

// platformSetTopMost 置顶开关未适配。
func platformSetTopMost(bool) {}

// startShell 托盘与全局快捷键未适配（§15.6 仅 Windows 实测）。
func startShell(*UI) {}

// trayDelete 无托盘图标可清。
func trayDelete() {}

// reRegisterHotkey 无全局快捷键可重注册（startShell 未适配）。
func reRegisterHotkey() {}

// postLogoMenu logo 右键菜单未适配（shell 线程不存在，§15.6 仅 Windows 实测）。
func postLogoMenu() {}

// postBubbleMenu 气泡右键菜单未适配（shell 线程不存在，D92 仅 Windows 实测）。
func postBubbleMenu() {}

// subclassCloseToHide 关窗拦截未适配（窗口照常销毁）。
func subclassCloseToHide(uintptr) {}

// hideFromTaskbar 任务栏屏蔽未适配（窗口照常上任务栏）。
func hideFromTaskbar(uintptr) {}

// hideUntilFirstPresent 非 Windows 无隐藏路径（Win32ViewEvent 不投递、onHWND 不会跑）。
func hideUntilFirstPresent(uintptr) {}

// revealMainWindow 非 Windows 无揭示路径（Gio 常规渲染本就直接显示）。
func revealMainWindow(uintptr) {}
