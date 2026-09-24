// Package app 应用层：Turn 循环（唯一的编排）、上下文装配、会话命令（DESIGN §7）。
//
// 依赖方向：adapter → port ← app → domain。app 只依赖 port 与 domain，
// 具体适配器（LLM/存储/UI）由装配根 cmd/aquarius 注入。
package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/Tonyjh07/Aquarius/internal/domain/conversation"
	"github.com/Tonyjh07/Aquarius/internal/domain/tool"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

// ErrMaxTurns 单次 Run 的"生成 + 工具"循环次数用尽（DESIGN §7.1）。
var ErrMaxTurns = errors.New("agent: 超过最大轮次数")

// defaultMaxTurns Config.MaxTurns <= 0 时的默认值（DESIGN §8 limits.max_turns）。
const defaultMaxTurns = 8

// Deps Agent 依赖（全部端口，测试注入替身）。
type Deps struct {
	LLM   port.LLM
	UI    port.Presenter
	IDs   port.IDGen
	Clock port.Clock
	// Tools 为 nil = 无工具（M0）；Blobs 为 nil = 图片以内联占位代替（附件库 M3）。
	Tools port.ToolRunner
	Blobs port.AttachmentStore
}

// Config Agent 行为配置。
type Config struct {
	Model    string
	System   string // 空 = 内置默认 system 提示
	Sampling port.Sampling
	Budget   port.TokenBudget
	MaxTurns int // <=0 = 8
}

// Agent Turn 循环：一次"模型生成 + 0..n 次工具执行"（DESIGN §7.1，内核唯一的编排）。
type Agent struct {
	llm      port.LLM
	ui       port.Presenter
	ids      port.IDGen
	clock    port.Clock
	tools    port.ToolRunner
	blobs    port.AttachmentStore
	cfg      Config
	system   string
	maxTurns int
}

// New 创建 Agent 并校验必需依赖。
func New(d Deps, cfg Config) (*Agent, error) {
	switch {
	case d.LLM == nil:
		return nil, errors.New("agent: LLM 依赖为空")
	case d.UI == nil:
		return nil, errors.New("agent: Presenter 依赖为空")
	case d.IDs == nil:
		return nil, errors.New("agent: IDGen 依赖为空")
	case d.Clock == nil:
		return nil, errors.New("agent: Clock 依赖为空")
	case strings.TrimSpace(cfg.Model) == "":
		return nil, errors.New("agent: model 为空")
	}
	system := strings.TrimSpace(cfg.System)
	if system == "" {
		system = defaultSystem
	}
	maxTurns := cfg.MaxTurns
	if maxTurns <= 0 {
		maxTurns = defaultMaxTurns
	}
	return &Agent{
		llm:      d.LLM,
		ui:       d.UI,
		ids:      d.IDs,
		clock:    d.Clock,
		tools:    d.Tools,
		blobs:    d.Blobs,
		cfg:      cfg,
		system:   system,
		maxTurns: maxTurns,
	}, nil
}

// Run 从当前 Head 出发执行一轮 Turn（DESIGN §7.1 / §10）：
//   - 流式增量只经 Presenter 进 UI，每次生成结束一次性 Commit 不可变节点（D3）；
//     节点 ID 在 Turn 开始时预分配，作流事件关联 ID。
//   - 工具失败转 OK=false 照常回填，不中断 Turn。
//   - 取消 = 已生成部分以 Outcome: cancelled 提交后返回 nil（可 /edit 重试）；
//     模型/网络错误 = Outcome: error 提交 + ErrorEvent 上抛。
func (a *Agent) Run(ctx context.Context, c *conversation.Conversation) error {
	if c == nil {
		return errors.New("agent: nil conversation")
	}
	if len(c.Path()) == 0 {
		return errors.New("agent: 会话为空，无可生成的上下文")
	}

	for turn := 0; turn < a.maxTurns; turn++ {
		req, err := a.buildRequest(ctx, c)
		if err != nil {
			return a.emitError(ctx, fmt.Errorf("装配上下文: %w", err))
		}
		mid := a.ids.MessageID() // 预分配关联 ID
		buf := &commitBuffer{id: mid, parent: c.Head}

		stream, err := a.llm.Generate(ctx, req)
		if err != nil {
			return a.commitFailure(ctx, c, buf, fmt.Errorf("发起生成: %w", err))
		}
		calls, recvErr := a.consume(ctx, stream, buf, mid)
		calls = a.normalizeCalls(calls)
		if recvErr == nil && ctx.Err() != nil {
			recvErr = ctx.Err() // 断流恰好落在取消瞬间：以取消为准
		}
		outcome := conversation.OutcomeDone
		switch {
		case recvErr == nil:
		case errors.Is(recvErr, context.Canceled), errors.Is(recvErr, context.DeadlineExceeded):
			outcome = conversation.OutcomeCancelled
		default:
			outcome = conversation.OutcomeError
		}
		node := buf.commit(a.clock.Now(), outcome, a.cfg.Model, calls)
		if err := c.AppendCommitted(node); err != nil {
			return a.emitError(ctx, fmt.Errorf("提交节点: %w", err))
		}
		_ = a.ui.Emit(ctx, port.CommittedEvent{Message: node})

		if recvErr != nil {
			if outcome == conversation.OutcomeCancelled {
				return nil // 取消已提交，不视为失败
			}
			return a.emitError(ctx, fmt.Errorf("生成失败: %w", recvErr))
		}
		if len(node.ToolCalls) == 0 {
			return nil
		}

		for _, call := range node.ToolCalls {
			_ = a.ui.Emit(ctx, port.ToolCallEvent{MessageID: mid, Call: call})
			res := a.execTool(ctx, call)
			tnode := conversation.Message{
				ID:         a.ids.MessageID(),
				Parent:     c.Head,
				Role:       conversation.RoleTool,
				ToolResult: &res,
				CreatedAt:  a.clock.Now(),
			}
			if err := c.AppendCommitted(tnode); err != nil {
				return a.emitError(ctx, fmt.Errorf("提交工具结果: %w", err))
			}
			_ = a.ui.Emit(ctx, port.ToolResultEvent{Result: res})
		}
	}
	return a.emitError(ctx, fmt.Errorf("%w（%d）", ErrMaxTurns, a.maxTurns))
}

// buildRequest 装配本轮请求：一条 system 提示 + Path 全量 + 工具清单（DESIGN §7.1）。
// 记忆索引自 M2 起并入 system（此处先留静态提示）。
func (a *Agent) buildRequest(ctx context.Context, c *conversation.Conversation) (port.GenerateRequest, error) {
	path, err := assemblePath(ctx, c.Path(), a.blobs)
	if err != nil {
		return port.GenerateRequest{}, err
	}
	msgs := make([]port.PromptMessage, 0, len(path)+1)
	msgs = append(msgs, port.PromptMessage{
		Role:    "system",
		Content: []port.PromptPart{{Kind: "text", Text: a.system}},
	})
	msgs = append(msgs, path...)

	req := port.GenerateRequest{
		Model:    a.cfg.Model,
		Messages: msgs,
		Params:   a.cfg.Sampling,
		Budget:   a.cfg.Budget,
	}
	if a.tools != nil {
		specs, err := a.tools.Specs(ctx)
		if err != nil {
			return port.GenerateRequest{}, fmt.Errorf("工具清单: %w", err)
		}
		req.Tools = specs
	}
	return req, nil
}

// consume 边收边发 DeltaEvent（只进 UI），返回聚合后的工具调用。
func (a *Agent) consume(ctx context.Context, stream port.Stream, buf *commitBuffer, mid conversation.MessageID) ([]tool.Call, error) {
	defer stream.Close()
	for {
		d, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, err // 半截工具调用不带出（未完成调用不能进树）
		}
		buf.add(d)
		_ = a.ui.Emit(ctx, port.DeltaEvent{MessageID: mid, Delta: d})
	}
	return buf.finalize(), nil
}

// execTool 执行工具；错误转为 OK=false 结果照常回填（§10：失败不中断 Turn）。
func (a *Agent) execTool(ctx context.Context, call tool.Call) tool.Result {
	res, err := a.tools.Execute(ctx, call)
	if err != nil {
		return tool.Result{CallID: call.ID, OK: false, Err: err.Error()}
	}
	if res.CallID == "" {
		res.CallID = call.ID
	}
	return res
}

// normalizeCalls 补齐/去重调用 ID（个别兼容服务不回传 ID），并给空参数补 {}。
func (a *Agent) normalizeCalls(calls []tool.Call) []tool.Call {
	if len(calls) == 0 {
		return nil
	}
	seen := map[tool.CallID]bool{}
	for i := range calls {
		if calls[i].ID == "" || seen[calls[i].ID] {
			calls[i].ID = a.ids.CallID()
		}
		seen[calls[i].ID] = true
		if len(calls[i].Args) == 0 {
			calls[i].Args = json.RawMessage(`{}`)
		}
	}
	return calls
}

// commitFailure 以 Outcome: error 提交节点并上抛 UI（§10）。
func (a *Agent) commitFailure(ctx context.Context, c *conversation.Conversation, buf *commitBuffer, cause error) error {
	node := buf.commit(a.clock.Now(), conversation.OutcomeError, a.cfg.Model, nil)
	if err := c.AppendCommitted(node); err != nil {
		_ = a.ui.Emit(ctx, port.ErrorEvent{Err: cause})
		return fmt.Errorf("%w（提交失败: %v）", cause, err)
	}
	_ = a.ui.Emit(ctx, port.CommittedEvent{Message: node})
	return a.emitError(ctx, cause)
}

// emitError 把错误上抛 UI 并原样返回。
func (a *Agent) emitError(ctx context.Context, err error) error {
	_ = a.ui.Emit(ctx, port.ErrorEvent{Err: err})
	return err
}

// commitBuffer 流式缓冲：只进 UI，Turn 结束一次性 Commit（D3）。
type commitBuffer struct {
	id     conversation.MessageID
	parent conversation.MessageID
	text   strings.Builder
	calls  callAssembler
	usage  conversation.Usage
}

// add 累积一个增量。
func (b *commitBuffer) add(d port.Delta) {
	b.text.WriteString(d.Text)
	b.calls.add(d.ToolCalls)
	if d.Usage != nil {
		b.usage = *d.Usage
	}
}

// finalize 返回聚合后的工具调用。
func (b *commitBuffer) finalize() []tool.Call { return b.calls.finalize() }

// commit 组装终态节点。非 done 终态丢弃半截工具调用——未完成的调用若进树，
// 后续装配会产出"无应答的 tool_calls"被服务端拒（DESIGN §10 取消提交语义）。
func (b *commitBuffer) commit(now time.Time, outcome conversation.Outcome, model string, calls []tool.Call) conversation.Message {
	var content []conversation.Part
	if b.text.Len() > 0 {
		content = []conversation.Part{{Kind: conversation.PartText, Text: b.text.String()}}
	}
	if outcome != conversation.OutcomeDone {
		calls = nil
	}
	return conversation.Message{
		ID:        b.id,
		Parent:    b.parent,
		Role:      conversation.RoleAssistant,
		Content:   content,
		ToolCalls: calls,
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
