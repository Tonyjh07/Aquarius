package app

import (
	"context"
	"errors"
	"fmt"

	"github.com/Tonyjh07/Aquarius/internal/domain/conversation"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

// ErrNothingToCompact /compact 无可压缩历史：摘要之上没有新增内容（D21）。
// 文本面向模型（context_compact 工具回填），/compact 命令按 errors.Is 单独给中文提示。
var ErrNothingToCompact = errors.New("agent: nothing to compact")

// Compact 生成上下文压缩摘要（D21 手动轨，/compact）：
// 把当前上下文（persona + 现有摘要 + 其后历史）交给模型转写为一条 system 摘要节点入树
// （记 Model/Usage，链式吸收旧摘要），其上历史此后装配不再回传。
// 摘要按英文结构化模板生成（compact_prompt.go，D21/§7.1）：已有旧摘要走合并更新提示词；
// 输出缺模板小节则发 NoticeEvent 并带提醒重试一次（两次生成的 Usage 累计入节点），
// 两次都不合格按失败返回。
// 失败只报错、树无损；无可压缩历史（摘要之上无新增）返回 ErrNothingToCompact。
// 返回 (摘要节点, 被吸收的历史节点数（persona 恒回传不计）, nil)。
func (a *Agent) Compact(ctx context.Context, c *conversation.Conversation) (conversation.Message, int, error) {
	if c == nil {
		return conversation.Message{}, 0, errors.New("agent: nil conversation")
	}
	path := c.Path()
	if len(path) <= 1 { // 仅 Root
		return conversation.Message{}, 0, ErrNothingToCompact
	}
	personaIdx, watermarkIdx := waterline(path)
	start := 1
	if watermarkIdx >= 0 {
		start = watermarkIdx
	}
	absorbed := len(path) - start
	if personaIdx >= start {
		absorbed-- // persona 恒回传，不计入被吸收的历史
	}
	// 可压缩增量 = 水位之后的非摘要节点；为 0 表示上下文没有新内容（重复压缩短路）。
	fresh := 0
	for i := start; i < len(path); i++ {
		if path[i].Role != conversation.RoleSystem {
			fresh++
		}
	}
	if fresh == 0 {
		return conversation.Message{}, 0, ErrNothingToCompact
	}

	// 压缩输入 = 当前上下文（已水位化：链式吸收只吞旧摘要与其后的新历史）。
	history, err := assemblePath(ctx, path, a.blobs, a.cfg.EchoThinking)
	if err != nil {
		return conversation.Message{}, 0, fmt.Errorf("assemble compact input: %w", err)
	}
	// 提示词两态：水位存在 = 已有旧摘要 → 合并更新，否则首次压缩（compact_prompt.go）。
	// 生成/重试/模板校验与 /summary 共用（agent_summary.go）。
	mid := a.ids.MessageID() // 预分配关联 ID（重试复用，作流事件关联 ID；随后即节点 ID）
	text, usage, err := a.generateSummary(ctx, mid, history, watermarkIdx >= 0)
	if err != nil {
		return conversation.Message{}, 0, err
	}
	node := conversation.Message{
		ID:        mid,
		Parent:    c.Head,
		Role:      conversation.RoleSystem,
		Content:   []conversation.Part{{Kind: conversation.PartText, Text: text}},
		Outcome:   conversation.OutcomeDone,
		Model:     a.modelName(),
		Usage:     usage,
		CreatedAt: a.clock.Now(),
	}
	if err := c.AppendCommitted(node); err != nil {
		return conversation.Message{}, 0, fmt.Errorf("commit summary node: %w", err)
	}
	_ = a.ui.Emit(ctx, port.CommittedEvent{Message: node})
	return node, absorbed, nil
}

// autoCompact 自动压缩轨（D21 轨2 / M2）：估算达阈值时先尝试 Compact——
// 成功则重建请求（水位生效）并发 NoticeEvent；无可压缩内容则维持原请求；
// 失败回退"最旧裁剪"（D21：保 leading system 与最近、丢中间）并提示省略条数。
// 返回（执行后的请求, 用于 usage 校准的该请求估算）。
func (a *Agent) autoCompact(ctx context.Context, c *conversation.Conversation, req port.GenerateRequest, est int, buf *turnBuffer) (port.GenerateRequest, int) {
	_, _, err := a.Compact(ctx, c)
	switch {
	case err == nil:
		_ = a.ui.Emit(ctx, port.NoticeEvent{
			Text: fmt.Sprintf("上下文 ≈%d tokens 达自动压缩阈值 %d，已生成摘要", est, a.compactAt),
		})
		next, berr := a.buildRequest(ctx, c, buf)
		if berr != nil {
			return req, est // 重建失败：沿用原请求（下一轮生成会暴露同因错误）
		}
		ne, _ := a.est.Estimate(ctx, next)
		return next, ne
	case errors.Is(err, ErrNothingToCompact):
		return req, est // 摘要之上无新内容（如 persona 巨大）：无可压，维持原请求
	default:
		kept, omitted := a.est.trimOldest(ctx, req.Messages, a.compactAt, req.Tools...)
		req.Messages = kept
		text := fmt.Sprintf("自动压缩失败（%v），已回退最旧裁剪", err)
		if omitted > 0 {
			text += fmt.Sprintf("，已省略 %d 条较早消息", omitted)
		}
		_ = a.ui.Emit(ctx, port.NoticeEvent{Text: text})
		ne, _ := a.est.Estimate(ctx, req)
		return req, ne
	}
}
