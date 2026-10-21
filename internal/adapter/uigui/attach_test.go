package uigui

import (
	"image"
	"slices"
	"strings"
	"testing"

	"gioui.org/io/input"

	"github.com/Tonyjh07/Aquarius/internal/domain/conversation"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

// TestAttachStageFlow D186 多附件：选择结果暂存 → 随文提交 Raw{Attachments} → 暂存清空；
// 空文本 = 仅附件；编辑态不消费暂存；逐项移除与清空；chip 渲染不炸帧。
func TestAttachStageFlow(t *testing.T) {
	u := newEditUI() // inCh 可断言（SingleLine 编辑器对齐生产）
	q := new(input.Router)
	complFrame(q, u)

	// 暂存（attachMsg 应用）→ 随文提交。
	u.apply(attachMsg{paths: []string{`C:\docs\报告.pdf`}})
	if n := len(u.m.stagedFiles); n != 1 || u.m.stagedFiles[0].path != `C:\docs\报告.pdf` {
		t.Fatalf("stagedFiles = %+v", u.m.stagedFiles)
	}
	complFrame(q, u) // chip 呈现帧（渲染路径冒烟）
	u.editor.SetText("总结这个文件")
	u.submitEditor()
	select {
	case in := <-u.inCh:
		if in.Raw == nil || in.Raw.Kind != "file" || len(in.Raw.Attachments) != 1 ||
			in.Raw.Attachments[0].Path != `C:\docs\报告.pdf` || in.Raw.Text != "总结这个文件" {
			t.Fatalf("inCh = %+v, want Raw{attachments, 随文}", in)
		}
	default:
		t.Fatal("inCh 未收到 Raw 提交")
	}
	if len(u.m.stagedFiles) != 0 || u.editor.Text() != "" {
		t.Fatalf("提交后未清空: staged=%+v editor=%q", u.m.stagedFiles, u.editor.Text())
	}
	if n := len(u.m.blocks); n != 1 || u.m.blocks[n-1].text != "总结这个文件 〔附件：报告.pdf〕" {
		t.Fatalf("回显块 = %+v", u.m.blocks)
	}

	// 空文本 = 仅附件；两个附件走同一条 Raw。
	u.apply(attachMsg{paths: []string{`C:\docs\a.png`, `C:\docs\b.txt`}})
	complFrame(q, u)
	u.submitEditor()
	select {
	case in := <-u.inCh:
		if in.Raw == nil || in.Raw.Text != "" || len(in.Raw.Attachments) != 2 ||
			in.Raw.Attachments[0].Path != `C:\docs\a.png` || in.Raw.Attachments[1].Path != `C:\docs\b.txt` {
			t.Fatalf("inCh = %+v, want 两项仅附件", in)
		}
	default:
		t.Fatal("inCh 未收到仅附件提交")
	}
	if n := len(u.m.blocks); n == 0 || !strings.Contains(u.m.blocks[n-1].text, "附件 2 个") {
		t.Fatalf("多项回显 = %+v", u.m.blocks)
	}

	// 编辑态消费暂存（D188 作废 D104⑤ 的「编辑态不消费」）：新增附件随 /edit 走
	// Command.Attachments（顺序即 chip 排顺序），无文本的已有附件用 --drop 表达（下一段覆盖）。
	u.apply(editMsg{id: "u1", text: "旧问题", part: -1})
	u.apply(attachMsg{paths: []string{`C:\x\c.md`}})
	u.editor.SetText("新文本")
	u.submitEditor()
	select {
	case in := <-u.inCh:
		if in.Command == nil || in.Command.Name != "edit" {
			t.Fatalf("编辑态应走结构化 /edit, got %+v", in)
		}
		if want := []string{"u1", "新文本"}; !slices.Equal(in.Command.Args, want) {
			t.Fatalf("Args = %v, want %v", in.Command.Args, want)
		}
		if len(in.Command.Attachments) != 1 || in.Command.Attachments[0].Path != `C:\x\c.md` {
			t.Fatalf("Attachments = %+v, want 一项路径附件", in.Command.Attachments)
		}
	default:
		t.Fatal("inCh 未收到")
	}
	if n := len(u.m.stagedFiles); n != 0 {
		t.Fatalf("编辑态提交后应清空暂存: %+v", u.m.stagedFiles)
	}

	// 逐项移除（展开态 chip 点击同路径）：暂两项，删第一项。
	u.apply(attachMsg{paths: []string{`C:\x\d.txt`, `C:\x\e.txt`}})
	u.m.unstageAttach(0)
	if n := len(u.m.stagedFiles); n != 1 || u.m.stagedFiles[0].path != `C:\x\e.txt` {
		t.Fatalf("移除第 0 项后 = %+v", u.m.stagedFiles)
	}
	u.m.clearAttach()
	if len(u.m.stagedFiles) != 0 {
		t.Fatal("未清空暂存")
	}
}

// TestAttachStageLimits D186 口径三：同一文件不重复暂存；超过单条上限后拒收并提示。
func TestAttachStageLimits(t *testing.T) {
	u := newEditUI()

	u.m.stageAttach(`C:\x\a.txt`)
	u.m.stageAttach(`C:\x\A.TXT`) // 大小写不同 = 同一路径
	if n := len(u.m.stagedFiles); n != 1 {
		t.Fatalf("重复路径应去重: %+v", u.m.stagedFiles)
	}

	for i := 1; i < port.MaxAttachments; i++ {
		u.m.stageAttach(`C:\x\` + string(rune('a'+i)) + ".txt")
	}
	if n := len(u.m.stagedFiles); n != port.MaxAttachments {
		t.Fatalf("stagedFiles = %d, want %d", n, port.MaxAttachments)
	}
	u.m.stageAttach(`C:\x\overflow.txt`)
	if n := len(u.m.stagedFiles); n != port.MaxAttachments {
		t.Fatalf("超上限后仍加了: %d", n)
	}
	if n := len(u.m.blocks); n == 0 || u.m.blocks[n-1].kind != blockNotice {
		t.Fatalf("超上限应提示: %+v", u.m.blocks)
	}
}

// TestAttachBufferFull D104 后果③：缓冲满投递失败 → 保留暂存 + notice，不制造已发送错觉。
func TestAttachBufferFull(t *testing.T) {
	u := &UI{plat: newFakePlat(), followTail: true, inCh: make(chan port.UserInput, 1)}
	u.m = newModel(u)
	u.th = testTheme()
	u.editor.Submit = true
	u.editor.SingleLine = true

	u.m.stageAttach(`C:\x\a.txt`)
	u.m.submitRaw([]stagedAtt{{path: `C:\x\a.txt`}}, "第一条") // 占满缓冲（容量 1）
	u.m.stageAttach(`C:\x\b.txt`)
	u.editor.SetText("第二条")
	u.submitEditor()
	select {
	case in := <-u.inCh:
		if in.Raw == nil || len(in.Raw.Attachments) != 1 ||
			in.Raw.Attachments[0].Path != `C:\x\a.txt` {
			t.Fatalf("inCh = %+v", in)
		}
	default:
		t.Fatal("inCh 空")
	}
	if n := len(u.m.stagedFiles); n != 1 || u.m.stagedFiles[0].path != `C:\x\b.txt` {
		t.Fatalf("失败后应保留暂存: %+v", u.m.stagedFiles)
	}
	if n := len(u.m.blocks); n == 0 || u.m.blocks[n-1].kind != blockNotice {
		t.Fatalf("末块 = %+v, want notice", u.m.blocks)
	}
}

// TestAttachRowsWrap D186 口径四：chip 超宽按行折（每行放得下才算一行），总高随行数变。
func TestAttachRowsWrap(t *testing.T) {
	u := newFrameUI()
	for i := 0; i < 4; i++ {
		u.m.stageAttach(`C:\docs\` + strings.Repeat("文件", 3) + string(rune('A'+i)) + ".pdf")
	}
	u.m.stageAttach(`C:\docs\x.txt`)

	wide, _ := frameGtx(input.Source{})
	narrow, _ := frameGtxSize(input.Source{}, 120, winHeightDp)

	rows := u.attachRows(narrow, narrow.Constraints.Max.X)
	if len(rows) < 2 {
		t.Fatalf("窄宽应折行: rows = %v", rows)
	}
	total := 0
	for _, r := range rows {
		if len(r) == 0 {
			t.Fatalf("空行: %v", rows)
		}
		total += len(r)
	}
	if total != len(u.m.stagedFiles) {
		t.Fatalf("行内下标总数 = %d, want %d", total, len(u.m.stagedFiles))
	}
	if h := u.attachFlowHeightPx(narrow, narrow.Constraints.Max.X); h < len(rows)*narrow.Dp(attachChipDp) {
		t.Fatalf("chip 排总高 = %d, rows = %d", h, len(rows))
	}
	// 宽窗 + 短名下全放一行（总高 = 一个 chip）。
	uw := newFrameUI()
	uw.frameSize = image.Pt(winWidthDp, winHeightDp)
	uw.m.stageAttach(`C:\docs\a.txt`)
	uw.m.stageAttach(`C:\docs\b.txt`)
	if rows := uw.attachRows(wide, uw.attachFlowMaxW(wide)); len(rows) != 1 {
		t.Fatalf("宽窗应单行: %v", rows)
	}
	if uw.attachOverflowPx(wide) != 0 {
		t.Fatalf("单行时不应加高: %d", uw.attachOverflowPx(wide))
	}
	u.frameSize = image.Pt(120, winHeightDp)
	if got, want := u.attachOverflowPx(narrow), u.attachFlowHeightPx(narrow, u.attachFlowMaxW(narrow))-narrow.Dp(attachChipDp); got != want {
		t.Fatalf("overflow = %d, want %d", got, want)
	}
}

// TestAttachBlobStaged D187：已入库项暂存后按 Blob 提交（不再走文件系统）。
func TestAttachBlobStaged(t *testing.T) {
	u := newEditUI()
	ref := conversation.BlobRef{Hash: "3f2a", Name: "粘贴.png", MIME: "image/png", Size: 1024}
	u.m.stageBlob(ref)
	if n := len(u.m.stagedFiles); n != 1 || u.m.stagedFiles[0].blob == nil {
		t.Fatalf("stagedFiles = %+v", u.m.stagedFiles)
	}
	u.submitEditor()
	select {
	case in := <-u.inCh:
		if in.Raw == nil || len(in.Raw.Attachments) != 1 || in.Raw.Attachments[0].Blob == nil {
			t.Fatalf("inCh = %+v, want Blob 附件", in)
		}
		if in.Raw.Attachments[0].Path != "" {
			t.Fatalf("Blob 项不应带路径: %+v", in.Raw.Attachments[0])
		}
	default:
		t.Fatal("inCh 未收到")
	}
	if n := len(u.m.blocks); n == 0 || !strings.Contains(u.m.blocks[n-1].text, "粘贴.png") {
		t.Fatalf("回显 = %+v", u.m.blocks)
	}
}
