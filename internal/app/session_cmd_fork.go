package app

import (
	"context"
	"errors"
	"fmt"

	"github.com/Tonyjh07/Aquarius/internal/domain/conversation"
	"github.com/Tonyjh07/Aquarius/internal/i18n"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

// execFork 实现 /fork：<节点id> [标题]（D312）——把该节点及其整棵子树摘成一份**新会话**。
//
// 与 /edit --copy 的分工：那是树内深拷贝，拷贝留在原会话里；这个是分叉带走另起一摊——
// 子树结构与兄弟顺序原样保留、Head 落在分叉节点上，节点 ID 全部重新生成，「修订自」与
// 溯源路牌不跨会话复制，附件引用原样带上（内容寻址，字节共享）。保存成功即切换并清屏回放
// （与 /import 同一套规则：界面刷新失败不报成分叉失败）；原会话一字不动，也不为它多写盘。
func (s *Session) execFork(ctx context.Context, cmd port.Command) (string, error) {
	if len(cmd.Args) == 0 {
		return "", errors.New(i18n.T("usage.fork"))
	}
	id, err := s.resolveNode(cmd.Args[0])
	if err != nil {
		return "", err
	}
	if m, ok := s.cur.Find(id); !ok {
		return "", fmt.Errorf("session: %w", conversation.ErrNotFound)
	} else if m.Role == conversation.RoleRoot {
		return "", errors.New("session: " + i18n.T("tree.fork.root")) // 整会话复制不是分叉（D312 否决 1 的反面）
	}
	c, err := s.forkConversation(id, joinArgs(cmd.Args[1:]))
	if err != nil {
		return "", err
	}
	// 切会话会离开树编辑：与 /new 同一个出口（D258）。
	if note, ok, err := s.exitTreeEditForSwitch(ctx); err != nil {
		return "", err
	} else if !ok {
		return note, nil
	}
	if err := s.store.Save(ctx, c); err != nil {
		return "", wrapSession("err.save_fork", err)
	}
	s.cur = c
	// 会话已写入磁盘并切过来了：往下是「界面刷新」，失败不能把已完成的事实报成失败。
	if err := s.emitClear(ctx); err != nil {
		return "", wrapSession("exchange.refresh_failed", err)
	}
	if _, err := s.replayHistory(ctx, "replay.verb.forked"); err != nil {
		return "", wrapSession("exchange.replay_failed", err)
	}
	return i18n.Tf("tree.fork.done", "id", c.ID, "title", c.Title, "from", id), nil
}

// forkConversation 从当前会话摘出 point 的整棵子树，长成一份新会话（D312）。
//
// 标题缺省沿用原会话标题；Character 与 Tail 带过去（会话属性不随子树变化）；节点
// CreatedAt 保留原值（历史是它发生的时间），会话的 CreatedAt/UpdatedAt 取分叉时刻。
// 「修订自」与 Detachments 一律不带：版本链是原会话内部的编辑史，跨会话没有出处。
func (s *Session) forkConversation(point conversation.MessageID, title string) (*conversation.Conversation, error) {
	src := s.cur.Snapshot()
	// 子树集合：从分叉节点沿结构边下钻。摘整棵子树而不是摘到 Head 的路径——
	// 摘路径会把别的分支静默丢弃（D312 否决 2）。
	sub := map[conversation.MessageID]bool{}
	kids := make(map[conversation.MessageID][]conversation.MessageID, len(src.Edges))
	for _, e := range src.Edges {
		kids[e.Parent] = e.Childs
	}
	var walk func(nid conversation.MessageID)
	walk = func(nid conversation.MessageID) {
		sub[nid] = true
		for _, ch := range kids[nid] {
			walk(ch)
		}
	}
	walk(point)

	newID := s.ids.ConversationID()
	newRoot := conversation.MessageID(newID)
	idMap := make(map[conversation.MessageID]conversation.MessageID, len(sub))
	for nid := range sub {
		idMap[nid] = s.ids.MessageID()
	}
	now := s.clock.Now()
	if title == "" {
		title = src.Title
	}
	snap := conversation.Snapshot{
		ID:        newID,
		Title:     title,
		Head:      idMap[point],
		Character: src.Character,
		Tail:      src.Tail,
		// Tags/Pinned 不带（D313）：标签与置顶是源会话的组织属性，子树搬走不等于组织关系搬走；
		// 新会话从零值起步。
		Nodes:     make([]conversation.Message, 0, len(sub)+1),
		Edges:     make([]conversation.SnapshotEdge, 0, len(sub)+1),
		CreatedAt: now,
		UpdatedAt: now,
	}
	// 新根（persona 由 /respond 等后续写入，D20 的构造规则：Root 空节点起步）。
	snap.Nodes = append(snap.Nodes, conversation.Message{
		ID:        newRoot,
		Role:      conversation.RoleRoot,
		CreatedAt: now,
	})
	for _, m := range src.Nodes { // 已按 ID 升序，新会话的节点序与来源一致
		if !sub[m.ID] {
			continue
		}
		parent := m.Parent
		if m.ID == point {
			parent = newRoot // 分叉节点挂上新根；其余节点的父必在子树内
		} else {
			parent = idMap[m.Parent]
		}
		m.ID = idMap[m.ID]
		m.Parent = parent
		snap.Nodes = append(snap.Nodes, m) // Restore 侧有防御性拷贝，Part 不共享底层数组
	}
	snap.Edges = append(snap.Edges,
		conversation.SnapshotEdge{Parent: "", Childs: []conversation.MessageID{newRoot}},
		conversation.SnapshotEdge{Parent: newRoot, Childs: []conversation.MessageID{idMap[point]}})
	// 子树内部的父子边：沿来源结构边走，兄弟顺序原样保留（节点序不等于兄弟序，D152）。
	for _, e := range src.Edges {
		if !sub[e.Parent] {
			continue
		}
		childs := make([]conversation.MessageID, 0, len(e.Childs))
		for _, ch := range e.Childs {
			if sub[ch] {
				childs = append(childs, idMap[ch])
			}
		}
		if len(childs) > 0 {
			snap.Edges = append(snap.Edges, conversation.SnapshotEdge{Parent: idMap[e.Parent], Childs: childs})
		}
	}
	out, err := conversation.Restore(snap)
	if err != nil {
		return nil, wrapSession("err.fork_structure", err)
	}
	return out, nil
}
