package storejson

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/Tonyjh07/Aquarius/internal/domain/conversation"
	"github.com/Tonyjh07/Aquarius/internal/domain/tool"
)

// mustConv 造一棵小会话：u1(user) ← a1(assistant)。
func mustConv(t *testing.T) *conversation.Conversation {
	t.Helper()
	c := conversation.New(conversation.ID("conv-1"), "测试会话")
	if _, err := c.Append(conversation.RoleUser, []conversation.Part{{Kind: conversation.PartText, Text: "你好"}}); err != nil {
		t.Fatalf("append user: %v", err)
	}
	if _, err := c.Append(conversation.RoleAssistant, []conversation.Part{{Kind: conversation.PartText, Text: "你好！"}}); err != nil {
		t.Fatalf("append assistant: %v", err)
	}
	return c
}

func TestSaveLoadRoundtrip(t *testing.T) {
	s, err := New(t.TempDir())
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	ctx := context.Background()
	src := mustConv(t)

	if err := s.Save(ctx, src); err != nil {
		t.Fatalf("save: %v", err)
	}
	got, err := s.Load(ctx, src.ID)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if err := got.Validate(); err != nil {
		t.Fatalf("loaded conv invalid: %v", err)
	}
	if got.ID != src.ID || got.Title != src.Title || got.Head != src.Head {
		t.Fatalf("meta mismatch: got %+v want %+v", got, src)
	}
	if len(got.Nodes) != len(src.Nodes) {
		t.Fatalf("nodes = %d, want %d", len(got.Nodes), len(src.Nodes))
	}
	for id, want := range src.Nodes {
		gnode, ok := got.Find(id)
		if !ok {
			t.Fatalf("node %s missing", id)
		}
		if gnode.Role != want.Role || len(gnode.Content) != len(want.Content) {
			t.Fatalf("node %s mismatch: got %+v want %+v", id, gnode, want)
		}
		if len(want.Content) > 0 && gnode.Content[0].Text != want.Content[0].Text {
			t.Fatalf("node %s text = %q, want %q", id, gnode.Content[0].Text, want.Content[0].Text)
		}
	}
	// 首次保存不应产生 .bak（无旧文件可留）。
	if _, err := os.Stat(filepath.Join(s.dir, "conv-1.json.bak")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("unexpected .bak after first save: %v", err)
	}
}

// TestRoundtripThinkingPart D42：思考分片落盘回环——kind/text 原样保留且整树校验通过。
func TestRoundtripThinkingPart(t *testing.T) {
	s, err := New(t.TempDir())
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	ctx := context.Background()
	c := mustConv(t)
	if _, err := c.Append(conversation.RoleAssistant, []conversation.Part{
		{Kind: conversation.PartThinking, Text: "先想一想"},
		{Kind: conversation.PartText, Text: "答案"},
	}); err != nil {
		t.Fatalf("append assistant: %v", err)
	}
	if err := s.Save(ctx, c); err != nil {
		t.Fatalf("save: %v", err)
	}
	got, err := s.Load(ctx, c.ID)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if err := got.Validate(); err != nil {
		t.Fatalf("loaded conv invalid: %v", err)
	}
	node, ok := got.Find(got.Head)
	if !ok {
		t.Fatal("head missing")
	}
	if len(node.Content) != 2 ||
		node.Content[0].Kind != conversation.PartThinking || node.Content[0].Text != "先想一想" ||
		node.Content[1].Kind != conversation.PartText || node.Content[1].Text != "答案" {
		t.Fatalf("content = %+v, want [thinking 先想一想, text 答案]", node.Content)
	}
}

func TestSaveKeepsOneGenerationBak(t *testing.T) {
	s, err := New(t.TempDir())
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	ctx := context.Background()
	c := mustConv(t)

	if err := s.Save(ctx, c); err != nil {
		t.Fatalf("save#1: %v", err)
	}
	// 第一次保存后追加一条消息（标题也改），再保存：旧代应落进 .bak。
	if _, err := c.Append(conversation.RoleUser, []conversation.Part{{Kind: conversation.PartText, Text: "第二句"}}); err != nil {
		t.Fatalf("append: %v", err)
	}
	c.Title = "改过标题"
	if err := s.Save(ctx, c); err != nil {
		t.Fatalf("save#2: %v", err)
	}

	bakData, err := os.ReadFile(filepath.Join(s.dir, "conv-1.json.bak"))
	if err != nil {
		t.Fatalf("read .bak: %v", err)
	}
	var bak conversation.Conversation
	if err := json.Unmarshal(bakData, &bak); err != nil {
		t.Fatalf("parse .bak: %v", err)
	}
	if bak.Title != "测试会话" {
		t.Fatalf(".bak title = %q, want 上一代标题", bak.Title)
	}
	if len(bak.Nodes) != 3 {
		t.Fatalf(".bak nodes = %d, want 3（上一代：root+u+a）", len(bak.Nodes))
	}

	cur, err := s.Load(ctx, c.ID)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(cur.Nodes) != 4 || cur.Title != "改过标题" {
		t.Fatalf("current = %d nodes title %q, want 4 nodes 改过标题", len(cur.Nodes), cur.Title)
	}
}

func TestLoadFallsBackToBakWhenMainCorrupt(t *testing.T) {
	s, err := New(t.TempDir())
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	ctx := context.Background()
	c := mustConv(t)
	if err := s.Save(ctx, c); err != nil {
		t.Fatalf("save#1: %v", err)
	}
	if _, err := c.Append(conversation.RoleUser, []conversation.Part{{Kind: conversation.PartText, Text: "追加"}}); err != nil {
		t.Fatalf("append: %v", err)
	}
	if err := s.Save(ctx, c); err != nil {
		t.Fatalf("save#2: %v", err)
	}
	// 损坏主文件。
	if err := os.WriteFile(s.path(c.ID), []byte("{corrupt!!"), 0o644); err != nil {
		t.Fatalf("corrupt: %v", err)
	}
	got, err := s.Load(ctx, c.ID)
	if err != nil {
		t.Fatalf("load should fall back to .bak: %v", err)
	}
	if len(got.Nodes) != 3 {
		t.Fatalf("fallback nodes = %d, want 3（.bak 一代：root+u+a）", len(got.Nodes))
	}
}

func TestLoadMissingReturnsNotFound(t *testing.T) {
	s, err := New(t.TempDir())
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	if _, err := s.Load(context.Background(), conversation.ID("nope")); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestListSortedAndSkipsBak(t *testing.T) {
	s, err := New(t.TempDir())
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	ctx := context.Background()

	a := conversation.New(conversation.ID("conv-a"), "A")
	if _, err := a.Append(conversation.RoleUser, []conversation.Part{{Kind: conversation.PartText, Text: "a"}}); err != nil {
		t.Fatalf("append: %v", err)
	}
	if err := s.Save(ctx, a); err != nil {
		t.Fatalf("save a: %v", err)
	}
	b := conversation.New(conversation.ID("conv-b"), "B")
	if err := s.Save(ctx, b); err != nil {
		t.Fatalf("save b: %v", err)
	}
	// 让 b 更新（UpdatedAt 更晚）。
	if _, err := b.Append(conversation.RoleUser, []conversation.Part{{Kind: conversation.PartText, Text: "b"}}); err != nil {
		t.Fatalf("append b: %v", err)
	}
	if err := s.Save(ctx, b); err != nil {
		t.Fatalf("save b#2: %v", err)
	}
	// 一个损坏文件不应让列表瘫痪。
	if err := os.WriteFile(filepath.Join(s.dir, "conv-x.json"), []byte("garbage"), 0o644); err != nil {
		t.Fatalf("write garbage: %v", err)
	}

	list, err := s.List(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("list len = %d, want 2（损坏文件与 .bak 被跳过）", len(list))
	}
	if list[0].ID != "conv-b" || list[1].ID != "conv-a" {
		t.Fatalf("order = %s, %s; want conv-b 在前（最新）", list[0].ID, list[1].ID)
	}
	if list[0].Title != "B" || list[0].MessageN != 1 {
		t.Fatalf("summary b = %+v", list[0])
	}
	if list[1].MessageN != 1 {
		t.Fatalf("summary a messageN = %d, want 1", list[1].MessageN)
	}
}

func TestRemoveDeletesFileAndBak(t *testing.T) {
	s, err := New(t.TempDir())
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	ctx := context.Background()
	c := mustConv(t)
	if err := s.Save(ctx, c); err != nil {
		t.Fatalf("save#1: %v", err)
	}
	if err := s.Save(ctx, c); err != nil {
		t.Fatalf("save#2 (产生 .bak): %v", err)
	}

	if err := s.Remove(ctx, c.ID); err != nil {
		t.Fatalf("remove: %v", err)
	}
	for _, p := range []string{s.path(c.ID), s.path(c.ID) + ".bak"} {
		if _, err := os.Stat(p); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("%s should be gone: %v", p, err)
		}
	}
	if err := s.Remove(ctx, c.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("second remove err = %v, want ErrNotFound", err)
	}
	if _, err := s.Load(ctx, c.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("load after remove err = %v, want ErrNotFound", err)
	}
}

func TestSaveRejectsInvalidTree(t *testing.T) {
	s, err := New(t.TempDir())
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	c := mustConv(t)
	// 手工把树改坏：Head 指向不存在的节点。
	c.Head = conversation.MessageID("GHOST")
	if err := s.Save(context.Background(), c); err == nil {
		t.Fatal("save invalid tree should fail")
	}
	if _, err := os.Stat(s.path(c.ID)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("invalid tree must not be written: %v", err)
	}
}

// legacyConversationJSON 造 M0 虚拟 Root 旧格式文件内容。
func legacyConversationJSON(id string) string {
	return fmt.Sprintf(`{
  "id": %q,
  "title": "旧会话",
  "nodes": {"U1": {"id": "U1", "parent": "", "role": "user", "content": [{"kind": "text", "text": "hi"}], "created_at": "2026-01-01T00:00:00Z"}},
  "children": {"": ["U1"]},
  "head": "",
  "revised_from": {},
  "created_at": "2026-01-01T00:00:00Z",
  "updated_at": "2026-01-01T00:00:00Z"
}`, id)
}

// TestLoadRejectsLegacyVirtualRootFormat D19 破坏性切换：M0 虚拟 Root 格式必须明确报因。
func TestLoadRejectsLegacyVirtualRootFormat(t *testing.T) {
	s, err := New(t.TempDir())
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	if err := os.WriteFile(filepath.Join(s.dir, "conv-old.json"), []byte(legacyConversationJSON("conv-old")), 0o644); err != nil {
		t.Fatalf("write legacy: %v", err)
	}
	_, err = s.Load(context.Background(), conversation.ID("conv-old"))
	if err == nil || !errors.Is(err, ErrLegacyFormat) || !strings.Contains(err.Error(), "旧格式") {
		t.Fatalf("err = %v, want ErrLegacyFormat 报因", err)
	}
}

// TestListReportsLegacyFormat D19：List 不得静默跳过旧格式——聚合列出 ID 报因，
// 否则升级后旧数据隐形、用户无从得知要删哪个文件。
func TestListReportsLegacyFormat(t *testing.T) {
	s, err := New(t.TempDir())
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	if err := os.WriteFile(filepath.Join(s.dir, "conv-old.json"), []byte(legacyConversationJSON("conv-old")), 0o644); err != nil {
		t.Fatalf("write legacy: %v", err)
	}
	// 另有一个可用会话：报因必须点名旧格式 ID，而不是笼统失败。
	if err := s.Save(context.Background(), mustConv(t)); err != nil {
		t.Fatalf("save: %v", err)
	}
	_, err = s.List(context.Background())
	if !errors.Is(err, ErrLegacyFormat) || !strings.Contains(err.Error(), "conv-old") {
		t.Fatalf("list err = %v, want 聚合点名 conv-old", err)
	}
}

// legacyToolNodeJSON 造 D95 前旧格式（独立 tool 节点 + assistant.tool_calls）文件内容。
func legacyToolNodeJSON(id string) string {
	return fmt.Sprintf(`{
  "id": %q,
  "title": "工具旧会话",
  "nodes": {
    %q: {"id": %q, "parent": "", "role": "root", "created_at": "2026-01-01T00:00:00Z"},
    "U1": {"id": "U1", "parent": %q, "role": "user", "content": [{"kind": "text", "text": "hi"}], "created_at": "2026-01-01T00:00:01Z"},
    "A1": {"id": "A1", "parent": "U1", "role": "assistant", "content": [{"kind": "text", "text": "调用"}], "tool_calls": [{"id": "c1", "name": "echo", "args": {"m": "x"}}], "created_at": "2026-01-01T00:00:02Z"},
    "T1": {"id": "T1", "parent": "A1", "role": "tool", "tool_result": {"call_id": "c1", "ok": true, "output": "pong"}, "created_at": "2026-01-01T00:00:03Z"}
  },
  "children": {"": [%q], "U1": ["A1"], "A1": ["T1"]},
  "head": "T1",
  "revised_from": {},
  "created_at": "2026-01-01T00:00:00Z",
  "updated_at": "2026-01-01T00:00:03Z"
}`, id, id, id, id, id)
}

// TestLoadRejectsLegacyToolNodeFormat D95 破坏切换：独立 tool 节点格式必须明确报因，
// 不自动迁移（D95⑥：迁移正确性风险与双格式共存成本不值）。
func TestLoadRejectsLegacyToolNodeFormat(t *testing.T) {
	s, err := New(t.TempDir())
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	if err := os.WriteFile(filepath.Join(s.dir, "conv-t.json"), []byte(legacyToolNodeJSON("conv-t")), 0o644); err != nil {
		t.Fatalf("write legacy: %v", err)
	}
	_, err = s.Load(context.Background(), conversation.ID("conv-t"))
	if err == nil || !errors.Is(err, ErrLegacyFormat) || !strings.Contains(err.Error(), "tool") {
		t.Fatalf("err = %v, want ErrLegacyFormat 报因", err)
	}
}

// TestListReportsLegacyToolNodeFormat D95：List 同样聚合点名，不让旧数据隐形。
func TestListReportsLegacyToolNodeFormat(t *testing.T) {
	s, err := New(t.TempDir())
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	if err := os.WriteFile(filepath.Join(s.dir, "conv-t.json"), []byte(legacyToolNodeJSON("conv-t")), 0o644); err != nil {
		t.Fatalf("write legacy: %v", err)
	}
	_, err = s.List(context.Background())
	if !errors.Is(err, ErrLegacyFormat) || !strings.Contains(err.Error(), "conv-t") {
		t.Fatalf("list err = %v, want 聚合点名 conv-t", err)
	}
}

// TestLoadRejectsOrphanToolCallsField 防静默丢失：assistant 带 tool_calls 却无独立
// tool 节点的旧文件，普通反序列化会"看起来正常"地丢掉调用声明——影子解码必须报因。
func TestLoadRejectsOrphanToolCallsField(t *testing.T) {
	s, err := New(t.TempDir())
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	body := `{
  "id": "conv-o",
  "title": "残留声明",
  "nodes": {
    "conv-o": {"id": "conv-o", "parent": "", "role": "root", "created_at": "2026-01-01T00:00:00Z"},
    "U1": {"id": "U1", "parent": "conv-o", "role": "user", "content": [{"kind": "text", "text": "hi"}], "created_at": "2026-01-01T00:00:01Z"},
    "A1": {"id": "A1", "parent": "U1", "role": "assistant", "tool_calls": [{"id": "c1", "name": "echo"}], "created_at": "2026-01-01T00:00:02Z"}
  },
  "children": {"": ["conv-o"], "conv-o": ["U1"], "U1": ["A1"]},
  "head": "A1",
  "revised_from": {},
  "created_at": "2026-01-01T00:00:00Z",
  "updated_at": "2026-01-01T00:00:02Z"
}`
	if err := os.WriteFile(filepath.Join(s.dir, "conv-o.json"), []byte(body), 0o644); err != nil {
		t.Fatalf("write legacy: %v", err)
	}
	_, err = s.Load(context.Background(), conversation.ID("conv-o"))
	if !errors.Is(err, ErrLegacyFormat) {
		t.Fatalf("err = %v, want ErrLegacyFormat（防 tool_calls 被静默丢弃）", err)
	}
}

// TestRoundtripToolPart 新格式回环：工具分片（调用+结果同片）经 Save/Load 字节级保真。
func TestRoundtripToolPart(t *testing.T) {
	s, err := New(t.TempDir())
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	ctx := context.Background()
	src := mustConv(t)
	parent := src.Head
	if err := src.AppendCommitted(conversation.Message{
		ID: "A2", Parent: parent, Role: conversation.RoleAssistant,
		Content: []conversation.Part{
			{Kind: conversation.PartText, Text: "调用"},
			{Kind: conversation.PartTool, Tool: &conversation.ToolPart{
				CallID: "c1", Name: "echo", Args: json.RawMessage(`{"m":"x"}`),
				Result: &tool.Result{CallID: "c1", OK: true, Output: "pong"},
			}},
			{Kind: conversation.PartTool, Tool: &conversation.ToolPart{
				CallID: "c2", Name: "ghost", Args: json.RawMessage(`{}`),
			}},
		},
		Outcome: conversation.OutcomeDone,
	}); err != nil {
		t.Fatalf("commit tool part node: %v", err)
	}

	if err := s.Save(ctx, src); err != nil {
		t.Fatalf("save: %v", err)
	}
	got, err := s.Load(ctx, src.ID)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	want, _ := src.Find("A2")
	gnode, ok := got.Find("A2")
	if !ok {
		t.Fatal("node A2 missing")
	}
	if len(gnode.Content) != len(want.Content) {
		t.Fatalf("content = %d, want %d", len(gnode.Content), len(want.Content))
	}
	for i := range want.Content {
		got, wantP := gnode.Content[i].Tool, want.Content[i].Tool
		if (got == nil) != (wantP == nil) {
			t.Fatalf("part %d tool presence mismatch", i)
		}
		if got == nil {
			continue
		}
		// MarshalIndent 会重排 RawMessage 空白：参数按 JSON 语义比较，其余字段直比。
		if got.CallID != wantP.CallID || got.Name != wantP.Name ||
			got.Approval != wantP.Approval || !reflect.DeepEqual(got.Result, wantP.Result) {
			t.Fatalf("part %d tool = %+v, want %+v", i, *got, *wantP)
		}
		if !sameJSON(got.Args, wantP.Args) {
			t.Fatalf("part %d args = %s, want %s", i, got.Args, wantP.Args)
		}
	}
	if err := got.Validate(); err != nil {
		t.Fatalf("validate: %v", err)
	}
}

// sameJSON 两段 JSON 字节语义等价（忽略空白）。
func sameJSON(a, b []byte) bool {
	var x, y any
	if json.Unmarshal(a, &x) != nil || json.Unmarshal(b, &y) != nil {
		return false
	}
	return reflect.DeepEqual(x, y)
}

func TestCanceledContextRejected(t *testing.T) {
	s, err := New(t.TempDir())
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := s.Save(ctx, mustConv(t)); !errors.Is(err, context.Canceled) {
		t.Fatalf("save err = %v, want context.Canceled", err)
	}
	if _, err := s.List(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("list err = %v, want context.Canceled", err)
	}
}
