package uicommon

// 转写状态机的行为测试：事件序列 → 状态断言。覆盖 D95 草稿调和、思维链时序、
// 服务端读链接（D310）与提示分流（D267）。渲染钩子用闭包替身，不涉及任何前端。

import (
	"strings"
	"testing"

	"github.com/Tonyjh07/Aquarius/internal/domain/conversation"
	"github.com/Tonyjh07/Aquarius/internal/domain/tool"
	"github.com/Tonyjh07/Aquarius/internal/i18n"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

// recorder 收集各出口的调用记录。
type recorder struct {
	thinks     []string // FlushThink 收到的整段
	segments   []string // FlushSegment 收到的原始段
	handles    []int    // 落块句柄（替身按序号生成）
	serverRead []string
	toolCalls  []string
	toolRes    []tool.Result
	committed  []conversation.Message
	errors     []string
	notices    []noticeRec
	cleared    int
	unhandled  []port.Event
}

type noticeRec struct {
	text      string
	attention bool
}

// sink 替身：落「块」就是记一个序号句柄并记入 Flushed（与前端同法）。
func (r *recorder) sink(t *Transcript) *Sink {
	return &Sink{
		FlushThink: func(text string) { r.thinks = append(r.thinks, text) },
		FlushSegment: func(raw string) {
			if raw == "" {
				return
			}
			r.segments = append(r.segments, raw)
			h := len(r.handles)
			r.handles = append(r.handles, h)
			t.Flushed = append(t.Flushed, h)
		},
		ServerRead: func(text string) { r.serverRead = append(r.serverRead, text) },
		ToolCall:   func(call tool.Call) { r.toolCalls = append(r.toolCalls, call.Name) },
		ToolResult: func(res tool.Result) { r.toolRes = append(r.toolRes, res) },
		Committed:  func(msg conversation.Message) { r.committed = append(r.committed, msg) },
		Error:      func(text string) { r.errors = append(r.errors, text) },
		Notice:     func(text string, attention bool) { r.notices = append(r.notices, noticeRec{text, attention}) },
		Clear:      func() { r.cleared++ },
		Unhandled:  func(ev port.Event) { r.unhandled = append(r.unhandled, ev) },
	}
}

// TestDispatchDraftAndFlushSegment D95：正文增量入草稿；工具边界把草稿落为正文段
// 并记入待调和清单；草稿清空后第 2 轮文本重新流式。
func TestDispatchDraftAndFlushSegment(t *testing.T) {
	var tr Transcript
	r := &recorder{}
	s := r.sink(&tr)

	tr.Dispatch(port.DeltaEvent{Delta: port.Delta{Text: "draft seg"}}, s)
	if !tr.Drafting || !strings.Contains(tr.Draft.String(), "draft seg") {
		t.Fatalf("draft = %q drafting=%v, want 流式草稿", tr.Draft.String(), tr.Drafting)
	}
	tr.Dispatch(port.ToolCallEvent{Call: tool.Call{Name: "echo"}}, s)
	if len(r.segments) != 1 || r.segments[0] != "draft seg" {
		t.Fatalf("segments = %v, want 工具边界落段", r.segments)
	}
	if len(tr.Flushed) != 1 || tr.Draft.Len() != 0 || tr.Drafting {
		t.Fatalf("flushed=%v draft=%q drafting=%v, want 段入清单、草稿清空",
			tr.Flushed, tr.Draft.String(), tr.Drafting)
	}
	tr.Dispatch(port.DeltaEvent{Delta: port.Delta{Text: "the answer"}}, s)
	if !strings.Contains(tr.Draft.String(), "the answer") {
		t.Fatalf("第 2 轮未重新流式: %q", tr.Draft.String())
	}

	// 纯空白草稿不落段（草稿照样清空）。
	tr.ResetDraft()
	tr.DraftDelta("  \n\t ")
	tr.Dispatch(port.ToolResultEvent{Result: tool.Result{OK: true}}, s)
	if len(r.segments) != 1 {
		t.Fatalf("空白段不应落块: %v", r.segments)
	}
	if tr.Draft.Len() != 0 || tr.Drafting {
		t.Fatal("空白段取走后草稿应清空")
	}

	// 用量分片（Text 为空）不进草稿。
	tr.DraftDelta("x")
	tr.Dispatch(port.DeltaEvent{Delta: port.Delta{Usage: &conversation.Usage{InputTokens: 9}}}, s)
	if tr.Draft.String() != "x" {
		t.Fatalf("用量分片不应动草稿: %q", tr.Draft.String())
	}
}

// TestReconcileDrafts D95 提交调和：按节点权威内容切段，FIFO 回填已落块段，
// 余段待追加；段数少于草稿段数时句柄截到配对数；待调和清单清空。
func TestReconcileDrafts(t *testing.T) {
	tr := &Transcript{}
	tr.Flushed = []int{7, 8}
	handles, backfill, extra := tr.ReconcileDrafts([]conversation.Part{
		{Kind: conversation.PartText, Text: "seg1"},
		{Kind: conversation.PartTool, Tool: &conversation.ToolPart{CallID: "c", Name: "echo"}},
		{Kind: conversation.PartText, Text: "seg2"},
		{Kind: conversation.PartTool, Tool: &conversation.ToolPart{CallID: "c", Name: "echo"}},
		{Kind: conversation.PartText, Text: "seg3"},
	}, SegOpts{Mode: ImageNamed})
	if len(handles) != 2 || handles[0] != 7 || handles[1] != 8 {
		t.Fatalf("handles = %v, want [7 8]", handles)
	}
	if len(backfill) != 2 || backfill[0].Text != "seg1" || backfill[1].Text != "seg2" {
		t.Fatalf("backfill = %+v", backfill)
	}
	if backfill[0].Start != 0 || backfill[1].Start != 1 {
		t.Fatalf("段序 = %d/%d, want 0/1（D107④）", backfill[0].Start, backfill[1].Start)
	}
	if len(extra) != 1 || extra[0].Text != "seg3" {
		t.Fatalf("extra = %+v", extra)
	}
	if len(tr.Flushed) != 0 {
		t.Fatalf("待调和清单未清空: %v", tr.Flushed)
	}

	// 草稿段多于权威段：配对截到段数，多余的句柄由调用方按 lastFlushed 规则自理。
	tr.Flushed = []int{1, 2, 3}
	handles, backfill, extra = tr.ReconcileDrafts([]conversation.Part{
		{Kind: conversation.PartText, Text: "唯一一段"},
	}, SegOpts{Mode: ImageNamed})
	if len(handles) != 1 || handles[0] != 1 || len(backfill) != 1 || len(extra) != 0 {
		t.Fatalf("handles=%v backfill=%+v extra=%+v, want 只配对一段", handles, backfill, extra)
	}

	// 空内容：全空返回，清单清空。
	tr.Flushed = nil
	handles, backfill, extra = tr.ReconcileDrafts(nil, SegOpts{Mode: ImageNamed})
	if len(handles)+len(backfill)+len(extra) != 0 {
		t.Fatalf("空内容应全空: %v/%+v/%+v", handles, backfill, extra)
	}
}

// TestSegmentsImages 切段的图片规则（D207）：ImageOf 收图时纯图段保留、正文不留占位；
// 不收图（行式界面）时图片按策略落文字占位。
func TestSegmentsImages(t *testing.T) {
	ref := func(hash string) *conversation.BlobRef { return &conversation.BlobRef{Hash: hash, Name: hash + ".png"} }
	parts := []conversation.Part{
		{Kind: conversation.PartText, Text: "part A"},
		{Kind: conversation.PartImage, Ref: ref("h1")},
		{Kind: conversation.PartTool, Tool: &conversation.ToolPart{CallID: "c", Name: "echo"}},
		{Kind: conversation.PartImage, Ref: ref("h2")},
	}

	segs := Segments(parts, SegOpts{
		Mode: ImageWhenUnaddressable,
		ImageOf: func(p conversation.Part) any {
			if p.Ref != nil {
				return p.Ref.Hash
			}
			return nil
		},
	})
	if len(segs) != 2 {
		t.Fatalf("段数 = %d, want 2: %+v", len(segs), segs)
	}
	if segs[0].Text != "part A" || !imgsEqual(segs[0].Image, "h1") {
		t.Fatalf("segs[0] = %+v, want part A + h1", segs[0])
	}
	if segs[1].Text != "" || !imgsEqual(segs[1].Image, "h2") {
		t.Fatalf("纯图段 = %+v, want 无字有图（D207）", segs[1])
	}

	textSegs := Segments(parts, SegOpts{Mode: ImageNamed})
	if len(textSegs) != 2 {
		t.Fatalf("段数 = %d, want 2: %+v", len(textSegs), textSegs)
	}
	if !strings.Contains(textSegs[1].Text, i18n.Tf("attach.image_named", "name", "h2.png")) {
		t.Fatalf("不收图时图片应落占位: %q", textSegs[1].Text)
	}
	if textSegs[1].Image != nil {
		t.Fatalf("行式界面不应有图片引用: %+v", textSegs[1])
	}
	if !strings.Contains(textSegs[0].Text, "part A") || textSegs[0].Image != nil {
		t.Fatalf("segs[0] = %+v, want 收图段外无引用", textSegs[0])
	}

	// 缺引用的分片回落占位文本。
	fall := Segments([]conversation.Part{{Kind: conversation.PartImage}}, SegOpts{
		Mode:    ImageWhenUnaddressable,
		ImageOf: func(conversation.Part) any { return nil },
	})
	if len(fall) != 1 || !strings.Contains(fall[0].Text, i18n.T("attach.image")) {
		t.Fatalf("缺引用分片应落占位: %+v", fall)
	}
}

// TestDispatchThinkFlow D34/D42：思维链增量积累实时草稿、不进正文草稿；正文到达先
// 落暗块；首个增量带 first 标记。
func TestDispatchThinkFlow(t *testing.T) {
	var tr Transcript
	r := &recorder{}
	s := r.sink(&tr)

	var firsts []bool
	s.OnThink = func(first bool) { firsts = append(firsts, first) }
	tr.Dispatch(port.DeltaEvent{Delta: port.Delta{Text: "think ", Reasoning: true}}, s)
	tr.Dispatch(port.DeltaEvent{Delta: port.Delta{Text: "more ", Reasoning: true}}, s)
	if !strings.Contains(tr.Think.String(), "think more ") {
		t.Fatalf("think = %q", tr.Think.String())
	}
	if tr.Draft.Len() != 0 {
		t.Fatalf("思维链不应进正文草稿: %q", tr.Draft.String())
	}
	if len(firsts) != 2 || !firsts[0] || firsts[1] {
		t.Fatalf("first 标记 = %v, want [true false]", firsts)
	}

	// 正文到达：思维链先落块。
	tr.Dispatch(port.DeltaEvent{Delta: port.Delta{Text: "the answer"}}, s)
	if len(r.thinks) != 1 || r.thinks[0] != "think more " {
		t.Fatalf("thinks = %v, want 落一段暗块", r.thinks)
	}
	if tr.Think.Len() != 0 {
		t.Fatal("思维链应清空")
	}
	if tr.Draft.String() != "the answer" {
		t.Fatalf("draft = %q", tr.Draft.String())
	}
}

// TestDispatchServerRead D310：服务端读链接经 ServerRead 出口呈现（正文不进客户端），
// 草稿先按工具边界落段。
func TestDispatchServerRead(t *testing.T) {
	var tr Transcript
	r := &recorder{}
	s := r.sink(&tr)

	tr.Dispatch(port.DeltaEvent{Delta: port.Delta{Text: "head"}}, s)
	tr.Dispatch(port.DeltaEvent{Delta: port.Delta{Server: &port.ServerBlock{URL: "https://x/a"}}}, s)
	if len(r.serverRead) != 1 || r.serverRead[0] != i18n.Tf("ui.notice.server_read", "url", "https://x/a") {
		t.Fatalf("serverRead = %v", r.serverRead)
	}
	if len(r.segments) != 1 || r.segments[0] != "head" {
		t.Fatalf("读链接前的草稿应落段: %v", r.segments)
	}
	if tr.Draft.Len() != 0 {
		t.Fatalf("服务端增量不应进正文草稿: %q", tr.Draft.String())
	}
}

// TestDispatchNoticeSanitizedAndRouted D267：提示带 attention 标记分流、出口消毒。
func TestDispatchNoticeSanitizedAndRouted(t *testing.T) {
	var tr Transcript
	r := &recorder{}
	s := r.sink(&tr)

	tr.Dispatch(port.NoticeEvent{Text: "回执\x1b[2J行", Attention: false}, s)
	tr.Dispatch(port.NoticeEvent{Text: "截断\x07了", Attention: true}, s)
	if len(r.notices) != 2 {
		t.Fatalf("notices = %+v", r.notices)
	}
	if r.notices[0].attention || strings.Contains(r.notices[0].text, "\x1b") {
		t.Fatalf("回执 = %+v", r.notices[0])
	}
	if !r.notices[1].attention || strings.Contains(r.notices[1].text, "\x07") {
		t.Fatalf("attention 提示 = %+v", r.notices[1])
	}

	// 错误同样消毒。
	tr.Dispatch(port.ErrorEvent{Err: strErr("boom\x1b[31m")}, s)
	if len(r.errors) != 1 || strings.Contains(r.errors[0], "\x1b") || !strings.Contains(r.errors[0], "boom") {
		t.Fatalf("errors = %v", r.errors)
	}
}

type strErr string

func (e strErr) Error() string { return string(e) }

// TestDispatchClearAndCommitted D75/D95：清屏重置全部语义状态并通知前端；提交与
// 回放节点原样转交。
func TestDispatchClearAndCommitted(t *testing.T) {
	tr := &Transcript{Usage: conversation.Usage{InputTokens: 5}}
	tr.Flushed = []int{3}
	r := &recorder{}
	s := r.sink(tr)

	tr.DraftDelta("未定稿")
	tr.ThinkDelta("想着")
	msg := conversation.Message{Role: conversation.RoleAssistant, ID: "a1"}
	tr.Dispatch(port.CommittedEvent{Message: msg}, s)
	if len(r.committed) != 1 || r.committed[0].ID != "a1" {
		t.Fatalf("committed = %+v", r.committed)
	}

	tr.Dispatch(port.ClearEvent{}, s)
	if r.cleared != 1 || tr.Draft.Len() != 0 || tr.Drafting || tr.Think.Len() != 0 ||
		len(tr.Flushed) != 0 || tr.Usage.InputTokens != 0 {
		t.Fatalf("清屏后状态未归零: cleared=%d draft=%q think=%q flushed=%v usage=%+v",
			r.cleared, tr.Draft.String(), tr.Think.String(), tr.Flushed, tr.Usage)
	}

	// 未识别事件走 Unhandled 兜底。
	unknown := port.OpenViewEvent{Plugin: "p", ViewID: "v"}
	tr.Dispatch(unknown, s)
	if len(r.unhandled) != 1 || r.unhandled[0] != port.Event(unknown) {
		t.Fatalf("unhandled = %v", r.unhandled)
	}
}

// imgsEqual 断言段的图片收列恰为给定引用。
func imgsEqual(img any, want ...string) bool {
	items, ok := img.([]any)
	if !ok || len(items) != len(want) {
		return false
	}
	for i, it := range items {
		if it.(string) != want[i] {
			return false
		}
	}
	return true
}

// TestDispatchSummaryNotice 摘要生成流标注：首条摘要增量发一次「正在生成摘要」，
// 同一段流不重复发；摘要流结束后再来普通增量，下一段摘要流再发一次。
func TestDispatchSummaryNotice(t *testing.T) {
	r := &recorder{}
	tr := &Transcript{}
	s := r.sink(tr)
	sum := port.DeltaEvent{Delta: port.Delta{Text: "a"}, Summary: true}
	tr.Dispatch(sum, s)
	tr.Dispatch(sum, s)
	tr.Dispatch(port.DeltaEvent{Delta: port.Delta{Text: "answer"}}, s)
	tr.Dispatch(sum, s)
	if len(r.notices) != 2 {
		t.Fatalf("notices = %d, want 2（每段摘要流一条）: %+v", len(r.notices), r.notices)
	}
	for _, n := range r.notices {
		if n.text != i18n.T("compact.generating") || !n.attention {
			t.Fatalf("notice = %+v", n)
		}
	}
}
