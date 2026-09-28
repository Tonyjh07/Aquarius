//go:build windows

package uigui

// postClose 平台关闭投递：次窗无 WM_CLOSE 子类化 → Gio 默认销毁 → DestroyEvent
// → 事件循环退出。PostMessage 自身线程安全（异步投递、不回投等待，铁律 1 不涉）。
func (h *winHandle) postClose() {
	procPostMessageW.Call(h.hwnd.Load(), wmClose, 0, 0)
}

// focus 尽力唤到前台（单实例重开时）：ShowWindow/SetForegroundWindow 属修改性
// 调用——经次窗自己的 Window.Run 送其窗口线程（铁律 1 同款；runFn/hwnd 未就绪 =
// 放弃聚焦，单实例语义已由注册表保证）。
func (h *winHandle) focus() {
	hwnd := h.hwnd.Load()
	runFn := h.runFn.Load()
	if hwnd == 0 || runFn == nil {
		return
	}
	(*runFn)(func() {
		procShowWindow.Call(hwnd, swRestore)
		procSetForegroundWindow.Call(hwnd)
	})
}
