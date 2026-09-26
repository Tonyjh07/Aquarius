//go:build !windows

// 非 Windows 桩：GUI 窗口壳仅 Windows 实测（§15.6），其余平台可构建、未适配——
// 形裁/定位等补位能力一律 no-op，Win32ViewEvent 不投递。
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

// applyRegion 形裁未适配。
func applyRegion(_, _, _, _, _ int32) bool { return false }
