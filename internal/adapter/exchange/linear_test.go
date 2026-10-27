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

// linearConv 造一棵小会话：system(persona) → user → assistant（含思考与工具分片）。
//
// 用来验线性出口的三件事：角色映射、只带正文、丢掉的逐项进回执。
func linearConv(t *testing.T) *conversation.Conversation {
	t.Helper()
	c := conversation.New(conversation.ID("conv-lin"), "线性往返")
	if err := c.AppendCommitted(conversation.Message{
		ID: "S1", Parent: conversation.MessageID(c.ID), Role: conversation.RoleSystem,
		Content:   []conversation.Part{{Kind: conversation.PartText, Text: "你是一个助手"}},
		CreatedAt: c.CreatedAt,
	}); err != nil {
		t.Fatalf("append system: %v", err)
	}
	if _, err := c.Append(conversation.RoleUser, []conversation.Part{{Kind: conversation.PartText, Text: "你好"}}); err != nil {
		t.Fatalf("append user: %v", err)
	}
	if _, err := c.Append(conversation.RoleAssistant, []conversation.Part{
		{Kind: conversation.PartThinking, Text: "想一想"},
		{Kind: conversation.PartText, Text: "在的"},
	}); err != nil {
		t.Fatalf("append assistant: %v", err)
	}
	return c
}

// TestExportOpenAIShape 导出的形状：`{"messages":[...]}`，role 用对方的取值，
// content 是字符串而不是分片数组（兼容端读字符串的那一档最多）。
func TestExportOpenAIShape(t *testing.T) {
	var buf bytes.Buffer
	rep, err := New(Options{}).Export(context.Background(), linearConv(t), "openai", &buf)
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	var doc struct {
		Messages []struct {
			Role    string          `json:"role"`
			Content json.RawMessage `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(buf.Bytes(), &doc); err != nil {
		t.Fatalf("不是合法 JSON: %v", err)
	}
	if len(doc.Messages) != 3 {
		t.Fatalf("消息数 = %d, want 3（system + user + assistant）", len(doc.Messages))
	}
	want := []string{"system", "user", "assistant"}
	for i, m := range doc.Messages {
		if m.Role != want[i] {
			t.Fatalf("第 %d 条角色 = %q, want %q", i, m.Role, want[i])
		}
		var s string
		if err := json.Unmarshal(m.Content, &s); err != nil {
			t.Fatalf("第 %d 条 content 不是字符串: %s", i, m.Content)
		}
	}
	var text string
	if err := json.Unmarshal(doc.Messages[2].Content, &text); err != nil {
		t.Fatalf("解析 content: %v", err)
	}
	if text != "在的" {
		t.Fatalf("assistant 正文 = %q, want 在的（思考分片不进）", text)
	}
	if rep.Bytes != int64(buf.Len()) || rep.Nodes != 3 {
		t.Fatalf("回执 = %+v（写出 %d 字节）", rep, buf.Len())
	}
	if !strings.Contains(strings.Join(rep.Dropped, " "), "思考过程 1 个") {
		t.Fatalf("回执应说明思考分片没带出去: %v", rep.Dropped)
	}
}

// TestOpenAIRoundtrip 导出再导入：角色序列与正文一致，节点数不含 Root。
func TestOpenAIRoundtrip(t *testing.T) {
	src := linearConv(t)
	var buf bytes.Buffer
	if _, err := New(Options{}).Export(context.Background(), src, "openai", &buf); err != nil {
		t.Fatalf("export: %v", err)
	}
	got, rep, err := New(Options{}).Import(context.Background(), bytes.NewReader(buf.Bytes()),
		port.ExchangeFormat{Name: "openai"})
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if rep.Nodes != 3 || got.NodeCount() != 4 {
		t.Fatalf("节点数 = %d/%d, want 3 条消息 + Root = 4", rep.Nodes, got.NodeCount())
	}
	path := got.Path()[1:] // 跳过 Root
	wantRoles := []conversation.Role{conversation.RoleSystem, conversation.RoleUser, conversation.RoleAssistant}
	wantText := []string{"你是一个助手", "你好", "在的"}
	if len(path) != len(wantRoles) {
		t.Fatalf("Head 链长度 = %d, want %d", len(path), len(wantRoles))
	}
	for i, m := range path {
		if m.Role != wantRoles[i] {
			t.Fatalf("第 %d 条角色 = %q, want %q", i, m.Role, wantRoles[i])
		}
		if m.Content[0].Text != wantText[i] {
			t.Fatalf("第 %d 条正文 = %q, want %q", i, m.Content[0].Text, wantText[i])
		}
	}
}

// TestParseLinearContent 分片数组的 content：只取文本分片，图片与不认的类型进计数。
func TestParseLinearContent(t *testing.T) {
	counts := map[string]int{}
	parts := parseLinearContent(json.RawMessage(`[
		{"type":"text","text":"第一段"},
		{"type":"input_text","text":"第二段"},
		{"type":"image_url","image_url":{"url":"https://x/y.png"}},
		{"type":"audio_url"}
	]`), counts)
	if len(parts) != 2 || parts[0].Text != "第一段" || parts[1].Text != "第二段" {
		t.Fatalf("分片 = %+v, want 两段文本", parts)
	}
	if counts["图片（只有地址，字节取不到）"] != 1 {
		t.Fatalf("图片计数 = %v", counts)
	}
	if counts["正文分片 audio_url"] != 1 {
		t.Fatalf("不认的分片应计数: %v", counts)
	}
	// 空字符串与空数组都不产生分片（不建空节点）。
	if got := parseLinearContent(json.RawMessage(`""`), counts); got != nil {
		t.Fatalf("空字符串应无分片: %+v", got)
	}
	if got := parseLinearContent(json.RawMessage(`[]`), counts); got != nil {
		t.Fatalf("空数组应无分片: %+v", got)
	}
}

// TestLinearRoleMapping 角色双向映射：user/assistant 照搬，system 与 inject 都落成 system，
// 导入侧把 system 读回 system、tool/function 读成注入节点。
func TestLinearRoleMapping(t *testing.T) {
	out := map[conversation.Role]string{
		conversation.RoleUser:      "user",
		conversation.RoleAssistant: "assistant",
		conversation.RoleSystem:    "system",
		conversation.RoleInject:    "system",
		conversation.RoleRoot:      "system",
	}
	for in, want := range out {
		if got := linearRole(in); got != want {
			t.Errorf("linearRole(%q) = %q, want %q", in, got, want)
		}
	}
	in := map[string]conversation.Role{
		"user":      conversation.RoleUser,
		"assistant": conversation.RoleAssistant,
		"system":    conversation.RoleSystem,
		"developer": conversation.RoleSystem,
		"tool":      conversation.RoleInject,
		"function":  conversation.RoleInject,
	}
	for role, want := range in {
		got, ok := linearImportRole(role)
		if !ok || got != want {
			t.Errorf("linearImportRole(%q) = %q, %v, want %q", role, got, ok, want)
		}
	}
	if _, ok := linearImportRole("critic"); ok {
		t.Error("不认的角色不该被接住")
	}
}

// TestOpenAIImportInjects 导入侧把工具结果读成注入节点，不丢内容也不冒充用户。
func TestOpenAIImportInjects(t *testing.T) {
	const doc = `{"messages":[
		{"role":"user","content":"问"},
		{"role":"tool","content":"文件内容"},
		{"role":"critic","content":"不认的角色"},
		{"role":"assistant","content":"答"}
	]}`
	c, rep, err := New(Options{}).Import(context.Background(), strings.NewReader(doc),
		port.ExchangeFormat{Name: "openai"})
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	var roles []conversation.Role
	for _, m := range c.Snapshot().Nodes {
		roles = append(roles, m.Role)
	}
	if len(roles) != 4 || roles[2] != conversation.RoleInject {
		t.Fatalf("角色 = %v, want [root user inject assistant]", roles)
	}
	if !strings.Contains(strings.Join(rep.Dropped, " "), "critic") {
		t.Fatalf("不认的角色应计入回执: %v", rep.Dropped)
	}
}

// TestExportLinearDropsBranch 线性导出只带当前那条链：旁支上的消息、思考与工具分片都要进
// 回执，不静默消失。
func TestExportLinearDropsBranch(t *testing.T) {
	src := linearConv(t)
	// 先从 assistant 往后追加一个工具分片，再从 Head 分叉出一条旁支：Head 链上既有思考与工具，
	// 也有被留在旁支的节点。
	if err := src.AppendCommitted(conversation.Message{
		ID: "T1", Parent: src.Head, Role: conversation.RoleUser,
		Content:   []conversation.Part{{Kind: conversation.PartText, Text: "再问一句"}},
		CreatedAt: src.UpdatedAt,
	}); err != nil {
		t.Fatalf("append: %v", err)
	}
	if _, err := src.Revise("T1", []conversation.Part{{Kind: conversation.PartText, Text: "改问一句"}},
		conversation.Fresh); err != nil {
		t.Fatalf("revise: %v", err)
	}
	for _, format := range []string{"openai", "sillytavern"} {
		var buf bytes.Buffer
		rep, err := New(Options{}).Export(context.Background(), src, format, &buf)
		if err != nil {
			t.Fatalf("%s export: %v", format, err)
		}
		joined := strings.Join(rep.Dropped, " / ")
		if !strings.Contains(joined, "旁支上的") {
			t.Fatalf("%s 回执应说明旁支没带出去: %v", format, rep.Dropped)
		}
		if !strings.Contains(joined, "思考过程") {
			t.Fatalf("%s 回执应说明思考分片没带出去: %v", format, rep.Dropped)
		}
	}
}

// TestExportSillyTavernShape `.jsonl` 的每行一条：首行 header，其后每行一条 mes，
// 角色靠 is_user / is_system 表达（对方就是这么判的）。
func TestExportSillyTavernShape(t *testing.T) {
	var buf bytes.Buffer
	rep, err := New(Options{}).Export(context.Background(), linearConv(t), "sillytavern", &buf)
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	lines := splitLines(buf.Bytes())
	if len(lines) != 4 {
		t.Fatalf("行数 = %d, want 4（首行 header + 3 条消息）", len(lines))
	}
	var header map[string]any
	if err := json.Unmarshal(lines[0], &header); err != nil {
		t.Fatalf("首行不是 JSON: %v", err)
	}
	if _, ok := header["create_date"]; !ok {
		t.Fatalf("首行应带 create_date: %v", header)
	}
	var rows []map[string]any
	for _, line := range lines[1:] {
		var row map[string]any
		if err := json.Unmarshal(line, &row); err != nil {
			t.Fatalf("消息行不是 JSON: %v", err)
		}
		rows = append(rows, row)
	}
	if rows[0]["is_system"] != true || rows[1]["is_user"] != true {
		t.Fatalf("角色位不对: %v", rows)
	}
	if rows[2]["mes"] != "在的" {
		t.Fatalf("assistant 正文 = %v, want 在的", rows[2]["mes"])
	}
	if rep.Nodes != 3 || rep.Bytes != int64(buf.Len()) {
		t.Fatalf("回执 = %+v（写出 %d 字节）", rep, buf.Len())
	}
}

// TestSillyTavernRoundtrip 导出再导入：角色、正文、时间都回来（时间精确到秒）。
func TestSillyTavernRoundtrip(t *testing.T) {
	src := linearConv(t)
	var buf bytes.Buffer
	if _, err := New(Options{}).Export(context.Background(), src, "sillytavern", &buf); err != nil {
		t.Fatalf("export: %v", err)
	}
	got, rep, err := New(Options{}).Import(context.Background(), bytes.NewReader(buf.Bytes()),
		port.ExchangeFormat{Name: "sillytavern"})
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if rep.Nodes != 3 {
		t.Fatalf("回执节点数 = %d, want 3", rep.Nodes)
	}
	path := got.Path()[1:]
	if len(path) != 3 {
		t.Fatalf("Head 链长度 = %d, want 3", len(path))
	}
	if path[0].Role != conversation.RoleSystem || path[1].Role != conversation.RoleUser ||
		path[2].Role != conversation.RoleAssistant {
		t.Fatalf("角色 = %v", []conversation.Role{path[0].Role, path[1].Role, path[2].Role})
	}
	if path[1].Content[0].Text != "你好" {
		t.Fatalf("user 正文 = %q", path[1].Content[0].Text)
	}
	// 时间戳回得来（SillyTavern 精确到秒）：与源相差不超过 1 秒。
	if d := path[1].CreatedAt.Sub(src.Path()[2].CreatedAt); d > 1e9 || d < -1e9 {
		t.Fatalf("时间差 %v，want 1 秒内（格式只到秒）", d)
	}
	// 对方的配置不进会话：回执里说清楚。
	if !strings.Contains(strings.Join(rep.Dropped, " "), "世界书") {
		t.Fatalf("回执应说明对方的配置没带进来: %v", rep.Dropped)
	}
}

// TestImportSillyTavernBadFiles 坏文件报因：空文件、首行不是 header、中间某行不是 JSON。
func TestImportSillyTavernBadFiles(t *testing.T) {
	cases := map[string]struct {
		doc  string
		want string
	}{
		"空文件":        {"", "空"},
		"首行不是头":      {"{\"mes\":\"x\"}\n", ""},
		"中间行不是 JSON": {"{\"user_name\":\"u\"}\n{\"mes\":\"好\"}\n不是 JSON\n", "第 3 行"},
	}
	for name, tc := range cases {
		_, _, err := New(Options{}).Import(context.Background(), strings.NewReader(tc.doc),
			port.ExchangeFormat{Name: "sillytavern"})
		if tc.want == "" {
			// 首行带 mes 也能读（有些导出把 header 省了）：不当作错误，但要能解析。
			if err != nil {
				t.Fatalf("%s: err = %v，want 无错（没有 header 时按只有消息处理）", name, err)
			}
			continue
		}
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("%s: err = %v, want 含 %q", name, err, tc.want)
		}
	}
}

// TestDetectLinear 线性格式的识别：`.jsonl` 看首行字段，消息数组看 `messages` 键。
func TestDetectLinear(t *testing.T) {
	ex := New(Options{})
	if f, ok := ex.Detect("conv.jsonl", []byte("{\"user_name\":\"u\",\"character_name\":\"c\"}\n")); !ok || f.Name != "sillytavern" {
		t.Fatalf("带 header 的 .jsonl 应认成 sillytavern: %+v %v", f, ok)
	}
	if f, ok := ex.Detect("x.jsonl", []byte("{\"mes\":\"hi\",\"is_user\":true}\n")); !ok || f.Name != "sillytavern" {
		t.Fatalf("带 mes 的行也应认成 sillytavern: %+v %v", f, ok)
	}
	if f, ok := ex.Detect("chat.json", []byte(`{"messages":[{"role":"user","content":"x"}]}`)); !ok || f.Name != "openai" {
		t.Fatalf("消息数组应认成 openai: %+v %v", f, ok)
	}
	// 认不出的要老实说认不出：随手一个 .jsonl 不该被当成对话。
	for _, tc := range []struct {
		name string
		head string
	}{
		{"note.jsonl", "{\"a\":1}\n"},
		{"note.jsonl", "随便写点什么\n"},
		{"x.json", `{"other":[]}`},
	} {
		if f, ok := ex.Detect(tc.name, []byte(tc.head)); ok {
			t.Fatalf("%q/%q 不该被认成 %s", tc.name, tc.head, f.Name)
		}
	}
}
