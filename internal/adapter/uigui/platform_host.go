package uigui

// 平台回调宿主（D111 修订②）：平台层只回传动作枚举，业务语义（命令注入、开窗、写
// config、帖转写）都留在这里。实现须线程安全——回调可能来自托盘线程/对话框 goroutine。

import "github.com/Tonyjh07/Aquarius/internal/adapter/uigui/platform"

// platHost 把平台回调桥到 UI（u 的方法本身已按跨线程口径实现：post 线程安全、
// 菜单项集只读原子槽/回调访问器）。
type platHost struct{ u *UI }

// MenuItems 按出处现组菜单项集（勾选态/权限档随时间变，故呈现前回调现取）。
func (h *platHost) MenuItems(kind platform.MenuKind) []platform.MenuItem {
	return h.u.menuItems(kind)
}

// MenuChosen 菜单项被选中 → 交回 uigui 分发（与键入同路径的语义在此）。
func (h *platHost) MenuChosen(kind platform.MenuKind, id uintptr) {
	h.u.menuChosen(kind, id)
}

// ToggleShow 托盘左键：显隐切换。
func (h *platHost) ToggleShow() { h.u.toggleWindow() }

// HotkeyPressed 全局快捷键：隐藏 → 呼出；可见 → 展开↔收起。
func (h *platHost) HotkeyPressed() { h.u.hotkeyToggle() }

// ToggleTopMost 置顶开关（含位置记忆落盘）。
func (h *platHost) ToggleTopMost() { h.u.toggleTopMost() }

// Exit 菜单退出（中断进行中轮次 → EOF 收尾）。
func (h *platHost) Exit() { h.u.exitViaShell() }

// FilePicked 文件选择框选中（空 = 取消）：投 attachMsg 进事件循环暂存。
func (h *platHost) FilePicked(path string) {
	if path == "" {
		return
	}
	h.u.post(attachMsg{path: path})
}

// SystemThemeChanged 系统深浅变化（仅 system 档重解析）。
func (h *platHost) SystemThemeChanged() { h.u.post(sysThemeMsg{}) }

// HotkeySetting 当前快捷键配置（平台注册/改绑时现取；空 = 未配置）。
func (h *platHost) HotkeySetting() string {
	if v, ok := h.u.hotkeyCfg.Load().(string); ok {
		return v
	}
	return h.u.opts.Hotkey
}

// Notice 平台降级提示 → 转写区一条纯文本（用户可见的中文提示）。
func (h *platHost) Notice(msg string) {
	if msg == "" {
		return
	}
	h.u.post(sayMsg{text: msg})
}
