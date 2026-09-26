//go:build !windows

// 非 Windows 桩：GUI 窗口壳仅 Windows 实测（§15.6），其余平台可构建、未适配——
// 形裁/半透明/淡出 overlay 一律 no-op，Win32ViewEvent 不投递（窗口保持普通卡片形态，
// 内容照常渲染）。
package uigui

import "gioui.org/io/event"

// viewEvent 非 Windows 无 Win32 视图事件。
func (u *UI) viewEvent(event.Event) (uintptr, bool) { return 0, false }

// windowRectPx 无窗口句柄可查。
func windowRectPx() (rect, bool) { return rect{}, false }

// moveWindowTo 无窗口可移。
func moveWindowTo(int32, int32) {}

// cursorPos 无光标跟踪。
func cursorPos() point { return point{} }

// clampToWorkArea 原样返回（无显示器信息源）。
func clampToWorkArea(x, y, _, _ int32) (int32, int32) { return x, y }

// applyShapesRegion 形裁未适配。
func applyShapesRegion([]shapePhys) bool { return false }

// applyAlpha 半透明未适配。
func applyAlpha(byte) bool { return false }

// overlayPresent 淡出 overlay 未适配。
func overlayPresent(int32, int32, int32, int32, []byte, byte) bool { return false }

// overlaySetVisible 淡出 overlay 未适配。
func overlaySetVisible(bool) {}

// topMostQuery 非 Windows 无置顶概念（恒真，缺省口径与 Windows 一致）。
func topMostQuery() bool { return true }

// platformSetTopMost 置顶开关未适配。
func platformSetTopMost(bool) {}

// startShell 托盘与全局快捷键未适配（§15.6 仅 Windows 实测）。
func startShell(*UI) {}

// trayDelete 无托盘图标可清。
func trayDelete() {}

// subclassCloseToHide 关窗拦截未适配（窗口照常销毁）。
func subclassCloseToHide(uintptr) {}
