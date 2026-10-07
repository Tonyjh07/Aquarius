//go:build windows

// 右键菜单呈现（Windows，TPM）：HMENU 构建（buildMenu）、TrackPopupMenu 呈现
// （runMenu）与命令回传（项集与命令语义归宿主，菜单**项集**是中性数据，见 uigui/menu.go）。
package platform

import (
	"syscall"
	"unsafe"
)

var (
	procCreatePopupMenu = user32.NewProc("CreatePopupMenu")
	procAppendMenuW     = user32.NewProc("AppendMenuW")
	procTrackPopupMenu  = user32.NewProc("TrackPopupMenu")
	procDestroyMenu     = user32.NewProc("DestroyMenu")
)

// 菜单常量与命令 ID（Win32 头文件取值）。
const (
	logoMenuMsg   = wmApp + 3 // 托盘线程呈现 logo 右键菜单（D72：Gio 检出右键后投递）
	bubbleMenuMsg = wmApp + 4 // 托盘线程呈现气泡右键菜单（D92：项集由宿主现取）

	mfString    = 0x00000000
	mfSeparator = 0x00000800
	mfChecked   = 0x00000008 // 勾选（置顶开关当前态）
	mfPopup     = 0x00000010 // 二级菜单：父项 uIDNewItem = 子菜单句柄（D73 权限档）
	tpmRightBtn = 0x0002
	tpmRetCmd   = 0x0100 // 返回命令 ID（不再发 WM_COMMAND）
)

// showLogoMenu logo 右键菜单呈现（托盘线程，D72/D73）：项集回调宿主现取，owner = 托盘
// 消息窗（与托盘菜单同款 TPM 收尾）。
func (p *Plat) showLogoMenu() {
	h := shellHWND.Load()
	if h == 0 || p.host == nil {
		return
	}
	p.dispatch(MenuLogo, p.runMenu(h, p.host.MenuItems(MenuLogo)))
}

// showBubbleMenu 气泡右键菜单呈现（托盘线程，D92）：项集回调宿主现取（上下文在宿主
// 的原子槽，平台不碰），owner = 托盘消息窗（与 logo 菜单同款 TPM 收尾）。
func (p *Plat) showBubbleMenu() {
	h := shellHWND.Load()
	if h == 0 || p.host == nil {
		return
	}
	p.dispatch(MenuBubble, p.runMenu(h, p.host.MenuItems(MenuBubble)))
}

// dispatch 回传菜单选择给宿主（0 = 取消/无选中，不回传）。
func (p *Plat) dispatch(kind MenuKind, id uintptr) {
	if id != 0 && p.host != nil {
		p.host.MenuChosen(kind, id)
	}
}

// buildMenu 建单（D73：二级菜单父项 MF_POPUP 挂子句柄、递归建单；DestroyMenu 对父单
// 递归销毁子单）。仅建单不显示。
func (p *Plat) buildMenu(items []MenuItem) uintptr {
	menu, _, _ := procCreatePopupMenu.Call()
	for _, it := range items {
		if len(it.Sub) > 0 {
			sub := p.buildMenu(it.Sub)
			label, _ := syscall.UTF16PtrFromString(it.Label)
			procAppendMenuW.Call(menu, mfPopup, sub, uintptr(unsafe.Pointer(label)))
			continue
		}
		if it.ID == 0 {
			procAppendMenuW.Call(menu, mfSeparator, 0, 0)
			continue
		}
		lab, _ := syscall.UTF16PtrFromString(it.Label)
		f := uintptr(mfString)
		if it.Checked {
			f |= mfChecked // 勾选 = 当前置顶态 / 当前权限档
		}
		procAppendMenuW.Call(menu, f, it.ID, uintptr(unsafe.Pointer(lab)))
	}
	return menu
}

// runMenu 共享呈现件（D72/D73）：建单（buildMenu）→ 光标位 TrackPopupMenu
// （TPM_RETURNCMD 直接返回叶子命令，不发 WM_COMMAND）→ MSDN 收尾 WM_NULL → 销毁。
// 返回 0 = 取消。
// 【D116】呈现期把主窗降出 topmost 带、收尾按原值恢复：菜单窗不保证压在 topmost
// 主窗之上（用户实测：气泡菜单与窗体重叠的部分被窗体像素盖住——透缝处可见、卡片/
// 输入栏处被盖 = 窗在菜单之上），且遮挡只在其重叠时发生（"有时候"）。降档后菜单
// 必然在主窗之上；气泡/logo 右键时主窗本是前台窗，非 topmost 带内仍居最高，观感
// 无跳变；置顶的持久化只在 toggleTopMost，此处不影响记忆值。
func (p *Plat) runMenu(hwnd uintptr, items []MenuItem) uintptr {
	menu := p.buildMenu(items)
	pt := p.CursorPos()
	procSetForegroundWindow.Call(hwnd)
	wasTop := p.TopMost()
	p.SetTopMost(false)
	r, _, _ := procTrackPopupMenu.Call(menu, tpmRightBtn|tpmRetCmd,
		uintptr(pt.X), uintptr(pt.Y), 0, hwnd, 0)
	p.SetTopMost(wasTop)
	procPostMessageW.Call(hwnd, wmNull, 0, 0) // MSDN 要求：收尾防菜单不消失
	procDestroyMenu.Call(menu)
	return r
}
