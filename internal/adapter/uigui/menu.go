package uigui

// 菜单内容（中性，D111/S4b 口径：菜单**项集**是 UI 关注点、平台无关；HMENU 构建与
// TrackPopupMenu 呈现归平台实现，见 menu_windows.go）。项序与文案是契约，测试锁定。

// 菜单命令 ID（托盘/logo/气泡三处共用一张表；平台实现只回传被选中的 ID）。
const (
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

// menuIt 菜单项（托盘/logo/气泡右键菜单共用，D72/D73）；id 0 = 分隔线，或二级菜单父项
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

// trayMenuItems 托盘右键菜单项集（§15.1：显示/隐藏、置顶开关、功能窗入口 + 退出）。
func trayMenuItems() []menuIt {
	return []menuIt{
		{id: cmdToggle, label: "显示 / 隐藏输入窗"},
		{id: cmdTopMost, label: "窗口置顶", checked: topMostQuery()},
		{},
		{id: cmdSettings, label: "设置"},
		{id: cmdHistory, label: "会话历史"},
		{id: cmdWelcome, label: "欢迎 / 首次运行引导"},
		{},
		{id: cmdExit, label: "退出"},
	}
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

// permLevelOf 当前权限档（D73 子菜单勾选源）：Status 现取（可跨 goroutine 读——
// lvl/agent 访问器均为原子，TUI 状态行同此回调）；未就绪 = ""（不勾，切换后自愈）。
func permLevelOf(u *UI) string {
	if u.opts.Status == nil {
		return ""
	}
	return u.opts.Status().Level
}
