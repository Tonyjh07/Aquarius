package exchange

import (
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/Tonyjh07/Aquarius/internal/domain/conversation"
)

// tree.go 外部格式解析出来的一棵「待入库」树，以及把它搬成我们这棵树的唯一入口。
//
// 为什么中间隔一层：chatgpt / openwebui 都是树形、openai / sillytavern 是线性，四家的字段名
// 各说各的，但「谁是父、谁是子、谁是这个角色、正文是什么」是一回事。各家解析器只负责把它们
// 的字段摊成 importedTree，节点 ID 重分配、Root 与 persona 的安置、不变量自检只写一遍。
// 否则每接一家都要重抄一次「怎么建 Root」，而那种手抄最容易漏掉的正是校验。

// 导入侧正文与结构上限（§5.16：超限报错，不静默截断）。
const (
	// maxImportBytes 一份导入文件的总字节上限（先整个读进内存再解析，所以必须有个上限）。
	maxImportBytes = 256 << 20
	// maxImportTextBytes 全部节点正文合计上限。
	maxImportTextBytes = 64 << 20
	// maxImportNodes 节点数上限（与 aqx 同一口径）。
	maxImportNodes = 20000
)

// importedNode 外部格式里的一个节点（字段已归一，ID 仍用源里的）。
type importedNode struct {
	SrcID    string
	Role     conversation.Role
	Parts    []conversation.Part
	Created  time.Time
	Parent   string   // "" = 树的挂点（我们的 Root 之下）
	Children []string // 有序；源里没给就按 Parent 反推
}

// importedTree 一份外部会话。
type importedTree struct {
	Title     string
	Head      string // 源里的「当前节点」；空 = 取最深的一条链
	CreatedAt time.Time
	UpdatedAt time.Time
	Nodes     []importedNode
	// Dropped 源里我们不认、或装不下而丢掉的东西（逐项可读，进回执）。
	//
	// materialize 也会往里补：它自己发现的事实（比如当前节点被跳过了）同样要进回执，
	// 所以它改的是这份树的 Dropped，调用方在 materialize 之后才读。
	Dropped []string
	// ParentOf 源里的父指针查询（second = 该 ID 在源里确实存在）。
	//
	// 只在「当前节点被跳过」时需要：那时要顺着父指针上溯到最近收下的一条。源里没有父指针
	// 概念的格式（线性）留空，Head 一定落在收下的节点上，用不到它。
	ParentOf func(id string) (parent string, ok bool)
}

// materialize 把 importedTree 搬成我们这棵树：新会话 ID、节点 ID 全部重分配、Root 补在最前，
// 最后由 conversation.Restore 做统一自检。
//
// 源 ID 只用来建边，绝不带进新树：导两次同一份文件得到两棵互相独立的树，也不会与既有会话
// 撞 ID（§5.16「落成新会话」）。
//
// 收 *importedTree 而不是值：它自己发现的事实（当前节点被跳过时回退了）也要进回执，而回执
// 的丢项清单是调用方在返回后读的那一份。
func materialize(in *importedTree) (*conversation.Conversation, error) {
	nodes, err := checkImported(*in)
	if err != nil {
		return nil, err
	}
	if len(nodes) > maxImportNodes {
		return nil, fmt.Errorf("会话节点数 %d 超过 %d 上限", len(nodes), maxImportNodes)
	}
	var textBytes int64
	for _, n := range nodes {
		for _, p := range n.Parts {
			textBytes += int64(len(p.Text)) + int64(len(p.Transcript))
		}
	}
	if textBytes > maxImportTextBytes {
		return nil, fmt.Errorf("正文合计 %d MB 超过 %d MB 上限", textBytes>>20, maxImportTextBytes>>20)
	}

	id := conversation.NewID()
	rootID := conversation.MessageID(id)
	now := time.Now().UTC()
	created := in.CreatedAt
	if created.IsZero() {
		created = now
	}
	updated := in.UpdatedAt
	if updated.IsZero() {
		updated = created
	}
	root := conversation.Message{
		ID:        rootID,
		Role:      conversation.RoleRoot,
		CreatedAt: created,
	}
	// 源 ID → 新 ID。根挂点（""）与新 Root 都指向 Root。
	newIDs := map[string]conversation.MessageID{"": rootID}
	restored := make([]conversation.Message, 0, len(nodes)+1)
	restored = append(restored, root)
	for _, n := range nodes {
		newIDs[n.SrcID] = conversation.NewMessageID()
	}
	for _, n := range nodes {
		parent, ok := newIDs[n.Parent]
		if !ok {
			return nil, fmt.Errorf("节点 %q 的父节点不在文件里（源 ID %q）", n.SrcID, n.Parent)
		}
		restored = append(restored, conversation.Message{
			ID:        newIDs[n.SrcID],
			Parent:    parent,
			Role:      n.Role,
			Content:   n.Parts,
			CreatedAt: nodeTime(n, created),
		})
	}
	edges, err := buildEdges(nodes, newIDs, rootID)
	if err != nil {
		return nil, err
	}
	// 树外挂点单独一条边（"" → Root）：不变量 1 要求它恒存在且只有一个孩子（D19）。
	edges = append([]conversation.SnapshotEdge{{Parent: "", Childs: []conversation.MessageID{rootID}}}, edges...)
	head, headNote := resolveHead(*in, nodes, newIDs, rootID)
	if headNote != "" {
		in.Dropped = append(in.Dropped, headNote)
	}
	c, err := conversation.Restore(conversation.Snapshot{
		ID:        id,
		Title:     titleOrDefault(in.Title),
		Head:      head,
		Nodes:     restored,
		Edges:     edges,
		CreatedAt: created,
		UpdatedAt: updated,
	})
	if err != nil {
		return nil, fmt.Errorf("导入的会话结构不合法: %w", err)
	}
	return c, nil
}

// resolveHead 定 Head：源里声明了就按它，没声明就取最深的一条链。
//
// 源里声明的那个节点可能被解析阶段跳过了（纯附件、纯思考、空壳——它们建不出消息），此时
// 顺着源父指针上溯到最近一个收下的节点，并返回一行说明进回执。整份拒收是不对的：
// Head 落在附件消息上是常态（用户发了张图、助手还没答），我们自己导出的 chatgpt 文件里
// current_node 就可能正是这种节点，拒收等于「自己写的文件自己读不回来」。
func resolveHead(in importedTree, nodes []importedNode, newIDs map[string]conversation.MessageID, rootID conversation.MessageID) (conversation.MessageID, string) {
	if in.Head == "" {
		if deepest := deepestNode(nodes); deepest != "" {
			return newIDs[deepest], ""
		}
		return rootID, ""
	}
	if id, ok := newIDs[in.Head]; ok {
		return id, ""
	}
	if in.ParentOf == nil {
		// 没有父指针可走（线性格式）：Head 只可能是解析器自己写的那条，走不到这里。
		return rootID, ""
	}
	cur := in.Head
	for hops := 0; hops <= len(nodes); hops++ {
		parent, ok := in.ParentOf(cur)
		if !ok {
			// 源里根本没有这个 ID：不是「被跳过」而是文件对不上，仍按缺失处理但说明白。
			return rootID, "当前节点不在文件里（已退回到会话开头）"
		}
		if id, kept := newIDs[parent]; kept {
			return id, "当前节点没有可显示的内容（不是消息），已退回到上一条"
		}
		cur = parent
	}
	return rootID, "当前节点没有可显示的内容（不是消息），已退回到会话开头"
}

// nodeTime 节点时间：源里给了就用，没给退回 fallback——字段缺失不等于「1970 年」。
func nodeTime(n importedNode, fallback time.Time) time.Time {
	if n.Created.IsZero() {
		return fallback
	}
	return n.Created
}

// checkImported 结构自检：源 ID 唯一且非空、角色可识别、正文不为空的结构错误先报出来。
func checkImported(in importedTree) ([]importedNode, error) {
	if len(in.Nodes) == 0 {
		return nil, errors.New("文件里没有消息（这份会话是空的）")
	}
	seen := make(map[string]bool, len(in.Nodes))
	for i, n := range in.Nodes {
		if n.SrcID == "" {
			return nil, fmt.Errorf("第 %d 个节点没有 ID", i+1)
		}
		if seen[n.SrcID] {
			return nil, fmt.Errorf("节点 ID %q 重复", n.SrcID)
		}
		seen[n.SrcID] = true
		switch n.Role {
		case conversation.RoleUser, conversation.RoleAssistant,
			conversation.RoleSystem, conversation.RoleInject:
		default:
			return nil, fmt.Errorf("节点 %q 的角色 %q 不认识", n.SrcID, n.Role)
		}
	}
	return in.Nodes, nil
}

// buildEdges 建结构边：源里有 children 就按它（顺序即兄弟顺序，D152），没有就按 Parent 反推。
//
// 两条都走：children 只列了一部分时，缺的那部分仍要按 Parent 补上——「尽力修复」与「补全
// 源里漏写的结构」不是一回事，后者只补源里已经表达过的父子关系。
func buildEdges(nodes []importedNode, newIDs map[string]conversation.MessageID, rootID conversation.MessageID) ([]conversation.SnapshotEdge, error) {
	order := make(map[string][]string, len(nodes))
	for _, n := range nodes {
		order[n.SrcID] = append(order[n.SrcID], n.Children...)
	}
	listed := make(map[string]bool, len(nodes))
	for parent, kids := range order {
		for _, k := range kids {
			if _, ok := newIDs[k]; !ok {
				return nil, fmt.Errorf("节点 %q 的子节点 %q 不在文件里", parent, k)
			}
			listed[k] = true
		}
	}
	for _, n := range nodes {
		if listed[n.SrcID] {
			continue
		}
		order[n.Parent] = append(order[n.Parent], n.SrcID)
	}
	out := make([]conversation.SnapshotEdge, 0, len(order))
	parents := make([]string, 0, len(order))
	for parent := range order {
		parents = append(parents, parent)
	}
	sort.Strings(parents) // map 没有顺序：排一遍，同一份文件导两次得到同样的边
	for _, parent := range parents {
		kids := order[parent]
		pid, ok := newIDs[parent]
		if !ok {
			return nil, fmt.Errorf("节点 %q 的父节点不在文件里", parent)
		}
		ids := make([]conversation.MessageID, 0, len(kids))
		for _, k := range kids {
			ids = append(ids, newIDs[k])
		}
		out = append(out, conversation.SnapshotEdge{Parent: pid, Childs: ids})
	}
	return out, nil
}

// deepestNode 最深的一条链的末端（源里没声明当前节点时用它当 Head）。
//
// 线性格式与一些导出不带 `current_node`；此时「最后一条」比「随便挑一个」更接近用户预期。
func deepestNode(nodes []importedNode) string {
	parent := make(map[string]string, len(nodes))
	ids := make([]string, 0, len(nodes))
	for _, n := range nodes {
		parent[n.SrcID] = n.Parent
		ids = append(ids, n.SrcID)
	}
	best, depth := "", -1
	for _, id := range ids {
		d, ok := depthOf(id, parent)
		if !ok {
			continue
		}
		if d > depth {
			best, depth = id, d
		}
	}
	return best
}

// depthOf 从节点往上数到挂点；父指针有环时返回 false（不能靠它挑头，交给自检报错）。
func depthOf(id string, parent map[string]string) (int, bool) {
	seen := map[string]bool{}
	d := 0
	for id != "" {
		if seen[id] {
			return 0, false
		}
		seen[id] = true
		id = parent[id]
		d++
	}
	return d, true
}

// titleOrDefault 会话标题：源里有就用，没有就给个中性名字（不拿首条消息硬凑）。
func titleOrDefault(title string) string {
	if title == "" {
		return "导入的会话"
	}
	return title
}
