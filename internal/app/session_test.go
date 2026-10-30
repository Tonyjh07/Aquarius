package app

// session_test.go 会话基础：内存会话库替身、人格首节点、文本轮次与回放、
// 会话切换与命令表、记忆/用量命令。

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/Tonyjh07/Aquarius/internal/domain/conversation"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

// memStore 内存会话库（测试替身，按 UpdatedAt 降序返回列表）。
type memStore struct {
	convs map[conversation.ID]*conversation.Conversation
	err   error // 注入的保存错误
	// saveLimit > 0 时只允许成功保存这么多次，其后返回错误（多会话导入的回滚用例）。
	saveLimit int
	saves     int
}

func newMemStore() *memStore {
	return &memStore{convs: map[conversation.ID]*conversation.Conversation{}}
}

func (m *memStore) Save(_ context.Context, c *conversation.Conversation) error {
	if m.err != nil {
		return m.err
	}
	if m.saveLimit > 0 && m.saves >= m.saveLimit {
		return errors.New("memStore: 磁盘满了")
	}
	m.saves++
	m.convs[c.ID] = cloneConv(c) // 存副本：与真实 storejson 一样切断与活树的别名，持久化断言才可信
	return nil
}

// cloneConv 经 Snapshot/Restore 做深拷贝（测试替身，重建失败即缺陷）。
//
// 这里顺带验证了一件事：任何一棵合法的树都能原样搬进搬出——不再依赖「整个结构体
// 恰好可 JSON 序列化」这种隐含假设。
func cloneConv(c *conversation.Conversation) *conversation.Conversation {
	out, err := conversation.Restore(c.Snapshot())
	if err != nil {
		panic(fmt.Sprintf("memStore: restore conversation: %v", err))
	}
	return out
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
			ID: c.ID, Title: c.Title, MessageN: c.NodeCount() - 1, UpdatedAt: c.UpdatedAt, // 不含 Root，与 storejson 对齐
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
		UI:    rec, // 会话级呈现（D40 历史回放、摄取 Notice）
	})
	if err != nil {
		t.Fatalf("new session: %v", err)
	}
	return s, llm, rec
}

// TestSessionPersonaIncludesEnv D37：环境块随 config 快照进 persona 首节点入树。
func TestSessionPersonaIncludesEnv(t *testing.T) {
	store := newMemStore()
	rec := &recorder{}
	llm := &scriptLLM{t: t}
	agent := newAgent(t, llm, rec, Deps{}, Config{})
	s, err := NewSession(context.Background(), SessionDeps{
		Store: store,
		Agent: agent,
		IDs:   &seqIDs{},
		Clock: fixedClock{testTime},
		Persona: NewPersonaComposer(PromptSpec{}, RuntimeEnv{Platform: "windows/amd64",
			Terminal: "tui", SandboxDir: `C:\data\sandbox`}, func() time.Time { return testTime }),
	})
	if err != nil {
		t.Fatalf("new session: %v", err)
	}
	persona := s.Current().Path()[1]
	text := persona.Content[0].Text
	for _, want := range []string{"Runtime environment:", "Platform: windows/amd64", "Terminal: tui"} {
		if !strings.Contains(text, want) {
			t.Fatalf("persona missing %q:\n%s", want, text)
		}
	}
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
	if len(persona.Content) != 1 || !strings.HasPrefix(persona.Content[0].Text, defaultSystem) {
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
	if cur.NodeCount() != 4 {
		t.Fatalf("nodes = %d, want 4（root+persona+user+assistant）", cur.NodeCount())
	}
	if cur.Title != "讲个笑话" {
		t.Fatalf("title = %q, want 首条消息摘要", cur.Title)
	}
	// 已落盘且可跨"进程"恢复。
	saved := store.convs[cur.ID]
	if saved == nil || saved.NodeCount() != 4 {
		t.Fatalf("saved = %+v, want 落盘 4 节点", saved)
	}

	// 重启会话：恢复同一棵树继续对话。
	s2, llm2, _ := newTestSession(t, store, textStream("继续"))
	if s2.Current().ID != cur.ID || s2.Current().NodeCount() != 4 {
		t.Fatalf("resume = %s/%d nodes, want 同一会话", s2.Current().ID, s2.Current().NodeCount())
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
		if m.Role == port.PromptUser {
			userN++
		}
	}
	if userN != 2 {
		t.Fatalf("user messages in context = %d, want 2（历史随恢复进上下文）", userN)
	}
}

// TestSessionReplayHistory D40/§7.4：启动恢复后回放水位→Head 的可见历史——
// 先 NoticeEvent 报会话身份，再逐节点 HistoryEvent；persona/Root 不回放。
func TestSessionReplayHistory(t *testing.T) {
	store := newMemStore()
	s, _, _ := newTestSession(t, store, textStream("哈"))
	if _, err := s.Handle(context.Background(), port.UserInput{Text: "讲个笑话"}); err != nil {
		t.Fatalf("handle: %v", err)
	}

	// 重启：同一 store 恢复 → 回放 user + assistant 两条。
	s2, _, rec2 := newTestSession(t, store)
	if !s2.resumed {
		t.Fatal("应处于恢复态")
	}
	n, err := s2.ReplayHistory(context.Background())
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if n != 2 {
		t.Fatalf("回放条数 = %d, want 2（user+assistant，persona 不回放）", n)
	}
	names := eventNames(rec2.events)
	if len(names) != 3 || names[0] != "notice" || names[1] != "history" || names[2] != "history" {
		t.Fatalf("events = %v, want [notice history history]", names)
	}
	notice := rec2.events[0].(port.NoticeEvent)
	for _, want := range []string{"已恢复会话", s2.Current().Title, string(s2.Current().ID), "2 条历史"} {
		if !strings.Contains(notice.Text, want) {
			t.Fatalf("notice = %q, 缺 %q", notice.Text, want)
		}
	}
	got := []conversation.Role{
		rec2.events[1].(port.HistoryEvent).Message.Role,
		rec2.events[2].(port.HistoryEvent).Message.Role,
	}
	if got[0] != conversation.RoleUser || got[1] != conversation.RoleAssistant {
		t.Fatalf("roles = %v, want [user assistant]", got)
	}
	if text := rec2.events[1].(port.HistoryEvent).Message.Content[0].Text; text != "讲个笑话" {
		t.Fatalf("user 历史文本 = %q", text)
	}

	// 同一会话内重复调用幂等可重放（回放本身不改树）。
	if n2, err := s2.ReplayHistory(context.Background()); err != nil || n2 != 2 {
		t.Fatalf("二次回放 = %d/%v, want 2/nil", n2, err)
	}
}

// TestSessionReplayHistoryWatermark D21×D40：有压缩摘要时从摘要节点起回放
// （与 assemblePath 同口径——回放内容 = 模型可见上下文）。
func TestSessionReplayHistoryWatermark(t *testing.T) {
	store := newMemStore()
	c := conversation.New(conversation.ID("Cw"), "带摘要")
	root := conversation.MessageID(c.ID)
	persona := commitNode(t, c, root, conversation.RoleSystem, "人格")
	u1 := commitNode(t, c, persona.ID, conversation.RoleUser, "旧问题")
	commitNode(t, c, u1.ID, conversation.RoleAssistant, "旧回答")
	sum := commitNode(t, c, c.Head, conversation.RoleSystem, "摘要内容")
	u2 := commitNode(t, c, c.Head, conversation.RoleUser, "新问题")
	commitNode(t, c, u2.ID, conversation.RoleAssistant, "新回答")
	if err := store.Save(context.Background(), c); err != nil {
		t.Fatalf("save: %v", err)
	}

	s, _, rec := newTestSession(t, store)
	n, err := s.ReplayHistory(context.Background())
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if n != 3 {
		t.Fatalf("回放条数 = %d, want 3（摘要+新问题+新回答，摘要之上不回放）", n)
	}
	if names := eventNames(rec.events); len(names) != 4 || names[0] != "notice" {
		t.Fatalf("events = %v", names)
	}
	var roles []conversation.Role
	var texts []string
	for _, ev := range rec.events[1:] {
		m := ev.(port.HistoryEvent).Message
		roles = append(roles, m.Role)
		texts = append(texts, m.Content[0].Text)
	}
	wantTexts := []string{"摘要内容", "新问题", "新回答"}
	for i := range wantTexts {
		if texts[i] != wantTexts[i] {
			t.Fatalf("文本[%d] = %q, want %q", i, texts[i], wantTexts[i])
		}
	}
	if roles[0] != conversation.RoleSystem || roles[1] != conversation.RoleUser || roles[2] != conversation.RoleAssistant {
		t.Fatalf("roles = %v", roles)
	}
	if sum.ID == "" || s.Current().ID != "Cw" {
		t.Fatal("回放不应改动当前会话")
	}
}

// TestSessionReplayHistoryFreshNoop 新建会话无历史：不发任何事件。
func TestSessionReplayHistoryFreshNoop(t *testing.T) {
	store := newMemStore()
	s, _, rec := newTestSession(t, store)
	n, err := s.ReplayHistory(context.Background())
	if err != nil || n != 0 || len(rec.events) != 0 {
		t.Fatalf("fresh replay = %d/%v/events=%d, want 0/nil/0", n, err, len(rec.events))
	}
}

// TestSessionReplayHistoryNoHistoryOnlyPersona 恢复但只有 persona（没聊过）：静默。
func TestSessionReplayHistoryNoHistoryOnlyPersona(t *testing.T) {
	store := newMemStore()
	c := conversation.New(conversation.ID("Cp"), "空会话")
	commitNode(t, c, conversation.MessageID(c.ID), conversation.RoleSystem, "人格")
	if err := store.Save(context.Background(), c); err != nil {
		t.Fatalf("save: %v", err)
	}
	s, _, rec := newTestSession(t, store)
	n, err := s.ReplayHistory(context.Background())
	if err != nil || n != 0 || len(rec.events) != 0 {
		t.Fatalf("replay = %d/%v/events=%d, want 0/nil/0", n, err, len(rec.events))
	}
}

// TestSessionSwitchCommand D75：/switch 切到既有会话——精确/唯一前缀解析、切前 Save 当前
// 会话（/edit 只改内存，不落盘即丢）、先 ClearEvent 再 Notice + HistoryEvent 回放目标可见历史；
// 无参/未知/歧义报错且不改动当前会话、不发任何事件。
func TestSessionSwitchCommand(t *testing.T) {
	ctx := context.Background()
	store := newMemStore()
	// 第三条流：乙的 /edit 现编辑用户消息即重新生成（D93）。
	s, _, rec := newTestSession(t, store, textStream("甲的回复"), textStream("乙的回复"), textStream("乙的改后回答"))

	if _, err := s.Handle(ctx, port.UserInput{Text: "会话甲的内容"}); err != nil {
		t.Fatalf("甲对话: %v", err)
	}
	idA, titleA := s.Current().ID, s.Current().Title
	if _, err := s.Handle(ctx, port.UserInput{Command: &port.Command{Name: "new", Args: []string{"乙"}}}); err != nil {
		t.Fatalf("/new: %v", err)
	}
	if s.Current().ID == idA {
		t.Fatal("/new 后应指向新会话")
	}
	if _, err := s.Handle(ctx, port.UserInput{Text: "会话乙的内容"}); err != nil {
		t.Fatalf("乙对话: %v", err)
	}
	idB := s.Current().ID

	// /edit 只改内存（不即时落盘）：切换必须先把当前会话 Save，否则切走即丢。
	edited := "改过的乙内容"
	pathB := s.Current().Path()
	if _, err := s.Handle(ctx, port.UserInput{Command: &port.Command{Name: "edit",
		Args: []string{string(pathB[2].ID), edited}}}); err != nil {
		t.Fatalf("/edit: %v", err)
	}

	rec.events = nil
	if _, err := s.Handle(ctx, port.UserInput{Command: &port.Command{Name: "switch", Args: []string{string(idA)}}}); err != nil {
		t.Fatalf("/switch: %v", err)
	}
	if s.Current().ID != idA {
		t.Fatalf("当前会话 = %s, want %s", s.Current().ID, idA)
	}
	// 清屏 → 切换提示 → 回放甲的可见历史（persona 不回放 = 2 条）。
	names := eventNames(rec.events)
	if len(names) != 4 || names[0] != "clear" || names[1] != "notice" || names[2] != "history" || names[3] != "history" {
		t.Fatalf("events = %v, want [clear notice history history]", names)
	}
	notice := rec.events[1].(port.NoticeEvent)
	for _, want := range []string{"已切换到会话", titleA, string(idA), "回放 2 条历史"} {
		if !strings.Contains(notice.Text, want) {
			t.Fatalf("notice = %q, want 含 %q", notice.Text, want)
		}
	}
	// 切前 Save：乙的 /edit 已进库（memStore 存深拷贝，读到即证明保存过）。
	saved, err := store.Load(ctx, idB)
	if err != nil {
		t.Fatalf("load 乙: %v", err)
	}
	found := false
	for _, n := range saved.Path() {
		for _, p := range n.Content {
			if strings.Contains(p.Text, edited) {
				found = true
			}
		}
	}
	if !found {
		t.Fatal("切换前未把当前会话的 /edit 落盘")
	}

	// 切回乙：唯一前缀解析 + 同样清屏回放。
	rec.events = nil
	if _, err := s.Handle(ctx, port.UserInput{Command: &port.Command{Name: "switch", Args: []string{string(idB)}}}); err != nil {
		t.Fatalf("/switch 乙: %v", err)
	}
	if s.Current().ID != idB {
		t.Fatalf("当前会话 = %s, want %s", s.Current().ID, idB)
	}
	if names := eventNames(rec.events); len(names) < 2 || names[0] != "clear" || names[1] != "notice" {
		t.Fatalf("events = %v, want 以 [clear notice ...] 开头", names)
	}

	// 无参/未知/歧义：报错、当前会话不动、不发事件（不得清屏）。
	for _, tc := range []struct {
		args []string
		want string
	}{
		{nil, "用法"},
		{[]string{"ZZZ"}, "没有会话"},
		{[]string{"C"}, "有歧义"}, // 会话 id 形如 C<n>，前缀 C 必命中多个
	} {
		rec.events = nil
		_, err := s.Handle(ctx, port.UserInput{Command: &port.Command{Name: "switch", Args: tc.args}})
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("/switch %v err = %v, want 含 %q", tc.args, err, tc.want)
		}
		if s.Current().ID != idB {
			t.Fatalf("/switch %v 失败后当前会话 = %s, want %s（不动）", tc.args, s.Current().ID, idB)
		}
		if len(rec.events) != 0 {
			t.Fatalf("/switch %v 失败发了事件 %v, want 无", tc.args, eventNames(rec.events))
		}
	}
}

// TestSessionNewClearsTranscript D75（修订 D72 后果④）：/new 除落盘外还发 ClearEvent
// 清屏——新会话无可回放，故只有 clear 一条，不带 HistoryEvent。
func TestSessionNewClearsTranscript(t *testing.T) {
	ctx := context.Background()
	store := newMemStore()
	s, _, rec := newTestSession(t, store, textStream("回复"))
	if _, err := s.Handle(ctx, port.UserInput{Text: "聊一句"}); err != nil {
		t.Fatalf("对话: %v", err)
	}
	rec.events = nil
	if _, err := s.Handle(ctx, port.UserInput{Command: &port.Command{Name: "new"}}); err != nil {
		t.Fatalf("/new: %v", err)
	}
	names := eventNames(rec.events)
	if len(names) != 1 || names[0] != "clear" {
		t.Fatalf("events = %v, want [clear]", names)
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

	// /help：M1 树交互与 M3 /jobs 命令在列。
	out, err := s.Handle(context.Background(), port.UserInput{Command: &port.Command{Name: "help"}})
	if err != nil {
		t.Fatalf("help: %v", err)
	}
	for _, want := range []string{"/new", "/switch", "/quit", "/goto", "/edit", "/branch", "/rm", "/jobs", "/model", "/plugin"} {
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
	// /list：两行，当前会话带 * 标记（UpdatedAt 同刻按 ID 序——全局计数器位移
	// 不影响本断言语义，按标题定位行而非位置）。
	out, err = s.Handle(context.Background(), port.UserInput{Command: &port.Command{Name: "list"}})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	lines := strings.Split(out, "\n")
	if len(lines) != 2 {
		t.Fatalf("list lines = %d (%q), want 2", len(lines), out)
	}
	curLine, otherLine := "", ""
	for _, ln := range lines {
		switch {
		case strings.Contains(ln, "项目 X"):
			curLine = ln
		case strings.Contains(ln, defaultTitle):
			otherLine = ln
		}
	}
	if !strings.HasPrefix(curLine, "*") {
		t.Fatalf("当前行 = %q, want * 标记", curLine)
	}
	if otherLine == "" || strings.HasPrefix(otherLine, "*") {
		t.Fatalf("旧会话行 = %q, want 无标记", otherLine)
	}
	// 未启用与未知命令报因（memory 已启用：未配置编辑器时报配置错）。
	_, err = s.Handle(context.Background(), port.UserInput{Command: &port.Command{Name: "memory"}})
	if err == nil || !strings.Contains(err.Error(), "未配置记忆编辑器") {
		t.Fatalf("memory err = %v", err)
	}
	// /jobs 已启用（M3）：未配置任务管理器时报配置错；/model 未接模型服务报配置错（D32）。
	_, err = s.Handle(context.Background(), port.UserInput{Command: &port.Command{Name: "jobs"}})
	if err == nil || !strings.Contains(err.Error(), "未配置任务管理器") {
		t.Fatalf("jobs err = %v", err)
	}
	_, err = s.Handle(context.Background(), port.UserInput{Command: &port.Command{Name: "model"}})
	if err == nil || !strings.Contains(err.Error(), "未配置模型服务") {
		t.Fatalf("model err = %v", err)
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

// TestSessionMemoryCommand /memory：缺省开全局、带参会话 id 前缀解析、未配置编辑器报因。
func TestSessionMemoryCommand(t *testing.T) {
	store := newMemStore()
	mem := &fakeMemories{}
	s, err := NewSession(context.Background(), SessionDeps{
		Store: store, Agent: newTestAgent(t), IDs: &seqIDs{}, Clock: fixedClock{testTime},
		Memories: mem,
	})
	if err != nil {
		t.Fatalf("new session: %v", err)
	}
	// 其他会话（前缀解析用）。
	if _, err := NewSession(context.Background(), SessionDeps{
		Store: store, Agent: newTestAgent(t), IDs: &seqIDs{}, Clock: fixedClock{testTime},
	}); err != nil {
		t.Fatalf("second session: %v", err)
	}
	cur := string(s.Current().ID)

	out, err := s.Handle(context.Background(), port.UserInput{Command: &port.Command{Name: "memory"}})
	if err != nil || len(mem.opened) != 1 || mem.opened[0] != port.GlobalMemoryDoc {
		t.Fatalf("全局: out=%q err=%v opened=%v", out, err, mem.opened)
	}
	if !strings.Contains(out, "/tmp/"+port.GlobalMemoryDoc) {
		t.Fatalf("out = %q", out)
	}

	// 精确会话 id。
	if _, err := s.Handle(context.Background(), port.UserInput{Command: &port.Command{
		Name: "memory", Args: []string{cur},
	}}); err != nil || mem.opened[1] != port.SessionMemoryDoc(s.Current().ID) {
		t.Fatalf("会话: err=%v opened=%v", err, mem.opened)
	}

	// 未知会话 / 参数过多。
	_, err = s.Handle(context.Background(), port.UserInput{Command: &port.Command{
		Name: "memory", Args: []string{"zzzz"},
	}})
	if err == nil || !strings.Contains(err.Error(), "没有会话") {
		t.Fatalf("未知会话 err = %v", err)
	}
	_, err = s.Handle(context.Background(), port.UserInput{Command: &port.Command{
		Name: "memory", Args: []string{"a", "b"},
	}})
	if err == nil || !strings.Contains(err.Error(), "用法") {
		t.Fatalf("参数过多 err = %v", err)
	}
}

// TestSessionUsageCommand /usage：估算/上轮实测/累计三段展示（脚本流带 usage）。
func TestSessionUsageCommand(t *testing.T) {
	store := newMemStore()
	agent := newTestAgent(t)
	s, err := NewSession(context.Background(), SessionDeps{
		Store: store, Agent: agent, IDs: &seqIDs{}, Clock: fixedClock{testTime},
	})
	if err != nil {
		t.Fatalf("new session: %v", err)
	}
	// 一轮对话（带服务端实测 usage）。
	agent.llm.(*scriptLLM).streams = append(agent.llm.(*scriptLLM).streams,
		withUsage(textStream("答"), conversation.Usage{InputTokens: 88, OutputTokens: 12}))
	if _, err := s.Handle(context.Background(), port.UserInput{Text: "hi"}); err != nil {
		t.Fatalf("turn: %v", err)
	}

	out, err := s.Handle(context.Background(), port.UserInput{Command: &port.Command{Name: "usage"}})
	if err != nil {
		t.Fatalf("usage: %v", err)
	}
	for _, want := range []string{
		"当前上下文:≈", "tokens", "自动压缩阈值", "44800",
		"计数方式: 估算", "校准",
		"上轮实测: in=88 out=12",
		"本会话累计(Path): in=88 out=12 tokens（1 次生成）",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("/usage 缺 %q:\n%s", want, out)
		}
	}
}
