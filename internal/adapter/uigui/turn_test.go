package uigui

// turn_test.go Turn 过程折叠（D326）：
//   - 分组：assistant 定稿前的紧邻思考/chip 折成一行摘要（含 ≥1 chip 才折），纯思考组
//     不折、无定稿（纯工具轮）不折；
//   - 展开点击 → 子块铺回、键序不变；再折叠恢复；
//   - 摘要文案：秒段求和（<1s 毫秒 / ≥1s 一位小数秒）、回放无耗时省略秒段；
//   - 清屏复位开合态。

import (
	"strings"
	"testing"

	"gioui.org/io/input"
	"gioui.org/io/pointer"

	"github.com/Tonyjh07/Aquarius/internal/domain/conversation"
	"github.com/Tonyjh07/Aquarius/internal/domain/tool"
	"github.com/Tonyjh07/Aquarius/internal/i18n"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

var _ = pointer.Move

// portReasoning / portToolCall / portToolResult / textPartsOf 事件与内容的小构造器。
func portReasoning(text string) port.Event {
	return port.DeltaEvent{Delta: port.Delta{Text: text, Reasoning: true}}
}

func portToolCall(id, name string) port.Event {
	return port.ToolCallEvent{Call: tool.Call{ID: tool.CallID(id), Name: name}}
}

func portToolResult(id string, ok bool, out string) port.Event {
	return port.ToolResultEvent{Result: tool.Result{CallID: tool.CallID(id), OK: ok, Output: out}}
}

func textPartsOf(s string) []conversation.Part {
	return []conversation.Part{{Kind: conversation.PartText, Text: s}}
}

// turnFixture 一轮「思考 → 两个工具 → 回答」的定稿序列（含一个已完成 chip）。
func turnFixture(t *testing.T) (*model, *UI) {
	t.Helper()
	m, u := newTestModel(t)
	u.m = m // newTestModel 不接 u.m（frameItems 需要），测试里接上
	m.handleEvent(portReasoning("想一想"))
	m.handleEvent(portToolCall("c1", "file_read"))
	m.handleEvent(portToolResult("c1", true, "完毕"))
	m.handleEvent(portToolCall("c2", "term_exec"))
	m.handleEvent(portToolResult("c2", false, "炸了"))
	m.flushThink()
	m.commit(conversation.Message{ID: "a1", Role: conversation.RoleAssistant,
		Content: textPartsOf("回答")})
	return m, u
}

// TestTurnFoldGroupsGroups 分组与摘要条目：组内块不出条目，摘要条目带子块与计数。
func TestTurnFoldGroups(t *testing.T) {
	t.Parallel()
	_, u := turnFixture(t)
	items := u.frameItems()
	if len(items) != 2 { // 摘要 + assistant 正文（live 无）
		t.Fatalf("items = %d, want 2（摘要 + 正文）: %+v", len(items), items)
	}
	turn, ans := items[0], items[1]
	if turn.kind != blockTurn || turn.turn == nil || ans.kind != blockAssistant {
		t.Fatalf("items = %+v, want [摘要, 正文]", items)
	}
	if turn.turn.tools != 2 || len(turn.turn.blocks) != 3 {
		t.Fatalf("turn = tools %d / blocks %d, want 2 / 3", turn.turn.tools, len(turn.turn.blocks))
	}
	if turn.turn.ms < 0 {
		t.Fatalf("ms = %d, want 思考耗时求和（≥0，不因回放态为 -1）", turn.turn.ms)
	}
	// 键数恒 = 子块键数和：思考 1 + chip 2 + chip 2 = 5（开合不漂移，D265 同规则）。
	if n := turn.selCount(); n != 5 {
		t.Fatalf("selCount = %d, want 5", n)
	}
}

// TestTurnFoldExclusions 纯思考组不折叠；纯工具轮（无 assistant 定稿）不折叠。
func TestTurnFoldExclusions(t *testing.T) {
	t.Parallel()
	m, u := newTestModel(t)
	u.m = m
	m.handleEvent(portReasoning("只想不干"))
	m.flushThink()
	m.commit(conversation.Message{ID: "a1", Role: conversation.RoleAssistant,
		Content: textPartsOf("回答")})
	items := u.frameItems()
	if len(items) != 2 || items[0].kind != blockThinking || items[1].kind != blockAssistant {
		t.Fatalf("纯思考组 items = %+v, want 思考块 + 正文（不折摘要）", items)
	}

	m, _ = newTestModel(t)
	u.m = m
	m.handleEvent(portToolCall("c1", "file_read"))
	m.handleEvent(portToolResult("c1", true, "完毕"))
	if items := u.frameItems(); len(items) != 1 || items[0].kind != blockTool {
		t.Fatalf("纯工具轮 items = %+v, want chip 原样（无定稿不折）", items)
	}
	_ = u
}

// frameTurnFixture 带完整主题的帧级夹具（同 turnFixture 序列，经 newFrameUI）。
func frameTurnFixture(t *testing.T) *UI {
	t.Helper()
	u := newFrameUI()
	m := u.m
	m.handleEvent(portReasoning("想一想"))
	m.handleEvent(portToolCall("c1", "file_read"))
	m.handleEvent(portToolResult("c1", true, "完毕"))
	m.handleEvent(portToolCall("c2", "term_exec"))
	m.handleEvent(portToolResult("c2", false, "炸了"))
	m.flushThink()
	m.commit(conversation.Message{ID: "a1", Role: conversation.RoleAssistant,
		Content: textPartsOf("回答")})
	return u
}

// TestTurnFoldExpandCollapse 点击摘要 → 子块铺回（键序不变），再点 → 折回。
func TestTurnFoldExpandCollapse(t *testing.T) {
	t.Parallel()
	u := frameTurnFixture(t)
	q := new(input.Router)
	gtx, ops := frameGtx(q.Source())
	u.layout(gtx)
	q.Frame(ops)

	// 点击摘要行：摘要条目的 bi = assistant 块序（fixture 中 = 3）。
	u.turnClick(3).Click()
	gtx, ops = frameGtx(q.Source())
	u.layout(gtx)
	q.Frame(ops)
	items := u.frameItems()
	// 展开态：摘要行常驻 + 思考 + 两个 chip + 正文；键数与折叠态一致（5 + 1 = 6）
	// ——开合不漂移后续行序号。
	if len(items) != 5 || items[0].kind != blockTurn ||
		items[1].kind != blockThinking || items[2].kind != blockTool ||
		items[3].kind != blockTool || items[4].kind != blockAssistant {
		t.Fatalf("展开态 items = %+v, want 摘要常驻 + 子块铺回", items)
	}
	total := 0
	for _, it := range items {
		total += u.itemSelCount(it) // 展开态摘要行让位（0 键），子块用自己的键
	}
	if total != 6 {
		t.Fatalf("展开态键数 = %d, want 6（与折叠态一致）", total)
	}
	if !u.turnOpen["a1"] {
		t.Fatal("点击后未记开合态（turnID = a1）")
	}

	// 再点 = 折回。
	u.turnClick(3).Click()
	gtx, ops = frameGtx(q.Source())
	u.layout(gtx)
	q.Frame(ops)
	if u.turnOpen["a1"] {
		t.Fatal("第二次点击后仍在展开态")
	}
	items = u.frameItems()
	if len(items) != 2 || items[0].kind != blockTurn {
		t.Fatalf("折回后 items = %+v, want 摘要 + 正文", items)
	}
}

// TestTurnFoldSpansMidTextAndConfirm 一轮里中途落过正文段、又插过确认条时，前面的
// 工具 chip 仍要收进同一行摘要（回归：中途段落曾把整轮切成两截，前半截的工具与确认条
// 孤零零留在转写区顶上看不住）。中途正文段原位不动，确认条折进去但报数进摘要。
func TestTurnFoldSpansMidTextAndConfirm(t *testing.T) {
	t.Parallel()
	m, u := newTestModel(t)
	u.m = m
	m.handleEvent(portReasoning("想一想"))
	m.handleEvent(portToolCall("c1", "file_read"))
	m.handleEvent(portToolResult("c1", true, "完毕"))
	// D95 中途段：工具边界 flush 出来的正文块，没有节点 ID。
	m.blocks = append(m.blocks, block{kind: blockAssistant, text: "先看看"})
	m.handleEvent(portToolCall("c2", "term_exec"))
	m.handleEvent(portToolResult("c2", false, "炸了"))
	m.blocks = append(m.blocks, block{kind: blockConfirm, part: -1,
		confirms: []confirmEntry{{q: "run term_exec? args: y", a: "允许"}}})
	m.flushThink()
	m.commit(conversation.Message{ID: "a1", Role: conversation.RoleAssistant,
		Content: textPartsOf("回答")})

	items := u.frameItems()
	if len(items) != 3 || items[0].kind != blockTurn ||
		items[1].kind != blockAssistant || items[2].kind != blockAssistant {
		t.Fatalf("items = %+v, want [摘要, 中途正文, 正文]", items)
	}
	tv := items[0].turn
	if tv.tools != 2 || tv.confirms != 1 || len(tv.blocks) != 4 {
		t.Fatalf("turn = tools %d / confirms %d / blocks %d, want 2 / 1 / 4（两个 chip + 确认条 + 思考）",
			tv.tools, tv.confirms, len(tv.blocks))
	}
	if want := " · " + i18n.Tf("ui.confirm.strip_total", "n", 1); !strings.Contains(turnHeaderText(tv, false), want) {
		t.Fatalf("摘要 = %q, want 含 %q（确认条收起来了，数不能跟着消失）", turnHeaderText(tv, false), want)
	}
}

// TestTurnSummarySlotStable 摘要行的点击件槽两态一致（回归：展开态曾用组首块序取
// 点击件，和点击消费端的组尾块序对不上，展开后就点不回去）。
func TestTurnSummarySlotStable(t *testing.T) {
	t.Parallel()
	m, u := turnFixture(t)
	want := -1
	for bi, b := range m.blocks { // 组尾 assistant 块序
		if b.kind == blockAssistant && b.id != "" {
			want = bi
		}
	}
	check := func(state string) {
		items := u.frameItems()
		if len(items) == 0 || items[0].kind != blockTurn {
			t.Fatalf("%s: items = %+v, want 摘要行在首位", state, items)
		}
		if items[0].bi != want {
			t.Fatalf("%s: 摘要行 bi = %d, want %d（槽 = 组尾 assistant 块序）", state, items[0].bi, want)
		}
		if tv, ok := u.turnAnchors[items[0].bi]; !ok || tv == nil || tv.slot != want {
			t.Fatalf("%s: turnAnchors[%d] = %+v, want 组自身的 slot %d", state, items[0].bi, tv, want)
		}
	}
	check("折叠态")
	u.turnOpen = map[conversation.MessageID]bool{"a1": true}
	check("展开态")
}

// TestTurnHeaderText 摘要文案：秒段格式与思考块同一套；回放（无耗时）省略秒段。
func TestTurnHeaderText(t *testing.T) {
	t.Parallel()
	cases := []struct {
		ms, tools int
		want      string
	}{
		{800, 2, "▸ " + i18n.Tf("ui.turn.summary", "sec", i18n.Tf("ui.turn.sec_ms", "n", 800), "n", 2)},
		{1500, 1, "▸ " + i18n.Tf("ui.turn.summary", "sec", i18n.Tf("ui.turn.sec_s", "n", "1.5"), "n", 1)},
		{-1, 3, "▸ " + i18n.Tf("ui.turn.summary_plain", "n", 3)},
	}
	for _, c := range cases {
		tv := &turnView{ms: c.ms, tools: c.tools}
		if got := turnHeaderText(tv, false); got != c.want {
			t.Fatalf("ms %d / tools %d = %q, want %q", c.ms, c.tools, got, c.want)
		}
	}
}

// TestTurnClearResets 清屏复位开合态（与 thinkOpen/cmdOpen 同规则）。
func TestTurnClearResets(t *testing.T) {
	t.Parallel()
	m, u := turnFixture(t)
	u.turnOpen = map[conversation.MessageID]bool{"a1": true}
	m.clear()
	if u.turnOpen != nil || u.turnAnchors != nil || len(u.turnClicks) != 0 {
		t.Fatalf("清屏后未复位: open=%v anchors=%v clicks=%d", u.turnOpen, u.turnAnchors, len(u.turnClicks))
	}
}

// TestTurnFrameRendersSummary 摘要行整帧渲染不炸、子块折叠后不占气泡矩形（右键
// 不可达 = 过程隐藏）。
func TestTurnFrameRendersSummary(t *testing.T) {
	t.Parallel()
	u := frameTurnFixture(t)
	q := new(input.Router)
	gtx, ops := frameGtx(q.Source())
	u.layout(gtx)
	q.Frame(ops)
	chipRows := 0
	for _, h := range u.bubbleRects {
		if h.kind == blockTool {
			chipRows++
		}
	}
	if chipRows != 0 {
		t.Fatalf("折叠态 bubbleRects 含 %d 个 chip 行, want 0（过程已隐藏）", chipRows)
	}
}
