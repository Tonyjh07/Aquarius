package uigui

import (
	"strings"
	"testing"

	"gioui.org/io/input"
	"gioui.org/io/key"
)

// newInputRouter 帧事件路由（complFrame 的输入源）。
func newInputRouter() *input.Router { return new(input.Router) }

// TestComplOverlayFlow 浮层帧流（headless，D250）：空格进入参数相而不是关闭、命令相
// 前缀过滤、Esc 搁置与文本变更重开、未声明参数的命令也给一行提示。
func TestComplOverlayFlow(t *testing.T) {
	u, q := complFixtureUI()

	u.editor.SetText("/e")
	complFrame(q, u)
	if !u.complOpenNow || len(u.complList) != 3 || u.complList[0].insert != "/edit" {
		t.Fatalf("浮层未按 /e 开启: open=%v list=%+v", u.complOpenNow, u.complList)
	}
	if u.complSel != 0 {
		t.Fatalf("初始高亮 = %d, want 0", u.complSel)
	}
	if len(u.complRows) != 3 {
		t.Fatalf("行矩形未登记: %d", len(u.complRows))
	}

	// 空格 → 参数相（仍在），首位参数是自由文本 = 一行不可填的提示。
	u.editor.SetText("/edit ")
	complFrame(q, u)
	if !u.complOpenNow {
		t.Fatal("空格后浮层应留在参数相")
	}
	if got := u.complRowsText(); len(got) != 3 || strings.Contains(strings.Join(got, ","), "<id>") {
		t.Fatalf("自由文本提示行不该登记命中矩形: %v", got)
	}
	if len(u.complList) != 4 { // 3 个开关 + <id> 提示行
		t.Fatalf("参数相候选 = %+v", u.complList)
	}

	// Esc 搁置（文本不动）；文本变更即重开。
	q.Queue(key.Event{Name: key.NameEscape, State: key.Press})
	complFrame(q, u)
	if u.complOpenNow || u.editor.Text() != "/edit " || !u.complDismissed {
		t.Fatalf("Esc 未搁置: open=%v text=%q dismissed=%v", u.complOpenNow, u.editor.Text(), u.complDismissed)
	}
	u.editor.SetText("/edit --k")
	complFrame(q, u)
	if !u.complOpenNow || len(u.complList) != 1 || u.complList[0].insert != "--keep" {
		t.Fatalf("文本变更未重开浮层: open=%v list=%+v", u.complOpenNow, u.complList)
	}
	if len(u.complRows) != 1 {
		t.Fatalf("可填行应登记命中矩形: %v", u.complRowsText())
	}
}

// TestComplTabRotation Tab 轮换（D250）：首按锁定候选并填第一项，此后每按依次替换同一
// 段 token，候选表不随文本变窄；换行时高亮跟着走。
func TestComplTabRotation(t *testing.T) {
	u, q := complFixtureUI()
	u.editor.SetText("/e")
	complFrame(q, u)

	q.Queue(key.Event{Name: key.NameTab, State: key.Press})
	for _, want := range []struct {
		text string
		sel  int
	}{
		{"/edit", 0},   // 第一项
		{"/effort", 1}, // 第二项
		{"/exit", 2},   // 第三项（与首项不同的候选，证明候选没被文本拖窄）
		{"/edit", 0},   // 环回
	} {
		complFrame(q, u)
		if got := u.editor.Text(); got != want.text {
			t.Fatalf("Tab 后文本 = %q, want %q", got, want.text)
		}
		if u.complSel != want.sel {
			t.Fatalf("Tab 后高亮 = %d, want %d", u.complSel, want.sel)
		}
		if len(u.complList) != 3 {
			t.Fatalf("轮换期候选被重算成 %d 条", len(u.complList))
		}
		q.Queue(key.Event{Name: key.NameTab, State: key.Press})
	}
	complFrame(q, u) // 吃掉最后一帧排队的那下 Tab（环回到 /effort）

	// 用户敲别的字符 → 退出轮换，下一次 Tab 重新起一轮。
	u.editor.SetText("/ex")
	complFrame(q, u)
	if u.cycle.on {
		t.Fatal("文本变更后仍在轮换")
	}
	q.Queue(key.Event{Name: key.NameTab, State: key.Press})
	complFrame(q, u)
	if u.editor.Text() != "/exit" || !u.cycle.on {
		t.Fatalf("新的一轮 = %q, on=%v", u.editor.Text(), u.cycle.on)
	}
}

// TestComplTabArgs 参数相 Tab 只替换末尾 token，不吞掉前面已写好的参数（含中文参数
// 名——按 rune 换算光标才不偏）。
func TestComplTabArgs(t *testing.T) {
	u, q := complFixtureUI()
	u.editor.SetText("/effort m")
	complFrame(q, u)
	q.Queue(key.Event{Name: key.NameTab, State: key.Press})
	complFrame(q, u)
	if got := u.editor.Text(); got != "/effort minimal" {
		t.Fatalf("参数相 Tab = %q, want /effort minimal", got)
	}
	q.Queue(key.Event{Name: key.NameTab, State: key.Press})
	complFrame(q, u)
	if got := u.editor.Text(); got != "/effort medium" {
		t.Fatalf("参数相 Tab 第二次 = %q, want /effort medium", got)
	}
	// 全部候选都轮过一遍后回到首项。
	for i := 0; i < 3; i++ {
		q.Queue(key.Event{Name: key.NameTab, State: key.Press})
		complFrame(q, u)
	}
	if got := u.editor.Text(); got != "/effort minimal" {
		t.Fatalf("环回 = %q, want /effort minimal", got)
	}
}

// TestComplShiftTabReverse Shift+Tab 与 Tab 同一套轮换、方向相反（D250）：首按落到末项，
// 此后后退并环回；Tab 接着按仍往前走。
func TestComplShiftTabReverse(t *testing.T) {
	u, q := complFixtureUI()
	u.editor.SetText("/e")
	complFrame(q, u)

	// 真机上窗口把 Tab / Shift+Tab 包成 SystemEvent（焦点导航保留键），过滤器得能接住。
	shiftTab := input.SystemEvent{Event: key.Event{Name: key.NameTab, State: key.Press, Modifiers: key.ModShift}}
	q.Queue(shiftTab)
	complFrame(q, u)
	if got := u.editor.Text(); got != "/exit" {
		t.Fatalf("Shift+Tab 首按 = %q, want /exit（末项）", got)
	}
	if u.complSel != 2 {
		t.Fatalf("Shift+Tab 首按高亮 = %d, want 2", u.complSel)
	}
	q.Queue(shiftTab)
	complFrame(q, u)
	if got := u.editor.Text(); got != "/effort" {
		t.Fatalf("Shift+Tab 第二次 = %q, want /effort（后退）", got)
	}
	q.Queue(shiftTab)
	complFrame(q, u)
	if got := u.editor.Text(); got != "/edit" {
		t.Fatalf("Shift+Tab 第三次 = %q, want /edit", got)
	}
	if got := u.editor.Text(); !u.cycle.on || got != "/edit" {
		t.Fatalf("Shift+Tab 后轮换状态: on=%v text=%q", u.cycle.on, got)
	}
	// 反向接着按 Tab：方向掉头，从 /edit 往前走到 /effort。
	q.Queue(key.Event{Name: key.NameTab, State: key.Press})
	complFrame(q, u)
	if got := u.editor.Text(); got != "/effort" {
		t.Fatalf("反向转向前进 = %q, want /effort", got)
	}
	// 逆向环回：从首项再退一步落到末项。
	u.editor.SetText("/e")
	complFrame(q, u)
	q.Queue(shiftTab)
	complFrame(q, u)
	if got := u.editor.Text(); got != "/exit" {
		t.Fatalf("新的一轮 Shift+Tab = %q, want /exit", got)
	}
}

// TestComplEnterSubmits Enter 恒提交（D250）：浮层开着也直接发出去，不补全。
func TestComplEnterSubmits(t *testing.T) {
	u := newEditUI() // inCh 可断言
	u.opts.Commands = fakeCatalog{list: complFixture()}
	u.focusPending = true
	q := newInputRouter()
	complFrame(q, u)

	u.editor.SetText("/edit")
	complFrame(q, u)
	if !u.complOpenNow {
		t.Fatal("浮层未开启，本测试前提不成立")
	}
	q.Queue(key.Event{Name: key.NameEnter, State: key.Press})
	complFrame(q, u)
	select {
	case in := <-u.inCh:
		if in.Command == nil || in.Command.Name != "edit" || len(in.Command.Args) != 0 {
			t.Fatalf("inCh = %+v, want /edit 命令（未补全）", in)
		}
	default:
		t.Fatal("Enter 未提交")
	}
	if u.editor.Text() != "" {
		t.Fatalf("提交后编辑框未清空: %q", u.editor.Text())
	}
}

// TestComplNoTabInText 补全落笔不带尾随空白（D250）：带空格的话下一次 Tab 就从「新参数
// 开头」重新起一轮，轮换再也接不上。
func TestComplNoTabInText(t *testing.T) {
	u, q := complFixtureUI()
	u.editor.SetText("/e")
	complFrame(q, u)
	q.Queue(key.Event{Name: key.NameTab, State: key.Press})
	complFrame(q, u)
	if strings.ContainsAny(u.editor.Text(), " \t") {
		t.Fatalf("命令相落笔带了空白: %q", u.editor.Text())
	}
}
