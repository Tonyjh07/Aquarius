package app

// agent_summary.go 「插入摘要」的模型档（D152，§7.3）与 /compact 共用的摘要生成。
//
// 与 /compact（agent_compact.go）的分工：
//   - /compact = 把**当前上下文**压成一条摘要**接在 Head 之后**（摘要成为新的水位）；
//   - /summary <id>（无文本）= 把 **id 之上的祖先链**压成一条摘要**插到 id 之前**
//     （id 及其子树留在水位之后，零拷贝随行）——「在某节点前插入摘要」。
//
// 两处共用同一套压缩提示词（compact_prompt.go）、同一条两段式重试与模板校验（generateSummary），
// 失败一律只报错、树无损。

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/Tonyjh07/Aquarius/internal/domain/conversation"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

// ErrNoHistoryAbove 目标节点之上没有可总结的历史（祖先链只剩 Root，或水位之上无新内容）。
// 文本面向用户（/summary 命令按 errors.Is 单独给中文提示）。
var ErrNoHistoryAbove = errors.New("agent: no history above target")

// generateSummary 用压缩提示词生成摘要正文（D21/D152 共用）。history = 已装配的历史消息，
// existing = 其上已有旧摘要（合并更新提示词）。id 是流式事件的关联 ID：调用方随后用同一个
// ID 落节点，UI 才能把流内草稿替换成定稿。
// 至多两次生成：输出为空或缺模板小节时发 NoticeEvent 并带提醒重试一次；两次都不合格按失败
// 返回（调用方不落树）。返回（摘要正文, 两次生成的累计用量, 错误）。
func (a *Agent) generateSummary(ctx context.Context, id conversation.MessageID, history []port.PromptMessage, existing bool) (string, conversation.Usage, error) {
	base := make([]port.PromptMessage, 0, len(history)+2)
	base = append(base, port.PromptMessage{
		Role:    port.PromptSystem,
		Content: []port.PromptPart{{Kind: port.PromptText, Text: compactPrompt(existing)}},
	})
	base = append(base, history...)
	base = append(base, port.PromptMessage{
		Role:    port.PromptUser,
		Content: []port.PromptPart{{Kind: port.PromptText, Text: compactNudge}},
	})

	var (
		usage conversation.Usage
		text  string
	)
	for attempt := 0; attempt < 2; attempt++ {
		msgs := make([]port.PromptMessage, 0, len(base)+1)
		msgs = append(msgs, base...)
		if attempt > 0 {
			_ = a.ui.Emit(ctx, port.NoticeEvent{Text: "摘要输出未匹配模板，正在带提醒重试"})
			msgs = append(msgs, port.PromptMessage{
				Role:    port.PromptUser,
				Content: []port.PromptPart{{Kind: port.PromptText, Text: compactRetryReminder}},
			})
		}
		buf := newTurnBuffer(id)
		stream, err := a.llm.Generate(ctx, port.GenerateRequest{
			Model:    a.modelName(),
			Messages: msgs,
			Budget:   a.cfg.Budget,
		})
		if err != nil {
			return "", usage, fmt.Errorf("generate summary: %w", err)
		}
		// 流式过程只进 UI；失败不提交（树无损）。
		if _, err := a.consume(ctx, stream, buf, id); err != nil {
			return "", usage, fmt.Errorf("generate summary: %w", err)
		}
		usage.InputTokens += buf.round.InputTokens
		usage.OutputTokens += buf.round.OutputTokens
		usage.CostUSD += buf.round.CostUSD
		text = strings.TrimSpace(buf.text.String())
		if text != "" && hasSummarySection(text) {
			break
		}
		if attempt == 1 {
			if text == "" {
				return "", usage, errors.New("generate summary: model returned empty output")
			}
			return "", usage, errors.New("generate summary: output did not match the summary template")
		}
	}
	return text, usage, nil
}

// SummarizeBefore 生成「id 之上历史」的摘要并插到 id **之前**（D152 /summary 无文本档）。
//
// 输入 = Root→id 的父链（**不含 id 自身**：摘要只覆盖它上面的部分，id 及其子树留在水位之后），
// 水位化与提示词同 /compact（已有旧摘要则合并更新）。成功后节点入树、发 CommittedEvent，
// Head 不变、子树零拷贝；失败只报错、树无损。返回插入的摘要节点（含 Model/Usage 记账）。
//
// 目标约束（/summary 与图侧卡片共同把关）：
//   - 不能是 Root（根前无处可插）；
//   - 不能是顶层节点（父 = Root）——摘要会顶到路径第二位，被装配当成 persona（persona 与
//     水位都是按位置认的），水位语义随之失效；顶层节点之上本来也没有可总结的历史。
func (a *Agent) SummarizeBefore(ctx context.Context, c *conversation.Conversation, target conversation.MessageID) (conversation.Message, error) {
	if c == nil {
		return conversation.Message{}, errors.New("agent: nil conversation")
	}
	tm, ok := c.Find(target)
	if !ok {
		return conversation.Message{}, fmt.Errorf("summary: %w", conversation.ErrNotFound)
	}
	if tm.Role == conversation.RoleRoot {
		return conversation.Message{}, errors.New("summary: 会话根节点之前无处可插")
	}
	chain, ok := ancestorPath(c, target)
	if !ok || len(chain) <= 1 {
		return conversation.Message{}, ErrNoHistoryAbove
	}
	// 可总结增量 = 水位之后的非摘要节点；为 0 表示祖先链上只有 persona（或水位之后无内容）。
	_, watermarkIdx := waterline(chain)
	start := 1
	if watermarkIdx >= 0 {
		start = watermarkIdx
	}
	fresh := 0
	for i := start; i < len(chain); i++ {
		if chain[i].Role != conversation.RoleSystem {
			fresh++
		}
	}
	if fresh == 0 {
		return conversation.Message{}, ErrNoHistoryAbove
	}

	history, err := assemblePath(ctx, chain, a.assembleOpts())
	if err != nil {
		return conversation.Message{}, fmt.Errorf("assemble summary input: %w", err)
	}
	mid := a.ids.MessageID() // 预分配：流事件关联 ID 与入树节点 ID 同一
	text, usage, err := a.generateSummary(ctx, mid, history, watermarkIdx >= 0)
	if err != nil {
		return conversation.Message{}, err
	}
	pad := conversation.Message{
		ID:        mid,
		Role:      conversation.RoleSystem,
		Content:   []conversation.Part{{Kind: conversation.PartText, Text: text}},
		Outcome:   conversation.OutcomeDone,
		Model:     a.modelName(),
		Usage:     usage,
		CreatedAt: a.clock.Now(),
	}
	node, err := c.InsertCommitted(target, pad)
	if err != nil {
		return conversation.Message{}, fmt.Errorf("insert summary node: %w", err)
	}
	_ = a.ui.Emit(ctx, port.CommittedEvent{Message: node})
	return node, nil
}

// ancestorPath 沿 Parent 回溯取 Root→id 的线性序列（含 Root、**不含 id 自身**）。
// id 不在树中 / 链断裂时 ok=false（树不变量保证不会断裂）。
func ancestorPath(c *conversation.Conversation, id conversation.MessageID) ([]conversation.Message, bool) {
	m, ok := c.Find(id)
	if !ok {
		return nil, false
	}
	var rev []conversation.Message
	for cur := m.Parent; cur != ""; {
		p, ok := c.Find(cur)
		if !ok {
			return nil, false
		}
		rev = append(rev, p)
		cur = p.Parent
	}
	out := make([]conversation.Message, 0, len(rev))
	for i := len(rev) - 1; i >= 0; i-- {
		out = append(out, rev[i])
	}
	return out, true
}
