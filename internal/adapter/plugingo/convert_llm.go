package plugingo

import (
	"bytes"
	"encoding/json"

	"github.com/Tonyjh07/Aquarius/internal/domain/conversation"
	"github.com/Tonyjh07/Aquarius/internal/domain/tool"
	"github.com/Tonyjh07/Aquarius/internal/port"
	v1 "github.com/Tonyjh07/Aquarius/pluginapi"
)

// convert_llm.go 模型面的转换（plugin-design.md §1.2）：请求出去（内核 → 插件）、
// 增量回来（插件 → 内核），加模型清单与探测结论。
//
// 请求方向要格外小心三件：图片字节已由内核解析完（不是路径）；`ReasoningSignature` 与
// `ServerBlocks` 是透传字节（原样保真，逐轮回传）；`ServerTools` 非空时 `Tools` 必须为空
// ——协议里两者互斥，转换层顺便把这条不变量守住（违反时清空 Tools 并让调用方报因）。

// toV1Request 装配后的请求 → 插件看到的请求。
func toV1Request(req port.GenerateRequest) v1.GenerateRequest {
	out := v1.GenerateRequest{
		Model:       req.Model,
		Params:      toV1Sampling(req.Params),
		Budget:      v1.TokenBudget{MaxOutputTokens: req.Budget.MaxOutputTokens, MaxCostUSD: req.Budget.MaxCostUSD},
		Title:       req.Title,
		ServerTools: append([]string(nil), req.ServerTools...),
	}
	out.Messages = make([]v1.PromptMessage, 0, len(req.Messages))
	for _, m := range req.Messages {
		out.Messages = append(out.Messages, toV1Message(m))
	}
	// 服务端工具与本地 function tools 互斥：非空时不给 Tools，免得插件按协议硬发被 400。
	if len(out.ServerTools) == 0 {
		out.Tools = make([]v1.ToolSpec, 0, len(req.Tools))
		for _, s := range req.Tools {
			out.Tools = append(out.Tools, toV1ToolSpec(s))
		}
	}
	return out
}

// toV1Message 一条消息。
func toV1Message(m port.PromptMessage) v1.PromptMessage {
	out := v1.PromptMessage{
		Role:               v1.PromptRole(m.Role),
		Reasoning:          m.Reasoning,
		ReasoningSignature: m.ReasoningSignature,
		CallID:             m.CallID,
	}
	out.Content = make([]v1.PromptPart, 0, len(m.Content))
	for _, p := range m.Content {
		out.Content = append(out.Content, v1.PromptPart{
			Kind:   v1.PromptKind(p.Kind),
			Text:   p.Text,
			MIME:   p.MIME,
			Data:   bytes.Clone(p.Data), // 插件可能留存，不与内核缓冲共享底层数组
			FileID: p.FileID,
		})
	}
	if len(m.ToolCalls) > 0 {
		out.ToolCalls = make([]v1.ToolCall, 0, len(m.ToolCalls))
		for _, c := range m.ToolCalls {
			out.ToolCalls = append(out.ToolCalls, v1.ToolCall{
				ID:   string(c.ID),
				Name: c.Name,
				Args: bytes.Clone(c.Args),
			})
		}
	}
	for _, raw := range m.ServerBlocks {
		out.ServerBlocks = append(out.ServerBlocks, bytes.Clone(raw))
	}
	return out
}

// toV1Sampling 采样参数。指针字段要另开新指针：直接把内核的指针交出去，插件写一下就
// 改到了本轮装配用的那份配置。
func toV1Sampling(s port.Sampling) v1.Sampling {
	out := v1.Sampling{
		MaxTokens:       s.MaxTokens,
		Stop:            append([]string(nil), s.Stop...),
		ReasoningEffort: s.ReasoningEffort,
	}
	out.Temperature = cloneFloat(s.Temperature)
	out.TopP = cloneFloat(s.TopP)
	if s.Thinking != nil {
		v := *s.Thinking
		out.Thinking = &v
	}
	return out
}

func cloneFloat(p *float64) *float64 {
	if p == nil {
		return nil
	}
	v := *p
	return &v
}

// toPortDelta 插件的增量 → 内核增量。七项语义一一对齐（D42 / D175 / D309 / D310）。
func toPortDelta(d v1.Delta) port.Delta {
	out := port.Delta{
		Text:               d.Text,
		Reasoning:          d.Reasoning,
		ReasoningSignature: string(d.ReasoningSignature), // 透传：v1 用字节，内核用带前缀的串
		Title:              d.Title,
	}
	if d.Usage != nil {
		u := toPortUsage(*d.Usage)
		out.Usage = &u
	}
	if len(d.ToolCalls) > 0 {
		out.ToolCalls = make([]port.ToolCallDelta, 0, len(d.ToolCalls))
		for _, c := range d.ToolCalls {
			out.ToolCalls = append(out.ToolCalls, port.ToolCallDelta{
				Index:     c.Index,
				ID:        c.ID,
				Name:      c.Name,
				ArgsDelta: c.ArgsDelta,
			})
		}
	}
	if d.Server != nil {
		out.Server = &port.ServerBlock{
			Tool:  d.Server.Tool,
			Kind:  d.Server.Kind,
			URL:   d.Server.URL,
			Title: d.Server.Title,
			Raw:   json.RawMessage(bytes.Clone(d.Server.Raw)),
		}
	}
	return out
}

// toPortUsage 用量。InputTokens 是总输入（含缓存两段，D308 对外口径），CostUSD 不进 v1
// ——成本折算归内核，插件只报 token。
func toPortUsage(u v1.Usage) conversation.Usage {
	return conversation.Usage{
		InputTokens:  u.InputTokens,
		OutputTokens: u.OutputTokens,
		CacheRead:    u.CacheRead,
		CacheWrite:   u.CacheWrite,
		Thinking:     u.Thinking,
		Safety:       u.Safety,
	}
}

// toV1ModelInfo 模型清单条目。
func toV1ModelInfo(m port.ModelInfo) v1.ModelInfo {
	return v1.ModelInfo{
		Name:          m.Name,
		Caps:          v1.ModelCaps{Tools: m.Caps.Tools, Reasoning: m.Caps.Reasoning, Vision: m.Caps.Vision},
		ContextWindow: m.ContextWindow,
		CostIn:        m.CostIn,
		CostOutUSD:    m.CostOutUSD,
	}
}

// toPortModelInfo 反向：插件报上来的模型进降级链。
func toPortModelInfo(m v1.ModelInfo) port.ModelInfo {
	return port.ModelInfo{
		Name:          m.Name,
		Caps:          port.ModelCaps{Tools: m.Caps.Tools, Reasoning: m.Caps.Reasoning, Vision: m.Caps.Vision},
		ContextWindow: m.ContextWindow,
		CostIn:        m.CostIn,
		CostOutUSD:    m.CostOutUSD,
	}
}

// toPortProbeResult 探测结论。Reason 原样带过去（可能夹着服务端原文，那是数据不是文案）。
func toPortProbeResult(r v1.ProbeResult) port.ProbeResult {
	return port.ProbeResult{
		Capability: r.Capability,
		Verdict:    port.ProbeVerdict(r.Verdict), // 三态常量两边同序（0/1/2），顺序即契约
		Reason:     r.Reason,
		Elapsed:    r.Elapsed,
		Usage:      toPortUsage(r.Usage),
	}
}

// toV1ToolSpec 工具声明。Schema 是透传字节；内核的 Describe 函数字段不进 v1，其对位是
// 可选 Describer 接口（D320）。
func toV1ToolSpec(s tool.Spec) v1.ToolSpec {
	return v1.ToolSpec{
		Name:        s.Name,
		Description: s.Description,
		Schema:      bytes.Clone(s.Schema),
		Risk:        toV1Risk(s.Risk),
	}
}
