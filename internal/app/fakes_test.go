package app

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/Tonyjh07/Aquarius/internal/domain/conversation"
	"github.com/Tonyjh07/Aquarius/internal/domain/tool"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

// ---------------------------------------------------------------------------
// 脚本化 LLM（录制请求 + 按脚本回放流；DESIGN §11 app 层测试手段）
// ---------------------------------------------------------------------------

// scriptStep 一步：delta 非零值时返回增量；err 非 nil 时以该错误终止流。
type scriptStep struct {
	delta port.Delta
	err   error
}

// scriptStream 按脚本回放的生成流；脚本走尽即 io.EOF。
type scriptStream struct {
	steps []scriptStep
	i     int
}

func (s *scriptStream) Recv() (port.Delta, error) {
	if s.i >= len(s.steps) {
		return port.Delta{}, io.EOF
	}
	step := s.steps[s.i]
	s.i++
	if step.err != nil {
		return port.Delta{}, step.err
	}
	return step.delta, nil
}

func (s *scriptStream) Close() error { s.i = len(s.steps); return nil }

// scriptLLM 每次 Generate 弹出一个脚本流，并记录全部请求。
type scriptLLM struct {
	t        *testing.T
	requests []port.GenerateRequest
	streams  []*scriptStream
	genErr   error
}

func (l *scriptLLM) Generate(_ context.Context, req port.GenerateRequest) (port.Stream, error) {
	l.requests = append(l.requests, req)
	if l.genErr != nil {
		return nil, l.genErr
	}
	if len(l.streams) == 0 {
		l.t.Fatalf("scriptLLM: 第 %d 次 Generate 没有对应脚本", len(l.requests))
	}
	s := l.streams[0]
	l.streams = l.streams[1:]
	return s, nil
}

func (l *scriptLLM) Models(context.Context) ([]port.ModelInfo, error) { return nil, nil }

// textStream 文本流脚本快捷构造。
func textStream(parts ...string) *scriptStream {
	s := &scriptStream{}
	for _, p := range parts {
		s.steps = append(s.steps, scriptStep{delta: port.Delta{Text: p}})
	}
	return s
}

// withUsage 给流追加流末尾用量分片。
func withUsage(s *scriptStream, u conversation.Usage) *scriptStream {
	cp := *s
	cp.steps = append(append([]scriptStep{}, s.steps...), scriptStep{delta: port.Delta{Usage: &u}})
	return &cp
}

// ---------------------------------------------------------------------------
// 脚本队列 Prompter（交互回放的输入端；DESIGN §11 app 层测试手段）
// ---------------------------------------------------------------------------

// scriptPrompter 按脚本行回放用户输入；解析规则与 repl.Next 一致（斜杠开头 = 命令）。
type scriptPrompter struct {
	lines   []string
	i       int
	lastRaw string // 当前行原文（回显进 transcript 用）
}

var _ port.Prompter = (*scriptPrompter)(nil)

func (p *scriptPrompter) Next(context.Context) (port.UserInput, error) {
	if p.i >= len(p.lines) {
		return port.UserInput{}, io.EOF
	}
	line := strings.TrimSpace(p.lines[p.i])
	p.i++
	p.lastRaw = line
	if line == "" {
		return port.UserInput{}, nil
	}
	if strings.HasPrefix(line, "/") {
		fields := strings.Fields(line)
		return port.UserInput{Command: &port.Command{
			Name: strings.TrimPrefix(fields[0], "/"),
			Args: fields[1:],
		}}, nil
	}
	return port.UserInput{Text: line}, nil
}

// ---------------------------------------------------------------------------
// 脚本确认器（自动应答；DESIGN §5.10 Confirmer 测试替身）
// ---------------------------------------------------------------------------

// askedConfirm 一次已应答的确认请求。
type askedConfirm struct {
	prompt string
	answer bool
}

// scriptConfirmer 按脚本应答：记录提示并弹出预置答案；脚本走尽即测试失败
// （能抓住"不该确认却确认了"之类的多余调用）。
type scriptConfirmer struct {
	t       *testing.T
	answers []bool
	asked   []askedConfirm
}

var _ port.Confirmer = (*scriptConfirmer)(nil)

func (c *scriptConfirmer) Confirm(_ context.Context, prompt string) (bool, error) {
	if len(c.answers) == 0 {
		c.t.Fatalf("scriptConfirmer: 没有对应脚本的确认请求: %s", prompt)
	}
	a := c.answers[0]
	c.answers = c.answers[1:]
	c.asked = append(c.asked, askedConfirm{prompt: prompt, answer: a})
	return a, nil
}

// drain 取出并清空已应答的确认记录（每步回放渲染一次）。
func (c *scriptConfirmer) drain() []askedConfirm {
	out := c.asked
	c.asked = nil
	return out
}

// ---------------------------------------------------------------------------
// 收集器 Presenter
// ---------------------------------------------------------------------------

// recorder 收集全部事件。
type recorder struct{ events []port.Event }

func (r *recorder) Emit(_ context.Context, ev port.Event) error {
	r.events = append(r.events, ev)
	return nil
}

// eventNames 提取事件类型名序列（断言事件顺序用）。
func eventNames(events []port.Event) []string {
	out := make([]string, 0, len(events))
	for _, ev := range events {
		switch ev.(type) {
		case port.DeltaEvent:
			out = append(out, "delta")
		case port.CommittedEvent:
			out = append(out, "committed")
		case port.ToolCallEvent:
			out = append(out, "tool_call")
		case port.ToolResultEvent:
			out = append(out, "tool_result")
		case port.ErrorEvent:
			out = append(out, "error")
		case port.NoticeEvent:
			out = append(out, "notice")
		default:
			out = append(out, fmt.Sprintf("%T", ev))
		}
	}
	return out
}

// ---------------------------------------------------------------------------
// 确定性 Clock / IDGen
// ---------------------------------------------------------------------------

// fixedClock 固定时钟。
type fixedClock struct{ t time.Time }

func (c fixedClock) Now() time.Time { return c.t }

// seqCounter 测试内全局递增计数器：模拟真实 ULID 的进程级唯一
// （不同 IDGen 实例、不同"进程重启"间也不重复）。
var seqCounter int

// seqIDs 递增 ID 生成器。
type seqIDs struct{}

func (g *seqIDs) ConversationID() conversation.ID {
	seqCounter++
	return conversation.ID(fmt.Sprintf("C%d", seqCounter))
}
func (g *seqIDs) MessageID() conversation.MessageID {
	seqCounter++
	return conversation.MessageID(fmt.Sprintf("M%d", seqCounter))
}
func (g *seqIDs) CallID() tool.CallID {
	seqCounter++
	return tool.CallID(fmt.Sprintf("K%d", seqCounter))
}

// ulidIDs 与 domain 同源的 ULID 生成器（golden 回放用：ID 恒 26 字符、无前缀特例，
// 且不依赖包级计数器——单跑与全量跑的回放结果一致）。
type ulidIDs struct{}

var _ port.IDGen = ulidIDs{}

func (ulidIDs) ConversationID() conversation.ID   { return conversation.NewID() }
func (ulidIDs) MessageID() conversation.MessageID { return conversation.NewMessageID() }
func (ulidIDs) CallID() tool.CallID               { return tool.CallID(conversation.NewMessageID()) }

// ---------------------------------------------------------------------------
// 工具执行替身
// ---------------------------------------------------------------------------

// fakeRunner 按 CallID 返回预设结果/错误。
type fakeRunner struct {
	specs   []tool.Spec
	results map[tool.CallID]tool.Result
	errs    map[tool.CallID]error
	ran     []tool.Call
}

func (r *fakeRunner) Specs(context.Context) ([]tool.Spec, error) { return r.specs, nil }

func (r *fakeRunner) Execute(_ context.Context, call tool.Call) (tool.Result, error) {
	r.ran = append(r.ran, call)
	if err, ok := r.errs[call.ID]; ok {
		return tool.Result{}, err
	}
	if res, ok := r.results[call.ID]; ok {
		return res, nil
	}
	return tool.Result{CallID: call.ID, OK: true, Output: "ok:" + call.Name}, nil
}

// ---------------------------------------------------------------------------
// 记忆端口替身（M2）
// ---------------------------------------------------------------------------

// fakeMemory map 实现的 port.MemoryStore（记忆注入/工具回放用）。
type fakeMemory struct{ docs map[string]string }

var _ port.MemoryStore = (*fakeMemory)(nil)

func newFakeMemory() *fakeMemory { return &fakeMemory{docs: map[string]string{}} }

func (m *fakeMemory) Index(context.Context) ([]port.MemoryIndexEntry, error) {
	var out []port.MemoryIndexEntry
	for name, content := range m.docs {
		if strings.TrimSpace(content) == "" {
			continue
		}
		out = append(out, port.MemoryIndexEntry{Name: name, Summary: strings.Split(content, "\n")[0]})
	}
	return out, nil
}

func (m *fakeMemory) Read(_ context.Context, name string) (port.MemoryDoc, error) {
	c, ok := m.docs[name]
	if !ok {
		return port.MemoryDoc{}, port.ErrMemoryNotFound
	}
	return port.MemoryDoc{Name: name, Content: c}, nil
}

func (m *fakeMemory) Write(_ context.Context, doc port.MemoryDoc) error {
	m.docs[doc.Name] = doc.Content
	return nil
}

func (m *fakeMemory) Remove(_ context.Context, name string) error {
	if _, ok := m.docs[name]; !ok {
		return port.ErrMemoryNotFound
	}
	delete(m.docs, name)
	return nil
}

func (m *fakeMemory) Search(_ context.Context, q string) ([]port.MemoryHit, error) {
	var out []port.MemoryHit
	for name, content := range m.docs {
		for i, line := range strings.Split(content, "\n") {
			if strings.Contains(strings.ToLower(line), strings.ToLower(q)) {
				out = append(out, port.MemoryHit{Name: name, Line: i + 1, Snippet: line})
			}
		}
	}
	return out, nil
}

// ---------------------------------------------------------------------------
// 附件库替身
// ---------------------------------------------------------------------------

// fakeBlobs 内存附件库（只实现读路径）。
type fakeBlobs struct{ data map[string][]byte }

func (b *fakeBlobs) Put(context.Context, io.Reader, string, string) (conversation.BlobRef, error) {
	panic("not implemented")
}

func (b *fakeBlobs) Get(_ context.Context, ref conversation.BlobRef) (io.ReadCloser, error) {
	data, ok := b.data[ref.Hash]
	if !ok {
		return nil, fmt.Errorf("blob %s not found", ref.Hash)
	}
	return io.NopCloser(bytes.NewReader(data)), nil
}

func (b *fakeBlobs) Stat(_ context.Context, ref conversation.BlobRef) (bool, error) {
	_, ok := b.data[ref.Hash]
	return ok, nil
}

func (b *fakeBlobs) GC(context.Context, map[string]bool) error { return nil }

// ---------------------------------------------------------------------------
// 公共构造
// ---------------------------------------------------------------------------

// testTime 固定时间戳。
var testTime = time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)

// newConv 造一棵带单条 user 消息的会话。
func newConv(t *testing.T) *conversation.Conversation {
	t.Helper()
	c := conversation.New(conversation.ID("conv"), "t")
	if _, err := c.Append(conversation.RoleUser, []conversation.Part{{Kind: conversation.PartText, Text: "hi"}}); err != nil {
		t.Fatalf("append user: %v", err)
	}
	return c
}

// newAgent 组装带全套替身的 Agent。
func newAgent(t *testing.T, llm *scriptLLM, rec *recorder, d Deps, cfg Config) *Agent {
	t.Helper()
	d.LLM = llm
	d.UI = rec
	if d.IDs == nil {
		d.IDs = &seqIDs{}
	}
	if d.Clock == nil {
		d.Clock = fixedClock{testTime}
	}
	if cfg.Model == "" {
		cfg.Model = "test-model"
	}
	a, err := New(d, cfg)
	if err != nil {
		t.Fatalf("new agent: %v", err)
	}
	return a
}
