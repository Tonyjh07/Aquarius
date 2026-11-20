package app

import (
	"context"
	"errors"
	"fmt"

	"github.com/Tonyjh07/Aquarius/internal/domain/conversation"
	"github.com/Tonyjh07/Aquarius/internal/i18n"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

// usesServerTool 节点里是否带着服务端工具块（D310）。
func usesServerTool(m conversation.Message) bool {
	for _, p := range m.Content {
		if p.Kind == conversation.PartServer {
			return true
		}
	}
	return false
}

// execRespond 实现 /respond：从所选节点继续生成一条响应。
//
// 落法 = /goto（移 Head + 清屏回放，D81）叠加一轮生成：`Checkout(id)` 把 Head 移到
// 所选节点，`Agent.Run` 内部按 `Path()`（Root→Head）装配、`assemblePath` 再按
// 水位（persona / 末条摘要 system）裁剪——故**实际发往 API 的正是「水位 → 所选节点
// （含该节点）」**，装配侧一行都不用改。
//
// 产物是该节点的 **assistant 子节点**：所选节点已有子节点时即开一条新分支（旧分支
// 原样保留，与 /regen 的「旧回答留作历史分支」同向）。会话根节点即会话本身、没有可
// 续写的上下文，作为生成起点直接拒绝。
func (s *Session) execRespond(ctx context.Context, cmd port.Command) (string, error) {
	if len(cmd.Args) != 1 {
		return "", errors.New(i18n.T("usage.respond"))
	}
	id, err := s.resolveNode(cmd.Args[0])
	if err != nil {
		return "", err
	}
	m, ok := s.cur.Find(id)
	if !ok {
		return "", fmt.Errorf("session: %w", conversation.ErrNotFound)
	}
	if m.Role == conversation.RoleRoot {
		return "", errors.New(i18n.T("respond.root"))
	}
	// 用过服务端阅读的回答不能续写（D310）：协议会以 400 拒绝「续写的回复用过
	// deepseek_read_url」，且没有可剥离的字段——在本地就拒，比发出去再失败好懂。
	if usesServerTool(m) {
		return "", errors.New(i18n.T("respond.server_tool"))
	}
	if err := s.cur.Checkout(id); err != nil {
		return "", wrapSession("respond.move_head", err)
	}
	if err := s.persist(ctx); err != nil {
		return "", err
	}
	// 移 Head 后清屏回放（与 /goto 同规则 D81）：转写区恒与 Head 一致，生成过程可见。
	if err := s.emitClear(ctx); err != nil {
		return "", err
	}
	if _, err := s.replayHistory(ctx, "replay.verb.moved"); err != nil {
		return "", err
	}
	if err := s.runTurn(ctx); err != nil {
		return "", err
	}
	return i18n.Tf("respond.done", "head", s.cur.Head), nil
}
