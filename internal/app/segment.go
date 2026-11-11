package app

// segment.go 轮内切段（D273）：把「用户在两段回答之间插的一句话」落成树里真的两个角色。
//
// 切段只做一件有后果的事——把当前缓冲提交成一个 assistant 节点，然后把引导消息按到达序
// 追加成 user 节点，再开一个新缓冲继续跑本轮。于是转写区的顺序天然是
// 「回答前半 → 我说的那句 → 接着跑的回答」，而树里三者的父子关系也是真的：
// 编辑、重放、分叉都按普通节点走，不需要任何「装配时投影」的特殊口径。

import (
	"context"
	"fmt"

	"github.com/Tonyjh07/Aquarius/internal/domain/conversation"
	"github.com/Tonyjh07/Aquarius/internal/i18n"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

// takeSteers 取走清单里全部引导（保持插入序）；没有清单或没有引导 = nil。
func (a *Agent) takeSteers() []port.PendingInput {
	if a.pending == nil {
		return nil
	}
	return a.pending.takeSteer()
}

// cutSegment 切段：有引导才真的切。返回新的段 ID 与新缓冲；cut=false 表示原样返回
// （没有引导，或本轮已被取消——取消之后不该再开新段）。
//
// 缓冲为空（这一段没产出任何正文/工具/思考）时不提交空节点，直接接 user 节点。
// 提交顺序固定为 assistant 段 → user 节点：前端照事件序追加即可。
func (a *Agent) cutSegment(ctx context.Context, c *conversation.Conversation, mid conversation.MessageID, buf *turnBuffer) (conversation.MessageID, *turnBuffer, bool, error) {
	if ctx.Err() != nil {
		return mid, buf, false, nil
	}
	steers := a.takeSteers()
	if len(steers) == 0 {
		return mid, buf, false, nil
	}
	if !buf.empty() {
		if _, err := a.finishTurn(ctx, c, buf, conversation.OutcomeDone); err != nil {
			return mid, buf, false, fmt.Errorf("%s: %w", i18n.T("agent.err_commit_segment"), err)
		}
	}
	for _, st := range steers {
		if _, err := a.appendUserNode(ctx, c, st.Text); err != nil {
			return mid, buf, false, err
		}
	}
	next := a.ids.MessageID()
	return next, newTurnBuffer(next), true, nil
}

// appendUserNode 追加一个 user 节点并广播（轮内切段与轮末队列续跑共用）。
//
// 这条用户消息从来没经过前端回显（打字时一轮正在跑，前端把它放进了清单），所以它必须
// 走 CommittedEvent——否则转写区里那句话凭空消失。常态输入相反：前端先本地回显，
// 事件到达时按「最早未盖章且文本一致」回填 ID，不会画两遍。
func (a *Agent) appendUserNode(ctx context.Context, c *conversation.Conversation, text string) (conversation.Message, error) {
	a.noteDateChange(ctx, c) // 跨过午夜就先补一条日期说明，位置恒在这句话之前
	node := conversation.Message{
		ID:        a.ids.MessageID(),
		Parent:    c.Head,
		Role:      conversation.RoleUser,
		Content:   []conversation.Part{{Kind: conversation.PartText, Text: text}},
		CreatedAt: a.clock.Now(),
	}
	if err := c.AppendCommitted(node); err != nil {
		return conversation.Message{}, fmt.Errorf("%s: %w", i18n.T("agent.err_append_pending"), err)
	}
	_ = a.ui.Emit(ctx, port.CommittedEvent{Message: node})
	return node, nil
}
