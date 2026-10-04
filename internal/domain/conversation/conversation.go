// Package conversation 实现 Aquarius 的核心领域：一棵不可变消息树 + 一个 Head 游标。
//
// 核心语义（DESIGN §4.1）：
//   - 节点创建后内容只读；用户"修改"永远走 Revise（创建同级新节点），绝不就地改写。
//   - Revise Carry = 边转移：子树零拷贝改挂到新节点（D2/D16）；Clone = 深拷贝子树分叉（D96）。
//   - Root 实节点是唯一根（D19 修订 D15 载体）：ID = 会话 ID、Role = root、空内容；
//     顶层消息 = Root 的孩子（可多条，支撑 Revise 首条消息）；Head 初始 = Root，无空串特例。
//   - system 节点承载 persona（D20）与压缩摘要（D21）；root/system 组装节点一律走 AppendCommitted。
//   - 工具调用与结果内嵌为 assistant 节点的 PartTool 分片（D95），无跨节点引用。
//   - 流式中间态不进领域：Turn 结束一次性 AppendCommitted 提交不可变节点（D3）。
package conversation

import (
	"errors"
	"fmt"
	"sort"
	"time"
)

// 哨兵错误。
var (
	// ErrNotFound 目标消息不在树中。
	ErrNotFound = errors.New("conversation: message not found")
	// ErrInvalidNode 节点不满足入树条件：空 ID、重复 ID、角色非法、父不存在等。
	ErrInvalidNode = errors.New("conversation: invalid node")
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
	// Clone 旧节点的子树深拷贝为新分支（全新 ID、原子树字节级不动，D96）；
	// 与 Fresh/Carry 并列的显式模式——"复制整段历史另试走向"，数据翻倍由选用者承担。
	Clone
)

// String 实现 fmt.Stringer。
func (k KeepMode) String() string {
	switch k {
	case Carry:
		return "carry"
	case Clone:
		return "clone"
	default:
		return "fresh"
	}
}

// Conversation 一棵不可变消息树 + 一个 Head 游标。
//
// 一切变化 = 新增节点 / 增删边 / 边转移 / 移动 Head；单会话内操作串行（Head 唯一）。
type Conversation struct {
	ID          ID                        `json:"id"`
	Title       string                    `json:"title"`
	Nodes       map[MessageID]Message     `json:"nodes"`        // 只增不改（Prune 硬删除外）
	Children    map[MessageID][]MessageID `json:"children"`     // 结构边（Root 的孩子 = 顶层消息；树外键 "" 仅挂 Root）
	Head        MessageID                 `json:"head"`         // 初始 = Root ID；无空串特例（D19）
	RevisedFrom map[MessageID]MessageID   `json:"revised_from"` // 新→旧：版本链（纯结构元数据）
	CreatedAt   time.Time                 `json:"created_at"`
	UpdatedAt   time.Time                 `json:"updated_at"`
}

// New 创建空会话：Root 空节点（ID = id、Role = root）入树，Children[""] = [Root]，Head 指向 Root。
// persona 首节点由应用层写入（D20）。
func New(id ID, title string) *Conversation {
	now := nowFunc()
	root := Message{
		ID:        MessageID(id),
		Role:      RoleRoot,
		CreatedAt: now,
	}
	return &Conversation{
		ID:          id,
		Title:       title,
		Nodes:       map[MessageID]Message{root.ID: root},
		Children:    map[MessageID][]MessageID{"": {root.ID}},
		Head:        root.ID,
		RevisedFrom: map[MessageID]MessageID{},
		CreatedAt:   now,
		UpdatedAt:   now,
	}
}

// Append 追加一条 user|assistant 内容消息，父节点为当前 Head；成功后 Head 移到新节点。
// root/system 一律走 AppendCommitted（Root 由 New 创建，system 为组装节点）。
func (c *Conversation) Append(role Role, content []Part) (Message, error) {
	if role != RoleUser && role != RoleAssistant {
		return Message{}, fmt.Errorf("append: %w: 仅 user|assistant 可走 Append，root/system 用 AppendCommitted", ErrInvalidNode)
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

// AppendCommitted 提交一个已组装好的不可变节点（system 等；Turn 结束一次性 Commit 的入口）。
// Root 不可提交（由 New 创建，D19）；Parent 必须非空且存在；入树前校验不变量，
// 并对切片/指针做防御性拷贝；成功后 Head 移到新节点。
// D95 后无跨节点引用检查：工具调用与结果同片，合法性由 checkNodeShape 把守（CallID 节点内唯一）。
func (c *Conversation) AppendCommitted(m Message) error {
	if m.Role == RoleRoot {
		return fmt.Errorf("append %q: %w: root 由 New 创建，不可提交", m.ID, ErrInvalidNode)
	}
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

	stored := m.Clone()
	c.Nodes[stored.ID] = stored
	c.Children[stored.Parent] = append(c.Children[stored.Parent], stored.ID)
	c.Head = stored.ID
	c.UpdatedAt = nowFunc()
	return nil
}

// Revise 创建 id 的同级新版本节点（同父、同角色，内容替换），并记录版本链 RevisedFrom 新→旧。
// root 不可 Revise（Root 即会话，D19）；system 可以（persona/摘要可改写）。
// id 为顶层消息时新节点同为顶层（挂 Root 下，D15 语义）。
// 内容整体替换：旧节点的工具分片随旧版本保留，新版本不含（修订即改写该版本，D95）。
//
// Head 语义（D16/D96）：
//   - Fresh：Head 移到新节点，旧子树原样留作历史分支；
//   - Carry：id 的子树边转移到新节点；旧 Head 若是 id 的严格后代（随子树转移）则保持不变，
//     否则移到新节点；
//   - Clone：id 的子树深拷贝到新节点下（全新 ID，原子树不动）；旧 Head 若在原子树内
//     则平移到拷贝对应节点，否则移到新节点。
func (c *Conversation) Revise(id MessageID, content []Part, mode KeepMode) (Message, error) {
	old, ok := c.Nodes[id]
	if !ok {
		return Message{}, fmt.Errorf("revise %q: %w", id, ErrNotFound)
	}
	if old.Role == RoleRoot {
		return Message{}, fmt.Errorf("revise %q: %w: root 即会话，不可修订", id, ErrInvalidNode)
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

	// Head 判定必须在子树处置（转移/拷贝）之前：转移会改写链路，之后旧 Head 就不再可达 id。
	headInSubtree := c.isStrictDescendant(id, prevHead)
	switch mode {
	case Carry:
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
	case Clone:
		mapping := c.cloneSubtree(id, m.ID) // 深拷贝子树：全新 ID、原子树字节级不动（D96）
		if headInSubtree {
			if mapped, ok := mapping[prevHead]; ok {
				c.Head = mapped // 历史随行，Head 平移到拷贝对应节点
				c.UpdatedAt = nowFunc()
				return c.Nodes[m.ID], nil
			}
		}
	}
	c.Head = m.ID
	c.UpdatedAt = nowFunc()
	return c.Nodes[m.ID], nil
}

// cloneSubtree 把 id 的整棵子树深拷贝到 parent 之下（不含 id 自身——新节点即其"新版本"）：
// 拷贝节点 ID 全新、内容经 Clone 深拷、CreatedAt 保留原值（保真历史时刻，D96）；
// 返回 原ID → 拷贝ID 映射。
func (c *Conversation) cloneSubtree(id, parent MessageID) map[MessageID]MessageID {
	mapping := map[MessageID]MessageID{}
	var walk func(src, dstParent MessageID)
	walk = func(src, dstParent MessageID) {
		cm := c.Nodes[src].Clone()
		cm.ID = NewMessageID()
		cm.Parent = dstParent
		c.Nodes[cm.ID] = cm
		c.Children[dstParent] = append(c.Children[dstParent], cm.ID)
		mapping[src] = cm.ID
		for _, ch := range c.Children[src] {
			walk(ch, cm.ID)
		}
	}
	for _, ch := range c.Children[id] {
		walk(ch, parent)
	}
	return mapping
}

// Prune 剪掉 id 及整棵子树（唯一破坏性操作，硬删；Root 不可剪——Root 即会话，D19；
// storejson 写前留一代 .bak 兜底，D7）。
// Head 落在被移除子树内时，回退到最近存活祖先（Root 不可删，链必止于 Root）。
// D95 后无跨节点引用，不存在失联节点——剪除范围即子树本身。
func (c *Conversation) Prune(id MessageID) error {
	m, ok := c.Nodes[id]
	if !ok {
		return fmt.Errorf("prune %q: %w", id, ErrNotFound)
	}
	if m.Role == RoleRoot {
		return fmt.Errorf("prune %q: %w: root 即会话，不可删除", id, ErrInvalidNode)
	}
	sub := map[MessageID]bool{}
	c.collectSubtree(id, sub)

	if sub[c.Head] {
		cur := c.Nodes[c.Head].Parent
		for sub[cur] {
			cur = c.Nodes[cur].Parent
		}
		c.Head = cur
	}

	for nid := range sub {
		delete(c.Nodes, nid)
		delete(c.Children, nid)
		delete(c.RevisedFrom, nid)
	}
	for newID, oldID := range c.RevisedFrom {
		if sub[oldID] {
			delete(c.RevisedFrom, newID)
		}
	}
	for pid, kids := range c.Children {
		kept := kids[:0]
		for _, k := range kids {
			if !sub[k] {
				kept = append(kept, k)
			}
		}
		if len(kept) == 0 {
			delete(c.Children, pid)
		} else {
			c.Children[pid] = kept
		}
	}
	c.UpdatedAt = nowFunc()
	return nil
}

// Checkout 将 Head 移到任意节点（分支导航；含 Root = 回根）。
// 无空串特例（D19）：id 不在树中（含 ""）一律 ErrNotFound。
func (c *Conversation) Checkout(id MessageID) error {
	if _, ok := c.Nodes[id]; !ok {
		return fmt.Errorf("checkout %q: %w", id, ErrNotFound)
	}
	c.Head = id
	c.UpdatedAt = nowFunc()
	return nil
}

// Tail 返回 id 版本子树的对话末端（D94 分支切换落点）：id 自身是叶子（无孩子）时即
// 自身；否则取子树全部叶子中 CreatedAt 最新者（平局取 ID 序最大，保证确定性）。
// 切换消息版本应恢复该版本的对话全程——Head 停在消息节点上时其回答不在 Head 路径上。
// id 不在树中 → ok=false。
func (c *Conversation) Tail(id MessageID) (MessageID, bool) {
	if _, ok := c.Nodes[id]; !ok {
		return "", false
	}
	if len(c.Children[id]) == 0 {
		return id, true
	}
	var best MessageID
	var bestAt time.Time
	var walk func(MessageID)
	walk = func(x MessageID) {
		kids := c.Children[x]
		if len(kids) == 0 {
			n := c.Nodes[x]
			if best == "" || n.CreatedAt.After(bestAt) || (n.CreatedAt.Equal(bestAt) && n.ID > best) {
				best, bestAt = x, n.CreatedAt
			}
			return
		}
		for _, kid := range kids {
			walk(kid)
		}
	}
	walk(id)
	return best, true
}

// Path 返回 Root→Head 的线性节点序列 = 本次推理的上下文（早→近）；首元素恒为 Root（装配时滤掉）。
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
// 供 UI 做新旧版本对比。Root 的分叉 = 其孩子（顶层消息）；id 不在树中返回 nil。
func (c *Conversation) Branches(id MessageID) []Message {
	m, ok := c.Nodes[id]
	if !ok {
		return nil
	}
	parent := m.Parent
	if m.Role == RoleRoot {
		parent = id // Root 的"同级"取其孩子：顶层消息（D15 语义）
	}
	var out []Message
	for _, nid := range c.Children[parent] {
		if nid == id {
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
