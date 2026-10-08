package uigui

import (
	"image"
	"testing"

	"gioui.org/f32"
	"gioui.org/io/input"
	"gioui.org/io/pointer"

	"github.com/Tonyjh07/Aquarius/internal/domain/conversation"
	"github.com/Tonyjh07/Aquarius/internal/domain/tool"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

// fakeTree 固定分支视图（测试替身，D80 port.TreeView）。
type fakeTree map[conversation.MessageID]port.BranchInfo

func (f fakeTree) Branches(id conversation.MessageID) (port.BranchInfo, bool) {
	bi, ok := f[id]
	return bi, ok
}

// Tail 版本末端（D94）：固定视图未配置末端表 → 即自身（既有分派测试口径不变）。
func (f fakeTree) Tail(id conversation.MessageID) (conversation.MessageID, bool) {
	return id, true
}

// Graph 整树快照（D112②/D120⑦）：固定视图不配置图 → 未发布（S3 左栏/关系图另有专项替身）。
func (f fakeTree) Graph() (port.TreeGraph, bool) {
	return port.TreeGraph{}, false
}

// fakeTreeTail 带末端表的树视图（D94 分派测试用）：tails 未命中的 id 回退自身。
type fakeTreeTail struct {
	fakeTree
	tails map[conversation.MessageID]conversation.MessageID
}

func (f fakeTreeTail) Tail(id conversation.MessageID) (conversation.MessageID, bool) {
	if t, ok := f.tails[id]; ok {
		return t, ok
	}
	return id, true
}

// newBranchUI 带树视图与输入通道的最小 UI（headless，同 newFrameUI 口径）。
func newBranchUI(tree port.TreeView) *UI {
	u := newFrameUI()
	u.opts.Tree = tree
	u.inCh = make(chan port.UserInput, inputCap)
	return u
}

// TestCommitStampsUserEcho D87 复现：实时路径 submit 回显的 user 块没有节点 ID
// （分叉条跳过空 ID 块 → /goto 后再发消息、分叉已建但按钮不出现），user 节点
// commit 时应按「最早未盖章且文本一致」回填 ID，分叉条随即出现。
func TestCommitStampsUserEcho(t *testing.T) {
	// 新消息节点 n2 与旧分支 a_old 是同级（树里孩子数 2，用户实测场景）。
	u := newBranchUI(fakeTree{
		"n2": {IDs: []conversation.MessageID{"a_old", "n2"}, Index: 1},
	})
	u.m.submit("再来一条")
	if b := u.m.blocks[0]; b.kind != blockUser || b.id != "" {
		t.Fatalf("block = %+v, want 未盖章 user 回显块", b)
	}
	u.m.commit(conversation.Message{
		ID:      "n2",
		Role:    conversation.RoleUser,
		Content: []conversation.Part{{Kind: conversation.PartText, Text: "再来一条"}},
	})
	if b := u.m.blocks[0]; b.id != "n2" {
		t.Fatalf("回显块 id = %q, want n2（commit 应回填）", b.id)
	}
	strips := u.branchStrips()
	if len(strips) != 1 || strips[0].id != "n2" || strips[0].index != 1 || !strips[0].right {
		t.Fatalf("strips = %+v, want n2 的右对齐分叉条 index=1", strips)
	}
}

// TestCommitStampMatching D87 匹配规则：重名文本按 FIFO 对齐；文本不一致不盖章；
// 命令行回显（永不为节点文本）不被错盖。
func TestCommitStampMatching(t *testing.T) {
	u := newBranchUI(fakeTree{})
	m := u.m
	m.submit("hi")
	m.submit("hi")
	m.submit("/goto x") // 排队命令：回显但永不成为节点
	m.commit(conversation.Message{ID: "n1", Role: conversation.RoleUser,
		Content: []conversation.Part{{Kind: conversation.PartText, Text: "hi"}}})
	m.commit(conversation.Message{ID: "n2", Role: conversation.RoleUser,
		Content: []conversation.Part{{Kind: conversation.PartText, Text: "hi"}}})
	if got := [3]conversation.MessageID{m.blocks[0].id, m.blocks[1].id, m.blocks[2].id}; got != [3]conversation.MessageID{"n1", "n2", ""} {
		t.Fatalf("ids = %v, want [n1 n2 ]（FIFO 对齐，命令回显不盖章）", got)
	}
	m.commit(conversation.Message{ID: "n3", Role: conversation.RoleUser,
		Content: []conversation.Part{{Kind: conversation.PartText, Text: "别的"}}})
	if m.blocks[2].id != "" {
		t.Fatalf("命令回显被错盖: %q", m.blocks[2].id)
	}
}

// TestReplayToolTurnAnchorsFork D89 复现：纯工具轮 assistant 节点（只有 thinking +
// tool_calls、无正文——模型径直发起调用）回放时 thinking 卡与 chip 都不携节点 ID、
// 空正文不产生 assistant 块 → 转写里没有任何带 ID 的块 → 分叉条无处可挂，
// 「切到该分支后切不回」（S1-1f 用户实测）。锚点应回填到本轮 chip/思考块上。
func TestReplayToolTurnAnchorsFork(t *testing.T) {
	u := newBranchUI(fakeTree{
		"a1": {IDs: []conversation.MessageID{"a1", "u2"}, Index: 0},
	})
	u.m.clear()
	u.m.replay(conversation.Message{ID: "u1", Role: conversation.RoleUser,
		Content: []conversation.Part{{Kind: conversation.PartText, Text: "whoami"}}})
	// D95：调用声明在节点工具分片里。
	u.m.replay(conversation.Message{
		ID:   "a1",
		Role: conversation.RoleAssistant,
		Content: []conversation.Part{
			{Kind: conversation.PartThinking, Text: "Just run it."},
			{Kind: conversation.PartTool, Tool: &conversation.ToolPart{
				CallID: "call1", Name: "term_exec", Args: []byte(`{"command":"whoami"}`),
			}},
		},
		Outcome: conversation.OutcomeDone,
	})
	found := false
	for _, b := range u.m.blocks {
		if b.id == "a1" {
			found = true
		}
	}
	if !found {
		t.Fatal("纯工具轮回放后没有任何块携带节点 ID（分叉条无处可挂）")
	}
	strips := u.branchStrips()
	if len(strips) != 1 || strips[0].id != "a1" || strips[0].index != 0 || len(strips[0].ids) != 2 {
		t.Fatalf("strips = %+v, want a1 的分叉条 index=0 n=2", strips)
	}
}

// TestCommitToolTurnAnchorsChip D89 live 路径：本轮 chip 由 ToolCallEvent 先行入块，
// CommittedEvent 到达时正文为空 → chip 块应按调用 ID 回填节点 ID。
func TestCommitToolTurnAnchorsChip(t *testing.T) {
	u := newBranchUI(fakeTree{
		"a1": {IDs: []conversation.MessageID{"a1", "u2"}, Index: 0},
	})
	m := u.m
	m.handleEvent(port.ToolCallEvent{Call: tool.Call{ID: "call1", Name: "term_exec", Args: []byte(`{}`)}})
	m.commit(conversation.Message{
		ID:   "a1",
		Role: conversation.RoleAssistant,
		Content: []conversation.Part{{Kind: conversation.PartTool, Tool: &conversation.ToolPart{
			CallID: "call1", Name: "term_exec",
		}}},
		Outcome: conversation.OutcomeDone,
	})
	found := false
	for _, b := range m.blocks {
		if b.kind == blockTool && b.chip != nil && b.id == "a1" {
			found = true
		}
	}
	if !found {
		t.Fatal("纯工具轮 commit 后 chip 块未携带节点 ID")
	}
	if strips := u.branchStrips(); len(strips) != 1 || strips[0].id != "a1" {
		t.Fatalf("strips = %+v, want a1 锚在 chip 上的分叉条", strips)
	}
}

// TestBranchStrips 分叉条生成（D81）：仅「带节点 ID 且同级 ≥2」的正文块生成一条；
// 同级唯一的节点、无 ID 的块（notice/思考/命令输出）都不生成；对齐随气泡。
func TestBranchStrips(t *testing.T) {
	u := newBranchUI(fakeTree{
		"u1": {IDs: []conversation.MessageID{"u1"}, Index: 0},             // 独子 → 不生成
		"a1": {IDs: []conversation.MessageID{"a1", "a2", "a3"}, Index: 1}, // 三分叉 → 生成
	})
	u.m.addMsg(blockUser, "问（独子）", "u1")
	u.m.addMsg(blockAssistant, "答（三分叉）", "a1")
	u.m.add(blockNotice, "提示（无节点）")

	strips := u.branchStrips()
	if len(strips) != 1 {
		t.Fatalf("strips = %d, want 1（仅 a1 块有同级分叉）", len(strips))
	}
	s := strips[0]
	if s.blockIdx != 1 || s.id != "a1" || s.index != 1 || len(s.ids) != 3 {
		t.Fatalf("strip = %+v, want blockIdx=1 id=a1 index=1 n=3", s)
	}
	if s.right {
		t.Fatal("assistant 气泡左对齐 → 分叉条也应左对齐")
	}
	if s.slot != 0 {
		t.Fatalf("slot = %d, want 0", s.slot)
	}
}

// TestBranchStripsAlignWithUserBubble user 气泡右对齐 → 其分叉条同样右对齐。
func TestBranchStripsAlignWithUserBubble(t *testing.T) {
	u := newBranchUI(fakeTree{"u1": {IDs: []conversation.MessageID{"u1", "u2"}, Index: 1}})
	u.m.addMsg(blockUser, "问", "u1")
	strips := u.branchStrips()
	if len(strips) != 1 || !strips[0].right {
		t.Fatalf("strips = %+v, want 1 条且右对齐", strips)
	}
}

// TestBranchStripsNoTree Tree 未注入（nil）→ 不生成任何分叉条（默认安全）。
func TestBranchStripsNoTree(t *testing.T) {
	u := newFrameUI()
	u.m.addMsg(blockUser, "问", "u1")
	if got := u.branchStrips(); got != nil {
		t.Fatalf("strips = %+v, want nil", got)
	}
}

// TestFrameItemsInsertBranch 分叉条紧跟其所属正文块插入，且**不占行选键**
// （selCount=0）——分叉条增删不漂移其它行的选择序号（D63/D66 键稳定性）。
func TestFrameItemsInsertBranch(t *testing.T) {
	u := newBranchUI(fakeTree{"a1": {IDs: []conversation.MessageID{"a1", "a2"}, Index: 0}})
	u.m.addMsg(blockUser, "问", "")
	u.m.addMsg(blockAssistant, "答", "a1")

	items := u.frameItems()
	if len(items) != 3 {
		t.Fatalf("items = %d, want 3（问/答/分叉条）", len(items))
	}
	if items[1].kind != blockAssistant || items[2].kind != blockBranch || items[2].branch == nil {
		t.Fatalf("items[1..2] = %+v / %+v, want assistant 后跟分叉条", items[1], items[2])
	}
	if items[2].branch.id != "a1" {
		t.Fatalf("分叉条所源节点 = %s, want a1", items[2].branch.id)
	}
	if got := items[2].selCount(); got != 0 {
		t.Fatalf("分叉条 selCount = %d, want 0（不占行选键）", got)
	}

	// 行选键总数 = 问 1 + 答 1 + 分叉条 0 = 2（与不插分叉条时一致）。
	gtx, _ := frameGtx(input.Source{})
	u.transcript(gtx, 600, 400)
	if len(u.selRows) != 2 {
		t.Fatalf("selRows = %d, want 2（分叉条不占键）", len(u.selRows))
	}
}

// TestGotoBranchDispatch 切换投递（D81）：以 `/goto <兄弟id>` 经输入通道走内核命令
// 路径（壳内不旁路）；边界不环绕；输入缓冲满 → 提示而非静默丢弃。
func TestGotoBranchDispatch(t *testing.T) {
	u := newBranchUI(nil)
	ids := []conversation.MessageID{"n1", "n2", "n3"}

	// 中间节点 → 左右都能切。
	s := branchStrip{id: "n2", ids: ids, index: 1}
	u.gotoBranch(s, -1)
	if in := <-u.inCh; in.Command == nil || in.Command.Name != "goto" ||
		len(in.Command.Args) != 1 || in.Command.Args[0] != "n1" {
		t.Fatalf("左切投递 = %+v, want /goto n1", in)
	}
	u.gotoBranch(s, +1)
	if in := <-u.inCh; in.Command == nil || len(in.Command.Args) != 1 || in.Command.Args[0] != "n3" {
		t.Fatalf("右切投递 = %+v, want /goto n3", in)
	}

	// 边界不环绕：首节点左切、末节点右切都是 no-op（不投递、不入块）。
	u.gotoBranch(branchStrip{id: "n1", ids: ids, index: 0}, -1)
	u.gotoBranch(branchStrip{id: "n3", ids: ids, index: 2}, +1)
	select {
	case in := <-u.inCh:
		t.Fatalf("边界误投递 %+v", in)
	default:
	}
	if len(u.m.blocks) != 0 {
		t.Fatalf("边界切换入块 = %+v, want 无", u.m.blocks)
	}

	// 缓冲满：给提示而非静默丢弃。
	full := &UI{plat: newFakePlat(), inCh: make(chan port.UserInput)}
	full.m = newModel(full)
	full.gotoBranch(branchStrip{id: "n2", ids: ids, index: 1}, +1)
	if len(full.m.blocks) != 1 || full.m.blocks[0].kind != blockNotice {
		t.Fatalf("缓冲满 blocks = %+v, want 1 条 notice", full.m.blocks)
	}
}

// TestGotoBranchDispatchTail D94：切换落点 = 目标版本的对话末端（port.TreeView.Tail）
// ——/goto 停在消息节点上时其回答不在 Head 路径上，切用户消息版本会只剩半截；
// 端口不可用回退兄弟 id（上方用例 nil 树即此口径）。
func TestGotoBranchDispatchTail(t *testing.T) {
	u := newBranchUI(fakeTreeTail{
		fakeTree: fakeTree{},
		tails:    map[conversation.MessageID]conversation.MessageID{"n1": "old_leaf"},
	})
	ids := []conversation.MessageID{"n1", "n2", "n3"}
	s := branchStrip{id: "n2", ids: ids, index: 1}

	u.gotoBranch(s, -1) // 左切到 n1 → 末端 old_leaf
	if in := <-u.inCh; in.Command == nil || in.Command.Args[0] != "old_leaf" {
		t.Fatalf("左切投递 = %+v, want /goto old_leaf", in)
	}
	u.gotoBranch(s, +1) // 右切到 n3 → 末端表未命中 → 回退自身
	if in := <-u.inCh; in.Command == nil || in.Command.Args[0] != "n3" {
		t.Fatalf("右切投递 = %+v, want /goto n3", in)
	}
}

// TestBranchClickDispatches 点击链路（headless 帧，§15.5）：
//   - 几何：分叉条在视口内登记形状（D62 逐像素命中的前提），且指针移入右箭头位置
//     时该点击件确为 hover 态（命中区随记录宏在 paintRow 重放后落位正确）；
//   - 分发：点击件命中 → 投递 `/goto <兄弟>` 到输入通道（与键入同路径）。
//
// 指针路由与 Clickable 的帧序在 Gio 无窗口 Router 下不稳定，故"按下-抬起"用
// Clickable 的程序化点击表达（同一 Clicked 通路）；几何由 hover 独立证明。
func TestBranchClickDispatches(t *testing.T) {
	u := newBranchUI(fakeTree{"a1": {IDs: []conversation.MessageID{"a1", "a2"}, Index: 0}})
	u.m.addMsg(blockUser, "问", "")
	u.m.addMsg(blockAssistant, "答", "a1")

	q := new(input.Router)
	frame := func() {
		gtx, ops := frameGtx(q.Source())
		u.layout(gtx)
		q.Frame(ops) // 提交 hit 树与过滤器（Gio 也在 render 之后、Present 之前做这一步）
	}
	frame() // 登记形状与过滤器

	// 分叉条 = 底板为 cardTool 的那条形状（转写行里只有分叉条用该色）。
	var strip image.Rectangle
	found := false
	for _, s := range u.shapes {
		if s.fill == cardTool {
			strip, found = s.outline, true
		}
	}
	if !found {
		t.Fatalf("分叉条未登记形状：shapes = %d 条", len(u.shapes))
	}
	if len(u.branchNext) != 1 || len(u.branchPrev) != 1 {
		t.Fatalf("点击件未就绪：prev=%d next=%d", len(u.branchPrev), len(u.branchNext))
	}

	// 几何：指针移到右箭头（内容盒最右端，条内左右各留 cardPadXDp）→ 该点击件 hover。
	right := f32.Pt(float32(strip.Max.X-cardPadXDp-4), float32((strip.Min.Y+strip.Max.Y)/2))
	q.Queue(pointer.Event{Kind: pointer.Move, Position: right, PointerID: 9})
	frame()
	if !u.branchNext[0].Hovered() {
		t.Fatalf("右箭头位置 %v 未命中：点击件不可达（转写区未按预期登记命中区？）", right)
	}
	if u.branchPrev[0].Hovered() {
		t.Fatal("左箭头不应在右箭头位置被命中")
	}

	// 分发：点击 → `/goto a2`。
	u.branchNext[0].Click()
	frame()
	select {
	case in := <-u.inCh:
		if in.Command == nil || in.Command.Name != "goto" ||
			len(in.Command.Args) != 1 || in.Command.Args[0] != "a2" {
			t.Fatalf("点击右箭头投递 = %+v, want /goto a2", in)
		}
	default:
		t.Fatal("点击右箭头未投递任何输入")
	}
}
