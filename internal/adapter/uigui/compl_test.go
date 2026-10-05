package uigui

import (
	"testing"

	"gioui.org/io/input"
	"gioui.org/io/key"

	"github.com/Tonyjh07/Aquarius/internal/port"
)

// fakeCatalog 补全测试用命令清单（port.CommandCatalog 假实现）。
type fakeCatalog struct{ list []port.CommandInfo }

func (f fakeCatalog) Commands() []port.CommandInfo { return f.list }

// complFixture 清单样例：覆盖主名前缀、别名条目（quit/exit）。
func complFixture() []port.CommandInfo {
	return []port.CommandInfo{
		{Names: []string{"edit"}, Usage: "<id> [--keep|--copy] <文本>", Desc: "Revise 修订"},
		{Names: []string{"effort"}, Usage: "[档位]", Desc: "推理档位"},
		{Names: []string{"quit", "exit"}, Desc: "退出"},
	}
}

// complFrame 一帧布局 + 提交 hit 树（Gio 次序：渲染之后、present 之前）。
func complFrame(q *input.Router, u *UI) {
	gtx, ops := frameGtx(q.Source())
	u.layout(gtx)
	q.Frame(ops)
}

// TestComplPhase 词法相判定（D103③）：`/` 开头且无空格。
func TestComplPhase(t *testing.T) {
	for text, want := range map[string]bool{
		"": false, "/": true, "/ed": true, "/EDIT": true, "/中文": true,
		"/edit ": false, "/edit x": false, "ed": false,
	} {
		if got := complPhase(text); got != want {
			t.Fatalf("complPhase(%q) = %v, want %v", text, got, want)
		}
	}
}

// TestComplFilter 前缀过滤（D103③）：保持清单序、大小写不敏感、别名命中不回写主名。
func TestComplFilter(t *testing.T) {
	all := complFixture()
	got := complFilter("E", all)
	if len(got) != 3 || got[0].name != "edit" || got[1].name != "effort" || got[2].name != "exit" {
		t.Fatalf("filter(E) = %+v", got)
	}
	got = complFilter("exit", all)
	if len(got) != 1 || got[0].name != "exit" || got[0].info.Names[0] != "quit" {
		t.Fatalf("filter(exit) = %+v", got)
	}
	if len(complFilter("zz", all)) != 0 {
		t.Fatal("无命中应为空")
	}
}

// TestComplAccept 落笔规则（D103③）：完全一致 → 提交；否则补全为 "/名 "。
func TestComplAccept(t *testing.T) {
	it := complItem{info: complFixture()[2], name: "exit"}
	if text, submit := complAccept("/exit", it); !submit || text != "/exit" {
		t.Fatalf("一致未提交: (%q, %v)", text, submit)
	}
	if text, submit := complAccept("/ex", it); submit || text != "/exit " {
		t.Fatalf("补全 = (%q, %v)", text, submit)
	}
}

// TestComplOverlayFlow 浮层帧流（headless）：词法相开启/前缀过滤/↑↓ 高亮/Esc 搁置
// 与重开/空格参数相关闭。
func TestComplOverlayFlow(t *testing.T) {
	u := newFrameUI()
	u.opts.Commands = fakeCatalog{list: complFixture()}
	u.focusPending = true // 首帧焦点入编辑器（Focus 过滤器路由前提，newUI 同款）
	q := new(input.Router)
	complFrame(q, u)

	u.editor.SetText("/e")
	complFrame(q, u)
	if !u.complOpenNow || len(u.complList) != 3 || u.complList[0].name != "edit" {
		t.Fatalf("浮层未按 /e 开启: open=%v list=%+v", u.complOpenNow, u.complList)
	}
	if u.complSel != 0 {
		t.Fatalf("初始高亮 = %d, want 0", u.complSel)
	}
	if len(u.complRows) != 3 {
		t.Fatalf("行矩形未登记: %d", len(u.complRows))
	}

	// ↑↓ 循环移动高亮。
	q.Queue(key.Event{Name: key.NameDownArrow, State: key.Press})
	complFrame(q, u)
	if u.complSel != 1 {
		t.Fatalf("Down 后高亮 = %d, want 1", u.complSel)
	}
	q.Queue(key.Event{Name: key.NameUpArrow, State: key.Press})
	complFrame(q, u)
	if u.complSel != 0 {
		t.Fatalf("Up 后高亮 = %d, want 0", u.complSel)
	}

	// Esc 搁置（文本不动）；文本变更即重开。
	q.Queue(key.Event{Name: key.NameEscape, State: key.Press})
	complFrame(q, u)
	if u.complOpenNow || u.editor.Text() != "/e" || !u.complDismissed {
		t.Fatalf("Esc 未搁置: open=%v text=%q dismissed=%v", u.complOpenNow, u.editor.Text(), u.complDismissed)
	}
	u.editor.SetText("/ed")
	complFrame(q, u)
	if !u.complOpenNow || u.complSel != 0 {
		t.Fatalf("文本变更未重开浮层: open=%v sel=%d", u.complOpenNow, u.complSel)
	}

	// 空格落参数相 → 关闭。
	u.editor.SetText("/edit ")
	complFrame(q, u)
	if u.complOpenNow {
		t.Fatal("参数相不应显示浮层")
	}
}

// TestComplEnter 落笔（headless）：不一致补全不提交、一致直接提交、补全后随词法相关闭。
func TestComplEnter(t *testing.T) {
	u := newEditUI() // inCh 可断言
	u.opts.Commands = fakeCatalog{list: complFixture()}
	q := new(input.Router)
	complFrame(q, u)

	u.editor.SetText("/e")
	complFrame(q, u)
	u.complEnter()
	if u.editor.Text() != "/edit " {
		t.Fatalf("补全落笔 = %q, want /edit␠", u.editor.Text())
	}
	select {
	case <-u.inCh:
		t.Fatal("补全不应提交")
	default:
	}
	complFrame(q, u)
	if u.complOpenNow {
		t.Fatal("补全后浮层应随参数相关闭")
	}

	u.editor.SetText("/edit")
	complFrame(q, u)
	u.complEnter()
	select {
	case in := <-u.inCh:
		if in.Command == nil || in.Command.Name != "edit" {
			t.Fatalf("inCh = %+v, want /edit 命令", in)
		}
	default:
		t.Fatal("一致未提交")
	}
}
