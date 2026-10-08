package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
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
		Env:   RuntimeEnv{Platform: "windows/amd64", Terminal: "tui", SandboxDir: `C:\data\sandbox`},
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
	// /jobs 已启用（M3）：未配置任务管理器时报配置错；/model 无 ListModels 报配置错（D32）。
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
	var opened []string
	store := newMemStore()
	s, err := NewSession(context.Background(), SessionDeps{
		Store: store, Agent: newTestAgent(t), IDs: &seqIDs{}, Clock: fixedClock{testTime},
		OpenMemory: func(name string) (string, error) {
			opened = append(opened, name)
			return "/data/" + name, nil
		},
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
	if err != nil || len(opened) != 1 || opened[0] != port.GlobalMemoryDoc {
		t.Fatalf("全局: out=%q err=%v opened=%v", out, err, opened)
	}
	if !strings.Contains(out, "/data/"+port.GlobalMemoryDoc) {
		t.Fatalf("out = %q", out)
	}

	// 精确会话 id。
	if _, err := s.Handle(context.Background(), port.UserInput{Command: &port.Command{
		Name: "memory", Args: []string{cur},
	}}); err != nil || opened[1] != port.SessionMemoryDoc(s.Current().ID) {
		t.Fatalf("会话: err=%v opened=%v", err, opened)
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

// newTestAgent 组装注入脚本流 LLM 的最小 Agent（会话测试用）。
func newTestAgent(t *testing.T) *Agent {
	t.Helper()
	a, err := New(
		Deps{LLM: &scriptLLM{t: t}, UI: &recorder{}, IDs: &seqIDs{}, Clock: fixedClock{testTime}},
		Config{Model: "m"},
	)
	if err != nil {
		t.Fatalf("new agent: %v", err)
	}
	return a
}

// TestSessionTitleAndExitCommands /title 无参显示、有参改写并落盘；/exit = /quit 别名。
// TestSessionJobsCommand /jobs 命令（M3）：list/logs/kill、前缀解析、用法与空表提示。
func TestSessionJobsCommand(t *testing.T) {
	store := newMemStore()
	s, _, _ := newTestSession(t, store)
	jobs := &fakeJobs{
		jobs: []port.Job{{
			ID: "j001", Status: port.JobRunning, PID: 7, StartedAt: testTime,
			Spec: port.JobSpec{Command: "srv", Args: []string{"--port", "1"}},
		}},
		logs: map[port.JobID]string{"j001": "listening\n"},
	}
	s.jobs = jobs

	// list：展示状态与命令行。
	out, err := handleCmd(s, "jobs")
	if err != nil || !strings.Contains(out, "j001") || !strings.Contains(out, "running") ||
		!strings.Contains(out, "srv --port 1") {
		t.Fatalf("list = %q, %v", out, err)
	}
	// logs：前缀解析 + 缺省 tail + 内容回显。
	out, err = handleCmd(s, "jobs", "logs", "j0")
	if err != nil || !strings.Contains(out, "listening") {
		t.Fatalf("logs = %q, %v", out, err)
	}
	if jobs.lastTail != defaultJobLogsTail {
		t.Fatalf("tail = %d, want %d", jobs.lastTail, defaultJobLogsTail)
	}
	// logs：自定义行数透传。
	if _, err := handleCmd(s, "jobs", "logs", "j001", "5"); err != nil {
		t.Fatalf("logs n: %v", err)
	}
	if jobs.lastTail != 5 {
		t.Fatalf("tail = %d, want 5", jobs.lastTail)
	}
	// kill：前缀解析并转发。
	out, err = handleCmd(s, "jobs", "kill", "j0")
	if err != nil || !strings.Contains(out, "已终止 j001") {
		t.Fatalf("kill = %q, %v", out, err)
	}
	if len(jobs.killed) != 1 || jobs.killed[0] != "j001" {
		t.Fatalf("killed = %v", jobs.killed)
	}
	// 未知子命令与坏参数报用法。
	if _, err := handleCmd(s, "jobs", "wat"); err == nil || !strings.Contains(err.Error(), "用法") {
		t.Fatalf("wat err = %v", err)
	}
	if _, err := handleCmd(s, "jobs", "logs", "j001", "-3"); err == nil ||
		!strings.Contains(err.Error(), "正整数") {
		t.Fatalf("bad tail err = %v", err)
	}
	if _, err := handleCmd(s, "jobs", "logs", "j999"); err == nil ||
		!strings.Contains(err.Error(), "no task") {
		t.Fatalf("unknown id err = %v", err)
	}
}

// TestSessionJobsEmpty 空表提示。
func TestSessionJobsEmpty(t *testing.T) {
	store := newMemStore()
	s, _, _ := newTestSession(t, store)
	s.jobs = &fakeJobs{}
	out, err := handleCmd(s, "jobs", "list")
	if err != nil || !strings.Contains(out, "暂无后台任务") {
		t.Fatalf("out = %q, %v", out, err)
	}
}

// TestSessionJobsEdges /jobs 边界：前缀歧义、List 故障上抛、空日志提示。
func TestSessionJobsEdges(t *testing.T) {
	store := newMemStore()
	s, _, _ := newTestSession(t, store)
	jobs := []port.Job{
		{ID: "j001", Status: port.JobDone, StartedAt: testTime, Spec: port.JobSpec{Command: "a"}},
		{ID: "j002", Status: port.JobDone, StartedAt: testTime, Spec: port.JobSpec{Command: "b"}},
	}

	// 前缀歧义（"j0" 命中两个）。
	s.jobs = &fakeJobs{jobs: jobs}
	if _, err := handleCmd(s, "jobs", "logs", "j0"); err == nil ||
		!strings.Contains(err.Error(), "ambiguous") {
		t.Fatalf("歧义 err = %v", err)
	}
	if _, err := handleCmd(s, "jobs", "kill", "j0"); err == nil ||
		!strings.Contains(err.Error(), "ambiguous") {
		t.Fatalf("kill 歧义 err = %v", err)
	}

	// List 故障上抛（不静默）。
	s.jobs = &fakeJobs{err: errors.New("存储炸了")}
	if _, err := handleCmd(s, "jobs"); err == nil || !strings.Contains(err.Error(), "存储炸了") {
		t.Fatalf("list err = %v", err)
	}

	// 空日志提示。
	s.jobs = &fakeJobs{jobs: jobs, logs: map[port.JobID]string{"j001": "  \n"}}
	out, err := handleCmd(s, "jobs", "logs", "j001")
	if err != nil || !strings.Contains(out, "日志为空") {
		t.Fatalf("out = %q, %v", out, err)
	}
}

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
		withUsage(textStream("## Objective\n- 压缩后的摘要"), conversation.Usage{InputTokens: 40, OutputTokens: 15}),
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
	if last.Role != conversation.RoleSystem || last.Content[0].Text != "## Objective\n- 压缩后的摘要" {
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
		return c.Nodes[m.ID]
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
	if n == "" || c.Nodes[c.Head].Parent != n ||
		c.Nodes[c.Head].Content[0].Text != "改后回答" {
		t.Fatalf("编辑后未重新生成: head=%s (%+v) n=%s", c.Head, c.Nodes[c.Head], n)
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
	nuMsg := c.Nodes[nu]
	if nuMsg.Parent != "p" || nuMsg.Role != conversation.RoleUser || nuMsg.Content[0].Text != "问题" {
		t.Fatalf("新 user 节点 = %+v, want 同父同角色原内容", nuMsg)
	}
	// 旧分支原样保留：u1 → a1 → t1 → u2 仍在。
	if got := c.Children["u1"]; len(got) != 1 || got[0] != "a1" {
		t.Fatalf("children[u1] = %v, want 旧分支保留", got)
	}
	// 新回答生成在新 user 之下，Head 到位。
	if c.Nodes[c.Head].Role != conversation.RoleAssistant || c.Nodes[c.Head].Parent != nu {
		t.Fatalf("head = %s (parent %s), want 新 user 下的 assistant 回答", c.Head, c.Nodes[c.Head].Parent)
	}
	if got := c.Nodes[c.Head].Content[0].Text; got != "新的回答" {
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
	ans := c.Nodes[c.Head]
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
	nm := c.Nodes[n]
	if nm.Parent != "p" || nm.Role != conversation.RoleUser || nm.Content[0].Text != "新问题" {
		t.Fatalf("new node = %+v, want 同父同角色新内容", nm)
	}
	if got := c.Children["u1"]; len(got) != 1 || got[0] != "a1" {
		t.Fatalf("children[u1] = %v, want 旧分支原样保留", got)
	}
	// Head = 新分支的回答（编辑即重发，D93）。
	if c.Nodes[c.Head].Parent != n || c.Nodes[c.Head].Role != conversation.RoleAssistant ||
		c.Nodes[c.Head].Content[0].Text != "新回答" {
		t.Fatalf("head = %s (%+v), want 新节点下的回答", c.Head, c.Nodes[c.Head])
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
	a1Before := c.Nodes["a1"].Clone()
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
		"a1": a1Before, "u2": u2Before,
	} {
		if snap(before) != snap(c.Nodes[id]) {
			t.Fatalf("被转移后代 %s 字节被改写:\nbefore %s\nafter  %s", id, snap(before), snap(c.Nodes[id]))
		}
	}
	// 零拷贝：只 +1 个修订节点（子树各节点仍是原对象，无复制）。
	if len(c.Nodes) != 6 {
		t.Fatalf("nodes = %d, want 6（5 + 1 修订节点，零拷贝）", len(c.Nodes))
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
			id, mode, part, text, err := parseEditArgs(tc.args)
			if tc.fail != "" {
				if err == nil || !strings.Contains(err.Error(), tc.fail) {
					t.Fatalf("err = %v, want 含 %q", err, tc.fail)
				}
				return
			}
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			if id != tc.args[0] || mode != tc.mode || part != -1 || text != tc.text {
				t.Fatalf("got %q/%v/%d/%q, want %q/%v/-1/%q", id, mode, part, text, tc.args[0], tc.mode, tc.text)
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
	a1Before, err := json.Marshal(c.Nodes["a1"])
	if err != nil {
		t.Fatalf("marshal a1: %v", err)
	}
	u2Before, err := json.Marshal(c.Nodes["u2"])
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
	nm := c.Nodes[n]
	if nm.Parent != "p" || nm.Role != conversation.RoleUser || nm.Content[0].Text != "新问题" {
		t.Fatalf("new node = %+v, want 同父同角色新内容", nm)
	}
	if got := c.Children["u1"]; len(got) != 1 || got[0] != "a1" {
		t.Fatalf("children[u1] = %v, want 旧分支原样保留", got)
	}
	// 拷贝子树：ID 全新、内容保真、CreatedAt 保留（D96）。
	copied := c.Children[n]
	if len(copied) != 1 || copied[0] == "a1" {
		t.Fatalf("children[new] = %v, want 拷贝出的新 a1（ID 全新）", copied)
	}
	a1c := c.Nodes[copied[0]]
	if a1c.Parent != n || len(a1c.Content) != 2 || a1c.Content[0].Text != "回复" ||
		a1c.Content[1].Tool == nil || a1c.Content[1].Tool.CallID != "k1" {
		t.Fatalf("copied a1 = %+v, want 挂新节点下且内容保真", a1c)
	}
	u2c := c.Children[a1c.ID]
	if len(u2c) != 1 || u2c[0] == "u2" || c.Nodes[u2c[0]].Content[0].Text != "追问" {
		t.Fatalf("copied u2 = %v (%+v), want 新 ID 且内容保真", u2c, c.Nodes[u2c[0]])
	}
	if !a1c.CreatedAt.Equal(c.Nodes["a1"].CreatedAt) {
		t.Fatalf("copied CreatedAt = %v, want 保留原值 %v", a1c.CreatedAt, c.Nodes["a1"].CreatedAt)
	}
	// 旧子树字节级不动（Clone 不改写任何原有对象）。
	after, err := json.Marshal(c.Nodes["a1"])
	if err != nil {
		t.Fatalf("marshal a1 after: %v", err)
	}
	if string(after) != string(a1Before) {
		t.Fatalf("旧 a1 被改写:\nbefore %s\nafter  %s", a1Before, after)
	}
	after, err = json.Marshal(c.Nodes["u2"])
	if err != nil {
		t.Fatalf("marshal u2 after: %v", err)
	}
	if string(after) != string(u2Before) {
		t.Fatalf("旧 u2 被改写:\nbefore %s\nafter  %s", u2Before, after)
	}
	// 节点数 = 5 + 1 修订 + 2 拷贝；Head 平移到拷贝对应节点（旧 Head u2 在原子树内）。
	if len(c.Nodes) != 8 {
		t.Fatalf("nodes = %d, want 8（5 + 1 修订 + 2 拷贝）", len(c.Nodes))
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
	nm := c.Nodes[n]
	// D107③：缺省（无 --part）= 非正文分片原位保留、正文并为新文本——a1 的工具分片
	//（buildTree 产生）随行，不再是 D95 后果⑥ 的整节点替换。
	if nm.Role != conversation.RoleAssistant || len(nm.Content) != 2 ||
		nm.Content[0].Kind != conversation.PartText || nm.Content[0].Text != "改写的回答" ||
		nm.Content[1].Kind != conversation.PartTool || nm.Content[1].Tool == nil ||
		nm.Content[1].Tool.CallID != "k1" {
		t.Fatalf("new node = %+v, want 正文替换 + 工具分片保留", nm)
	}
	if got := c.Children["a1"]; len(got) != 1 || got[0] != "u2" {
		t.Fatalf("children[a1] = %v, want 旧分支原样保留", got)
	}
	if len(c.Nodes) != 6 {
		t.Fatalf("nodes = %d, want 6（修订不触发生成，无追加）", len(c.Nodes))
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
	nm := c.Nodes[revisedID(c, m.ID)]
	if len(nm.Content) != 2 ||
		nm.Content[0].Kind != conversation.PartText || nm.Content[0].Text != "改写文本" ||
		nm.Content[1].Kind != conversation.PartImage || nm.Content[1].Ref == nil || *nm.Content[1].Ref != *ref {
		t.Fatalf("revised content = %+v, want [新文本, 原 image Ref]", nm.Content)
	}
	if _, err := handleCmd(s, "edit", string(m2.ID), "并为一"); err != nil {
		t.Fatalf("edit2: %v", err)
	}
	nm2 := c.Nodes[revisedID(c, m2.ID)]
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
	if len(c.Nodes) != 6 {
		t.Fatalf("nodes = %d, want 6（5 + 1 修订节点，零拷贝）", len(c.Nodes))
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
	// 一条流：u1 的编辑是用户消息（Fresh）→ 即重新生成（D93），Head 落在回答上。
	s, _, _ := newTestSession(t, store, textStream("新回答"))
	c := buildTree(t, s)

	if _, err := handleCmd(s, "edit", "u1", "改"); err != nil {
		t.Fatalf("edit: %v", err)
	}
	n := revisedID(c, "u1")
	if n == "" || c.Nodes[c.Head].Parent != n {
		t.Fatalf("编辑后 Head 应在新分支回答上: head=%s n=%s", c.Head, n)
	}

	// 显式 id：自身 + 同级（新旧版本对比）+ 下级，带修订标记；新分支已有回答，
	// 同级行不再标 Head（D93）。
	out, err := handleCmd(s, "branch", "u1")
	if err != nil {
		t.Fatalf("branch u1: %v", err)
	}
	for _, want := range []string{
		"当前: u1 [user] 问题",
		"同级分叉（1 条，不含自身）:",
		string(n) + " [user] 改",
		"（修订自 u1）",
		"下级（1 条）:",
		"a1 [assistant] 回复",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("branch 缺 %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, string(n)+" [user] 改 ← Head") {
		t.Fatalf("新分支已有回答，同级行不应标 Head:\n%s", out)
	}

	// 缺省 = Head（编辑生成的回答节点，标 Head）。
	out, err = handleCmd(s, "branch")
	if err != nil || !strings.Contains(out, "当前: "+string(c.Head)) ||
		!strings.Contains(out, "[assistant] 新回答 ← Head") {
		t.Fatalf("branch 缺省 = %q, %v", out, err)
	}

	// 无同级分叉 / 错误路径。
	out, err = handleCmd(s, "branch", "a1")
	if err != nil || !strings.Contains(out, "同级分叉: 无") || !strings.Contains(out, "下级（1 条）:\n  u2 [user] 追问") {
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
		if len(c.Nodes) != 5 {
			t.Fatalf("nodes = %d, want 树未改动 5", len(c.Nodes))
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
		// 子树 u1 = {u1,a1,u2} 共 3 条；Head(u2) 回退到最近存活祖先 p。
		if !strings.Contains(out, "共 3 条节点") && !strings.Contains(out, "3 条节点") {
			t.Fatalf("out = %q, want 子树计数 3", out)
		}
		if !strings.Contains(out, "Head → p") {
			t.Fatalf("out = %q, want Head 回退到 p", out)
		}
		for _, id := range []string{"u1", "a1", "u2"} {
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
		if len(s.cur.Nodes) != 5 {
			t.Fatalf("nodes = %d, want 回滚到 5", len(s.cur.Nodes))
		}
		if saved := store.convs[c.ID]; saved == nil || len(saved.Nodes) != 5 {
			t.Fatalf("saved = %v, want 5 节点副本未被改写", saved)
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

// TestSessionTitleTargetsOtherConversation /title --id 改非当前会话标题（D120⑥）：
// 目标落盘、当前会话与其标题不受影响；无标题回显目标标题；--id 缺省作用于当前（回归）；
// 非 --id 的 "--x" 仍按标题文本处理。
func TestSessionTitleTargetsOtherConversation(t *testing.T) {
	store := newMemStore()
	s, _, _ := newTestSession(t, store)
	ctx := context.Background()

	idA := s.Current().ID
	if _, err := s.Handle(ctx, port.UserInput{Command: &port.Command{Name: "new", Args: []string{"乙调"}}}); err != nil {
		t.Fatalf("/new: %v", err)
	}
	idB := s.Current().ID

	out, err := handleCmd(s, "title", "--id", string(idA), "甲改")
	if err != nil || !strings.Contains(out, string(idA)) || !strings.Contains(out, "甲改") {
		t.Fatalf("title --id = %q, %v", out, err)
	}
	if got := store.convs[idA].Title; got != "甲改" {
		t.Fatalf("甲标题 = %q, want 甲改", got)
	}
	if s.Current().ID != idB || s.Current().Title != "乙调" {
		t.Fatalf("当前会话被误动: %s/%q", s.Current().ID, s.Current().Title)
	}
	if got := store.convs[idB].Title; got != "乙调" {
		t.Fatalf("乙标题 = %q, want 乙调", got)
	}

	// 无标题：回显目标标题（不写盘）。
	if out, err = handleCmd(s, "title", "--id", string(idA)); err != nil || !strings.Contains(out, "甲改") {
		t.Fatalf("title --id 查看 = %q, %v", out, err)
	}
	// 错误前缀：报错、不误写。
	if _, err := handleCmd(s, "title", "--id", "ZZZ", "x"); err == nil || !strings.Contains(err.Error(), "没有会话") {
		t.Fatalf("未知 --id err = %v", err)
	}
	// 缺 --id 参数：用法错误（不消费后续文本）。
	if _, err := handleCmd(s, "title", "--id"); err == nil || !strings.Contains(err.Error(), "用法") {
		t.Fatalf("缺参 err = %v", err)
	}
	// --id 缺省：改当前标题（回归）。
	if _, err := handleCmd(s, "title", "乙改名"); err != nil {
		t.Fatalf("title 当前: %v", err)
	}
	if s.Current().Title != "乙改名" {
		t.Fatalf("当前标题 = %q, want 乙改名", s.Current().Title)
	}
	// "--" 开头但非 --id：按标题文本处理。
	if _, err := handleCmd(s, "title", "--你好", "世界"); err != nil {
		t.Fatalf("title 文本: %v", err)
	}
	if s.Current().Title != "--你好 世界" {
		t.Fatalf("标题 = %q, want --你好 世界", s.Current().Title)
	}
}

// TestSessionRmconvOtherConversation /rmconv 删非当前会话：目标从库中消失、当前会话不动、
// 恰好一次二次确认。
func TestSessionRmconvOtherConversation(t *testing.T) {
	store := newMemStore()
	s, conf := newConfirmedSession(t, store, []bool{true})
	ctx := context.Background()
	idA := s.Current().ID
	if _, err := s.Handle(ctx, port.UserInput{Command: &port.Command{Name: "new", Args: []string{"乙"}}}); err != nil {
		t.Fatalf("/new: %v", err)
	}
	idB := s.Current().ID

	out, err := handleCmd(s, "rmconv", string(idA))
	if err != nil || !strings.Contains(out, "已删除会话") {
		t.Fatalf("rmconv = %q, %v", out, err)
	}
	if _, ok := store.convs[idA]; ok {
		t.Fatal("甲会话应已从库中删除")
	}
	if s.Current().ID != idB {
		t.Fatalf("当前会话 = %s, want %s（删非当前不应动 Head）", s.Current().ID, idB)
	}
	if len(conf.asked) != 1 || !strings.Contains(conf.asked[0].prompt, string(idA)) {
		t.Fatalf("确认 = %+v, want 恰一次且提示含目标 id", conf.asked)
	}
}

// TestSessionRmconvCurrentSuccessor 删当前会话 → 继任到其余会话并清屏（D75/D81 口径）。
func TestSessionRmconvCurrentSuccessor(t *testing.T) {
	store := newMemStore()
	rec := &recorder{}
	conf := &scriptConfirmer{t: t, answers: []bool{true}}
	s, err := NewSession(context.Background(), SessionDeps{
		Store: store, Agent: newAgent(t, &scriptLLM{t: t}, rec, Deps{}, Config{}),
		IDs: &seqIDs{}, Clock: fixedClock{testTime}, UI: rec, Confirmer: conf,
	})
	if err != nil {
		t.Fatalf("new session: %v", err)
	}
	idA := s.Current().ID
	if _, err := s.Handle(context.Background(), port.UserInput{Command: &port.Command{Name: "new", Args: []string{"乙"}}}); err != nil {
		t.Fatalf("/new: %v", err)
	}
	idB := s.Current().ID

	// 切回甲，使「删除对象 = 当前会话」。
	if _, err := s.Handle(context.Background(), port.UserInput{Command: &port.Command{Name: "switch", Args: []string{string(idA)}}}); err != nil {
		t.Fatalf("/switch: %v", err)
	}
	rec.events = nil
	out, err := handleCmd(s, "rmconv", string(idA))
	if err != nil || !strings.Contains(out, "已删除当前会话") || !strings.Contains(out, string(idB)) {
		t.Fatalf("rmconv 当前 = %q, %v", out, err)
	}
	if _, ok := store.convs[idA]; ok {
		t.Fatal("当前会话应已删除")
	}
	if s.Current().ID != idB {
		t.Fatalf("继任 = %s, want %s", s.Current().ID, idB)
	}
	// 继任会话（仅 Root+persona）无历史：清屏、无 notice。
	if names := eventNames(rec.events); len(names) != 1 || names[0] != "clear" {
		t.Fatalf("events = %v, want [clear]", names)
	}
}

// TestSessionRmconvLastCreatesEmpty 删唯一会话 → 新建空会话（保持「总有当前会话」不变量）。
func TestSessionRmconvLastCreatesEmpty(t *testing.T) {
	store := newMemStore()
	s, _ := newConfirmedSession(t, store, []bool{true})
	idA := s.Current().ID

	out, err := handleCmd(s, "rmconv", string(idA))
	if err != nil || !strings.Contains(out, "已切换到") {
		t.Fatalf("rmconv 唯一 = %q, %v", out, err)
	}
	if _, ok := store.convs[idA]; ok {
		t.Fatal("旧会话应已删除")
	}
	cur := s.Current()
	if cur.ID == idA {
		t.Fatal("应新建继任会话（旧 id 不得复用）")
	}
	if cur.Title != defaultTitle {
		t.Fatalf("继任标题 = %q, want %q", cur.Title, defaultTitle)
	}
	if len(store.convs) != 1 {
		t.Fatalf("库 = %d 个会话, want 仅新会话", len(store.convs))
	}
	if saved := store.convs[cur.ID]; saved == nil || saved.ID != cur.ID {
		t.Fatalf("新会话未落盘: %+v", saved)
	}
	if err := cur.Validate(); err != nil {
		t.Fatalf("新会话不合法: %v", err)
	}
}

// TestSessionRmconvGuards /rmconv 的用法/确认器/拒绝三分支。
func TestSessionRmconvGuards(t *testing.T) {
	t.Run("用法与未知 id", func(t *testing.T) {
		s, _ := newConfirmedSession(t, newMemStore(), nil)
		if _, err := handleCmd(s, "rmconv"); err == nil || !strings.Contains(err.Error(), "用法") {
			t.Fatalf("用法 err = %v", err)
		}
		if _, err := handleCmd(s, "rmconv", "ZZZ"); err == nil || !strings.Contains(err.Error(), "没有会话") {
			t.Fatalf("未知 id err = %v", err)
		}
	})

	t.Run("未配置确认器", func(t *testing.T) {
		store := newMemStore()
		agent := newAgent(t, &scriptLLM{t: t}, &recorder{}, Deps{}, Config{})
		s, err := NewSession(context.Background(), SessionDeps{
			Store: store, Agent: agent, IDs: &seqIDs{}, Clock: fixedClock{testTime},
		})
		if err != nil {
			t.Fatalf("new session: %v", err)
		}
		id := s.Current().ID
		if _, err := handleCmd(s, "rmconv", string(id)); err == nil || !strings.Contains(err.Error(), "未配置确认器") {
			t.Fatalf("err = %v, want 未配置确认器", err)
		}
		if _, ok := store.convs[id]; !ok {
			t.Fatal("无确认器时不得删除")
		}
	})

	t.Run("拒绝", func(t *testing.T) {
		store := newMemStore()
		s, _ := newConfirmedSession(t, store, []bool{false})
		id := s.Current().ID
		out, err := handleCmd(s, "rmconv", string(id))
		if err != nil || !strings.Contains(out, "已取消删除会话") {
			t.Fatalf("deny = %q, %v", out, err)
		}
		if _, ok := store.convs[id]; !ok {
			t.Fatal("拒绝后会话不应删除")
		}
		if s.Current().ID != id {
			t.Fatal("拒绝后当前会话不应变")
		}
	})
}

// TestSessionEditPartFlag --part N 只在 flag 位识别（D107②）：与 --keep/--copy 同区、
// 任意顺序同用；非数字/缺序号/负数按用法拒绝；字面 "--part" 之外的 "--x" 仍归文本。
func TestSessionEditPartFlag(t *testing.T) {
	cases := []struct {
		name string
		args []string
		part int
		mode conversation.KeepMode
		text string
		fail string
	}{
		{"part 紧跟 id", []string{"a1", "--part", "1", "新文本"}, 1, conversation.Fresh, "新文本", ""},
		{"part 与 keep 同用", []string{"a1", "--keep", "--part", "0", "x"}, 0, conversation.Carry, "x", ""},
		{"part 在 keep 前", []string{"a1", "--part", "2", "--copy", "x"}, 2, conversation.Clone, "x", ""},
		{"part 缺序号", []string{"a1", "--part"}, -1, conversation.Fresh, "", "用法"},
		{"part 非数字", []string{"a1", "--part", "x", "t"}, -1, conversation.Fresh, "", "用法"},
		{"part 为负", []string{"a1", "--part", "-1", "t"}, -1, conversation.Fresh, "", "用法"},
		{"空参数", nil, -1, conversation.Fresh, "", "用法"},
		{"字面未知 flag 归文本", []string{"a1", "--partx", "t"}, -1, conversation.Fresh, "--partx t", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			id, mode, part, text, err := parseEditArgs(tc.args)
			if tc.fail != "" {
				if err == nil || !strings.Contains(err.Error(), tc.fail) {
					t.Fatalf("err = %v, want 含 %q", err, tc.fail)
				}
				return
			}
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			if id != "a1" || part != tc.part || mode != tc.mode || text != tc.text {
				t.Fatalf("got %q/%d/%v/%q, want a1/%d/%v/%q", id, part, mode, text, tc.part, tc.mode, tc.text)
			}
		})
	}
}

// TestSessionEditAssistantPartFidelity 带工具的助手 Turn 编辑分片保真（D107，用户实测
// 「前半段丢失」的回归测试）：[思考, 前半段, 工具, 后半段] 编辑 --part 1 只换后半段；
// --part 0 只换前半段；缺省并段但思考/工具保留；越界报错。
func TestSessionEditAssistantPartFidelity(t *testing.T) {
	store := newMemStore()
	s, _, _ := newTestSession(t, store)
	c := buildTree(t, s)

	mk, err := c.Append(conversation.RoleAssistant, []conversation.Part{
		{Kind: conversation.PartThinking, Text: "思考过程"},
		{Kind: conversation.PartText, Text: "前半段"},
		{Kind: conversation.PartTool, Tool: &conversation.ToolPart{
			CallID: "t9", Name: "file_read", Args: json.RawMessage(`{"path":"x"}`),
			Result: &tool.Result{CallID: "t9", OK: true, Output: "内容"},
		}},
		{Kind: conversation.PartText, Text: "后半段"},
	})
	if err != nil {
		t.Fatalf("append turn: %v", err)
	}
	origTool := func(msg conversation.Message) *conversation.ToolPart {
		for _, p := range msg.Content {
			if p.Kind == conversation.PartTool {
				return p.Tool
			}
		}
		return nil
	}

	// --part 1：只换后半段，思考/前半段/工具逐字节保留。
	if _, err := handleCmd(s, "edit", string(mk.ID), "--part", "1", "改写后半段"); err != nil {
		t.Fatalf("edit --part 1: %v", err)
	}
	n1 := revisedID(c, mk.ID)
	nm1 := c.Nodes[n1]
	if len(nm1.Content) != 4 ||
		nm1.Content[0].Kind != conversation.PartThinking || nm1.Content[0].Text != "思考过程" ||
		nm1.Content[1].Text != "前半段" ||
		nm1.Content[2].Kind != conversation.PartTool || nm1.Content[2].Tool.CallID != "t9" ||
		nm1.Content[3].Text != "改写后半段" {
		t.Fatalf("--part 1 内容 = %+v", nm1.Content)
	}
	if got, want := nm1.Content[2].Tool, origTool(mk); got.Args == nil || string(got.Args) != string(want.Args) {
		t.Fatalf("工具分片未逐字节保留: %+v vs %+v", got, want)
	}

	// --part 0（对上一修订节点链式再修，避免 revisedID 多笔歧义）：只换前半段。
	cur := revisedID(c, mk.ID)
	if _, err := handleCmd(s, "edit", string(cur), "--part", "0", "改写前半段"); err != nil {
		t.Fatalf("edit --part 0: %v", err)
	}
	nm0 := c.Nodes[revisedID(c, cur)]
	if len(nm0.Content) != 4 || nm0.Content[1].Text != "改写前半段" || nm0.Content[3].Text != "改写后半段" {
		t.Fatalf("--part 0 内容 = %+v", nm0.Content)
	}

	// 缺省：正文并为一条、置于首个正文位，思考/工具保留（D107③）。
	cur = revisedID(c, cur)
	if _, err := handleCmd(s, "edit", string(cur), "合并正文"); err != nil {
		t.Fatalf("edit 缺省: %v", err)
	}
	nmD := c.Nodes[revisedID(c, cur)]
	if len(nmD.Content) != 3 ||
		nmD.Content[0].Kind != conversation.PartThinking ||
		nmD.Content[1].Text != "合并正文" ||
		nmD.Content[2].Kind != conversation.PartTool || nmD.Content[2].Tool.CallID != "t9" {
		t.Fatalf("缺省内容 = %+v", nmD.Content)
	}

	// 越界：报错（不产生可续修状态，链上最后节点即当前 Head）。
	cur = revisedID(c, cur)
	if _, err := handleCmd(s, "edit", string(cur), "--part", "5", "x"); err == nil ||
		!strings.Contains(err.Error(), "越界") {
		t.Fatalf("越界 err = %v, want 含 越界", err)
	}
}
