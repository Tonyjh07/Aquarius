package uigui

import (
	"slices"
	"testing"

	"gioui.org/io/input"
	"gioui.org/io/key"

	"github.com/Tonyjh07/Aquarius/internal/domain/conversation"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

// newEditUI 带 inCh 的编辑流测试 UI（newFrameUI 无 inCh——编辑提交要断言通道）。
// 编辑器对齐生产配置（SingleLine，gui.go:326）——多行文本在 SetText/Insert 时压平。
func newEditUI() *UI {
	u := &UI{followTail: true, inCh: make(chan port.UserInput, 8)}
	u.m = newModel(u)
	u.th = testTheme()
	u.editor.Submit = true
	u.editor.SingleLine = true
	return u
}

// TestEditSubmitStructured D92 编辑态提交：结构化 /edit 命令（Args=[id, 文本] 直达，
// 不经斜杠解析），不回显 blockUser（修订结果由清屏回放呈现）；提交后退出编辑态。
// 多行文本在 SingleLine 编辑框内压平为空格（所见即所发，D92 后果②；命令管线对
// buffer 内容零再加工）。
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
		// D107②：editMsg 零值 part=0（user 气泡恒段 0）→ 结构化提交附 --part 0。
		want := []string{"u1", "--part", "0", "新问题 第二行"}
		if in.Command == nil || in.Command.Name != "edit" || !slices.Equal(in.Command.Args, want) {
			t.Fatalf("inCh = %+v, want 结构化 edit 命令且所见即所发（Args %v）", in, want)
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

// TestEditSubmitModes D97 编辑态三方式提交：editMode 随 editMsg 设定，提交转为
// flag token（--keep=Carry / --copy=Clone，与 typed 路径同解析，D97①）；Fresh 无
// token；提交后 editMode 复位 Fresh。
func TestEditSubmitModes(t *testing.T) {
	cases := []struct {
		name string
		mode conversation.KeepMode
		want []string
	}{
		{"Fresh", conversation.Fresh, []string{"u1", "--part", "0", "新问题"}},
		{"Carry", conversation.Carry, []string{"u1", "--keep", "--part", "0", "新问题"}},
		{"Clone", conversation.Clone, []string{"u1", "--copy", "--part", "0", "新问题"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			u := newEditUI()
			u.apply(editMsg{id: "u1", text: "旧", mode: tc.mode})
			if u.m.editTarget != "u1" || u.m.editMode != tc.mode {
				t.Fatalf("编辑态 = target %q mode %v, want u1/%v", u.m.editTarget, u.m.editMode, tc.mode)
			}
			u.editor.SetText("新问题")
			u.submitEditor()
			if u.m.editTarget != "" || u.m.editMode != conversation.Fresh {
				t.Fatalf("提交后 = target %q mode %v, want 退出且复位 Fresh", u.m.editTarget, u.m.editMode)
			}
			select {
			case in := <-u.inCh:
				if in.Command == nil || in.Command.Name != "edit" || !slices.Equal(in.Command.Args, tc.want) {
					t.Fatalf("inCh = %+v, want Args %v", in, tc.want)
				}
			default:
				t.Fatal("inCh 未收到提交")
			}
		})
	}
}

// TestQuoteInto D98 引用装配纯函数："> " 单行引用前缀（换行压为空格——SingleLine
// 编辑框口径）、非空以空格衔接。
func TestQuoteInto(t *testing.T) {
	cases := []struct {
		name     string
		existing string
		quoted   string
		want     string
	}{
		{"空框单行", "", "引用文本", "> 引用文本"},
		{"空框多行压平", "", "一\n二", "> 一 二"},
		{"非空衔接", "草稿", "引用", "草稿 > 引用"},
		{"非空多行引用", "草稿", "一\n二", "草稿 > 一 二"},
		{"引用含空行", "", "一\n\n二", "> 一  二"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := quoteInto(tc.existing, tc.quoted); got != tc.want {
				t.Fatalf("quoteInto(%q, %q) = %q, want %q", tc.existing, tc.quoted, got, tc.want)
			}
		})
	}
}

// TestQuoteDispatch D98 引用 apply：追加而非替换（保留既有草稿）、不进编辑态、焦点待入。
func TestQuoteDispatch(t *testing.T) {
	u := newEditUI()
	u.apply(quoteMsg{text: "一\n二"})
	if u.m.editTarget != "" || !u.focusPending {
		t.Fatalf("编辑态 = %q focusPending=%v, want 空且待入焦", u.m.editTarget, u.focusPending)
	}
	if got := u.editor.Text(); got != "> 一 二" {
		t.Fatalf("编辑框 = %q, want > 一 二", got)
	}
	u.editor.SetText("已有草稿")
	u.apply(quoteMsg{text: "引用"})
	if got := u.editor.Text(); got != "已有草稿 > 引用" {
		t.Fatalf("编辑框 = %q, want 追加", got)
	}
}

// TestEditTextHint D92/D97 编辑态提示：占用状态行（无生成状态时），方式后缀随
// editMode；生成状态优先。
func TestEditTextHint(t *testing.T) {
	u := newEditUI()
	if u.statusText() != "" {
		t.Fatalf("静息 statusText = %q, want 空", u.statusText())
	}
	u.m.editTarget = "u1"
	if got := u.statusText(); got != "编辑中 · Enter 提交 / Esc 取消" {
		t.Fatalf("Fresh 编辑态 statusText = %q", got)
	}
	u.m.editMode = conversation.Carry
	if got := u.statusText(); got != "编辑中（转移历史）· Enter 提交 / Esc 取消" {
		t.Fatalf("Carry 编辑态 statusText = %q", got)
	}
	u.m.editMode = conversation.Clone
	if got := u.statusText(); got != "编辑中（复制历史）· Enter 提交 / Esc 取消" {
		t.Fatalf("Clone 编辑态 statusText = %q", got)
	}
	u.m.editMode = conversation.Fresh
	u.generating.Store(true)
	if got := u.statusText(); got == "" || got == "编辑中 · Enter 提交 / Esc 取消" {
		t.Fatalf("生成状态应优先: %q", got)
	}
}

// TestEditSubmitPartArg D107②：编辑态带分片序号提交 = 结构化 /edit 附 --part N；
// 取消收尾分片序号归位（生命周期与 editTarget 同步）。
func TestEditSubmitPartArg(t *testing.T) {
	u := newEditUI()
	u.apply(editMsg{id: "a1", text: "前半段", part: 0})
	if u.m.editPart != 0 {
		t.Fatalf("editPart = %d, want 0", u.m.editPart)
	}
	u.editor.SetText("新前半段")
	u.submitEditor()
	select {
	case in := <-u.inCh:
		want := []string{"a1", "--part", "0", "新前半段"}
		if in.Command == nil || !slices.Equal(in.Command.Args, want) {
			t.Fatalf("Args = %v, want %v", in.Command.Args, want)
		}
	default:
		t.Fatal("inCh 未收到提交")
	}

	u.apply(editMsg{id: "a2", text: "x", part: 2})
	u.cancelEdit()
	if u.m.editPart != -1 || u.m.editTarget != "" {
		t.Fatalf("取消未收尾: part=%d target=%q", u.m.editPart, u.m.editTarget)
	}
}
