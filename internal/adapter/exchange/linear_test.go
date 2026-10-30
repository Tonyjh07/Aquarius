package exchange

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

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
	rep, err := exportOne(context.Background(), New(Options{}), linearConv(t), "openai", &buf)
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
	if _, err := exportOne(context.Background(), New(Options{}), src, "openai", &buf); err != nil {
		t.Fatalf("export: %v", err)
	}
	got, rep, err := importOne(context.Background(), New(Options{}), bytes.NewReader(buf.Bytes()),
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

// TestImportSkipsEmptyButCounts 导入时正文为空的消息被跳过，但必须计入回执。
//
// 「没丢就不出现丢项」的镜像要求：丢了却不出现同样是错的——少了一条消息而回执说一切安好，
// 用户没有任何线索知道少了什么。
func TestImportSkipsEmptyButCounts(t *testing.T) {
	cases := map[string]string{
		"openai": `{"messages":[
			{"role":"user","content":"第一句"},
			{"role":"assistant","content":""},
			{"role":"user","content":"第三句"}]}`,
		"sillytavern": `{"user_name":"u"}
{"is_user":true,"mes":"第一句"}
{"is_user":false,"mes":"   "}
{"is_user":true,"mes":"第三句"}`,
	}
	for name, doc := range cases {
		t.Run(name, func(t *testing.T) {
			c, rep, err := importOne(context.Background(), New(Options{}), strings.NewReader(doc),
				port.ExchangeFormat{Name: name})
			if err != nil {
				t.Fatalf("import: %v", err)
			}
			if rep.Nodes != 2 {
				t.Fatalf("读入节点数 = %d, want 2（空消息不建节点）", rep.Nodes)
			}
			if !strings.Contains(strings.Join(rep.Dropped, " "), "没有正文") {
				t.Fatalf("跳过空消息必须进回执: %v", rep.Dropped)
			}
			if c.NodeCount() != 3 { // Root + 两条有正文的
				t.Fatalf("树节点数 = %d, want 3", c.NodeCount())
			}
		})
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
	c, rep, err := importOne(context.Background(), New(Options{}), strings.NewReader(doc),
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
		rep, err := exportOne(context.Background(), New(Options{}), src, format, &buf)
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
	rep, err := exportOne(context.Background(), New(Options{}), linearConv(t), "sillytavern", &buf)
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
	if _, err := exportOne(context.Background(), New(Options{}), src, "sillytavern", &buf); err != nil {
		t.Fatalf("export: %v", err)
	}
	got, rep, err := importOne(context.Background(), New(Options{}), bytes.NewReader(buf.Bytes()),
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
	// 对方的世界书/正则/作者注不装：这是格式事实，写在格式表的 LossNote 里，不进逐次回执
	// ——文件里没有这些配置时回执也报一句「丢了」就是在说并不存在的东西。
	if len(rep.Dropped) != 0 {
		t.Fatalf("这份文件没有可丢的内容，回执不该有丢项: %v", rep.Dropped)
	}
}

// TestSillyTavernTimeSymmetric 时间串按本地时区写、按本地时区读，两侧必须对称。
//
// 只做一半（导出打 UTC、导入按本地解）会让时间轴整体漂一个时区，而且「导入 → 再导出」
// 每往返一次就再漂一次。这里把本地时区钉成 UTC+8，再要求往返不漂。
func TestSillyTavernTimeSymmetric(t *testing.T) {
	t.Setenv("TZ", "Asia/Shanghai")
	loc, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		t.Skipf("本机没有时区数据库: %v", err)
	}
	saved := time.Local
	time.Local = loc
	t.Cleanup(func() { time.Local = saved })

	// 源时间在 UTC：导出必须换成 +08:00 的本地字面串。
	at := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	if got := formatSTTime(at); got != "2026-01-02 11:04:05" {
		t.Fatalf("formatSTTime(UTC 03:04:05) = %q, want 本地 11:04:05", got)
	}
	// 读回来要还原成同一时刻（不是同一个字面串）。
	if got := parseSTTime(formatSTTime(at)); !got.Equal(at) {
		t.Fatalf("往返后 = %v, want %v（差 %v）", got, at, got.Sub(at))
	}

	// 端到端：导出 → 导入 → 再导出，字面串不许变（漂时区会在这里露出来）。
	src := conversation.New(conversation.ID("conv-tz"), "跨时区")
	if err := src.AppendCommitted(conversation.Message{
		ID: "S1", Parent: conversation.MessageID(src.ID), Role: conversation.RoleUser,
		Content:   []conversation.Part{{Kind: conversation.PartText, Text: "几点了"}},
		CreatedAt: at,
	}); err != nil {
		t.Fatalf("append: %v", err)
	}
	var first bytes.Buffer
	if _, err := exportOne(context.Background(), New(Options{}), src, "sillytavern", &first); err != nil {
		t.Fatalf("export#1: %v", err)
	}
	c, _, err := importOne(context.Background(), New(Options{}), bytes.NewReader(first.Bytes()),
		port.ExchangeFormat{Name: "sillytavern"})
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	var second bytes.Buffer
	if _, err := exportOne(context.Background(), New(Options{}), c, "sillytavern", &second); err != nil {
		t.Fatalf("export#2: %v", err)
	}
	// 首行是 header（create_date 来自会话创建时间），比它更稳的是比消息行的时间。
	line1 := strings.Split(strings.TrimSpace(first.String()), "\n")
	line2 := strings.Split(strings.TrimSpace(second.String()), "\n")
	if len(line1) != 2 || len(line2) != 2 {
		t.Fatalf("行数 = %d / %d, want 各 2 行", len(line1), len(line2))
	}
	var m1, m2 stMessage
	if err := json.Unmarshal([]byte(line1[1]), &m1); err != nil {
		t.Fatalf("解析第一次产物: %v", err)
	}
	if err := json.Unmarshal([]byte(line2[1]), &m2); err != nil {
		t.Fatalf("解析第二次产物: %v", err)
	}
	if m1.SendDate != m2.SendDate {
		t.Fatalf("往返后时间漂了: %q → %q", m1.SendDate, m2.SendDate)
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
		_, _, err := importOne(context.Background(), New(Options{}), strings.NewReader(tc.doc),
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
