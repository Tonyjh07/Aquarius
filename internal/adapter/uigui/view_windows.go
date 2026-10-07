//go:build windows

package uigui

import (
	"gioui.org/app"
	"gioui.org/io/event"

	"github.com/Tonyjh07/Aquarius/internal/adapter/uigui/platform"
)

// viewEvent 从 Gio 事件提取主窗原生句柄（`app.Win32ViewEvent` 仅 Windows 定义）。平台层
// 不 import gio（D111 修订①），故这一步提取留在 uigui——句柄对平台是不透明 DTO。
func (u *UI) viewEvent(ev event.Event) (platform.Handle, bool) {
	e, ok := ev.(app.Win32ViewEvent)
	if !ok {
		return 0, false
	}
	return platform.Handle(e.HWND), true
}
