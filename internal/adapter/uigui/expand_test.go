package uigui

import (
	"testing"

	"gioui.org/f32"
	"gioui.org/io/input"
	"gioui.org/io/key"

	"github.com/Tonyjh07/Aquarius/internal/port"
)

// expandFrame 一帧布局 + 提交 hit 树（复用 complFrame 语义，独立名防串扰）。
func expandFrame(q *input.Router, u *UI) {
	gtx, ops := frameGtx(q.Source())
	u.layout(gtx)
	q.Frame(ops)
}

// newExpandUI 展开态测试 UI（inCh 可断言；首帧焦点入编辑器）。
func newExpandUI(t *testing.T) (*UI, *input.Router) {
	t.Helper()
	u := &UI{followTail: true, inCh: make(chan port.UserInput, 8)}
	u.m = newModel(u)
	u.th = newTheme()
	u.editor.Submit = true
	u.editor.SingleLine = true
	u.focusPending = true
	q := new(input.Router)
	expandFrame(q, u)
	return u, q
}

// TestExpandGeometry D106②：展开态转写区压缩（460 − (72+112) = 276 @1x，修订⑸ 高 160）、
// 编辑器切多行；收起还原 388、SingleLine 回位。常态几何零变化（D76 口径）。
func TestExpandGeometry(t *testing.T) {
	u, q := newExpandUI(t)
	u.m.add(blockUser, "问") // 空转写不量高（早退分支）——放一条消息让 transH 生效
	expandFrame(q, u)
	if u.transH != 388 {
		t.Fatalf("常态 transH = %d, want 388", u.transH)
	}

	u.setExpanded(true)
	expandFrame(q, u)
	if !u.expanded || u.editor.SingleLine {
		t.Fatalf("展开态未生效: expanded=%v singleLine=%v", u.expanded, u.editor.SingleLine)
	}
	if u.transH != 276 {
		t.Fatalf("展开态 transH = %d, want 276（压缩 112）", u.transH)
	}

	u.setExpanded(false)
	expandFrame(q, u)
	if u.transH != 388 {
		t.Fatalf("收起 transH = %d, want 388", u.transH)
	}
}

// TestExpandMultilineSubmit D106③：多行编辑正常提交（文本带换行入管线，编辑框清空）。
func TestExpandMultilineSubmit(t *testing.T) {
	u, _ := newExpandUI(t)
	u.setExpanded(true)

	u.editor.SetText("第一行\n第二行")
	if u.editor.Text() != "第一行\n第二行" {
		t.Fatalf("多行文本未保留: %q", u.editor.Text())
	}
	u.submitEditor()
	select {
	case in := <-u.inCh:
		if in.Text != "第一行\n第二行" {
			t.Fatalf("inCh = %+v, want 多行文本", in)
		}
	default:
		t.Fatal("inCh 未收到提交")
	}
	if u.editor.Text() != "" {
		t.Fatalf("提交后编辑框 = %q", u.editor.Text())
	}
}

// TestExpandCollapseKeepsText D106 修订⑶：收起不压平——换行原样保留（数据保真），
// 重新展开即完整多行。
func TestExpandCollapseKeepsText(t *testing.T) {
	u, _ := newExpandUI(t)
	u.setExpanded(true)
	u.editor.SetText("一\n二\n三")
	u.setExpanded(false)
	if u.editor.Text() != "一\n二\n三" {
		t.Fatalf("收起应保留换行: %q", u.editor.Text())
	}
	if !u.editor.SingleLine {
		t.Fatal("收起后应为 SingleLine")
	}
	u.setExpanded(true)
	if u.editor.Text() != "一\n二\n三" {
		t.Fatalf("重新展开应还原多行: %q", u.editor.Text())
	}
}

// TestExpandEsc D106③：展开态 Esc 收起（文本保留）——浮层/编辑态优先级让渡另测。
func TestExpandEsc(t *testing.T) {
	u, q := newExpandUI(t)
	u.setExpanded(true)
	expandFrame(q, u) // 注册 Esc 过滤器（Focus 限编辑器）

	u.editor.SetText("草稿")
	q.Queue(key.Event{Name: key.NameEscape, State: key.Press})
	expandFrame(q, u)
	if u.expanded {
		t.Fatal("Esc 未收起")
	}
	if u.editor.Text() != "草稿" {
		t.Fatalf("收起应保留文本: %q", u.editor.Text())
	}
}

// TestExpandEnterShiftEnter D106③：展开态 Enter = 提交（不落换行）、Shift+Enter = 换行。
func TestExpandEnterShiftEnter(t *testing.T) {
	u, q := newExpandUI(t)
	u.setExpanded(true)
	expandFrame(q, u)

	// Shift+Enter 落换行（插入位置 = 光标处——先移到末尾）。
	u.editor.SetText("一")
	u.editor.SetCaret(len([]rune(u.editor.Text())), len([]rune(u.editor.Text())))
	q.Queue(key.Event{Name: key.NameReturn, State: key.Press, Modifiers: key.ModShift})
	expandFrame(q, u)
	if u.editor.Text() != "一\n" {
		t.Fatalf("Shift+Enter = %q, want 一\\n", u.editor.Text())
	}

	// Enter 提交（文本不被追加换行——提交内容 = 编辑框原样）。
	u.editor.SetText("一\n二")
	q.Queue(key.Event{Name: key.NameReturn, State: key.Press})
	expandFrame(q, u)
	select {
	case in := <-u.inCh:
		if in.Text != "一\n二" {
			t.Fatalf("Enter 提交 = %+v, want 一\\n二", in)
		}
	default:
		t.Fatal("Enter 未提交")
	}
	if u.editor.Text() != "" {
		t.Fatalf("提交后编辑框 = %q", u.editor.Text())
	}
}

// TestExpandCollapseWindow D106③：收起窗口（球）时展开态一并复位（文本原样保留，
// 修订⑶ 不压平）。
func TestExpandCollapseWindow(t *testing.T) {
	u, _ := newExpandUI(t)
	u.setExpanded(true)
	u.editor.SetText("一\n二")
	u.beginCollapse()
	if u.expanded {
		t.Fatal("收起窗口应复位展开态")
	}
	if u.editor.Text() != "一\n二" {
		t.Fatalf("复位应保留文本: %q", u.editor.Text())
	}
}

// TestEditMultilineAutoExpand D106 修订⑷：编辑多行消息自动展开，预填保真（含换行）。
func TestEditMultilineAutoExpand(t *testing.T) {
	u, _ := newExpandUI(t)
	u.apply(editMsg{id: "u1", text: "第一行\n第二行"})
	if !u.expanded || u.editor.SingleLine {
		t.Fatalf("多行原文未自动展开: expanded=%v singleLine=%v", u.expanded, u.editor.SingleLine)
	}
	if u.m.editTarget != "u1" || u.editor.Text() != "第一行\n第二行" {
		t.Fatalf("预填失真: target=%q editor=%q", u.m.editTarget, u.editor.Text())
	}

	// 单行原文不改变展开态。
	u.setExpanded(false)
	u.apply(editMsg{id: "u2", text: "单行"})
	if u.expanded {
		t.Fatal("单行原文不应展开")
	}
	if u.editor.Text() != "单行" {
		t.Fatalf("单行预填 = %q", u.editor.Text())
	}
}

// TestCaretRect D106 修订⑴：光标杆高度只随行高（20px 行高 → 杆高 20），与内容高无关。
func TestCaretRect(t *testing.T) {
	r := caretRect(f32.Pt(5, 100), 20)
	if r.Dy() != 20 || r.Min.Y != 100-16 || r.Max.Y != 100+4 {
		t.Fatalf("caretRect = %v, want 高 20、基线上 0.8 行下 0.2 行", r)
	}
}

// TestSubmitAutoCollapse D106 修订⑥：发送后展开态自动收起（编辑态提交同路）。
func TestSubmitAutoCollapse(t *testing.T) {
	u, _ := newExpandUI(t)
	u.setExpanded(true)
	u.editor.SetText("一条消息")
	u.submitEditor()
	if u.expanded {
		t.Fatal("发送后应自动收起")
	}
	if !u.editor.SingleLine {
		t.Fatal("收起后应为 SingleLine")
	}

	// 常态提交不受影响。
	u.editor.SetText("第二条")
	u.submitEditor()
	if u.expanded {
		t.Fatal("常态提交不应展开")
	}
}
