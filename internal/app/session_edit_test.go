package app

// session_edit_test.go 树交互之编辑：/goto、/edit（Fresh/Carry/--keep/--copy）、
// /regen 与重放。夹具（确认器会话、造树、命令调用、修订定位）也在这里，
// session_tree_test.go 共用。

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/Tonyjh07/Aquarius/internal/domain/conversation"
	"github.com/Tonyjh07/Aquarius/internal/domain/tool"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

// newConfirmedSession 组装带脚本确认器的会话。
func newConfirmedSession(t *testing.T, store port.ConversationStore, answers []bool) (*Session, *scriptConfirmer) {
	t.Helper()
	agent := newAgent(t, &scriptLLM{t: t}, &recorder{}, Deps{}, Config{})
	conf := &scriptConfirmer{t: t, answers: answers}
	s, err := NewSession(context.Background(), SessionDeps{
		Store:     store,
		Agent:     agent,
		IDs:       &seqIDs{},
		Clock:     fixedClock{testTime},
		Confirmer: conf,
	})
	if err != nil {
		t.Fatalf("new session: %v", err)
	}
	return s, conf
}

// buildTree 把一棵显式 ID 的树挂进会话（替换默认会话）：
// convT(root) → p(persona) → u1(user) → a1(assistant，正文+工具分片 k1{call,result}) → u2(user)；
// Head 在 u2。显式 ID 便于断言前缀解析与结构变化。
func buildTree(t *testing.T, s *Session) *conversation.Conversation {
	t.Helper()
	c := conversation.New(conversation.ID("convT"), "树交互")
	mk := func(m conversation.Message) conversation.Message {
		t.Helper()
		if err := c.AppendCommitted(m); err != nil {
			t.Fatalf("commit %s: %v", m.ID, err)
		}
		return nodeOf(t, c, m.ID)
	}
	txt := func(s string) []conversation.Part {
		return []conversation.Part{{Kind: conversation.PartText, Text: s}}
	}
	root := conversation.MessageID(c.ID)
	p := mk(conversation.Message{ID: "p", Parent: root, Role: conversation.RoleSystem, Content: txt("人格")})
	u1 := mk(conversation.Message{ID: "u1", Parent: p.ID, Role: conversation.RoleUser, Content: txt("问题")})
	mk(conversation.Message{
		ID: "a1", Parent: u1.ID, Role: conversation.RoleAssistant,
		Content: append(txt("回复"), conversation.Part{Kind: conversation.PartTool, Tool: &conversation.ToolPart{
			CallID: "k1", Name: "think", Args: json.RawMessage(`{}`),
			Result: &tool.Result{CallID: "k1", OK: true, Output: "思考完毕"},
		}}),
	})
	mk(conversation.Message{ID: "u2", Parent: "a1", Role: conversation.RoleUser, Content: txt("追问")})
	if err := c.Validate(); err != nil {
		t.Fatalf("build tree: %v", err)
	}
	s.cur = c
	s.publishTree() // D80/§7.5：换树后重发只读快照（Handle 外改 s.cur 时须手动发布）
	return c
}

// handleCmd 直接走命令入口。
func handleCmd(s *Session, name string, args ...string) (string, error) {
	return s.Handle(context.Background(), port.UserInput{Command: &port.Command{Name: name, Args: args}})
}

// revisedID 找出以 old 为旧版本的新节点 ID（反向查询：Edition 是从新到旧，
// 测试里通常已知旧节点要捞新节点，所以走快照遍历）。
func revisedID(c *conversation.Conversation, old conversation.MessageID) conversation.MessageID {
	for _, ed := range c.Snapshot().RevisedFrom {
		if ed.Old == old {
			return ed.New
		}
	}
	return ""
}

// TestSessionGotoCommand /goto：Head 导航 + 唯一前缀解析 + 落盘 + 错误路径。
func TestSessionGotoCommand(t *testing.T) {
	store := newMemStore()
	s, _ := newConfirmedSession(t, store, nil)
	c := buildTree(t, s)

	// 精确 id 并落盘（memStore 存副本，断言真实发生过 Save）。
	out, err := handleCmd(s, "goto", "a1")
	if err != nil || out != "Head → a1" {
		t.Fatalf("goto = %q, %v", out, err)
	}
	if c.Head != "a1" {
		t.Fatalf("head = %s, want a1", c.Head)
	}
	if saved := store.convs[c.ID]; saved == nil || saved.Head != "a1" {
		t.Fatalf("saved head = %+v, want 已落盘 a1", saved)
	}

	// 唯一前缀命中。
	if _, err := handleCmd(s, "goto", "u1"); err != nil {
		t.Fatalf("goto u1: %v", err)
	}
	if _, err := handleCmd(s, "goto", "a"); err != nil { // 仅 a1 一个候选
		t.Fatalf("goto 唯一前缀 a: %v", err)
	}

	// 前缀歧义 / 不存在 / 用法。
	if _, err := handleCmd(s, "goto", "u"); err == nil || !strings.Contains(err.Error(), "有歧义") {
		t.Fatalf("歧义 err = %v", err)
	}
	if _, err := handleCmd(s, "goto", "zz"); err == nil || !strings.Contains(err.Error(), "没有节点") {
		t.Fatalf("不存在 err = %v", err)
	}
	if _, err := handleCmd(s, "goto"); err == nil || !strings.Contains(err.Error(), "用法") {
		t.Fatalf("用法 err = %v", err)
	}

	// 回根：会话 ID 即 Root 节点（D19），前缀 "convT" 命中。
	if _, err := handleCmd(s, "goto", "convT"); err != nil || c.Head != conversation.MessageID(c.ID) {
		t.Fatalf("回根 = %v, head %s", err, c.Head)
	}
	if err := c.Validate(); err != nil {
		t.Fatalf("validate: %v", err)
	}
}

// TestSessionGotoClearsAndReplays D81：/goto 移 Head 后清屏 + 回放新路径——转写区
// 恒与 Head 一致（此前不回放是缺口：切分支后界面停在旧分支）。
func TestSessionGotoClearsAndReplays(t *testing.T) {
	ctx := context.Background()
	s, _, rec := newTestSession(t, newMemStore(), textStream("回复一"), textStream("回复二"))
	text := func(m conversation.Message) string {
		var b strings.Builder
		for _, p := range m.Content {
			b.WriteString(p.Text)
		}
		return b.String()
	}

	if _, err := s.Handle(ctx, port.UserInput{Text: "你好"}); err != nil {
		t.Fatalf("首轮: %v", err)
	}
	// path = [root persona user assistant]
	first := s.Current().Path()[3].ID

	// Revise 造出同级分叉（Head 移到新节点），旧回答 first 留作历史分支。
	if _, err := s.Handle(ctx, port.UserInput{Command: &port.Command{Name: "edit",
		Args: []string{string(first), "改过的回复"}}}); err != nil {
		t.Fatalf("/edit: %v", err)
	}
	sib := revisedID(s.Current(), first)
	if sib == "" || s.Current().Head != sib {
		t.Fatalf("edit 后 head = %s, want %s", s.Current().Head, sib)
	}

	// 切回旧分支：清屏 + 回放 first 所在路径（persona 之后 = user + first，2 条）。
	rec.events = nil
	out, err := s.Handle(ctx, port.UserInput{Command: &port.Command{Name: "goto", Args: []string{string(first)}}})
	if err != nil {
		t.Fatalf("/goto: %v", err)
	}
	if out != "Head → "+string(first) {
		t.Fatalf("goto 输出 = %q, want Head → %s", out, first)
	}
	if s.Current().Head != first {
		t.Fatalf("head = %s, want %s", s.Current().Head, first)
	}

	names := eventNames(rec.events)
	if len(names) != 4 || names[0] != "clear" || names[1] != "notice" || names[2] != "history" || names[3] != "history" {
		t.Fatalf("events = %v, want [clear notice history history]", names)
	}
	notice, ok := rec.events[1].(port.NoticeEvent)
	if !ok {
		t.Fatalf("第二个事件 = %T, want NoticeEvent", rec.events[1])
	}
	for _, want := range []string{"已切换分支至会话", "回放 2 条历史"} {
		if !strings.Contains(notice.Text, want) {
			t.Fatalf("notice = %q, want 含 %q", notice.Text, want)
		}
	}
	// 回放内容 = 新 Head 路径（含旧回答"回复一"，不含改后的"改过的回复"）。
	hist, ok := rec.events[3].(port.HistoryEvent)
	if !ok {
		t.Fatalf("末事件 = %T, want HistoryEvent", rec.events[3])
	}
	if hist.Message.ID != first {
		t.Fatalf("回放末节点 = %s, want %s", hist.Message.ID, first)
	}
	if got := text(hist.Message); !strings.Contains(got, "回复一") {
		t.Fatalf("回放文本 = %q, want 含旧分支内容「回复一」", got)
	}

	// 切回新分支：同样清屏回放，且回放内容换成改后的回复。
	rec.events = nil
	if _, err := s.Handle(ctx, port.UserInput{Command: &port.Command{Name: "goto", Args: []string{string(sib)}}}); err != nil {
		t.Fatalf("/goto 新分支: %v", err)
	}
	if names := eventNames(rec.events); len(names) < 2 || names[0] != "clear" || names[1] != "notice" {
		t.Fatalf("events = %v, want 以 [clear notice ...] 开头", names)
	}
	last, ok := rec.events[len(rec.events)-1].(port.HistoryEvent)
	if !ok {
		t.Fatalf("末事件 = %T, want HistoryEvent", rec.events[len(rec.events)-1])
	}
	if got := text(last.Message); !strings.Contains(got, "改过的回复") {
		t.Fatalf("回放文本 = %q, want 含新分支内容「改过的回复」", got)
	}
}

// TestSessionEditClearsAndReplays D92：/edit 修订后清屏 + 回放新路径——Head 移动类
// 命令统一口径（D81；此前只入树不回放，界面停在旧文本）。
func TestSessionEditClearsAndReplays(t *testing.T) {
	ctx := context.Background()
	s, _, rec := newTestSession(t, newMemStore(), textStream("回复一"))
	if _, err := s.Handle(ctx, port.UserInput{Text: "你好"}); err != nil {
		t.Fatalf("首轮: %v", err)
	}
	first := s.Current().Path()[3].ID

	rec.events = nil
	if _, err := s.Handle(ctx, port.UserInput{Command: &port.Command{Name: "edit",
		Args: []string{string(first), "改过的回复"}}}); err != nil {
		t.Fatalf("/edit: %v", err)
	}
	sib := revisedID(s.Current(), first)
	names := eventNames(rec.events)
	if len(names) != 4 || names[0] != "clear" || names[1] != "notice" || names[2] != "history" || names[3] != "history" {
		t.Fatalf("events = %v, want [clear notice history history]", names)
	}
	notice := rec.events[1].(port.NoticeEvent)
	if !strings.Contains(notice.Text, "已修订会话") || !strings.Contains(notice.Text, "回放 2 条历史") {
		t.Fatalf("notice = %q", notice.Text)
	}
	hist := rec.events[3].(port.HistoryEvent)
	if hist.Message.ID != sib || !strings.Contains(hist.Message.Content[0].Text, "改过的回复") {
		t.Fatalf("回放末节点 = %s %q, want 新分支「改过的回复」", hist.Message.ID, hist.Message.Content[0].Text)
	}
}

// TestSessionEditUserRegenerates D93：编辑用户消息（Fresh 分叉）= 改写并重新生成——
// 清屏回放后 Head 在新节点上直接重跑一轮；assistant 修订不触发生成（既有口径）。
func TestSessionEditUserRegenerates(t *testing.T) {
	ctx := context.Background()
	s, _, rec := newTestSession(t, newMemStore(), textStream("回复一"), textStream("改后回答"))
	if _, err := s.Handle(ctx, port.UserInput{Text: "你好"}); err != nil {
		t.Fatalf("首轮: %v", err)
	}
	userID := s.Current().Path()[2].ID

	rec.events = nil
	if _, err := s.Handle(ctx, port.UserInput{Command: &port.Command{Name: "edit",
		Args: []string{string(userID), "你好（改）"}}}); err != nil {
		t.Fatalf("/edit: %v", err)
	}
	c := s.Current()
	n := revisedID(c, userID)
	if n == "" || nodeOf(t, c, c.Head).Parent != n ||
		nodeOf(t, c, c.Head).Content[0].Text != "改后回答" {
		t.Fatalf("编辑后未重新生成: head=%s (%+v) n=%s", c.Head, nodeOf(t, c, c.Head), n)
	}
	names := eventNames(rec.events)
	if len(names) < 3 || names[0] != "clear" || names[1] != "notice" {
		t.Fatalf("events 前缀 = %v, want [clear notice ...]", names)
	}
	if !slices.Contains(names, "delta") || !slices.Contains(names, "committed") {
		t.Fatalf("events = %v, want 编辑后自动生成（delta/committed）", names)
	}

	// assistant 修订不触发生成。
	rec.events = nil
	asst := c.Path()[3].ID
	if _, err := s.Handle(ctx, port.UserInput{Command: &port.Command{Name: "edit",
		Args: []string{string(asst), "回答（改）"}}}); err != nil {
		t.Fatalf("/edit assistant: %v", err)
	}
	if names := eventNames(rec.events); slices.Contains(names, "delta") || slices.Contains(names, "committed") {
		t.Fatalf("assistant 修订不应触发生成: %v", names)
	}
}

// TestSessionRegenCommand D92 /regen：上游最近用户消息原样重发为同父兄弟（Fresh 分叉，
// 旧回答保留），清屏回放后重跑一轮生成。
func TestSessionRegenCommand(t *testing.T) {
	store := newMemStore()
	s, _, rec := newTestSession(t, store, textStream("新的回答"), textStream("重发回答"))
	c := buildTree(t, s)

	// regen 助手消息 a1：上游最近 user = u1，原样重发为同父兄弟节点。
	if _, err := handleCmd(s, "regen", "a1"); err != nil {
		t.Fatalf("/regen: %v", err)
	}
	nu := revisedID(c, "u1")
	if nu == "" {
		t.Fatal("RevisedFrom 应记录 新user→u1")
	}
	nuMsg := nodeOf(t, c, nu)
	if nuMsg.Parent != "p" || nuMsg.Role != conversation.RoleUser || nuMsg.Content[0].Text != "问题" {
		t.Fatalf("新 user 节点 = %+v, want 同父同角色原内容", nuMsg)
	}
	// 旧分支原样保留：u1 → a1 → t1 → u2 仍在。
	if got := c.ChildIDs("u1"); len(got) != 1 || got[0] != "a1" {
		t.Fatalf("children[u1] = %v, want 旧分支保留", got)
	}
	// 新回答生成在新 user 之下，Head 到位。
	if nodeOf(t, c, c.Head).Role != conversation.RoleAssistant || nodeOf(t, c, c.Head).Parent != nu {
		t.Fatalf("head = %s (parent %s), want 新 user 下的 assistant 回答", c.Head, nodeOf(t, c, c.Head).Parent)
	}
	if got := nodeOf(t, c, c.Head).Content[0].Text; got != "新的回答" {
		t.Fatalf("新回答 = %q, want 新的回答", got)
	}
	// 事件序：clear → notice → 回放新路径（nu）→ 新一轮 Turn 流式。
	names := eventNames(rec.events)
	if len(names) < 4 || names[0] != "clear" || names[1] != "notice" || names[2] != "history" {
		t.Fatalf("events 前缀 = %v, want [clear notice history ...]", names)
	}
	if !slices.Contains(names, "delta") || !slices.Contains(names, "committed") {
		t.Fatalf("events = %v, want 新一轮含 delta/committed", names)
	}
	hist := rec.events[2].(port.HistoryEvent)
	if hist.Message.ID != nu {
		t.Fatalf("回放节点 = %s, want %s", hist.Message.ID, nu)
	}
	if saved := store.convs[c.ID]; saved == nil || saved.Head != c.Head {
		t.Fatalf("saved head = %+v, want 已落盘", saved)
	}
	if err := c.Validate(); err != nil {
		t.Fatalf("validate: %v", err)
	}

	// regen 无回答的用户消息（D93）：不重复分叉——Head 移过去直接生成（u2 是 t1
	// 之下的无回答叶子，此前会造出同文本冗余兄弟）。
	if _, err := handleCmd(s, "regen", "u2"); err != nil {
		t.Fatalf("/regen u2: %v", err)
	}
	if revisedID(c, "u2") != "" {
		t.Fatal("无回答的 u2 不应分叉出冗余兄弟")
	}
	ans := nodeOf(t, c, c.Head)
	if ans.Parent != "u2" || ans.Role != conversation.RoleAssistant || ans.Content[0].Text != "重发回答" {
		t.Fatalf("u2 的新回答 = %+v, want 直接生成在 u2 之下", ans)
	}
	if err := c.Validate(); err != nil {
		t.Fatalf("validate: %v", err)
	}

	// 错误路径：用法 / 不存在 / 上游无用户消息（persona 与 Root）。
	if _, err := handleCmd(s, "regen"); err == nil || !strings.Contains(err.Error(), "用法") {
		t.Fatalf("用法 err = %v", err)
	}
	if _, err := handleCmd(s, "regen", "zz"); err == nil || !strings.Contains(err.Error(), "没有节点") {
		t.Fatalf("不存在 err = %v", err)
	}
	if _, err := handleCmd(s, "regen", "p"); err == nil || !strings.Contains(err.Error(), "上游没有用户消息") {
		t.Fatalf("persona err = %v", err)
	}
	if _, err := handleCmd(s, "regen", "convT"); err == nil || !strings.Contains(err.Error(), "上游没有用户消息") {
		t.Fatalf("root err = %v", err)
	}
}

// TestSessionEditFresh /edit 缺省 Fresh：同级新节点、旧分支原样保留、Head 移到新节点、
// 落盘；编辑用户消息即重新生成回答（D93），Head 落在新分支的回答上。
func TestSessionEditFresh(t *testing.T) {
	store := newMemStore()
	s, _, _ := newTestSession(t, store, textStream("新回答"))
	c := buildTree(t, s)

	out, err := handleCmd(s, "edit", "u1", "新问题")
	if err != nil {
		t.Fatalf("edit: %v", err)
	}
	n := revisedID(c, "u1")
	if n == "" {
		t.Fatal("RevisedFrom 应记录 新→旧")
	}
	if !strings.Contains(out, "fresh") || !strings.Contains(out, "旧分支保留") ||
		!strings.Contains(out, "已重新生成回答") || !strings.Contains(out, string(n)) {
		t.Fatalf("out = %q, want fresh/旧分支保留/已重新生成回答/新节点 id", out)
	}
	nm := nodeOf(t, c, n)
	if nm.Parent != "p" || nm.Role != conversation.RoleUser || nm.Content[0].Text != "新问题" {
		t.Fatalf("new node = %+v, want 同父同角色新内容", nm)
	}
	if got := c.ChildIDs("u1"); len(got) != 1 || got[0] != "a1" {
		t.Fatalf("children[u1] = %v, want 旧分支原样保留", got)
	}
	// Head = 新分支的回答（编辑即重发，D93）。
	if nodeOf(t, c, c.Head).Parent != n || nodeOf(t, c, c.Head).Role != conversation.RoleAssistant ||
		nodeOf(t, c, c.Head).Content[0].Text != "新回答" {
		t.Fatalf("head = %s (%+v), want 新节点下的回答", c.Head, nodeOf(t, c, c.Head))
	}
	if saved := store.convs[c.ID]; saved == nil || saved.Head != c.Head {
		t.Fatalf("saved = %+v, want 已落盘新 Head", saved)
	}
	if err := c.Validate(); err != nil {
		t.Fatalf("validate: %v", err)
	}
}

// TestSessionEditCarry /edit --keep：子树边转移到新节点，Head 留在被转移子树内（D2/D16），节点内容不变。
func TestSessionEditCarry(t *testing.T) {
	store := newMemStore()
	s, _ := newConfirmedSession(t, store, nil)
	c := buildTree(t, s)
	a1Before := nodeOf(t, c, "a1").Clone()
	u2Before := nodeOf(t, c, "u2").Clone()

	out, err := handleCmd(s, "edit", "u1", "--keep", "新问题")
	if err != nil {
		t.Fatalf("edit --keep: %v", err)
	}
	n := revisedID(c, "u1")
	if n == "" {
		t.Fatal("RevisedFrom 应记录 新→旧")
	}
	if !strings.Contains(out, "carry") || !strings.Contains(out, "后续历史已转移") {
		t.Fatalf("out = %q, want carry/后续历史已转移", out)
	}
	if got := c.ChildIDs(n); len(got) != 1 || got[0] != "a1" {
		t.Fatalf("children[new] = %v, want 边转移 a1", got)
	}
	if len(c.ChildIDs("u1")) != 0 {
		t.Fatal("旧节点应成为无孩子的旧版本叶子")
	}
	if nodeOf(t, c, "a1").Parent != n {
		t.Fatalf("a1.parent = %s, want %s（直接孩子 Parent 指针改写）", nodeOf(t, c, "a1").Parent, n)
	}
	// 被转移后代整节点字节不变（剔除 Parent 结构边；M1 验收）。
	snap := func(m conversation.Message) string {
		m.Parent = ""
		b, err := json.Marshal(m)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		return string(b)
	}
	for id, before := range map[conversation.MessageID]conversation.Message{
		"a1": a1Before, "u2": u2Before,
	} {
		if snap(before) != snap(nodeOf(t, c, id)) {
			t.Fatalf("被转移后代 %s 字节被改写:\nbefore %s\nafter  %s", id, snap(before), snap(nodeOf(t, c, id)))
		}
	}
	// 零拷贝：只 +1 个修订节点（子树各节点仍是原对象，无复制）。
	if c.NodeCount() != 6 {
		t.Fatalf("nodes = %d, want 6（5 + 1 修订节点，零拷贝）", c.NodeCount())
	}
	// 旧 Head（u2）是 u1 的严格后代 → 随子树转移，Head 保持不变（D16）。
	if c.Head != "u2" {
		t.Fatalf("head = %s, want u2（随子树保持）", c.Head)
	}
	if out2, err := handleCmd(s, "goto", "u2"); err != nil || !strings.Contains(out2, "u2") {
		t.Fatalf("转移后仍可达: %q, %v", out2, err)
	}
	if err := c.Validate(); err != nil {
		t.Fatalf("validate: %v", err)
	}
}

// TestSessionEditKeepFlagPosition --keep/--copy 只在紧跟 id 的位置识别（D97①）：
// 文本里的字面 flag 原样保留，不静默切换模式。
func TestSessionEditKeepFlagPosition(t *testing.T) {
	cases := []struct {
		name string
		args []string
		mode conversation.KeepMode
		text string
		fail string
	}{
		{"flag 紧跟 id", []string{"u1", "--keep", "新问题"}, conversation.Carry, "新问题", ""},
		{"文本含字面 keep", []string{"u1", "请加 --keep 参数"}, conversation.Fresh, "请加 --keep 参数", ""},
		{"flag 后多余的 keep 归文本", []string{"u1", "--keep", "保留 --keep 字样"}, conversation.Carry, "保留 --keep 字样", ""},
		{"flag 在 id 前拒绝", []string{"--keep", "u1", "x"}, conversation.Fresh, "", "用法"},
		{"有 flag 无文本", []string{"u1", "--keep"}, conversation.Carry, "", "用法"},
		{"copy 紧跟 id", []string{"u1", "--copy", "新问题"}, conversation.Clone, "新问题", ""},
		{"文本含字面 copy", []string{"u1", "请加 --copy 参数"}, conversation.Fresh, "请加 --copy 参数", ""},
		{"copy 在 id 前拒绝", []string{"--copy", "u1", "x"}, conversation.Fresh, "", "用法"},
		{"有 copy 无文本", []string{"u1", "--copy"}, conversation.Clone, "", "用法"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			opt, err := parseEditArgs(tc.args)
			if tc.fail != "" {
				if err == nil || !strings.Contains(err.Error(), tc.fail) {
					t.Fatalf("err = %v, want 含 %q", err, tc.fail)
				}
				return
			}
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			if opt.target != tc.args[0] || opt.mode != tc.mode || opt.part != -1 || opt.text != tc.text {
				t.Fatalf("got %q/%v/%d/%q, want %q/%v/-1/%q",
					opt.target, opt.mode, opt.part, opt.text, tc.args[0], tc.mode, tc.text)
			}
		})
	}
}

// TestSessionEditClone /edit --copy（D96/D97）：子树深拷贝到新节点下——拷贝 ID 全新、
// 内容与 CreatedAt 保真、旧分支字节级不动；Head 平移到拷贝对应节点；不触发生成（D97②）。
func TestSessionEditClone(t *testing.T) {
	store := newMemStore()
	s, _ := newConfirmedSession(t, store, nil)
	c := buildTree(t, s)
	a1Before, err := json.Marshal(nodeOf(t, c, "a1"))
	if err != nil {
		t.Fatalf("marshal a1: %v", err)
	}
	u2Before, err := json.Marshal(nodeOf(t, c, "u2"))
	if err != nil {
		t.Fatalf("marshal u2: %v", err)
	}

	out, err := handleCmd(s, "edit", "u1", "--copy", "新问题")
	if err != nil {
		t.Fatalf("edit --copy: %v", err)
	}
	n := revisedID(c, "u1")
	if n == "" {
		t.Fatal("RevisedFrom 应记录 新→旧")
	}
	if !strings.Contains(out, "clone") || !strings.Contains(out, "后续历史已复制") ||
		strings.Contains(out, "已重新生成回答") {
		t.Fatalf("out = %q, want clone/后续历史已复制 且不重发", out)
	}
	// 新节点：同父同角色新内容；旧分支原样保留。
	nm := nodeOf(t, c, n)
	if nm.Parent != "p" || nm.Role != conversation.RoleUser || nm.Content[0].Text != "新问题" {
		t.Fatalf("new node = %+v, want 同父同角色新内容", nm)
	}
	if got := c.ChildIDs("u1"); len(got) != 1 || got[0] != "a1" {
		t.Fatalf("children[u1] = %v, want 旧分支原样保留", got)
	}
	// 拷贝子树：ID 全新、内容保真、CreatedAt 保留（D96）。
	copied := c.ChildIDs(n)
	if len(copied) != 1 || copied[0] == "a1" {
		t.Fatalf("children[new] = %v, want 拷贝出的新 a1（ID 全新）", copied)
	}
	a1c := nodeOf(t, c, copied[0])
	if a1c.Parent != n || len(a1c.Content) != 2 || a1c.Content[0].Text != "回复" ||
		a1c.Content[1].Tool == nil || a1c.Content[1].Tool.CallID != "k1" {
		t.Fatalf("copied a1 = %+v, want 挂新节点下且内容保真", a1c)
	}
	u2c := c.ChildIDs(a1c.ID)
	if len(u2c) != 1 || u2c[0] == "u2" || nodeOf(t, c, u2c[0]).Content[0].Text != "追问" {
		t.Fatalf("copied u2 = %v (%+v), want 新 ID 且内容保真", u2c, nodeOf(t, c, u2c[0]))
	}
	if !a1c.CreatedAt.Equal(nodeOf(t, c, "a1").CreatedAt) {
		t.Fatalf("copied CreatedAt = %v, want 保留原值 %v", a1c.CreatedAt, nodeOf(t, c, "a1").CreatedAt)
	}
	// 旧子树字节级不动（Clone 不改写任何原有对象）。
	after, err := json.Marshal(nodeOf(t, c, "a1"))
	if err != nil {
		t.Fatalf("marshal a1 after: %v", err)
	}
	if string(after) != string(a1Before) {
		t.Fatalf("旧 a1 被改写:\nbefore %s\nafter  %s", a1Before, after)
	}
	after, err = json.Marshal(nodeOf(t, c, "u2"))
	if err != nil {
		t.Fatalf("marshal u2 after: %v", err)
	}
	if string(after) != string(u2Before) {
		t.Fatalf("旧 u2 被改写:\nbefore %s\nafter  %s", u2Before, after)
	}
	// 节点数 = 5 + 1 修订 + 2 拷贝；Head 平移到拷贝对应节点（旧 Head u2 在原子树内）。
	if c.NodeCount() != 8 {
		t.Fatalf("nodes = %d, want 8（5 + 1 修订 + 2 拷贝）", c.NodeCount())
	}
	if c.Head != u2c[0] {
		t.Fatalf("head = %s, want 拷贝末端 %s（D96 Head 平移）", c.Head, u2c[0])
	}
	if err := c.Validate(); err != nil {
		t.Fatalf("validate: %v", err)
	}
}

// TestSessionEditAssistantNoRegen 助手消息修订（Fresh）不触发生成（D97②）：Head 落在
// 修订节点、无新节点追加；整节点替换为编辑文本（D95 后果⑥，旧分支原样）。
func TestSessionEditAssistantNoRegen(t *testing.T) {
	store := newMemStore()
	s, _, _ := newTestSession(t, store, textStream("不应生成"))
	c := buildTree(t, s)

	out, err := handleCmd(s, "edit", "a1", "改写的回答")
	if err != nil {
		t.Fatalf("edit a1: %v", err)
	}
	if !strings.Contains(out, "fresh") || strings.Contains(out, "已重新生成回答") {
		t.Fatalf("out = %q, want fresh 且不重发", out)
	}
	n := revisedID(c, "a1")
	if n == "" {
		t.Fatal("RevisedFrom 应记录 新→a1")
	}
	if c.Head != n {
		t.Fatalf("head = %s, want 修订节点 %s（Fresh 恒移）", c.Head, n)
	}
	nm := nodeOf(t, c, n)
	// D107③：缺省（无 --part）= 非正文分片原位保留、正文并为新文本——a1 的工具分片
	//（buildTree 产生）随行，不再是 D95 后果⑥ 的整节点替换。
	if nm.Role != conversation.RoleAssistant || len(nm.Content) != 2 ||
		nm.Content[0].Kind != conversation.PartText || nm.Content[0].Text != "改写的回答" ||
		nm.Content[1].Kind != conversation.PartTool || nm.Content[1].Tool == nil ||
		nm.Content[1].Tool.CallID != "k1" {
		t.Fatalf("new node = %+v, want 正文替换 + 工具分片保留", nm)
	}
	if got := c.ChildIDs("a1"); len(got) != 1 || got[0] != "u2" {
		t.Fatalf("children[a1] = %v, want 旧分支原样保留", got)
	}
	if c.NodeCount() != 6 {
		t.Fatalf("nodes = %d, want 6（修订不触发生成，无追加）", c.NodeCount())
	}
	if err := c.Validate(); err != nil {
		t.Fatalf("validate: %v", err)
	}
}

// TestSessionEditUserAttachmentKept user 节点修订保留附件分片（D97⑤）：仅文本分片被
// 替换并为一（新文本落在首个文本分片位置），附件 Ref 原序原值随行。
func TestSessionEditUserAttachmentKept(t *testing.T) {
	store := newMemStore()
	s, _, _ := newTestSession(t, store, textStream("新回答"), textStream("新回答2"))
	c := buildTree(t, s)
	ref := &conversation.BlobRef{Hash: "abc123", MIME: "image/png", Name: "shot.png", Size: 7}
	m, err := c.Append(conversation.RoleUser, []conversation.Part{
		{Kind: conversation.PartText, Text: "看这张图"},
		{Kind: conversation.PartImage, Ref: ref},
	})
	if err != nil {
		t.Fatalf("append user: %v", err)
	}
	ref2 := &conversation.BlobRef{Hash: "def456", MIME: "application/pdf", Name: "doc.pdf", Size: 9}
	m2, err := c.Append(conversation.RoleUser, []conversation.Part{
		{Kind: conversation.PartDoc, Ref: ref2},
		{Kind: conversation.PartText, Text: "第一段"},
		{Kind: conversation.PartText, Text: "第二段"},
	})
	if err != nil {
		t.Fatalf("append user2: %v", err)
	}

	if _, err := handleCmd(s, "edit", string(m.ID), "改写文本"); err != nil {
		t.Fatalf("edit: %v", err)
	}
	nm := nodeOf(t, c, revisedID(c, m.ID))
	if len(nm.Content) != 2 ||
		nm.Content[0].Kind != conversation.PartText || nm.Content[0].Text != "改写文本" ||
		nm.Content[1].Kind != conversation.PartImage || nm.Content[1].Ref == nil || *nm.Content[1].Ref != *ref {
		t.Fatalf("revised content = %+v, want [新文本, 原 image Ref]", nm.Content)
	}
	if _, err := handleCmd(s, "edit", string(m2.ID), "并为一"); err != nil {
		t.Fatalf("edit2: %v", err)
	}
	nm2 := nodeOf(t, c, revisedID(c, m2.ID))
	if len(nm2.Content) != 2 ||
		nm2.Content[0].Kind != conversation.PartDoc || nm2.Content[0].Ref == nil || *nm2.Content[0].Ref != *ref2 ||
		nm2.Content[1].Kind != conversation.PartText || nm2.Content[1].Text != "并为一" {
		t.Fatalf("revised content2 = %+v, want [原 doc Ref, 新文本]（文本片并入首个文本位）", nm2.Content)
	}
	if err := c.Validate(); err != nil {
		t.Fatalf("validate: %v", err)
	}
}

// TestSessionEditPersonaKeep persona（system 首节点，D20）修订：--keep 边转移整棵对话树，
// 文档推荐的人格改写用法必须成立。
func TestSessionEditPersonaKeep(t *testing.T) {
	store := newMemStore()
	s, _ := newConfirmedSession(t, store, nil)
	c := buildTree(t, s) // Head 在 u2

	out, err := handleCmd(s, "edit", "p", "--keep", "新的人格")
	if err != nil {
		t.Fatalf("edit persona: %v", err)
	}
	if !strings.Contains(out, "carry") {
		t.Fatalf("out = %q, want carry", out)
	}
	n := revisedID(c, "p")
	if n == "" {
		t.Fatal("RevisedFrom 应记录 新→p")
	}
	nm := nodeOf(t, c, n)
	if nm.Role != conversation.RoleSystem || nm.Parent != conversation.MessageID(c.ID) || nm.Content[0].Text != "新的人格" {
		t.Fatalf("new persona = %+v, want root 下的 system 新节点", nm)
	}
	if got := c.ChildIDs(n); len(got) != 1 || got[0] != "u1" {
		t.Fatalf("children[new] = %v, want 整棵对话树转移", got)
	}
	if len(c.ChildIDs("p")) != 0 {
		t.Fatal("旧 persona 应成为无孩子的旧版本叶子")
	}
	if c.Head != "u2" {
		t.Fatalf("head = %s, want u2（随树保持）", c.Head)
	}
	if c.NodeCount() != 6 {
		t.Fatalf("nodes = %d, want 6（5 + 1 修订节点，零拷贝）", c.NodeCount())
	}
	if err := c.Validate(); err != nil {
		t.Fatalf("validate: %v", err)
	}
}

// TestSessionEditRejects 用法/空文本/未知 id/root/tool 的拒绝路径（D18/D19）。
func TestSessionEditRejects(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want string
		is   error
	}{
		{"无参数", nil, "用法", nil},
		{"缺文本", []string{"u1"}, "用法", nil},
		{"缺文本但有flag", []string{"u1", "--keep"}, "用法", nil},
		{"flag 在 id 前", []string{"--keep", "u1", "x"}, "用法", nil},
		{"空文本", []string{"u1", ""}, "不可为空", nil},
		{"未知节点", []string{"zz", "x"}, "没有节点", nil},
		{"root", []string{"convT", "x"}, "会话根节点就是会话本身", conversation.ErrInvalidNode},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := newMemStore()
			s, _ := newConfirmedSession(t, store, nil)
			buildTree(t, s)
			before := s.cur.NodeCount()
			_, err := handleCmd(s, "edit", tc.args...)
			if err == nil {
				t.Fatal("want error")
			}
			if tc.want != "" && !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want 含 %q", err, tc.want)
			}
			if tc.is != nil && !errors.Is(err, tc.is) {
				t.Fatalf("err = %v, want errors.Is(%v)", err, tc.is)
			}
			if s.cur.NodeCount() != before {
				t.Fatal("失败的修订不应改树")
			}
		})
	}
}
