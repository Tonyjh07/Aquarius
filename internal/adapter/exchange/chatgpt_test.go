package exchange

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/Tonyjh07/Aquarius/internal/domain/conversation"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

// TestExportChatGPTShape 导出的形状要能被认这套的工具直接读：顶层数组、一项会话、
// `mapping` 里每个节点带 parent/children、顶层 current_node 是我们的 Head。
func TestExportChatGPTShape(t *testing.T) {
	c := conversation.New(conversation.ID("conv-g"), "树形互通")
	if _, err := c.Append(conversation.RoleUser, []conversation.Part{{Kind: conversation.PartText, Text: "第一句"}}); err != nil {
		t.Fatalf("append: %v", err)
	}
	if _, err := c.Append(conversation.RoleAssistant, []conversation.Part{{Kind: conversation.PartText, Text: "回答"}}); err != nil {
		t.Fatalf("append: %v", err)
	}
	var buf bytes.Buffer
	rep, err := New(Options{}).Export(context.Background(), c, "chatgpt", &buf)
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	if rep.Bytes != int64(buf.Len()) || rep.Nodes != 2 {
		t.Fatalf("回执 = %+v（写出 %d 字节）", rep, buf.Len())
	}

	var env []map[string]any
	if err := json.Unmarshal(buf.Bytes(), &env); err != nil {
		t.Fatalf("不是合法 JSON 数组: %v", err)
	}
	if len(env) != 1 {
		t.Fatalf("顶层数组有 %d 项，want 1", len(env))
	}
	mapping, ok := env[0]["mapping"].(map[string]any)
	if !ok {
		t.Fatalf("缺 mapping: %v", env[0])
	}
	if len(mapping) != 3 { // Root + 两条消息
		t.Fatalf("mapping 有 %d 个节点，want 3（含空根）", len(mapping))
	}
	if env[0]["current_node"] != string(c.Head) {
		t.Fatalf("current_node = %v, want %s", env[0]["current_node"], c.Head)
	}
	root := mapping[string(c.ID)].(map[string]any)
	if root["message"] != nil {
		t.Fatalf("根节点应是空 message（与 ChatGPT 导出同形）: %v", root["message"])
	}
	if kids, ok := root["children"].([]any); !ok || len(kids) != 1 {
		t.Fatalf("根的孩子 = %v, want 1 个", root["children"])
	}
}

// TestChatGPTImportTree 读回来：结构（含旁支）、角色、正文都对；空根与只带图片的消息不建空节点。
func TestChatGPTImportTree(t *testing.T) {
	// 一份手工写的最小导出：root 空 → s(user) → a1(assistant) 与 a2(assistant，旁支)。
	const doc = `[{
	  "title": "带旁支的会话",
	  "create_time": 1700000000.5,
	  "current_node": "a1",
	  "mapping": {
	    "root": {"id": "root", "message": null, "parent": null, "children": ["s"]},
	    "s": {"id": "s", "message": {"author": {"role": "user"}, "content": {"content_type": "text", "parts": ["你好"]}, "create_time": 1700000001}, "parent": "root", "children": ["a1", "a2"]},
	    "a1": {"id": "a1", "message": {"author": {"role": "assistant"}, "content": {"content_type": "text", "parts": ["回答一"]}, "create_time": 1700000002}, "parent": "s", "children": []},
	    "a2": {"id": "a2", "message": {"author": {"role": "assistant"}, "content": {"content_type": "text", "parts": ["回答二"]}, "create_time": 1700000003}, "parent": "s", "children": []}
	  }
	}]`
	c, rep, err := New(Options{}).Import(context.Background(), strings.NewReader(doc),
		port.ExchangeFormat{Name: "chatgpt"})
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if c.Title != "带旁支的会话" {
		t.Fatalf("标题 = %q", c.Title)
	}
	if c.NodeCount() != 4 { // Root + 3 条消息（空根不建节点、persona 不凭空造）
		t.Fatalf("节点数 = %d, want 4", c.NodeCount())
	}
	if rep.Nodes != 3 {
		t.Fatalf("回执节点数 = %d, want 3", rep.Nodes)
	}
	path := c.Path()
	if len(path) != 3 || path[1].Role != conversation.RoleUser || path[2].Role != conversation.RoleAssistant {
		t.Fatalf("Head 链 = %+v, want root→user→assistant（current_node = a1）", path)
	}
	if got := path[2].Content[0].Text; got != "回答一" {
		t.Fatalf("Head 落在 %q，want 回答一（current_node 指定）", got)
	}
	// 旁支仍在：user 有两个孩子。
	snap := c.Snapshot()
	kids := 0
	for _, e := range snap.Edges {
		if len(e.Childs) == 2 {
			kids = 2
		}
	}
	if kids != 2 {
		t.Fatalf("分叉丢了：边 = %+v", snap.Edges)
	}
	// ID 全部重分配：源里的 "s"/"a1" 不该出现在新树里。
	for _, m := range snap.Nodes {
		if m.ID == "s" || m.ID == "a1" || m.ID == "root" {
			t.Fatalf("源 ID %q 被带进了新树", m.ID)
		}
	}
}

// TestChatGPTImportSkipsAndReparents 只带图片的消息与空壳节点被跳过，但它们的子节点要接上——
// 否则一份「中间夹着空的」导出会丢掉后半段。
func TestChatGPTImportSkipsAndReparents(t *testing.T) {
	const doc = `[{
	  "title": "夹空",
	  "mapping": {
	    "root": {"id": "root", "message": null, "children": ["s"]},
	    "s": {"id": "s", "message": {"author": {"role": "user"}, "content": {"content_type": "text", "parts": ["问"]}}, "parent": "root", "children": ["img"]},
	    "img": {"id": "img", "message": {"author": {"role": "user"}, "content": {"content_type": "multimodal_text", "parts": [{"content_type": "image_asset_pointer", "asset_pointer": "file-service://x"}]}}, "parent": "s", "children": ["a"]},
	    "a": {"id": "a", "message": {"author": {"role": "assistant"}, "content": {"content_type": "text", "parts": ["答"]}}, "parent": "img", "children": []}
	  }
	}]`
	c, rep, err := New(Options{}).Import(context.Background(), strings.NewReader(doc),
		port.ExchangeFormat{Name: "chatgpt"})
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if c.NodeCount() != 3 {
		t.Fatalf("节点数 = %d, want 3（Root + 问 + 答；图片消息不建空节点）", c.NodeCount())
	}
	var joined strings.Builder
	joined.WriteString(strings.Join(rep.Dropped, " "))
	if !strings.Contains(joined.String(), "图片") {
		t.Fatalf("回执应说明图片没带进来: %v", rep.Dropped)
	}
	// 回答接在「问」之下（中间那张图不在，链路不能断）。
	path := c.Path()
	if len(path) != 3 || path[2].Content[0].Text != "答" {
		t.Fatalf("链路 = %+v, want root→问→答", path)
	}
}

// TestChatGPTImportRoles 角色映射：tool → 注入节点（工具正文不是用户发言），system 照旧，
// 奇特角色不认并计入丢弃项。
func TestChatGPTImportRoles(t *testing.T) {
	const doc = `[{
	  "mapping": {
	    "root": {"id": "root", "message": null, "children": ["s"]},
	    "s": {"id": "s", "message": {"author": {"role": "system"}, "content": {"content_type": "text", "parts": ["你是一个助手"]}}, "parent": "root", "children": ["t"]},
	    "t": {"id": "t", "message": {"author": {"role": "tool"}, "content": {"content_type": "text", "parts": ["文件内容"]}}, "parent": "s", "children": ["c"]},
	    "c": {"id": "c", "message": {"author": {"role": "critic"}, "content": {"content_type": "text", "parts": ["不认的角色"]}}, "parent": "t", "children": []}
	  }
	}]`
	c, rep, err := New(Options{}).Import(context.Background(), strings.NewReader(doc),
		port.ExchangeFormat{Name: "chatgpt"})
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	var roles []conversation.Role
	for _, m := range c.Snapshot().Nodes {
		roles = append(roles, m.Role)
	}
	// Root + system + inject（tool 那条）；critic 被跳过。
	if len(roles) != 3 || roles[1] != conversation.RoleSystem || roles[2] != conversation.RoleInject {
		t.Fatalf("角色 = %v, want [root system inject]", roles)
	}
	if !strings.Contains(strings.Join(rep.Dropped, " "), "critic") {
		t.Fatalf("不认的角色应计入回执: %v", rep.Dropped)
	}
}

// TestChatGPTExportImportRoundtrip 导出再导入：节点数、角色序列、Head 链一致（结构无损）。
func TestChatGPTExportImportRoundtrip(t *testing.T) {
	c := conversation.New(conversation.ID("conv-r"), "往返")
	if _, err := c.Append(conversation.RoleUser, []conversation.Part{{Kind: conversation.PartText, Text: "问一"}}); err != nil {
		t.Fatalf("append: %v", err)
	}
	if _, err := c.Append(conversation.RoleAssistant, []conversation.Part{{Kind: conversation.PartText, Text: "答一"}}); err != nil {
		t.Fatalf("append: %v", err)
	}
	// 旁支：从第一条 user 分叉。
	u := c.Path()[1]
	if _, err := c.Revise(u.ID, []conversation.Part{{Kind: conversation.PartText, Text: "问二"}}, conversation.Fresh); err != nil {
		t.Fatalf("revise: %v", err)
	}
	var buf bytes.Buffer
	if _, err := New(Options{}).Export(context.Background(), c, "chatgpt", &buf); err != nil {
		t.Fatalf("export: %v", err)
	}
	got, _, err := New(Options{}).Import(context.Background(), bytes.NewReader(buf.Bytes()),
		port.ExchangeFormat{Name: "chatgpt"})
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if got.NodeCount() != c.NodeCount() {
		t.Fatalf("节点数 = %d, want %d", got.NodeCount(), c.NodeCount())
	}
	if len(got.Path()) != len(c.Path()) {
		t.Fatalf("Head 链长度 = %d, want %d", len(got.Path()), len(c.Path()))
	}
	// 兄弟顺序（D152）经 children 保留：分叉数应一致。
	branches := 0
	for _, e := range got.Snapshot().Edges {
		if len(e.Childs) > 1 {
			branches++
		}
	}
	if branches == 0 {
		t.Fatal("分叉在往返里丢了")
	}
}

// TestDetectChatGPT 文件头认 chatgpt；普通 JSON 与坏文件都不认。
func TestDetectChatGPT(t *testing.T) {
	ex := New(Options{})
	if f, ok := ex.Detect("conversations.json", []byte(`[{"title":"t","mapping":{},"current_node":"x"}]`)); !ok || f.Name != "chatgpt" {
		t.Fatalf("应认成 chatgpt: %+v %v", f, ok)
	}
	// 头部被截断（Detect 只读前 512 字节）：弱判断仍要认出来。
	if f, ok := ex.Detect("conv.json", []byte(`[{"title":"t","mapping":{"a":`)); !ok || f.Name != "chatgpt" {
		t.Fatalf("截断的头部也应认成 chatgpt: %+v %v", f, ok)
	}
	for _, head := range []string{`{"messages":[]}`, `[1,2,3]`, `not json`, ``} {
		if f, ok := ex.Detect("x.json", []byte(head)); ok {
			t.Fatalf("%q 不该被认成 %s", head, f.Name)
		}
	}
}

// TestChatGPTImportRejectsBadStructure 坏结构一律报因：父节点悬空、角色不认识、文件里没会话。
func TestChatGPTImportRejectsBadStructure(t *testing.T) {
	cases := map[string]string{
		"父节点不在文件里": `[{"mapping":{
			"a":{"id":"a","message":{"author":{"role":"user"},"content":{"content_type":"text","parts":["x"]}},"parent":"nope"}}}]`,
		"没有会话": `[]`,
	}
	for want, doc := range cases {
		_, _, err := New(Options{}).Import(context.Background(), strings.NewReader(doc),
			port.ExchangeFormat{Name: "chatgpt"})
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Fatalf("%s: err = %v", want, err)
		}
	}
}
