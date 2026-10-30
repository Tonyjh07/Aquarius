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
	src.Character = "aria"
	src.Tail = "After history, stay Aria."

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
	if got.Character != "aria" || got.Tail != "After history, stay Aria." {
		t.Fatalf("metadata mismatch: character=%q tail=%q", got.Character, got.Tail)
	}
	if got.NodeCount() != src.NodeCount() {
		t.Fatalf("nodes = %d, want %d", got.NodeCount(), src.NodeCount())
	}
	for _, want := range src.Snapshot().Nodes {
		gnode, ok := got.Find(want.ID)
		if !ok {
			t.Fatalf("node %s missing", want.ID)
		}
		if gnode.Role != want.Role || len(gnode.Content) != len(want.Content) {
			t.Fatalf("node %s mismatch: got %+v want %+v", want.ID, gnode, want)
		}
		if len(want.Content) > 0 && gnode.Content[0].Text != want.Content[0].Text {
			t.Fatalf("node %s text = %q, want %q", want.ID, gnode.Content[0].Text, want.Content[0].Text)
		}
	}
	// 首次保存不应产生 .bak（无旧文件可留）。
	if _, err := os.Stat(filepath.Join(s.dir, "conv-1.json.bak")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("unexpected .bak after first save: %v", err)
	}
}

// TestRoundtripThinkingPart D42/D175：思考分片落盘回环——kind/text/signature 原样保留且整树校验通过。
func TestRoundtripThinkingPart(t *testing.T) {
	s, err := New(t.TempDir())
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	ctx := context.Background()
	c := mustConv(t)
	if _, err := c.Append(conversation.RoleAssistant, []conversation.Part{
		{Kind: conversation.PartThinking, Text: "先想一想", Signature: "anthropic:sig-1"},
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
		node.Content[0].Signature != "anthropic:sig-1" ||
		node.Content[1].Kind != conversation.PartText || node.Content[1].Text != "答案" {
		t.Fatalf("content = %+v, want [thinking 先想一想, text 答案]（含签名）", node.Content)
	}
}

// TestRoundtripInjectTrust 注入节点的信任位随树落盘（D170）：装配在重载后仍要区分
// 宿主事实与外部内容，只按 Source 前缀猜是 fail-open。
func TestRoundtripInjectTrust(t *testing.T) {
	s, err := New(t.TempDir())
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	ctx := context.Background()
	c := mustConv(t)
	if err := c.AppendCommitted(conversation.Message{
		ID: "I1", Parent: c.Head, Role: conversation.RoleInject,
		Content: []conversation.Part{{Kind: conversation.PartText, Text: "任务完成"}},
		Source:  "job:j1", Trust: conversation.TrustHost,
	}); err != nil {
		t.Fatalf("append inject: %v", err)
	}
	if err := s.Save(ctx, c); err != nil {
		t.Fatalf("save: %v", err)
	}
	got, err := s.Load(ctx, c.ID)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	node, ok := got.Find("I1")
	if !ok {
		t.Fatal("node I1 missing")
	}
	if node.Trust != conversation.TrustHost || node.Source != "job:j1" {
		t.Fatalf("trust/source = %q/%q, want host/job:j1", node.Trust, node.Source)
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
	bak, err := decode(bakData, conversation.ID("conv-1"))
	if err != nil {
		t.Fatalf("parse .bak: %v", err)
	}
	if bak.Title != "测试会话" {
		t.Fatalf(".bak title = %q, want 上一代标题", bak.Title)
	}
	if bak.NodeCount() != 3 {
		t.Fatalf(".bak nodes = %d, want 3（上一代：root+u+a）", bak.NodeCount())
	}

	cur, err := s.Load(ctx, c.ID)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cur.NodeCount() != 4 || cur.Title != "改过标题" {
		t.Fatalf("current = %d nodes title %q, want 4 nodes 改过标题", cur.NodeCount(), cur.Title)
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
	if got.NodeCount() != 3 {
		t.Fatalf("fallback nodes = %d, want 3（.bak 一代：root+u+a）", got.NodeCount())
	}
}

// bakTitle 读某个备份/主文件并取标题（测试用）；读不出或解不开即失败。
func bakTitle(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	c, err := decode(data, conversation.ID("conv-1"))
	if err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	return c.Title
}

// TestBackupRotationKeepsGenerations 备份轮转保留 N 代（D254）：keep=3 时第 1 代沿用
// 历史名 `.bak`，更旧为 `.bak.1` / `.bak.2`，最老的出局；主文件始终是最新的一代。
func TestBackupRotationKeepsGenerations(t *testing.T) {
	s, err := NewWithKeep(t.TempDir(), 3)
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	ctx := context.Background()
	c := mustConv(t)
	dst := s.path(c.ID)
	for _, title := range []string{"t1", "t2", "t3", "t4"} {
		c.Title = title
		if err := s.Save(ctx, c); err != nil {
			t.Fatalf("save %s: %v", title, err)
		}
	}
	if got := bakTitle(t, dst); got != "t4" {
		t.Fatalf("main = %q, want t4", got)
	}
	for suffix, want := range map[string]string{".bak": "t3", ".bak.1": "t2", ".bak.2": "t1"} {
		if got := bakTitle(t, dst+suffix); got != want {
			t.Errorf("%s = %q, want %q", suffix, got, want)
		}
	}
	if _, err := os.Stat(dst + ".bak.3"); err == nil {
		t.Fatal("第 4 代不该存在（keep=3）")
	}
}

// TestBackupRotationDefaultKeepsOne 默认（New）只保留一代 `.bak`，不产生 `.bak.1`（D254）。
func TestBackupRotationDefaultKeepsOne(t *testing.T) {
	s, err := New(t.TempDir())
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	ctx := context.Background()
	c := mustConv(t)
	dst := s.path(c.ID)
	for _, title := range []string{"t1", "t2", "t3"} {
		c.Title = title
		if err := s.Save(ctx, c); err != nil {
			t.Fatalf("save %s: %v", title, err)
		}
	}
	if got := bakTitle(t, dst+".bak"); got != "t2" {
		t.Fatalf(".bak = %q, want t2（上一代）", got)
	}
	if _, err := os.Stat(dst + ".bak.1"); err == nil {
		t.Fatal("默认只该留一代，不该有 .bak.1")
	}
}

// TestBackupRotationSkipsCorruptMain 保存前完整性校验（D254）：现文件坏掉时不轮转，
// 坏文件不挤掉更旧的可用备份。
func TestBackupRotationSkipsCorruptMain(t *testing.T) {
	s, err := NewWithKeep(t.TempDir(), 3)
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	ctx := context.Background()
	c := mustConv(t)
	dst := s.path(c.ID)
	c.Title = "good-1"
	if err := s.Save(ctx, c); err != nil {
		t.Fatalf("save good-1: %v", err)
	}
	c.Title = "good-2"
	if err := s.Save(ctx, c); err != nil {
		t.Fatalf("save good-2: %v", err)
	}
	// 主文件坏掉后再保存：跳过轮转，`.bak` 仍是 good-1。
	if err := os.WriteFile(dst, []byte("{corrupt!!"), 0o644); err != nil {
		t.Fatalf("corrupt: %v", err)
	}
	c.Title = "good-3"
	if err := s.Save(ctx, c); err != nil {
		t.Fatalf("save good-3: %v", err)
	}
	if got := bakTitle(t, dst+".bak"); got != "good-1" {
		t.Fatalf(".bak = %q, want good-1（坏文件不该被轮成备份）", got)
	}
	if _, err := os.Stat(dst + ".bak.1"); err == nil {
		t.Fatal("坏文件不该推进轮转，.bak.1 不该出现")
	}
	if got := bakTitle(t, dst); got != "good-3" {
		t.Fatalf("main = %q, want good-3", got)
	}
}

// TestLoadFallsBackAcrossGenerations 主文件与第 1 代都坏时，回退到更旧的一代（D254）。
func TestLoadFallsBackAcrossGenerations(t *testing.T) {
	s, err := NewWithKeep(t.TempDir(), 3)
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	ctx := context.Background()
	c := mustConv(t)
	dst := s.path(c.ID)
	for _, title := range []string{"g1", "g2", "g3"} {
		c.Title = title
		if err := s.Save(ctx, c); err != nil {
			t.Fatalf("save %s: %v", title, err)
		}
	}
	// 主文件(g3)与 .bak(g2)都坏；应回退 .bak.1(g1)。
	if err := os.WriteFile(dst, []byte("{corrupt"), 0o644); err != nil {
		t.Fatalf("corrupt main: %v", err)
	}
	if err := os.WriteFile(dst+".bak", []byte("{corrupt"), 0o644); err != nil {
		t.Fatalf("corrupt bak: %v", err)
	}
	got, err := s.Load(ctx, c.ID)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if got.Title != "g1" {
		t.Fatalf("fallback title = %q, want g1（跳过坏的两代）", got.Title)
	}
}

// TestRemoveClearsAllBackupGenerations Remove 连各代备份与遗留高代一起清（D254）：
// keep 调小后目录里可能还留着超出当前 keep 的更旧代。
func TestRemoveClearsAllBackupGenerations(t *testing.T) {
	dir := t.TempDir()
	s, err := NewWithKeep(dir, 3)
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	ctx := context.Background()
	c := mustConv(t)
	for _, title := range []string{"g1", "g2", "g3", "g4"} {
		c.Title = title
		if err := s.Save(ctx, c); err != nil {
			t.Fatalf("save %s: %v", title, err)
		}
	}
	// 模拟 keep 调小前的遗留：多写一个超出当前 keep 的更高代。
	if err := os.WriteFile(s.path(c.ID)+".bak.9", []byte("{}"), 0o644); err != nil {
		t.Fatalf("write legacy bak: %v", err)
	}
	if err := s.Remove(ctx, c.ID); err != nil {
		t.Fatalf("remove: %v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("readdir: %v", err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "conv-1.json") {
			t.Fatalf("残留文件: %s", e.Name())
		}
	}
}

// TestNewWithKeepNormalizesNonPositive keep < 1 归默认值，不因 0 变成不轮转（D254）。
func TestNewWithKeepNormalizesNonPositive(t *testing.T) {
	for _, keep := range []int{0, -5} {
		s, err := NewWithKeep(t.TempDir(), keep)
		if err != nil {
			t.Fatalf("new: %v", err)
		}
		if s.keep != DefaultKeep {
			t.Fatalf("keep=%d 归一为 %d, want %d", keep, s.keep, DefaultKeep)
		}
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
	a.Character = "alice" // §5.17/D243：角色 slug 随会话元数据落盘，列表带出
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
	if list[1].Character != "alice" {
		t.Fatalf("summary a character = %q, want alice（列表带出角色 slug）", list[1].Character)
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

// TestLoadOldFileWithoutMetadataKeys 缺键即空（D243）：没有 character/tail 键的旧文件
// 照常加载，两个元数据字段取零值——升级不要求迁移。
func TestLoadOldFileWithoutMetadataKeys(t *testing.T) {
	s, err := New(t.TempDir())
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	old := `{
  "id": "conv-old",
  "title": "旧会话",
  "head": "conv-old",
  "nodes": {"conv-old": {"id": "conv-old", "parent": "", "role": "root", "created_at": "2026-01-01T00:00:00Z"}},
  "children": {"": ["conv-old"]},
  "revised_from": {},
  "created_at": "2026-01-01T00:00:00Z",
  "updated_at": "2026-01-01T00:00:00Z"
}`
	if err := os.WriteFile(filepath.Join(s.dir, "conv-old.json"), []byte(old), 0o644); err != nil {
		t.Fatalf("write old: %v", err)
	}
	got, err := s.Load(context.Background(), conversation.ID("conv-old"))
	if err != nil {
		t.Fatalf("load old file: %v", err)
	}
	if got.Character != "" || got.Tail != "" {
		t.Fatalf("missing keys must read as empty: character=%q tail=%q", got.Character, got.Tail)
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
	if err == nil || !errors.Is(err, ErrLegacyFormat) || !strings.Contains(err.Error(), "旧版工具节点") {
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
