package app

// contextview_test.go `/context`（§7.1）：视图必须与实发请求逐字节同源。
//
// 这一组测试守的是「别长出第二套装配」——视图一旦自己重拼上下文，它给出的账就不再是
// 发出去的那份，而这种分叉不会以崩溃的形式暴露，只会慢慢变成误导。

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/Tonyjh07/Aquarius/internal/domain/conversation"
	"github.com/Tonyjh07/Aquarius/internal/i18n"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

// appendUserText 往当前会话的 Head 上挂一条用户消息（不跑轮次）。
func appendUserText(t *testing.T, s *Session, text string) {
	t.Helper()
	if _, err := s.cur.Append(conversation.RoleUser, []conversation.Part{{Kind: conversation.PartText, Text: text}}); err != nil {
		t.Fatalf("append user: %v", err)
	}
}

// TestContextReportMatchesRequest 段正文拼起来 = 请求正文；合计 = /usage 的估算。
func TestContextReportMatchesRequest(t *testing.T) {
	rec := &recorder{}
	a := newAgent(t, &scriptLLM{t: t}, rec, Deps{}, Config{})
	c := newConv(t)
	ctx := context.Background()

	req, err := a.buildRequest(ctx, c, nil)
	if err != nil {
		t.Fatalf("buildRequest: %v", err)
	}
	rep, err := a.ContextReport(ctx, c)
	if err != nil {
		t.Fatalf("ContextReport: %v", err)
	}

	// 每条消息都要有段，且段文本拼起来与该消息正文完全相同。
	got := map[int]string{}
	for _, sec := range rep.Sections {
		got[sec.Msg] += sec.Text
	}
	if len(got) != len(req.Messages) {
		t.Fatalf("覆盖 %d 条消息，请求里有 %d 条", len(got), len(req.Messages))
	}
	for i, msg := range req.Messages {
		want, _, _ := partsText(msg.Content)
		if strings.ReplaceAll(got[i+1], "\n", "") != strings.ReplaceAll(want, "\n", "") {
			t.Fatalf("第 %d 条消息的段正文与请求不一致：\n段 = %q\n请求 = %q", i+1, got[i+1], want)
		}
	}

	ur, err := a.UsageReport(ctx, c)
	if err != nil {
		t.Fatalf("UsageReport: %v", err)
	}
	if rep.Total != ur.Estimate {
		t.Fatalf("合计 %d 与 /usage 的 %d 不一致（两套装配）", rep.Total, ur.Estimate)
	}
	if rep.Budget != ur.MaxCtx || rep.CompactAt != ur.CompactAt {
		t.Fatalf("预算/压缩线 %d/%d 与 /usage 的 %d/%d 不一致",
			rep.Budget, rep.CompactAt, ur.MaxCtx, ur.CompactAt)
	}
}

// TestContextReportLabels 装配期追加的几段都要有各自的类别，而不是并成「第一条 system」。
func TestContextReportLabels(t *testing.T) {
	rec := &recorder{}
	a := newAgent(t, &scriptLLM{t: t}, rec, Deps{}, Config{})
	a.memory = &fakeMemory{docs: map[string]string{
		port.SessionMemoryDoc("conv"): "note body",
	}}
	c := newConv(t)
	rep, err := a.ContextReport(context.Background(), c)
	if err != nil {
		t.Fatalf("ContextReport: %v", err)
	}
	kinds := map[string]int{}
	for _, sec := range rep.Sections {
		if sec.Msg == 1 {
			kinds[sec.Kind]++
		}
	}
	for _, want := range []string{labelPersona, labelMemory, labelNote} {
		if kinds[want] == 0 {
			t.Fatalf("首条 system 缺 %q 段，实际 %v", want, kinds)
		}
	}
}

// TestContextSummaryAndBody 概览给块账，`/context <n>` 给该条全文。
func TestContextSummaryAndBody(t *testing.T) {
	store := newMemStore()
	s, _, _ := newTestSession(t, store)
	appendUserText(t, s, "hi")
	out, err := s.execContext(context.Background(), port.Command{})
	if err != nil {
		t.Fatalf("execContext: %v", err)
	}
	for _, want := range []string{"test-model", i18n.T("session.ctx.kind.persona"), i18n.T("session.ctx.kind.user"), "hi"} {
		if !strings.Contains(out, want) {
			t.Fatalf("概览缺 %q：\n%s", want, out)
		}
	}
	body, err := s.execContext(context.Background(), port.Command{Args: []string{"2"}})
	if err != nil {
		t.Fatalf("execContext 2: %v", err)
	}
	if !strings.Contains(body, "hi") {
		t.Fatalf("第 2 条正文里没有用户消息：%q", body)
	}
	if _, err := s.execContext(context.Background(), port.Command{Args: []string{"99"}}); err == nil {
		t.Fatal("越界编号应当报错")
	}
}

// TestContextDumpWritesFile `full` 落进导出目录，回执带路径。
func TestContextDumpWritesFile(t *testing.T) {
	dir := t.TempDir()
	store := newMemStore()
	rec := &recorder{}
	llm := &scriptLLM{t: t}
	agent := newAgent(t, llm, rec, Deps{}, Config{})
	s, err := NewSession(context.Background(), SessionDeps{
		Store: store, Agent: agent, IDs: &seqIDs{}, Clock: fixedClock{testTime},
		UI: rec, ExportsDir: dir,
	})
	if err != nil {
		t.Fatalf("new session: %v", err)
	}
	appendUserText(t, s, "hi")
	out, err := s.execContext(context.Background(), port.Command{Args: []string{"full"}})
	if err != nil {
		t.Fatalf("execContext full: %v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 1 {
		t.Fatalf("导出目录应有 1 个文件，实际 %v（err=%v）", entries, err)
	}
	data, err := os.ReadFile(dir + string(os.PathSeparator) + entries[0].Name())
	if err != nil {
		t.Fatalf("读回: %v", err)
	}
	if !strings.Contains(string(data), "hi") || !strings.Contains(string(data), i18n.T("session.ctx.kind.persona")) {
		t.Fatalf("dump 内容不完整：\n%s", data)
	}
	if !strings.Contains(out, entries[0].Name()) {
		t.Fatalf("回执里没有文件名：%q", out)
	}
	// 次窗事件与文件同源：内容必须逐字节相同，否则「看的」和「存的」会各说一套。
	var view *port.ContextViewEvent
	for _, ev := range rec.events {
		if cv, ok := ev.(port.ContextViewEvent); ok {
			view = &cv
		}
	}
	if view == nil {
		t.Fatal("没有投出上下文全文事件")
	}
	if view.Text != string(data) {
		t.Fatalf("次窗内容与文件不一致：view=%d 字节 file=%d 字节", len(view.Text), len(data))
	}
	if view.Title == "" {
		t.Fatal("次窗标题为空")
	}
}

// TestContextLastAfterRun `last` 取的是上一轮真正发出去的请求。
func TestContextLastAfterRun(t *testing.T) {
	rec := &recorder{}
	llm := &scriptLLM{t: t, streams: []*scriptStream{textStream("pong")}}
	a := newAgent(t, llm, rec, Deps{}, Config{})
	c := newConv(t)
	if err := a.Run(context.Background(), c); err != nil {
		t.Fatalf("run: %v", err)
	}
	if _, ok := a.LastContextReport(context.Background()); !ok {
		t.Fatal("跑过一轮后应当有实发快照")
	}
	rep, _ := a.LastContextReport(context.Background())
	last := llm.requests[len(llm.requests)-1]
	if len(rep.Sections) == 0 || rep.Model != last.Model {
		t.Fatalf("快照与实发请求不一致：%+v", rep)
	}
	var text strings.Builder
	for _, sec := range rep.Sections {
		text.WriteString(sec.Text)
	}
	if !strings.Contains(text.String(), "hi") {
		t.Fatal("实发快照里没有本轮的用户消息")
	}
}

// TestContextLastEmpty 没发过请求时 `last` 明确报错，而不是给一份空概览。
func TestContextLastEmpty(t *testing.T) {
	store := newMemStore()
	s, _, _ := newTestSession(t, store)
	if _, err := s.execContext(context.Background(), port.Command{Args: []string{"last"}}); err == nil {
		t.Fatal("没有实发请求时应当报错")
	}
}

// TestShiftMarks 裁剪后的标签平移：前导 system 内部不动、其后前移、被删区间丢弃。
func TestShiftMarks(t *testing.T) {
	in := []sectionMark{
		{Msg: 0, Part: 0, Kind: labelPersona},
		{Msg: 0, Part: 1, Kind: labelNote},
		{Msg: 1, Part: -1, Kind: labelUser},
		{Msg: 2, Part: -1, Kind: labelAssistant},
		{Msg: 3, Part: -1, Kind: labelUser},
	}
	got := shiftMarks(in, 1, 2) // lead=1（首条 system），删掉下标 1、2
	if len(got) != 3 {
		t.Fatalf("应剩 3 条标签，实际 %d：%+v", len(got), got)
	}
	if got[0].Part != 0 || got[1].Part != 1 {
		t.Fatalf("前导 system 内部的标签被改动了：%+v", got[:2])
	}
	if got[2].Msg != 1 {
		t.Fatalf("尾部下标应平移到 1，实际 %d", got[2].Msg)
	}
	if n := shiftMarks(in, 1, 0); len(n) != len(in) {
		t.Fatalf("没删东西时不该动：%d != %d", len(n), len(in))
	}
}

// TestContextSnapshotAfterCompact 自动压缩换掉请求时，快照跟着换（不拿旧标签索引新消息）。
func TestContextSnapshotAfterCompact(t *testing.T) {
	rec := &recorder{}
	llm := &scriptLLM{t: t, streams: []*scriptStream{textStream("ok")}}
	a := newAgent(t, llm, rec, Deps{}, Config{})
	c := newConv(t)
	req, marks, err := a.buildRequestTracked(context.Background(), c, nil)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	// 造一次「裁剪后」的调用路径：把消息砍到只剩首条 system 与最后一条。
	trimmed := append([]port.PromptMessage{}, req.Messages...)
	marks = shiftMarks(marks, 1, len(req.Messages)-2)
	trimmed = append(trimmed[:1], trimmed[len(trimmed)-1:]...)
	req.Messages = trimmed
	a.saveLast(req, marks)
	rep, ok := a.LastContextReport(context.Background())
	if !ok {
		t.Fatal("快照缺失")
	}
	for _, sec := range rep.Sections {
		if sec.Msg > len(trimmed) {
			t.Fatalf("标签指向已删除的消息：%+v（共 %d 条）", sec, len(trimmed))
		}
	}
	if len(rep.Sections) == 0 || rep.Sections[0].Kind != labelPersona {
		t.Fatalf("首段应仍是人格：%+v", rep.Sections)
	}
}

// 编译期锚：拼接正文的消息类型不随视图改动。
var _ = conversation.Message{}
