package uigui

// chip_test.go D67 工具合并 chip 测试：调用/确认/结果合并、CallID 配对、确认归属
// （工具并入 / 非工具独立）、commit 收口、头部文本状态机、渲染视图（折叠默认、
// 待确认强制展开、行选键恒 2）。

import (
	"strings"
	"testing"

	"gioui.org/io/input"
	"gioui.org/widget"

	"github.com/Tonyjh07/Aquarius/internal/domain/conversation"
	"github.com/Tonyjh07/Aquarius/internal/domain/tool"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

// TestToolChipConfirmMerge 确认并入 chip：无 plain 行、应答写回 chip 且送达 runner
// （D67；应答丢失 = 权限等待永不解除）。
func TestToolChipConfirmMerge(t *testing.T) {
	m, _ := newTestModel(t)
	m.handleEvent(port.ToolCallEvent{Call: tool.Call{ID: "c1", Name: "term_exec", Args: []byte(`{"cmd":"ls"}`)}})
	reply := make(chan port.ConfirmAnswer, 1)
	m.startConfirm(`允许执行 term_exec？参数: {"cmd":"ls"}`, reply)

	if len(m.blocks) != 1 {
		t.Fatalf("blocks = %d, want 1（确认并入 chip）", len(m.blocks))
	}
	if m.blocks[0].chip == nil || m.blocks[0].chip.confirmQ == "" || m.confirmChip != 0 {
		t.Fatalf("confirm 未入 chip: chip=%+v confirmChip=%d", m.blocks[0].chip, m.confirmChip)
	}

	m.replyConfirm(port.ConfirmAnswer{Allow: true})
	if m.blocks[0].chip.confirmA != "允许" {
		t.Fatalf("confirmA = %q, want 允许", m.blocks[0].chip.confirmA)
	}
	if m.confirm != nil {
		t.Fatal("确认态未清")
	}
	select {
	case ok := <-reply:
		if !ok.Allow {
			t.Fatal("应答应为 true")
		}
	default:
		t.Fatal("应答未送达 runner")
	}

	m.handleEvent(port.ToolResultEvent{Result: tool.Result{CallID: "c1", OK: true, Output: "out"}})
	if len(m.blocks) != 1 {
		t.Fatalf("结果应并入同一 chip, blocks = %d", len(m.blocks))
	}
	c := m.blocks[0].chip
	if !c.done || !c.ok || c.result != "out" {
		t.Fatalf("chip = %+v", c)
	}
}

// TestToolChipConfirmDeny 拒绝路径同样留痕并送达。
func TestToolChipConfirmDeny(t *testing.T) {
	m, _ := newTestModel(t)
	m.handleEvent(port.ToolCallEvent{Call: tool.Call{ID: "c1", Name: "term_exec"}})
	reply := make(chan port.ConfirmAnswer, 1)
	m.startConfirm("允许执行 term_exec？参数: {}", reply)
	m.replyConfirm(port.ConfirmAnswer{})
	if m.blocks[0].chip.confirmA != "拒绝" {
		t.Fatalf("confirmA = %q, want 拒绝", m.blocks[0].chip.confirmA)
	}
	select {
	case ok := <-reply:
		if ok.Allow {
			t.Fatal("应答应为 false")
		}
	default:
		t.Fatal("应答未送达 runner")
	}
	m.handleEvent(port.ToolResultEvent{Result: tool.Result{CallID: "c1", OK: false, Err: "用户拒绝"}})
	if c := m.blocks[0].chip; !c.done || c.ok || c.result != "用户拒绝" {
		t.Fatalf("chip = %+v", c)
	}
}

// TestConfirmNonToolStaysPlain 非工具确认（/rm 二次确认，问句不含工具名）不并入
// chip：独立文本行 + 原回显（D67）。
func TestConfirmNonToolStaysPlain(t *testing.T) {
	m, _ := newTestModel(t)
	m.handleEvent(port.ToolCallEvent{Call: tool.Call{ID: "c1", Name: "term_exec"}})
	reply := make(chan port.ConfirmAnswer, 1)
	m.startConfirm("确认删除 n1 及其子树（至少 3 条节点）？此操作不可恢复", reply)

	if m.confirmChip != -1 {
		t.Fatalf("confirmChip = %d, want -1（不并入）", m.confirmChip)
	}
	if len(m.blocks) != 2 || m.blocks[1].kind != blockPlain {
		t.Fatalf("blocks = %+v, want chip + plain 行", m.blocks)
	}
	m.replyConfirm(port.ConfirmAnswer{Allow: true})
	if m.blocks[0].chip.confirmA != "" {
		t.Fatal("不应写入 chip")
	}
	if !strings.Contains(allText(m), "→ true") {
		t.Fatal("非 chip 确认应保留文本行回显")
	}
}

// TestToolChipCommitClosesOpen commit 收口未完成 chip（取消/异常没有结果事件，D67）。
func TestToolChipCommitClosesOpen(t *testing.T) {
	m, _ := newTestModel(t)
	m.handleEvent(port.ToolCallEvent{Call: tool.Call{ID: "c1", Name: "sleep_tool"}})
	m.commit(conversation.Message{Role: conversation.RoleAssistant, Outcome: conversation.OutcomeCancelled})
	c := m.blocks[0].chip
	if c == nil || !c.done || c.ok || !strings.Contains(c.result, "未返回") {
		t.Fatalf("chip 未收口: %+v", c)
	}
}

// TestToolChipCallIDPairing 并行调用交错按 CallID 回填（D67 否决 FIFO 的原因）。
func TestToolChipCallIDPairing(t *testing.T) {
	m, _ := newTestModel(t)
	m.handleEvent(port.ToolCallEvent{Call: tool.Call{ID: "a", Name: "t1"}})
	m.handleEvent(port.ToolCallEvent{Call: tool.Call{ID: "b", Name: "t2"}})
	m.handleEvent(port.ToolResultEvent{Result: tool.Result{CallID: "b", OK: true, Output: "B"}})
	if len(m.blocks) != 2 {
		t.Fatalf("blocks = %d, want 2", len(m.blocks))
	}
	if c := m.blocks[0].chip; c.done {
		t.Fatal("t1（后回填）不应被误收")
	}
	if c := m.blocks[1].chip; !c.done || c.result != "B" {
		t.Fatalf("t2 chip = %+v", c)
	}
	m.handleEvent(port.ToolResultEvent{Result: tool.Result{CallID: "a", OK: false, Err: "E"}})
	if c := m.blocks[0].chip; !c.done || c.ok || c.result != "E" {
		t.Fatalf("t1 chip = %+v", c)
	}
}

// TestChipHeaderText 头部行状态机：▸/▾、…/待确认/✓/✗、参数首行截断（D67）。
func TestChipHeaderText(t *testing.T) {
	long := `{"path":"a.json","extra":"` + strings.Repeat("x", 60) + `"}`
	c := &toolChip{name: "file_read", args: long}
	if h := chipHeaderText(c, false); !strings.HasPrefix(h, "▸ 🔧 file_read") || !strings.Contains(h, "…") {
		t.Fatalf("折叠头部 = %q", h)
	}
	if h := chipHeaderText(c, true); !strings.HasPrefix(h, "▾") {
		t.Fatalf("展开头部 = %q", h)
	}
	if got := chipHeaderText(c, false); len([]rune(got)) > 80 {
		t.Fatalf("头部未截断参数: %q", got)
	}
	c.done, c.ok = true, true
	if h := chipHeaderText(c, true); !strings.HasSuffix(h, "✓") {
		t.Fatalf("成功头部 = %q", h)
	}
	c.ok = false
	if h := chipHeaderText(c, true); !strings.HasSuffix(h, "✗") {
		t.Fatalf("失败头部 = %q", h)
	}
	pend := &toolChip{name: "x", confirmQ: "允许执行 x？"}
	if h := chipHeaderText(pend, false); !strings.Contains(h, "待确认") {
		t.Fatalf("待确认头部 = %q", h)
	}
}

// TestToolChipView 渲染视图：frameItems 携块序、键位间隔恒 2、默认折叠、待确认
// 强制展开、应答后回落、chip 为非气泡卡（D67）。
func TestToolChipView(t *testing.T) {
	u := newFrameUI()
	u.m.handleEvent(port.ToolCallEvent{Call: tool.Call{ID: "c1", Name: "file_read", Args: []byte(`{"path":"a.txt"}`)}})

	items := u.frameItems()
	if len(items) != 1 || items[0].chip == nil || items[0].chipIdx != 0 {
		t.Fatalf("items = %+v", items)
	}
	if items[0].selCount() != 2 {
		t.Fatalf("selCount = %d, want 2（键位间隔稳定）", items[0].selCount())
	}

	gtx, _ := frameGtx(input.Source{})
	if u.chipIsOpen(0, items[0].chip) {
		t.Fatal("默认折叠")
	}
	u.m.startConfirm(`允许执行 file_read？参数: {"path":"a.txt"}`, make(chan port.ConfirmAnswer, 1))
	if !u.chipIsOpen(0, u.m.blocks[0].chip) {
		t.Fatal("待确认应强制展开")
	}
	u.m.replyConfirm(port.ConfirmAnswer{Allow: true})
	if u.chipIsOpen(0, u.m.blocks[0].chip) {
		t.Fatal("应答后应回落用户选择（默认折叠）")
	}
	u.m.handleEvent(port.ToolResultEvent{Result: tool.Result{CallID: "c1", OK: true, Output: "全文输出"}})
	if c := u.m.blocks[0].chip; !c.done || c.result != "全文输出" {
		t.Fatalf("结果回填失败: %+v", c)
	}

	_, _, _, _, bubble := u.rowStyle(gtx, u.frameItems()[0], func(int) *widget.Selectable { return nil })
	if bubble {
		t.Fatal("工具 chip 应为非气泡卡")
	}
}

// TestToolChipTranscriptKeys 行选键惰性物化（D67）：折叠 chip 头部不挂行选 → 0 键；
// 展开后参数/结果 2 键；后续行的键位间隔由 selCount 保持（本测试只有 chip，看总量）。
func TestToolChipTranscriptKeys(t *testing.T) {
	u := newFrameUI()
	u.m.handleEvent(port.ToolCallEvent{Call: tool.Call{ID: "c1", Name: "file_read", Args: []byte(`{"path":"a.txt"}`)}})
	u.m.handleEvent(port.ToolResultEvent{Result: tool.Result{CallID: "c1", OK: true, Output: "输出"}})

	gtx, _ := frameGtx(input.Source{})
	u.transcript(gtx, 600, 400)
	if len(u.selRows) != 0 {
		t.Fatalf("折叠 chip 不应挂行选键: %d", len(u.selRows))
	}
	if len(u.shapes) != 1 {
		t.Fatalf("shapes = %d, want 1（单 chip 单底板）", len(u.shapes))
	}

	u.chipOpen = map[int]bool{0: true}
	u.transcript(gtx, 600, 400)
	if len(u.selRows) != 2 {
		t.Fatalf("展开后 selRows = %d, want 2（参数/结果）", len(u.selRows))
	}
}
