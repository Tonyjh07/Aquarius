package uigui

import (
	"image"
	"slices"
	"testing"

	"gioui.org/io/input"

	"github.com/Tonyjh07/Aquarius/internal/domain/conversation"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

// editAttsFixture 两条已有附件的快照（序号 = 节点内出现序）。
func editAttsFixture() []port.AttachmentView {
	return []port.AttachmentView{
		{Index: 0, Name: "报表.pdf", MIME: "application/pdf", Size: 1200},
		{Index: 1, Name: "截图.png", MIME: "image/png", Size: 3400},
	}
}

// TestEditAttachChipItems D188：编辑态 chip 排 = 已有附件（按序号）+ 新暂存项；
// 非编辑态只摆暂存项；标记删除只改渲染态，不动列表。
func TestEditAttachChipItems(t *testing.T) {
	u := newEditUI()
	u.m.stageAttach(`C:\x\draft.md`)
	if items := u.chipItems(); len(items) != 1 || items[0].label != "draft.md" {
		t.Fatalf("非编辑态 chipItems = %+v, want 仅暂存项", items)
	}
	u.m.clearAttach() // 起草稿的暂存清掉，免得混进编辑态断言

	u.apply(editMsg{id: "u1", text: "旧问题", part: -1, atts: editAttsFixture()})
	u.m.stageAttach(`C:\x\new.md`)
	items := u.chipItems()
	if len(items) != 3 {
		t.Fatalf("chipItems = %+v, want 已有 2 + 新增 1", items)
	}
	for i, want := range []string{"报表.pdf", "截图.png", "new.md"} {
		if items[i].label != want {
			t.Fatalf("items[%d] = %q, want %q（已有在前、新增在后）", i, items[i].label, want)
		}
		if items[i].dropped {
			t.Fatalf("items[%d] 未点击就被标成删除", i)
		}
	}

	u.m.toggleEditAtt(1)
	items = u.chipItems()
	if !items[1].dropped || items[0].dropped || items[2].dropped {
		t.Fatalf("标记删除应只作用于第 1 项: %+v", items)
	}
	if len(u.m.editAtts) != 2 || len(u.m.stagedFiles) != 1 {
		t.Fatalf("标记删除不应改列表: atts=%+v staged=%+v", u.m.editAtts, u.m.stagedFiles)
	}
	u.m.toggleEditAtt(1)
	if u.chipItems()[1].dropped {
		t.Fatal("再次点击应恢复")
	}
}

// TestEditAttachDropOnly D188 口径五：删掉已有附件、正文留空 → 只发 --drop，不夹带文本。
func TestEditAttachDropOnly(t *testing.T) {
	u := newEditUI()
	u.apply(editMsg{id: "u1", text: "旧问题", part: -1, atts: editAttsFixture()})
	u.m.toggleEditAtt(1) // 逆序点，验证 Args 里按序号升序
	u.m.toggleEditAtt(0)
	u.editor.SetText("")
	u.submitEditor()

	select {
	case in := <-u.inCh:
		want := []string{"u1", "--drop", "0", "--drop", "1"}
		if in.Command == nil || !slices.Equal(in.Command.Args, want) {
			t.Fatalf("inCh = %+v, want Args %v", in, want)
		}
		if len(in.Command.Attachments) != 0 {
			t.Fatalf("没有新增附件却带了载荷: %+v", in.Command.Attachments)
		}
	default:
		t.Fatal("只改附件也应投递 /edit（空文本不等于取消）")
	}
	if u.m.editTarget != "" || u.m.editDropped != nil || len(u.m.editAtts) != 0 {
		t.Fatalf("提交后编辑态未收尾: target=%q dropped=%v atts=%+v",
			u.m.editTarget, u.m.editDropped, u.m.editAtts)
	}
}

// TestEditAttachDropAndText D188：正文改写 + 删一项 + 路径新增 → 同一条 /edit 带三类 token，
// 新增附件按出现序排在 flag 区（文本最后）。
func TestEditAttachDropAndText(t *testing.T) {
	u := newEditUI()
	u.apply(editMsg{id: "u1", text: "旧问题", part: -1, atts: editAttsFixture()})
	u.m.toggleEditAtt(0)
	u.m.stageAttach(`C:\x\新报表.pdf`)
	u.editor.SetText("改过的问题")
	u.submitEditor()

	select {
	case in := <-u.inCh:
		if in.Command == nil {
			t.Fatalf("inCh = %+v", in)
		}
		if want := []string{"u1", "--drop", "0", "改过的问题"}; !slices.Equal(in.Command.Args, want) {
			t.Fatalf("Args = %v, want %v", in.Command.Args, want)
		}
		if len(in.Command.Attachments) != 1 || in.Command.Attachments[0].Path != `C:\x\新报表.pdf` {
			t.Fatalf("Attachments = %+v, want 一项路径附件", in.Command.Attachments)
		}
	default:
		t.Fatal("inCh 未收到")
	}
}

// TestEditAttachBlobPayload D188：新增附件一律走 Command.Attachments——粘贴图片没有路径，
// 选文件有路径，两者都进同一列表（顺序即 chip 排顺序）；纯新增、正文留空也能提交。
func TestEditAttachBlobPayload(t *testing.T) {
	u := newEditUI()
	u.apply(editMsg{id: "u1", text: "旧问题", part: -1, atts: editAttsFixture()})
	u.m.stageBlob(conversation.BlobRef{Hash: "h1", Name: "粘贴.png", MIME: "image/png", Size: 9})
	u.m.stageAttach(`C:\x\后选的.pdf`)
	u.editor.SetText("")
	u.submitEditor()

	select {
	case in := <-u.inCh:
		if in.Command == nil {
			t.Fatalf("inCh = %+v", in)
		}
		if want := []string{"u1"}; !slices.Equal(in.Command.Args, want) {
			t.Fatalf("Args = %v, want %v（新增附件不进 flag 区、无文本不补空串）", in.Command.Args, want)
		}
		if len(in.Command.Attachments) != 2 {
			t.Fatalf("Attachments = %+v, want 2 项", in.Command.Attachments)
		}
		if in.Command.Attachments[0].Blob == nil || in.Command.Attachments[0].Path != "" {
			t.Fatalf("[0] = %+v, want 已入库引用（无路径）", in.Command.Attachments[0])
		}
		if in.Command.Attachments[1].Path != `C:\x\后选的.pdf` {
			t.Fatalf("[1] = %+v, want 路径附件且保持 chip 序", in.Command.Attachments[1])
		}
	default:
		t.Fatal("inCh 未收到")
	}
}

// TestEditAttachEmptySubmitCancels D92 旧口径保留：编辑态既没改文本也没动附件 → 空提交
// 仍是取消，不投递。
func TestEditAttachEmptySubmitCancels(t *testing.T) {
	u := newEditUI()
	u.apply(editMsg{id: "u1", text: "旧问题", part: -1, atts: editAttsFixture()})
	u.editor.SetText("")
	u.submitEditor()
	select {
	case in := <-u.inCh:
		t.Fatalf("空提交不应投递: %+v", in)
	default:
	}
	if u.m.editTarget != "" {
		t.Fatalf("空提交应退出编辑态: target=%q", u.m.editTarget)
	}
}

// TestEditAttachCancelKeepsStaged D188：Esc 退出编辑态清掉已有附件快照与删除标记，
// 但这次为修订新选的暂存附件保留（用户已经花过力气挑文件）。
func TestEditAttachCancelKeepsStaged(t *testing.T) {
	u := newEditUI()
	u.apply(editMsg{id: "u1", text: "旧问题", part: -1, atts: editAttsFixture()})
	u.m.toggleEditAtt(0)
	u.m.stageAttach(`C:\x\new.md`)

	u.cancelEdit()
	if u.m.editTarget != "" || u.m.editAtts != nil || u.m.editDropped != nil {
		t.Fatalf("取消未清编辑态: target=%q atts=%+v dropped=%v",
			u.m.editTarget, u.m.editAtts, u.m.editDropped)
	}
	if n := len(u.m.stagedFiles); n != 1 || u.m.stagedFiles[0].path != `C:\x\new.md` {
		t.Fatalf("暂存应保留: %+v", u.m.stagedFiles)
	}
}

// TestEditAttachDropAllViaClear D186 汇总 chip 的「清空全部」在编辑态 = 已有附件全标记删除
// + 清空暂存（提交时仍是 --drop 序列，不动附件库）。
func TestEditAttachDropAllViaClear(t *testing.T) {
	u := newEditUI()
	u.apply(editMsg{id: "u1", text: "旧问题", part: -1, atts: editAttsFixture()})
	u.m.stageAttach(`C:\x\new.md`)
	u.m.clearAttach()
	if len(u.m.stagedFiles) != 0 {
		t.Fatalf("暂存未清: %+v", u.m.stagedFiles)
	}
	if got := u.m.editDroppedIndexes(); !slices.Equal(got, []int{0, 1}) {
		t.Fatalf("已有附件未全标记删除: %v", got)
	}
}

// TestEditAttachBufferFull D188：投递缓冲满时编辑态照常收尾（不把用户卡在半截状态），
// 但这次挑的附件退回暂存（挑文件比打字贵，不跟着丢）。
func TestEditAttachBufferFull(t *testing.T) {
	u := &UI{plat: newFakePlat(), followTail: true, inCh: make(chan port.UserInput, 1)}
	u.m = newModel(u)
	u.th = testTheme()
	u.editor.Submit = true
	u.editor.SingleLine = true

	u.apply(editMsg{id: "u1", text: "旧问题", part: -1, atts: editAttsFixture()})
	u.m.toggleEditAtt(0)
	u.m.stageAttach(`C:\x\new.md`)
	u.m.submitCommand("/branch") // 占满缓冲（容量 1）
	u.editor.SetText("")
	u.submitEditor()

	if u.m.editTarget != "" || u.m.editDropped != nil {
		t.Fatalf("缓冲满也应退出编辑态: target=%q dropped=%v", u.m.editTarget, u.m.editDropped)
	}
	if n := len(u.m.stagedFiles); n != 1 || u.m.stagedFiles[0].path != `C:\x\new.md` {
		t.Fatalf("暂存应退回: %+v", u.m.stagedFiles)
	}
	if n := len(u.m.blocks); n == 0 || u.m.blocks[n-1].kind != blockNotice {
		t.Fatalf("末块 = %+v, want notice", u.m.blocks)
	}
}

// TestEditAttachFrameRenders D188：编辑态 chip 排（含删除态）走一遍完整帧布局不炸——
// 尺寸量测、删除线、换行都与暂存 chip 共用同一条渲染路径。
func TestEditAttachFrameRenders(t *testing.T) {
	u := newEditUI()
	u.frameSize = image.Pt(winWidthDp, winHeightDp)
	q := new(input.Router)
	u.apply(editMsg{id: "u1", text: "旧问题", part: -1, atts: editAttsFixture()})
	u.m.toggleEditAtt(1)
	u.m.stageAttach(`C:\x\` + "很长的附件名字很长的附件名字.md")
	complFrame(q, u)
	gtx, _ := frameGtx(q.Source())
	if rows := u.attachRows(gtx, u.attachFlowMaxW(gtx)); len(rows) == 0 {
		t.Fatal("编辑态应有 chip 行")
	}
}
