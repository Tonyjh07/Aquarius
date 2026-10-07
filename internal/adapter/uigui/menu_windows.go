//go:build windows

// 右键菜单（Windows，TPM）：托盘/logo/气泡三处菜单的项清单、HMENU 构建与命令
// 分发（D72/D73/D92/D97-D99；呈现共享件 runMenu 亦在此）。
package uigui

import (
	"syscall"
	"unsafe"

	"github.com/Tonyjh07/Aquarius/internal/domain/conversation"
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
	bubbleMenuMsg = wmApp + 4 // 托盘线程呈现气泡右键菜单（D92：上下文在 u.bubbleMenu 原子槽）

	mfString    = 0x00000000
	mfSeparator = 0x00000800
	mfChecked   = 0x00000008 // 勾选（置顶开关当前态）
	mfPopup     = 0x00000010 // 二级菜单：父项 uIDNewItem = 子菜单句柄（D73 权限档）
	tpmRightBtn = 0x0002
	tpmRetCmd   = 0x0100 // 返回命令 ID（不再发 WM_COMMAND）

	cmdToggle   = 101
	cmdExit     = 102
	cmdTopMost  = 103
	cmdHistory  = 104 // 功能窗入口（§15.7/D60：会话历史占位壳）
	cmdWelcome  = 105 // 功能窗入口（§15.7/D60：欢迎/首次运行占位壳）
	cmdSettings = 106 // 功能窗入口（§15.7/D60：设置窗核心档）
	cmdNew      = 107 // 新对话（logo 右键菜单，D72：注入 /new 与键入同路径）
	cmdPermBase = 108 // 权限子菜单四档基值（D73：+i 对齐 settings.go permLevels 序）

	cmdBubbleEdit     = 120 // 气泡右键：编辑（D92/D97 → editMsg Fresh 进编辑态）
	cmdBubbleRegen    = 121 // 气泡右键：重新生成（D92 → /regen 与键入同路径）
	cmdBubbleCopy     = 122 // 气泡右键：复制（D92 → copyMsg，下一帧写剪贴板）
	cmdBubbleEditKeep = 123 // 气泡右键：编辑并转移历史（D97 → editMsg Carry）
	cmdBubbleEditCopy = 124 // 气泡右键：编辑并复制历史（D97 → editMsg Clone）
	cmdBubbleQuote    = 125 // 气泡右键：引用（D98 → quoteMsg 追加引用前缀进输入框）
	cmdBubbleRaw      = 126 // 气泡右键：查看原文（D99 → rawView 原子槽 + winRaw 次窗）
)

// menuIt 菜单项（托盘/logo 右键菜单共用，D72/D73）；id 0 = 分隔线，或二级菜单父项
// （sub 非空时 id 不用）。
type menuIt struct {
	id      uintptr
	label   string
	checked bool
	sub     []menuIt // 非空 = 二级菜单（D73：权限四档）
}

// logoMenuItems logo 右键菜单（D72/D73，项序/文案为契约、测试锁定）：置顶勾选随
// topMostQuery（查询类，跨线程直调）。
func logoMenuItems(level string) []menuIt {
	permSub := make([]menuIt, 0, len(permLevels))
	for i, lv := range permLevels {
		permSub = append(permSub, menuIt{id: cmdPermBase + uintptr(i), label: lv, checked: lv == level})
	}
	return []menuIt{
		{id: cmdNew, label: "新对话"},
		{label: "权限", sub: permSub}, // D73：二级菜单四档，当前档打勾
		{id: cmdHistory, label: "消息历史"},
		{id: cmdSettings, label: "设置"},
		{id: cmdTopMost, label: "置顶", checked: topMostQuery()},
		{id: cmdToggle, label: "隐藏"},
		{}, // 分隔线
		{id: cmdExit, label: "退出"},
	}
}

// postLogoMenu 投递 logo 右键菜单请求到托盘线程（D72：呈现归 shell 线程的独立消息
// 泵，TrackPopupMenu 不嵌 Gio 泵）。shell 未就绪（启动微窗/headless）= 静默放弃。
func postLogoMenu() {
	if h := shellHWND.Load(); h != 0 {
		procPostMessageW.Call(h, logoMenuMsg, 0, 0)
	}
}

// postBubbleMenu 投递气泡右键菜单请求到托盘线程（D92：命中上下文已存 u.bubbleMenu
// 原子槽，Gio 线程 Store 先于本调用）。shell 未就绪（启动微窗/headless）= 静默放弃。
func postBubbleMenu() {
	if h := shellHWND.Load(); h != 0 {
		procPostMessageW.Call(h, bubbleMenuMsg, 0, 0)
	}
}

// showLogoMenu logo 右键菜单呈现（托盘线程，D72/D73）：项清单走共享件，owner = 托盘消息窗
// （与托盘菜单同款 TPM 收尾）。
func showLogoMenu() {
	h := shellHWND.Load()
	if h == 0 {
		return
	}
	if u := shellUI.Load(); u != nil {
		menuDispatch(u, runMenu(h, logoMenuItems(permLevelOf(u))))
	}
}

// permLevelOf 当前权限档（D73 子菜单勾选源）：Status 现取（shell 线程可跨 goroutine
// 读——lvl/agent 访问器均为原子，TUI 状态行同此回调）；未就绪 = ""（不勾，切换后自愈）。
func permLevelOf(u *UI) string {
	if u.opts.Status == nil {
		return ""
	}
	return u.opts.Status().Level
}

// bubbleMenuItems 气泡右键菜单（D92/D97/D98/D99/D100，项序/文案为契约测试锁定）：
// user/assistant 同集七项——编辑三方式（Fresh 新分叉 / Carry 边转移后续历史 / Clone
// 深拷贝后续历史）+ 重新生成/复制/引用/查看原文；thinking 定稿块与工具 chip = 复制/
// 查看原文两项（D100③）。
func bubbleMenuItems(kind blockKind) []menuIt {
	cp := menuIt{id: cmdBubbleCopy, label: "复制"}
	raw := menuIt{id: cmdBubbleRaw, label: "查看原文"}
	if kind == blockThinking || kind == blockTool {
		return []menuIt{cp, raw}
	}
	regen := menuIt{id: cmdBubbleRegen, label: "重新生成"}
	return []menuIt{
		{id: cmdBubbleEdit, label: "编辑"},
		{id: cmdBubbleEditKeep, label: "编辑并转移历史"},
		{id: cmdBubbleEditCopy, label: "编辑并复制历史"},
		regen, cp,
		{id: cmdBubbleQuote, label: "引用"},
		raw,
	}
}

// showBubbleMenu 气泡右键菜单呈现（托盘线程，D92）：上下文取 u.bubbleMenu 原子槽
// （Gio 线程 Store 先于 PostMessage），owner = 托盘消息窗（与 logo 菜单同款 TPM 收尾）。
func showBubbleMenu() {
	h := shellHWND.Load()
	if h == 0 {
		return
	}
	u := shellUI.Load()
	if u == nil {
		return
	}
	ctx := u.bubbleMenu.Load()
	if ctx == nil {
		return
	}
	menuDispatchBubble(u, ctx, runMenu(h, bubbleMenuItems(ctx.kind)))
}

// menuDispatchBubble 气泡菜单命令分发（D92/D97/D98；托盘线程调用——修改性调用一律经
// post，§15.6 铁律 1）：重新生成 = /regen（与键入同路径，内核分叉重发）；编辑三方式 =
// editMsg 进编辑态（预填/提交/Esc 生命周期在 Gio 侧，mode 随项而设）；复制 = copyMsg →
// 下一帧 gtx.Execute(clipboard.WriteCmd)（剪贴板写入必须在 Gio 帧）；引用 = quoteMsg →
// 输入框追加引用块（Gio 侧 apply）。
func menuDispatchBubble(u *UI, ctx *bubbleMenuCtx, r uintptr) {
	switch r {
	case cmdBubbleEdit:
		_ = u.post(editMsg{id: ctx.id, text: ctx.edit, mode: conversation.Fresh, part: ctx.part})
	case cmdBubbleEditKeep:
		_ = u.post(editMsg{id: ctx.id, text: ctx.edit, mode: conversation.Carry, part: ctx.part})
	case cmdBubbleEditCopy:
		_ = u.post(editMsg{id: ctx.id, text: ctx.edit, mode: conversation.Clone, part: ctx.part})
	case cmdBubbleRegen:
		_ = u.post(inputMsg{text: "/regen " + string(ctx.id)})
	case cmdBubbleCopy:
		_ = u.post(copyMsg{text: ctx.copy})
	case cmdBubbleQuote:
		_ = u.post(quoteMsg{text: ctx.copy})
	case cmdBubbleRaw:
		// D99/D100 查看原文：raw 内容（bubbleCtx 按块角色组装——正文原文/思考原文/
		// 工具 JSON）入原子槽 → 单实例次窗呈现；已开 = 聚焦 + 原地刷新（invalidateKind）。
		raw := ctx.raw
		u.rawView.Store(&raw)
		u.wins.openWin(winRaw)
		u.wins.invalidateKind(winRaw)
	}
}

// menuDispatch 命令分发（托盘/logo 菜单共用，D72；托盘线程调用——openWin 锁内单
// 实例、修改性调用经 onWindowThread/post，§15.6 铁律 1）。
func menuDispatch(u *UI, r uintptr) {
	// 权限子菜单（D73）：cmdPermBase+i → /permission permLevels[i]（与键入同路径——
	// D22 config 写回 + Agent 热切换，菜单只做呈现与勾选）。
	if r >= cmdPermBase && r < cmdPermBase+uintptr(len(permLevels)) {
		_ = u.post(inputMsg{text: "/permission " + permLevels[r-cmdPermBase]})
		return
	}
	switch r {
	case cmdNew:
		_ = u.post(inputMsg{text: "/new"}) // 新对话：与键入同路径（parseInput → inCh）
	case cmdToggle:
		u.toggleWindow()
	case cmdTopMost:
		u.toggleTopMost()
	case cmdSettings:
		u.wins.openWin(winSettings) // 单实例防重开（§15.7；注册表线程安全）
	case cmdHistory:
		u.wins.openWin(winHistory)
	case cmdWelcome:
		u.wins.openWin(winWelcome)
	case cmdExit:
		u.exitViaShell()
	}
}

// buildMenu 建单（D73：二级菜单父项 MF_POPUP 挂子句柄、递归建单；DestroyMenu 对父单
// 递归销毁子单）。仅建单不显示。
func buildMenu(items []menuIt) uintptr {
	menu, _, _ := procCreatePopupMenu.Call()
	for _, it := range items {
		if len(it.sub) > 0 {
			sub := buildMenu(it.sub)
			label, _ := syscall.UTF16PtrFromString(it.label)
			procAppendMenuW.Call(menu, mfPopup, sub, uintptr(unsafe.Pointer(label)))
			continue
		}
		if it.id == 0 {
			procAppendMenuW.Call(menu, mfSeparator, 0, 0)
			continue
		}
		lab, _ := syscall.UTF16PtrFromString(it.label)
		f := uintptr(mfString)
		if it.checked {
			f |= mfChecked // 勾选 = 当前置顶态 / 当前权限档
		}
		procAppendMenuW.Call(menu, f, it.id, uintptr(unsafe.Pointer(lab)))
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
func runMenu(hwnd uintptr, items []menuIt) uintptr {
	menu := buildMenu(items)
	var pt point
	procGetCursorPos.Call(uintptr(unsafe.Pointer(&pt)))
	procSetForegroundWindow.Call(hwnd)
	wasTop := topMostQuery()
	platformSetTopMost(false)
	r, _, _ := procTrackPopupMenu.Call(menu, tpmRightBtn|tpmRetCmd,
		uintptr(pt.x), uintptr(pt.y), 0, hwnd, 0)
	platformSetTopMost(wasTop)
	procPostMessageW.Call(hwnd, wmNull, 0, 0) // MSDN 要求：收尾防菜单不消失
	procDestroyMenu.Call(menu)
	return r
}
