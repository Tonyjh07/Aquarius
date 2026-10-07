package app

import (
	"context"
	"errors"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/Tonyjh07/Aquarius/internal/domain/conversation"
	"github.com/Tonyjh07/Aquarius/internal/domain/tool"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

// consume 边收边发 DeltaEvent（只进 UI），返回聚合后的工具调用。
// Reasoning 分片同时进 commit buffer 的思考缓冲，随节点提交入树（D42）。
func (a *Agent) consume(ctx context.Context, stream port.Stream, buf *turnBuffer, mid conversation.MessageID) ([]tool.Call, error) {
	defer stream.Close()
	for {
		d, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, err // 半截工具调用不带出（未完成调用不能进树）
		}
		buf.add(d) // Reasoning → 思考缓冲（D42），其余 → 当前轮正文/调用（D3）
		_ = a.ui.Emit(ctx, port.DeltaEvent{MessageID: mid, Delta: d})
	}
	return buf.takeCalls(), nil
}

// turnBuffer 流式缓冲（D95 Turn 粒度）：一次 Turn（可含多轮"生成+工具"循环）只提交
// 一个不可变节点。已定稿段存 content（按到达序交错：思考/正文段与工具分片）；当前轮
// 增量进 text/thinking builder，轮末 sealRound 定稿；usage 逐轮累计（roundUsage 供校准）。
type turnBuffer struct {
	id       conversation.MessageID
	content  []conversation.Part
	text     strings.Builder    // 当前轮正文
	thinking strings.Builder    // 当前轮思考
	calls    callAssembler      // 当前轮调用聚合（takeCalls 后复位，轮界隔离）
	usage    conversation.Usage // Turn 累计（各轮流末 usage 求和）
	round    conversation.Usage // 当前轮流末 usage（校准用，sealRound 并入 usage）
}

// newTurnBuffer 以 Turn 预分配的关联 ID 建缓冲。
func newTurnBuffer(id conversation.MessageID) *turnBuffer { return &turnBuffer{id: id} }

// add 累积一个增量（按 Reasoning 分流当前轮思考/正文；usage 记当前轮流末值）。
// 工具调用与文本来源无关，恒聚合——推理分片上若捎带调用分片同样不得丢（审查修复）。
func (b *turnBuffer) add(d port.Delta) {
	if d.Reasoning {
		b.thinking.WriteString(d.Text)
	} else {
		b.text.WriteString(d.Text)
	}
	b.calls.add(d.ToolCalls)
	if d.Usage != nil {
		b.round = *d.Usage
	}
}

// takeCalls 返回当前轮聚合的调用并复位聚合器（轮界隔离：下一轮分片不得混入）。
func (b *turnBuffer) takeCalls() []tool.Call {
	c := b.calls.finalize()
	b.calls = callAssembler{}
	return c
}

// sealRound 把当前轮的思考/正文定稿进 content（幂等：builder 空时 no-op），
// 并把本轮 usage 并入 Turn 累计。思考段在前、正文段在后（D42 段内序）。
func (b *turnBuffer) sealRound() {
	if b.thinking.Len() > 0 {
		b.content = append(b.content, conversation.Part{Kind: conversation.PartThinking, Text: b.thinking.String()})
		b.thinking.Reset()
	}
	if b.text.Len() > 0 {
		b.content = append(b.content, conversation.Part{Kind: conversation.PartText, Text: b.text.String()})
		b.text.Reset()
	}
	b.usage.InputTokens += b.round.InputTokens
	b.usage.OutputTokens += b.round.OutputTokens
	b.usage.CostUSD += b.round.CostUSD
	b.round = conversation.Usage{}
}

// addToolPart 把一个"调用+结果"作为工具分片按到达序追加进 content（D95 同片落库）。
func (b *turnBuffer) addToolPart(call tool.Call, res *tool.Result) {
	b.content = append(b.content, conversation.Part{Kind: conversation.PartTool, Tool: &conversation.ToolPart{
		CallID: string(call.ID),
		Name:   call.Name,
		Args:   call.Args,
		Result: res,
	}})
}

// empty 缓冲是否没有任何可提交内容。
func (b *turnBuffer) empty() bool {
	return len(b.content) == 0 && b.text.Len() == 0 && b.thinking.Len() == 0
}

// inFlight 未提交缓冲的只读投影（轮间 buildRequest 用）：合成 assistant 节点，
// 不进树、仅本次请求可见；已定稿段 + 当前轮 builder（防御性并入，正常轮界为空）。
func (b *turnBuffer) inFlight() conversation.Message {
	parts := make([]conversation.Part, len(b.content), len(b.content)+2)
	copy(parts, b.content)
	if b.thinking.Len() > 0 {
		parts = append(parts, conversation.Part{Kind: conversation.PartThinking, Text: b.thinking.String()})
	}
	if b.text.Len() > 0 {
		parts = append(parts, conversation.Part{Kind: conversation.PartText, Text: b.text.String()})
	}
	return conversation.Message{ID: b.id, Role: conversation.RoleAssistant, Content: parts}
}

// commit 组装 Turn 终态节点（每 Turn 恰一次）：parent 取提交时的 Head——mid-turn 的
// compact 摘要会前移 Head，节点挂到最新 Head 之下保持路径线性。收场轮的半截思考/正文
// 经 sealRound 幂等定稿；半截工具调用从未进缓冲（非 done 终态不执行工具，§10）。
func (b *turnBuffer) commit(parent conversation.MessageID, now time.Time, outcome conversation.Outcome, model string) conversation.Message {
	b.sealRound()
	return conversation.Message{
		ID:        b.id,
		Parent:    parent,
		Role:      conversation.RoleAssistant,
		Content:   b.content,
		Outcome:   outcome,
		Model:     model,
		Usage:     b.usage,
		CreatedAt: now,
	}
}

// callAssembler 按 Index 聚合流式工具调用分片（port.ToolCallDelta → tool.Call）。
type callAssembler struct {
	order   []int
	byIndex map[int]*tool.Call
}

// add 累积一批分片。
func (a *callAssembler) add(deltas []port.ToolCallDelta) {
	if len(deltas) == 0 {
		return
	}
	if a.byIndex == nil {
		a.byIndex = map[int]*tool.Call{}
	}
	for _, d := range deltas {
		c, ok := a.byIndex[d.Index]
		if !ok {
			c = &tool.Call{}
			a.byIndex[d.Index] = c
			a.order = append(a.order, d.Index)
		}
		if d.ID != "" {
			c.ID = tool.CallID(d.ID)
		}
		if d.Name != "" {
			c.Name = d.Name
		}
		if d.ArgsDelta != "" {
			c.Args = append(c.Args, d.ArgsDelta...)
		}
	}
}

// finalize 按 Index 升序返回聚合结果。
func (a *callAssembler) finalize() []tool.Call {
	if len(a.order) == 0 {
		return nil
	}
	idx := append([]int(nil), a.order...)
	sort.Ints(idx)
	out := make([]tool.Call, 0, len(idx))
	for _, i := range idx {
		out = append(out, *a.byIndex[i])
	}
	return out
}
