package app

// session_exchange_e2e_test.go 会话进出的端到端验收（DESIGN §5.16 的验收口径）：
// 走**命令面**（不是直接调适配器）导一份 `aqx` 出去、再导回来，逐项比对两棵树。
//
// 与适配器侧的往返测试分工：那边证明「格式能原样搬运」，这边证明「接上命令面之后仍然是
// 同一棵树」——路径挑选、ID 重分配、落新会话、清屏回放、回执这几步都在中间，任何一步写错
// 都会让导回来的树少一点东西。
//
// 借用真实适配器（临时目录里的 blobfs / storejson / exchange）属于链路级验收，
// AGENTS.md 允许测试这么做；生产代码的跨层直连仍由 arch 守卫盯着。

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Tonyjh07/Aquarius/internal/adapter/blobfs"
	"github.com/Tonyjh07/Aquarius/internal/adapter/exchange"
	"github.com/Tonyjh07/Aquarius/internal/adapter/storejson"
	"github.com/Tonyjh07/Aquarius/internal/domain/conversation"
	"github.com/Tonyjh07/Aquarius/internal/domain/tool"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

// newExchangeE2ESession 装一套真装配（磁盘会话库 + 附件库 + 进出适配器）的会话。
// 附件目录与导出目录都在临时目录下，导出的文件可以事后核对。
func newExchangeE2ESession(t *testing.T, streams ...*scriptStream) (*Session, *blobfs.Store, string) {
	t.Helper()
	dir := t.TempDir()
	blobs, err := blobfs.New(filepath.Join(dir, "attachments"))
	if err != nil {
		t.Fatalf("blobfs: %v", err)
	}
	store, err := storejson.New(filepath.Join(dir, "conversations"))
	if err != nil {
		t.Fatalf("storejson: %v", err)
	}
	exports := filepath.Join(dir, "exports")
	rec := &recorder{}
	llm := &scriptLLM{t: t, streams: streams}
	agent := newAgent(t, llm, rec, Deps{Blobs: blobs}, Config{})
	ex := exchange.New(exchange.Options{Blobs: blobs})
	s, err := NewSession(context.Background(), SessionDeps{
		Store:      store,
		Agent:      agent,
		IDs:        &seqIDs{},
		Clock:      fixedClock{testTime},
		UI:         rec,
		Exporter:   ex,
		Importer:   ex,
		ExportsDir: exports,
	})
	if err != nil {
		t.Fatalf("new session: %v", err)
	}
	return s, blobs, exports
}

// seedExchangeTree 往当前会话里放一棵「字段尽量用满」的树：分叉、版本链、思考分片、
// 工具分片、附件引用。往返等价要在这几种东西上比，才测得出「哪个字段被静默丢了」。
func seedExchangeTree(t *testing.T, s *Session, blobs *blobfs.Store) {
	t.Helper()
	c := s.Current()
	if _, err := c.Append(conversation.RoleUser, []conversation.Part{
		{Kind: conversation.PartText, Text: "你好"},
	}); err != nil {
		t.Fatalf("append user: %v", err)
	}
	ref, err := blobs.Put(context.Background(), strings.NewReader("图片字节"), "image/png", "pic.png")
	if err != nil {
		t.Fatalf("put blob: %v", err)
	}
	if _, err := c.Append(conversation.RoleUser, []conversation.Part{
		{Kind: conversation.PartImage, Ref: &ref},
	}); err != nil {
		t.Fatalf("append image: %v", err)
	}
	if _, err := c.Append(conversation.RoleAssistant, []conversation.Part{
		{Kind: conversation.PartThinking, Text: "想一想", Signature: "anthropic:sig"},
		{Kind: conversation.PartText, Text: "在"},
		{Kind: conversation.PartTool, Tool: &conversation.ToolPart{
			CallID: "k1", Name: "file_read", Args: json.RawMessage(`{"path":"a.txt"}`),
			Result: &tool.Result{CallID: "k1", OK: true, Output: "文件内容"},
		}},
	}); err != nil {
		t.Fatalf("append assistant: %v", err)
	}
	if err := c.AppendCommitted(conversation.Message{
		ID: "J1", Parent: c.Head, Role: conversation.RoleInject,
		Content: []conversation.Part{{Kind: conversation.PartText, Text: "任务完成"}},
		Source:  "job:j1", Trust: conversation.TrustHost, CreatedAt: testTime,
	}); err != nil {
		t.Fatalf("append inject: %v", err)
	}
	// 分叉：再答一条（旁支），然后把带图那条 user 改掉（Fresh）——树里因此既有旁支又有版本链。
	// 改完再把 Head 挪回注入节点：线性格式要能导出「含思考与工具的那条链」，得让它在当前
	// 这条链上（否则它们只会被算进「旁支上的 N 条」）。
	if _, err := c.Append(conversation.RoleAssistant, []conversation.Part{
		{Kind: conversation.PartText, Text: "旁支"},
	}); err != nil {
		t.Fatalf("append branch: %v", err)
	}
	if _, err := c.Revise(c.Path()[3].ID,
		[]conversation.Part{{Kind: conversation.PartText, Text: "带图那条（改了）"}}, conversation.Fresh); err != nil {
		t.Fatalf("revise: %v", err)
	}
	if err := c.Checkout("J1"); err != nil {
		t.Fatalf("checkout: %v", err)
	}
}

// TestExchangeAQXRoundtripThroughCommands /export（缺省 aqx）→ /import 得到等价会话。
func TestExchangeAQXRoundtripThroughCommands(t *testing.T) {
	s, blobs, exports := newExchangeE2ESession(t)
	seedExchangeTree(t, s, blobs)
	srcCanon := canonTree(t, s.Current())
	srcTitle := s.Current().Title
	srcNodes := s.Current().NodeCount()

	out, err := s.Handle(context.Background(), port.UserInput{
		Command: &port.Command{Name: "export"},
	})
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	path := filepath.Join(exports, port.FileStem(srcTitle)+".aqx")
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("导出的文件不在 %s: %v\n%s", path, err, out)
	}
	for _, want := range []string{path, "aqx", "无损", fmt.Sprintf("%d 条消息", srcNodes-1)} {
		if !strings.Contains(out, want) {
			t.Fatalf("导出回执缺 %q:\n%s", want, out)
		}
	}

	oldID := s.Current().ID
	out, err = s.Handle(context.Background(), port.UserInput{
		Command: &port.Command{Name: "import", Args: []string{path}},
	})
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if s.Current().ID == oldID {
		t.Fatal("导入该落成新会话，而不是覆盖当前会话")
	}
	if got := s.Current().NodeCount(); got != srcNodes {
		t.Fatalf("导入后节点数 = %d, want %d", got, srcNodes)
	}
	if s.Current().Title != srcTitle {
		t.Fatalf("导入后标题 = %q, want %q", s.Current().Title, srcTitle)
	}
	if !strings.Contains(out, "已落成新会话") || strings.Contains(out, "没有带过去") {
		t.Fatalf("无损导入回执不该列丢东西:\n%s", out)
	}
	// 逐项等价：节点内容、结构、Head、版本链全都对得上（ID 一律重分配，故比对规范形）。
	if got, want := canonTree(t, s.Current()), srcCanon; got != want {
		t.Fatalf("导回来不是同一棵树:\n--- want ---\n%s\n--- got ---\n%s", want, got)
	}
	// 旧会话仍在库里（导入不覆盖），新会话也已落盘。
	store := s.store
	if _, err := store.Load(context.Background(), oldID); err != nil {
		t.Fatalf("旧会话应仍在库里: %v", err)
	}
	if _, err := store.Load(context.Background(), s.Current().ID); err != nil {
		t.Fatalf("导入的会话应已落盘: %v", err)
	}
}

// TestExchangeLossyExportThroughCommands 线性格式走命令面：回执如实列丢什么，产物能被自己认回来。
func TestExchangeLossyExportThroughCommands(t *testing.T) {
	s, blobs, _ := newExchangeE2ESession(t)
	seedExchangeTree(t, s, blobs)
	target := filepath.Join(t.TempDir(), "对话.json")
	out, err := s.Handle(context.Background(), port.UserInput{
		Command: &port.Command{Name: "export", Args: []string{"--format", "openai", "--path", target}},
	})
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	for _, want := range []string{"有损", "没有带过去", "思考过程", "附件", "旁支"} {
		if !strings.Contains(out, want) {
			t.Fatalf("有损导出回执缺 %q:\n%s", want, out)
		}
	}
	before, srcNodes := s.Current().ID, s.Current().NodeCount()
	if _, err := s.Handle(context.Background(), port.UserInput{
		Command: &port.Command{Name: "import", Args: []string{target}},
	}); err != nil {
		t.Fatalf("import: %v", err)
	}
	if s.Current().ID == before {
		t.Fatal("导入该落成新会话")
	}
	// 线性格式只带得回那条链上的正文：节点数必然少于原树（旁支、思考与工具分片都没了）。
	if got := s.Current().NodeCount(); got >= srcNodes {
		t.Fatalf("线性导入的节点数 = %d，不该与原树的 %d 一样多", got, srcNodes)
	}
}

// canonTree 树的规范形：抹掉 ID，只留「结构和内容如何相连」。
//
// 为什么不能直接比快照：导入按口径重分配全部节点 ID（源 ID 只用来建边），两张快照的 ID
// 必然不同。把节点按「从 Root 深度优先、兄弟按结构序」编号，ID 的差异就消失了，剩下的
// 差异才是真差异。
func canonTree(t *testing.T, c *conversation.Conversation) string {
	t.Helper()
	snap := c.Snapshot()
	nodes := make(map[conversation.MessageID]conversation.Message, len(snap.Nodes))
	for _, m := range snap.Nodes {
		nodes[m.ID] = m
	}
	kids := make(map[conversation.MessageID][]conversation.MessageID, len(snap.Edges))
	for _, e := range snap.Edges {
		kids[e.Parent] = e.Childs
	}
	idx := make(map[conversation.MessageID]int, len(snap.Nodes))
	var order []conversation.MessageID
	var walk func(id conversation.MessageID)
	walk = func(id conversation.MessageID) {
		idx[id] = len(order)
		order = append(order, id)
		for _, k := range kids[id] {
			walk(k)
		}
	}
	walk(conversation.MessageID(snap.ID))

	var b strings.Builder
	fmt.Fprintf(&b, "title=%s\n", snap.Title)
	head, ok := idx[snap.Head]
	if !ok {
		t.Fatalf("Head %q 不在树里", snap.Head)
	}
	fmt.Fprintf(&b, "head=%d\n", head)
	for i, id := range order {
		m, ok := nodes[id]
		if !ok {
			t.Fatalf("边指向树外节点 %q", id)
		}
		parts, err := json.Marshal(m.Content)
		if err != nil {
			t.Fatalf("序列化节点内容: %v", err)
		}
		fmt.Fprintf(&b, "[%d] role=%s outcome=%s model=%s source=%s trust=%s trigger=%v usage=%+v at=%s parts=%s\n",
			i, m.Role, m.Outcome, m.Model, m.Source, m.Trust, m.Trigger, m.Usage,
			// 内存里的时间带单调时钟读数、落盘后没有：比对前统一去掉（Round(0) 只保留墙钟）。
			m.CreatedAt.Round(0).UTC().Format("2006-01-02T15:04:05Z07:00"), parts)
	}
	for _, ed := range snap.RevisedFrom {
		a, ok1 := idx[ed.New]
		o, ok2 := idx[ed.Old]
		if !ok1 || !ok2 {
			t.Fatalf("版本链指向树外: %s -> %s", ed.New, ed.Old)
		}
		fmt.Fprintf(&b, "revised %d->%d\n", a, o)
	}
	return b.String()
}
