package app

// session_summary_test.go D152：/summary 两档（手写直插 / 模型总结后插入）、
// 目标约束、失败不落树、水位效果与清屏回放。

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Tonyjh07/Aquarius/internal/domain/conversation"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

// summaryFixture 建 [root,persona,user,assistant] 的四节点会话，返回会话与两个可见节点。
func summaryFixture(t *testing.T, store port.ConversationStore,
	streams ...*scriptStream) (*Session, *scriptLLM, *recorder, conversation.Message, conversation.Message) {
	t.Helper()
	s, llm, rec := newTestSession(t, store, streams...)
	if _, err := s.Handle(context.Background(), port.UserInput{Text: "hi"}); err != nil {
		t.Fatalf("handle: %v", err)
	}
	path := s.Current().Path()
	if len(path) != 4 {
		t.Fatalf("path = %v, want [root persona user assistant]", roles(path))
	}
	return s, llm, rec, path[2], path[3]
}

// TestSessionSummaryHandwritten 手写档：摘要插到目标之前（目标仍在摘要之后），落盘，
// 无模型调用，Head 不变，清屏回放。
func TestSessionSummaryHandwritten(t *testing.T) {
	store := newMemStore()
	s, llm, rec, user, assistant := summaryFixture(t, store, textStream("模型回复"))
	pathBefore := len(s.Current().Path())

	out, err := handleCmd(s, "summary", string(assistant.ID), "手写", "摘要")
	if err != nil {
		t.Fatalf("summary: %v", err)
	}
	if !strings.Contains(out, "已在 "+string(assistant.ID)+" 之前插入摘要（手写）") {
		t.Fatalf("out = %q", out)
	}
	if len(llm.requests) != 1 { // 只有首轮回复那一次；手写档不调模型
		t.Fatalf("llm requests = %d, want 1", len(llm.requests))
	}
	path := s.Current().Path()
	if len(path) != pathBefore+1 {
		t.Fatalf("path = %v, want 长度 +1", roles(path))
	}
	pad := path[3]
	if pad.Role != conversation.RoleSystem || pad.Content[0].Text != "手写 摘要" {
		t.Fatalf("pad = %+v, want system 摘要", pad)
	}
	if path[4].ID != assistant.ID || path[2].ID != user.ID {
		t.Fatalf("path = %v, want 摘要夹在 user 与 assistant 之间", roles(path))
	}
	if s.Current().Head != assistant.ID {
		t.Fatalf("head = %s, want 不变 %s", s.Current().Head, assistant.ID)
	}
	if _, ok := store.convs[s.Current().ID].Find(pad.ID); !ok {
		t.Fatal("摘要节点应已落盘")
	}
	if got := eventNames(rec.events); !contains(got, "clear") {
		t.Fatalf("events = %v, want 清屏回放", got)
	}
	if err := s.Current().Validate(); err != nil {
		t.Fatalf("validate: %v", err)
	}
}

// TestSessionSummaryModel 模型档：总结「目标之上的历史」后插入；入树节点带 Model/Usage 记账；
// 目标自身的内容不进摘要输入（它留在水位之后）。
func TestSessionSummaryModel(t *testing.T) {
	store := newMemStore()
	s, llm, _, _, assistant := summaryFixture(t, store,
		textStream("模型回复"),
		withUsage(textStream("## Objective\n- 模型摘要"), conversation.Usage{InputTokens: 40, OutputTokens: 15}),
	)

	out, err := handleCmd(s, "summary", string(assistant.ID))
	if err != nil {
		t.Fatalf("summary: %v", err)
	}
	if !strings.Contains(out, "已在 "+string(assistant.ID)+" 之前插入摘要（in=40 out=15 tokens）") {
		t.Fatalf("out = %q", out)
	}
	pad := s.Current().Path()[3]
	if pad.Role != conversation.RoleSystem || pad.Content[0].Text != "## Objective\n- 模型摘要" {
		t.Fatalf("pad = %+v", pad)
	}
	if pad.Model == "" || pad.Usage.InputTokens != 40 || pad.Outcome != conversation.OutcomeDone {
		t.Fatalf("pad 缺记账: %+v", pad)
	}
	// 摘要输入 = 目标之上的历史（persona + 用户消息），不含目标自身。
	req := llm.requests[1]
	if txt := reqText(req); !strings.Contains(txt, "Do not mention the summary process") || !strings.Contains(txt, "hi") {
		t.Fatalf("摘要请求不符合压缩提示词口径:\n%s", txt)
	}
	if strings.Contains(reqText(req), "模型回复") {
		t.Fatal("目标节点自身的内容不得进摘要输入（它留在水位之后）")
	}
	if _, ok := store.convs[s.Current().ID].Find(pad.ID); !ok {
		t.Fatal("摘要节点应已落盘")
	}
}

// TestSessionSummarySecondRunShortCircuits 水位之上无新历史：第二次对同一目标 /summary
// 不再发起生成、会话树不动（模型档短路在生成之前）。
func TestSessionSummarySecondRunShortCircuits(t *testing.T) {
	store := newMemStore()
	s, llm, _, _, assistant := summaryFixture(t, store,
		textStream("模型回复"),
		textStream("## Objective\n- 模型摘要"),
	)
	if _, err := handleCmd(s, "summary", string(assistant.ID)); err != nil {
		t.Fatalf("summary: %v", err)
	}
	before := s.Current().NodeCount()
	reqs := len(llm.requests)

	out, err := handleCmd(s, "summary", string(assistant.ID))
	if err != nil || !strings.Contains(out, "没有可总结的历史") {
		t.Fatalf("second summary = %q, %v", out, err)
	}
	if len(llm.requests) != reqs {
		t.Fatalf("llm requests = %d, want %d（无可总结内容不得发起生成）", len(llm.requests), reqs)
	}
	if s.Current().NodeCount() != before {
		t.Fatalf("nodes = %d, want %d", s.Current().NodeCount(), before)
	}
}

// TestSessionSummaryRejects 目标约束与用法错误：根、顶层节点（含人格）一律拒；
// 未知节点报错；失败不改树。
func TestSessionSummaryRejects(t *testing.T) {
	store := newMemStore()
	s, _, _, user, _ := summaryFixture(t, store, textStream("模型回复"))
	persona := s.Current().Path()[1]
	before := s.Current().NodeCount()

	if _, err := handleCmd(s, "summary"); err == nil || !strings.Contains(err.Error(), "用法") {
		t.Fatalf("无参数 = %v, want 用法提示", err)
	}
	for _, tc := range []struct {
		name   string
		id     conversation.MessageID
		substr string
	}{
		{"根节点", conversation.MessageID(s.Current().ID), "不能在会话根或会话首节点之前插入摘要"},
		{"人格节点", persona.ID, "不能在会话根或会话首节点之前插入摘要"},
		{"未知节点", "nope", "没有节点"},
	} {
		_, err := handleCmd(s, "summary", string(tc.id), "文本")
		if err == nil || !strings.Contains(err.Error(), tc.substr) {
			t.Fatalf("%s: err = %v, want 含 %q", tc.name, err, tc.substr)
		}
		if s.Current().NodeCount() != before {
			t.Fatalf("%s: 失败不得改动会话树", tc.name)
		}
	}

	// 首条 user 消息之前可以手写插入：人格快照之后、任何往来之前都是合法水位位。
	if _, err := handleCmd(s, "summary", string(user.ID), "开篇摘要"); err != nil {
		t.Fatalf("summary before first user message: %v", err)
	}
	path := s.Current().Path()
	if len(path) != 5 || path[2].Content[0].Text != "开篇摘要" || path[3].ID != user.ID {
		t.Fatalf("path = %v, want [root persona 开篇摘要 user assistant]", roles(path))
	}
}

// TestSessionSummaryModelFailureKeepsTree 生成失败（含模板不合格、取消、传输错误）
// 一律只报错：会话树与落盘状态都不动。
func TestSessionSummaryModelFailureKeepsTree(t *testing.T) {
	cases := []struct {
		name    string
		streams []*scriptStream // 空输出/缺模板会带提醒重试一次 → 两次生成
		want    string
	}{
		{"空输出", []*scriptStream{textStream("   "), textStream("   ")}, "empty output"},
		{"缺模板小节", []*scriptStream{textStream("随便写点什么"), textStream("还是随便写")}, "summary template"},
		{"取消", []*scriptStream{{steps: []scriptStep{{err: context.Canceled}}}}, "已取消插入摘要"},
	}
	for _, tc := range cases {
		store := newMemStore()
		s, _, _, _, assistant := summaryFixture(t, store, append([]*scriptStream{textStream("模型回复")}, tc.streams...)...)
		before := s.Current().NodeCount()

		out, err := handleCmd(s, "summary", string(assistant.ID))
		if err == nil {
			if !strings.Contains(out, tc.want) {
				t.Fatalf("%s: out = %q, want 含 %q", tc.name, out, tc.want)
			}
		} else if !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("%s: err = %v, want 含 %q", tc.name, err, tc.want)
		}
		if s.Current().NodeCount() != before {
			t.Fatalf("%s: 失败不得改动会话树", tc.name)
		}
		if store.convs[s.Current().ID].NodeCount() != before {
			t.Fatalf("%s: 失败不得落盘", tc.name)
		}
	}
}

// TestSessionSummaryTransportErrorFails 传输错误原样上抛（不吞成友好文案）。
func TestSessionSummaryTransportErrorFails(t *testing.T) {
	store := newMemStore()
	s, llm, _, _, assistant := summaryFixture(t, store, textStream("模型回复"))
	llm.genErr = errors.New("boom")

	if _, err := handleCmd(s, "summary", string(assistant.ID)); err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("err = %v, want boom", err)
	}
}

// TestSessionSummaryWaterlineApplies 插入摘要后装配只回传到它：其后往来的装配请求里
// 不再出现被吸收的历史（水位生效）。
func TestSessionSummaryWaterlineApplies(t *testing.T) {
	store := newMemStore()
	s, llm, _, _, assistant := summaryFixture(t, store,
		textStream("模型回复"),
		textStream("## Objective\n- 模型摘要"),
		textStream("新回答"),
	)
	if _, err := handleCmd(s, "summary", string(assistant.ID)); err != nil {
		t.Fatalf("summary: %v", err)
	}
	// 水位之后继续对话：装配应只带 persona + 摘要 + 其后往来，"hi" 不再回传。
	if _, err := s.Handle(context.Background(), port.UserInput{Text: "继续"}); err != nil {
		t.Fatalf("continue: %v", err)
	}
	txt := reqText(llm.requests[len(llm.requests)-1])
	if !strings.Contains(txt, "模型摘要") || !strings.Contains(txt, "继续") {
		t.Fatalf("装配缺摘要/新往来:\n%s", txt)
	}
	if strings.Contains(txt, "hi") {
		t.Fatalf("摘要之上的历史仍被回传（水位未生效）:\n%s", txt)
	}
}

// TestSessionSummaryRejectsCoveredTarget 目标之下已有摘要：新摘要会落在旧摘要之上，
// 装配水位仍停在旧摘要——插了等于没插（模型档还白花一次生成），必须拦在动手之前。
func TestSessionSummaryRejectsCoveredTarget(t *testing.T) {
	store := newMemStore()
	s, llm, _, user, assistant := summaryFixture(t, store, textStream("模型回复"))
	if _, err := handleCmd(s, "summary", string(assistant.ID), "旧摘要"); err != nil {
		t.Fatalf("first summary: %v", err)
	}
	pad := s.Current().Path()[3]
	before := s.Current().NodeCount()
	reqs := len(llm.requests)

	for _, tc := range []struct {
		name string
		id   conversation.MessageID
	}{
		{"目标在旧摘要之上", user.ID},
		{"目标就是旧摘要", pad.ID},
	} {
		for _, model := range []bool{false, true} {
			args := []string{string(tc.id)}
			if !model {
				args = append(args, "新摘要")
			}
			_, err := handleCmd(s, "summary", args...)
			if err == nil || !strings.Contains(err.Error(), "已被摘要覆盖") {
				t.Fatalf("%s（模型档=%v）: err = %v, want 已被摘要覆盖", tc.name, model, err)
			}
			if s.Current().NodeCount() != before {
				t.Fatalf("%s: 拒绝不得改动会话树", tc.name)
			}
		}
	}
	if len(llm.requests) != reqs {
		t.Fatalf("llm requests = %d, want %d（拦在生成之前，不白花钱）", len(llm.requests), reqs)
	}

	// 旧摘要之后的节点仍可插：新摘要才是路径上最后一个 system。
	if _, err := handleCmd(s, "summary", string(assistant.ID), "新摘要"); err != nil {
		t.Fatalf("summary below old watermark: %v", err)
	}
	path := s.Current().Path()
	if got := path[len(path)-2]; got.Role != conversation.RoleSystem || got.Content[0].Text != "新摘要" {
		t.Fatalf("新摘要应紧贴在 assistant 之前: %v", roles(path))
	}
}

// TestSummaryPadKeepsBranchSlots 插摘要不把原来同级的分叉拆散：a1/a2 仍是同一问题的两个
// 版本（1/2、2/2），摘要自己不占槽位——它是衬垫，不是可切换的版本。
func TestSummaryPadKeepsBranchSlots(t *testing.T) {
	c := conversation.New(conversation.ID("convPad"), "衬垫")
	root := conversation.MessageID(c.ID)
	txt := func(s string) []conversation.Part {
		return []conversation.Part{{Kind: conversation.PartText, Text: s}}
	}
	commit := func(id string, parent conversation.MessageID, role conversation.Role, at time.Time) {
		t.Helper()
		if err := c.AppendCommitted(conversation.Message{
			ID: conversation.MessageID(id), Parent: parent, Role: role,
			Content: txt(id), CreatedAt: at,
		}); err != nil {
			t.Fatalf("commit %s: %v", id, err)
		}
	}
	commit("p", root, conversation.RoleSystem, testTime)
	commit("u1", "p", conversation.RoleUser, testTime.Add(time.Second))
	commit("a1", "u1", conversation.RoleAssistant, testTime.Add(2*time.Second))
	commit("a2", "u1", conversation.RoleAssistant, testTime.Add(3*time.Second))
	pad, err := c.InsertBefore("a1", conversation.RoleSystem, txt("摘要"))
	if err != nil {
		t.Fatalf("insert summary: %v", err)
	}

	snap := makeTreeSnapshot(c)
	if bi := snap.branches["a1"]; len(bi.IDs) != 2 || bi.IDs[0] != "a1" || bi.IDs[1] != "a2" || bi.Index != 0 {
		t.Fatalf("Branches(a1) = %+v, want [a1 a2] 且 a1 在下标 0（摘要不拆散版本组）", bi)
	}
	if bi := snap.branches["a2"]; bi.Index != 1 || len(bi.IDs) != 2 {
		t.Fatalf("Branches(a2) = %+v, want 下标 1、共 2 个版本", bi)
	}
	if bi, ok := snap.branches[pad.ID]; ok {
		t.Fatalf("摘要衬垫不该有版本视图，got %+v", bi)
	}
	// 图上同理：版本编号只给真正的版本，衬垫不给。
	for _, n := range snap.graph.Nodes {
		switch n.ID {
		case "a1":
			if n.SiblingIdx != 0 || n.SiblingCount != 2 {
				t.Fatalf("a1 版本 = %d/%d, want 0/2", n.SiblingIdx, n.SiblingCount)
			}
		case "a2":
			if n.SiblingIdx != 1 || n.SiblingCount != 2 {
				t.Fatalf("a2 版本 = %d/%d, want 1/2", n.SiblingIdx, n.SiblingCount)
			}
		case pad.ID:
			if n.SiblingCount != 0 {
				t.Fatalf("摘要衬垫的版本数 = %d, want 0（不属于任何版本组）", n.SiblingCount)
			}
		}
	}
	if err := c.Validate(); err != nil {
		t.Fatalf("validate: %v", err)
	}
}

// TestSessionSummaryKeepsBranchStrip /summary 之后命令行与图上口径一致：/branch a2 把 a1
// 列为同级分叉，不把摘要衬垫当成分叉。
func TestSessionSummaryKeepsBranchStrip(t *testing.T) {
	store := newMemStore()
	s, _ := newConfirmedSession(t, store, nil)
	c := buildTree(t, s) // p → u1 → a1 → u2
	if err := c.Checkout("u1"); err != nil {
		t.Fatalf("checkout: %v", err)
	}
	if _, err := c.Append(conversation.RoleAssistant, []conversation.Part{{Kind: conversation.PartText, Text: "并列回答"}}); err != nil {
		t.Fatalf("append a2: %v", err)
	}
	s.publishTree()
	a2 := c.Head

	out, err := handleCmd(s, "summary", "a1", "摘要")
	if err != nil {
		t.Fatalf("summary: %v", err)
	}
	// Head 在 a2 那条分支上，故摘要不在当前路径里：它挂在 u1 下（衬垫占 a1 的原位）。
	var pad conversation.Message
	for _, kid := range c.ChildIDs("u1") {
		if m, ok := c.Find(kid); ok && m.Role == conversation.RoleSystem {
			pad = m
		}
	}
	if pad.ID == "" {
		t.Fatal("摘要节点未插到 u1 下")
	}

	out, err = handleCmd(s, "branch", string(a2))
	if err != nil {
		t.Fatalf("branch: %v", err)
	}
	if !strings.Contains(out, "同级分叉（1 条") || !strings.Contains(out, "a1 [assistant] 回复") {
		t.Fatalf("/branch 应把 a1 列为同级分叉:\n%s", out)
	}
	if strings.Contains(out, string(pad.ID)) {
		t.Fatalf("/branch 不该把摘要衬垫列为分叉:\n%s", out)
	}
}

// contains 查字符串切片是否含目标（断言用）。
func contains(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}
