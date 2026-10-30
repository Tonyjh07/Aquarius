package uigui

// history.go 会话历史次窗（D112/S3，§15.7）：左栏会话选择 + 右栏关系图 + 底部状态栏。
//
// 数据面（D120§6.1）：左栏 = port.SessionLister（app 原子发布快照），右图 = port.TreeView
// .Graph()——均为不可变只读快照，历史窗 goroutine 无锁读（§15.5），**不做任何 store I/O**。
//
// 行为（D120§5.2）：点会话行 = 投 `/switch <id>`（经 u.inCh，与键入同路径串行执行）；
// 切会话后 app 在同款发布点重建快照 → 本窗按**签名**识别变更、重排并重锚（R3：
// 平移/缩放/悬停不重排，只有数据变更 / rebudget 才重排——数据变更 = 快照签名变化）。
//
// 落地序（§10.3）：第 6 步 = 左栏 + 状态栏 + 窗壳（右图占位）；第 7 步 = 右图自绘、
// 手势与右键菜单（relmap_frame.go，接入 st.graph）。

// 本文件只留窗的状态与行为（快照同步、行点击投递、键与指针分派）；
// 左栏见 history_left.go，帧骨架与右栏见 history_view.go，关系图本体见 relmap_*.go。

import (
	"fmt"
	"io"
	"strings"

	"gioui.org/io/clipboard"
	"gioui.org/layout"
	"gioui.org/widget"

	"github.com/Tonyjh07/Aquarius/internal/domain/conversation"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

// historyState winHistory 次窗自持帧状态（runSecondary 创建、仅该窗 goroutine 读写）。
type historyState struct {
	// list/rows 左栏滚动与点击件（行数变化时 get-or-create，按序缓存）。
	list     widget.List
	rows     []*widget.Clickable
	dels     []*widget.Clickable   // 行尾「✕」删除钮（D157；同上按行序缓存）
	delShown []bool                // 上帧「✕」显隐（悬浮态翻转时补一帧，见 rowDelSlot）
	sessions []port.SessionSummary // 上帧读到的左栏快照（行数对齐）
	// sig/model/vis 右图数据面：sig = 快照签名（变化才重建模型与预算，R3 懒重排）；
	// model = 派生模型（§6.2），vis = 当前预算切片（A7 的「呈现 M」）。
	sig    string
	model  *relModel
	vis    relVisible
	budget int // 预算（rebudget：−/＋，A5/A7）
	// graph 右图帧态（第 7 步：重排/手势/绘制）。
	graph *relGraph
	// ov 消息查看/编辑遮罩（D139，nil = 未开启；开启期间背景照常渲染但不消费事件）。
	ov *overlayState
	// 状态栏控件（− / ＋ / 装下整树 / 回到初始态 / 三个聚焦域：D128、D140）。
	btnMinus, btnPlus, btnHome widget.Clickable
	btnSeed, btnLine, btnNode  widget.Clickable
	// btnLeft 左栏收起/展开（D159）：展开时画在左栏标题行右端、收起时画在窄条顶部；
	// leftCollapsed 是它的帧态（本窗自持，不落盘——收起是临时视图选择，不是配置）。
	btnLeft       widget.Clickable
	leftCollapsed bool
	// btnNew 左栏标题行的「＋」新建对话（D160）：与键入 /new 同路径投命令，壳内不做任何
	// 建会话逻辑（铁律 5）——内核对完自会发布新快照，左栏与右图随签名单帧刷新。
	btnNew widget.Clickable
	// btnImport / btnExport 会话进出的两个入口：导入走单选文件框、导出走保存框，选完都
	// 只投一条内核命令（`/import <路径>`、`/export --path <路径>`），壳内不做搬运。
	// 导出作用对象是**当前会话**——历史窗左栏能点开别的会话来导出这件事内核不支持，
	// 摆一个会「先切会话再导出」的按钮等于替用户做主。
	btnImport widget.Clickable
	btnExport widget.Clickable
	// cfm 跨窗同步的二次确认弹窗（D157）：帧态无常驻开关——弹窗在屏与否由主窗发布的
	// 确认投影（u.confirmShare）决定，这里只存控件与滚动位。
	cfm confirmDialog
	// sum 「插入摘要」小卡（D152，nil = 未开启）：图侧菜单动作开卡，卡内两钮各投一条
	// /summary；同样在屏期间按 modalActive 遮住背景。
	sum *summaryDialog
	// pick 导入勾选面板（D249，nil = 未开启）：选完一个多会话归档后弹，勾哪几个导哪几个。
	// 在屏期间同样按 modalActive 遮住背景。
	pick *importPickDialog
	// selMode/sel 左栏批量选择态（D249）：selMode = 每行出复选框、底部换动作条；
	// sel 按**会话 ID**记勾选（不按行序——列表随内核快照重排后勾选不会错位）。
	selMode bool
	sel     map[conversation.ID]bool
	// selPending 批量导出按下「导出」时勾中的 ID（**按左栏顺序**的字符串）：保存框是异步的，
	// 落点回来时用户可能已经退出选择态（sel 已清空），不能那时再去读 sel。
	selPending []string
	// btnSel* 选择态的动作条控件（进入 / 全选 / 取消 / 导出 / 删除）。
	btnSelect, btnSelAll, btnSelCancel widget.Clickable
	btnSelExport, btnSelDel            widget.Clickable
	// toast 窗内轻提示（复制 ID / 复制会话 ID 等的即时反馈）：帧尾渲染，约 2 秒后自动消失。
	toast toastState
	// avatars 会话行左槽的角色卡头像缓存（§5.17）：本窗独占一份（见 avatar_cache.go——
	// 两窗分属不同 goroutine，不共享缓存实例）。inval 由 runSecondary 填（解码完成后催
	// 本窗重绘）；测试直建 st 时留空即可。
	avatars *avatarCache
	inval   func()
}

// newHistoryState 构造 winHistory 帧状态并做首帧数据同步。
func newHistoryState(u *UI) *historyState {
	st := &historyState{
		list:   widget.List{List: layout.List{Axis: layout.Vertical}},
		budget: relDefaultBudget,
		graph:  newRelGraph(),
	}
	// 头像缓存：回调**调用时**现取 st.inval（runSecondary 在构造后才填 w.Invalidate）。
	st.avatars = newAvatarCache(histAvatarPx, func() {
		if st.inval != nil {
			st.inval()
		}
	})
	// 问句区竖向可滚（layout.List 的零值轴是**横向**，不显式设会变成横滚 + 横向滚动条）。
	st.cfm.promptList.List.Axis = layout.Vertical
	st.sync(u)
	return st
}

// currentTitle 当前会话的标题（空串 = 没有当前会话行，或标题为空）。与 title() 分开：
// 那个是给人看的（空标题写「新会话」、无行写「（未就绪）」），这里是拿去派生文件名的。
func (st *historyState) currentTitle() string {
	for _, s := range st.sessions {
		if s.Current {
			return s.Title
		}
	}
	return ""
}

// title 当前会话标题（状态栏「会话：」与根节点陈述用）。
func (st *historyState) title() string {
	for _, s := range st.sessions {
		if s.Current {
			if s.Title == "" {
				return "新会话"
			}
			return s.Title
		}
	}
	return "（未就绪）"
}

// applyBudget rebudget：调整预算并重排（R3：数据/预算变更才重排；重排后仍收敛静止）。
func (st *historyState) applyBudget(delta int) {
	if st.model == nil {
		return
	}
	max := st.model.total
	if max > relHardCap {
		max = relHardCap
	}
	st.budget += delta
	if st.budget < 1 {
		st.budget = 1
	}
	if st.budget > max {
		st.budget = max
	}
	st.vis = relBudget(st.model, st.budget)
	if st.graph != nil {
		st.graph.rebuild(st.model, st.vis)
	}
}

// relSig 快照签名：Total + Anchor + 末节点（Nodes 按 (CreatedAt, ID) 升序，任何树变更
// 都会反映到节点集或末节点——签名相同 = 数据未变 = 不重排）。
func relSig(g port.TreeGraph) string {
	var last conversation.MessageID
	if len(g.Nodes) > 0 {
		last = g.Nodes[len(g.Nodes)-1].ID
	}
	return fmt.Sprintf("%d|%s|%s", g.Total, g.Anchor, last)
}

// sync 帧首同步数据面：左栏快照直接取（行数对齐）；右图签名变化才重建模型与预算。
func (st *historyState) sync(u *UI) {
	if u.opts.Tree != nil {
		if g, ok := u.opts.Tree.Graph(); ok {
			if sig := relSig(g); sig != st.sig {
				st.sig = sig
				if m, ok := relNewModel(g); ok {
					st.model = m
					st.vis = relBudget(m, st.budget)
					if st.graph != nil {
						st.graph.rebuild(m, st.vis)
					}
				}
			}
		}
	}
	if u.opts.Lister != nil {
		st.sessions = u.opts.Lister.List()
	}
}

// rowClick 左栏会话行点击件 get-or-create（按行序缓存，同 branchClick 口径）。
func (st *historyState) rowClick(i int) *widget.Clickable {
	for len(st.rows) <= i {
		st.rows = append(st.rows, new(widget.Clickable))
	}
	return st.rows[i]
}

// sendSwitch 点会话行 → 投 /switch <id>（D120§5.2 resolve；经 u.inCh 与键入同路径，
// 壳内不旁路内核）。缓冲满时丢弃（历史窗无转写区，静默——与右图「失败不空转」同向）。
func (st *historyState) sendSwitch(u *UI, id conversation.ID) {
	select {
	case u.inCh <- port.UserInput{Command: &port.Command{Name: "switch", Args: []string{string(id)}}}:
	default:
	}
}

// sendNew 点左栏标题行「＋」→ 投 /new（无参 = 默认标题，D160）：经 u.inCh 与键入同路径，
// 壳内不建会话（铁律 5）。缓冲满丢弃（同 sendSwitch）。
func (st *historyState) sendNew(u *UI) {
	select {
	case u.inCh <- port.UserInput{Command: &port.Command{Name: "new"}}:
	default:
	}
}

// rowDelClick 行尾「✕」点击件 get-or-create（同上按行序缓存）。
func (st *historyState) rowDelClick(i int) *widget.Clickable {
	for len(st.dels) <= i {
		st.dels = append(st.dels, new(widget.Clickable))
	}
	return st.dels[i]
}

// sendRmconv 点行尾「✕」→ 投 /rmconv <完整 ID>（D157）：删除整会话（消息树一并删除、
// 删当前会话自动继任）——**复用内核既有命令**，与键入同路径，壳内不做任何删除逻辑。
// 二次确认由内核侧发出、两窗同步呈现（本窗弹窗见 confirmFrame）。缓冲满丢弃（同 sendSwitch）。
func (st *historyState) sendRmconv(u *UI, id conversation.ID) {
	if id == "" {
		return
	}
	select {
	case u.inCh <- port.UserInput{Command: &port.Command{Name: "rmconv", Args: []string{string(id)}}}:
	default:
	}
}

// dispatch 帧内点击消费：会话行切换 + 行尾「✕」删除 + 左栏收起 + 状态栏控件（预算 −/＋/
// 装下整树/回到初始态/两个聚焦域）。
//
// **遮罩或确认弹窗开启期一律不取事件**（D139/D157）：纵深防御——两者的整窗输入区已按
// Gio 命中链把背景指针事件一并遮蔽（overlay.go 头注），此处再显式关一道：将来若输入区不是
// 整窗、或背景控件被排到弹窗之上，模态语义仍在。左栏行与状态栏钮因此在模态期间完全失灵。
func (st *historyState) dispatch(u *UI, gtx layout.Context) {
	if st.modalActive(u) {
		return
	}
	// 左栏收起/展开（D159）：收起态只剩窄条，那颗钮仍要能点开（它是收起态唯一的交互件）。
	if st.btnLeft.Clicked(gtx) {
		st.leftCollapsed = !st.leftCollapsed
		return
	}
	// 左栏标题行「＋」新建对话（D160）：投 /new，与键入同路径。
	if st.btnNew.Clicked(gtx) {
		st.sendNew(u)
		return
	}
	// 会话进出（§5.16）：先要路径，结果回来后才投命令（那是下一帧的事）。
	if st.btnImport.Clicked(gtx) {
		st.requestImport(u)
		return
	}
	if st.btnExport.Clicked(gtx) {
		st.requestExport(u)
		return
	}
	// 左栏底部「选择」钮：进批量选择态（D249）。
	if st.btnSelect.Clicked(gtx) {
		st.enterSelMode()
		return
	}
	// 批量选择态（D249）：行点击在这里表达「挑这一行」，**不**是切会话——继续往下走会
	// 顺带把当前会话切走，用户以为只是选了一个备份对象。标题行四颗钮仍然照常工作
	// （新建 / 导入 / 导出当前会话 / 收起都与「选了哪几个」无关，且它们都画在屏上，
	// 画出来却点不动比多一次确认更糟）。
	if st.selMode {
		st.dispatchSel(u, gtx)
		st.dispatchView(gtx)
		return
	}
	// D157：行尾「✕」**先收**。实测点 ✕ 时只投 /rmconv（✕ 的命中区把同位置的行遮掉），
	// 但顺序照旧钉死在前：不依赖「谁遮了谁」这种命中链细节——将来排版挪动、或 Gio 的
	// 遮挡口径变化，也不会一次点击既删会话又切会话。
	//
	// 两个循环都只走**当前快照的行数**：点击件是按行序缓存的，会话删过之后尾部的旧件
	// 已经不在屏上（收不到事件），不该再有机会参与判读。
	for i := 0; i < len(st.sessions) && i < len(st.dels); i++ {
		if st.dels[i].Clicked(gtx) {
			st.sendRmconv(u, st.sessions[i].ID)
			return
		}
	}
	for i := 0; i < len(st.sessions) && i < len(st.rows); i++ {
		if st.rows[i].Clicked(gtx) {
			st.sendSwitch(u, st.sessions[i].ID)
			return
		}
	}
	st.dispatchView(gtx)
}

// dispatchView 状态栏取景控件（预算 −/＋、装下整树、回到初始态、两个聚焦域）。
//
// 与列表动作分开：选择态只改列表的语义，取景控件照常可用（画在屏上的钮没有理由失效）。
func (st *historyState) dispatchView(gtx layout.Context) {
	switch {
	case st.btnMinus.Clicked(gtx):
		st.applyBudget(-10)
	case st.btnPlus.Clicked(gtx):
		st.applyBudget(+10)
	case st.btnHome.Clicked(gtx): // 装下整树（D140：原「回到当前」——只对右基准位，树装不下时什么都改不了）
		if st.graph != nil {
			st.graph.viewPending = vaFitAll
		}
	case st.btnSeed.Clicked(gtx): // 回到初始态：重排 + 视口容纳整树
		if st.graph != nil {
			st.graph.relayoutFromSeed()
		}
	case st.btnLine.Clicked(gtx): // 聚焦当前历史：视口容纳 root → Head 的历史线
		if st.graph != nil {
			st.graph.viewPending = vaFocusLine
		}
	case st.btnNode.Clicked(gtx): // 聚焦当前节点：视口容纳 Head 及其父子一跳
		if st.graph != nil {
			st.graph.viewPending = vaFocusNode
		}
	}
}

// drainNodeAction 排空图侧菜单动作槽（D132）：菜单分发在 shell 线程，而**剪贴板写入必须
// 在 Gio 帧内**、聚焦待办属图状态——故两者都经 u.nodeAction 原子槽投到本窗帧首执行。
// D139 再加两项：开遮罩（编辑三项/查看原文改道）与「转到」（/goto + 唤出主窗）。
func (st *historyState) drainNodeAction(u *UI, gtx layout.Context) {
	act := u.nodeAction.Swap(nil)
	if act == nil || st.graph == nil {
		return
	}
	switch act.kind {
	case nodeActCopyID:
		if id := string(act.id); id != "" {
			gtx.Execute(clipboard.WriteCmd{Type: "application/text",
				Data: io.NopCloser(strings.NewReader(id))})
			st.toast.show("已复制节点 ID", gtx.Now)
		}
	case nodeActGoto:
		st.sendGoto(u, act.id)
		_ = u.post(showExpandMsg{}) // /goto 只改主窗转写区：主窗收在球里则等于没反应
	case nodeActOpenOverlay:
		st.openOverlay(u, act.id, act.mode, act.page)
	case nodeActSummary:
		st.openSummary(u, act.id)
	}
}

// sendGoto 图侧「转到」（D139；2026-10-19 由「在主窗定位」缩短）：投 /goto <id>（与键入同路径，缓冲满丢弃）。
func (st *historyState) sendGoto(u *UI, id conversation.MessageID) {
	if id == "" {
		return
	}
	select {
	case u.inCh <- port.UserInput{Command: &port.Command{Name: "goto", Args: []string{string(id)}}}:
	default:
	}
}
