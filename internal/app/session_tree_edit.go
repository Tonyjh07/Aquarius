package app

import (
	"context"
	"errors"
	"fmt"

	"github.com/Tonyjh07/Aquarius/internal/domain/conversation"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

// treeEditSession 一次树编辑会话（D258）：结构改动只改内存，退出时统一落一次盘。
//
// 为什么要会话而不是「每次改父都落盘」：一次整理往往连着改十几处，逐次写盘会把备份轮转
// 刷成同一天的中间态，而中间态恰恰是最没有价值的备份。
type treeEditSession struct {
	baseline conversation.Snapshot // 进入时的整树快照（放弃时回到它）
	before   int                   // 进入时的上下文条数（退出比对用，装配口径）
	edits    int                   // 已生效的改父次数
	saved    bool                  // 会话期间落过盘（来了新消息/跑了轮次），放弃时要把那次写盘也撤回
}

// treeEditUsage /tree-edit 的用法句。
const treeEditUsage = "用法: /tree-edit（进入）｜--exit（界面上关开关：有改动先问保存/放弃）｜--done（保存退出）｜--cancel（放弃并退出）"

// TreeEditState 报告当前是否在树编辑模式，以及已改处数（D258），实现 port.TreeView。
// 关系图的状态栏开关按它渲染——UI 不自己记状态，状态只有一份。
func (s *Session) TreeEditState() (active bool, edits int) {
	if s.treeEdit == nil {
		return false, 0
	}
	return true, s.treeEdit.edits
}

// execTreeEdit 实现 /tree-edit：进入、保存退出、放弃退出。
func (s *Session) execTreeEdit(ctx context.Context, cmd port.Command) (string, error) {
	if len(cmd.Args) > 1 {
		return "", errors.New(treeEditUsage)
	}
	if len(cmd.Args) == 0 {
		if s.treeEdit != nil {
			return s.treeEditStatus(), nil
		}
		return s.beginTreeEdit()
	}
	switch cmd.Args[0] {
	case "--exit":
		// 关开关 / 按 Esc 的出口：与切会话走**同一个**三选一（保存 → 放弃 → 取消）。
		// UI 侧不复刻这套判断，否则「开关怎么关」会有第二种说法。取消时 line 是那句
		// 「仍在树编辑模式」（不是错误）——开关读状态即知自己还是开着的。
		//
		// 不在模式里**不算错**：--done/--cancel 的语义是「改点什么」，不在模式里就无事可
		// 做；--exit 的语义是「确保出来」，已经出来了就是成功（开关连点两下的第二下不该
		// 在主窗转写区留下一行红字）。
		if s.treeEdit == nil {
			return "当前不在树编辑模式（无需退出）", nil
		}
		line, _, err := s.exitTreeEditForSwitch(ctx)
		return line, err
	case "--done":
		return s.commitTreeEdit(ctx)
	case "--cancel":
		return s.abortTreeEdit(ctx)
	default:
		return "", errors.New(treeEditUsage)
	}
}

func (s *Session) treeEditStatus() string {
	t := s.treeEdit
	if t.edits == 0 {
		return "树编辑模式：还没改过（拖动节点改挂；/tree-edit --done 保存退出，--cancel 放弃）"
	}
	return fmt.Sprintf("树编辑模式：已改 %d 处（退出时统一保存；/tree-edit --cancel 放弃全部改动）", t.edits)
}

func (s *Session) beginTreeEdit() (string, error) {
	s.treeEdit = &treeEditSession{
		baseline: s.cur.Snapshot(),
		before:   contextCount(s.cur),
	}
	return "已进入树编辑模式：改挂只改内存，退出时统一保存（/tree-edit --done 保存，--cancel 放弃）", nil
}

// commitTreeEdit 保存并退出：上下文骤减先确认，再写一次盘。
func (s *Session) commitTreeEdit(ctx context.Context) (string, error) {
	t := s.treeEdit
	if t == nil {
		return "", errors.New("当前不在树编辑模式（/tree-edit 进入）")
	}
	after := contextCount(s.cur)
	shrink := after < t.before

	// 上下文骤减要说清再存：树合法、不报错，但「改动吞掉了一段历史」是用户看不见的后果。
	if t.edits > 0 && shrink {
		ok, err := s.confirm(ctx, fmt.Sprintf(
			"树编辑的 %d 处改动会让进上下文的节点从 %d 条减到 %d 条（旧历史不再回传给模型），仍保存退出？",
			t.edits, t.before, after))
		if err != nil {
			return "", err
		}
		if !ok {
			return fmt.Sprintf("已取消保存：改动仍在（%d 处），可继续编辑或用 /tree-edit --cancel 放弃", t.edits), nil
		}
	}

	if err := s.store.Save(ctx, s.cur); err != nil {
		return "", fmt.Errorf("session: 保存会话: %w", err)
	}
	s.treeEdit = nil
	line := fmt.Sprintf("已保存并退出树编辑：%d 处改挂", t.edits)
	if after != t.before {
		line += fmt.Sprintf("；上下文 %d → %d 条", t.before, after)
	}
	return line, nil
}

// abortTreeEdit 放弃并退出：整棵树回到进入时的样子。
//
// 会话期间若来过新消息（它照常落过盘），这里要再写一次把文件也拉回基线——否则内存里
// 是旧树、盘上是新树，下次打开就「复活」了被放弃的内容。
func (s *Session) abortTreeEdit(ctx context.Context) (string, error) {
	t := s.treeEdit
	if t == nil {
		return "", errors.New("当前不在树编辑模式（/tree-edit 进入）")
	}
	restored, err := conversation.Restore(t.baseline)
	if err != nil {
		return "", fmt.Errorf("session: 回滚树编辑: %w", err)
	}
	s.cur = restored
	s.treeEdit = nil
	if t.saved {
		if err := s.store.Save(ctx, s.cur); err != nil {
			return "", fmt.Errorf("session: 回滚后保存: %w", err)
		}
	}
	if t.edits == 0 {
		return "已退出树编辑（没有改动）", nil
	}
	line := fmt.Sprintf("已放弃 %d 处改挂，树回到进入编辑时的样子", t.edits)
	if t.saved {
		line += "（会话期间落过盘，文件已一并回滚）"
	}
	return line, nil
}

// exitTreeEditForSwitch 切会话/关窗前的同一个出口：有改动就问「保存 / 放弃 / 取消」。
//
// Confirmer 只有两档，所以拆成两问：先问保存，拒绝再问放弃，再拒绝就是取消（留在编辑模式）。
// 返回 true = 可以继续离开。
func (s *Session) exitTreeEditForSwitch(ctx context.Context) (string, bool, error) {
	t := s.treeEdit
	if t == nil {
		return "", true, nil
	}
	if t.edits == 0 {
		line, err := s.abortTreeEdit(ctx) // 没改过就静默离开，不打扰
		return line, true, err
	}
	ok, err := s.confirm(ctx, fmt.Sprintf(
		"树编辑有 %d 处未保存改动，保存后退出？", t.edits))
	if err != nil {
		return "", false, err
	}
	if ok {
		line, err := s.commitTreeEdit(ctx)
		return line, err == nil, err
	}
	ok, err = s.confirm(ctx, fmt.Sprintf("放弃这 %d 处改动并退出树编辑？", t.edits))
	if err != nil {
		return "", false, err
	}
	if !ok {
		return fmt.Sprintf("已取消：仍在树编辑模式（%d 处改动未保存）", t.edits), false, nil
	}
	line, err := s.abortTreeEdit(ctx)
	return line, err == nil, err
}

// confirm 包一层确认器：没有确认器时按「允许」处理——树编辑的退出确认不该让
// 无交互环境（测试、无人值守）卡死；真正的守卫是「拒绝保存就不退出」。
func (s *Session) confirm(ctx context.Context, prompt string) (bool, error) {
	if s.confirmer == nil {
		return true, nil
	}
	ans, err := s.confirmer.Confirm(ctx, prompt)
	if err != nil {
		return false, fmt.Errorf("session: 确认: %w", err)
	}
	return ans.Allow, nil
}

// contextCount 数一棵树会进上下文的节点条数（装配口径，D20/D21）：persona 恒回传、
// 最新摘要之上的历史不回传、Root 不进上下文。退出编辑时用它前后对比。
func contextCount(c *conversation.Conversation) int {
	path := c.Path()
	personaIdx, watermarkIdx := waterline(path)
	start := 1 // 起点恒 ≥ 1：Root 不进上下文
	if watermarkIdx >= 0 {
		start = watermarkIdx
	}
	n := 0
	if watermarkIdx >= 0 && personaIdx == 1 {
		n++
	}
	for i := start; i < len(path); i++ {
		if path[i].Role != conversation.RoleRoot {
			n++
		}
	}
	return n
}
