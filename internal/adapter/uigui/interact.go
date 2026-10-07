package uigui

import (
	"image"
	"io"
	"strings"

	"gioui.org/gesture"
	"gioui.org/io/clipboard"
	"gioui.org/io/event"
	"gioui.org/io/input"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"

	"github.com/Tonyjh07/Aquarius/internal/port"

	"github.com/Tonyjh07/Aquarius/internal/domain/conversation"
)

// frameItems 定稿块 + 实时思考/草稿（流式可见；对齐 D33"流式原样、定稿渲染"口径）。
// 助手定稿块 = 单条目携 markdown 结构块（D66 单回复单气泡），工具块 = 合并 chip
// 携块序（D67），live 草稿原样单行；带分叉的正文块后紧跟一条分叉条（D81）。
func (u *UI) frameItems() []blockView {
	strips := u.branchStrips()
	next := 0 // strips 按块序升序，指针单向前进即对齐
	items := make([]blockView, 0, len(u.m.blocks)+len(strips)+2)
	for bi, b := range u.m.blocks {
		switch {
		case b.kind == blockAssistant:
			items = append(items, blockView{kind: blockAssistant, md: u.mdBlocks(b.text), bi: bi, id: b.id})
		case b.kind == blockTool && b.chip != nil:
			// bi 同填（D100：chip 右键经 bubbleHit.bi 回查 chip 态；chipIdx = 点击件缓存槽）。
			items = append(items, blockView{kind: blockTool, chip: b.chip, chipIdx: bi, bi: bi})
		default:
			items = append(items, blockView{kind: b.kind, text: b.text, secs: b.secs, bi: bi, id: b.id})
		}
		if next < len(strips) && strips[next].blockIdx == bi {
			s := strips[next] // 拷贝：条目持值，避免共享切片元素
			items = append(items, blockView{kind: blockBranch, branch: &s})
			next++
		}
	}
	if u.m.think.Len() > 0 {
		items = append(items, blockView{kind: blockThinking, text: u.m.think.String(), live: true})
	}
	if u.m.drafting || u.m.draft.Len() > 0 {
		items = append(items, blockView{
			kind: blockAssistant,
			text: strings.TrimRight(u.m.draft.String(), "\n"),
			live: true,
		})
	}
	return items
}

// statusText 状态行文本（数据源同 uitui Status 回调：Model/Level/Effort 现取）。
func (u *UI) statusText() string {
	if u.generating.Load() {
		phase := "生成中"
		if u.m.think.Len() > 0 {
			phase = "思考中"
		}
		parts := []string{phase}
		if u.opts.Status != nil {
			st := u.opts.Status()
			if st.Model != "" {
				parts = append(parts, st.Model)
			}
			if st.Level != "" {
				parts = append(parts, st.Level)
			}
			if st.Effort != "" {
				parts = append(parts, st.Effort)
			}
		}
		return strings.Join(parts, " · ")
	}
	// D92/D97 编辑态提示（无生成状态时占用状态行；生成优先——编辑可跨生成提交排队）；
	// 方式后缀让当前修订方式在提交前始终可见（D97④）。
	if u.m.editTarget != "" {
		switch u.m.editMode {
		case conversation.Carry:
			return "编辑中（转移历史）· Enter 提交 / Esc 取消"
		case conversation.Clone:
			return "编辑中（复制历史）· Enter 提交 / Esc 取消"
		}
		return "编辑中 · Enter 提交 / Esc 取消"
	}
	return ""
}

// flushCopy 处理挂起的复制请求（D92，frame 每帧调用）：clipboard.WriteCmd 须在 Gio
// 帧上下文执行——shell 线程分发的 copyMsg 经主循环记账（pendingCopy）到这里落盘。
func (u *UI) flushCopy(gtx layout.Context) {
	if u.pendingCopy == "" {
		return
	}
	t := u.pendingCopy
	u.pendingCopy = ""
	gtx.Execute(clipboard.WriteCmd{
		Type: "application/text", // Gio 自家 Selectable 复制同款 MIME（selCopy 同款）
		Data: io.NopCloser(strings.NewReader(t)),
	})
}

// updateClicks 控件行为：发送/停止/允许/拒绝（logo 手势与悬停在 updateLogo）。
// D54：动画期间几何在动（右钮半程在飞），不接受点击。
func (u *UI) updateClicks(gtx layout.Context) {
	if u.expandAn.active {
		return
	}
	if u.sendBtn.Clicked(gtx) {
		u.submitEditor()
	}
	if u.stopBtn.Clicked(gtx) {
		u.interruptNow() // 生成中发送键变停止键（§15.2）
	}
	if u.allowBtn.Clicked(gtx) {
		u.m.replyConfirm(port.ConfirmAnswer{Allow: true})
	}
	if u.denyBtn.Clicked(gtx) {
		u.m.replyConfirm(port.ConfirmAnswer{Reason: u.confirmReason()})
	}
	if u.elevateBtn.Clicked(gtx) {
		u.confirmElevate() // D86：/permission 升一档 + 放行本次
	}
	if u.attachBtn.Clicked(gtx) {
		u.requestFileDlg() // D104：附件槽 → shell 线程文件选择框（模态泵不嵌 Gio 泵）
	}
	if u.attachClear.Clicked(gtx) {
		u.m.clearAttach() // D104：暂存 chip 点击 = 取消暂存
	}
	if u.expandBtn.Clicked(gtx) {
		u.setExpanded(!u.expanded) // D106：展开/收起切换
	}
	// 工具 chip 头部点击 → 折叠/展开（D67；m.blocks 只增，块序即 chipIdx）。
	for i, b := range u.m.blocks {
		if b.kind == blockTool && b.chip != nil && u.chipClick(i).Clicked(gtx) {
			if u.chipOpen == nil {
				u.chipOpen = map[int]bool{}
			}
			u.chipOpen[i] = !u.chipOpen[i]
		}
	}
	// 分叉条左右切换（D81）：投递 `/goto <兄弟id>` 经输入通道（与键入同路径，壳内不
	// 旁路）；已在边界则不环绕（按钮置暗且不投递）。
	for _, s := range u.branchStrips() {
		switch {
		case u.branchClick(s.slot, false).Clicked(gtx):
			u.gotoBranch(s, -1)
		case u.branchClick(s.slot, true).Clicked(gtx):
			u.gotoBranch(s, +1)
		}
	}
}

// updateDrag 拖动定位（§15.6 铁律 2：按下记「窗口左上角 + 光标屏幕坐标」按差值
// 定位——窗口移动会改变指针本地坐标，本地增量法与移动互为反馈会回弹）。
// 背景把手 = 输入栏空白/状态行/收起态整窗（气泡区是滚动区，§15.1）；
// 收起态单击球（位移小于阈值）= 再展开。
func (u *UI) updateDrag(gtx layout.Context) {
	for {
		ev, ok := u.drag.Update(gtx.Metric, gtx.Source, gesture.Both)
		if !ok {
			break
		}
		switch ev.Kind {
		case pointer.Press:
			u.undockInstant() // D50：按下即脱离停靠（拖动/点击都从贴齐亮态起）
			u.beginDrag()
		case pointer.Drag:
			u.moveDrag()
		case pointer.Release, pointer.Cancel:
			was := u.dragging
			u.endDrag()
			// 收起态：单击球 = 再展开；移动 = 拖窗（§15.1）。
			// D54：动画中本把手与 logo 钮重叠，展开语义归 logo 的互切（防背景误触发反向）。
			if ev.Kind == pointer.Release && was && u.collapsed &&
				!u.expandAn.active && u.clickHeld() {
				u.beginExpand() // 展开即入焦点（含）
			}
		}
	}
}

// bubbleHit 右键命中的气泡底板矩形（D92）：rect = 气泡底板（窗口系，与形状登记同一
// bgRect——padding 区也是气泡的一部分）；bi/id/kind 定位块，keyBase/keyN = 该块的
// 行选键区间（整条复制按键区间取渲染文本）。随帧复位、消费者阶段读上一帧（一帧陈旧）。
type bubbleHit struct {
	rect          image.Rectangle
	bi            int
	id            conversation.MessageID
	kind          blockKind
	keyBase, keyN int
}

// bubbleRight 气泡右键手势（D92，D72 logoRight 同款）：Secondary 按下武装、同指针
// 抬起 = fire（携按下/抬起两点——是否「原位」由消费方按气泡粒度判定）；Cancel/非
// 右键 = 放弃。gesture 系跳过非主键按下，与 D63/D91 主键选态零冲突。按下与抬起
// 分属两批事件，按下位置必须持久化在手势态里（Press/Release 事件各成一批送达）。
type bubbleRight struct {
	armed bool
	pid   pointer.ID
	press image.Point // 按下位置（窗口系；抬起时与抬起点比对定「原位」）
}

// add 注册右键手势（转写视口 clip 内、与 D91 观察者同组命中链；事件坐标 = 窗口系）。
func (r *bubbleRight) add(ops *op.Ops) { event.Op(ops, r) }

// update 消费手势事件：同指针按下后抬起返回 (按下点, 抬起点, true)；移出后抬起、
// Cancel、非右键按下均解除武装（位置门控的气泡级判定在消费方——hitBubble 比对）。
func (r *bubbleRight) update(q input.Source) (press, release image.Point, fire bool) {
	for {
		ev, ok := q.Event(pointer.Filter{
			Target: r,
			Kinds:  pointer.Press | pointer.Release | pointer.Cancel,
		})
		if !ok {
			break
		}
		e, ok := ev.(pointer.Event)
		if !ok {
			continue
		}
		switch e.Kind {
		case pointer.Press:
			r.armed = e.Buttons.Contain(pointer.ButtonSecondary)
			r.pid = e.PointerID
			r.press = e.Position.Round()
		case pointer.Release:
			if r.armed && e.PointerID == r.pid {
				release = e.Position.Round()
				press = r.press
				fire = true
			}
			r.armed = false
		case pointer.Cancel:
			r.armed = false
		}
	}
	return press, release, fire
}

// hitBubble 点落在哪个气泡底板（D92）；未命中返回 -1。
func (u *UI) hitBubble(p image.Point) int {
	for i := range u.bubbleRects {
		if p.In(u.bubbleRects[i].rect) {
			return i
		}
	}
	return -1
}

// updateBubbleRight 气泡右键消费者（D92）：命中解析 + 菜单请求。bubbleRects 一帧
// 陈旧（与 keyRects 同口径）——命中基于上一帧气泡矩形；「原位」= 按下与抬起落在
// 同一气泡（抬起位置定菜单位）。
func (u *UI) updateBubbleRight(gtx layout.Context) {
	press, release, fire := u.bubbleRight.update(gtx.Source)
	if !fire {
		return
	}
	pi := u.hitBubble(press)
	if pi < 0 || pi != u.hitBubble(release) {
		return
	}
	u.requestBubbleMenu(u.bubbleCtx(pi))
}

// bubbleMenuCtx 气泡右键菜单上下文（D92/D97/D99/D100/D107）：Gio 线程命中时组好、
// atomic.Pointer 过线程到 shell；edit = 编辑预填文本（user/assistant 块原文，D97 开放
// 助手编辑），part = 编辑目标正文分片序号（D107②，-1 = 缺省合段），copy = 复制文本
// （选区优先，否则按块角色取），raw = 查看原文内容（D99/D100：标题 + 原始文本/JSON）。
type bubbleMenuCtx struct {
	id   conversation.MessageID
	kind blockKind
	edit string
	part int
	copy string
	raw  rawContent
}

// bubbleCtx 组装菜单上下文（D92/D97/D99/D100）：复制文本此刻定——选区激活取选区
// （D91 后果⑤：副键留菜单复用选态），否则按块角色（chip 用其态字段，见 D100④——
// 折叠态键未铺开、blockText 不可靠；其余逐键 Text 拼接与 selCopy 同口径）；编辑预填
// 仅 user/assistant；raw 按块角色组装。
func (u *UI) bubbleCtx(h int) *bubbleMenuCtx {
	it := u.bubbleRects[h]
	ctx := &bubbleMenuCtx{id: it.id, kind: it.kind}
	chip := u.chipOf(it) // D100：chip 态取自所源块（bubbleHit 不携指针）
	switch {
	case u.sel.active:
		ctx.copy = u.selText()
	case chip != nil:
		ctx.copy = chipCopyText(chip)
	default:
		ctx.copy = u.blockText(it.keyBase, it.keyN)
	}
	if (it.kind == blockUser || it.kind == blockAssistant) && it.bi < len(u.m.blocks) {
		ctx.edit = u.m.blocks[it.bi].text
		ctx.part = u.m.blocks[it.bi].part // D107②：编辑目标正文分片序号
	}
	switch {
	case chip != nil:
		ctx.raw = chipRaw(chip)
	case it.kind == blockThinking && it.bi < len(u.m.blocks):
		ctx.raw = rawContent{title: "思考原文 · " + shortID(string(it.id)), text: u.m.blocks[it.bi].text}
	case it.bi < len(u.m.blocks):
		ctx.raw = rawContent{title: "原文 · " + shortID(string(it.id)), text: u.m.blocks[it.bi].text}
	}
	return ctx
}

// chipOf 命中矩形对应的 chip（D100）：经块序回查 model 块（越界/非工具块 = nil）。
func (u *UI) chipOf(it bubbleHit) *toolChip {
	if it.kind != blockTool || it.bi >= len(u.m.blocks) {
		return nil
	}
	return u.m.blocks[it.bi].chip
}

// requestBubbleMenu 请求弹出气泡右键菜单（D92）：测试经 bubbleMenuHook 回执；生产
// 存上下文原子槽并投 shell 线程呈现（TrackPopupMenu 不嵌 Gio 泵，D72 同款）。
func (u *UI) requestBubbleMenu(ctx *bubbleMenuCtx) {
	if u.bubbleMenuHook != nil {
		u.bubbleMenuHook(ctx)
		return
	}
	u.bubbleMenu.Store(ctx)
	postBubbleMenu()
}

// beginDrag 记录拖动基准（窗口左上角 + 光标位置，铁律 2 绝对跟踪；按下时窗口未
// 就绪则忽略本次触发）。基点取逻辑位 u.x/u.y 而非 windowRectPx——停靠态按下先
// undockInstant 记账（D55，SetWindowPos 帧末才发），此刻 OS 矩形还是滑出位；按旧值
// 起基，点击期间 ≥1px 抖动的 moveDrag 会把贴齐位回退成滑出位（离边超 snapDp →
// 布防/停靠断链，§15.1 实测缺陷）。
func (u *UI) beginDrag() {
	if u.hwnd != 0 {
		u.dragWin0 = point{x: u.x, y: u.y}
		u.dragCur0 = cursorPos()
		u.dragging = true
	}
}

// moveDrag 主窗跟随光标（拖动中，铁律 2 绝对跟踪）；可见锚点实时夹取（D50 不出桌面）。
func (u *UI) moveDrag() {
	if !u.dragging {
		return
	}
	cur := cursorPos()
	x := u.dragWin0.x + (cur.x - u.dragCur0.x)
	y := u.dragWin0.y + (cur.y - u.dragCur0.y)
	u.x, u.y = u.clampPos(x, y)
	u.requestMove() // D55：commitWinGeom 一拍提交（本帧内只记账）
}

// endDrag 抬起/取消收尾：锚点夹取 + 四边吸附贴齐（D50）+ 持久化位置。
// 按下时已脱离停靠（undockInstant），故落盘恒为未停靠。
// 纯点击（位移 ≤ dragClickSlackPx）跳过夹取/吸附：「点击脱离停靠」把窗口落在半出屏
// 贴边位（球锚点越界合法），若再按抬手时的态夹一次，展开态整窗锚点会把它推离边缘、
// 收起后球离边超 snapDp → 布防/停靠断链（D50 实测缺陷修订，§15.1）。
func (u *UI) endDrag() {
	if u.dragging {
		if !u.clickHeld() {
			u.x, u.y = u.clampPos(u.x, u.y)
			if a, ok := u.anchorFor(); ok {
				pos := point{x: u.x, y: u.y}
				if work, wok := platformWorkArea(anchorCenter(pos, a)); wok {
					if d := snapDelta(pos, a, work, int32(u.frameMetric.Dp(snapDp))); d != (point{}) {
						u.x += d.x
						u.y += d.y
					}
				}
			}
			u.requestMove() // D55：吸附后落位记账，commitWinGeom 一拍提交
		}
		// 抬手（点击/拖动）=「曾悬停」的证据：直接布防（D50 拍板"拖到可停靠区移开也重停"），
		// 贴边由下一瞬的 evalDockFrame 校验 edge，不贴边/展开态自然清掉。
		u.dockArm = true
		u.savePosRec("")
	}
	u.dragging = false
}

// clickHeld 抬起时位移小于阈值 = 单击（与拖窗判定互斥）。
func (u *UI) clickHeld() bool {
	cur := cursorPos()
	dx, dy := cur.x-u.dragCur0.x, cur.y-u.dragCur0.y
	if dx < 0 {
		dx = -dx
	}
	if dy < 0 {
		dy = -dy
	}
	return dx <= dragClickSlackPx && dy <= dragClickSlackPx
}
