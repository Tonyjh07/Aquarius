//go:build windows

// 次窗（设置/会话历史/欢迎/查看原文）的平台操作：关闭投递与聚焦。
package platform

// CloseWindow 平台关闭投递：次窗无 WM_CLOSE 子类化 → Gio 默认销毁 → DestroyEvent
// → 事件循环退出。PostMessage 自身线程安全（异步投递、不回投等待，铁律 1 不涉）。
func (p *Plat) CloseWindow(h Handle) {
	if h == 0 {
		return
	}
	procPostMessageW.Call(uintptr(h), wmClose, 0, 0)
}

// FocusWindow 尽力唤到前台（单实例重开时）：ShowWindow/SetForegroundWindow 属修改性
// 调用——经该窗自己的 Window.Run 送其窗口线程（铁律 1 同款；run/hwnd 未就绪 =
// 放弃聚焦，单实例语义已由调用方注册表保证）。
func (p *Plat) FocusWindow(h Handle, run RunFunc) {
	if h == 0 || run == nil {
		return
	}
	run(func() {
		procShowWindow.Call(uintptr(h), swRestore)
		procSetForegroundWindow.Call(uintptr(h))
	})
}
