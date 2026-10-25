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

// checkInvariants 在 ValidateEveryChange 打开时做整树自检（op 用于错误溯源）。
// 生产构建下 ValidateEveryChange 为 false，此函数被内联成 no-op，无扫描开销。
func (c *Conversation) checkInvariants(op string) error {
	if !ValidateEveryChange {
		return nil
	}
	if err := c.Validate(); err != nil {
		return fmt.Errorf("%s: %w: %w", op, ErrInvalidNode, err)
	}
	return nil
}

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
//
// 树的三份结构（节点表、边表、版本链）一律**私有**：外部只能经方法读（View/Find/
// ChildIDs/Branches/Tail/NodeCount/SubtreeSize/Edition...）、经方法改（Append/Revise/
// Prune/Checkout/InsertBefore）。这是聚合根成立的底线——导出 map 等于把「只有方法能改」
// 的约束作废，任何一处 `c.Nodes[id] = {...}` 都能绕过不变量检查。
//
// 持久化走 Snapshot/Restore（snapshot.go）：适配器拿一份只读的整树导出，映射成自己的
// wire 格式；反过来用 Restore 重建并自检。领域不为「磁盘上长什么样」负责。
//
// 因此本类型**不是** wire 格式：它故意没有任何 JSON tag——直接 json.Marshal 它只会得到
// ID/Title/Head 与两个时间戳（三张结构表是私有的），落盘会静默丢掉整棵树。要存盘请走
// Snapshot，要读盘请走 Restore。
type Conversation struct {
	ID    ID
	Title string

	nodes       map[MessageID]Message     // 只增不改（Prune 硬删除外）
	children    map[MessageID][]MessageID // 结构边（Root 的孩子 = 顶层消息；树外键 "" 仅挂 Root）
	revisedFrom map[MessageID]MessageID   // 新→旧：版本链（纯结构元数据）

	// Head 初始 = Root ID；无空串特例（D19）。留作导出字段：它是单个游标而非结构表，
	// 移动语义仍走 Checkout（写 Head 而不更新 UpdatedAt 才会破坏一致性，那是加载路径的事）。
	Head MessageID

	CreatedAt time.Time
	UpdatedAt time.Time
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
		nodes:       map[MessageID]Message{root.ID: root},
		children:    map[MessageID][]MessageID{"": {root.ID}},
		Head:        root.ID,
		revisedFrom: map[MessageID]MessageID{},
		CreatedAt:   now,
		UpdatedAt:   now,
	}
}

// Append 追加一条 user|assistant 内容消息，父节点为当前 Head；成功后 Head 移到新节点。
// root/system/inject 一律走 AppendCommitted（Root 由 New 创建，system/inject 为组装节点）。
func (c *Conversation) Append(role Role, content []Part) (Message, error) {
	if role != RoleUser && role != RoleAssistant {
		return Message{}, fmt.Errorf("append: %w: 仅 user|assistant 可走 Append，root/system/inject 用 AppendCommitted", ErrInvalidNode)
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
	return c.nodes[m.ID], nil
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
	if _, exists := c.nodes[m.ID]; exists {
		return fmt.Errorf("append %q: %w: duplicate id", m.ID, ErrInvalidNode)
	}
	if m.Parent != "" {
		if _, ok := c.nodes[m.Parent]; !ok {
			return fmt.Errorf("append %q: %w: parent %q not in tree", m.ID, ErrInvalidNode, m.Parent)
		}
	}

	stored := m.Clone()
	c.nodes[stored.ID] = stored
	c.children[stored.Parent] = append(c.children[stored.Parent], stored.ID)
	c.Head = stored.ID
	c.UpdatedAt = nowFunc()
	if err := c.checkInvariants("append"); err != nil {
		return err
	}
	return nil
}

// Revise 创建 id 的同级新版本节点（同父、同角色，内容替换），并记录版本链 RevisedFrom 新→旧。
// root 不可 Revise（Root 即会话，D19）；system/inject 可以（persona/摘要/注入内容可改写，
// D150——Source/Trigger 随原节点）。
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
	old, ok := c.nodes[id]
	if !ok {
		return Message{}, fmt.Errorf("revise %q: %w", id, ErrNotFound)
	}
	if old.Role == RoleRoot {
		return Message{}, fmt.Errorf("revise %q: %w: 会话根节点就是会话本身，不可修订", id, ErrInvalidNode)
	}

	m := Message{
		ID:      NewMessageID(),
		Parent:  old.Parent,
		Role:    old.Role,
		Content: cloneParts(content),
		// Outcome 随原节点（D107 补）：修订 = 同一轮的改写版，终态语义随行——缺省零值
		// 会命中呈现层「非 done 终态标记」分支（修订消息顶部多出 `[]`）。
		// Model/Usage 不随：改写文本出自用户，不误标模型/用量。
		Outcome: old.Outcome,
		// Source/Trigger 随原节点（D150）：改一条注入只应换内容，不该丢掉来源与触发记录
		// ——否则呈现层与审计面会把改写后的注入写成「无来源注入」。
		// Trust 同理（D170）：改内容不是改信任级的操作，丢了会让宿主事实降级成外部数据。
		Source:    old.Source,
		Trust:     old.Trust,
		Trigger:   old.Trigger,
		CreatedAt: nowFunc(),
	}
	prevHead := c.Head
	if err := c.AppendCommitted(m); err != nil {
		return Message{}, err
	}
	c.revisedFrom[m.ID] = id

	// Head 判定必须在子树处置（转移/拷贝）之前：转移会改写链路，之后旧 Head 就不再可达 id。
	headInSubtree := c.isStrictDescendant(id, prevHead)
	switch mode {
	case Carry:
		if moved := c.children[id]; len(moved) > 0 {
			c.children[m.ID] = moved // 边转移：整批边零拷贝改挂
			delete(c.children, id)
			for _, ch := range moved { // 仅直接孩子的 Parent 边指针改写（D16）
				cm := c.nodes[ch]
				cm.Parent = m.ID
				c.nodes[ch] = cm
			}
		}
		if headInSubtree {
			c.Head = prevHead // 历史随行，Head 保持在被转移子树内
			c.UpdatedAt = nowFunc()
			if err := c.checkInvariants("revise carry"); err != nil {
				return Message{}, err
			}
			return c.nodes[m.ID], nil
		}
	case Clone:
		mapping := c.cloneSubtree(id, m.ID) // 深拷贝子树：全新 ID、原子树字节级不动（D96）
		if headInSubtree {
			if mapped, ok := mapping[prevHead]; ok {
				c.Head = mapped // 历史随行，Head 平移到拷贝对应节点
				c.UpdatedAt = nowFunc()
				if err := c.checkInvariants("revise clone"); err != nil {
					return Message{}, err
				}
				return c.nodes[m.ID], nil
			}
		}
	}
	c.Head = m.ID
	c.UpdatedAt = nowFunc()
	if err := c.checkInvariants("revise"); err != nil {
		return Message{}, err
	}
	return c.nodes[m.ID], nil
}

// cloneSubtree 把 id 的整棵子树深拷贝到 parent 之下（不含 id 自身——新节点即其"新版本"）：
// 拷贝节点 ID 全新、内容经 Clone 深拷、CreatedAt 保留原值（保真历史时刻，D96）；
// 返回 原ID → 拷贝ID 映射。
func (c *Conversation) cloneSubtree(id, parent MessageID) map[MessageID]MessageID {
	mapping := map[MessageID]MessageID{}
	var walk func(src, dstParent MessageID)
	walk = func(src, dstParent MessageID) {
		cm := c.nodes[src].Clone()
		cm.ID = NewMessageID()
		cm.Parent = dstParent
		c.nodes[cm.ID] = cm
		c.children[dstParent] = append(c.children[dstParent], cm.ID)
		mapping[src] = cm.ID
		for _, ch := range c.children[src] {
			walk(ch, cm.ID)
		}
	}
	for _, ch := range c.children[id] {
		walk(ch, parent)
	}
	return mapping
}

// Prune 剪掉 id 及整棵子树（唯一破坏性操作，硬删；Root 不可剪——Root 即会话，D19；
// storejson 写前留一代 .bak 兜底，D7）。
// Head 落在被移除子树内时，回退到最近存活祖先（Root 不可删，链必止于 Root）。
// D95 后无跨节点引用，不存在失联节点——剪除范围即子树本身。
func (c *Conversation) Prune(id MessageID) error {
	m, ok := c.nodes[id]
	if !ok {
		return fmt.Errorf("prune %q: %w", id, ErrNotFound)
	}
	if m.Role == RoleRoot {
		return fmt.Errorf("prune %q: %w: 会话根节点就是会话本身，不可删除", id, ErrInvalidNode)
	}
	sub := map[MessageID]bool{}
	c.collectSubtree(id, sub)

	if sub[c.Head] {
		cur := c.nodes[c.Head].Parent
		for sub[cur] {
			cur = c.nodes[cur].Parent
		}
		c.Head = cur
	}

	for nid := range sub {
		delete(c.nodes, nid)
		delete(c.children, nid)
		delete(c.revisedFrom, nid)
	}
	for newID, oldID := range c.revisedFrom {
		if sub[oldID] {
			delete(c.revisedFrom, newID)
		}
	}
	for pid, kids := range c.children {
		kept := kids[:0]
		for _, k := range kids {
			if !sub[k] {
				kept = append(kept, k)
			}
		}
		if len(kept) == 0 {
			delete(c.children, pid)
		} else {
			c.children[pid] = kept
		}
	}
	c.UpdatedAt = nowFunc()
	if err := c.checkInvariants("prune"); err != nil {
		return err
	}
	return nil
}

// Checkout 将 Head 移到任意节点（分支导航；含 Root = 回根）。
// 无空串特例（D19）：id 不在树中（含 ""）一律 ErrNotFound。
func (c *Conversation) Checkout(id MessageID) error {
	if _, ok := c.nodes[id]; !ok {
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
	if _, ok := c.nodes[id]; !ok {
		return "", false
	}
	if len(c.children[id]) == 0 {
		return id, true
	}
	var best MessageID
	var bestAt time.Time
	var walk func(MessageID)
	walk = func(x MessageID) {
		kids := c.children[x]
		if len(kids) == 0 {
			n := c.nodes[x]
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
		m, ok := c.nodes[cur]
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

// Branches 返回 id 所在版本组的其他成员（不含 id 自身），按创建时间升序；供 UI 做新旧
// 版本对比、命令行 `/branch` 列同级分叉。Root 的分叉 = 其孩子（顶层消息）；id 不在树中、
// 或 id 本身是衬垫（它不属于任何版本组）返回 nil。
//
// 版本组跨过衬垫（padNode）：在 a1 之前插了摘要之后，a1 的结构父变成了衬垫，但它与 a2
// 仍然是同一个问题的两个版本——都还在这一组里。
func (c *Conversation) Branches(id MessageID) []Message {
	m, ok := c.nodes[id]
	if !ok {
		return nil
	}
	if m.Role == RoleRoot {
		out := make([]Message, 0, len(c.children[id]))
		for _, nid := range c.versionGroup(id) {
			if m, ok := c.nodes[nid]; ok {
				out = append(out, m)
			}
		}
		return out
	}
	if c.padNode(id) {
		return nil
	}
	var out []Message
	for _, nid := range c.versionGroup(c.versionOwner(id)) {
		if nid == id {
			continue
		}
		if m, ok := c.nodes[nid]; ok {
			out = append(out, m)
		}
	}
	sort.Slice(out, func(i, j int) bool { return nodeLess(out[i], out[j]) })
	return out
}

// Find 查找节点。
func (c *Conversation) Find(id MessageID) (Message, bool) {
	m, ok := c.nodes[id]
	return m, ok
}

// collectSubtree 把 id 及整棵子树（含 id）收集进 out。
func (c *Conversation) collectSubtree(id MessageID, out map[MessageID]bool) {
	if out[id] {
		return
	}
	out[id] = true
	for _, ch := range c.children[id] {
		c.collectSubtree(ch, out)
	}
}

// isStrictDescendant 判断 node 是否为 id 的严格后代（至少隔一代）。
func (c *Conversation) isStrictDescendant(id, node MessageID) bool {
	for cur := node; cur != ""; {
		m, ok := c.nodes[cur]
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
