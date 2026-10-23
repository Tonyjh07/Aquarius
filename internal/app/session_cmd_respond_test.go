package app

// session_cmd_respond_test.go /respond：从任意非根节点继续生成一条响应——
// Head 移到该节点后跑一轮（发送内容 = 水位 → 该节点），产物是该节点的 assistant 子节点；
// 已有子节点则开新分支（旧分支保留）；会话根拒绝。

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/Tonyjh07/Aquarius/internal/domain/conversation"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

// respondFixture 建 [root,persona,user,assistant] 会话（Head = assistant 叶子）。
func respondFixture(t *testing.T, streams ...*scriptStream) (*Session, *scriptLLM, *recorder) {
	t.Helper()
	s, llm, rec := newTestSession(t, newMemStore(), streams...)
	if _, err := s.Handle(context.Background(), port.UserInput{Text: "hi"}); err != nil {
		t.Fatalf("handle: %v", err)
	}
	path := s.Current().Path()
	if len(path) != 4 {
		t.Fatalf("path = %v, want [root persona user assistant]", roles(path))
	}
	return s, llm, rec
}

// TestSessionRespondLeaf 叶子节点（无子节点）：Head 移过去后原样追加一条 assistant 子节点，
// 不产生分叉；清屏 + 回放（与 /goto 同口径）。
func TestSessionRespondLeaf(t *testing.T) {
	store := newMemStore()
	s, llm, rec := newTestSession(t, store, textStream("首答"), textStream("续答"))
	if _, err := s.Handle(context.Background(), port.UserInput{Text: "hi"}); err != nil {
		t.Fatalf("首轮: %v", err)
	}
	assistant := s.Current().Head
	kidsBefore := len(s.Current().ChildIDs(assistant))
	mark := len(rec.events) // 只看 respond 这一轮

	out, err := handleCmd(s, "respond", string(assistant))
	if err != nil {
		t.Fatalf("respond: %v", err)
	}
	if !strings.Contains(out, "已从该节点生成响应") {
		t.Fatalf("out = %q", out)
	}
	c := s.Current()
	if c.Head == assistant {
		t.Fatalf("Head 未前移：%s", c.Head)
	}
	born := nodeOf(t, c, c.Head)
	if born.Parent != assistant || born.Role != conversation.RoleAssistant {
		t.Fatalf("产物 = %+v, want assistant 子节点 of %s", born, assistant)
	}
	if len(c.ChildIDs(assistant)) != kidsBefore+1 {
		t.Fatalf("子节点数 = %d, want %d（叶子追加，不分叉）", len(c.ChildIDs(assistant)), kidsBefore+1)
	}
	if err := c.Validate(); err != nil {
		t.Fatalf("validate: %v", err)
	}
	if len(llm.requests) != 2 {
		t.Fatalf("llm requests = %d, want 2", len(llm.requests))
	}
	if saved := store.convs[c.ID]; saved == nil || saved.Head != c.Head {
		t.Fatalf("saved = %+v, want 已落盘 Head=%s", saved, c.Head)
	}
	if !slices.Contains(eventNames(rec.events[mark:]), "clear") {
		t.Fatal("respond 未清屏（转写区须与 Head 一致）")
	}
}

// TestSessionRespondSendsWaterlineToTarget 关键口径：发往 API 的内容是「水位 → 所选节点」。
// 在有回答的 user 节点上 respond，装配请求须含该 user 文本、不含其后旧的 assistant 回答，
// 产物与之同级（旧分支保留）。
func TestSessionRespondSendsWaterlineToTarget(t *testing.T) {
	s, llm, _ := respondFixture(t, textStream("旧回答"), textStream("新回答"))
	path := s.Current().Path()
	user, oldAssistant := path[2], path[3]

	out, err := handleCmd(s, "respond", string(user.ID))
	if err != nil {
		t.Fatalf("respond: %v", err)
	}
	if !strings.Contains(out, "已从该节点生成响应") {
		t.Fatalf("out = %q", out)
	}
	req := llm.requests[len(llm.requests)-1]
	var joined strings.Builder
	for _, m := range req.Messages {
		for _, p := range m.Content {
			joined.WriteString(p.Text)
		}
	}
	got := joined.String()
	if !strings.Contains(got, "hi") {
		t.Fatalf("装配请求未含所选节点正文: %q", got)
	}
	if strings.Contains(got, "旧回答") {
		t.Fatalf("装配请求含了所选节点之后的旧回答（水位口径错）: %q", got)
	}

	c := s.Current()
	born := nodeOf(t, c, c.Head)
	if born.Parent != user.ID || born.Role != conversation.RoleAssistant {
		t.Fatalf("产物 = %+v, want assistant 子节点 of %s", born, user.ID)
	}
	// 旧回答仍是 user 的孩子（旧分支保留）。
	found := false
	for _, k := range c.ChildIDs(user.ID) {
		if k == oldAssistant.ID {
			found = true
		}
	}
	if !found {
		t.Fatalf("旧回答 %s 未保留为分支", oldAssistant.ID)
	}
	if err := c.Validate(); err != nil {
		t.Fatalf("validate: %v", err)
	}
}

// TestSessionRespondRejectsRoot 会话根即会话本身，作为生成起点拒绝；失败不改树。
func TestSessionRespondRejectsRoot(t *testing.T) {
	s, _, _ := respondFixture(t, textStream("首答"))
	root := conversation.MessageID(s.Current().ID)
	before := s.Current().NodeCount()

	_, err := handleCmd(s, "respond", string(root))
	if err == nil || !strings.Contains(err.Error(), "会话根") {
		t.Fatalf("err = %v, want 拒绝会话根", err)
	}
	if s.Current().NodeCount() != before {
		t.Fatalf("失败不得改动会话树：节点数 %d → %d", before, s.Current().NodeCount())
	}
}

// TestSessionRespondUsage 用法/未知节点错误路径。
func TestSessionRespondUsage(t *testing.T) {
	s, _, _ := respondFixture(t, textStream("首答"))
	if _, err := handleCmd(s, "respond"); err == nil || !strings.Contains(err.Error(), "用法") {
		t.Fatalf("无参 err = %v, want 用法", err)
	}
	if _, err := handleCmd(s, "respond", "nope"); err == nil || !strings.Contains(err.Error(), "没有节点") {
		t.Fatalf("未知节点 err = %v", err)
	}
}
