package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"testing"

	"github.com/Tonyjh07/Aquarius/internal/domain/conversation"
	"github.com/Tonyjh07/Aquarius/internal/domain/perm"
	"github.com/Tonyjh07/Aquarius/internal/domain/tool"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

// memStore 内存会话库（测试替身，按 UpdatedAt 降序返回列表）。
type memStore struct {
	convs map[conversation.ID]*conversation.Conversation
	err   error // 注入的保存错误
}

func newMemStore() *memStore {
	return &memStore{convs: map[conversation.ID]*conversation.Conversation{}}
}

func (m *memStore) Save(_ context.Context, c *conversation.Conversation) error {
	if m.err != nil {
		return m.err
	}
	m.convs[c.ID] = cloneConv(c) // 存副本：与真实 storejson 一样切断与活树的别名，持久化断言才可信
	return nil
}

// cloneConv 经 JSON 往返做深拷贝（会话树全字段可序列化；测试替身，编码失败即缺陷）。
func cloneConv(c *conversation.Conversation) *conversation.Conversation {
	data, err := json.Marshal(c)
	if err != nil {
		panic(fmt.Sprintf("memStore: marshal conversation: %v", err))
	}
	var out conversation.Conversation
	if err := json.Unmarshal(data, &out); err != nil {
		panic(fmt.Sprintf("memStore: unmarshal conversation: %v", err))
	}
	return &out
}

func (m *memStore) Load(_ context.Context, id conversation.ID) (*conversation.Conversation, error) {
	c, ok := m.convs[id]
	if !ok {
		return nil, errors.New("memStore: not found")
	}
	return c, nil
}

func (m *memStore) List(context.Context) ([]port.ConversationSummary, error) {
	var out []port.ConversationSummary
	for _, c := range m.convs {
		out = append(out, port.ConversationSummary{
			ID: c.ID, Title: c.Title, MessageN: len(c.Nodes) - 1, UpdatedAt: c.UpdatedAt, // 不含 Root，与 storejson 对齐
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].UpdatedAt.Equal(out[j].UpdatedAt) {
			return out[i].UpdatedAt.After(out[j].UpdatedAt)
		}
		return out[i].ID > out[j].ID
	})
	return out, nil
}

func (m *memStore) Remove(_ context.Context, id conversation.ID) error {
	if _, ok := m.convs[id]; !ok {
		return errors.New("memStore: not found")
	}
	delete(m.convs, id)
	return nil
}

// newTestSession 组装会话（脚本 LLM 无流：textTurn 提供单轮文本流）。
func newTestSession(t *testing.T, store port.ConversationStore, streams ...*scriptStream) (*Session, *scriptLLM, *recorder) {
	t.Helper()
	rec := &recorder{}
	llm := &scriptLLM{t: t, streams: streams}
	agent := newAgent(t, llm, rec, Deps{}, Config{})
	s, err := NewSession(context.Background(), SessionDeps{
		Store: store,
		Agent: agent,
		IDs:   &seqIDs{},
		Clock: fixedClock{testTime},
	})
	if err != nil {
		t.Fatalf("new session: %v", err)
	}
	return s, llm, rec
}

// TestNewSessionWritesPersonaFirstNode D20：新建会话 = Root + persona 首节点，Head 在 persona。
func TestNewSessionWritesPersonaFirstNode(t *testing.T) {
	store := newMemStore()
	s, _, _ := newTestSession(t, store)

	path := s.Current().Path()
	if len(path) != 2 {
		t.Fatalf("path len = %d, want 2 (root,persona)", len(path))
	}
	persona := path[1]
	if persona.Role != conversation.RoleSystem || persona.Parent != path[0].ID {
		t.Fatalf("persona = %+v, want system under root", persona)
	}
	if len(persona.Content) != 1 || persona.Content[0].Text != defaultSystem {
		t.Fatalf("persona content = %+v, want 内置默认人格", persona.Content)
	}
	if s.Current().Head != persona.ID {
		t.Fatalf("head = %s, want persona", s.Current().Head)
	}
	if persona.CreatedAt.IsZero() {
		t.Fatal("persona CreatedAt 应由 Clock 注入")
	}
}

func TestSessionTextRoundPersists(t *testing.T) {
	store := newMemStore()
	s, llm, _ := newTestSession(t, store, textStream("哈"))

	out, err := s.Handle(context.Background(), port.UserInput{Text: "讲个笑话"})
	if err != nil {
		t.Fatalf("handle: %v", err)
	}
	if out != "" {
		t.Fatalf("文本输入不应有命令输出: %q", out)
	}
	if len(llm.requests) != 1 {
		t.Fatalf("llm requests = %d, want 1", len(llm.requests))
	}
	cur := s.Current()
	if len(cur.Nodes) != 4 {
		t.Fatalf("nodes = %d, want 4（root+persona+user+assistant）", len(cur.Nodes))
	}
	if cur.Title != "讲个笑话" {
		t.Fatalf("title = %q, want 首条消息摘要", cur.Title)
	}
	// 已落盘且可跨"进程"恢复。
	saved := store.convs[cur.ID]
	if saved == nil || len(saved.Nodes) != 4 {
		t.Fatalf("saved = %+v, want 落盘 4 节点", saved)
	}

	// 重启会话：恢复同一棵树继续对话。
	s2, llm2, _ := newTestSession(t, store, textStream("继续"))
	if s2.Current().ID != cur.ID || len(s2.Current().Nodes) != 4 {
		t.Fatalf("resume = %s/%d nodes, want 同一会话", s2.Current().ID, len(s2.Current().Nodes))
	}
	if _, err := s2.Handle(context.Background(), port.UserInput{Text: "再来一个"}); err != nil {
		t.Fatalf("handle after resume: %v", err)
	}
	if len(llm2.requests) != 1 {
		t.Fatalf("llm2 requests = %d", len(llm2.requests))
	}
	// 恢复后的上下文携带历史两条用户消息。
	var userN int
	for _, m := range llm2.requests[0].Messages {
		if m.Role == "user" {
			userN++
		}
	}
	if userN != 2 {
		t.Fatalf("user messages in context = %d, want 2（历史随恢复进上下文）", userN)
	}
}

func TestSessionEmptyInputNoop(t *testing.T) {
	store := newMemStore()
	s, llm, rec := newTestSession(t, store)
	out, err := s.Handle(context.Background(), port.UserInput{Text: "   "})
	if err != nil || out != "" {
		t.Fatalf("out/err = %q/%v", out, err)
	}
	if len(llm.requests) != 0 || len(rec.events) != 0 {
		t.Fatal("空输入不应触发生成")
	}
}

// TestSessionCommands /help 命令总览；/new /list 元数据命令；未知与未启用命令报因。
func TestSessionCommands(t *testing.T) {
	store := newMemStore()
	s, _, _ := newTestSession(t, store)

	// /help：M1 树交互命令在列。
	out, err := s.Handle(context.Background(), port.UserInput{Command: &port.Command{Name: "help"}})
	if err != nil {
		t.Fatalf("help: %v", err)
	}
	for _, want := range []string{"/new", "/quit", "/goto", "/edit", "/branch", "/rm"} {
		if !strings.Contains(out, want) {
			t.Fatalf("help 缺 %q:\n%s", want, out)
		}
	}
	// /new
	out, err = s.Handle(context.Background(), port.UserInput{Command: &port.Command{Name: "new", Args: []string{"项目", "X"}}})
	if err != nil || !strings.Contains(out, "已新建") {
		t.Fatalf("new = %q, %v", out, err)
	}
	if s.Current().Title != "项目 X" {
		t.Fatalf("title = %q", s.Current().Title)
	}
	if store.convs[s.Current().ID] == nil {
		t.Fatal("/new 应立即落盘")
	}
	// /list：两行，当前会话带 * 标记，最新在前。
	out, err = s.Handle(context.Background(), port.UserInput{Command: &port.Command{Name: "list"}})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	lines := strings.Split(out, "\n")
	if len(lines) != 2 {
		t.Fatalf("list lines = %d (%q), want 2", len(lines), out)
	}
	if !strings.HasPrefix(lines[0], "*") || !strings.Contains(lines[0], "项目 X") {
		t.Fatalf("list[0] = %q, want 当前行", lines[0])
	}
	if !strings.Contains(lines[1], defaultTitle) {
		t.Fatalf("list[1] = %q, want 旧会话", lines[1])
	}
	// 未启用与未知命令报因。
	_, err = s.Handle(context.Background(), port.UserInput{Command: &port.Command{Name: "memory"}})
	if err == nil || !strings.Contains(err.Error(), "尚未启用") {
		t.Fatalf("memory err = %v", err)
	}
	_, err = s.Handle(context.Background(), port.UserInput{Command: &port.Command{Name: "wat"}})
	if err == nil || !strings.Contains(err.Error(), "未知命令") {
		t.Fatalf("unknown err = %v", err)
	}
	// /quit
	_, err = s.Handle(context.Background(), port.UserInput{Command: &port.Command{Name: "quit"}})
	if !errors.Is(err, ErrQuit) {
		t.Fatalf("quit err = %v, want ErrQuit", err)
	}
}

// TestSessionTitleAndExitCommands /title 无参显示、有参改写并落盘；/exit = /quit 别名。
func TestSessionTitleAndExitCommands(t *testing.T) {
	store := newMemStore()
	s, _, _ := newTestSession(t, store)

	out, err := s.Handle(context.Background(), port.UserInput{Command: &port.Command{Name: "title"}})
	if err != nil || !strings.Contains(out, defaultTitle) {
		t.Fatalf("title 无参 = %q, %v", out, err)
	}
	out, err = s.Handle(context.Background(), port.UserInput{Command: &port.Command{Name: "title", Args: []string{"改个", "名字"}}})
	if err != nil || !strings.Contains(out, "改个 名字") {
		t.Fatalf("title 有参 = %q, %v", out, err)
	}
	if store.convs[s.Current().ID].Title != "改个 名字" {
		t.Fatalf("标题未落盘: %q", store.convs[s.Current().ID].Title)
	}
	if _, err := s.Handle(context.Background(), port.UserInput{Command: &port.Command{Name: "exit"}}); !errors.Is(err, ErrQuit) {
		t.Fatalf("exit = %v, want ErrQuit", err)
	}
}

// TestSessionPermissionCommand /permission 展示矩阵、切换等级并写回 config（D22）。
func TestSessionPermissionCommand(t *testing.T) {
	store := newMemStore()
	rec := &recorder{}
	llm := &scriptLLM{t: t}
	agent := newAgent(t, llm, rec, Deps{}, Config{})
	var persisted []perm.Level
	s, err := NewSession(context.Background(), SessionDeps{
		Store:        store,
		Agent:        agent,
		IDs:          &seqIDs{},
		Clock:        fixedClock{testTime},
		SandboxPath:  "/data/sandbox",
		PersistLevel: func(l perm.Level) error { persisted = append(persisted, l); return nil },
	})
	if err != nil {
		t.Fatalf("new session: %v", err)
	}

	// 无参：默认 strict + 矩阵 + 特权目录。
	out, err := s.Handle(context.Background(), port.UserInput{Command: &port.Command{Name: "permission"}})
	if err != nil {
		t.Fatalf("permission: %v", err)
	}
	for _, want := range []string{"strict", "/data/sandbox", "read-only", "permissive", "full-access", "特权 rw"} {
		if !strings.Contains(out, want) {
			t.Fatalf("report 缺 %q:\n%s", want, out)
		}
	}
	if !strings.Contains(out, "* strict") {
		t.Fatalf("当前等级应带 * 标记:\n%s", out)
	}

	// 非法等级报因。
	if _, err := s.Handle(context.Background(), port.UserInput{Command: &port.Command{Name: "permission", Args: []string{"root"}}}); err == nil {
		t.Fatal("非法等级应报错")
	}
	// 切换并写回。
	out, err = s.Handle(context.Background(), port.UserInput{Command: &port.Command{Name: "permission", Args: []string{"PERMISSIVE"}}})
	if err != nil || !strings.Contains(out, "permissive") {
		t.Fatalf("switch = %q, %v", out, err)
	}
	if len(persisted) != 1 || persisted[0] != perm.Permissive {
		t.Fatalf("persisted = %v, want [permissive]", persisted)
	}
	out, _ = s.Handle(context.Background(), port.UserInput{Command: &port.Command{Name: "permission"}})
	if !strings.Contains(out, "* permissive") {
		t.Fatalf("切换后报告:\n%s", out)
	}
	// 相同等级短路：不再写回。
	if _, err := s.Handle(context.Background(), port.UserInput{Command: &port.Command{Name: "permission", Args: []string{"permissive"}}}); err != nil {
		t.Fatalf("same level: %v", err)
	}
	if len(persisted) != 1 {
		t.Fatalf("persisted = %v, want 不重复写回", persisted)
	}

	// 无持久化回调时拒绝切换。
	s2, err := NewSession(context.Background(), SessionDeps{
		Store: newMemStore(), Agent: agent, IDs: &seqIDs{}, Clock: fixedClock{testTime},
	})
	if err != nil {
		t.Fatalf("new session: %v", err)
	}
	if _, err := s2.Handle(context.Background(), port.UserInput{Command: &port.Command{Name: "permission", Args: []string{"full-access"}}}); err == nil {
		t.Fatal("无持久化回调应报错")
	}
}

// TestSessionCompactCommand D21 手动轨：/compact 生成摘要节点、落盘、报记账；重复压缩短路。
func TestSessionCompactCommand(t *testing.T) {
	store := newMemStore()
	s, _, _ := newTestSession(t, store,
		textStream("模型回复"),
		withUsage(textStream("压缩后的摘要"), conversation.Usage{InputTokens: 40, OutputTokens: 15}),
	)
	if _, err := s.Handle(context.Background(), port.UserInput{Text: "hi"}); err != nil {
		t.Fatalf("handle: %v", err)
	}

	out, err := s.Handle(context.Background(), port.UserInput{Command: &port.Command{Name: "compact"}})
	if err != nil {
		t.Fatalf("compact: %v", err)
	}
	// 会话 = [root,persona,user,assistant]：absorbed 应为 2（persona 恒回传不计）。
	if !strings.Contains(out, "已压缩 2 条历史") || !strings.Contains(out, "in=40 out=15") {
		t.Fatalf("out = %q, want 精确计数与记账", out)
	}
	path := s.Current().Path()
	last := path[len(path)-1]
	if last.Role != conversation.RoleSystem || last.Content[0].Text != "压缩后的摘要" {
		t.Fatalf("last = %+v, want 摘要节点", last)
	}
	if _, ok := store.convs[s.Current().ID].Nodes[last.ID]; !ok {
		t.Fatal("摘要节点应已落盘")
	}

	// 重复压缩：其后无新内容 → 短路提示，不发起生成。
	out, err = s.Handle(context.Background(), port.UserInput{Command: &port.Command{Name: "compact"}})
	if err != nil || !strings.Contains(out, "无需压缩") {
		t.Fatalf("second compact = %q, %v", out, err)
	}
}

// TestSessionCompactCancelFriendly 压缩被取消：友好文案、会话树不动。
func TestSessionCompactCancelFriendly(t *testing.T) {
	store := newMemStore()
	s, _, _ := newTestSession(t, store,
		textStream("回复"),
		&scriptStream{steps: []scriptStep{{err: context.Canceled}}},
	)
	if _, err := s.Handle(context.Background(), port.UserInput{Text: "hi"}); err != nil {
		t.Fatalf("handle: %v", err)
	}
	before := len(s.Current().Nodes)

	out, err := s.Handle(context.Background(), port.UserInput{Command: &port.Command{Name: "compact"}})
	if err != nil || !strings.Contains(out, "已取消压缩") {
		t.Fatalf("compact cancel = %q, %v", out, err)
	}
	if len(s.Current().Nodes) != before {
		t.Fatalf("nodes = %d, want 会话未改动 %d", len(s.Current().Nodes), before)
	}
}

func TestSessionSaveFailureSurfaces(t *testing.T) {
	store := newMemStore()
	s, _, _ := newTestSession(t, store, textStream("x"))
	store.err = errors.New("disk full")

	_, err := s.Handle(context.Background(), port.UserInput{Text: "hello"})
	if err == nil || !strings.Contains(err.Error(), "disk full") {
		t.Fatalf("err = %v, want 保存失败上抛", err)
	}

	// Turn 错误与保存失败同时发生：两个错误都要保留。
	store2 := newMemStore()
	s2, llm2, _ := newTestSession(t, store2, textStream("x"))
	llm2.genErr = errors.New("boom")
	store2.err = errors.New("disk full")
	_, err = s2.Handle(context.Background(), port.UserInput{Text: "hello"})
	if err == nil || !strings.Contains(err.Error(), "boom") || !strings.Contains(err.Error(), "disk full") {
		t.Fatalf("err = %v, want 同时保留 boom 与 disk full", err)
	}
}

func TestNewSessionDependenciesValidated(t *testing.T) {
	if _, err := NewSession(context.Background(), SessionDeps{}); err == nil {
		t.Fatal("缺少依赖应报错")
	}
}

// ---------------------------------------------------------------------------
// M1 树交互命令：/goto /edit /branch /rm
// ---------------------------------------------------------------------------

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
// convT(root) → p(persona) → u1(user) → a1(assistant，带调用 k1) → t1(tool 结果) → u2(user)；
// Head 在 u2。显式 ID 便于断言前缀解析与结构变化。
func buildTree(t *testing.T, s *Session) *conversation.Conversation {
	t.Helper()
	c := conversation.New(conversation.ID("convT"), "树交互")
	mk := func(m conversation.Message) conversation.Message {
		t.Helper()
		if err := c.AppendCommitted(m); err != nil {
			t.Fatalf("commit %s: %v", m.ID, err)
		}
		return c.Nodes[m.ID]
	}
	txt := func(s string) []conversation.Part {
		return []conversation.Part{{Kind: conversation.PartText, Text: s}}
	}
	root := conversation.MessageID(c.ID)
	p := mk(conversation.Message{ID: "p", Parent: root, Role: conversation.RoleSystem, Content: txt("人格")})
	u1 := mk(conversation.Message{ID: "u1", Parent: p.ID, Role: conversation.RoleUser, Content: txt("问题")})
	a1 := mk(conversation.Message{
		ID: "a1", Parent: u1.ID, Role: conversation.RoleAssistant, Content: txt("回复"),
		ToolCalls: []tool.Call{{ID: "k1", Name: "think", Args: json.RawMessage(`{}`)}},
	})
	mk(conversation.Message{
		ID: "t1", Parent: a1.ID, Role: conversation.RoleTool,
		ToolResult: &tool.Result{CallID: "k1", OK: true, Output: "思考完毕"},
	})
	mk(conversation.Message{ID: "u2", Parent: "t1", Role: conversation.RoleUser, Content: txt("追问")})
	if err := c.Validate(); err != nil {
		t.Fatalf("build tree: %v", err)
	}
	s.cur = c
	return c
}

// handleCmd 直接走命令入口。
func handleCmd(s *Session, name string, args ...string) (string, error) {
	return s.Handle(context.Background(), port.UserInput{Command: &port.Command{Name: name, Args: args}})
}

// revisedID 找出以 old 为旧版本的新节点 ID。
func revisedID(c *conversation.Conversation, old conversation.MessageID) conversation.MessageID {
	for n, o := range c.RevisedFrom {
		if o == old {
			return n
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

// TestSessionEditFresh /edit 缺省 Fresh：同级新节点、旧分支原样保留、Head 移到新节点、落盘。
func TestSessionEditFresh(t *testing.T) {
	store := newMemStore()
	s, _ := newConfirmedSession(t, store, nil)
	c := buildTree(t, s)

	out, err := handleCmd(s, "edit", "u1", "新问题")
	if err != nil {
		t.Fatalf("edit: %v", err)
	}
	n := revisedID(c, "u1")
	if n == "" {
		t.Fatal("RevisedFrom 应记录 新→旧")
	}
	if !strings.Contains(out, "fresh") || !strings.Contains(out, "旧分支保留") || !strings.Contains(out, string(n)) {
		t.Fatalf("out = %q, want fresh/旧分支保留/新节点 id", out)
	}
	nm := c.Nodes[n]
	if nm.Parent != "p" || nm.Role != conversation.RoleUser || nm.Content[0].Text != "新问题" {
		t.Fatalf("new node = %+v, want 同父同角色新内容", nm)
	}
	if got := c.Children["u1"]; len(got) != 1 || got[0] != "a1" {
		t.Fatalf("children[u1] = %v, want 旧分支原样保留", got)
	}
	if c.Head != n {
		t.Fatalf("head = %s, want %s（Fresh 移到新节点）", c.Head, n)
	}
	if saved := store.convs[c.ID]; saved == nil || saved.Head != n {
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
	a1Before := c.Nodes["a1"].Clone()
	t1Before := c.Nodes["t1"].Clone()
	u2Before := c.Nodes["u2"].Clone()

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
	if got := c.Children[n]; len(got) != 1 || got[0] != "a1" {
		t.Fatalf("children[new] = %v, want 边转移 a1", got)
	}
	if _, ok := c.Children["u1"]; ok {
		t.Fatal("旧节点应成为无孩子的旧版本叶子")
	}
	if c.Nodes["a1"].Parent != n {
		t.Fatalf("a1.parent = %s, want %s（直接孩子 Parent 指针改写）", c.Nodes["a1"].Parent, n)
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
		"a1": a1Before, "t1": t1Before, "u2": u2Before,
	} {
		if snap(before) != snap(c.Nodes[id]) {
			t.Fatalf("被转移后代 %s 字节被改写:\nbefore %s\nafter  %s", id, snap(before), snap(c.Nodes[id]))
		}
	}
	// 零拷贝：只 +1 个修订节点（子树各节点仍是原对象，无复制）。
	if len(c.Nodes) != 7 {
		t.Fatalf("nodes = %d, want 7（6 + 1 修订节点，零拷贝）", len(c.Nodes))
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

// TestSessionEditKeepFlagPosition --keep 只在紧跟 id 的位置识别：
// 文本里的字面 --keep 原样保留，不静默切换模式。
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
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			id, mode, text, err := parseEditArgs(tc.args)
			if tc.fail != "" {
				if err == nil || !strings.Contains(err.Error(), tc.fail) {
					t.Fatalf("err = %v, want 含 %q", err, tc.fail)
				}
				return
			}
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			if id != tc.args[0] || mode != tc.mode || text != tc.text {
				t.Fatalf("got %q/%v/%q, want %q/%v/%q", id, mode, text, tc.args[0], tc.mode, tc.text)
			}
		})
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
	nm := c.Nodes[n]
	if nm.Role != conversation.RoleSystem || nm.Parent != conversation.MessageID(c.ID) || nm.Content[0].Text != "新的人格" {
		t.Fatalf("new persona = %+v, want root 下的 system 新节点", nm)
	}
	if got := c.Children[n]; len(got) != 1 || got[0] != "u1" {
		t.Fatalf("children[new] = %v, want 整棵对话树转移", got)
	}
	if _, ok := c.Children["p"]; ok {
		t.Fatal("旧 persona 应成为无孩子的旧版本叶子")
	}
	if c.Head != "u2" {
		t.Fatalf("head = %s, want u2（随树保持）", c.Head)
	}
	if len(c.Nodes) != 7 {
		t.Fatalf("nodes = %d, want 7（零拷贝）", len(c.Nodes))
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
		{"root", []string{"convT", "x"}, "root 即会话", conversation.ErrInvalidNode},
		{"tool", []string{"t1", "x"}, "", conversation.ErrReviseTool},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := newMemStore()
			s, _ := newConfirmedSession(t, store, nil)
			buildTree(t, s)
			before := len(s.cur.Nodes)
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
			if len(s.cur.Nodes) != before {
				t.Fatal("失败的修订不应改树")
			}
		})
	}
}

// TestSessionBranchCommand /branch：缺省取 Head、显式 id、Head/修订标记、无分叉与错误路径。
func TestSessionBranchCommand(t *testing.T) {
	store := newMemStore()
	s, _ := newConfirmedSession(t, store, nil)
	c := buildTree(t, s)

	if _, err := handleCmd(s, "edit", "u1", "改"); err != nil {
		t.Fatalf("edit: %v", err)
	}
	n := revisedID(c, "u1") // Head 在新节点

	// 显式 id：自身 + 同级（新旧版本对比）+ 下级，带 Head 与修订标记。
	out, err := handleCmd(s, "branch", "u1")
	if err != nil {
		t.Fatalf("branch u1: %v", err)
	}
	for _, want := range []string{
		"当前: u1 [user] 问题",
		"同级分叉（1 条，不含自身）:",
		string(n) + " [user] 改",
		"← Head",
		"（修订自 u1）",
		"下级（1 条）:",
		"a1 [assistant] 回复",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("branch 缺 %q:\n%s", want, out)
		}
	}

	// 缺省 = Head。
	out, err = handleCmd(s, "branch")
	if err != nil || !strings.Contains(out, "当前: "+string(n)) || !strings.Contains(out, "u1 [user] 问题") {
		t.Fatalf("branch 缺省 = %q, %v", out, err)
	}

	// 无同级分叉 / 错误路径。
	out, err = handleCmd(s, "branch", "a1")
	if err != nil || !strings.Contains(out, "同级分叉: 无") || !strings.Contains(out, "下级（1 条）:\n  t1 [tool]") {
		t.Fatalf("无分叉 = %q, %v", out, err)
	}

	// Root：孩子即"顶层消息"，不重复列下级。
	out, err = handleCmd(s, "branch", "convT")
	if err != nil || !strings.Contains(out, "顶层消息（1 条") || strings.Contains(out, "下级") {
		t.Fatalf("root branch = %q, %v", out, err)
	}

	if _, err := handleCmd(s, "branch", "zz"); err == nil || !strings.Contains(err.Error(), "没有节点") {
		t.Fatalf("未知 id err = %v", err)
	}
	if _, err := handleCmd(s, "branch", "a", "b"); err == nil || !strings.Contains(err.Error(), "用法") {
		t.Fatalf("多参数 err = %v", err)
	}
}

// TestSessionRmCommand /rm：二次确认三态（未配置/拒绝/同意）、root 先于确认拒绝、子树计数与 Head 回退。
func TestSessionRmCommand(t *testing.T) {
	t.Run("未配置确认器", func(t *testing.T) {
		store := newMemStore()
		agent := newAgent(t, &scriptLLM{t: t}, &recorder{}, Deps{}, Config{})
		s, err := NewSession(context.Background(), SessionDeps{
			Store: store, Agent: agent, IDs: &seqIDs{}, Clock: fixedClock{testTime},
		})
		if err != nil {
			t.Fatalf("new session: %v", err)
		}
		buildTree(t, s)
		if _, err := handleCmd(s, "rm", "u1"); err == nil || !strings.Contains(err.Error(), "未配置确认器") {
			t.Fatalf("err = %v, want 未配置确认器", err)
		}
	})

	t.Run("root 先于确认拒绝", func(t *testing.T) {
		store := newMemStore()
		s, conf := newConfirmedSession(t, store, nil) // 确认脚本为空：被调用即 Fatalf
		buildTree(t, s)
		if _, err := handleCmd(s, "rm", "convT"); err == nil || !strings.Contains(err.Error(), "root 即会话") {
			t.Fatalf("err = %v, want root 即会话不可删除", err)
		}
		if len(conf.asked) != 0 {
			t.Fatalf("不应消费确认: %+v", conf.asked)
		}
	})

	t.Run("拒绝", func(t *testing.T) {
		store := newMemStore()
		s, _ := newConfirmedSession(t, store, []bool{false})
		c := buildTree(t, s)
		out, err := handleCmd(s, "rm", "u1")
		if err != nil || !strings.Contains(out, "已取消删除 u1") {
			t.Fatalf("deny = %q, %v", out, err)
		}
		if len(c.Nodes) != 6 {
			t.Fatalf("nodes = %d, want 树未改动 6", len(c.Nodes))
		}
	})

	t.Run("同意", func(t *testing.T) {
		store := newMemStore()
		s, _ := newConfirmedSession(t, store, []bool{true})
		c := buildTree(t, s)
		out, err := handleCmd(s, "rm", "u1")
		if err != nil {
			t.Fatalf("rm: %v", err)
		}
		// 子树 u1 = {u1,a1,t1,u2} 共 4 条；Head(u2) 回退到最近存活祖先 p。
		if !strings.Contains(out, "共 4 条节点") && !strings.Contains(out, "4 条节点") {
			t.Fatalf("out = %q, want 子树计数 4", out)
		}
		if !strings.Contains(out, "Head → p") {
			t.Fatalf("out = %q, want Head 回退到 p", out)
		}
		for _, id := range []string{"u1", "a1", "t1", "u2"} {
			if _, ok := c.Nodes[conversation.MessageID(id)]; ok {
				t.Fatalf("节点 %s 应已删除", id)
			}
		}
		if len(c.Nodes) != 2 || c.Head != "p" {
			t.Fatalf("nodes/head = %d/%s, want 2/p", len(c.Nodes), c.Head)
		}
		saved := store.convs[c.ID]
		if saved == nil || saved.Head != "p" || len(saved.Nodes) != 2 {
			t.Fatalf("saved = %+v, want 删除结果已落盘", saved)
		}
		if err := c.Validate(); err != nil {
			t.Fatalf("validate: %v", err)
		}
	})

	t.Run("落盘失败回滚", func(t *testing.T) {
		store := newMemStore()
		s, _ := newConfirmedSession(t, store, []bool{true})
		c := buildTree(t, s)
		if err := store.Save(context.Background(), c); err != nil {
			t.Fatalf("预存: %v", err)
		}
		store.err = errors.New("disk full")
		_, err := handleCmd(s, "rm", "u1")
		if err == nil || !strings.Contains(err.Error(), "回滚") || !strings.Contains(err.Error(), "disk full") {
			t.Fatalf("err = %v, want 回滚且保留 disk full", err)
		}
		// 内存与已存副本都回到删除前状态：删除绝不"半生效"。
		if len(s.cur.Nodes) != 6 {
			t.Fatalf("nodes = %d, want 回滚到 6", len(s.cur.Nodes))
		}
		if saved := store.convs[c.ID]; saved == nil || len(saved.Nodes) != 6 {
			t.Fatalf("saved = %v, want 6 节点副本未被改写", saved)
		}
	})

	t.Run("错误路径", func(t *testing.T) {
		store := newMemStore()
		s, _ := newConfirmedSession(t, store, nil)
		buildTree(t, s)
		if _, err := handleCmd(s, "rm"); err == nil || !strings.Contains(err.Error(), "用法") {
			t.Fatalf("用法 err = %v", err)
		}
		if _, err := handleCmd(s, "rm", "zz"); err == nil || !strings.Contains(err.Error(), "没有节点") {
			t.Fatalf("未知 id err = %v", err)
		}
	})
}
