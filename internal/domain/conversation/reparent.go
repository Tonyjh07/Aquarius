package conversation

import (
	"fmt"

	"github.com/Tonyjh07/Aquarius/internal/i18n"
)

// reparent.go 改父（D255）：把一棵子树从一个父挪到另一个父。
//
// 这是领域里唯一一条「既存节点换父」的路径。Append 的父恒为 Head、Revise 的父恒为原父、
// InsertCommitted 只改目标的父，Prune 只删——在这之前，节点一旦入树就再也不能换位置。
//
// 拦什么、不拦什么：
//   - 拦 Root 作被搬节点（Root 即会话，D19）、拦 Root 作新父（装配会静默丢掉人格，见契约 §4.1）；
//   - 拦环（新父是自己或自己的严格后代）；
//   - 拦「新父就是当前父」（无事可做，报错比假装成功好）；
//   - **不拦角色**：人格是 Root 的首孩子、其余节点全在它下面，搬它必命中环，已被结构钉死；
//     摘要与衬垫可以搬，落点衬垫会让它降级为普通节点——那是调用方要提示的事，不是领域要拒绝的事。
//
// 不做的事：不判装配水位（那是应用层的口径）、不动 Head（它在被搬子树内就随行、在外则完全
// 不受影响，两种情形都不需要动）、不改版本链（结构归属与版本关系是两条线，D255 允许并存）。

// ReparentResult 改父的回执：搬动了多少节点（整棵子树计数）。
// 水位前后对比不在这里——「哪些节点进上下文」是装配口径，领域不认。
type ReparentResult struct{ Moved int }

// Reparent 把 id 及其整棵子树挪到 newParent 下，成为它最后一个孩子。
// 成功时记一条路牌（锚点 = 原父、根 = [id]、新位置 = newParent），返回被搬子树的节点数。
func (c *Conversation) Reparent(id, newParent MessageID) (ReparentResult, error) {
	m, ok := c.nodes[id]
	if !ok {
		return ReparentResult{}, fmt.Errorf("reparent %q: %w", id, ErrNotFound)
	}
	np, ok := c.nodes[newParent]
	if !ok {
		return ReparentResult{}, fmt.Errorf("reparent %q -> %q: %w: new parent not in tree", id, newParent, ErrNotFound)
	}
	if m.Role == RoleRoot {
		return ReparentResult{}, fmt.Errorf("%s: %w", i18n.Tf("conv.err.reparent_root", "id", id), ErrInvalidNode)
	}
	// Root 不能作新父：Head 落在该支时路径成了 Root → 该支，path[1] 不再是 system，
	// 装配（waterline 只认位置）会静默丢掉人格。这条不经环拦截，必须单独关。
	if np.Role == RoleRoot {
		return ReparentResult{}, fmt.Errorf("%s: %w",
			i18n.Tf("conv.err.reparent_to_root", "id", id, "parent", newParent), ErrInvalidNode)
	}
	if newParent == id || c.isStrictDescendant(id, newParent) {
		return ReparentResult{}, fmt.Errorf("%s: %w",
			i18n.Tf("conv.err.reparent_descendant", "id", id, "parent", newParent), ErrInvalidNode)
	}
	if m.Parent == newParent {
		return ReparentResult{}, fmt.Errorf("%s: %w",
			i18n.Tf("conv.err.reparent_same_parent", "id", id, "parent", newParent), ErrInvalidNode)
	}

	oldParent := m.Parent
	c.detachChild(oldParent, id)
	c.attachChild(newParent, id)
	c.recordDetachment(oldParent, []MessageID{id}, newParent, DetachReparent)
	c.UpdatedAt = nowFunc()
	if err := c.checkInvariants("reparent"); err != nil {
		return ReparentResult{}, err
	}
	return ReparentResult{Moved: c.SubtreeSize(id)}, nil
}
