package conversation

// insert.go 衬垫节点插入（D152，DESIGN §4.1）：在某个节点**之前**插一个 system|inject 节点
// 并保持后续历史随行——用于「插入摘要」（system 摘要成为新的压缩水位，其上历史不再回传）
// 与其他只追加的上下文片段。
//
// 语义 = 单条边转移：新节点 N 挂在目标 T 的原父 P 下并**占据 T 在 Children[P] 中的位置**
// （兄弟顺序即图上左→右，D152），T 改挂到 N 下，T 的子树随之整体后移一位、零拷贝；
// Head 不变（T 及其子树仍可达，路径长度 +1），P 的其他孩子不动。
//
// 水位只认 system（D21）：inject 衬垫照样进上下文（投影成带围栏的 user 段），但不成为
// 压缩水位、也不切断其上历史——只有 system 衬垫才是「从这里往后才回传」的分界线。
//
// 为什么不用 Revise(Carry)：Revise 造的是 T 的**同级兄弟**并把 T 的子树转给新节点——结果是
// 「摘要替换了当前节点」，T 自己被留成旧版本叶子；这里要的是「插在前面、T 仍在摘要之后」。
//
// 两个入口：InsertBefore 只给内容（手写摘要）；InsertCommitted 接受**已组装**的衬垫节点
// （模型生成的摘要要记 Model/Usage 记账，与 AppendCommitted 对已组装节点的口径一致）。

import "fmt"

// insertablePadRole 衬垫节点可用的角色（D152/D150）：衬垫是上下文/水位，不是伪造的发言，
// 故只放 system（水位）与 inject（只追加的上下文）。D152 先把闸门按 system 收，注入节点
// 落地（D150）后在此一并放开。
func insertablePadRole(role Role) bool { return role == RoleSystem || role == RoleInject }

// InsertBefore 在 id 之前插入一个衬垫节点（D152）：内容由调用方给，ID/父/时间戳由领域定。
func (c *Conversation) InsertBefore(id MessageID, role Role, parts []Part) (Message, error) {
	return c.InsertCommitted(id, Message{Role: role, Content: cloneParts(parts)})
}

// InsertCommitted 在 id 之前插入一个已组装好的衬垫节点（D152）。
//   - pad.Role 必须是 system|inject（insertablePadRole）；pad.ID 为空则由领域分配，非空则沿用
//     （模型生成的摘要要先用该 ID 关联流式事件，再随节点入树）且不得与树中节点撞车；
//   - pad.Parent 不作数（位置由 id 决定，恒为 id 的原父），pad.CreatedAt 为空则取当前时刻；
//   - id 必须是树中节点且不是 Root（根前无处可插）；校验不过一律 ErrInvalidNode/ErrNotFound，
//     树保持原样（失败不落半截变更）。
//   - 成功后 Head 不变、RevisedFrom 不变、UpdatedAt 刷新；返回入树存本。
func (c *Conversation) InsertCommitted(id MessageID, pad Message) (Message, error) {
	target, ok := c.nodes[id]
	if !ok {
		return Message{}, fmt.Errorf("insert before %q: %w", id, ErrNotFound)
	}
	if target.Role == RoleRoot {
		return Message{}, fmt.Errorf("insert before %q: %w: 会话根节点之前无处可插", id, ErrInvalidNode)
	}
	if !insertablePadRole(pad.Role) {
		return Message{}, fmt.Errorf("insert before %q: %w: 衬垫节点角色仅限 system|inject（实为 %q）",
			id, ErrInvalidNode, pad.Role)
	}
	if len(pad.Content) == 0 {
		return Message{}, fmt.Errorf("insert before %q: %w: 衬垫节点没有内容", id, ErrInvalidNode)
	}
	parent := target.Parent
	pad.Parent = parent // 位置由目标决定，调用方给的 Parent 不作数
	if pad.ID == "" {
		pad.ID = NewMessageID()
	}
	if _, dup := c.nodes[pad.ID]; dup {
		return Message{}, fmt.Errorf("insert before %q: %w: duplicate id %q", id, ErrInvalidNode, pad.ID)
	}
	if pad.CreatedAt.IsZero() {
		pad.CreatedAt = nowFunc()
	}
	if err := checkNodeShape(pad); err != nil {
		return Message{}, err
	}

	// 占据 T 在 Children[P] 中的原位置（原地改写切片，兄弟顺序保真）。
	kids := c.children[parent]
	idx := -1
	for i, k := range kids {
		if k == id {
			idx = i
			break
		}
	}
	if idx < 0 {
		return Message{}, fmt.Errorf("insert before %q: %w: 目标节点在父节点 %q 下没有边",
			id, ErrInvalidNode, parent)
	}
	stored := pad.Clone()
	c.nodes[stored.ID] = stored
	kids[idx] = stored.ID
	c.children[stored.ID] = []MessageID{id}
	moved := c.nodes[id]
	moved.Parent = stored.ID // 单条边转移：子树随行、零拷贝
	c.nodes[id] = moved
	c.UpdatedAt = nowFunc()
	if err := c.checkInvariants("insert before"); err != nil {
		return Message{}, err
	}
	return c.nodes[stored.ID], nil
}
