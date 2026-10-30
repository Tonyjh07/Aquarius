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

import (
	"fmt"
	"image"
	"image/color"
	"io"
	"strings"

	"gioui.org/f32"
	"gioui.org/font"
	"gioui.org/io/clipboard"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"

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

// historyFrame winHistory 单帧：左栏 + 右图 + 状态栏（§15.7 常规窗，主题 Bg 铺底）。
func historyFrame(gtx layout.Context, th *material.Theme, u *UI, st *historyState) layout.Dimensions {
	st.sync(u)
	st.avatars.drain() // 角色卡头像后台解码结果入缓存（§5.17）
	st.drainNodeAction(u, gtx)
	st.drainFilePicks(u)
	// Esc 退出选择态：选择态不是模态层（不铺 scrim），所以不夺焦点，只挂一条不限焦点的
	// 过滤器；模态层在屏时 dispatch 见门即返回，Esc 归那一层自己。
	if !st.modalActive(u) {
		st.drainSelKeys(gtx)
	}
	st.dispatch(u, gtx)
	return layout.Stack{}.Layout(gtx,
		layout.Expanded(func(gtx layout.Context) layout.Dimensions {
			paint.FillShape(gtx.Ops, paperOf().win,
				clip.Rect(image.Rectangle{Max: gtx.Constraints.Max}).Op())
			return layout.Dimensions{}
		}),
		layout.Stacked(func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
				layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
					// 内容区（左栏 + 右图）与轻提示同层：轻提示贴**内容区**底缘，
					// 不让开状态栏会在顶层 Stack 里压住状态栏（顶层只知整窗高，取像实测）。
					return layout.Stack{}.Layout(gtx,
						layout.Expanded(func(gtx layout.Context) layout.Dimensions {
							return layout.Flex{Axis: layout.Horizontal}.Layout(gtx,
								layout.Rigid(func(gtx layout.Context) layout.Dimensions {
									// D159：左栏可收起——收起态只剩一条窄轨（展开钮），宽度让给关系图。
									// 两态都读**本帧**的 st.leftCollapsed：钮在同帧 dispatch 里翻转，
									// 故点一下当帧就换宽度，不等到下一帧。
									w, body := histLeftColumnDp, histLeftColumn
									if st.leftCollapsed {
										w, body = histLeftRailDp, histLeftRail
									}
									px := gtx.Dp(w)
									gtx.Constraints = layout.Constraints{Min: image.Pt(px, 0), Max: image.Pt(px, gtx.Constraints.Max.Y)}
									return body(gtx, th, u, st)
								}),
								// 左栏与右图之间一条 hairline：两栏各自整高填充同色时，没有这条线
								// 会长成一块——读者分不出「列表到此为止」。
								layout.Rigid(func(gtx layout.Context) layout.Dimensions {
									pp := paperOf()
									paint.FillShape(gtx.Ops, pp.hairline,
										clip.Rect(image.Rectangle{Max: image.Pt(gtx.Dp(1), gtx.Constraints.Max.Y)}).Op())
									return layout.Dimensions{Size: image.Pt(gtx.Dp(1), gtx.Constraints.Max.Y)}
								}),
								layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
									return histGraphArea(gtx, th, u, st)
								}),
							)
						}),
						// 轻提示（复制 ID / 复制会话 ID 反馈）：贴内容区底缘、短促、不拦截输入。
						layout.Stacked(func(gtx layout.Context) layout.Dimensions {
							return toastFrame(gtx, th, &st.toast, paperOf().tipBg)
						}),
					)
				}),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return histStatusBar(gtx, th, u, st)
				}),
			)
		}),
		// D139：遮罩层置顶（scrim + 居中卡片）。未开启时零尺寸，不影响布局。
		layout.Stacked(func(gtx layout.Context) layout.Dimensions {
			return st.overlayFrame(gtx, th, u)
		}),
		// D157：同步确认弹窗再置顶（模态；主窗确认态与它同源，点任一处都作答）。
		layout.Stacked(func(gtx layout.Context) layout.Dimensions {
			return st.confirmFrame(gtx, th, u)
		}),
		// D152：「插入摘要」小卡置顶（模态；由节点右键菜单开，两钮各投一条 /summary）。
		layout.Stacked(func(gtx layout.Context) layout.Dimensions {
			return st.summaryFrame(gtx, th, u)
		}),
		// D249：导入多会话归档的勾选面板置顶（模态；选完文件后由 drainFilePicks 开）。
		layout.Stacked(func(gtx layout.Context) layout.Dimensions {
			return st.importPickFrame(gtx, th, u)
		}),
	)
}

// histLeftColumnDp 左栏展开态宽度（§5.4）。
const histLeftColumnDp unit.Dp = 200

// histLeftRailDp 左栏收起态宽度（D159）：一条窄轨，只放展开钮——左栏存在的意义是选会话，
// 选完就收起把宽度让给关系图（长会话横向本来就不够看）。
const histLeftRailDp unit.Dp = 30

// histLeftColumn 左栏：会话列表（标题 / 时间 / 消息数；当前会话高亮）+ 标题行右端的收起钮。
//
// D249 批量选择态（st.selMode）下同一列换三处：每行**左侧**出复选框、行尾「✕」收起
// （删除这时走底部动作条，两个删除入口同时在场只会点错）、列表底部换成动作条。
func histLeftColumn(gtx layout.Context, th *material.Theme, u *UI, st *historyState) layout.Dimensions {
	rowH := gtx.Dp(44)
	return layout.UniformInset(unit.Dp(8)).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						l := material.Label(th, paperMicroSp(th), "会话")
						l.Font.Weight = font.Bold
						l.Color = paperOf().text3
						return l.Layout(gtx)
					}),
					layout.Flexed(1, layout.Spacer{}.Layout),
					// 会话进出的两个入口（§5.16 的 GUI 出口）：「导入」读外部文件成一个
					// 新会话、「导出」把**当前**会话写成一份归档。两者都只负责拿路径，选完投一条
					// 内核命令——搬运、防覆盖、回执全在命令面（铁律 5：壳内不复制内核逻辑）。
					layout.Rigid(histTextBtn(th, &st.btnImport, "导入")),
					layout.Rigid(layout.Spacer{Width: unit.Dp(8)}.Layout),
					layout.Rigid(histTextBtn(th, &st.btnExport, "导出")),
					layout.Rigid(layout.Spacer{Width: unit.Dp(8)}.Layout),
					// D160：标题行右端两颗钮——「＋」新建对话（左） + 「◀」收起左栏（右）。
					// 新建排在收起**左侧**：收起钮语义是「把这条栏收掉」，贴边最右更顺；
					// 且两颗钮都在这条标题行里，它们的位置语义与其它行操作一致。
					layout.Rigid(histPlusBtn(&st.btnNew)),
					// 两颗钮留 8dp 间隔：加号是细笔画、三角是实心块，贴在一起会读成
					// 一个「＋▶」连字（2026-10-19 取像 4x 复看确认需要分开）。
					layout.Rigid(layout.Spacer{Width: unit.Dp(8)}.Layout),
					layout.Rigid(histGlyphBtn(th, &st.btnLeft, "◀")),
				)
			}),
			layout.Rigid(layout.Spacer{Height: unit.Dp(6)}.Layout),
			layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
				return st.list.Layout(gtx, len(st.sessions), func(gtx layout.Context, i int) layout.Dimensions {
					c := st.rowClick(i)
					return layout.Stack{}.Layout(gtx,
						// 行底板（纯绘制，不取事件）：整行面，当前会话用选中色。
						layout.Expanded(func(gtx layout.Context) layout.Dimensions {
							s := st.sessions[i]
							pp := paperOf()
							fill := pp.win
							if s.Current {
								fill = pp.sel
							}
							paint.FillShape(gtx.Ops, fill,
								clip.UniformRRect(image.Rectangle{Max: gtx.Constraints.Max},
									gtx.Dp(unit.Dp(pp.rChip))).Op(gtx.Ops))
							return layout.Dimensions{Size: image.Pt(gtx.Constraints.Max.X, rowH)}
						}),
						// 行命中区（点行 = 切会话；悬浮行 = 显示「✕」）：上报**整行**盒，
						// 文字按内衬排布、右侧让出 ✕ 槽。
						layout.Stacked(func(gtx layout.Context) layout.Dimensions {
							return c.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
								// 选择态行尾没有「✕」（见下），就不必让出那个槽——否则列宽
								// 会平白少一截，标题更早被截断。
								right := unit.Dp(6)
								if !st.selMode {
									right += histRowDelSlotDp
								}
								layout.Inset{
									Top: unit.Dp(6), Bottom: unit.Dp(6),
									Left: unit.Dp(6), Right: right,
								}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
									if st.selMode {
										return selRowByIndex(gtx, th, st, i)
									}
									return st.histSessionRow(gtx, th, st.sessions[i])
								})
								return layout.Dimensions{Size: image.Pt(gtx.Constraints.Max.X, rowH)}
							})
						}),
						// 「✕」：与行点击件**同级兄弟**，排在它之后（命中链更靠前）。
						//
						// **不能**把它嵌进行点击件的 widget 里（2026-10-19 探针实测）：嵌
						// 进去的控件收不到任何指针事件——✕ 永远不悬浮、点了也没反应（逐点
						// 扫描：整行 hover 正常、✕ 恒 false）。同级后 ✕ 正常命中，且它会把
						// 同一位置的行的命中遮掉（点 ✕ 只投 /rmconv，不会顺带切会话）。
						//
						// 选择态**不画**它（D249）：删除走底部动作条，两个删除入口同时在场
						// 只会点错。
						layout.Stacked(func(gtx layout.Context) layout.Dimensions {
							if st.selMode {
								return layout.Dimensions{}
							}
							return st.rowDelSlot(gtx, th, c, i, rowH)
						}),
					)
				})
			}),
			// 底部：常态一颗「选择」（进批量选择态），选择态换成动作条（D249）。
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				if !st.selMode {
					return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
						layout.Rigid(histTextBtn(th, &st.btnSelect, "选择")),
						layout.Flexed(1, layout.Spacer{}.Layout),
					)
				}
				return layout.Stack{}.Layout(gtx,
					layout.Expanded(func(gtx layout.Context) layout.Dimensions {
						return selBarPaint(gtx)
					}),
					layout.Stacked(func(gtx layout.Context) layout.Dimensions {
						return layout.Inset{Top: unit.Dp(6), Bottom: unit.Dp(6), Left: unit.Dp(2), Right: unit.Dp(2)}.
							Layout(gtx, func(gtx layout.Context) layout.Dimensions {
								return st.selActionBar(gtx, th)
							})
					}),
				)
			}),
		)
	})
}

// histLeftRail 左栏收起态（D159）：整条铺 sunken 底色 + 顶部展开钮，钮外整条不取事件
// （收起态唯一的交互件就是那颗钮，点空白不该有反应）。u 不参与绘制，只为与 histLeftColumn
// 同签名（帧里按状态二选一）。
func histLeftRail(gtx layout.Context, th *material.Theme, _ *UI, st *historyState) layout.Dimensions {
	pp := paperOf()
	paint.FillShape(gtx.Ops, pp.sunken,
		clip.Rect(image.Rectangle{Max: gtx.Constraints.Max}).Op())
	return layout.Inset{Top: unit.Dp(12)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
					layout.Flexed(1, layout.Spacer{}.Layout),
					layout.Rigid(histGlyphBtn(th, &st.btnLeft, "▶")),
					layout.Flexed(1, layout.Spacer{}.Layout),
				)
			}),
		)
	})
}

// histChromeBtn 左栏标题行 / 收起窄轨上的小钮外壳：Clickable 自带 hover 反馈 + 左右 2dp 内衬。
// 内容由调用方给（文字字形或自绘图标）。
func histChromeBtn(c *widget.Clickable, content layout.Widget) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		return c.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Left: unit.Dp(2), Right: unit.Dp(2)}.Layout(gtx, content)
		})
	}
}

// histTextBtn 文字小钮（「导入」「导出」）：与字形象钮同款外壳，只是内容是一段字。
//
// 用文字不用图标：这两个动作在别处（命令行、文档）都以这两个词出现，图形符号得靠猜；
// 左栏标题行只有这一处能放它们，字号与旁边的「会话」标签同档。
func histTextBtn(th *material.Theme, c *widget.Clickable, label string) layout.Widget {
	return histChromeBtn(c, func(gtx layout.Context) layout.Dimensions {
		l := material.Caption(th, label)
		l.Color = paperOf().text2
		return l.Layout(gtx)
	})
}

// histGlyphBtn 文字字形小钮（◀ 收起 / ▶ 展开）。
func histGlyphBtn(th *material.Theme, c *widget.Clickable, glyph string) layout.Widget {
	return histChromeBtn(c, func(gtx layout.Context) layout.Dimensions {
		l := material.Caption(th, glyph)
		l.Color = paperOf().text2
		return l.Layout(gtx)
	})
}

// histPlusBtn 「＋」新建对话钮。
func histPlusBtn(c *widget.Clickable) layout.Widget {
	return histChromeBtn(c, histPlusGlyph)
}

// histPlusGlyph 自绘加号：全角「＋」是细十字，同字号下比 ◀/▶ 的实心三角淡一档（1x 几乎糊在
// 底上，加粗也没有粗体面可用）。两笔描边按 dp 定粗细与臂长，视觉重量与三角拉平。
func histPlusGlyph(gtx layout.Context) layout.Dimensions {
	d := gtx.Dp(unit.Dp(11))
	cx, cy := float32(d)/2, float32(d)/2
	a := float32(d) / 2
	var p clip.Path
	p.Begin(gtx.Ops)
	p.MoveTo(f32.Pt(cx-a, cy))
	p.LineTo(f32.Pt(cx+a, cy))
	p.MoveTo(f32.Pt(cx, cy-a))
	p.LineTo(f32.Pt(cx, cy+a))
	paint.FillShape(gtx.Ops, paperOf().text2,
		clip.Stroke{Path: p.End(), Width: float32(gtx.Dp(unit.Dp(2)))}.Op())
	return layout.Dimensions{Size: image.Pt(d, d)}
}

// histAvatarDp / histAvatarPx 会话行左槽头像口径：边长 30dp（行高 44dp 内衬后约 32dp），
// 解码 60px（2x 下采样）。
const (
	histAvatarDp unit.Dp = 30
	histAvatarPx         = 60
)

// histSessionRow 单行内容：左槽角色头像 + 标题 + （时间 · N 条）；当前会话带「●」。
func (st *historyState) histSessionRow(gtx layout.Context, th *material.Theme, s port.SessionSummary) layout.Dimensions {
	title := s.Title
	if title == "" {
		title = "新会话"
	}
	title = truncRunes(title, 18)
	mark := " "
	if s.Current {
		mark = "●"
	}
	meta := fmt.Sprintf("%s · %d 条", s.UpdatedAt.Format("01-02 15:04"), s.Messages)
	return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Right: unit.Dp(8)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return st.sessionAvatar(gtx, th, s)
			})
		}),
		layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					l := material.Label(th, paperNoteSp(th), mark+" "+title)
					l.Color = paperOf().text
					l.Font.Weight = font.Bold
					// D157：行尾让出「✕」槽后可用宽度变窄。标题已按 rune 截断，这里再钉一行
					// ——否则超长标题会折成两行把行高撑开，左栏行高参差。
					l.MaxLines = 1
					return l.Layout(gtx)
				}),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					l := material.Label(th, paperMicroSp(th), meta)
					l.Color = paperOf().text3
					l.MaxLines = 1
					return l.Layout(gtx)
				}),
			)
		}),
	)
}

// sessionAvatar 会话行左槽头像（§5.17）：角色卡真图（异步解码；未就绪/失败画兜底）；
// 非角色会话（无卡）画中性兜底方块。
func (st *historyState) sessionAvatar(gtx layout.Context, th *material.Theme, s port.SessionSummary) layout.Dimensions {
	side := gtx.Dp(histAvatarDp)
	if src := st.avatars.get(s.AvatarPath); src != nil {
		if op, ok := src.imageOp(); ok {
			drawThumb(gtx, image.Rect(0, 0, side, side), op)
			return layout.Dimensions{Size: image.Pt(side, side)}
		}
	}
	// 有卡无头像：显示角色首字；无卡：空名 → 纯色方块（charAvatarFallback 空名不画字）。
	return charAvatarFallback(gtx, th, s.Character, side)
}

// truncRunes 截断到最多 n 个 rune（超长标题防溢出）。
func truncRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

// histRowDelSlotDp 「✕」槽边长（D157）。行内容右侧**恒**让出这么多（`histLeftColumn` 的
// 内衬），故 ✕ 的显隐不引起行内文字重排；✕ 自己的盒也按这个边长定尺。
const histRowDelSlotDp unit.Dp = 20

// rowDelSlot 行尾「✕」槽（D157）：**仅指针在该行上时**绘制并登记点击件；未悬浮时连事件
// op 都不发（不显形、不占命中）。
//
// 判据取「行 ∪ ✕」两件（`row.Hovered() || del.Hovered()`）。两件都要看，是因为**指针一旦
// 停在 ✕ 上，行的 hover 就没了**（实测：✕ 的命中区在链上更靠前，会把同一位置的行的命中
// 遮掉——见 `histLeftColumn` 里兄弟结构的记录）；此时只有 `del.Hovered()` 能把 ✕ 撑住。
// 反过来，指针从右侧直接进入 ✕ 槽时，行的 hover 先成立一帧把 ✕ 登记出来，之后才轮到 ✕
// 自己接管——两帧之间不会闪空（进入/离开事件同一批投递，判据不会同时为假）。
//
// 悬浮态翻转时补一帧（wakeFrame）：保证「登记 ✕ / 撤下 ✕」与绘制落在同一帧，光标移入后
// 的第一次点击不会因为差一帧而落空。
//
// rowH 由调用方给（Stack 的 Stacked 子项拿到的 Max.Y 是整栏剩余高度，不是行高），用于
// 纵向居中；横向靠 `op.Offset` 贴行右缘（Stacked 子项 Min 被清 0，不能靠 Center/Right）。
func (st *historyState) rowDelSlot(gtx layout.Context, th *material.Theme, row *widget.Clickable, i, rowH int) layout.Dimensions {
	side := gtx.Dp(histRowDelSlotDp)
	del := st.rowDelClick(i)
	show := row.Hovered() || del.Hovered()
	if st.setDelShown(i, show) {
		wakeFrame(gtx)
	}
	if !show {
		return layout.Dimensions{}
	}
	margin := gtx.Dp(6) // 与行内容右侧内衬同宽：✕ 右缘和文字右缘对齐
	// 纵向：槽内居中后再上抬 2px——「✕」(U+2715) 的墨迹在行盒里偏下（CJK 字体的 ascent 大），
	// 直接居中看着往下掉；这点偏移是按默认字族调的（字体换了也仍是个位数像素的偏差）。
	y := (rowH-side)/2 - gtx.Dp(2)
	off := op.Offset(image.Pt(gtx.Constraints.Max.X-margin-side, y)).Push(gtx.Ops)
	defer off.Pop()
	gtx.Constraints = layout.Exact(image.Pt(side, side))
	return del.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		pp := paperOf()
		l := material.Label(th, paperNoteSp(th), "✕")
		l.Color = pp.text2
		if del.Hovered() {
			// 压在 ✕ 上：字形转**危险色**（在任何行面上都看得出来——下沉底与选中面几乎同色，
			// 只靠底色读不出来），并垫一层浅底下沉面，明确「这一下点的是删除」。
			l.Color = pp.danger
			paint.FillShape(gtx.Ops, pp.sunken,
				clip.UniformRRect(image.Rectangle{Max: gtx.Constraints.Max}, gtx.Dp(unit.Dp(pp.rCtl))).Op(gtx.Ops))
		}
		return layout.Center.Layout(gtx, l.Layout)
	})
}

// setDelShown 记下第 i 行「✕」的显隐，返回是否**翻转**（true = 需要补一帧重绘）。
func (st *historyState) setDelShown(i int, v bool) bool {
	for len(st.delShown) <= i {
		st.delShown = append(st.delShown, false)
	}
	if st.delShown[i] == v {
		return false
	}
	st.delShown[i] = v
	return true
}

// histGraphArea 右栏：关系图帧（第 7 步；数据未就绪时居中占位）。
func histGraphArea(gtx layout.Context, th *material.Theme, u *UI, st *historyState) layout.Dimensions {
	if st.graph != nil && st.model != nil {
		return st.graph.frame(gtx, th, u, st)
	}
	return layout.Center.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		l := material.Label(th, paperNoteSp(th), "关系图未就绪")
		l.Color = paperOf().text3
		return l.Layout(gtx)
	})
}

// histCtlItem 状态栏一个控件：click 非空 = 按钮，空 = 只读文字（缩放百分比）。
// 项集由 histStatusCtlItems 一处给出——测试锁定「有哪些控件 / 什么文案」，绘制侧只按序排
// （加减控件必须改这一处，不会出现「改了绘制、忘了改契约测试」）。
type histCtlItem struct {
	label string
	click *widget.Clickable
}

// histStatusCtlItems 状态栏控件项集（自左向右）。2026-10-19 删掉「展开全部」：会话在默认
// 预算内就已是全量，点了没有任何变化（用户批注「没什么效果，删了」）；预算仍由 −/＋ 调
// （上限 relHardCap）。
func (st *historyState) histStatusCtlItems() []histCtlItem {
	zoomPct := "100%"
	if st.graph != nil {
		zoomPct = fmt.Sprintf("%d%%", int(st.graph.zoom*100+0.5))
	}
	return []histCtlItem{
		{label: "−", click: &st.btnMinus},
		{label: "＋", click: &st.btnPlus},
		{label: "缩放 " + zoomPct}, // 只读文字
		{label: "装下整树", click: &st.btnHome},
		{label: "回到初始态", click: &st.btnSeed},
		{label: "聚焦当前历史", click: &st.btnLine},
		{label: "聚焦当前节点", click: &st.btnNode},
	}
}

// histStatusBar 状态栏（A7 落点）：会话标题 · 共 N · 呈现 M + 控件项集 + 图例。
func histStatusBar(gtx layout.Context, th *material.Theme, u *UI, st *historyState) layout.Dimensions {
	N, M := 0, 0
	if st.model != nil {
		N = st.model.total
		M = len(st.vis.nodes)
	}
	text := fmt.Sprintf("会话：%s · 共 %d 条 · 呈现 %d", st.title(), N, M)
	return layout.Stack{}.Layout(gtx,
		layout.Expanded(func(gtx layout.Context) layout.Dimensions {
			paint.FillShape(gtx.Ops, paperOf().sunken,
				clip.Rect(image.Rectangle{Max: gtx.Constraints.Max}).Op())
			return layout.Dimensions{}
		}),
		layout.Stacked(func(gtx layout.Context) layout.Dimensions {
			return layout.UniformInset(unit.Dp(6)).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						kids := []layout.FlexChild{
							layout.Rigid(func(gtx layout.Context) layout.Dimensions {
								l := material.Label(th, paperMicroSp(th), text)
								l.Color = paperOf().text2
								return l.Layout(gtx)
							}),
							layout.Flexed(1, layout.Spacer{}.Layout),
						}
						for _, it := range st.histStatusCtlItems() {
							it := it
							kids = append(kids, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
								return histCtl(gtx, th, it)
							}))
						}
						return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx, kids...)
					}),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return histLegend(gtx, th)
					}),
				)
			})
		}),
	)
}

// histCtl 状态栏一个小控件（click 空 = 只读文字）。悬停/按下靠 Clickable 自带的 ripple
// 投影到 Paper 的次级面上——状态栏底色已是 sunken，故只给文字色，不再叠第二层底。
// 每个控件左侧留 8dp 间距（否则「＋ 缩放 98%」会连成一句，见 D158）。
func histCtl(gtx layout.Context, th *material.Theme, it histCtlItem) layout.Dimensions {
	return layout.Inset{Left: unit.Dp(8)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		mk := func(gtx layout.Context) layout.Dimensions {
			l := material.Caption(th, it.label)
			l.Color = paperOf().text2
			return l.Layout(gtx)
		}
		if it.click == nil {
			return mk(gtx)
		}
		return it.click.Layout(gtx, mk)
	})
}

// histLegend 状态栏第二行：深度色带图例 + 形状图例（A2 的可解释性）。
func histLegend(gtx layout.Context, th *material.Theme) layout.Dimensions {
	swatch := func(c color.NRGBA) layout.Widget {
		return func(gtx layout.Context) layout.Dimensions {
			size := gtx.Dp(10)
			paint.FillShape(gtx.Ops, c, clip.RRect{Rect: image.Rectangle{Max: image.Pt(size, size)},
				SE: 2, SW: 2, NE: 2, NW: 2}.Op(gtx.Ops))
			return layout.Dimensions{Size: image.Pt(size, size)}
		}
	}
	label := func(s string) layout.Widget {
		return func(gtx layout.Context) layout.Dimensions {
			l := material.Label(th, paperMicroSp(th), s)
			l.Color = paperOf().text2
			return l.Layout(gtx)
		}
	}
	// 图例必须与画布同源：色块直接取 Paper 的深度色带，形状词与 relShapeOf 一一对应
	// （□ user / ● assistant / ◇ system / ◯ 结构根）。此前图例列 4 色而画布只现 3 档、
	// 结构根画成实心圆，图例与画面对不上——图例的用处就是「照着认」，对不上就是错的。
	// 2026-10-19 撤掉色带后面那个空心圆：它没有标签，读起来像第 5 档色带（用户批注
	// 「这里不知道哪来的圆」），而「◯ 会话根」在下面的形状组里已经写明。
	d := paperOf().depth
	return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
		layout.Rigid(label("色带")),
		layout.Rigid(swatch(d[0])),
		layout.Rigid(layout.Spacer{Width: unit.Dp(4)}.Layout),
		layout.Rigid(swatch(d[1])),
		layout.Rigid(layout.Spacer{Width: unit.Dp(4)}.Layout),
		layout.Rigid(swatch(d[2])),
		layout.Rigid(layout.Spacer{Width: unit.Dp(4)}.Layout),
		layout.Rigid(swatch(d[3])),
		layout.Rigid(layout.Spacer{Width: unit.Dp(12)}.Layout),
		layout.Rigid(label("形状：□ 你 · ● 助手 · ◇ 系统 · ◯ 会话根 · △ 注入")),
	)
}
