package port

import (
	"time"

	"github.com/Tonyjh07/Aquarius/internal/domain/conversation"
)

// BranchInfo 一个节点的同级视图（D80/§7.5）：同父全部孩子按**创建序**（含自身）与自身下标。
// 值语义、只读——UI 渲染分叉编号与左右切换只需这两个事实，拿不到树结构本体。
type BranchInfo struct {
	// IDs 同父全部孩子，按创建时间升序（含 id 自身）；至少含自身一条。
	IDs []conversation.MessageID
	// Index 自身在 IDs 中的下标（0 起）。
	Index int
}

// EdgeKind 节点与其父之间**边**的种类（D120§2.3）：会话树只持久化 `RevisedFrom`（新→旧），
// Fresh/Carry/Clone 的 KeepMode 不落库，故版本链**只分两类**、不猜细分。
type EdgeKind string

const (
	// EdgeSeq 顺接：目标节点不在 `RevisedFrom` 的值集中（普通追加）。
	EdgeSeq EdgeKind = "seq"
	// EdgeRevise 版本链：`RevisedFrom[目标]` 命中（修订/转移/复制都经 Revise）。
	EdgeRevise EdgeKind = "revise"
)

// GraphNode 整树快照的一个节点（D120§6.1）：只给**树事实**——Level/Depth/坐标/配色等
// 布局派生量一律由 UI 侧计算，不进端口。
type GraphNode struct {
	ID      conversation.MessageID
	Parent  conversation.MessageID // 结构根为空串
	Role    conversation.Role      // root/user/assistant/system（类别）
	Snippet string                 // 展示用短摘要（渲染文本首行片段；非推理口径）
	// CreatedAt 节点创建时刻（D96 的 Clone 保留原值，故不保证与 ID 序一致）。
	CreatedAt time.Time
	// EdgeKind 与父边的种类；结构根无父边，为零值（""）。
	EdgeKind EdgeKind
	// SiblingIdx/SiblingCount 同父孩子中的序号与总数（版本 i/n 口径，与 BranchInfo 同源）。
	SiblingIdx   int
	SiblingCount int
}

// TreeGraph 会话树整树只读快照（D112②/D120⑦，S3 relation-map 数据面）。
// 与 Branches/Tail 同源、同一不可变快照发布；展示口径，不参与推理装配。
type TreeGraph struct {
	// Anchor 当前 Head 消息（锚点，D120§2.1）；节点不在图内时为空串。
	Anchor conversation.MessageID
	// Nodes 全部节点，按 (创建时间, ID) 升序——布局可复现（同 CreatedAt 用 ID 定序）。
	Nodes []GraphNode
	// Total 节点总数 = len(Nodes)（A7「共 N」；与「呈现 M」不同源，不得互相推算）。
	Total int
}

// TreeView 会话树只读视图端口（D80/§7.5，前置 A）：UI 只读树事实用，**不给变更入口**
// （一切变更经内核命令，§6/§7.3）。
//
// 并发（§15.5）：实现在 app 侧以不可变快照 + 原子发布承载，UI 事件循环 goroutine
// 可无锁调用；实现方负责保证同一时刻的返回值是一个自洽快照。
type TreeView interface {
	// Branches 返回 id 的同级视图；id 不在树中时 ok=false（返回零值 BranchInfo）。
	Branches(id conversation.MessageID) (BranchInfo, bool)
	// Tail 返回 id 版本子树的对话末端（D94）：id 自身是叶子时即自身，否则取子树中
	// CreatedAt 最新的叶子——分支切换的落点（切版本应恢复该版本的对话全程，而非停在
	// 消息节点上——其回答不在 Head 路径上）；S3 树 UI 画叶子亦可复用。
	// id 不在树中时 ok=false。
	Tail(id conversation.MessageID) (conversation.MessageID, bool)
	// Graph 返回整树只读快照（D112②/D120⑦，S3 relation-map 数据面）；尚未发布时 ok=false。
	Graph() (TreeGraph, bool)
}

// SessionSummary 会话列表项（D120⑥，S3 左栏数据面）：UI 只读展示用。
type SessionSummary struct {
	ID        conversation.ID
	Title     string
	UpdatedAt time.Time
	// Messages 消息节点数（不含 Root，与 store 列表口径一致）。
	Messages int
	// Current 是否当前会话。
	Current bool
}

// SessionLister 会话列表只读端口（D120⑥，S3 左栏）：与 TreeView 同款**反向端口**
// （实现方 app、消费方 UI 适配器），**只读、无变更入口**——一切变更经内核命令。
//
// 并发（§15.5）：实现以不可变快照 + 原子发布承载，UI 事件循环 goroutine 可无锁调用
// （不得在此端口内做 store I/O——那会阻塞 UI 线程并破坏快照语义）。
type SessionLister interface {
	List() []SessionSummary
}
