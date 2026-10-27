package exchange

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"time"

	"github.com/Tonyjh07/Aquarius/internal/domain/conversation"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

// chatgpt.go ChatGPT 的 `conversations.json` 形状（DESIGN §5.16「树形互通」）。
//
// 选它而不自定一套，是因为它已经是事实标准：Open WebUI 一类见到 `mapping` 会自己转换。
// 我们的树本来就是树（parent 单值、children 有序），映射不需要降级成 DAG，所以这一档只丢
// 「分片种类」与「附件字节」，结构一点不丢——回执里照实说明丢的是这些。

// chatEnvelope 文件顶层：真实导出是**数组**，一项一个会话。
type chatEnvelope []chatConversation

// chatConversation 一个会话（只声明我们要用的字段；其余原样忽略）。
type chatConversation struct {
	Title       string              `json:"title"`
	CreateTime  float64             `json:"create_time,omitempty"`
	UpdateTime  float64             `json:"update_time,omitempty"`
	Mapping     map[string]chatNode `json:"mapping"`
	CurrentNode string              `json:"current_node,omitempty"`
}

// chatNode mapping 里的一个节点。
type chatNode struct {
	ID       string   `json:"id"`
	Message  *chatMsg `json:"message"`
	Parent   string   `json:"parent,omitempty"`
	Children []string `json:"children,omitempty"`
}

// chatMsg 一条消息。
type chatMsg struct {
	ID         string      `json:"id,omitempty"`
	Author     chatAuthor  `json:"author"`
	CreateTime float64     `json:"create_time,omitempty"`
	Content    chatContent `json:"content"`
}

// chatAuthor 作者（role 是这里唯一的语义）。
type chatAuthor struct {
	Role string `json:"role"`
	Name string `json:"name,omitempty"`
}

// chatContent 正文：`parts` 是主流形状（字符串或图片对象），`text` 是纯文本捷径。
type chatContent struct {
	ContentType string            `json:"content_type"`
	Parts       []json.RawMessage `json:"parts,omitempty"`
	Text        string            `json:"text,omitempty"`
}

// exportChatGPT 我们这棵树 → 一个 chatgpt 会话对象（顶层数组只放这一项）。
func (e *Exchange) exportChatGPT(ctx context.Context, c *conversation.Conversation, w io.Writer) (port.ExchangeReport, error) {
	if err := ctx.Err(); err != nil {
		return port.ExchangeReport{Format: "chatgpt"}, err
	}
	out, rep := toChatConversation(c)
	data, err := json.MarshalIndent(chatEnvelope{out}, "", "  ")
	if err != nil {
		return rep, fmt.Errorf("序列化 ChatGPT 会话: %w", err)
	}
	data = append(data, '\n')
	if _, err := w.Write(data); err != nil {
		return rep, fmt.Errorf("写 ChatGPT 会话: %w", err)
	}
	rep.Bytes = int64(len(data))
	return rep, nil
}

// toChatConversation 树的投影。
//
// 结构照搬：每个节点一条 mapping 记录，`parent` / `children` 直接用我们的结构边，`current_node`
// 是我们的 Head。Root 也要写（它的 message 为 null）——ChatGPT 的导出里同样有一个空根，
// 少了它读回来时顶层消息就无处挂。
func toChatConversation(c *conversation.Conversation) (chatConversation, port.ExchangeReport) {
	s := c.Snapshot()
	rep := port.ExchangeReport{Format: "chatgpt", Nodes: nodeCount(c)}
	out := chatConversation{
		Title:       s.Title,
		CreateTime:  unixSeconds(s.CreatedAt),
		UpdateTime:  unixSeconds(s.UpdatedAt),
		Mapping:     make(map[string]chatNode, len(s.Nodes)),
		CurrentNode: string(s.Head),
	}
	childs := make(map[conversation.MessageID][]conversation.MessageID, len(s.Edges))
	for _, e := range s.Edges {
		childs[e.Parent] = e.Childs
	}
	counts := map[string]int{}
	for _, m := range s.Nodes {
		n := chatNode{ID: string(m.ID)}
		if m.Role == conversation.RoleRoot {
			n.Message = nil // 空根：ChatGPT 的同一位置也只有一个挂点
		} else {
			msg := toChatMessage(m, counts)
			n.Message = &msg
		}
		n.Parent = string(m.Parent)
		for _, k := range childs[m.ID] {
			n.Children = append(n.Children, string(k))
		}
		out.Mapping[string(m.ID)] = n
	}
	rep.Dropped = droppedLines(counts)
	return out, rep
}

// toChatMessage 一个节点 → ChatGPT 消息；装不下的分片与元数据就地计入 counts。
//
// ChatGPT 的消息装得下文本、装不下附件字节、思考、工具调用（那些有各自的字段，但我们没有
// 对得上号的东西可写）。所以这里只搬正文，其余逐类计数——回执告诉用户「带出去了什么」，
// 而不是「大概能读」。
func toChatMessage(m conversation.Message, counts map[string]int) chatMsg {
	msg := chatMsg{
		ID:         string(m.ID),
		Author:     chatAuthor{Role: chatRole(m.Role)},
		CreateTime: unixSeconds(m.CreatedAt),
		Content:    chatContent{ContentType: "text"},
	}
	var texts []string
	for _, p := range m.Content {
		switch p.Kind {
		case conversation.PartText:
			texts = append(texts, p.Text)
		case conversation.PartDoc:
			// 文档的提取文本就是「这条消息的正文」，带出去；原文件字节带不了。
			if p.Text != "" {
				texts = append(texts, p.Text)
			}
			counts["文档原文件（只带提取的文本）"]++
		case conversation.PartImage, conversation.PartAudio:
			// 一个字段都不写：ChatGPT 的图片是它的文件服务句柄，我们这边没有可用的地址，
			// 硬塞一个对方取不到的伪指针只是把「没带」包装成「带了但取不到」。
			counts["附件（不带字节）"]++
		case conversation.PartThinking:
			counts["思考过程"]++
		case conversation.PartTool:
			counts["工具调用"]++
		default:
			counts["认不出的分片"]++
		}
	}
	if len(texts) == 0 {
		texts = []string{""}
	}
	parts := make([]json.RawMessage, len(texts))
	for i, t := range texts {
		parts[i], _ = json.Marshal(t)
	}
	msg.Content.Parts = parts
	countChatMeta(m, counts)
	return msg
}

// countChatMeta 逐类记下节点级元数据里带不出去的东西。
//
// ChatGPT 的消息没有地方放我们的终态/模型/用量，硬塞自定义键会被对方当成噪声；但「没带」
// 是事实，回执要说出来。逐条计数而不逐条列名字：这些字段几乎每条 assistant 消息都有，
// 按条列会把回执刷成一页。
func countChatMeta(m conversation.Message, counts map[string]int) {
	if m.Outcome != "" {
		counts["消息终态"]++
	}
	if m.Model != "" {
		counts["模型标注"]++
	}
	if m.Usage != (conversation.Usage{}) {
		counts["token 用量"]++
	}
	if m.Source != "" || m.Trust != conversation.TrustExternal || m.Trigger {
		counts["注入来源与信任级"]++
	}
	for _, p := range m.Content {
		if p.Kind == conversation.PartThinking && p.Signature != "" {
			counts["思考签名"]++
		}
	}
}

// chatRole 我们的角色 → ChatGPT 的角色。
//
// inject（注入节点）降级成 system：它不是用户发言，伪装成用户比降级成系统更不诚实。
func chatRole(r conversation.Role) string {
	switch r {
	case conversation.RoleUser:
		return "user"
	case conversation.RoleAssistant:
		return "assistant"
	default:
		return "system"
	}
}

// importChatGPT 读一份 ChatGPT 导出（数组只取第 1 个会话）。
func (e *Exchange) importChatGPT(ctx context.Context, r io.Reader) (*conversation.Conversation, port.ExchangeReport, error) {
	rep := port.ExchangeReport{Format: "chatgpt"}
	data, err := readLimited(r, maxImportBytes, "文件")
	if err != nil {
		return nil, rep, err
	}
	if err := ctx.Err(); err != nil {
		return nil, rep, err
	}
	var env chatEnvelope
	if err := json.Unmarshal(data, &env); err != nil {
		// 也接受单个会话对象（有些工具导出时不套数组）。
		var single chatConversation
		if err2 := json.Unmarshal(data, &single); err2 != nil || single.Mapping == nil {
			return nil, rep, fmt.Errorf("解析 ChatGPT 会话: %w", err)
		}
		env = chatEnvelope{single}
	}
	if len(env) == 0 {
		return nil, rep, errors.New("文件里没有会话")
	}
	in := parseChatTree(env[0])
	if len(env) > 1 {
		in.Dropped = append(in.Dropped,
			fmt.Sprintf("另外 %d 个会话（一个文件里只导入第 1 个）", len(env)-1))
	}
	c, err := materialize(&in)
	if err != nil {
		return nil, rep, err
	}
	rep.Nodes = nodeCount(c)
	rep.Dropped = in.Dropped
	return c, rep, nil
}

// parseChatTree ChatGPT 的 mapping → importedTree（不认的东西记进 Dropped）。
func parseChatTree(conv chatConversation) importedTree {
	in := importedTree{
		Title:     conv.Title,
		Head:      conv.CurrentNode,
		CreatedAt: fromUnixSeconds(conv.CreateTime),
		UpdatedAt: fromUnixSeconds(conv.UpdateTime),
		// 当前节点可能落在被跳过的消息上（纯图片、纯思考）：materialize 靠它上溯。
		ParentOf: func(id string) (string, bool) {
			n, ok := conv.Mapping[id]
			return n.Parent, ok
		},
	}
	counts := map[string]int{}
	ids := make([]string, 0, len(conv.Mapping))
	for id := range conv.Mapping {
		ids = append(ids, id)
	}
	sort.Strings(ids) // 源是 map：排序后再建，节点顺序才是确定的（同一份文件导两次结构相同）
	for _, id := range ids {
		n := conv.Mapping[id]
		if n.Message == nil {
			continue // 空根与挂在它下面的空节点：不是一个消息
		}
		role, ok := chatImportRole(n.Message.Author.Role)
		if !ok {
			counts["角色 "+n.Message.Author.Role+" 的消息"]++
			continue
		}
		parts := parseChatContent(n.Message.Content, counts)
		if len(parts) == 0 {
			continue // 整条都装不下（比如纯图片消息）：不建空节点；原因已由 parseChatContent 计数
		}
		in.Nodes = append(in.Nodes, importedNode{
			SrcID:    id,
			Role:     role,
			Parts:    parts,
			Created:  fromUnixSeconds(n.Message.CreateTime),
			Parent:   n.Parent,
			Children: n.Children,
		})
	}
	reparent(conv.Mapping, in.Nodes)
	in.Dropped = droppedLines(counts)
	return in
}

// reparent 把「父节点没被收下」的节点接到最近一个被收下的祖先上。
//
// ChatGPT 的导出里有几类空节点（空根、只有标题的节点）与被我们跳过的消息（纯图片）。它们的
// 子节点不能因此失去位置，也不能直接挂到根上把深度压平——顺着父指针往上找到第一个收下的
// 节点，结构与源里一致，只是中间那几个空壳不在。
func reparent(mapping map[string]chatNode, nodes []importedNode) {
	kept := make(map[string]bool, len(nodes))
	for _, n := range nodes {
		kept[n.SrcID] = true
	}
	for i := range nodes {
		p := nodes[i].Parent
		for hops := 0; p != "" && !kept[p] && hops < len(mapping); hops++ {
			next, ok := mapping[p]
			if !ok {
				break // 文件里根本没有这个父节点：留给自检报「父节点不在文件里」
			}
			p = next.Parent
		}
		if p != "" && !kept[p] {
			if _, exists := mapping[p]; !exists {
				// 悬空父：不改写，交给 materialize 明确报因（不「尽力修复」）。
				continue
			}
			p = "" // 祖先链走到了顶：挂到根下
		}
		nodes[i].Parent = p
		// 子节点清单也要筛：源里列的孩子可能正是被跳过的那几个空壳。
		if len(nodes[i].Children) > 0 {
			keptKids := make([]string, 0, len(nodes[i].Children))
			for _, k := range nodes[i].Children {
				if kept[k] {
					keptKids = append(keptKids, k)
				}
			}
			nodes[i].Children = keptKids
		}
	}
}

// chatImportRole ChatGPT 的角色 → 我们的角色。
//
// 这里的 `tool` 与我们已删除的 RoleTool 同名但不同义：它是「工具结果的正文」，我们把它读成
// 注入节点（系统上下文）——正文来自工具、不是用户说的，所以既不当用户发言，也不丢。
// 其余奇特角色（`critic` 之类）一律不认，计入丢弃项。
func chatImportRole(role string) (conversation.Role, bool) {
	switch role {
	case "user":
		return conversation.RoleUser, true
	case "assistant":
		return conversation.RoleAssistant, true
	case "system":
		return conversation.RoleSystem, true
	case "tool":
		return conversation.RoleInject, true
	default:
		return "", false
	}
}

// parseChatContent ChatGPT 的 content → 我们的分片。
func parseChatContent(content chatContent, counts map[string]int) []conversation.Part {
	if content.Text != "" && len(content.Parts) == 0 {
		return []conversation.Part{{Kind: conversation.PartText, Text: content.Text}}
	}
	var parts []conversation.Part
	for _, raw := range content.Parts {
		var s string
		if err := json.Unmarshal(raw, &s); err == nil {
			if s != "" {
				parts = append(parts, conversation.Part{Kind: conversation.PartText, Text: s})
			}
			continue
		}
		var obj struct {
			ContentType string `json:"content_type"`
		}
		if err := json.Unmarshal(raw, &obj); err != nil {
			counts["认不出的正文分片"]++
			continue
		}
		switch obj.ContentType {
		case "image_asset_pointer":
			counts["图片引用（只有地址，字节取不到）"]++
		default:
			counts["正文分片 "+obj.ContentType]++
		}
	}
	if len(parts) == 0 {
		switch content.ContentType {
		case "", "text", "multimodal_text", "code":
			// 这几种只是「装不下」的纯附件消息：分片层的丢项已逐类记过，这里不再重复。
			// 「整条消息因此消失」由调用方统一记一条，两条事实分开说。
		default:
			counts["正文类型 "+content.ContentType+" 的消息"]++
		}
	}
	return parts
}

// droppedLines 计数表 → 回执行（按种类名排序，稳定可读）。
func droppedLines(counts map[string]int) []string {
	lines := make([]string, 0, len(counts))
	for kind, n := range counts {
		if n > 0 {
			lines = append(lines, fmt.Sprintf("%s %d 个", kind, n))
		}
	}
	sort.Strings(lines)
	return lines
}

// unixSeconds / fromUnixSeconds 时间戳换算（ChatGPT 用浮点秒；0 = 字段缺失）。
func unixSeconds(t time.Time) float64 {
	if t.IsZero() {
		return 0
	}
	return float64(t.UnixNano()) / 1e9
}

func fromUnixSeconds(f float64) time.Time {
	if f <= 0 {
		return time.Time{}
	}
	sec := int64(f)
	nsec := int64((f - float64(sec)) * 1e9)
	return time.Unix(sec, nsec).UTC()
}
