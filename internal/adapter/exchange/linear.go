package exchange

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/Tonyjh07/Aquarius/internal/domain/conversation"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

// linear.go 线性互通（§5.16）：`openai` 与 `sillytavern`。
//
// 线性装不下一棵树，所以口径必须写死：**取 Head 路径**（= 当前上下文那条链），不做「自动挑
// 一个分支」的猜测。两者都只含正文分片：思考、工具、附件分片丢弃并计入回执；角色映射
// user→user、assistant→assistant、system+inject→system（注入不是用户发言，降级成 system 比
// 伪装成用户更诚实）。
//
// 导出与导入共用 headPath：出口是「把这一条链摊平」，入口是「把摊平的一串接成一条链」。

// headPath 导出用的那条链：当前上下文（Root → Head）。
//
// Root 是挂点，不写进任何格式；其余节点按原角色映射，persona（首个 system 节点）自然落成
// system——它是这次会话的语气来源，丢了会让导入回来的对话少一半上下文。
func headPath(c *conversation.Conversation) []conversation.Message {
	return c.Path()
}

// ---- openai ----

// oaDoc `{"messages":[{role,content}]}`（OpenAI 兼容端的通用消息数组）。
type oaDoc struct {
	Messages []oaMessage `json:"messages"`
}

// oaMessage 一条消息：content 既可能是字符串，也可能是分片数组（含图片）。
type oaMessage struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content,omitempty"`
	Name    string          `json:"name,omitempty"`
}

// exportOpenAI 导出 Head 路径为 OpenAI 消息数组。
func (e *Exchange) exportOpenAI(ctx context.Context, c *conversation.Conversation, w io.Writer) (port.ExchangeReport, error) {
	if err := ctx.Err(); err != nil {
		return port.ExchangeReport{Format: "openai"}, err
	}
	rep := port.ExchangeReport{Format: "openai", Nodes: nodeCount(c)}
	counts := map[string]int{}
	path := headPath(c)
	doc := oaDoc{Messages: make([]oaMessage, 0, len(path))}
	for _, m := range path {
		if m.Role == conversation.RoleRoot {
			continue
		}
		text, ok := linearText(m, counts)
		if !ok {
			continue
		}
		doc.Messages = append(doc.Messages, oaMessage{Role: linearRole(m.Role), Content: jsonQuote(text)})
	}
	data, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return rep, fmt.Errorf("序列化消息数组: %w", err)
	}
	data = append(data, '\n')
	if _, err := w.Write(data); err != nil {
		return rep, fmt.Errorf("写消息数组: %w", err)
	}
	rep.Bytes = int64(len(data))
	rep.Nodes = len(doc.Messages)
	rep.Dropped = droppedLines(counts)
	if note := branchNote(c); note != "" {
		rep.Dropped = append(rep.Dropped, note)
	}
	return rep, nil
}

// importOpenAI 读一个消息数组，接成一条链。
func (e *Exchange) importOpenAI(ctx context.Context, r io.Reader) (*conversation.Conversation, port.ExchangeReport, error) {
	rep := port.ExchangeReport{Format: "openai"}
	data, err := readLimited(r, maxImportBytes, "文件")
	if err != nil {
		return nil, rep, err
	}
	if err := ctx.Err(); err != nil {
		return nil, rep, err
	}
	var doc oaDoc
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, rep, fmt.Errorf("解析消息数组: %w", err)
	}
	counts := map[string]int{}
	in := importedTree{}
	prev := ""
	for i, m := range doc.Messages {
		role, ok := linearImportRole(m.Role)
		if !ok {
			counts["角色 "+m.Role+" 的消息"]++
			continue
		}
		parts := parseLinearContent(m.Content, counts)
		if len(parts) == 0 {
			counts["没有正文的消息"]++
			continue
		}
		id := fmt.Sprintf("m%d", i)
		in.Nodes = append(in.Nodes, importedNode{
			SrcID:  id,
			Role:   role,
			Parts:  parts,
			Parent: prev,
		})
		prev = id
	}
	in.Head = prev // 线性文件没有「当前节点」：最后一条就是
	in.Dropped = droppedLines(counts)
	c, err := materialize(&in)
	if err != nil {
		return nil, rep, err
	}
	rep.Nodes, rep.Dropped = nodeCount(c), in.Dropped
	return c, rep, nil
}

// ---- sillytavern ----

// stHeader SillyTavern `.jsonl` 的首行。
//
// 我们只声明读写需要的字段：`user_name` / `character_name` 是回执与标题的原料，其余（世界书、
// 正则、作者注）属于对方的工作区配置，不是这份对话本身（§5.16 不装配置）。
type stHeader struct {
	UserName      string `json:"user_name,omitempty"`
	CharacterName string `json:"character_name,omitempty"`
	CreateDate    string `json:"create_date,omitempty"`
}

// stMessage `.jsonl` 的每一行（只声明我们读写的字段；对方的 `extra` 等扩展原样忽略）。
type stMessage struct {
	Name     string `json:"name,omitempty"`
	IsUser   bool   `json:"is_user"`
	IsSystem bool   `json:"is_system,omitempty"`
	Mes      string `json:"mes"`
	SendDate string `json:"send_date,omitempty"`
}

// exportSillyTavern 导出 Head 路径为 `.jsonl`：首行 header + 每行一条 mes。
func (e *Exchange) exportSillyTavern(ctx context.Context, c *conversation.Conversation, w io.Writer) (port.ExchangeReport, error) {
	if err := ctx.Err(); err != nil {
		return port.ExchangeReport{Format: "sillytavern"}, err
	}
	rep := port.ExchangeReport{Format: "sillytavern", Nodes: nodeCount(c)}
	counts := map[string]int{}
	var b strings.Builder
	header, err := json.Marshal(stHeader{CreateDate: formatSTTime(c.CreatedAt)})
	if err != nil {
		return rep, fmt.Errorf("序列化首行: %w", err)
	}
	b.Write(header)
	b.WriteByte('\n')
	n := 0
	for _, m := range headPath(c) {
		if m.Role == conversation.RoleRoot {
			continue
		}
		text, ok := linearText(m, counts)
		if !ok {
			continue
		}
		line, err := json.Marshal(stMessage{
			Name:     stName(m.Role),
			IsUser:   m.Role == conversation.RoleUser,
			IsSystem: m.Role == conversation.RoleSystem || m.Role == conversation.RoleInject,
			Mes:      text,
			SendDate: formatSTTime(m.CreatedAt),
		})
		if err != nil {
			return rep, fmt.Errorf("序列化消息: %w", err)
		}
		b.Write(line)
		b.WriteByte('\n')
		n++
	}
	if _, err := io.WriteString(w, b.String()); err != nil {
		return rep, fmt.Errorf("写对话文件: %w", err)
	}
	rep.Bytes = int64(b.Len())
	rep.Nodes = n
	rep.Dropped = droppedLines(counts)
	if note := branchNote(c); note != "" {
		rep.Dropped = append(rep.Dropped, note)
	}
	return rep, nil
}

// importSillyTavern 读 `.jsonl`：首行 header、每行一条 mes。
func (e *Exchange) importSillyTavern(ctx context.Context, r io.Reader) (*conversation.Conversation, port.ExchangeReport, error) {
	rep := port.ExchangeReport{Format: "sillytavern"}
	data, err := readLimited(r, maxImportBytes, "文件")
	if err != nil {
		return nil, rep, err
	}
	if err := ctx.Err(); err != nil {
		return nil, rep, err
	}
	lines := splitLines(data)
	if len(lines) == 0 {
		return nil, rep, errors.New("文件是空的")
	}
	// 有些导出省掉首行头，直接从消息起（SillyTavern 自己也容得下）：首行带 `mes` 就整份按
	// 消息读，否则首行是 header、其余是消息。
	start := 1
	var header stHeader
	if !isSTMessageLine(lines[0]) {
		if err := json.Unmarshal(lines[0], &header); err != nil {
			return nil, rep, fmt.Errorf("第一行不是对话头（SillyTavern 的 .jsonl 首行是元信息）: %w", err)
		}
	} else {
		start = 0
	}
	counts := map[string]int{}
	in := importedTree{CreatedAt: parseSTTime(header.CreateDate)}
	if header.CharacterName != "" {
		in.Title = header.CharacterName
	}
	prev := ""
	for i, line := range lines[start:] {
		var m stMessage
		if err := json.Unmarshal(line, &m); err != nil {
			return nil, rep, fmt.Errorf("第 %d 行解析失败: %w", i+start+1, err)
		}
		role := conversation.RoleAssistant
		switch {
		case m.IsSystem:
			role = conversation.RoleSystem
		case m.IsUser:
			role = conversation.RoleUser
		}
		if strings.TrimSpace(m.Mes) == "" {
			counts["没有正文的消息"]++
			continue
		}
		id := fmt.Sprintf("m%d", i)
		in.Nodes = append(in.Nodes, importedNode{
			SrcID:   id,
			Role:    role,
			Parts:   []conversation.Part{{Kind: conversation.PartText, Text: m.Mes}},
			Created: parseSTTime(m.SendDate),
			Parent:  prev,
		})
		prev = id
	}
	in.Head = prev
	in.Dropped = droppedLines(counts)
	c, err := materialize(&in)
	if err != nil {
		return nil, rep, err
	}
	rep.Nodes, rep.Dropped = nodeCount(c), in.Dropped
	return c, rep, nil
}

// ---- 共用 ----

// linearText 一个节点在「只有正文」的线性格式里能带出去什么。
//
// 返回 ok=false 表示这条节点在线性格式里没有可写的内容（比如纯工具调用的回答）：不写空行，
// 但也必须让调用方知道它没被带出去——计数在这里，回执里会说明。
func linearText(m conversation.Message, counts map[string]int) (string, bool) {
	var texts []string
	for _, p := range m.Content {
		switch p.Kind {
		case conversation.PartText:
			texts = append(texts, p.Text)
		case conversation.PartDoc:
			if p.Text != "" {
				texts = append(texts, p.Text)
			}
			counts["文档原文件（只带提取的文本）"]++
		case conversation.PartImage, conversation.PartAudio:
			counts["附件"]++
		case conversation.PartThinking:
			counts["思考过程"]++
		case conversation.PartTool:
			counts["工具调用"]++
		default:
			counts["认不出的分片"]++
		}
	}
	if len(texts) == 0 {
		counts["没有正文的消息"]++
		return "", false
	}
	return strings.Join(texts, "\n"), true
}

// linearRole 我们的角色 → 线性格式的角色。
func linearRole(r conversation.Role) string {
	switch r {
	case conversation.RoleUser:
		return "user"
	case conversation.RoleAssistant:
		return "assistant"
	default:
		return "system"
	}
}

// linearImportRole 线性格式的角色 → 我们的角色。
func linearImportRole(role string) (conversation.Role, bool) {
	switch role {
	case "user":
		return conversation.RoleUser, true
	case "assistant":
		return conversation.RoleAssistant, true
	case "system", "developer":
		return conversation.RoleSystem, true
	case "tool", "function":
		// 工具结果：读成注入节点，既不冒充用户也不丢内容（与 chatgpt 侧同口径）。
		return conversation.RoleInject, true
	default:
		return "", false
	}
}

// parseLinearContent OpenAI 的 content：字符串或分片数组（`{"type":"text","text":"…"}`）。
func parseLinearContent(raw json.RawMessage, counts map[string]int) []conversation.Part {
	if len(raw) == 0 {
		return nil
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		if s == "" {
			return nil
		}
		return []conversation.Part{{Kind: conversation.PartText, Text: s}}
	}
	var items []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(raw, &items); err != nil {
		counts["认不出的正文"]++
		return nil
	}
	var parts []conversation.Part
	for _, it := range items {
		switch it.Type {
		case "text", "input_text", "output_text":
			if it.Text != "" {
				parts = append(parts, conversation.Part{Kind: conversation.PartText, Text: it.Text})
			}
		case "image_url", "input_image":
			counts["图片（只有地址，字节取不到）"]++
		default:
			counts["正文分片 "+it.Type]++
		}
	}
	return parts
}

// branchNote 线性导出时说明旁支不在其中（只在真有旁支时加这一句，免得每次都说）。
func branchNote(c *conversation.Conversation) string {
	path := make(map[conversation.MessageID]bool)
	for _, m := range c.Path() {
		path[m.ID] = true
	}
	skipped := 0
	for _, m := range c.Snapshot().Nodes {
		if m.Role != conversation.RoleRoot && !path[m.ID] {
			skipped++
		}
	}
	if skipped == 0 {
		return ""
	}
	return fmt.Sprintf("旁支上的 %d 条消息（只导出当前那条链）", skipped)
}

// stName SillyTavern 那一行的名字：对方靠 is_user / is_system 判角色，名字是展示用。
func stName(r conversation.Role) string {
	switch r {
	case conversation.RoleUser:
		return "User"
	case conversation.RoleSystem, conversation.RoleInject:
		return "System"
	default:
		return "Assistant"
	}
}

// formatSTTime / parseSTTime SillyTavern 的时间串（`2006-01-02 15:04:05`，本地时间）。
//
// 对方按本地时间读写，两侧必须对称：导出先换到本地时区再打字符串，导入按本地时区解。
// 只做一半（导出打 UTC、导入按本地解）会让时间轴整体漂一个时区，而且「导入 → 再导出」
// 每往返一次就再漂一次。
func formatSTTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.In(time.Local).Format("2006-01-02 15:04:05")
}

func parseSTTime(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	t, err := time.ParseInLocation("2006-01-02 15:04:05", s, time.Local)
	if err != nil {
		return time.Time{}
	}
	return t
}

// jsonQuote 把一个字符串编码成 JSON 值（消息数组里的 content 是字符串，不是对象）。
func jsonQuote(s string) json.RawMessage {
	b, err := json.Marshal(s)
	if err != nil {
		return json.RawMessage(`""`)
	}
	return b
}

// splitLines 按 UTF-8 文本切成 `.jsonl` 的行：去掉行尾 `\r`（对方可能写 CRLF）与空行。
func splitLines(data []byte) [][]byte {
	var out [][]byte
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSuffix(line, "\r")
		if strings.TrimSpace(line) == "" {
			continue
		}
		out = append(out, []byte(line))
	}
	return out
}

// isSTMessageLine 这一行是不是一条消息（而非首行的 header）：带 `mes` 就是。
func isSTMessageLine(line []byte) bool {
	var probe map[string]json.RawMessage
	if err := json.Unmarshal(line, &probe); err != nil {
		return false
	}
	_, ok := probe["mes"]
	return ok
}

// looksLikeSillyTavern `.jsonl` 判定：首行是 JSON 对象，且带 header（`user_name` 一类）或
// 一条消息（`mes` / `is_user`）的字段。
//
// 只按扩展名认太宽：用户随手存一个 `.jsonl` 就会被当成对话读进来。这里多看一眼首行的键——
// 认错时 Import 仍会报因，但「认不出」比「认成别的格式」更接近事实。
//
// 不看扩展名：SillyTavern 允许省掉首行头、直接从消息起，`{"mes":…}` 开头就够判。
func looksLikeSillyTavern(head []byte) bool {
	lines := splitLines(head)
	if len(lines) == 0 {
		return false
	}
	var probe map[string]json.RawMessage
	if err := json.Unmarshal(lines[0], &probe); err != nil {
		return false
	}
	for _, key := range []string{"mes", "is_user", "is_system", "user_name", "character_name", "create_date"} {
		if _, ok := probe[key]; ok {
			return true
		}
	}
	return false
}
