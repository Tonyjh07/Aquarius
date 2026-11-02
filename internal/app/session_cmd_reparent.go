package app

import (
	"context"
	"errors"
	"strings"

	"github.com/Tonyjh07/Aquarius/internal/domain/conversation"
	"github.com/Tonyjh07/Aquarius/internal/i18n"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

// reparentUsage /reparent 的用法句（两条路各一句，报错时原样给出）。
func reparentUsage() string { return i18n.T("usage.reparent") }

// execReparent 实现 /reparent（D255/D256）：整棵子树改挂到新父下，或反做最近一次搬运。
//
// 两条路走同一个领域原语（`Reparent` / `UndoLastDetachment`），关系图拖拽将来也走它——
// 界面与命令面在结构上做的是同一件事，不各写一套判断。
func (s *Session) execReparent(ctx context.Context, cmd port.Command) (string, error) {
	if len(cmd.Args) == 1 && cmd.Args[0] == "--undo" {
		return s.undoReparent(ctx)
	}
	if len(cmd.Args) != 2 {
		return "", errors.New(reparentUsage())
	}
	for _, a := range cmd.Args {
		if strings.HasPrefix(a, "--") {
			return "", errors.New(reparentUsage())
		}
	}
	id, err := s.resolveNode(cmd.Args[0])
	if err != nil {
		return "", err
	}
	parent, err := s.resolveNode(cmd.Args[1])
	if err != nil {
		return "", err
	}

	head := s.cur.Head
	res, err := s.cur.Reparent(id, parent)
	if err != nil {
		return "", wrapSession("err.reparent", err)
	}
	line := i18n.Tf("reparent.done", "n", res.Moved, "parent", parent)
	if s.cur.IsPad(parent) {
		line += i18n.T("reparent.done_pad")
	}
	// 树编辑会话内：只改内存，退出时统一落盘（D258）；转写区也不重画——
	// 编辑期间连着拖几下手会不停清屏，而这次改动的最终形状在退出时才需要确认。
	if s.treeEdit != nil {
		s.treeEdit.edits++
		return line + i18n.T("reparent.in_edit"), nil
	}
	if err := s.persist(ctx); err != nil {
		// 与 /rm 同口径：落盘失败不留在内存里半生效——回到最近一次成功保存的状态。
		if c, lerr := s.store.Load(ctx, s.cur.ID); lerr == nil {
			s.cur = c
			return "", wrapSession("err.reparent_persist_rollback", err)
		}
		return "", wrapSession("err.reparent_persist_stuck", err)
	}

	// Head 没动，但被搬子树若包含 Head，它上来的那条路换了——转写区要跟着重画
	// （与 /goto 的清屏回放同口径）。不包含时路径一字未变，不必打断阅读。
	if pathContains(s.cur, id, head) {
		if err := s.emitClear(ctx); err != nil {
			return "", err
		}
		if _, err := s.replayHistory(ctx, "replay.verb.reparented"); err != nil {
			return "", err
		}
	}
	return line, nil
}

// undoReparent 反做最近一条搬运记录：端点来自记录本身，撤销后再把记录消费掉。
func (s *Session) undoReparent(ctx context.Context) (string, error) {
	head := s.cur.Head
	d, err := s.cur.UndoLastDetachment()
	if err != nil {
		return "", wrapSession("err.reparent_undo", err)
	}
	what := i18n.T("reparent.kind.move")
	if d.Kind == conversation.DetachTransfer {
		what = i18n.T("reparent.kind.transfer")
	}
	line := i18n.Tf("reparent.undone", "what", what, "n", len(d.Roots), "anchor", d.Anchor)
	if s.treeEdit != nil {
		if s.treeEdit.edits > 0 {
			s.treeEdit.edits--
		}
		return line + i18n.T("reparent.in_edit"), nil
	}
	if err := s.persist(ctx); err != nil {
		if c, lerr := s.store.Load(ctx, s.cur.ID); lerr == nil {
			s.cur = c
			return "", wrapSession("err.undo_persist_rollback", err)
		}
		return "", wrapSession("err.undo_persist_stuck", err)
	}
	if pathContains(s.cur, d.Roots[0], head) {
		if err := s.emitClear(ctx); err != nil {
			return "", err
		}
		if _, err := s.replayHistory(ctx, "replay.verb.reparent_undone"); err != nil {
			return "", err
		}
	}
	return line, nil
}

// pathContains 报告 id 是否在 Head 的路径上（含 Head 自身）。
// 改父不改 Head，但把 Head 的某个祖先挪走会改变它上来的那条路。
func pathContains(c *conversation.Conversation, id, head conversation.MessageID) bool {
	for cur := head; cur != ""; {
		if cur == id {
			return true
		}
		m, ok := c.Find(cur)
		if !ok {
			return false
		}
		cur = m.Parent
	}
	return false
}
