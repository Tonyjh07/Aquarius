package conversation

import (
	"fmt"
	"time"

	"github.com/Tonyjh07/Aquarius/internal/i18n"
)

// detachment.go 溯源路牌（D256）：某处曾挂过的内容去了哪里。
//
// 它回答一个结构本身答不出的问题：Carry 之后旧节点成为无子叶，后续内容整批去了新版本；
// 从旧节点一侧再看，那些孩子像是凭空消失了。版本链只记「谁修订了谁」，而同一个旧节点可以
// 被转移两次（一对多），反查不唯一——所以要一条显式记录。
//
// **它不是第二父**。多父图是已被否决的方向（那会让路径、版本组、装配、删除统计全部重算）。
// 一条记录只描述「发生过一次搬运」，不改变任何结构事实：路径、版本组、装配、删除统计都不读它。
// 要「从断点挂回来」是两次改父，不是让记录承担父子关系。
//
// 与结构边的校验口径不同：记录天然违反「父子双向一致」（旧节点此刻无子，而它曾经的孩子挂在
// 别处），所以它不进结构边检查，只照版本链的先例查**三端都在树**；另加一条陈旧性检查——
// 被记的子树根现在必须确实挂在 NewHome 下，否则标 Stale（检出但不删：另一次搬运可能让它重新成立）。

// DetachmentKind 路牌的来源种类。
type DetachmentKind string

const (
	// DetachTransfer Revise Carry：旧节点的孩子边整批转给新版本。
	DetachTransfer DetachmentKind = "transfer"
	// DetachReparent Reparent：子树整棵挪到另一个父下。
	DetachReparent DetachmentKind = "reparent"
)

// String 实现 fmt.Stringer（落盘与回执都用它，避免两处各写一套词表）。
func (k DetachmentKind) String() string { return string(k) }

// Detachment 一条溯源路牌：一次搬运一条，不按孩子拆。
type Detachment struct {
	// Anchor 锚点：转移 = 被取代的旧版本节点；改父 = 原子树的父。
	Anchor MessageID
	// Roots 被搬走的子树根，按搬运前的兄弟顺序；转移是整批孩子，改父只有一项。
	Roots []MessageID
	// NewHome 新位置：转移 = 新版本节点；改父 = 新父。
	NewHome MessageID
	Kind    DetachmentKind
	At      time.Time
	// Stale 记录已失效：被搬的子树根现在不挂在 NewHome 下。存储里不存这个值，
	// 由 Detachments() 现算——派生状态落库会在下一次结构变动后变成谎言。
	Stale bool
}

// Detachments 返回全部路牌的只读副本，按发生时刻升序（即记录顺序）。
// Stale 在此现算，调用方不需要自己判断记录是否还成立。
func (c *Conversation) Detachments() []Detachment {
	if len(c.detached) == 0 {
		return nil
	}
	out := make([]Detachment, 0, len(c.detached))
	for _, d := range c.detached {
		cp := d
		cp.Roots = append([]MessageID(nil), d.Roots...)
		cp.Stale = !c.detachmentHolds(d)
		out = append(out, cp)
	}
	return out
}

// detachmentHolds 报告一条路牌是否仍然成立：NewHome 在树里，且每个被记的子树根都挂在它下面。
// 不成立不等于记录错——后来又被搬走就会这样，那条记录只是过时了。
func (c *Conversation) detachmentHolds(d Detachment) bool {
	if _, ok := c.nodes[d.NewHome]; !ok {
		return false
	}
	for _, r := range d.Roots {
		m, ok := c.nodes[r]
		if !ok || m.Parent != d.NewHome {
			return false
		}
	}
	return true
}

// recordDetachment 追加一条路牌（内部用：调用方已确认搬运发生）。
func (c *Conversation) recordDetachment(anchor MessageID, roots []MessageID, newHome MessageID, kind DetachmentKind) {
	if len(roots) == 0 {
		return
	}
	c.detached = append(c.detached, Detachment{
		Anchor:  anchor,
		Roots:   append([]MessageID(nil), roots...),
		NewHome: newHome,
		Kind:    kind,
		At:      nowFunc(),
	})
}

// UndoLastDetachment 反做最近一条路牌：把它记的子树根从 NewHome 挪回 Anchor，然后**消费掉**
// 这条记录。按发生顺序倒着走（栈语义），端点在记录里，不需要调用方再给参数。
//
// 不记新路牌——撤销本身产生的「搬运」若也入库，下一次 --undo 就会去撤自己的撤销。
// 记录已陈旧（根已不在 NewHome 下）时拒绝执行：那说明之后还有别的搬运，盲目往回挪会把
// 树搬到意料之外的位置；让人先处理更近的那一条。
func (c *Conversation) UndoLastDetachment() (Detachment, error) {
	if len(c.detached) == 0 {
		return Detachment{}, fmt.Errorf("%s: %w", i18n.T("conv.err.undo_none"), ErrNotFound)
	}
	d := c.detached[len(c.detached)-1]
	if !c.detachmentHolds(d) {
		return Detachment{}, fmt.Errorf("%s: %w",
			i18n.Tf("conv.err.undo_stale", "home", d.NewHome), ErrInvalidNode)
	}
	for _, r := range d.Roots {
		c.detachChild(d.NewHome, r)
		c.attachChild(d.Anchor, r)
	}
	c.detached[len(c.detached)-1] = Detachment{} // 放开引用，别让被消费的记录挂在底层数组上
	c.detached = c.detached[:len(c.detached)-1]
	c.UpdatedAt = nowFunc()
	if err := c.checkInvariants("undo detachment"); err != nil {
		return Detachment{}, err
	}
	out := d
	out.Roots = append([]MessageID(nil), d.Roots...)
	out.Stale = false
	return out, nil
}

// IsPad 报告 id 是否为衬垫（D152）。判据仍只有 padNode 一处；命令面与展示面要提示
// 「这条节点会被降级」时不必各写一遍结构判断。
func (c *Conversation) IsPad(id MessageID) bool { return c.padNode(id) }

// pruneDetachments 清掉端点落在被剪子树里的路牌：锚点、新位置、任一子树根被删，记录就不再
// 指向树内的真实位置。留存一半的路牌比没有更糟——它会让撤销去挪一个不存在的节点。
func (c *Conversation) pruneDetachments(sub map[MessageID]bool) {
	kept := c.detached[:0]
	for _, d := range c.detached {
		if sub[d.Anchor] || sub[d.NewHome] {
			continue
		}
		dead := false
		for _, r := range d.Roots {
			if sub[r] {
				dead = true
				break
			}
		}
		if dead {
			continue
		}
		kept = append(kept, d)
	}
	c.detached = kept
}
