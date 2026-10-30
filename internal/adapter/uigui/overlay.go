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

// 本文件只留遮罩的状态与行为（页签与模式常量、打开/关闭、事件与动作、提交）；
// 绘制见 overlay_view.go。

import (
	"image"
	"strings"

	"gioui.org/io/key"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/unit"
	"gioui.org/widget"

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

// modalActive 任一模态层在屏（遮罩 / 确认弹窗 / 插入摘要小卡 / 导入勾选面板）：背景
// （左栏 / 状态栏 / 右图手势）一律不取指针事件。
func (st *historyState) modalActive(u *UI) bool {
	return st.overlayActive() || st.confirmActive(u) || st.sum != nil || st.pick != nil
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
