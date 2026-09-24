package app

import (
	"context"
	"errors"
	"sort"
	"strings"
	"testing"

	"github.com/Tonyjh07/Aquarius/internal/domain/conversation"
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
	s, err := NewSession(context.Background(), SessionDeps{Store: store, Agent: agent, IDs: &seqIDs{}})
	if err != nil {
		t.Fatalf("new session: %v", err)
	}
	return s, llm, rec
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
	if len(cur.Nodes) != 2 {
		t.Fatalf("nodes = %d, want 2（user+assistant）", len(cur.Nodes))
	}
	if cur.Title != "讲个笑话" {
		t.Fatalf("title = %q, want 首条消息摘要", cur.Title)
	}
	// 已落盘且可跨"进程"恢复。
	saved := store.convs[cur.ID]
	if saved == nil || len(saved.Nodes) != 2 {
		t.Fatalf("saved = %+v, want 落盘 2 节点", saved)
	}

	// 重启会话：恢复同一棵树继续对话。
	s2, llm2, _ := newTestSession(t, store, textStream("继续"))
	if s2.Current().ID != cur.ID || len(s2.Current().Nodes) != 2 {
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
