// Package conversation 实现 Aquarius 的核心领域：一棵不可变消息树 + 一个 Head 游标。
//
// 核心语义（DESIGN §4.1）：
//   - 节点创建后内容只读；用户"修改"永远走 Revise（创建同级新节点），绝不就地改写。
//   - Revise Carry = 边转移：子树零拷贝改挂到新节点（D2/D16）。
//   - 虚拟 Root 是唯一根：Parent == "" 的顶层消息可多条；Head == "" 表示游标在虚拟 Root（D15）。
//   - 流式中间态不进领域：Turn 结束一次性 AppendCommitted 提交不可变节点（D3）。
package conversation

import (
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/Tonyjh07/Aquarius/internal/domain/tool"
)

// 哨兵错误。
var (
	// ErrNotFound 目标消息不在树中。
	ErrNotFound = errors.New("conversation: message not found")
	// ErrInvalidNode 节点不满足入树条件：空 ID、重复 ID、角色非法、父不存在、引用不合法等。
	ErrInvalidNode = errors.New("conversation: invalid node")
	// ErrReviseTool tool 结果节点不可 Revise（内容由模型调用产生，不可编辑，D18）。
	ErrReviseTool = errors.New("conversation: tool message cannot be revised")
)

// nowFunc 取当前时间；测试可替换以获得确定性输出。
var nowFunc = time.Now

// KeepMode Revise 的历史保留模式。
type KeepMode int

const (
	// Fresh 新节点空白开始；旧节点的子树原样留作历史分支。
	Fresh KeepMode = iota
	// Carry 旧节点的子树边转移到新节点；旧节点成为"旧版本"叶子。
	Carry
)

// String 实现 fmt.Stringer。
func (k KeepMode) String() string {
	if k == Carry {
		return "carry"
	}
	return "fresh"
}

// Conversation 一棵不可变消息树 + 一个 Head 游标。
//
// 一切变化 = 新增节点 / 增删边 / 边转移 / 移动 Head；单会话内操作串行（Head 唯一）。
type Conversation struct {
	ID          ID                        `json:"id"`
	Title       string                    `json:"title"`
	Nodes       map[MessageID]Message     `json:"nodes"`        // 只增不改（Prune 硬删除外）
	Children    map[MessageID][]MessageID `json:"children"`     // 结构边（虚拟 Root 的孩子键为 ""）
	Head        MessageID                 `json:"head"`         // "" = 虚拟 Root（空路径）
	RevisedFrom map[MessageID]MessageID   `json:"revised_from"` // 新→旧：版本链（纯结构元数据）
	CreatedAt   time.Time                 `json:"created_at"`
	UpdatedAt   time.Time                 `json:"updated_at"`
}

// New 创建空会话（Head 位于虚拟 Root）。
func New(id ID, title string) *Conversation {
	now := nowFunc()
	return &Conversation{
		ID:          id,
		Title:       title,
		Nodes:       map[MessageID]Message{},
		Children:    map[MessageID][]MessageID{},
		RevisedFrom: map[MessageID]MessageID{},
		CreatedAt:   now,
		UpdatedAt:   now,
	}
}

// Append 追加一条 user|assistant 内容消息，父节点为当前 Head；成功后 Head 移到新节点。
// tool 消息必须携带 ToolResult，请改用 AppendCommitted 提交。
func (c *Conversation) Append(role Role, content []Part) (Message, error) {
	if role == RoleTool {
		return Message{}, fmt.Errorf("append: %w: tool 消息须走 AppendCommitted", ErrInvalidNode)
	}
	m := Message{
		ID:        NewMessageID(),
		Parent:    c.Head,
		Role:      role,
		Content:   cloneParts(content),
		CreatedAt: nowFunc(),
	}
	if err := c.AppendCommitted(m); err != nil {
		return Message{}, err
	}
	return c.Nodes[m.ID], nil
}

// AppendCommitted 提交一个已组装好的不可变节点（Turn 结束一次性 Commit 的入口，含 tool 消息）。
// Parent 取 m.Parent（"" = 顶层消息）；入树前校验不变量，并对切片/指针做防御性拷贝；
// 成功后 Head 移到新节点。
func (c *Conversation) AppendCommitted(m Message) error {
	if err := checkNodeShape(m); err != nil {
		return err
	}
	if _, exists := c.Nodes[m.ID]; exists {
		return fmt.Errorf("append %q: %w: duplicate id", m.ID, ErrInvalidNode)
	}
	if m.Parent != "" {
		if _, ok := c.Nodes[m.Parent]; !ok {
			return fmt.Errorf("append %q: %w: parent %q not in tree", m.ID, ErrInvalidNode, m.Parent)
		}
	}
	for _, call := range m.ToolCalls {
		if c.hasCallID(call.ID) {
			return fmt.Errorf("append %q: %w: duplicate call id %q", m.ID, ErrInvalidNode, call.ID)
		}
	}
	// 不变量 2：存在性引用——tool 结果的 CallID 须匹配树中存在的某 assistant 调用（D12）。
	if m.Role == RoleTool && !c.hasCallID(m.ToolResult.CallID) {
		return fmt.Errorf("append %q: %w: tool result references unknown call id %q",
			m.ID, ErrInvalidNode, m.ToolResult.CallID)
	}

	stored := m.Clone()
	c.Nodes[stored.ID] = stored
	c.Children[stored.Parent] = append(c.Children[stored.Parent], stored.ID)
	c.Head = stored.ID
	c.UpdatedAt = nowFunc()
	return nil
}

// Revise 创建 id 的同级新版本节点（同父、同角色，内容替换），并记录版本链 RevisedFrom 新→旧。
// tool 节点不可 Revise（D18）。id 为顶层消息时新节点同为顶层消息（D15）。
//
// Head 语义（D16）：
//   - Fresh：Head 移到新节点，旧子树原样留作历史分支；
//   - Carry：id 的子树边转移到新节点；旧 Head 若是 id 的严格后代（随子树转移）则保持不变，
//     否则移到新节点。
func (c *Conversation) Revise(id MessageID, content []Part, mode KeepMode) (Message, error) {
	old, ok := c.Nodes[id]
	if !ok {
		return Message{}, fmt.Errorf("revise %q: %w", id, ErrNotFound)
	}
	if old.Role == RoleTool {
		return Message{}, fmt.Errorf("revise %q: %w", id, ErrReviseTool)
	}

	m := Message{
		ID:        NewMessageID(),
		Parent:    old.Parent,
		Role:      old.Role,
		Content:   cloneParts(content),
		CreatedAt: nowFunc(),
	}
	prevHead := c.Head
	if err := c.AppendCommitted(m); err != nil {
		return Message{}, err
	}
	c.RevisedFrom[m.ID] = id

	// Head 判定必须在边转移之前：转移会改写链路，之后旧 Head 就不再可达 id。
	headInSubtree := c.isStrictDescendant(id, prevHead)
	if mode == Carry {
		if moved := c.Children[id]; len(moved) > 0 {
			c.Children[m.ID] = moved // 边转移：整批边零拷贝改挂
			delete(c.Children, id)
			for _, ch := range moved { // 仅直接孩子的 Parent 边指针改写（D16）
				cm := c.Nodes[ch]
				cm.Parent = m.ID
				c.Nodes[ch] = cm
			}
		}
		if headInSubtree {
			c.Head = prevHead // 历史随行，Head 保持在被转移子树内
			c.UpdatedAt = nowFunc()
			return c.Nodes[m.ID], nil
		}
	}
	c.Head = m.ID
	c.UpdatedAt = nowFunc()
	return c.Nodes[m.ID], nil
}

// Prune 剪掉 id 及整棵子树（唯一破坏性操作，硬删；storejson 写前留一代 .bak 兜底，D7）。
//
// 连带处理（D18）：因引用的 assistant 节点被剪而失联的 tool 结果节点一并移除——
// 只删该节点本身，其子树改挂到最近存活祖先（历史不丢），从而维持存在性引用不变量。
// Head 落在被移除节点上时，回退到最近存活祖先（可为虚拟 Root）。
func (c *Conversation) Prune(id MessageID) error {
	if _, ok := c.Nodes[id]; !ok {
		return fmt.Errorf("prune %q: %w", id, ErrNotFound)
	}
	sub := map[MessageID]bool{}
	c.collectSubtree(id, sub)

	// 失联 tool 结果节点（在被剪子树外的）逐轮收敛。
	drop := map[MessageID]bool{}
	for {
		changed := false
		for nid, m := range c.Nodes {
			if sub[nid] || drop[nid] || m.Role != RoleTool {
				continue
			}
			if !c.hasCallID(m.ToolResult.CallID, sub, drop) {
				drop[nid] = true
				changed = true
			}
		}
		if !changed {
			break
		}
	}

	survives := func(n MessageID) bool { return n == "" || (!sub[n] && !drop[n]) }
	nearestSurvivingAncestor := func(n MessageID) MessageID {
		cur := c.Nodes[n].Parent
		for !survives(cur) {
			cur = c.Nodes[cur].Parent
		}
		return cur
	}
	headMoved := false
	head := c.Head
	if !survives(head) {
		head = nearestSurvivingAncestor(head)
		headMoved = true
	}
	// drop 节点的存活孩子改挂到最近存活祖先（子树零拷贝，仅直接孩子的 Parent 边指针改写）。
	for nid := range drop {
		p := nearestSurvivingAncestor(nid)
		for _, ch := range c.Children[nid] {
			if !survives(ch) {
				continue
			}
			cm := c.Nodes[ch]
			cm.Parent = p
			c.Nodes[ch] = cm
			c.Children[p] = append(c.Children[p], ch)
		}
	}

	removed := func(n MessageID) bool { return sub[n] || drop[n] }
	for nid := range sub {
		delete(c.Nodes, nid)
		delete(c.Children, nid)
		delete(c.RevisedFrom, nid)
	}
	for nid := range drop {
		delete(c.Nodes, nid)
		delete(c.Children, nid)
		delete(c.RevisedFrom, nid)
	}
	for newID, oldID := range c.RevisedFrom {
		if removed(oldID) {
			delete(c.RevisedFrom, newID)
		}
	}
	for pid, kids := range c.Children {
		kept := kids[:0]
		for _, k := range kids {
			if !removed(k) {
				kept = append(kept, k)
			}
		}
		if len(kept) == 0 {
			delete(c.Children, pid)
		} else {
			c.Children[pid] = kept
		}
	}
	if headMoved {
		c.Head = head
	}
	c.UpdatedAt = nowFunc()
	return nil
}

// Checkout 将 Head 移到任意节点（分支导航）；id 为 "" 时回到虚拟 Root（空路径）。
func (c *Conversation) Checkout(id MessageID) error {
	if id != "" {
		if _, ok := c.Nodes[id]; !ok {
			return fmt.Errorf("checkout %q: %w", id, ErrNotFound)
		}
	}
	c.Head = id
	c.UpdatedAt = nowFunc()
	return nil
}

// Path 返回 Root→Head 的线性节点序列 = 本次推理的上下文（早→近）。
func (c *Conversation) Path() []Message {
	var rev []Message
	for cur := c.Head; cur != ""; {
		m, ok := c.Nodes[cur]
		if !ok {
			break
		}
		rev = append(rev, m)
		cur = m.Parent
	}
	out := make([]Message, 0, len(rev))
	for i := len(rev) - 1; i >= 0; i-- {
		out = append(out, rev[i])
	}
	return out
}

// Branches 返回 id 的同级分叉（同一父节点下的其他孩子，不含 id 自身），按创建时间升序；
// 供 UI 做新旧版本对比。id 为 "" 时返回全部顶层消息；id 不在树中返回 nil。
func (c *Conversation) Branches(id MessageID) []Message {
	self := id
	if id != "" {
		m, ok := c.Nodes[id]
		if !ok {
			return nil
		}
		id = m.Parent // 同级 = 与 id 同父的孩子
	}
	var out []Message
	for _, nid := range c.Children[id] {
		if nid == self {
			continue
		}
		if m, ok := c.Nodes[nid]; ok {
			out = append(out, m)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].CreatedAt.Before(out[j].CreatedAt)
		}
		return out[i].ID < out[j].ID
	})
	return out
}

// Find 查找节点。
func (c *Conversation) Find(id MessageID) (Message, bool) {
	m, ok := c.Nodes[id]
	return m, ok
}

// collectSubtree 把 id 及整棵子树（含 id）收集进 out。
func (c *Conversation) collectSubtree(id MessageID, out map[MessageID]bool) {
	if out[id] {
		return
	}
	out[id] = true
	for _, ch := range c.Children[id] {
		c.collectSubtree(ch, out)
	}
}

// isStrictDescendant 判断 node 是否为 id 的严格后代（至少隔一代）。
func (c *Conversation) isStrictDescendant(id, node MessageID) bool {
	for cur := node; cur != ""; {
		m, ok := c.Nodes[cur]
		if !ok {
			return false
		}
		cur = m.Parent
		if cur == id {
			return true
		}
	}
	return false
}

// hasCallID 判断树中是否存在 assistant 节点声明了该调用 ID；skip 中的节点视作不存在。
func (c *Conversation) hasCallID(id tool.CallID, skip ...map[MessageID]bool) bool {
	if id == "" {
		return false
	}
	for nid, m := range c.Nodes {
		if m.Role != RoleAssistant {
			continue
		}
		gone := false
		for _, s := range skip {
			if s[nid] {
				gone = true
				break
			}
		}
		if gone {
			continue
		}
		for _, call := range m.ToolCalls {
			if call.ID == id {
				return true
			}
		}
	}
	return false
}
