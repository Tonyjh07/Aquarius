package app

import (
	"context"
	"errors"
	"fmt"

	"github.com/Tonyjh07/Aquarius/internal/domain/conversation"
	"github.com/Tonyjh07/Aquarius/internal/domain/tool"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

// convKey 当前会话对象的私有 ctx 键（app 内部；工具执行期随 ctx 传递）。
// 只给 app 自己的 context_compact 用——会话树对象不出 app（插件/适配器只见
// port.SessionIDFrom 的 ID 值拷贝，硬性规则 6）。
type convKey struct{}

// withConversation 注入当前会话（Agent.Run 执行工具前调用）。
func withConversation(ctx context.Context, c *conversation.Conversation) context.Context {
	return context.WithValue(ctx, convKey{}, c)
}

// conversationFrom 取出当前会话；不在工具执行上下文中时 ok=false。
func conversationFrom(ctx context.Context) (*conversation.Conversation, bool) {
	c, ok := ctx.Value(convKey{}).(*conversation.Conversation)
	return c, ok && c != nil
}

// ContextCompactTool 返回 context_compact 工具（DESIGN §4.3，D21 轨3）：
// 模型自我管理上下文，等价 /compact——经 Run 注入的会话对象触发 Compact，
// 摘要以 system 水位节点入树，同轮后续装配即用新水位。
// 实现放在 app：它编排的是 app 自己的 Compact（经 port.Tool 契约接入 runner，D13 不破）。
func (a *Agent) ContextCompactTool() port.Tool { return &contextCompactTool{a: a} }

// contextCompactTool 触发上下文压缩的内置工具。
type contextCompactTool struct{ a *Agent }

var (
	_ port.Tool = (*contextCompactTool)(nil)
)

func (t *contextCompactTool) Spec() tool.Spec {
	return tool.Spec{
		Name: "context_compact",
		Description: "Trigger a context compaction (equivalent to /compact): transcribe the current history into a single " +
			"summary watermark node, above which older history is no longer sent back. Call it when the context grows too long " +
			"or you want to deliberately drop mid-conversation details.",
		Schema: jsonSchemaEmptyObject(),
		Risk:   tool.Safe,
	}
}

func (t *contextCompactTool) Execute(ctx context.Context, _ tool.Call) (tool.Result, error) {
	c, ok := conversationFrom(ctx)
	if !ok {
		return tool.Result{OK: false, Err: "no current conversation context (context_compact only runs inside a conversation turn)"}, nil
	}
	node, absorbed, err := t.a.Compact(ctx, c)
	if errors.Is(err, ErrNothingToCompact) {
		return tool.Result{OK: true, Output: "nothing to compact (no new content above the summary)"}, nil
	}
	if err != nil {
		return tool.Result{}, err
	}
	return tool.Result{
		OK: true,
		Output: fmt.Sprintf("compacted %d history messages into 1 summary (in=%d out=%d tokens)",
			absorbed, node.Usage.InputTokens, node.Usage.OutputTokens),
	}, nil
}

// jsonSchemaEmptyObject 无参工具的空参数 Schema。
func jsonSchemaEmptyObject() []byte { return []byte(`{"type":"object","properties":{}}`) }
