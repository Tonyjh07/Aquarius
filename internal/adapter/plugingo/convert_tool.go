package plugingo

import (
	"bytes"

	"github.com/Tonyjh07/Aquarius/internal/domain/perm"
	"github.com/Tonyjh07/Aquarius/internal/domain/tool"
	"github.com/Tonyjh07/Aquarius/internal/port"
	v1 "github.com/Tonyjh07/Aquarius/pluginapi"
)

// convert_tool.go 工具面与确认链的转换（plugin-design.md §3.1）。
//
// 方向上只有一条准则：插件拿到的是**已经裁决过**的调用（确认、超时、裁剪都由内核
// ToolRunner 包在外面），所以这里的入参不带确认信息，出参也不带「是否允许」。

// toV1ToolCall 裁决后的调用 → 插件看到的调用。Args 原样（透传不解读）。
func toV1ToolCall(c tool.Call) v1.ToolCall {
	return v1.ToolCall{ID: string(c.ID), Name: c.Name, Args: bytes.Clone(c.Args)}
}

// toPortToolResult 插件交回的结果。失败原因填 Err 而不是让插件返 error：error 表示
// 「这次调用根本没跑起来」，模型看得见的失败要看 Output/Err（§10）。
func toPortToolResult(callID tool.CallID, r v1.ToolResult) tool.Result {
	out := tool.Result{
		CallID:   callID,
		OK:       r.OK,
		Output:   r.Output,
		Err:      r.Err,
		Fallback: r.Fallback,
	}
	if len(r.Images) > 0 {
		out.Images = make([]tool.ImageRef, 0, len(r.Images))
		for _, im := range r.Images {
			out.Images = append(out.Images, tool.ImageRef{
				Hash: im.Hash, MIME: im.MIME, Name: im.Name, Size: im.Size,
			})
		}
	}
	return out
}

// toPortToolSpec 插件声明的工具 → 内核工具声明（反向，出参侧用）。
// Describe 留空：插件若要提供确认摘要，宿主在装配期按 Describer 断言补上（D320）。
func toPortToolSpec(s v1.ToolSpec) tool.Spec {
	return tool.Spec{
		Name:        s.Name,
		Description: s.Description,
		Schema:      bytes.Clone(s.Schema),
		Risk:        toPortRisk(s.Risk),
	}
}

// toPortConfirmRequest 插件发起的确认 → 内核确认链的请求。Op / Risk 各有自己的 Has* 位，
// 零值不兼作「不适用」：插件说 Op 为空、HasOp=true 是不合法的组合，按「不涉及」处理。
func toPortConfirmRequest(r v1.ConfirmRequest) port.ConfirmRequest {
	out := port.ConfirmRequest{
		Origin:     port.ConfirmOrigin(r.Origin),
		Tool:       r.Tool,
		HasOp:      r.HasOp && r.Op != "",
		Target:     r.Target,
		HasRisk:    r.HasRisk && r.Risk != "",
		Task:       r.Task,
		Prompt:     r.Prompt,
		CanPersist: r.CanPersist,
	}
	if out.HasOp && r.Op == v1.OpWrite {
		out.Op = perm.OpWrite
	}
	if out.HasRisk && r.Risk == v1.RiskConfirm {
		out.Risk = tool.Confirm
	}
	return out
}

// toV1ConfirmAnswer 确认应答。Level 是用户直选的权限档（字符串透传，档位词表由权限侧认）；
// Persist 只表达「别再问这个」，范围由判定侧按工具性质算。
func toV1ConfirmAnswer(a port.ConfirmAnswer) v1.ConfirmAnswer {
	return v1.ConfirmAnswer{Allow: a.Allow, Reason: a.Reason, Level: a.Level, Persist: a.Persist}
}
