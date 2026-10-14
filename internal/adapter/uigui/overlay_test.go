package uigui

// 会话历史窗内的消息查看/编辑遮罩（D139）测试：开启路径（单击节点）、页签与预填、
// 提交的三种方式、只读节点、Esc/点空白关闭、背景事件隔离（Gio 广播 ⇒ 靠「不取」
// 而非「挡住」）。

import (
	"image"
	"image/color"
	"testing"

	"gioui.org/f32"
	"gioui.org/gpu/headless"
	"gioui.org/io/input"
	"gioui.org/io/key"
	"gioui.org/io/pointer"

	"github.com/Tonyjh07/Aquarius/internal/adapter/uigui/platform"
	"github.com/Tonyjh07/Aquarius/internal/domain/conversation"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

// histFrameFor 一帧**整窗**历史窗（含左栏/右图/状态栏 + 遮罩层；D139 的动作槽排空
// 与遮罩绘制都在这里）。720×560 = 生产窗几何。
func histFrameFor(q *input.Router, u *UI, st *historyState) {
	gtx, ops := frameGtxSize(q.Source(), 720, 560)
	historyFrame(gtx, u.th, u, st)
	q.Frame(ops)
}

// ovPaneToWindow 右图栏坐标 → 窗口坐标（左栏固定 200dp，D139 测试用）。
func ovPaneToWindow(p f32.Point) f32.Point { return f32.Pt(p.X+200, p.Y) }

// histOpenOverlay 在夹具上开遮罩（指定节点 + 预选方式/页签）。
func histOpenOverlay(t *testing.T, u *UI, st *historyState, id conversation.MessageID,
	mode conversation.KeepMode, page overlayPage) {
	t.Helper()
	st.openOverlay(u, id, mode, page)
	if st.ov == nil {
		t.Fatalf("遮罩未开启（id=%s）", id)
	}
}

// ovUserID 夹具里第一条用户消息（u1）的正文节点。
func ovUserID(st *historyState) conversation.MessageID {
	for _, n := range st.model.nodes {
		if n.role == conversation.RoleUser {
			return n.id
		}
	}
	return ""
}

// TestOverlaySubmitModes D139：提交 = 结构化 `/edit` 经 inCh，flag 紧跟 id（D97① 位置
// 口径）：Fresh 无 flag / Carry = `--keep` / Clone = `--copy`；提交后关遮罩并唤出主窗。
func TestOverlaySubmitModes(t *testing.T) {
	for _, tc := range []struct {
		name string
		mode conversation.KeepMode
		want []string
	}{
		{"Fresh", conversation.Fresh, []string{"--fresh"}},
		{"Carry", conversation.Carry, []string{"--keep"}},
		{"Clone", conversation.Clone, []string{"--copy"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			u, st, q := relGraphFixture(t)
			id := ovUserID(st)
			histOpenOverlay(t, u, st, id, tc.mode, overlayPageSource)
			st.ov.ed.SetText("改后的问题")

			st.ov.submit(u, st)

			select {
			case in := <-u.inCh:
				if in.Command == nil || in.Command.Name != "edit" {
					t.Fatalf("inCh = %+v, want /edit 命令", in)
				}
				args := in.Command.Args
				if len(args) == 0 || conversation.MessageID(args[0]) != id {
					t.Fatalf("Args[0] = %v, want %s", args, id)
				}
				if args[len(args)-1] != "改后的问题" {
					t.Fatalf("Args 末项 = %q, want 编辑后正文", args[len(args)-1])
				}
				if tc.name == "Fresh" {
					if len(args) != 2 {
						t.Fatalf("Fresh Args = %v, want [id 文本]（无 flag）", args)
					}
				} else if len(args) != 3 || args[1] != tc.want[0] {
					t.Fatalf("%s Args = %v, want [id %s 文本]", tc.name, args, tc.want[0])
				}
			default:
				t.Fatal("inCh 空：遮罩提交未投 /edit")
			}
			if st.ov != nil {
				t.Fatal("提交后遮罩应关闭")
			}
			var se showExpandMsg
			if !relDrainAs(t, u, &se) {
				t.Fatal("提交后未唤出主窗（生成过程看不见，D139）")
			}
			_ = q
		})
	}
}

// TestOverlaySubmitRejectsEmpty 空/纯空白文本不提交（内核亦拒），遮罩保持开启。
func TestOverlaySubmitRejectsEmpty(t *testing.T) {
	u, st, _ := relGraphFixture(t)
	histOpenOverlay(t, u, st, ovUserID(st), conversation.Fresh, overlayPageSource)
	st.ov.ed.SetText("   \n ")
	st.ov.submit(u, st)
	select {
	case in := <-u.inCh:
		t.Fatalf("空文本不应提交: %+v", in)
	default:
	}
	if st.ov == nil {
		t.Fatal("空文本提交失败不应关闭遮罩")
	}
}

// TestOverlaySubmitKeepsDraftWhenChannelFull 输入通道满时提交**不关遮罩**：命令没投出去，
// 用户改的文本不能凭空消失（D139 的数据保真口径）。
func TestOverlaySubmitKeepsDraftWhenChannelFull(t *testing.T) {
	u, st, _ := relGraphFixture(t)
	histOpenOverlay(t, u, st, ovUserID(st), conversation.Fresh, overlayPageSource)
	st.ov.ed.SetText("改后的问题")
	for i := 0; i < cap(u.inCh); i++ { // 灌满输入通道
		u.inCh <- port.UserInput{Text: "占位"}
	}
	st.ov.submit(u, st)
	if st.ov == nil {
		t.Fatal("命令未投出却关了遮罩——用户文本丢失")
	}
	if st.ov.ed.Text() != "改后的问题" {
		t.Fatalf("草稿被清 = %q", st.ov.ed.Text())
	}
}

// TestOverlayReadOnlyNodes root/system 与无正文节点只读：提交不了、编辑器只读。
func TestOverlayReadOnlyNodes(t *testing.T) {
	u, st, _ := relGraphFixture(t)
	var rootID conversation.MessageID
	for _, n := range st.model.nodes {
		if n.role == conversation.RoleRoot {
			rootID = n.id
		}
	}
	if rootID == "" {
		t.Fatal("夹具缺 root 节点")
	}
	histOpenOverlay(t, u, st, rootID, conversation.Fresh, overlayPageSource)
	if st.ov.editable {
		t.Fatal("root 节点应为只读")
	}
	if !st.ov.ed.ReadOnly {
		t.Fatal("只读节点的编辑器应为 ReadOnly")
	}
	st.ov.submit(u, st)
	select {
	case in := <-u.inCh:
		t.Fatalf("只读节点不应提交: %+v", in)
	default:
	}
	if st.ov == nil {
		t.Fatal("只读节点提交（应为空操作）不应关闭遮罩")
	}
	// 无正文节点（NodeText 未命中）同样收敛为只读空卡。
	u2, st2, _ := relGraphFixture(t)
	st2.openOverlay(u2, ovUserID(st2), conversation.Fresh, overlayPageRendered)
	if !st2.ov.editable {
		t.Fatal("有正文的用户节点应可编辑")
	}
	st2.closeOverlay()
	u3 := newHistUI(fakeRelTree{g: relFixtureGraph()}, fakeLister(nil)) // 不配 texts
	st3 := newHistoryState(u3)
	st3.openOverlay(u3, ovUserID(st3), conversation.Fresh, overlayPageRendered)
	if st3.ov == nil {
		t.Fatal("无正文节点也应开遮罩（只读空卡）")
	}
	if st3.ov.editable || st3.ov.src != "" {
		t.Fatalf("无正文节点 = editable %v src %q, want 只读空", st3.ov.editable, st3.ov.src)
	}
}

// TestOverlayEscCloses Esc 关闭遮罩。**注意帧序**：Gio 的 `key.FocusCmd` 在帧尾
// （`Router.Frame` 执行命令）才生效，故开遮罩后需先出一帧把焦点（呈现页落在遮罩自身
// ——无编辑器可获焦）立起来，Esc 才投得到。
func TestOverlayEscCloses(t *testing.T) {
	u, st, q := relGraphFixture(t)
	histOpenOverlay(t, u, st, ovUserID(st), conversation.Fresh, overlayPageRendered)
	histFrameFor(q, u, st) // 焦点立起来
	q.Queue(key.Event{Name: key.NameEscape, State: key.Press})
	histFrameFor(q, u, st)
	if st.ov != nil {
		t.Fatal("Esc 未关闭遮罩")
	}
}

// TestOverlayEscClosesFromSourcePage 源码页 Esc 也关（焦点在编辑器上，双挂过滤器）。
func TestOverlayEscClosesFromSourcePage(t *testing.T) {
	u, st, q := relGraphFixture(t)
	histOpenOverlay(t, u, st, ovUserID(st), conversation.Fresh, overlayPageSource)
	histFrameFor(q, u, st) // 焦点进编辑器
	q.Queue(key.Event{Name: key.NameEscape, State: key.Press})
	histFrameFor(q, u, st)
	if st.ov != nil {
		t.Fatal("源码页 Esc 未关闭遮罩")
	}
}

// TestOverlayClickOutsideCloses 点卡片**外**关闭、点卡片**内**保持（D139 的「点空白关闭」
// 靠自挂过滤器按卡片盒判定——画 Clickable 挡不住，Gio 事件广播给全部命中 handler）。
// 点击序列需 Move+Press+Release：Gio 的抬起只投给「已进入窗口」的命中集。
func TestOverlayClickOutsideCloses(t *testing.T) {
	u, st, q := relGraphFixture(t)
	histOpenOverlay(t, u, st, ovUserID(st), conversation.Fresh, overlayPageRendered)
	histFrameFor(q, u, st)

	// 卡片内 → 保持开启。
	ovClick(q, st.ov.card.Min.X+8, st.ov.card.Min.Y+8)
	histFrameFor(q, u, st)
	if st.ov == nil {
		t.Fatal("点卡片内不应关闭遮罩")
	}
	// 卡片外（视口角落）→ 关闭。
	ovClick(q, 2, 2)
	histFrameFor(q, u, st)
	if st.ov != nil {
		t.Fatal("点遮罩空白应关闭遮罩")
	}
}

// TestOverlayOpenClickDoesNotCloseItself 点节点开遮罩的那次点击**不会**把遮罩立刻关掉
// （Gio `gtx.Event` 排他消费：抬起已被右图的过滤器抽干，遮罩层收不到——故这里不需要
// armed 守卫）；随后点卡片外才关。变异敏感：若遮罩改为也消费那次抬起，本用例即红。
func TestOverlayOpenClickDoesNotCloseItself(t *testing.T) {
	u, st, q := relGraphFixture(t)
	histFrameFor(q, u, st) // 先出一帧：右图按真实面板尺寸就位
	wgtx, _ := frameGtxSize(q.Source(), 720, 560)
	card := overlayCardBox(wgtx)
	li := -1
	var pos f32.Point
	for i, gi := range st.graph.vis.nodes {
		x, y := st.graph.toScreen(st.graph.sol.Pos[gi])
		p := f32.Pt(x, y)
		if p.X < float32(card.Min.X) || p.X > float32(card.Max.X) {
			li, pos = i, ovPaneToWindow(p)
			break
		}
	}
	if li < 0 {
		t.Skip("全部节点都落在卡片盒内，本用例失去区分度")
	}
	ovClick(q, int(pos.X), int(pos.Y))
	histFrameFor(q, u, st)
	if st.ov == nil {
		t.Fatal("遮罩未开启")
	}
	if st.ov.card.Empty() {
		t.Fatal("卡片盒未记录")
	}
	// 开遮罩的那次点击不该触发关闭；再点一次卡片外才关。
	histFrameFor(q, u, st)
	if st.ov == nil {
		t.Fatal("遮罩被开启它的那次点击关掉了")
	}
	ovClick(q, 2, 2)
	histFrameFor(q, u, st)
	if st.ov != nil {
		t.Fatal("点遮罩空白应关闭遮罩")
	}
}

// TestOverlayBackgroundGraphInert 遮罩开启期右图手势完全失灵（不消费事件）：点节点不再
// 开新遮罩、拖空白不再平移视场。这是「模态」在 Gio 广播模型下的真实口径。
// 变异验证：去掉 `relGraph.frame` 里的 `overlayActive` 门 ⇒ 本用例红（遮罩被换节点 /
// 视场被平移）。
func TestOverlayBackgroundGraphInert(t *testing.T) {
	u, st, q := relGraphFixture(t)
	histOpenOverlay(t, u, st, ovUserID(st), conversation.Fresh, overlayPageRendered)
	histFrameFor(q, u, st)
	id := st.ov.id
	offX, offY := st.graph.offX, st.graph.offY

	// ① 点另一个**确实命中**的节点（原会开新遮罩）→ 遮罩内容不变。
	li := -1
	var sx f32.Point
	for i, gi := range st.graph.vis.nodes {
		x, y := st.graph.toScreen(st.graph.sol.Pos[gi])
		if st.graph.hitTest(x, y) == i && st.model.nodes[gi].id != id {
			li, sx = i, f32.Pt(x, y)
			break
		}
	}
	if li < 0 {
		t.Skip("夹具里没有另一个可命中的节点")
	}
	ovClick(q, int(sx.X)+200, int(sx.Y))
	histFrameFor(q, u, st)
	if st.ov.id != id {
		t.Fatalf("遮罩节点被背景点击换掉 = %q, want %q", st.ov.id, id)
	}

	// ② 拖空白平移视口 → 视场不动（空白点须确实无节点）。
	const bx, by = 10, 10
	if st.graph.hitTest(bx, by) != -1 {
		t.Skip("夹具角落不是空白")
	}
	q.Queue(relPointer(pointer.Press, ovPaneToWindow(f32.Pt(bx, by)), true))
	histFrameFor(q, u, st)
	moved := ovPaneToWindow(f32.Pt(bx+80, by+70))
	ev := relPointer(pointer.Move, moved, true)
	q.Queue(ev)
	histFrameFor(q, u, st)
	q.Queue(relPointer(pointer.Release, moved, false))
	histFrameFor(q, u, st)

	if st.graph.offX != offX || st.graph.offY != offY {
		t.Fatalf("遮罩开启期视场被平移: (%v,%v) → (%v,%v)", offX, offY, st.graph.offX, st.graph.offY)
	}
	if st.graph.pressed || st.graph.dragNode >= 0 {
		t.Fatalf("遮罩开启期右图仍在取手势: pressed=%v dragNode=%d", st.graph.pressed, st.graph.dragNode)
	}
}

// TestOverlayPagesPresentAndSource 两个页签：呈现页解析 markdown（只读预览）、源码页
// 预填原文且可编辑；解析按源码缓存（同一帧不重复解析）。
func TestOverlayPagesPresentAndSource(t *testing.T) {
	u, st, q := relGraphFixture(t)
	histOpenOverlay(t, u, st, ovUserID(st), conversation.Fresh, overlayPageRendered)
	histFrameFor(q, u, st)
	if len(st.ov.shown()) == 0 {
		t.Fatal("呈现页未解析出 markdown 块")
	}
	// 切到源码页：编辑器预填全文、可改（ReadOnly=false）。
	st.ov.page = overlayPageSource
	histFrameFor(q, u, st)
	if st.ov.ed.Text() != st.ov.src {
		t.Fatalf("源码页编辑器 = %q, want 原文 %q", st.ov.ed.Text(), st.ov.src)
	}
	if st.ov.ed.SingleLine || !st.ov.ed.Submit {
		t.Fatalf("源码页编辑器配置 = SingleLine %v Submit %v, want 多行 + Enter 提交",
			st.ov.ed.SingleLine, st.ov.ed.Submit)
	}
	st.ov.ed.SetText(st.ov.src + "\n追加")
	if st.ov.src != st.ov.src[:len(st.ov.src)] { // 源码快照与编辑内容解耦（提交读编辑器）
		t.Fatal("不应就地改写源码快照")
	}
	_ = q
}

// TestOverlayGotoMenuItem 图侧「在主窗定位」→ /goto 经 inCh + 唤出主窗（D139：`/goto`
// 由单击移来；顺带补唤出，否则主窗收在悬浮球里时点了没反应）。
func TestOverlayGotoMenuItem(t *testing.T) {
	u, st, q := relGraphFixture(t)
	const li = 4
	want := st.model.nodes[st.vis.nodes[li]].id
	q.Queue(relRightPress(st, li))
	q.Queue(relRightReleaseAt(st, li))
	relGFrame(q, u, st)
	u.menuChosen(platform.MenuBubble, cmdNodeGoto)

	act := u.nodeAction.Load()
	if act == nil || act.kind != nodeActGoto || act.id != want {
		t.Fatalf("动作槽 = %+v, want 在主窗定位 %s", act, want)
	}
	histFrameFor(q, u, st)
	select {
	case in := <-u.inCh:
		if in.Command == nil || in.Command.Name != "goto" ||
			len(in.Command.Args) != 1 || conversation.MessageID(in.Command.Args[0]) != want {
			t.Fatalf("inCh = %+v, want /goto %s", in, want)
		}
	default:
		t.Fatal("inCh 空：图侧定位未投 /goto")
	}
	var se showExpandMsg
	if !relDrainAs(t, u, &se) {
		t.Fatal("图侧定位未唤出主窗")
	}
}

// TestOverlayCardGeometryFitsViewport 卡片盒：居中、限宽 ≤560dp、高 ≤ 视口 70%。
func TestOverlayCardGeometryFitsViewport(t *testing.T) {
	_, _, _ = relGraphFixture(t)
	gtx, _ := frameGtxSize(input.Source{}, 720, 560)
	card := overlayCardBox(gtx)
	if card.Dx() > 560 || card.Dy() > int(560*overlayCardMaxHFrac) {
		t.Fatalf("卡片盒 = %dx%d, want ≤560 x ≤%d", card.Dx(), card.Dy(), int(560*overlayCardMaxHFrac))
	}
	if card.Min.X <= 0 || card.Min.Y <= 0 ||
		card.Max.X >= gtx.Constraints.Max.X || card.Max.Y >= gtx.Constraints.Max.Y {
		t.Fatalf("卡片盒 = %v，未居中留边（视口 %v）", card, gtx.Constraints.Max)
	}
}

// TestOverlayDimmedBackdropAndCard D139 的**观感**判据（离屏光栅化）：开启后背景被
// scrim 压暗（像素变暗），卡片区保持主题底色（不被压暗）——即「压暗一层 + 居中卡片」
// 真的画出来了，而不是只有状态位在变。
func TestOverlayDimmedBackdropAndCard(t *testing.T) {
	const viewW, viewH = 720, 560
	w, err := headless.NewWindow(viewW, viewH)
	if err != nil {
		t.Skipf("离屏渲染不可用（无 GPU/软件后端）: %v", err)
	}
	defer w.Release()

	u, st, q := relGraphFixture(t)
	render := func() *image.RGBA {
		gtx, ops := frameGtxSize(q.Source(), viewW, viewH)
		historyFrame(gtx, u.th, u, st)
		q.Frame(ops)
		if err := w.Frame(ops); err != nil {
			t.Fatalf("离屏渲染失败: %v", err)
		}
		img := image.NewRGBA(image.Rect(0, 0, viewW, viewH))
		if err := w.Screenshot(img); err != nil {
			t.Fatalf("截图失败: %v", err)
		}
		return img
	}
	base := render()
	histOpenOverlay(t, u, st, ovUserID(st), conversation.Fresh, overlayPageRendered)
	ov := render()
	card := st.ov.card

	lum := func(c color.RGBA) int { return int(c.R) + int(c.G) + int(c.B) }
	// ① 卡片外被 scrim 压暗。
	outside, outsideBase := ov.RGBAAt(2, 2), base.RGBAAt(2, 2)
	if lum(outside) >= lum(outsideBase) {
		t.Fatalf("scrim 未压暗背景: %v → %v", outsideBase, outside)
	}
	// ② 卡片内确有内容（标题/页签/正文渲染出像素差）——卡片不透明，中心可能与底色同色，
	// 故按「卡内变化像素数」判据，不逐点比亮度。
	diff := 0
	for y := card.Min.Y; y < card.Max.Y; y++ {
		for x := card.Min.X; x < card.Max.X; x++ {
			if ov.RGBAAt(x, y) != base.RGBAAt(x, y) {
				diff++
			}
		}
	}
	if diff < 100 {
		t.Fatalf("卡片内几乎没有内容绘制（变化像素 %d）——遮罩只画了底板？", diff)
	}
}

// TestOverlayBackgroundRowsInert 遮罩开启期**左栏会话行**不再响应（差分判据）：同一坐
// 标在无遮罩时点得动（有 /switch）、遮罩开启时点不动。左栏与状态栏是**同链**控件——
// 不像右图那样被遮罩的整窗输入区遮蔽，故这里的「不取事件」门是**必需**的。
// 变异验证：去掉 `historyState.dispatch` 的 `overlayActive` 门 ⇒ 本用例红。
func TestOverlayBackgroundRowsInert(t *testing.T) {
	// 本用例要**有会话行**可点：另起一个带左栏数据的夹具（关系图夹具的 Lister 为空）。
	texts := map[conversation.MessageID]string{}
	for _, n := range relFixtureGraph().Nodes {
		texts[n.ID] = relFixtureBody(n.ID)
	}
	lister := fakeLister{
		{ID: conversation.ID("C1"), Title: "甲", Messages: 3, Current: true},
		{ID: conversation.ID("C2"), Title: "乙", Messages: 5},
	}
	u := newHistUI(fakeRelTree{g: relFixtureGraph(), texts: texts}, lister)
	st := newHistoryState(u)
	q := new(input.Router)
	histFrameFor(q, u, st)

	// 自校准行位：扫一列 y 找**确实点得动**的会话行（行高与标题行高随主题字体变，写死坐
	// 标会脆）。找不到就跳过，而不是拿一个没点中的坐标冒充「无响应」。
	const rowX = 100
	rowY := -1
	for y := 24; y <= 200 && rowY < 0; y += 2 {
		ovClick(q, rowX, y)
		histFrameFor(q, u, st)
		select {
		case in := <-u.inCh:
			if in.Command != nil && in.Command.Name == "switch" {
				rowY = y
			}
		default:
		}
	}
	if rowY < 0 {
		t.Skip("未找到可点的会话行，本用例失去区分度")
	}

	histOpenOverlay(t, u, st, ovUserID(st), conversation.Fresh, overlayPageRendered)
	histFrameFor(q, u, st)
	ovClick(q, rowX, rowY)
	histFrameFor(q, u, st)
	select {
	case in := <-u.inCh:
		t.Fatalf("遮罩开启期左栏仍响应（y=%d）: %+v", rowY, in)
	default:
	}
}

// ovPointer 遮罩用例的鼠标事件（坐标取整像素）。
func ovPointer(kind pointer.Kind, x, y int) pointer.Event {
	return pointer.Event{Kind: kind, Position: f32.Pt(float32(x), float32(y)),
		PointerID: 9, Source: pointer.Mouse}
}

// ovClick 一次完整点击（Move → Press → Release）：Gio 的抬起只投给**已进入窗口**的
// 命中集，只排队 Release 收不到任何 handler。
func ovClick(q *input.Router, x, y int) {
	e := ovPointer(pointer.Move, x, y)
	q.Queue(e)
	e = ovPointer(pointer.Press, x, y)
	e.Buttons = pointer.ButtonPrimary
	q.Queue(e)
	q.Queue(ovPointer(pointer.Release, x, y))
}
