package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/Tonyjh07/Aquarius/internal/domain/conversation"
	"github.com/Tonyjh07/Aquarius/internal/domain/tool"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

// execTool 执行工具：工具级失败由 Runner 转为 OK=false 结果照常回填（§10：不中断 Turn）；
// 返回 error 仅限装配级错误（确认器缺失/报错、父 ctx 取消等基础设施故障），
// 由 Run 快速失败上抛（§14：不让模型空转到 MaxTurns）。
func (a *Agent) execTool(ctx context.Context, call tool.Call) (tool.Result, error) {
	if a.tools == nil {
		// 未声明工具却收到 tool_calls（嵌入方/配置缺陷）：按装配级错误中止，
		// 不让空指针 panic、也不回填让模型空转。
		return tool.Result{}, errors.New("no tool executor configured (Deps.Tools)")
	}
	res, err := a.tools.Execute(ctx, call)
	if err != nil {
		return tool.Result{}, err
	}
	if res.CallID == "" {
		res.CallID = call.ID
	}
	return res, nil
}

// runTools 执行本轮全部调用（D95 先执行后提交）：ToolCallEvent → 执行 → ToolResultEvent，
// 结果作为工具分片按到达序追加进 Turn 缓冲。
// 工具级失败由 Runner 转 OK=false 结果照常回填（§10：不中断 Turn）；
// 返回 error 仅限装配级错误（确认器缺失/报错、父 ctx 取消等基础设施故障），
// 由 Run 在提交后快速失败上抛（§14：不让模型空转到 MaxTurns）。
func (a *Agent) runTools(ctx context.Context, buf *turnBuffer, mid conversation.MessageID, calls []tool.Call) error {
	for i, call := range calls {
		_ = a.ui.Emit(ctx, port.ToolCallEvent{MessageID: mid, Call: call})
		res, err := a.execTool(ctx, call)
		if err != nil {
			// 装配级错误：先公告尚未呈现的调用，再连同本调用一并补失败结果
			// （保持调用与结果一一配对，树下次装配仍可发送），随后中止本轮。
			for _, rest := range calls[i+1:] {
				_ = a.ui.Emit(ctx, port.ToolCallEvent{MessageID: mid, Call: rest})
			}
			cause := fmt.Errorf("tool %s failed: %w", call.Name, err)
			a.abortToolCalls(ctx, buf, calls[i:], cause)
			return cause
		}
		buf.addToolPart(call, &res)
		_ = a.ui.Emit(ctx, port.ToolResultEvent{Result: res})
	}
	return nil
}

// abortToolCalls 装配级工具故障的收尾：为未完成的调用补 OK=false 结果分片
// （与调用一一配对，树下次装配仍可发送，D95）。结果填充与事件发射不产生错误。
// 与 §10 的"工具失败不中断 Turn"不同——那指的是工具级失败。
func (a *Agent) abortToolCalls(ctx context.Context, buf *turnBuffer, calls []tool.Call, cause error) {
	for _, call := range calls {
		res := tool.Result{CallID: call.ID, OK: false, Err: cause.Error()}
		buf.addToolPart(call, &res)
		_ = a.ui.Emit(ctx, port.ToolResultEvent{Result: res})
	}
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
