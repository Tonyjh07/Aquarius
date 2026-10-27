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

// openwebui.go Open WebUI 会话的**只读**导入（DESIGN §5.16「只读的一档」）。
//
// 它与 chatgpt 同为树形（`parentId` / `childrenIds` / 一个「当前节点」），所以结构上不缺任何
// 东西；缺的是「我们该不该维护第二套同义产物」。答案是不：要喂 Open WebUI，给它 `chatgpt`
// 那一份，它见到 `mapping` 会自己转换。于是这里只有入口，没有出口。
//
// 它的导出有两种形状（新旧版本各一份），这里都收：
//   - `chat.history.messages` 是以 id 为键的**对象**，配 `chat.history.currentId`；
//   - `chat.messages` 是**数组**，每项自带 `parentId` / `childrenIds`。
//
// 两种都是同一棵树，解析后交给共用的 materialize（节点 ID 重分配 + Restore 自检）。

// owDoc Open WebUI 导出的顶层（只声明我们要用的字段）。
type owDoc struct {
	Title string `json:"title"`
	Chat  owChat `json:"chat"`
}

// owChat 会话本体（`history` 与 `messages` 两种形状各占一半，实际只会出现一种）。
type owChat struct {
	Title   string      `json:"title"`
	History owHistory   `json:"history"`
	Msgs    []owMessage `json:"messages"`
}

// owHistory 旧版形状：`messages` 是以 id 为键的对象，`currentId` 是当前节点。
type owHistory struct {
	Messages  map[string]owMessage `json:"messages"`
	CurrentID string               `json:"currentId"`
}

// owMessage 一个节点。`content` 既可能是字符串，也可能是分片数组。
type owMessage struct {
	ID          string          `json:"id"`
	ParentID    string          `json:"parentId"`
	ChildrenIDs []string        `json:"childrenIds"`
	Role        string          `json:"role"`
	Content     json.RawMessage `json:"content"`
	Timestamp   int64           `json:"timestamp"`
}

// importOpenWebUI 读一份 Open WebUI 导出。
func (e *Exchange) importOpenWebUI(ctx context.Context, r io.Reader) (*conversation.Conversation, port.ExchangeReport, error) {
	rep := port.ExchangeReport{Format: "openwebui"}
	data, err := readLimited(r, maxImportBytes, "文件")
	if err != nil {
		return nil, rep, err
	}
	if err := ctx.Err(); err != nil {
		return nil, rep, err
	}
	var doc owDoc
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, rep, fmt.Errorf("解析 Open WebUI 会话: %w", err)
	}
	msgs := doc.Chat.Msgs
	if len(doc.Chat.History.Messages) > 0 {
		msgs = make([]owMessage, 0, len(doc.Chat.History.Messages))
		for _, m := range doc.Chat.History.Messages {
			msgs = append(msgs, m)
		}
		// map 没有顺序：按 id 排一遍，节点顺序与回执内容才是确定的。
		sort.Slice(msgs, func(i, j int) bool { return msgs[i].ID < msgs[j].ID })
	}
	if len(msgs) == 0 {
		return nil, rep, errors.New("文件里没有消息（这份会话是空的）")
	}
	in := parseOpenWebUI(doc, msgs)
	c, err := materialize(&in)
	if err != nil {
		return nil, rep, err
	}
	rep.Nodes, rep.Dropped = nodeCount(c), in.Dropped
	return c, rep, nil
}

// parseOpenWebUI 摊平成 importedTree。
func parseOpenWebUI(doc owDoc, msgs []owMessage) importedTree {
	title := doc.Chat.Title
	if title == "" {
		title = doc.Title
	}
	byID := make(map[string]owMessage, len(msgs))
	for _, m := range msgs {
		byID[m.ID] = m
	}
	in := importedTree{
		Title: title,
		Head:  doc.Chat.History.CurrentID,
		// 当前节点可能落在被跳过的消息上（纯图片、无 id）：materialize 靠它上溯。
		ParentOf: func(id string) (string, bool) {
			m, ok := byID[id]
			return m.ParentID, ok
		},
	}
	counts := map[string]int{}
	for _, m := range msgs {
		if m.ID == "" {
			counts["没有 id 的消息"]++
			continue
		}
		role, ok := linearImportRole(m.Role)
		if !ok {
			counts["角色 "+m.Role+" 的消息"]++
			continue
		}
		parts := parseLinearContent(m.Content, counts)
		if len(parts) == 0 {
			continue // 整条都装不下（比如纯图片）：不建空节点，交给 reparent 接子节点
		}
		node := importedNode{
			SrcID:    m.ID,
			Role:     role,
			Parts:    parts,
			Parent:   m.ParentID,
			Children: m.ChildrenIDs,
		}
		if m.Timestamp > 0 {
			node.Created = time.Unix(m.Timestamp, 0).UTC()
		}
		in.Nodes = append(in.Nodes, node)
	}
	// 与 chatgpt 侧同一套跳过口径：空节点与装不下的消息不留空壳，子节点顺父指针往上接。
	reparentOpenWebUI(byID, in.Nodes)
	in.Dropped = droppedLines(counts)
	return in
}

// reparentOpenWebUI 把「父节点没被收下」的节点接到最近一个被收下的祖先上。
//
// 与 chatgpt 的 reparent 同口径（那份读的是 mapping，这份读的是扁平消息表），所以各写一遍而
// 不是硬凑一个泛型：两边的源结构不同，硬抽出来只会多一层参数化的绕。

func reparentOpenWebUI(byID map[string]owMessage, nodes []importedNode) {
	kept := make(map[string]bool, len(nodes))
	for _, n := range nodes {
		kept[n.SrcID] = true
	}
	for i := range nodes {
		p := nodes[i].Parent
		for hops := 0; p != "" && !kept[p] && hops < len(byID); hops++ {
			next, ok := byID[p]
			if !ok {
				break
			}
			p = next.ParentID
		}
		if p != "" && !kept[p] {
			if _, exists := byID[p]; !exists {
				continue // 悬空父：不改写，交给自检报因
			}
			p = ""
		}
		nodes[i].Parent = p
		if len(nodes[i].Children) > 0 {
			var keptKids []string
			for _, k := range nodes[i].Children {
				if kept[k] {
					keptKids = append(keptKids, k)
				}
			}
			nodes[i].Children = keptKids
		}
	}
}
