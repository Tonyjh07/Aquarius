package app

// session_cmd_summary.go /summary 命令（D152，DESIGN §7.3）：
//
//	/summary <id> <文本>   手写摘要，直接插到 id 之前（无模型调用）
//	/summary <id>          让当前模型总结 id 之上的历史后插入（失败只报错、树无损）
//
// 插入 = 领域 InsertBefore/InsertCommitted：摘要占 id 的原位置、id 及其子树整体后移一位，
// Head 不变。摘要成为新的压缩水位（装配只回传到它），故插入后清屏回放——转写区恒与 Head 一致。

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/Tonyjh07/Aquarius/internal/domain/conversation"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

// execSummary 实现 /summary。
func (s *Session) execSummary(ctx context.Context, cmd port.Command) (string, error) {
	if len(cmd.Args) == 0 {
		return "", errors.New("用法: /summary <id> [摘要文本]（不带文本 = 让当前模型总结该节点之上的历史）")
	}
	id, err := s.resolveNode(cmd.Args[0])
	if err != nil {
		return "", err
	}
	if err := s.summaryTarget(id); err != nil {
		return "", err
	}

	if text := strings.TrimSpace(strings.Join(cmd.Args[1:], " ")); text != "" {
		// 手写档：内容由用户给，不调模型、不记用量。
		if _, err := s.cur.InsertBefore(id, conversation.RoleSystem,
			[]conversation.Part{{Kind: conversation.PartText, Text: text}}); err != nil {
			return "", fmt.Errorf("session: 插入摘要: %w", err)
		}
		if err := s.persist(ctx); err != nil {
			return "", err
		}
		return s.summaryAfterInsert(ctx, fmt.Sprintf("已在 %s 之前插入摘要（手写）", id))
	}

	// 模型档：总结 id 之上的祖先链后插入（失败只报错，会话树不动）。
	node, err := s.agent.SummarizeBefore(ctx, s.cur, id)
	if errors.Is(err, ErrNoHistoryAbove) {
		return "该节点之上没有可总结的历史（顶层节点之前不插摘要）", nil
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return "已取消插入摘要（会话未改动）", nil
	}
	if err != nil {
		return "", err
	}
	if err := s.persist(ctx); err != nil {
		return "", err
	}
	return s.summaryAfterInsert(ctx, fmt.Sprintf("已在 %s 之前插入摘要（in=%d out=%d tokens）",
		id, node.Usage.InputTokens, node.Usage.OutputTokens))
}

// summaryTarget 校验摘要目标：不能是 Root，也不能是会话首节点（父 = Root，即人格快照）——
// 摘要插在那里会顶到路径第二位、被装配当成 persona（persona 与压缩水位都按位置认），
// 水位语义随之失效；会话首节点之上本来也没有可总结的历史。
func (s *Session) summaryTarget(id conversation.MessageID) error {
	m, ok := s.cur.Find(id)
	if !ok {
		return fmt.Errorf("会话树中没有节点 %q", id)
	}
	if m.Role == conversation.RoleRoot || m.Parent == conversation.MessageID(s.cur.ID) {
		return errors.New("不能在会话根或会话首节点之前插入摘要（其上没有可总结的历史）")
	}
	return nil
}

// summaryAfterInsert 插入摘要后的收尾：清屏回放（水位随之可见，转写区恒与 Head 一致）。
func (s *Session) summaryAfterInsert(ctx context.Context, line string) (string, error) {
	if err := s.emitClear(ctx); err != nil {
		return "", err
	}
	if _, err := s.replayHistory(ctx, line+"；"); err != nil {
		return "", err
	}
	return line, nil
}
