package uigui

import (
	"testing"

	"gioui.org/io/input"
	"gioui.org/io/key"
	"gioui.org/layout"

	"github.com/Tonyjh07/Aquarius/internal/port"
)

// keyFrame 输入栏按键往返：每帧走一遍完整 layout，事件经 Router 送达。
type keyFrame struct {
	q input.Router
	u *UI
}

func newKeyFrame(u *UI) *keyFrame { return &keyFrame{u: u} }

func (f *keyFrame) frame(pre func(gtx layout.Context)) {
	gtx, ops := frameGtx(f.q.Source())
	if pre != nil {
		pre(gtx)
	}
	f.u.layout(gtx)
	f.q.Frame(ops)
}

func (f *keyFrame) focusEditor() {
	f.frame(func(gtx layout.Context) { gtx.Execute(key.FocusCmd{Tag: &f.u.editor}) })
	f.frame(nil)
}

// press 一个回车族按键（按下 + 抬起）。
func (f *keyFrame) press(name key.Name, mods key.Modifiers) {
	f.q.Queue(key.Event{Name: name, Modifiers: mods, State: key.Press})
	f.frame(nil)
	f.q.Queue(key.Event{Name: name, Modifiers: mods, State: key.Release})
	f.frame(nil)
}

// TestShiftEnterSteers 生成中按引导键：一句话进清单且标成 steer。
//
// 主回车在 Gio 里报的是 NameReturn（"⏎"），小键盘的才是 NameEnter（"⌤"）——两个都要
// 认。只认后者时真实键盘上的 Shift+Enter 会漏给编辑器当换行，表现为「按了没反应」。
func TestShiftEnterSteers(t *testing.T) {
	for _, name := range []key.Name{key.NameReturn, key.NameEnter} {
		p := &fakePending{}
		u := newPendingUI(p, true)
		u.th = testTheme()
		f := newKeyFrame(u)
		f.focusEditor()
		u.editor.SetText("先看这里")

		f.press(name, key.ModShift)

		if len(p.items) != 1 || p.items[0].Delivery != port.DeliverySteer {
			t.Fatalf("%q + Shift: 清单 = %+v, want 一条引导", name, p.items)
		}
	}
}

// TestEnterQueuesDuringGeneration 生成中纯回车 = 排队（不是立刻发出去）。
func TestEnterQueuesDuringGeneration(t *testing.T) {
	p := &fakePending{}
	u := newPendingUI(p, true)
	u.th = testTheme()
	f := newKeyFrame(u)
	f.focusEditor()
	u.editor.SetText("等服务完再说")

	f.press(key.NameReturn, 0)

	if len(p.items) != 1 || p.items[0].Delivery != port.DeliveryQueue {
		t.Fatalf("清单 = %+v, want 一条排队", p.items)
	}
	select {
	case in := <-u.inCh:
		t.Fatalf("生成中不该走输入通道，收到 %+v", in)
	default:
	}
}

// TestCtrlEnterNewline Ctrl+回车 = 换行不发送（并展开成多行）。
func TestCtrlEnterNewline(t *testing.T) {
	p := &fakePending{}
	u := newPendingUI(p, true)
	u.th = testTheme()
	f := newKeyFrame(u)
	f.focusEditor()
	u.editor.SetText("第一行")
	u.editor.SetCaret(3, 3) // 光标在行尾（真实键入后的位置）

	f.press(key.NameReturn, key.ModCtrl)

	if len(p.items) != 0 {
		t.Fatalf("换行不该进清单，实际 %+v", p.items)
	}
	if got := u.editor.Text(); got != "第一行\n" {
		t.Fatalf("编辑器 = %q, want 行尾换行", got)
	}
	if !u.expanded {
		t.Fatal("换行应顺带展开成多行输入")
	}
}

// TestSteerWhenIdleSends 空闲时按引导键与发送等价（没有「插话」这个位置）。
func TestSteerWhenIdleSends(t *testing.T) {
	p := &fakePending{}
	u := newPendingUI(p, false)
	u.th = testTheme()
	f := newKeyFrame(u)
	f.focusEditor()
	u.editor.SetText("现在就说")

	f.press(key.NameReturn, key.ModShift)

	if len(p.items) != 0 {
		t.Fatalf("空闲时不该进清单，实际 %+v", p.items)
	}
	select {
	case in := <-u.inCh:
		if in.Text != "现在就说" {
			t.Fatalf("输入通道 = %+v, want 现在就说", in)
		}
	default:
		t.Fatal("空闲时按引导键应直接发送")
	}
}
