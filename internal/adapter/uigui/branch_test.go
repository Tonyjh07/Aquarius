package uigui

import (
	"image"
	"testing"

	"gioui.org/f32"
	"gioui.org/io/input"
	"gioui.org/io/pointer"

	"github.com/Tonyjh07/Aquarius/internal/domain/conversation"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

// fakeTree 固定分支视图（测试替身，D80 port.TreeView）。
type fakeTree map[conversation.MessageID]port.BranchInfo

func (f fakeTree) Branches(id conversation.MessageID) (port.BranchInfo, bool) {
	bi, ok := f[id]
	return bi, ok
}

// newBranchUI 带树视图与输入通道的最小 UI（headless，同 newFrameUI 口径）。
func newBranchUI(tree port.TreeView) *UI {
	u := newFrameUI()
	u.opts.Tree = tree
	u.inCh = make(chan port.UserInput, inputCap)
	return u
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
	full := &UI{inCh: make(chan port.UserInput)}
	full.m = newModel(full)
	full.gotoBranch(branchStrip{id: "n2", ids: ids, index: 1}, +1)
	if len(full.m.blocks) != 1 || full.m.blocks[0].kind != blockNotice {
		t.Fatalf("缓冲满 blocks = %+v, want 1 条 notice", full.m.blocks)
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
