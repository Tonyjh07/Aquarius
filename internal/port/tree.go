package port

import "github.com/Tonyjh07/Aquarius/internal/domain/conversation"

// BranchInfo 一个节点的同级视图（D80/§7.5）：同父全部孩子按**创建序**（含自身）与自身下标。
// 值语义、只读——UI 渲染分叉编号与左右切换只需这两个事实，拿不到树结构本体。
type BranchInfo struct {
	// IDs 同父全部孩子，按创建时间升序（含 id 自身）；至少含自身一条。
	IDs []conversation.MessageID
	// Index 自身在 IDs 中的下标（0 起）。
	Index int
}

// TreeView 会话树只读视图端口（D80/§7.5，前置 A）：UI 只读树事实用，**不给变更入口**
// （一切变更经内核命令，§6/§7.3）。
//
// 并发（§15.5）：实现在 app 侧以不可变快照 + 原子发布承载，UI 事件循环 goroutine
// 可无锁调用；实现方负责保证同一时刻的返回值是一个自洽快照。
type TreeView interface {
	// Branches 返回 id 的同级视图；id 不在树中时 ok=false（返回零值 BranchInfo）。
	Branches(id conversation.MessageID) (BranchInfo, bool)
}
