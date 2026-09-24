package app

import (
	"context"
	"errors"
	"sort"
	"strings"
	"testing"

	"github.com/Tonyjh07/Aquarius/internal/domain/conversation"
	"github.com/Tonyjh07/Aquarius/internal/domain/perm"
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
	m.convs[c.ID] = c
	return nil
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
			ID: c.ID, Title: c.Title, MessageN: len(c.Nodes), UpdatedAt: c.UpdatedAt,
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

func TestSessionCommands(t *testing.T) {
	store := newMemStore()
	s, _, _ := newTestSession(t, store)

	// /help
	out, err := s.Handle(context.Background(), port.UserInput{Command: &port.Command{Name: "help"}})
	if err != nil || !strings.Contains(out, "/new") || !strings.Contains(out, "/quit") {
		t.Fatalf("help = %q, %v", out, err)
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
	_, err = s.Handle(context.Background(), port.UserInput{Command: &port.Command{Name: "goto"}})
	if err == nil || !strings.Contains(err.Error(), "尚未启用") {
		t.Fatalf("goto err = %v", err)
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
	if !strings.Contains(out, "已压缩") || !strings.Contains(out, "in=40 out=15") {
		t.Fatalf("out = %q, want 记账信息", out)
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

func TestSessionSaveFailureSurfaces(t *testing.T) {
	store := newMemStore()
	s, _, _ := newTestSession(t, store, textStream("x"))
	store.err = errors.New("disk full")

	_, err := s.Handle(context.Background(), port.UserInput{Text: "hello"})
	if err == nil || !strings.Contains(err.Error(), "disk full") {
		t.Fatalf("err = %v, want 保存失败上抛", err)
	}
}

func TestNewSessionDependenciesValidated(t *testing.T) {
	if _, err := NewSession(context.Background(), SessionDeps{}); err == nil {
		t.Fatal("缺少依赖应报错")
	}
}
