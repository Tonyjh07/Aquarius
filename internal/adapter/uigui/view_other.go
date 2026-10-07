//go:build !windows

package uigui

import (
	"gioui.org/io/event"

	"github.com/Tonyjh07/Aquarius/internal/adapter/uigui/platform"
)

// viewEvent 非 Windows 无 Win32 视图事件：Gio 不暴露原生窗口句柄，平台层也无需句柄
// （位置/尺寸/像素提交整体降级，见 platform 包）。
func (u *UI) viewEvent(event.Event) (platform.Handle, bool) { return 0, false }
