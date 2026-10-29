package conversation

import (
	"fmt"
	"sort"
	"time"
)

// snapshot.go 持久化与重建契约（§4.1/§5.3，D214）。
//
// 为什么领域要出这份 API：三张结构表私有之后，适配器不能再自己拼一棵树。但「把整棵树
// 倒出来再装回去」是真实需求（storejson 落盘、跨进程迁移、测试造树），所以给它一个
// **受控**的出口，而不是重新导出 map：
//
//   - Snapshot 返回只读数据（切片 + 副本），调用方拿去做自己的 wire 格式；
//   - Restore 是唯一的重建入口，进来先过一遍不变量自检——磁盘上的数据同样是不可信输入。
//
// 领域不为「磁盘上长什么样」负责：适配器定义自己的 DTO，字段名、兼容策略、旧格式识别
// 都在适配器里。这里只保证「这棵树」被完整搬出去、原样搬回来。

// Snapshot 会话的完整只读快照（导出用）。
type Snapshot struct {
	ID    ID
	Title string
	Head  MessageID
	// Nodes 全部节点，按 ID 升序（确定性，便于 diff 与人工核对）。
	Nodes []Message
	// Edges 结构边，按父 ID 升序；每个父的孩子保持结构插入序（兄弟顺序即图上左→右，D152）。
	Edges []SnapshotEdge
	// RevisedFrom 版本链，按新节点 ID 升序。
	RevisedFrom []SnapshotEdition
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// SnapshotEdge 一个父节点与它的孩子（父 "" 即结构根挂点，D19）。
type SnapshotEdge struct {
	Parent MessageID
	Childs []MessageID
}

// SnapshotEdition 一条版本链：New 是修订后的节点，Old 是被它取代的节点。
type SnapshotEdition struct {
	New MessageID
	Old MessageID
}

// Snapshot 导出整棵树。返回的切片与新分配的结构不与内部表共享，调用方可随意改写。
func (c *Conversation) Snapshot() Snapshot {
	s := Snapshot{
		ID:        c.ID,
		Title:     c.Title,
		Head:      c.Head,
		Nodes:     make([]Message, 0, len(c.nodes)),
		Edges:     make([]SnapshotEdge, 0, len(c.children)),
		CreatedAt: c.CreatedAt,
		UpdatedAt: c.UpdatedAt,
	}
	for _, m := range c.nodes {
		s.Nodes = append(s.Nodes, m)
	}
	sort.Slice(s.Nodes, func(i, j int) bool { return s.Nodes[i].ID < s.Nodes[j].ID })

	for parent, kids := range c.children {
		s.Edges = append(s.Edges, SnapshotEdge{
			Parent: parent,
			Childs: append([]MessageID(nil), kids...),
		})
	}
	sort.Slice(s.Edges, func(i, j int) bool { return s.Edges[i].Parent < s.Edges[j].Parent })

	s.RevisedFrom = make([]SnapshotEdition, 0, len(c.revisedFrom))
	for newID, oldID := range c.revisedFrom {
		s.RevisedFrom = append(s.RevisedFrom, SnapshotEdition{New: newID, Old: oldID})
	}
	sort.Slice(s.RevisedFrom, func(i, j int) bool { return s.RevisedFrom[i].New < s.RevisedFrom[j].New })
	return s
}

// Restore 由快照重建会话，并在返回前自检整树不变量。
//
// 磁盘数据是不可信输入：重复边、悬空父、Head 不在树、版本链指向树外，都会在这里被
// 明确拒绝，而不是进内存后变成难查的怪状态。关系完整性由 Validate 统一把守——
// 重建路径不再各写一套检查。
func Restore(s Snapshot) (*Conversation, error) {
	c := &Conversation{
		ID:          s.ID,
		Title:       s.Title,
		Head:        s.Head,
		nodes:       make(map[MessageID]Message, len(s.Nodes)),
		children:    make(map[MessageID][]MessageID, len(s.Edges)),
		revisedFrom: make(map[MessageID]MessageID, len(s.RevisedFrom)),
		CreatedAt:   s.CreatedAt,
		UpdatedAt:   s.UpdatedAt,
	}
	for _, m := range s.Nodes {
		if _, dup := c.nodes[m.ID]; dup {
			return nil, fmt.Errorf("restore: %w: duplicate node %q", ErrInvalidNode, m.ID)
		}
		// 防御性拷贝：快照里的分片可能与调用方共享底层数组。
		c.nodes[m.ID] = m.Clone()
	}
	for _, e := range s.Edges {
		c.children[e.Parent] = append([]MessageID(nil), e.Childs...)
	}
	for _, ed := range s.RevisedFrom {
		c.revisedFrom[ed.New] = ed.Old
	}
	if err := c.Validate(); err != nil {
		return nil, fmt.Errorf("restore: %w", err)
	}
	return c, nil
}
