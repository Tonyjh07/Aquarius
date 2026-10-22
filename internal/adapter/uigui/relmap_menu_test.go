package uigui

// relmap_menu_test.go 右图右键菜单：项集与文案、根/人格节点不弹摘要、拖走后
// 不开菜单、查看/原文/重生成/引用/删除/复制 ID/生成响应各动作，以及会话根
// 左键复制 ID。

import (
	"testing"
	"time"

	"gioui.org/f32"
	"gioui.org/io/input"
	"gioui.org/io/pointer"
	"github.com/Tonyjh07/Aquarius/internal/adapter/uigui/platform"
	"github.com/Tonyjh07/Aquarius/internal/domain/conversation"
)

// relFixtureBody 夹具节点的正文全文（D132）：刻意长得跟 40 字摘要（"s-<id>"）不同，
// 断言「拿到的是全文不是摘要」一眼可辨。
func relFixtureBody(id conversation.MessageID) string {
	return "【全文】" + string(id) + "：这一段正文比节点摘要长得多，用来验证编辑预填取的是全文。"
}

// relRightPress / relRightReleaseAt 右键按下 / 原位抬起（本地索引 li 的屏幕位置）。
func relRightPress(st *historyState, li int) pointer.Event {
	g := st.graph
	xs, ys := g.toScreen(g.sol.Pos[li])
	return pointer.Event{Kind: pointer.Press, Position: f32.Pt(xs, ys), PointerID: 7,
		Source: pointer.Mouse, Buttons: pointer.ButtonSecondary}
}

func relRightReleaseAt(st *historyState, li int) pointer.Event {
	e := relRightPress(st, li)
	e.Kind = pointer.Release
	e.Buttons = 0
	return e
}

// relNodeIndex 本地索引 li（st.vis.nodes[li]）满足模型节点 id 命中（全预算夹具里节点全可见）。
func relNodeIndex(t *testing.T, st *historyState, id conversation.MessageID) int {
	t.Helper()
	for li, mi := range st.vis.nodes {
		if st.model.nodes[mi].id == id {
			return li
		}
	}
	t.Fatalf("节点 %s 不在可见集", id)
	return -1
}

// relDrainAs 从收件箱取出指定类型的桥接消息（菜单分发经 post 投递，§15.6）。
func relDrainAs(t *testing.T, u *UI, dst any) bool {
	t.Helper()
	for {
		select {
		case m := <-u.inbox:
			switch d := dst.(type) {
			case *editMsg:
				if v, ok := m.(editMsg); ok {
					*d = v
					return true
				}
			case *inputMsg:
				if v, ok := m.(inputMsg); ok {
					*d = v
					return true
				}
			case *quoteMsg:
				if v, ok := m.(quoteMsg); ok {
					*d = v
					return true
				}
			case *showExpandMsg:
				if v, ok := m.(showExpandMsg); ok {
					*d = v
					return true
				}
			}
		default:
			return false
		}
	}
}

// TestRelGraphRightClickMenu 图节点右键 → 菜单上下文（D132：与主界面气泡菜单同一呈现/
// 分发，项集 = 气泡项 + 图侧三项）：右键按下武装、**原位抬起**才 fire（菜单不在按键未
// 松时弹出，免得吃掉这次抬起）；上下文 id = 节点、正文取全文（不是 40 字摘要）。
func TestRelGraphRightClickMenu(t *testing.T) {
	u, st, q := relGraphFixture(t)
	g := st.graph
	sx, li := relAnchorScreen(t, g)
	_ = li
	want := st.model.nodes[st.vis.nodes[g.anchor]].id
	if ctx := u.bubbleMenu.Load(); ctx != nil {
		t.Fatal("夹具不应残留菜单上下文")
	}
	// 右键按下（secondary）：只武装，不出菜单
	q.Queue(pointer.Event{Kind: pointer.Press, Position: sx, PointerID: 7,
		Source: pointer.Mouse, Buttons: pointer.ButtonSecondary})
	relGFrame(q, u, st)
	if !g.menuArm || g.menuNode != g.anchor {
		t.Fatalf("按下未武装: arm=%v node=%d, want true/%d", g.menuArm, g.menuNode, g.anchor)
	}
	if u.bubbleMenu.Load() != nil {
		t.Fatal("右键按下就出菜单（应为抬起才 fire）")
	}
	// 原位抬起 → 菜单上下文就位
	q.Queue(pointer.Event{Kind: pointer.Release, Position: sx, PointerID: 7,
		Source: pointer.Mouse})
	relGFrame(q, u, st)
	ctx := u.bubbleMenu.Load()
	if ctx == nil {
		t.Fatal("右键抬起未请求菜单")
	}
	if ctx.id != want || !ctx.graph {
		t.Fatalf("菜单上下文 = id %q graph=%v, want %q/true", ctx.id, ctx.graph, want)
	}
	if ctx.edit != relFixtureBody(want) {
		t.Fatalf("编辑预填 = %q, want 节点正文全文 %q（不是摘要）", ctx.edit, relFixtureBody(want))
	}
	if g.menuArm {
		t.Fatal("fire 后武装未解除")
	}
	// 项集 = 气泡七项 + 生成响应 + 分隔线 + 图侧四项（D139 加「转到」；D152 加「插入摘要」；
	// 2026-10-25 加「生成响应」，贴气泡项组末尾、分隔线之前）
	items := u.menuItems(platform.MenuBubble)
	wantLabels := []string{"编辑", "编辑并转移历史", "编辑并复制历史", "重新生成", "复制",
		"引用", "查看原文", "生成响应", "", "复制 ID", "插入摘要", "删除该节点及子树", "转到"}
	if len(items) != len(wantLabels) {
		t.Fatalf("项数 = %d, want %d", len(items), len(wantLabels))
	}
	for i, w := range wantLabels {
		if items[i].Label != w {
			t.Fatalf("第 %d 项 = %q, want %q", i, items[i].Label, w)
		}
	}
	if !ctx.canSummary {
		t.Fatal("普通节点（非根/非人格）应可插摘要")
	}
}

// TestRelGraphRightClickNoSummaryForRootAndPersona 会话根与会话首节点（人格）不出
// 「插入摘要」：根前无处可插；人格之前插摘要会让摘要顶到路径第二位、被装配当成 persona。
// 会话根整份菜单已删（2026-10-25 用户批注），故根只断言「不弹菜单」。
func TestRelGraphRightClickNoSummaryForRootAndPersona(t *testing.T) {
	u, st, q := relGraphFixture(t)
	// 会话根：右键不弹任何菜单。
	li := relNodeIndex(t, st, "r")
	q.Queue(relRightPress(st, li))
	q.Queue(relRightReleaseAt(st, li))
	relGFrame(q, u, st)
	if ctx := u.bubbleMenu.Load(); ctx != nil {
		t.Fatalf("会话根右键不应出上下文: %+v", ctx)
	}
	// 会话首节点（人格）：出菜单但没有「插入摘要」。
	li = relNodeIndex(t, st, "p")
	q.Queue(relRightPress(st, li))
	q.Queue(relRightReleaseAt(st, li))
	relGFrame(q, u, st)
	ctx := u.bubbleMenu.Load()
	if ctx == nil || ctx.id != "p" {
		t.Fatalf("节点 p 右键未出上下文: %+v", ctx)
	}
	if ctx.canSummary {
		t.Fatalf("节点 p（kind=%v）不应可插摘要", ctx.kind)
	}
	for _, it := range u.menuItems(platform.MenuBubble) {
		if it.Label == "插入摘要" {
			t.Fatal("节点 p 的菜单不该有「插入摘要」")
		}
	}
}

// TestRelGraphRightClickNoMenuOnDragAway 右键按下后**移开抬起**（非原位）→ 不出菜单。
func TestRelGraphRightClickNoMenuOnDragAway(t *testing.T) {
	u, st, q := relGraphFixture(t)
	sx, _ := relAnchorScreen(t, st.graph)
	q.Queue(pointer.Event{Kind: pointer.Press, Position: sx, PointerID: 7,
		Source: pointer.Mouse, Buttons: pointer.ButtonSecondary})
	relGFrame(q, u, st)
	q.Queue(pointer.Event{Kind: pointer.Release, Position: f32.Pt(sx.X+260, sx.Y+180), PointerID: 7,
		Source: pointer.Mouse})
	relGFrame(q, u, st)
	if u.bubbleMenu.Load() != nil {
		t.Fatal("非原位抬起不应出菜单")
	}
}

// TestRelGraphMenuEditOpensOverlayWithFullText 菜单「编辑并转移历史」→ **开遮罩并预选
// Carry**（D139：不再投主窗 editMsg）；正文在遮罩内经 NodeText 现取 = **全文**（D132 的
// 要点：摘要会截断消息）；part 缺省合段（遮罩提交不带 --part）。
func TestRelGraphMenuEditOpensOverlayWithFullText(t *testing.T) {
	u, st, q := relGraphFixture(t)
	const li = 4 // a1（assistant，正文块）
	wantID := st.model.nodes[st.vis.nodes[li]].id
	q.Queue(relRightPress(st, li))
	q.Queue(relRightReleaseAt(st, li))
	relGFrame(q, u, st)
	u.menuChosen(platform.MenuBubble, cmdBubbleEditKeep)

	act := u.nodeAction.Load()
	if act == nil || act.kind != nodeActOpenOverlay {
		t.Fatalf("图侧编辑未投开遮罩动作: %+v", act)
	}
	if act.id != wantID || act.mode != conversation.Carry || act.page != overlayPageRendered {
		t.Fatalf("开遮罩动作 = %+v, want id=%s Carry/呈现", act, wantID)
	}
	// 历史窗帧首排空 → 遮罩开，正文 = 全文（不是 40 字摘要）。
	histFrameFor(q, u, st)
	if st.ov == nil {
		t.Fatal("遮罩未开启")
	}
	if st.ov.src != relFixtureBody(wantID) {
		t.Fatalf("遮罩源码 = %q, want 全文 %q", st.ov.src, relFixtureBody(wantID))
	}
	if st.ov.mode != conversation.Carry || !st.ov.editable {
		t.Fatalf("遮罩态 = mode %v editable %v, want Carry/true", st.ov.mode, st.ov.editable)
	}
	// 图侧编辑**不得**再投 editMsg（否则跨窗又去主窗输入栏）。
	var got editMsg
	if relDrainAs(t, u, &got) {
		t.Fatal("图侧编辑不应再投 editMsg（D139 改走遮罩）")
	}
}

// TestRelGraphMenuRawOpensOverlaySourcePage 菜单「查看原文」→ 开遮罩并**定位源码页签**
// （D139：图侧不再另开 winRaw 次窗）。
func TestRelGraphMenuRawOpensOverlaySourcePage(t *testing.T) {
	u, st, q := relGraphFixture(t)
	const li = 4
	q.Queue(relRightPress(st, li))
	q.Queue(relRightReleaseAt(st, li))
	relGFrame(q, u, st)
	u.menuChosen(platform.MenuBubble, cmdBubbleRaw)
	act := u.nodeAction.Load()
	if act == nil || act.kind != nodeActOpenOverlay || act.page != overlayPageSource {
		t.Fatalf("查看原文动作 = %+v, want 开遮罩/源码页", act)
	}
	histFrameFor(q, u, st)
	if st.ov == nil || st.ov.page != overlayPageSource {
		t.Fatalf("遮罩页签 = %+v, want 源码页", st.ov)
	}
	if st.ov.ed.Text() == "" {
		t.Fatal("源码页编辑器未预填正文")
	}
}

// TestRelGraphMenuRegenAndQuote 菜单「重新生成」/「引用」与主界面同命令路径：/regen <id>
// 与 quoteMsg（引用文本 = 全文）。
func TestRelGraphMenuRegenAndQuote(t *testing.T) {
	u, st, q := relGraphFixture(t)
	const li = 4
	wantID := st.model.nodes[st.vis.nodes[li]].id
	q.Queue(relRightPress(st, li))
	q.Queue(relRightReleaseAt(st, li))
	relGFrame(q, u, st)
	u.menuChosen(platform.MenuBubble, cmdBubbleRegen)
	var in inputMsg
	if !relDrainAs(t, u, &in) {
		t.Fatal("重新生成未投 inputMsg")
	}
	if in.text != "/regen "+string(wantID) {
		t.Fatalf("inputMsg = %q, want /regen %s", in.text, wantID)
	}
	u.menuChosen(platform.MenuBubble, cmdBubbleQuote)
	var qm quoteMsg
	if !relDrainAs(t, u, &qm) {
		t.Fatal("引用未投 quoteMsg")
	}
	if qm.text != relFixtureBody(wantID) {
		t.Fatalf("引用文本 = %q, want 全文 %q", qm.text, relFixtureBody(wantID))
	}
}

// TestRelGraphMenuRootNodeNoMenu 会话根节点右键**不弹任何菜单**（2026-10-25 用户批注：
// root 的右键菜单整份删掉；root 左键改为复制会话 ID，见 TestRelGraphRootLeftClickCopiesID）。
func TestRelGraphMenuRootNodeNoMenu(t *testing.T) {
	u, st, q := relGraphFixture(t)
	q.Queue(relRightPress(st, 0)) // 0 = 局部 r（结构根）
	q.Queue(relRightReleaseAt(st, 0))
	relGFrame(q, u, st)
	if !st.graph.isRoot(0) {
		t.Fatal("夹具局部 0 应为结构根")
	}
	if st.graph.menuArm {
		t.Fatal("会话根右键不应武装（整份菜单已去掉）")
	}
	if ctx := u.bubbleMenu.Load(); ctx != nil {
		t.Fatalf("会话根右键不应弹菜单: %+v", ctx)
	}
	items := u.menuItems(platform.MenuBubble)
	if len(items) != 0 {
		t.Fatalf("会话根菜单项数 = %d, want 0（不弹菜单）", len(items))
	}
}

// TestRelGraphMenuNoBodyNode 图侧取不到正文（NodeText 未命中）→ 只剩图侧项：不拿空
// 文本预填编辑框（D132 的 hasText 分支；内核也会拒空修订文本，但菜单不该诱导）。
func TestRelGraphMenuNoBodyNode(t *testing.T) {
	u := newHistUI(fakeRelTree{g: relFixtureGraph()}, fakeLister(nil)) // 不配 texts
	st := newHistoryState(u)
	q := new(input.Router)
	relGFrame(q, u, st)
	q.Queue(relRightPress(st, 4))
	q.Queue(relRightReleaseAt(st, 4))
	relGFrame(q, u, st)
	ctx := u.bubbleMenu.Load()
	if ctx == nil || ctx.copy != "" {
		t.Fatalf("无正文节点上下文 = %+v, want copy 为空", ctx)
	}
	items := u.menuItems(platform.MenuBubble)
	want := []string{"生成响应", "", "复制 ID", "插入摘要", "删除该节点及子树", "转到"}
	if len(items) != len(want) {
		t.Fatalf("无正文节点项数 = %d, want %d", len(items), len(want))
	}
	for i, w := range want {
		if items[i].Label != w {
			t.Fatalf("第 %d 项 = %q, want %q", i, items[i].Label, w)
		}
	}
}

// TestRelGraphMenuRm 菜单「删除」→ /rm <id> 经 inCh（与键入同路径；分发在 shell 线程，
// 故这里直接走 menuChosen）。
func TestRelGraphMenuRm(t *testing.T) {
	u, st, q := relGraphFixture(t)
	const li = 4
	want := st.model.nodes[st.vis.nodes[li]].id
	q.Queue(relRightPress(st, li))
	q.Queue(relRightReleaseAt(st, li))
	relGFrame(q, u, st)
	u.menuChosen(platform.MenuBubble, cmdNodeRm)
	select {
	case in := <-u.inCh:
		if in.Command == nil || in.Command.Name != "rm" ||
			len(in.Command.Args) != 1 || conversation.MessageID(in.Command.Args[0]) != want {
			t.Fatalf("inCh = %+v, want /rm %s", in, want)
		}
	default:
		t.Fatal("inCh 空：菜单删除未投 /rm")
	}
}

// TestRelGraphMenuCopyIDAction 图侧「复制 ID」走动作槽 → **历史窗帧**内落剪贴板
// （剪贴板写入必须在 Gio 帧内，分发处不能直连，§15.6）。
func TestRelGraphMenuCopyIDAction(t *testing.T) {
	u, st, q := relGraphFixture(t)
	const li = 4
	want := st.model.nodes[st.vis.nodes[li]].id
	q.Queue(relRightPress(st, li))
	q.Queue(relRightReleaseAt(st, li))
	relGFrame(q, u, st)

	u.menuChosen(platform.MenuBubble, cmdNodeCopyID)
	act := u.nodeAction.Load()
	if act == nil || act.kind != nodeActCopyID || act.id != want {
		t.Fatalf("动作槽 = %+v, want nodeActCopyID %s", act, want)
	}
	gtx, _ := frameGtxSize(new(input.Router).Source(), 720, 560)
	st.drainNodeAction(u, gtx)      // 复制 ID 的剪贴板写入在帧内执行（headless 无外部可观察量，
	if u.nodeAction.Load() != nil { // 故只断言「动作被消费、不重复执行」）
		t.Fatal("动作槽未排空")
	}
	if !st.toast.active(gtx.Now) || st.toast.text != "已复制节点 ID" {
		t.Fatalf("复制 ID 未弹轻提示: %+v", st.toast)
	}
}

// TestRelGraphMenuRespond 图侧「生成响应」→ 唤出主窗 + 投 /respond <id>（与键入同路径）。
func TestRelGraphMenuRespond(t *testing.T) {
	u, st, q := relGraphFixture(t)
	const li = 4
	want := st.model.nodes[st.vis.nodes[li]].id
	q.Queue(relRightPress(st, li))
	q.Queue(relRightReleaseAt(st, li))
	relGFrame(q, u, st)

	ctx := u.bubbleMenu.Load()
	if ctx == nil || ctx.id != want {
		t.Fatalf("菜单上下文 = %+v, want id %s", ctx, want)
	}
	u.menuChosen(platform.MenuBubble, cmdNodeRespond)
	// 分发先 post showExpandMsg（唤出主窗），再 post inputMsg（/respond）——按序取。
	var se showExpandMsg
	if !relDrainAs(t, u, &se) {
		t.Fatal("生成响应未唤出主窗")
	}
	var im inputMsg
	if !relDrainAs(t, u, &im) {
		t.Fatal("生成响应未投 inputMsg")
	}
	if im.text != "/respond "+string(want) {
		t.Fatalf("投递命令 = %q, want /respond %s", im.text, want)
	}
}

// TestRelGraphRootLeftClickCopiesID 会话根**原位左键**抬起 → 复制会话 ID（= 根节点 ID，
// D19）+ 轻提示，**不开遮罩**（2026-10-25 用户批注：root 左键改为复制会话 ID、不弹窗）。
func TestRelGraphRootLeftClickCopiesID(t *testing.T) {
	u, st, q := relGraphFixture(t)
	histFrameFor(q, u, st)
	if !st.graph.isRoot(0) {
		t.Fatal("夹具局部 0 应为结构根")
	}
	xs, ys := st.graph.toScreen(st.graph.sol.Pos[0])
	rootID := st.model.nodes[st.vis.nodes[0]].id
	at := ovPaneToWindow(f32.Pt(xs, ys))
	q.Queue(relPointer(pointer.Press, at, true))
	histFrameFor(q, u, st)
	q.Queue(relPointer(pointer.Release, at, false))
	histFrameFor(q, u, st)
	if st.ov != nil {
		t.Fatal("会话根左键不应开遮罩")
	}
	if !st.toast.active(time.Now()) || st.toast.text != "已复制会话 ID" {
		t.Fatalf("会话根左键未弹「已复制会话 ID」: %+v", st.toast)
	}
	if string(rootID) == "" {
		t.Fatal("根节点 ID 为空")
	}
	select {
	case in := <-u.inCh:
		t.Fatalf("左键复制不应投命令: %+v", in)
	default:
	}
}
