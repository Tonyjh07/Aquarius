package uigui

import (
	"testing"

	"gioui.org/io/input"

	"github.com/Tonyjh07/Aquarius/internal/port"
)

// TestAttachStageFlow D104 暂存随文发：attachMsg 暂存 → 随文提交 Raw → 暂存清空；
// 空文本 = 仅附件；编辑态不消费暂存；取消清槽；chip 渲染不炸帧。
func TestAttachStageFlow(t *testing.T) {
	u := newEditUI() // inCh 可断言（SingleLine 编辑器对齐生产）
	q := new(input.Router)
	complFrame(q, u)

	// 暂存（attachMsg 应用）→ 随文提交。
	u.apply(attachMsg{path: `C:\docs\报告.pdf`})
	if u.m.stagedFile != `C:\docs\报告.pdf` {
		t.Fatalf("stagedFile = %q", u.m.stagedFile)
	}
	complFrame(q, u) // chip 呈现帧（渲染路径冒烟）
	u.editor.SetText("总结这个文件")
	u.submitEditor()
	select {
	case in := <-u.inCh:
		if in.Raw == nil || in.Raw.Kind != "file" || in.Raw.File != `C:\docs\报告.pdf` ||
			in.Raw.Text != "总结这个文件" {
			t.Fatalf("inCh = %+v, want Raw{file, 随文}", in)
		}
	default:
		t.Fatal("inCh 未收到 Raw 提交")
	}
	if u.m.stagedFile != "" || u.editor.Text() != "" {
		t.Fatalf("提交后未清空: staged=%q editor=%q", u.m.stagedFile, u.editor.Text())
	}
	if n := len(u.m.blocks); n != 1 || u.m.blocks[n-1].text != "总结这个文件 〔附件：报告.pdf〕" {
		t.Fatalf("回显块 = %+v", u.m.blocks)
	}

	// 空文本 = 仅附件（submitEditor 空文本不再提前返回）。
	u.apply(attachMsg{path: `C:\docs\b.png`})
	u.submitEditor()
	select {
	case in := <-u.inCh:
		if in.Raw == nil || in.Raw.Text != "" || in.Raw.File != `C:\docs\b.png` {
			t.Fatalf("inCh = %+v, want 仅附件", in)
		}
	default:
		t.Fatal("inCh 未收到仅附件提交")
	}

	// 编辑态不消费暂存（修订不夹带新附件，D104①）。
	u.apply(editMsg{id: "u1", text: "旧问题"})
	u.apply(attachMsg{path: `C:\x\c.md`})
	u.editor.SetText("新文本")
	u.submitEditor()
	select {
	case in := <-u.inCh:
		if in.Command == nil || in.Command.Name != "edit" {
			t.Fatalf("编辑态应走结构化 /edit, got %+v", in)
		}
	default:
		t.Fatal("inCh 未收到")
	}
	if u.m.stagedFile != `C:\x\c.md` {
		t.Fatalf("编辑态不应消费暂存: %q", u.m.stagedFile)
	}

	// 取消暂存（chip 点击同路径）。
	u.m.clearAttach()
	if u.m.stagedFile != "" {
		t.Fatal("未取消暂存")
	}
}

// TestAttachBufferFull D104 后果③：缓冲满投递失败 → 恢复暂存 + notice，不制造已发送错觉。
func TestAttachBufferFull(t *testing.T) {
	u := &UI{followTail: true, inCh: make(chan port.UserInput, 1)}
	u.m = newModel(u)
	u.th = testTheme()
	u.editor.Submit = true
	u.editor.SingleLine = true

	u.m.stageAttach(`C:\x\a.txt`)
	u.m.submitRaw(`C:\x\a.txt`, "第一条") // 占满缓冲（容量 1）
	u.m.stageAttach(`C:\x\b.txt`)
	u.editor.SetText("第二条")
	u.submitEditor()
	select {
	case in := <-u.inCh:
		if in.Raw == nil || in.Raw.File != `C:\x\a.txt` {
			t.Fatalf("inCh = %+v", in)
		}
	default:
		t.Fatal("inCh 空")
	}
	if u.m.stagedFile != `C:\x\b.txt` {
		t.Fatalf("失败后应恢复暂存: %q", u.m.stagedFile)
	}
	if n := len(u.m.blocks); n == 0 || u.m.blocks[n-1].kind != blockNotice {
		t.Fatalf("末块 = %+v, want notice", u.m.blocks)
	}
}
