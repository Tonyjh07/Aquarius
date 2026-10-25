package uigui

// overlay.go 会话历史窗内的消息查看/编辑遮罩（D139，§15.7）：关系图**单击节点**在
// 本窗内开一层自绘遮罩（半透暗色 scrim + 居中卡片），看/改该条消息——
//   - `呈现` 页签 = 主窗同款 markdown 渲染的**只读**预览（复用 mdBlockWidgetTh，
//     传 nil 行选挂接件即无选区态）；
//   - `源码` 页签 = 原始 markdown **多行可编辑**编辑器，提交给内核的就是它；
//   - **应用方式在界面上三选一**（另起新分支 Fresh / 转移历史 --keep / 复制历史
//     --copy，默认 Fresh）→ 提交 = 结构化 `Command{Name:"edit", Args:[id,
//     ("--keep"|"--copy")?, 文本]}` 经 u.inCh（与键入同路径，--part 缺省合段）。
//
// **隔离口径（D139 的要点，实测修正）**：Gio 把一次点击投给**所有命中**的 handler（不是
// 「顶层命中即止」），所以「画一层 Clickable 挡住底下」这条路**不存在**。真正起作用的是
// 两条叠加：
//  1. **整窗输入区**（本文件 overlayFrame 里 `clip.Rect(整窗)` + `event.Op(&ov.tag)`）：
//     Gio 的命中是**链式栈遍历**，排在链顶的整窗输入区会连带遮蔽背景——实测左栏会话行、
//     状态栏按钮、右图手势在遮罩开启时都收不到指针事件（变异验证：去掉它 + 去掉背景门，
//     左栏立刻恢复响应）。
//  2. **背景显式不消费**（`historyState.dispatch` 与 `relGraph.update` 见门即返回）：
//     纵深防御——若遮罩输入区不再是整窗、或背景控件被排到遮罩之上，隔离仍成立。
//
// **解析缓存**：u.mdCache 是主窗事件循环独占的裸 map，次窗不得调用 u.mdBlocks；
// 本窗解析走 mdBlocksCached + overlayState 自己的缓存（切页签/改源码时算一次）。

import (
	"image"
	"image/color"
	"strings"

	"gioui.org/io/event"
	"gioui.org/io/key"
	"gioui.org/io/pointer"
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

// overlayPage 遮罩页签（D139/D154）。
type overlayPage uint8

const (
	overlayPageRendered overlayPage = iota // 呈现：markdown 渲染只读预览
	overlayPageSource                      // 源码：原始 markdown 多行编辑
	overlayPageTemplate                    // 默认模板：人格编辑区（仅 persona 节点）
)

// 遮罩几何（dp；D139）：卡片居中，宽 320~560、高 ≤ 视口 70%、四周留边 32
// （留边与卡片外的视口部分即「点空白关闭」区）。
//
// D154 人格模式另有一套上限（640dp / 视口 85%）：模板页签要放三列模块网格 +
// 组合预览，560dp 会被压成单列、预览被挤出可视区。上限只在人格节点生效。
const (
	overlayCardMaxWdp   unit.Dp = 560
	overlayCardMaxHFac          = 0.7
	overlayCardMinWdp   unit.Dp = 320
	overlayCardMargindp unit.Dp = 32
	overlayCardPadDp    unit.Dp = 12
	overlayScrimAlpha   uint8   = 0x99 // 半透暗色（约 60%）

	// 人格模式（模板页签）的放宽上限。
	overlayPersonaMaxWdp  unit.Dp = 640
	overlayPersonaMaxHFac         = 0.85
)

// overlayState 遮罩自持帧状态（historyState 持有，**仅历史窗 goroutine 读写**，
// §15.7 并发模型）。零值不可用——由 openOverlay 构造。
type overlayState struct {
	// tag 指针事件过滤器的 Tag（Gio 要求地址稳定）。
	tag struct{}

	id       conversation.MessageID
	kind     blockKind         // user/assistant/system/root → 可编辑性
	role     conversation.Role // 快照内角色（标题措辞；未知 = ""）
	editable bool              // root 与无正文节点只读；system 里只有 persona 可编辑
	persona  bool              // 会话首节点（parent == Root 的 system，D154）
	// hasChildren 该节点是否已有后续内容（开卡时取一次）：三档应用方式在「无后续」时
	// 结果完全相同，说明行据此换一种说法，免得用户以为三档有区别。
	hasChildren bool
	name        string // 节点名（persona = "人格节点" 之类，标题行用）
	// source/trigger 注入节点的来源与触发记录（D166；仅 role == inject 有意义）：
	// 来源行与转写头部共用 port.InjectSourceLabel 一张表。
	source  string
	trigger bool
	page    overlayPage           // 当前页签
	mode    conversation.KeepMode // 应用方式（Fresh 零值 = 默认；persona 锁 Carry）

	src       string    // 源码快照（NodeText，开遮罩时取一次）
	blocks    []mdBlock // 呈现页签的解析结果
	blocksKey string    // blocks 对应的源码（缓存键；变化即重解析）
	cache     map[string][]mdBlock

	// 页签 / 方式 / 动作控件。
	tabRendered, tabSource, tabTemplate widget.Clickable
	modeFresh, modeCarry, modeClone     widget.Clickable
	btnSave, btnClose                   widget.Clickable
	srcList, shownList                  widget.List
	ed                                  widget.Editor

	// 模板页签（D154）：人格编辑区现状 + 页脚两钮 + 反馈行。编辑区只在打开遮罩且
	// 是 persona 时构造（首次切页签或开卡即建），关遮罩随遮罩一起丢弃。
	prompt     *promptEditor
	btnSaveTpl widget.Clickable // 保存模板（写回 config 的两键）
	btnApply   widget.Clickable // 应用到本会话（/edit --keep）
	tplList    widget.List
	tplStatus  string
	tplErr     bool

	card        image.Rectangle // 本帧卡片盒（窗口系；点空白关闭按它判内外）
	focusNeeded bool            // 源码页签要把键盘焦点交给编辑器
	focusSink   bool            // 呈现页签要把键盘焦点交给遮罩自身（Esc 落点）
}

// openOverlay 开遮罩（D139）：正文经 port.TreeView.NodeText 取**全文**（摘要不能用来
// 编辑——会静默截断消息，D132）；取不到正文（工具结果节点/快照未发布）→ 只读空卡。
// mode/page = 菜单项预选（右键「编辑三项」/「查看原文」）；页签落在源码时预填编辑器。
func (st *historyState) openOverlay(u *UI, id conversation.MessageID, mode conversation.KeepMode, page overlayPage) {
	if id == "" || u.opts.Tree == nil {
		return
	}
	st.closeSummary() // 两层模态不共存：开遮罩即收摘要小卡（两步都从同一动作槽来）
	src, _ := u.opts.Tree.NodeText(id)
	kind := st.nodeKindOf(id)
	role, _ := st.nodeRoleOf(id)
	persona := st.isPersona(id)
	source, trigger := st.injectInfoOf(id)
	// persona 在 relNodeKind 里落 blockPlain（system），但它可以改首节点快照（D154）。
	editable := src != "" && (kind == blockUser || kind == blockAssistant || persona)
	if persona {
		// D154：改人格必须保留后续历史——Carry 是边转移，新人格接管整棵对话；
		// Fresh 会把 Head 挪到新人格上、整段历史变成旧分支（用户看到的是
		// 「改一句人格，对话没了」）。故预选并锁定 Carry，不看菜单预选了哪一档。
		mode = conversation.Carry
	} else if page == overlayPageTemplate {
		page = overlayPageRendered // 模板页签只属于 persona（防调用方误传）
	}
	ov := &overlayState{
		id:          id,
		kind:        kind,
		role:        role,
		persona:     persona,
		editable:    editable,
		hasChildren: st.hasChildren(id),
		mode:        mode,
		page:        page,
		src:         src,
		source:      source,
		trigger:     trigger,
		cache:       map[string][]mdBlock{},
	}
	if persona && ov.page == overlayPageTemplate {
		ov.ensureTemplate(u)
	}
	ov.srcList.List.Axis = layout.Vertical
	ov.shownList.List.Axis = layout.Vertical
	ov.tplList.List.Axis = layout.Vertical
	// 源码编辑器：多行保真（SetText 不压平），Enter 提交 / Shift+Enter 换行
	//（Submit=true 时 Enter 出 SubmitEvent 且不落换行，同主窗展开胶囊 D106）。
	ov.ed.SingleLine = false
	ov.ed.Submit = true
	ov.ed.ReadOnly = !ov.editable
	ov.ed.SetText(src)
	switch page {
	case overlayPageSource:
		ov.focusNeeded = true
	case overlayPageTemplate:
		// 模板页没有会话编辑器可获焦：Esc 落在遮罩自身。
		ov.focusSink = true
	default:
		// 呈现页没有编辑器可获焦，Esc 必须有落点（Gio 只投键事件给获焦 handler）。
		ov.focusSink = true
	}
	st.ov = ov
}

// isPersona 是否会话首节点（persona）：system 角色且父节点是结构根（D154）。
// 摘要等其余 system 节点不可编辑。
func (st *historyState) isPersona(id conversation.MessageID) bool {
	if st.model == nil {
		return false
	}
	for _, n := range st.model.nodes {
		if n.id != id {
			continue
		}
		if n.role != conversation.RoleSystem || n.parent < 0 || n.parent >= len(st.model.nodes) {
			return false
		}
		return st.model.nodes[n.parent].role == conversation.RoleRoot
	}
	return false
}

// hasChildren 节点是否已有后续内容（版本图上是否有以它为父的节点）。修订出来的新版本
// 挂在原节点的父下，不算它的孩子——故这个判断就是「改这一条时会不会动到后面的历史」。
func (st *historyState) hasChildren(id conversation.MessageID) bool {
	if st.model == nil {
		return false
	}
	self := -1
	for i, n := range st.model.nodes {
		if n.id == id {
			self = i
			break
		}
	}
	if self < 0 {
		return false
	}
	for _, n := range st.model.nodes {
		if n.parent == self {
			return true
		}
	}
	return false
}

// nodeKindOf 快照内节点的展示档位（与图的 relNodeKind 同口径；找不到 = plain）。
func (st *historyState) nodeKindOf(id conversation.MessageID) blockKind {
	if st.model == nil {
		return blockPlain
	}
	for _, n := range st.model.nodes {
		if n.id == id {
			return relNodeKind(n.role)
		}
	}
	return blockPlain
}

// nodeRoleOf 快照内节点的 Role（找不到 = ok false；与 nodeKindOf 同一次遍历口径）。
func (st *historyState) nodeRoleOf(id conversation.MessageID) (conversation.Role, bool) {
	if st.model == nil {
		return "", false
	}
	for _, n := range st.model.nodes {
		if n.id == id {
			return n.role, true
		}
	}
	return "", false
}

// injectInfoOf 注入节点的来源与触发记录（D166）：遮罩来源行用；非注入节点或快照
// 未就绪 = 零值。与 nodeRoleOf 同一次遍历口径（模型里 Source/Trigger 只在 inject 上有值）。
func (st *historyState) injectInfoOf(id conversation.MessageID) (source string, trigger bool) {
	if st.model == nil {
		return "", false
	}
	for _, n := range st.model.nodes {
		if n.id == id {
			return n.source, n.trigger
		}
	}
	return "", false
}

// closeOverlay 关遮罩（Esc / 点空白 / 关闭钮 / 提交后）。
func (st *historyState) closeOverlay() { st.ov = nil }

// overlayActive 遮罩是否开启（背景交互见门即返回，§15.7 + D139）。
func (st *historyState) overlayActive() bool { return st.ov != nil }

// confirmActive 二次确认弹窗是否在屏（D157）：主窗发布的确认投影非空 = 本窗正在显示
// 模态确认卡片（弹窗状态不在本窗，而在主窗的快照里——所以关窗重开也照旧呈现）。
func (st *historyState) confirmActive(u *UI) bool { return u.confirmShare.Load() != nil }

// modalActive 任一模态层在屏（遮罩 / 确认弹窗 / 插入摘要小卡）：背景（左栏 / 状态栏 /
// 右图手势）一律不取指针事件。
func (st *historyState) modalActive(u *UI) bool {
	return st.overlayActive() || st.confirmActive(u) || st.sum != nil
}

// events 遮罩键鼠事件（D139）：Esc 关闭；卡片盒外的抬起 = 点空白关闭。
//
// **两处 Gio 口径**（缺任一则本层收不到事件）：
//  1. 指针：需 `event.Op` 在**整窗 clip** 内声明路由锚点（与右图同款），否则抬起根本
//     不会命中本 tag；这条整窗输入区同时就是背景遮蔽器（见文件头「隔离口径」）。
//  2. 键：Gio 只把键事件投给**当前获焦**的 handler。呈现页没有编辑器可获焦，故本层
//     自带焦点落点（`key.FocusFilter{Target: &ov.tag}` + 开遮罩时一次 FocusCmd），
//     Esc 双挂：编辑器焦点（源码页）与遮罩焦点（呈现页）各一。
//
// **无需 armed 守卫**：`gtx.Event` 是**排他消费**——开遮罩的那次抬起已被右图的过滤器
// 循环抽干，遮罩层收不到（变异验证：加守卫反而吞掉开遮罩后的第一次点击）。另：抬起只投给
// 「已进入窗口」的命中集，故真实点击是 Move → Press → Release 序列。
func (ov *overlayState) events(gtx layout.Context, st *historyState) {
	for {
		ev, ok := gtx.Event(key.Filter{Focus: &ov.tag, Name: key.NameEscape})
		if !ok {
			break
		}
		if ke, isKey := ev.(key.Event); isKey && ke.State == key.Press {
			st.closeOverlay()
			return
		}
	}
	for {
		ev, ok := gtx.Event(key.Filter{Focus: &ov.ed, Name: key.NameEscape})
		if !ok {
			break
		}
		if ke, isKey := ev.(key.Event); isKey && ke.State == key.Press {
			st.closeOverlay()
			return
		}
	}
	for {
		ev, ok := gtx.Event(pointer.Filter{Target: &ov.tag, Kinds: pointer.Release})
		if !ok {
			break
		}
		e := ev.(pointer.Event)
		p := image.Pt(int(e.Position.X), int(e.Position.Y))
		if !p.In(ov.card) {
			st.closeOverlay()
			return
		}
	}
}

// actions 页签切换 / 方式选择 / 关闭 / 保存（帧内消费；只读节点无方式与保存）。
// 人格节点多一档页签「默认模板」，且只有那一档可保存模板 / 应用到本会话（D154）。
func (ov *overlayState) actions(gtx layout.Context, st *historyState, u *UI) {
	if ov.tabRendered.Clicked(gtx) {
		ov.page = overlayPageRendered
		ov.focusNeeded, ov.focusSink = false, true
		wakeFrame(gtx)
	}
	if ov.tabSource.Clicked(gtx) {
		ov.page = overlayPageSource
		ov.focusNeeded, ov.focusSink = true, false
		wakeFrame(gtx)
	}
	if ov.persona && ov.tabTemplate.Clicked(gtx) {
		ov.page = overlayPageTemplate
		ov.focusNeeded, ov.focusSink = false, true
		ov.ensureTemplate(u)
		wakeFrame(gtx)
	}
	if ov.editable && ov.page != overlayPageTemplate {
		switch {
		case ov.modeFresh.Clicked(gtx) && !ov.persona:
			ov.mode = conversation.Fresh
		case ov.modeCarry.Clicked(gtx):
			ov.mode = conversation.Carry
		case ov.modeClone.Clicked(gtx) && !ov.persona:
			ov.mode = conversation.Clone
		}
	}
	if ov.btnClose.Clicked(gtx) {
		st.closeOverlay()
		return
	}
	if ov.page == overlayPageTemplate {
		if ov.prompt != nil {
			// 编辑弹窗开着时整块编辑区是模态的：页脚钮不再响应（否则会在旧内容上保存）。
			if ov.prompt.modEdit.open {
				return
			}
			ov.prompt.refEvents(gtx)
			ov.prompt.moduleGridEvents(gtx)
			ov.prompt.preview(u)
			if ov.btnSaveTpl.Clicked(gtx) {
				ov.saveTemplate(u)
			}
			if ov.btnApply.Clicked(gtx) {
				ov.applyTemplate(u, st)
			}
		}
		return
	}
	if ov.canSave() && ov.btnSave.Clicked(gtx) {
		ov.submit(u, st)
	}
}

// ensureTemplate 构造模板页签的编辑区（D154）：现取 config 快照，只在人格遮罩里存在。
// 已构造就保留现状——切页签不该丢掉刚改的模板（关遮罩才是放弃）。
func (ov *overlayState) ensureTemplate(u *UI) {
	if ov.prompt != nil || !ov.persona {
		return
	}
	ov.prompt = newPromptEditor(u)
	if u.opts.PreviewPrompts != nil {
		ov.prompt.preview(u)
	}
}

// canSave 保存可用性（D139 ⑤）：只读节点、空/纯空白草稿 = 不可用（按钮呈禁用态，点击
// 亦为 no-op——内核也会拒）。逐帧读编辑器文本，代价可忽略。
func (ov *overlayState) canSave() bool {
	return ov.editable && strings.TrimSpace(ov.ed.Text()) != ""
}

// submit 提交修订（D139）：结构化 `/edit`（flag 紧跟 id，D97① 位置口径；--part 缺省
// = 合段，沿用 D132 图侧口径）→ 关遮罩 + 唤出主窗展开（user + Fresh 会重新生成，
// D93；沿用 D132 的 showExpandMsg 口径，不唤出则生成过程看不见）。
func (ov *overlayState) submit(u *UI, st *historyState) {
	if !ov.canSave() {
		return // 只读节点 / 空草稿：无提交可言（内核亦拒）
	}
	text := ov.ed.Text()
	args := []string{string(ov.id)}
	switch ov.mode {
	case conversation.Carry:
		args = append(args, "--keep")
	case conversation.Clone:
		args = append(args, "--copy")
	}
	args = append(args, text)
	select {
	case u.inCh <- port.UserInput{Command: &port.Command{Name: "edit", Args: args}}:
	default:
		// 输入通道满：命令没投出去 ⇒ **不关遮罩**（否则用户改的文本凭空消失，且无迹可寻）。
		// 与图侧「缓冲满则丢弃」的既有口径同源，这里改成保数据。
		return
	}
	st.closeOverlay()
	_ = u.post(showExpandMsg{})
}

// shown 呈现页签的解析块（按源码缓存，变化才重解析——不逐帧跑 goldmark）。
func (ov *overlayState) shown() []mdBlock {
	if ov.blocksKey != ov.src {
		ov.blocks = mdBlocksCached(ov.src, &ov.cache)
		ov.blocksKey = ov.src
	}
	return ov.blocks
}

// wakeFrame 追加一帧重绘请求（Gio 动画口径，比 Window.Invalidate 轻；§15.5）。
func wakeFrame(gtx layout.Context) { gtx.Execute(op.InvalidateCmd{}) }

// overlayFrame 遮罩单帧（D139）：scrim 铺满视口 + 居中卡片。无遮罩时零尺寸空操作。
func (st *historyState) overlayFrame(gtx layout.Context, th *material.Theme, u *UI) layout.Dimensions {
	ov := st.ov
	if ov == nil {
		return layout.Dimensions{}
	}
	// 整窗命中区 + 路由锚点：抬起/悬停事件落到本 tag（点空白关闭的判定源）。
	// **这一处就是隔离的主机制**：Gio 的命中按链式栈遍历，排在链顶的整窗输入区会连带
	// 遮蔽背景（左栏 / 右图 / 状态栏实测都收不到指针事件）。背景侧的显式不取是纵深防御。
	stack := clip.Rect(image.Rectangle{Max: gtx.Constraints.Max}).Push(gtx.Ops)
	defer stack.Pop()
	gtx.Event(key.FocusFilter{Target: &ov.tag}) // 呈现页的键盘落点可获焦
	event.Op(gtx.Ops, &ov.tag)
	ov.events(gtx, st)
	if st.ov == nil { // 事件里关了：本帧不再画
		return layout.Dimensions{}
	}
	ov.actions(gtx, st, u)
	if st.ov == nil {
		return layout.Dimensions{}
	}
	card := overlayCardBoxFor(gtx, ov.persona)
	ov.card = card
	// scrim：铺满视口的半透暗色（背景仍在其下渲染 → 观感是「压暗一层」）。
	paint.FillShape(gtx.Ops, color.NRGBA{A: overlayScrimAlpha},
		clip.Rect(image.Rectangle{Max: gtx.Constraints.Max}).Op())
	// 遮罩卡片是浮层：阴影必须先于卡片铺（自小而大），否则会被卡片自身盖掉。
	cardRad := gtx.Dp(unit.Dp(paperOf().rCard))
	paperShadow(gtx, card, cardRad, 14, 8, 0x38)
	// 卡片：**显式定尺定位**到卡片盒。不能走 `layout.Center`——它只在 `Constraints.Min`
	// 内居中，而 `layout.Stacked` 会把子项的 Min 清成 0（stack.go），于是「居中」退化成
	// 画在窗口左上角：底板与内容贴左上，`ov.card`（命中盒/点空白判定）却仍在正中，二者
	// 错位（2026-10-15 实测：卡片自报尺寸 = 卡片盒，但 center 偏移 = 0）。
	// Min 也一并定成卡片盒：卡内内容按盒宽排布（长消息在盒内换行，不撑窗）。
	gtx.Constraints.Min, gtx.Constraints.Max = card.Size(), card.Size()
	trans := op.Offset(card.Min).Push(gtx.Ops)
	dims := overlayCard(gtx, th, ov, st, u)
	trans.Pop()
	dims.Size = card.Size() // 上报卡片盒（本层是 Stacked 子项，尺寸只影响自身记录）
	return dims
}

// overlayCardBox 卡片盒（居中、限宽限高：长消息在卡内滚，不撑窗）。
func overlayCardBox(gtx layout.Context) image.Rectangle {
	return overlayCardBoxFor(gtx, false)
}

// overlayCardBoxFor 按节点类型取卡片盒（D154）：人格模式上限放宽到 640dp / 视口 85%，
// 其余节点沿用 560dp / 70%。
func overlayCardBoxFor(gtx layout.Context, persona bool) image.Rectangle {
	margin := gtx.Dp(overlayCardMargindp)
	avail := gtx.Constraints.Max
	maxW := avail.X - 2*margin
	limW, limHf := overlayCardMaxWdp, float64(overlayCardMaxHFac)
	if persona {
		limW, limHf = overlayPersonaMaxWdp, overlayPersonaMaxHFac
	}
	if w := gtx.Dp(limW); maxW > w {
		maxW = w
	}
	if maxW < gtx.Dp(overlayCardMinWdp) {
		maxW = avail.X // 视口本身太窄：铺满不留边
	}
	maxH := int(float64(avail.Y) * limHf)
	if lim := avail.Y - 2*margin; maxH > lim {
		maxH = lim
	}
	if maxH < 1 {
		maxH = avail.Y
	}
	card := image.Rect(0, 0, maxW, maxH)
	return card.Add(image.Pt((avail.X-card.Dx())/2, (avail.Y-card.Dy())/2))
}

// overlayCard 卡片内容：标题行 + 页签行 + 内容区（呈现/源码）+ 底行（方式三选一 + 关闭/保存）。
func overlayCard(gtx layout.Context, th *material.Theme, ov *overlayState, st *historyState, u *UI) layout.Dimensions {
	pp := paperOf()
	paperPanel(gtx, image.Rectangle{Max: gtx.Constraints.Max},
		gtx.Dp(unit.Dp(pp.rCard)), gtx.Dp(1), pp.card, pp.hairline)
	return layout.UniformInset(overlayCardPadDp).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		children := []layout.FlexChild{
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return overlayTitle(gtx, th, ov)
			}),
			layout.Rigid(layout.Spacer{Height: unit.Dp(6)}.Layout),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return overlayTabs(gtx, th, ov)
			}),
		}
		// 注入节点（D166）：页签下方一行来源 + 触发状态——两个页签都看得到「这条从哪来」。
		if ov.role == conversation.RoleInject {
			children = append(children,
				layout.Rigid(layout.Spacer{Height: unit.Dp(4)}.Layout),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return overlayInjectSource(gtx, th, ov)
				}))
		}
		children = append(children,
			layout.Rigid(layout.Spacer{Height: unit.Dp(6)}.Layout),
			layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
				return overlayBody(gtx, th, ov, st, u)
			}),
			layout.Rigid(layout.Spacer{Height: unit.Dp(8)}.Layout),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return overlayFooter(gtx, th, ov)
			}),
		)
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx, children...)
	})
}

// overlayInjectSource 注入节点的来源行（D166）：「来源：<友好化来源> · 已触发响应/未触发」。
// 与主窗转写头部共用 port.InjectSourceLabel，措辞恒一致。
func overlayInjectSource(gtx layout.Context, th *material.Theme, ov *overlayState) layout.Dimensions {
	l := material.Caption(th, "来源："+port.InjectSourceLabel(ov.source)+" · "+injectState(ov.trigger))
	l.Color = paperOf().text2
	return l.Layout(gtx)
}

// overlayTitle 标题行：角色词 +「消息」+ 节点 ID 前缀（ID 与「原文 · <id>」同款口径）。
func overlayTitle(gtx layout.Context, th *material.Theme, ov *overlayState) layout.Dimensions {
	l := material.Label(th, paperBodySp(th), ov.titleText())
	l.Color = paperOf().text
	return l.Layout(gtx)
}

// titleText 标题文案（2026-10-15 用户拍板的口径）：角色词取**快照 Role**（不是 blockKind——
// root 与 system 都落 blockPlain，会把根节点写成「系统」）；根节点是会话容器、不是一条消息，
// 故只写「会话根 · <id>」，与图例「◯会话根」同词。Role 未知（快照未就绪）= 系统消息。
func (ov *overlayState) titleText() string {
	if ov.persona {
		return "人格节点 · " + shortID(string(ov.id))
	}
	switch ov.role {
	case conversation.RoleUser:
		return "用户消息 · " + shortID(string(ov.id))
	case conversation.RoleAssistant:
		return "助手消息 · " + shortID(string(ov.id))
	case conversation.RoleRoot:
		return "会话根 · " + shortID(string(ov.id))
	case conversation.RoleInject: // D166：注入上下文（只读；来源行在页签下方）
		return "注入上下文 · " + shortID(string(ov.id))
	default:
		return "系统消息 · " + shortID(string(ov.id))
	}
}

// overlayTabs 页签行：呈现 / 源码（人格节点多一档「默认模板」，D154）。
func overlayTabs(gtx layout.Context, th *material.Theme, ov *overlayState) layout.Dimensions {
	children := []layout.FlexChild{
		layout.Rigid(overlayPillBtn(&ov.tabRendered, th, "呈现", ov.page == overlayPageRendered)),
		layout.Rigid(layout.Spacer{Width: unit.Dp(6)}.Layout),
		layout.Rigid(overlayPillBtn(&ov.tabSource, th, "源码", ov.page == overlayPageSource)),
	}
	if ov.persona {
		children = append(children,
			layout.Rigid(layout.Spacer{Width: unit.Dp(6)}.Layout),
			layout.Rigid(overlayPillBtn(&ov.tabTemplate, th, "默认模板", ov.page == overlayPageTemplate)))
	}
	return layout.Flex{}.Layout(gtx, children...)
}

// overlayPill 卡内胶囊/按钮的**底板**（先量后画的正确姿势）：内容先量尺寸，底板按**内容盒**
// 画在内容之下。
//
// **不能**在 Inset 内用 `gtx.Constraints.Max` 当底板宽度（2026-10-15 用户截图报障）：
// 内衬先推了偏移，底板于是**从内衬之后起笔**、再被 `widget.Clickable` 的剪切
// （`clip.Rect{Max: dims.Size}`，见 gio widget/button.go）在右侧切平 ⇒ 看上去「文字贴着
// 左边、右边多出一块、右角是直角」。实测（1.25 缩放，未选中页签）：左内衬 2px / 右 14px，
// 修好后两侧相等。
func overlayPill(gtx layout.Context, bg color.NRGBA, radius unit.Dp, content layout.Widget) layout.Dimensions {
	return layout.Background{}.Layout(gtx,
		func(gtx layout.Context) layout.Dimensions {
			sz := gtx.Constraints.Min // Background 已把 Min 设成内容尺寸
			paint.FillShape(gtx.Ops, bg, clip.UniformRRect(image.Rectangle{Max: sz}, gtx.Dp(radius)).Op(gtx.Ops))
			return layout.Dimensions{Size: sz}
		},
		content,
	)
}

// overlayPillColors 胶囊配色：选中 = 深一档强调底 + 白字（AA 达标）；未选中 = 下沉面 + 次级字。
func overlayPillColors(th *material.Theme, on bool) (bg, fg color.NRGBA) {
	pp := paperOf()
	if on {
		return pp.accent, pp.accentFg
	}
	return pp.sunken, pp.text2
}

// overlayPillBtn 卡内胶囊钮（页签行与底行「应用方式三选一」共用一套控件语言：
// 选中反色、未选中次要底；2026-10-15 用户拍板统一）。
func overlayPillBtn(c *widget.Clickable, th *material.Theme, text string, on bool) layout.Widget {
	bg, fg := overlayPillColors(th, on)
	return func(gtx layout.Context) layout.Dimensions {
		return c.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			return overlayPill(gtx, bg, overlayPillRadiusDp, func(gtx layout.Context) layout.Dimensions {
				return layout.Inset{Top: unit.Dp(4), Bottom: unit.Dp(4), Left: unit.Dp(10), Right: unit.Dp(10)}.
					Layout(gtx, func(gtx layout.Context) layout.Dimensions {
						l := material.Caption(th, text)
						l.Color = fg
						return l.Layout(gtx)
					})
			})
		})
	}
}

// overlayBody 内容区：呈现页签 = 主窗同款 markdown 渲染只读预览；源码页签 = 多行编辑器；
// 模板页签 = 人格编辑区（与设置窗「系统提示词」页同一份实现，D154）。
func overlayBody(gtx layout.Context, th *material.Theme, ov *overlayState, st *historyState, u *UI) layout.Dimensions {
	if ov.page == overlayPageTemplate {
		return overlayTemplateBody(gtx, th, ov)
	}
	if ov.page == overlayPageSource {
		if ov.focusNeeded {
			ov.focusNeeded = false
			gtx.Execute(key.FocusCmd{Tag: &ov.ed})
		}
		for {
			evt, ok := ov.ed.Update(gtx)
			if !ok {
				break
			}
			if _, isSubmit := evt.(widget.SubmitEvent); isSubmit && ov.canSave() {
				ov.submit(u, st)
				return layout.Dimensions{}
			}
		}
		return ov.srcList.Layout(gtx, 1, func(gtx layout.Context, _ int) layout.Dimensions {
			return material.Editor(th, &ov.ed, "").Layout(gtx)
		})
	}
	if ov.focusSink {
		ov.focusSink = false
		gtx.Execute(key.FocusCmd{Tag: &ov.tag})
	}
	blocks := ov.shown()
	return ov.shownList.Layout(gtx, len(blocks), func(gtx layout.Context, i int) layout.Dimensions {
		w := mdBlockWidgetTh(th, blocks[i], nil, 0, image.Point{})
		return layout.Inset{Bottom: unit.Dp(4)}.Layout(gtx, w)
	})
}

// overlayFooter 底行：应用方式三选一（只读节点无）+ 关闭 / 保存。方式钮 = 与页签同款胶囊。
// 方式钮上方一行说明**当前这一档的实际后果**：三档各不相同（旧分支留不留、后续挂到谁
// 下面），说明随选中的方式即时切换，用户不必凭「转移/复制」两个词去猜。
// 人格节点（D154）：方式锁 Carry（另两档禁用 + 一行说明），模板页签换成两枚模板钮。
func overlayFooter(gtx layout.Context, th *material.Theme, ov *overlayState) layout.Dimensions {
	row := func(gtx layout.Context) layout.Dimensions { return overlayFooterRow(gtx, th, ov) }
	hint := ov.modeHint()
	if hint == "" {
		return row(gtx)
	}
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			l := material.Caption(th, hint)
			l.Color = paperOf().text3
			l.MaxLines = 2
			return layout.Inset{Bottom: unit.Dp(6)}.Layout(gtx, l.Layout)
		}),
		layout.Rigid(row),
	)
}

// modeHint 当前应用方式的后果说明（增删改遮罩的「会发生什么」）：只读节点没有方式可选、
// 模板页签谈的是人格模板而非这一段消息，都返回空串。
func (ov *overlayState) modeHint() string {
	switch {
	case ov.persona && ov.page == overlayPageTemplate:
		return ""
	case ov.persona:
		return "人格固定「转移历史」：旧人格留成无子叶，现有对话整体转到新人格下。"
	case !ov.editable:
		return ""
	case !ov.hasChildren:
		// 没有后续内容时三档等价：都是造一个同级新版本、旧节点留在原分支。
		return "该节点之后还没有内容：三种方式结果相同，都是留下旧节点、把视图移到新版本。"
	}
	switch ov.mode {
	case conversation.Carry:
		return "转移历史：旧节点留成无子叶，后续内容改挂到新版本下。"
	case conversation.Clone:
		return "复制历史：旧分支原样保留，新版本下另挂一份后续副本。"
	default:
		if ov.role == conversation.RoleUser {
			return "另起新分支：旧分支原样保留，在新版本下重新生成回答。"
		}
		return "另起新分支：旧节点与其后续留在原分支，视图移到新版本。"
	}
}

// overlayFooterRow 底行按钮本身（说明行由 overlayFooter 叠在它上面）。
func overlayFooterRow(gtx layout.Context, th *material.Theme, ov *overlayState) layout.Dimensions {
	children := []layout.FlexChild{}
	switch {
	case ov.persona && ov.page == overlayPageTemplate:
		children = append(children,
			layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
				return overlayTplStatus(gtx, th, ov)
			}),
			layout.Rigid(overlayBtn(&ov.btnSaveTpl, th, "保存模板", false, ov.prompt != nil)),
			layout.Rigid(layout.Spacer{Width: unit.Dp(6)}.Layout),
			layout.Rigid(overlayBtn(&ov.btnApply, th, "应用到本会话", true, ov.prompt != nil)),
		)
	case ov.persona:
		// 改人格必须保留后续历史：三档都画出来（不是隐藏），另两档禁用——用户要
		// 知道「另外两种应用方式存在，但这里不适用」，而不是以为功能被砍了。
		modes := []struct {
			c    *widget.Clickable
			text string
			m    conversation.KeepMode
		}{
			{&ov.modeFresh, "另起新分支", conversation.Fresh},
			{&ov.modeCarry, "转移历史", conversation.Carry},
			{&ov.modeClone, "复制历史", conversation.Clone},
		}
		for i, md := range modes {
			if i > 0 {
				children = append(children, layout.Rigid(layout.Spacer{Width: unit.Dp(6)}.Layout))
			}
			children = append(children, layout.Rigid(overlayPillBtnMode(md.c, th, md.text, ov.mode == md.m, ov.mode == md.m)))
		}
		children = append(children,
			layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
				return layout.Spacer{}.Layout(gtx)
			}),
			layout.Rigid(overlayBtn(&ov.btnSave, th, "保存", true, ov.canSave())),
		)
	case ov.editable:
		modes := []struct {
			c    *widget.Clickable
			text string
			m    conversation.KeepMode
		}{
			{&ov.modeFresh, "另起新分支", conversation.Fresh},
			{&ov.modeCarry, "转移历史", conversation.Carry},
			{&ov.modeClone, "复制历史", conversation.Clone},
		}
		for i, md := range modes {
			if i > 0 {
				children = append(children, layout.Rigid(layout.Spacer{Width: unit.Dp(6)}.Layout))
			}
			children = append(children, layout.Rigid(overlayPillBtn(md.c, th, md.text, ov.mode == md.m)))
		}
		children = append(children,
			layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
				return layout.Spacer{}.Layout(gtx)
			}),
			layout.Rigid(overlayBtn(&ov.btnSave, th, "保存", true, ov.canSave())),
		)
	default:
		children = append(children, layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
			l := material.Caption(th, "该节点只读")
			l.Color = paperOf().text2
			return l.Layout(gtx)
		}))
	}
	children = append(children,
		layout.Rigid(layout.Spacer{Width: unit.Dp(6)}.Layout), // 与页签行同款间距：两钮贴在一起会读成一块
		layout.Rigid(overlayBtn(&ov.btnClose, th, "关闭", false, true)))
	return layout.Flex{Alignment: layout.Middle}.Layout(gtx, children...)
}

// overlayPillBtnMode 方式胶囊的可禁用变体（人格节点锁 Carry，D154）：选中档仍用强调底，
// 禁用档用下沉面 + 三级字（点击不响应——调用方只在非 persona 分支消费 Clicked）。
func overlayPillBtnMode(c *widget.Clickable, th *material.Theme, text string, on, enabled bool) layout.Widget {
	if enabled {
		return overlayPillBtn(c, th, text, on)
	}
	pp := paperOf()
	return func(gtx layout.Context) layout.Dimensions {
		return overlayPill(gtx, pp.sunken, overlayPillRadiusDp, func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Top: unit.Dp(4), Bottom: unit.Dp(4), Left: unit.Dp(10), Right: unit.Dp(10)}.
				Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					l := material.Caption(th, text)
					l.Color = pp.text3
					return l.Layout(gtx)
				})
		})
	}
}

// overlayPillRadiusDp 卡内胶囊圆角。
const overlayPillRadiusDp unit.Dp = 6

// overlayBtnColors 底行动作钮配色：primary = 反色强调；disabled = 次要底 + 暗字
// （D139 ⑤「空文本禁用保存」的可见形态）。
func overlayBtnColors(th *material.Theme, primary, enabled bool) (bg, fg color.NRGBA) {
	pp := paperOf()
	switch {
	case !enabled:
		return pp.sunken, pp.text3
	case primary:
		return pp.accent, pp.accentFg
	default:
		return pp.sunken, pp.text
	}
}

// overlayBtn 底行动作钮（primary = 反色强调；enabled=false 呈禁用态，点击亦为 no-op）。
func overlayBtn(c *widget.Clickable, th *material.Theme, text string, primary, enabled bool) layout.Widget {
	bg, fg := overlayBtnColors(th, primary, enabled)
	return func(gtx layout.Context) layout.Dimensions {
		return c.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			return overlayPill(gtx, bg, overlayPillRadiusDp, func(gtx layout.Context) layout.Dimensions {
				return layout.Inset{Top: unit.Dp(4), Bottom: unit.Dp(4), Left: unit.Dp(12), Right: unit.Dp(12)}.
					Layout(gtx, func(gtx layout.Context) layout.Dimensions {
						l := material.Caption(th, text)
						l.Color = fg
						return l.Layout(gtx)
					})
			})
		})
	}
}
