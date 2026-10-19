package uigui

// 菜单内容与命令分发（中性，D111/S4b 口径：菜单**项集**与**命令语义**是 UI 关注点、
// 平台无关；HMENU 构建与 TrackPopupMenu 呈现归平台实现，见 platform 包）。项序与文案
// 是契约，测试锁定。

import (
	"github.com/Tonyjh07/Aquarius/internal/adapter/uigui/platform"
	"github.com/Tonyjh07/Aquarius/internal/domain/conversation"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

// 菜单命令 ID（托盘/logo/气泡三处共用一张表；平台实现只回传被选中的 ID）。
const (
	cmdToggle   = 101
	cmdExit     = 102
	cmdTopMost  = 103
	cmdHistory  = 104 // 功能窗入口（§15.7/D60：会话历史占位壳）
	cmdSettings = 106 // 功能窗入口（§15.7/D60：设置窗核心档）
	cmdNew      = 107 // 新对话（logo 右键菜单，D72：注入 /new 与键入同路径）
	cmdPermBase = 108 // 权限子菜单四档基值（D73：+i 对齐 settings.go permLevels 序）
	// 注：105 曾为 cmdWelcome（欢迎窗入口）。D141 起欢迎窗**入口移入设置窗**，两个菜单都
	// 不带此项，该 ID 及分发分支删除（不留死码）。

	cmdBubbleEdit     = 120 // 气泡右键：编辑（D92/D97 → editMsg Fresh 进编辑态）
	cmdBubbleRegen    = 121 // 气泡右键：重新生成（D92 → /regen 与键入同路径）
	cmdBubbleCopy     = 122 // 气泡右键：复制（D92 → copyMsg，下一帧写剪贴板）
	cmdBubbleEditKeep = 123 // 气泡右键：编辑并转移历史（D97 → editMsg Carry）
	cmdBubbleEditCopy = 124 // 气泡右键：编辑并复制历史（D97 → editMsg Clone）
	cmdBubbleQuote    = 125 // 气泡右键：引用（D98 → quoteMsg 追加引用前缀进输入框）
	cmdBubbleRaw      = 126 // 气泡右键：查看原文（D99 → rawView 原子槽 + winRaw 次窗）

	// 图侧（消息管理面）节点右键项（D132）：与气泡项同一张表、同一呈现（D132 决策：
	// 图菜单并入气泡菜单，尾部加这几项；不再自绘 Gio 浮层）。
	cmdNodeCopyID  = 130 // 复制节点 ID（图侧动作槽 → 历史窗帧内落剪贴板）
	cmdNodeRm      = 131 // 删除该节点及子树（/rm <id>，与键入同路径经 inCh）
	cmdNodeGoto    = 132 // 转到（/goto <id> + 唤出主窗；D139：原为单击动作）
	cmdNodeSummary = 133 // 插入摘要（D152：弹本窗小卡，卡片两钮分别投 /summary <id> [文本]）
	cmdNodeRespond = 134 // 生成响应（/respond <id> + 唤出主窗：从所选节点继续生成）
)

// nodeActKind 图侧动作槽的动作种类（D132：菜单分发在 shell 线程，而剪贴板写入必须在 Gio
// 帧内、聚焦待办属图状态，故一律经原子槽投历史窗帧执行；D139 追加开遮罩与「转到」）。
type nodeActKind uint8

const (
	nodeActCopyID      nodeActKind = iota + 1 // 复制 ID
	nodeActGoto                               // 转到（D139：/goto 由单击移来）
	nodeActOpenOverlay                        // 开消息查看/编辑遮罩（D139）
	nodeActSummary                            // 开「插入摘要」小卡（D152）
)

// nodeAction 图侧动作（D132）：id = 目标节点；槽在 UI 上（跨窗共享的原子槽，同 rawView
// 口径），由历史窗帧首排空。D139：开遮罩只带定位信息（id/方式/页签），**正文在帧内现取**
// —— 大文本不过原子槽，避免跨线程搬大对象。
type nodeAction struct {
	kind nodeActKind
	id   conversation.MessageID
	mode conversation.KeepMode // 开遮罩的预选方式（Fresh 零值）
	page overlayPage           // 开遮罩的目标页签
}

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
func logoMenuItems(level string, topMost bool) []menuIt {
	permSub := make([]menuIt, 0, len(permLevels))
	for i, lv := range permLevels {
		permSub = append(permSub, menuIt{id: cmdPermBase + uintptr(i), label: lv, checked: lv == level})
	}
	return []menuIt{
		{id: cmdNew, label: "新对话"},
		{label: "权限", sub: permSub}, // D73：二级菜单四档，当前档打勾
		{id: cmdHistory, label: "消息历史"},
		{id: cmdSettings, label: "设置"},
		{id: cmdTopMost, label: "置顶", checked: topMost},
		{id: cmdToggle, label: "隐藏"},
		{}, // 分隔线
		{id: cmdExit, label: "退出"},
	}
}

// trayMenuItems 托盘右键菜单项集（§15.1：显示/隐藏、置顶开关、功能窗入口 + 退出）。
// D141：**不带欢迎窗入口**——欢迎 / 首次运行引导从设置窗开（项集为契约、测试锁定）。
func trayMenuItems(topMost bool) []menuIt {
	return []menuIt{
		{id: cmdToggle, label: "显示 / 隐藏输入窗"},
		{id: cmdTopMost, label: "窗口置顶", checked: topMost},
		{},
		{id: cmdSettings, label: "设置"},
		{id: cmdHistory, label: "会话历史"},
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

// nodeMenuItems 图（消息管理面）节点右键菜单（D132）：**主界面气泡菜单那一套** +
// 「生成响应」 + 分隔线 + 图侧四项（复制 ID / 插入摘要 / 删除该节点及子树 / 转到；D139
// 加「转到」——`/goto` 由单击移来，原叫「在主窗定位」；D152 加「插入摘要」；2026-10-19
// 用户批注：删除项改写成后果（不再露出 `/rm` 命令名）、「在主窗定位」缩短为「转到」、
// 撤掉与状态栏重复的「装下整树」；2026-10-25 用户批注：加「生成响应」，放在气泡项组末尾、
// 分隔线之前）。同一呈现、同一分发，只有尾部不同。
// hasText=false（节点无正文可取：工具结果节点、或 NodeText 未就绪）时不给编辑类项——
// 拿空文本预填编辑框等于诱导用户提交空修订（内核会拒，但菜单不该诱导）；根/系统节点
// 不可编辑，只给复制/查看原文。
// canSummary=false（会话根与会话首节点人格）时不给「插入摘要」：根前无处可插；人格之前
// 也不该出现摘要——摘要会顶到路径第二位、被装配当成 persona（水位语义随之失效）。
// 注：会话根整份菜单在 openNodeMenu 处已被拦（2026-10-25 用户批注），本函数不再对根设门。
func nodeMenuItems(kind blockKind, hasText, canSummary bool) []menuIt {
	var head []menuIt
	switch {
	case !hasText:
		head = nil
	case kind == blockUser || kind == blockAssistant:
		head = bubbleMenuItems(kind) // 与主界面气泡同集七项
	default:
		head = []menuIt{{id: cmdBubbleCopy, label: "复制"}, {id: cmdBubbleRaw, label: "查看原文"}}
	}
	// 「生成响应」贴在**气泡项组末尾、分隔线之前**（2026-10-25 用户批注）：它是「对
	// 这条消息做什么」，与编辑类同组更顺；且**任何非根节点都给**（含人格/系统/工具
	// 节点）——从任意节点续写都合法（根节点整份菜单已去掉，故这里不再单独设门）。
	items := append(head, menuIt{id: cmdNodeRespond, label: "生成响应"})
	items = append(items, menuIt{}) // 分隔线
	items = append(items, menuIt{id: cmdNodeCopyID, label: "复制 ID"})
	if canSummary {
		items = append(items, menuIt{id: cmdNodeSummary, label: "插入摘要"})
	}
	return append(items,
		menuIt{id: cmdNodeRm, label: "删除该节点及子树"},
		menuIt{id: cmdNodeGoto, label: "转到"},
	)
}

// permLevelOf 当前权限档（D73 子菜单勾选源）：Status 现取（可跨 goroutine 读——
// lvl/agent 访问器均为原子，TUI 状态行同此回调）；未就绪 = ""（不勾，切换后自愈）。
func permLevelOf(u *UI) string {
	if u.opts.Status == nil {
		return ""
	}
	return u.opts.Status().Level
}

// menuItems 按出处现组项集并转平台 DTO（平台呈现前回调，见 platHost.MenuItems）。
func (u *UI) menuItems(kind platform.MenuKind) []platform.MenuItem {
	switch kind {
	case platform.MenuTray:
		return toMenuItems(trayMenuItems(u.plat.TopMost()))
	case platform.MenuLogo:
		return toMenuItems(logoMenuItems(permLevelOf(u), u.plat.TopMost()))
	case platform.MenuBubble:
		ctx := u.bubbleMenu.Load()
		if ctx == nil {
			return nil
		}
		if ctx.graph { // D132：图侧节点菜单 = 气泡项集 + 图侧四项
			return toMenuItems(nodeMenuItems(ctx.kind, ctx.copy != "", ctx.canSummary))
		}
		return toMenuItems(bubbleMenuItems(ctx.kind))
	}
	return nil
}

// toMenuItems 中性项集 → 平台 DTO（递归含二级菜单）。
func toMenuItems(items []menuIt) []platform.MenuItem {
	out := make([]platform.MenuItem, 0, len(items))
	for _, it := range items {
		out = append(out, platform.MenuItem{
			ID:      it.id,
			Label:   it.label,
			Checked: it.checked,
			Sub:     toMenuItems(it.sub),
		})
	}
	return out
}

// menuChosen 菜单命令分发（平台回传被选中的 ID；托盘/logo 走 menuDispatch，
// 气泡走 menuDispatchBubble——后者需气泡上下文原子槽）。
func (u *UI) menuChosen(kind platform.MenuKind, id uintptr) {
	if kind == platform.MenuBubble {
		if ctx := u.bubbleMenu.Load(); ctx != nil {
			menuDispatchBubble(u, ctx, id)
		}
		return
	}
	menuDispatch(u, id)
}

// menuDispatchBubble 气泡菜单命令分发（D92/D97/D98；可能来自托盘线程——修改性调用一律经
// post，§15.6 铁律 1）：重新生成 = /regen（与键入同路径，内核分叉重发）；编辑三方式 =
// editMsg 进编辑态（预填/提交/Esc 生命周期在 Gio 侧，mode 随项而设）；复制 = copyMsg →
// 下一帧 gtx.Execute(clipboard.WriteCmd)（剪贴板写入必须在 Gio 帧）；引用 = quoteMsg →
// 输入框追加引用块（Gio 侧 apply）。
func menuDispatchBubble(u *UI, ctx *bubbleMenuCtx, r uintptr) {
	// D132/D139：图侧触发（消息管理面在次窗）时，动作一律落到**本窗**遮罩，不再投主窗
	// 输入栏/另开次窗——编辑三项 = 开遮罩并预选方式、「查看原文」= 开遮罩并定位源码页签。
	// 气泡侧右键时主窗必在前景，编辑进输入栏、查看原文进 winRaw 的老口径**不变**。
	if ctx.graph {
		switch r {
		case cmdBubbleEdit:
			u.openOverlayAction(ctx, conversation.Fresh, overlayPageRendered)
			return
		case cmdBubbleEditKeep:
			u.openOverlayAction(ctx, conversation.Carry, overlayPageRendered)
			return
		case cmdBubbleEditCopy:
			u.openOverlayAction(ctx, conversation.Clone, overlayPageRendered)
			return
		case cmdBubbleRaw:
			u.openOverlayAction(ctx, conversation.Fresh, overlayPageSource)
			return
		case cmdNodeGoto:
			u.nodeAction.Store(&nodeAction{kind: nodeActGoto, id: ctx.id})
			return
		case cmdBubbleQuote, cmdBubbleRegen, cmdNodeRespond:
			// 这三项仍落在主窗输入栏/内核（引用追加、重生成、从节点续写），故图侧触发时
			// 先唤出主窗，否则用户什么也看不见（D132 口径，D139 保留）。
			_ = u.post(showExpandMsg{})
		}
	}
	switch r {
	case cmdBubbleEdit:
		_ = u.post(editMsg{id: ctx.id, text: ctx.edit, mode: conversation.Fresh, part: ctx.part})
	case cmdBubbleEditKeep:
		_ = u.post(editMsg{id: ctx.id, text: ctx.edit, mode: conversation.Carry, part: ctx.part})
	case cmdBubbleEditCopy:
		_ = u.post(editMsg{id: ctx.id, text: ctx.edit, mode: conversation.Clone, part: ctx.part})
	case cmdBubbleRegen:
		_ = u.post(inputMsg{text: "/regen " + string(ctx.id)})
	case cmdNodeRespond:
		// 图侧：从所选节点继续生成（与键入同路径，清屏回放后跑一轮）。
		_ = u.post(inputMsg{text: "/respond " + string(ctx.id)})
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
	case cmdNodeCopyID:
		// D132 图侧：复制 ID。剪贴板写入必须在 Gio 帧内（shell 线程不可直达），故经图侧
		// 动作槽投**历史窗帧**执行——那是本次交互所属、且确定在产帧的窗（不必依赖主窗
		// 的帧调度：主窗可能收在球里）。
		u.nodeAction.Store(&nodeAction{kind: nodeActCopyID, id: ctx.id})
	case cmdNodeRm:
		// D132 图侧：删除节点 = /rm <id>，与键入同路径经 inCh（缓冲满则丢弃，与图内既有
		// 口径一致：历史窗无转写区可回显，失败不空转）。
		select {
		case u.inCh <- port.UserInput{Command: &port.Command{Name: "rm", Args: []string{string(ctx.id)}}}:
		default:
		}
	case cmdNodeSummary:
		// D152 图侧：插入摘要 = 本窗开小卡（正文在卡里写；两钮分别投 /summary）。
		u.nodeAction.Store(&nodeAction{kind: nodeActSummary, id: ctx.id})
	}
}

// openOverlayAction 图侧请求开遮罩（D139）：只带 id/方式/页签过原子槽，正文由历史窗
// 帧内经 port.TreeView.NodeText 现取（大文本不过槽）。
func (u *UI) openOverlayAction(ctx *bubbleMenuCtx, mode conversation.KeepMode, page overlayPage) {
	u.nodeAction.Store(&nodeAction{kind: nodeActOpenOverlay, id: ctx.id, mode: mode, page: page})
}

// menuDispatch 命令分发（托盘/logo 菜单共用，D72；可能来自托盘线程——openWin 锁内单
// 实例、修改性调用经平台层或 post，§15.6 铁律 1）。
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
	case cmdExit:
		u.exitViaShell()
	}
}
