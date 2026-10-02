package uigui

import (
	"testing"

	"gioui.org/io/input"
	"gioui.org/io/key"

	"github.com/Tonyjh07/Aquarius/internal/port"
)

// newEditUI 带 inCh 的编辑流测试 UI（newFrameUI 无 inCh——编辑提交要断言通道）。
func newEditUI() *UI {
	u := &UI{followTail: true, inCh: make(chan port.UserInput, 8)}
	u.m = newModel(u)
	u.th = newTheme()
	return u
}

// TestEditSubmitStructured D92 编辑态提交：结构化 /edit 命令（Args=[id, 文本] 直达，
// 不经斜杠解析——多行文本保真），不回显 blockUser（修订结果由清屏回放呈现）；
// 提交后退出编辑态。
func TestEditSubmitStructured(t *testing.T) {
	u := newEditUI()
	u.m.addMsg(blockUser, "旧问题", "u1")
	u.apply(editMsg{id: "u1", text: "旧问题"})
	if u.m.editTarget != "u1" || u.editor.Text() != "旧问题" {
		t.Fatalf("编辑态未就位: target=%q editor=%q", u.m.editTarget, u.editor.Text())
	}

	u.editor.SetText("新问题\n第二行")
	u.submitEditor()
	if u.m.editTarget != "" {
		t.Fatal("提交后应退出编辑态")
	}
	select {
	case in := <-u.inCh:
		if in.Command == nil || in.Command.Name != "edit" ||
			len(in.Command.Args) != 2 || in.Command.Args[0] != "u1" || in.Command.Args[1] != "新问题\n第二行" {
			t.Fatalf("inCh = %+v, want 结构化 edit 命令且多行保真", in)
		}
	default:
		t.Fatal("inCh 未收到提交")
	}
	if len(u.m.blocks) != 1 {
		t.Fatalf("编辑提交不应新增转写块: %+v", u.m.blocks)
	}
}

// TestEditCancel D92 编辑态取消：Esc 清目标与编辑框（不投递）；空提交同样取消；
// 非编辑态提交照常回显。
func TestEditCancel(t *testing.T) {
	u := newEditUI()
	u.m.addMsg(blockUser, "问", "u1")
	u.apply(editMsg{id: "u1", text: "问"})

	// Esc：路由注入 Escape 键事件，次帧 updateEditor 消费。
	q := new(input.Router)
	bubbleFrame(q, u)
	q.Queue(key.Event{Name: key.NameEscape, State: key.Press})
	bubbleFrame(q, u)
	if u.m.editTarget != "" || u.editor.Text() != "" {
		t.Fatalf("Esc 未取消: target=%q editor=%q", u.m.editTarget, u.editor.Text())
	}

	// 空提交 = 取消（不投递）。
	u.apply(editMsg{id: "u1", text: "问"})
	u.editor.SetText("") // 预填已入编辑框，清空后提交才算空
	u.submitEditor()
	if u.m.editTarget != "" || u.editor.Text() != "" {
		t.Fatalf("空提交未取消: target=%q editor=%q", u.m.editTarget, u.editor.Text())
	}
	select {
	case <-u.inCh:
		t.Fatal("取消不应投递")
	default:
	}

	// 非编辑态照常：提交投递且回显 blockUser。
	u.editor.SetText("正常输入")
	u.submitEditor()
	select {
	case in := <-u.inCh:
		if in.Text != "正常输入" {
			t.Fatalf("普通提交 = %+v", in)
		}
	default:
		t.Fatal("普通提交未投递")
	}
	if len(u.m.blocks) != 2 || u.m.blocks[1].kind != blockUser || u.m.blocks[1].text != "正常输入" {
		t.Fatalf("普通提交应回显: %+v", u.m.blocks)
	}
}

// TestEditTextHint D92 编辑态提示：占用状态行（无生成状态时）；生成状态优先。
func TestEditTextHint(t *testing.T) {
	u := newEditUI()
	if u.statusText() != "" {
		t.Fatalf("静息 statusText = %q, want 空", u.statusText())
	}
	u.m.editTarget = "u1"
	if got := u.statusText(); got != "编辑中 · Enter 提交 / Esc 取消" {
		t.Fatalf("编辑态 statusText = %q", got)
	}
	u.generating.Store(true)
	if got := u.statusText(); got == "" || got == "编辑中 · Enter 提交 / Esc 取消" {
		t.Fatalf("生成状态应优先: %q", got)
	}
}
