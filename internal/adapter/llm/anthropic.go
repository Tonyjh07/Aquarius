package llm

// anthropic 适配器（D175 口径一/二/三/四）：Anthropic Messages API 的线上格式。
// HTTP、鉴权头、错误分类（*port.Quota / ErrTransient）与 SSE 骨架复用 Client
// （openai.go / stream.go）——换一套线上格式不该顺带换掉重试、密钥池与计数。

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/Tonyjh07/Aquarius/internal/domain/conversation"
	"github.com/Tonyjh07/Aquarius/internal/domain/tool"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

const anthropicPath = "/messages"

// anthropicVersion 必需的 API 版本头（Messages API 要求显式声明版本）。
const anthropicVersion = "2023-06-01"

// anthropicSigPrefix 思考块签名的协议前缀（D175 口径三）：只有本协议认它。
const anthropicSigPrefix = "anthropic:"

// anthropicDefaultMaxTokens 未配置输出预算时的 max_tokens。
// Messages API 的 max_tokens **必填**（不像 chat/responses 可以省略）——只能由适配器兜一个。
const anthropicDefaultMaxTokens = 8192

// anthropicMinThinkingBudget extended thinking 的最小预算（服务端硬要求）。
const anthropicMinThinkingBudget = 1024

// anthropicDefaultThinkingBudget 未给推理档位但思考开着时的预算。
const anthropicDefaultThinkingBudget = 2048

// anthropicThinkingBudgets 推理档位 → 思考预算 tokens。
//
// 两套词表本来对不上：本产品的档位是 minimal/low/medium/high 四档语义，Messages API 只认
// 「给多少 token 想」。这里按档位递增给一个保守的映射——**不给大值**：预算与 max_tokens
// 有硬约束（思考 token 计入 max_tokens），而多数 Claude 模型的输出上限就是 8192。
var anthropicThinkingBudgets = map[string]int{
	"minimal": anthropicMinThinkingBudget,
	"low":     2048,
	"medium":  3072,
	"high":    4096,
}

// ---------------------------------------------------------------------------
// 请求体编码
// ---------------------------------------------------------------------------

type anthropicRequest struct {
	Model     string             `json:"model"`
	MaxTokens int                `json:"max_tokens"` // 必填
	System    []anthropicText    `json:"system,omitempty"`
	Messages  []anthropicInbound `json:"messages"`
	Tools     []anthropicTool    `json:"tools,omitempty"`
	Stream    bool               `json:"stream"`
	// Temperature 采样温度（nil = 不发送，交服务端默认 1.0；0 会照发，D204）。
	// Messages API 只接受 0..1，超范围在启动校验时报因。
	Temperature *float64 `json:"temperature,omitempty"`
	// TopP 核采样（D204）；nil = 不发送（开扩展思考时该协议不接受，适配器置 nil）。
	TopP     *float64 `json:"top_p,omitempty"`
	StopSeqs []string `json:"stop_sequences,omitempty"`
	// Thinking 扩展思考开关（省略 = 交服务端默认）。
	Thinking *anthropicThinking `json:"thinking,omitempty"`
}

// anthropicInbound 输入消息：content 恒为内容块数组（字符串形态表达力不足：
// 工具结果是内容块、图片也是内容块）。
type anthropicInbound struct {
	Role    string `json:"role"` // "user" | "assistant"
	Content []any  `json:"content"`
}

type anthropicText struct {
	Type string `json:"type"` // 恒为 "text"
	Text string `json:"text"`
}

type anthropicImage struct {
	Type   string               `json:"type"` // 恒为 "image"
	Source anthropicImageSource `json:"source"`
}

type anthropicImageSource struct {
	Type      string `json:"type"` // 恒为 "base64"
	MediaType string `json:"media_type"`
	Data      string `json:"data"`
}

type anthropicToolUse struct {
	Type  string         `json:"type"` // 恒为 "tool_use"
	ID    string         `json:"id"`
	Name  string         `json:"name"`
	Input map[string]any `json:"input"`
}

type anthropicToolResult struct {
	Type      string `json:"type"` // 恒为 "tool_result"
	ToolUseID string `json:"tool_use_id"`
	Content   string `json:"content"`
}

// anthropicThinkingBlock 回放的思考块：thinking 与 signature 必须原样带回
// （Anthropic 会校验签名），所以两者缺一不回放。
type anthropicThinkingBlock struct {
	Type      string `json:"type"` // 恒为 "thinking"
	Thinking  string `json:"thinking"`
	Signature string `json:"signature"`
}

type anthropicThinking struct {
	Type         string `json:"type"` // "enabled" | "disabled"
	BudgetTokens int    `json:"budget_tokens,omitempty"`
}

// anthropicTool 工具声明：JSON schema 字段叫 input_schema（不叫 parameters）。
type anthropicTool struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	InputSchema json.RawMessage `json:"input_schema"`
}

// encodeAnthropicRequest 把 GenerateRequest 编码为 Messages 请求体（stream 恒为 true）。
func encodeAnthropicRequest(req port.GenerateRequest) ([]byte, error) {
	wn := newWireNames(req.Tools)
	msgs, system, err := anthropicMessages(req.Messages, wn)
	if err != nil {
		return nil, err
	}
	if len(msgs) == 0 {
		return nil, errors.New("llm: 没有可发送的消息")
	}
	// Messages API 要求首条是 user（system 走顶层，不占消息位）。
	if msgs[0].Role != "user" {
		return nil, errors.New("llm: 会话上下文以 assistant 开头，该协议要求首条消息为 user")
	}
	out := anthropicRequest{
		Model:       req.Model,
		System:      system,
		Messages:    msgs,
		Tools:       toAnthropicTools(req.Tools, wn),
		Stream:      true,
		Temperature: req.Params.Temperature,
		TopP:        req.Params.TopP,
		StopSeqs:    req.Params.Stop,
	}
	// max_tokens 必填：本轮预算 → 采样上限 → 适配器兜底。
	out.MaxTokens = req.Budget.MaxOutputTokens
	if out.MaxTokens == 0 {
		out.MaxTokens = req.Params.MaxTokens
	}
	if out.MaxTokens <= 0 {
		out.MaxTokens = anthropicDefaultMaxTokens
	}
	if th := anthropicThinkingOf(req.Params); th != nil {
		out.Thinking = th
		if th.Type == "enabled" {
			// 思考 token 计入 max_tokens：输出预算装不下思考预算时服务端必然拒绝
			// （budget_tokens 必须小于 max_tokens）。补到「正好装得下」——这是规则冲突，
			// 不是用户填错，报错也只会让用户去猜该填多少。
			if need := th.BudgetTokens + anthropicMinThinkingBudget; need > out.MaxTokens {
				out.MaxTokens = need
			}
			// 开启思考时温度只能是 1：省略即取服务端默认（1.0）；用户设的其它档位
			// 在这个组合下必然 400，省掉一次白跑。top_p 同理——该协议在扩展思考下
			// 不接受采样参数（D204）。
			out.Temperature = nil
			out.TopP = nil
		}
	}
	data, err := json.Marshal(out)
	if err != nil {
		return nil, fmt.Errorf("llm: 编码请求体: %w", err)
	}
	return data, nil
}

// anthropicMessages 端口消息序列 → (消息数组, 顶层 system 块)。
//
// 两处结构性差异：
//   - system 是**顶层字段**，Messages API 没有 system 角色。所有 system 消息按原序
//     并进顶层 system（相对顺序保留；与其它消息的前后位置就此丢失——API 没有别的表达方式）。
//   - tool 结果不是独立角色，而是 **user 消息里的 tool_result 内容块**；且 API 要求角色
//     交替，所以相邻的同角色消息必须合并（工具结果 + 紧随其后的工具图片正好合成一条）。
func anthropicMessages(msgs []port.PromptMessage, wn *wireNames) ([]anthropicInbound, []anthropicText, error) {
	var (
		out    []anthropicInbound
		system []anthropicText
	)
	push := func(role string, blocks ...any) {
		if len(blocks) == 0 {
			return
		}
		if n := len(out); n > 0 && out[n-1].Role == role {
			out[n-1].Content = append(out[n-1].Content, blocks...)
			return
		}
		out = append(out, anthropicInbound{Role: role, Content: blocks})
	}
	for _, m := range msgs {
		switch m.Role {
		case "system":
			blocks, err := anthropicBlocks(m.Content, false)
			if err != nil {
				return nil, nil, err
			}
			for _, b := range blocks {
				tb, ok := b.(anthropicText)
				if !ok {
					return nil, nil, errors.New("llm: system 消息不接受图片分片")
				}
				system = append(system, tb)
			}
		case "user":
			blocks, err := anthropicBlocks(m.Content, true)
			if err != nil {
				return nil, nil, err
			}
			push("user", blocks...)
		case "tool":
			push("user", anthropicToolResult{
				Type: "tool_result", ToolUseID: m.CallID, Content: flattenText(m.Content),
			})
		case "assistant":
			blocks, err := anthropicAssistantBlocks(m, wn)
			if err != nil {
				return nil, nil, err
			}
			push("assistant", blocks...)
		default:
			return nil, nil, fmt.Errorf("llm: 未知消息角色 %q", m.Role)
		}
	}
	return out, system, nil
}

// anthropicBlocks 消息内容 → 内容块（文本 + 图片）。allowImage=false 时图片报错：
// 图片只从 user 消息进（D169 工具产图另起一条 user 消息），出现在别处说明装配接错线。
func anthropicBlocks(parts []port.PromptPart, allowImage bool) ([]any, error) {
	out := make([]any, 0, len(parts))
	for _, p := range parts {
		if p.Kind == "image" {
			if !allowImage {
				return nil, errors.New("llm: 该角色的消息不接受图片分片")
			}
			if len(p.Data) == 0 {
				return nil, errors.New("llm: 图片片段缺少数据（AttachmentStore 未提供字节）")
			}
			mime := p.MIME
			if mime == "" {
				mime = "image/png"
			}
			out = append(out, anthropicImage{
				Type: "image",
				Source: anthropicImageSource{
					Type: "base64", MediaType: mime, Data: base64.StdEncoding.EncodeToString(p.Data),
				},
			})
			continue
		}
		if p.Text == "" {
			continue
		}
		out = append(out, anthropicText{Type: "text", Text: p.Text})
	}
	return out, nil
}

// anthropicAssistantBlocks assistant 消息 → 内容块。顺序固定为
// 思考 → 正文 → 工具调用：思考块必须在其 assistant 消息的开头且原样（含签名）。
func anthropicAssistantBlocks(m port.PromptMessage, wn *wireNames) ([]any, error) {
	var blocks []any
	if sig, ok := decodeAnthropicSignature(m.ReasoningSignature); ok && strings.TrimSpace(m.Reasoning) != "" {
		blocks = append(blocks, anthropicThinkingBlock{
			Type: "thinking", Thinking: m.Reasoning, Signature: sig,
		})
	}
	content, err := anthropicBlocks(m.Content, false)
	if err != nil {
		return nil, err
	}
	blocks = append(blocks, content...)
	for _, call := range m.ToolCalls {
		input, ierr := anthropicToolInput(call.Args)
		if ierr != nil {
			return nil, ierr
		}
		blocks = append(blocks, anthropicToolUse{
			Type: "tool_use", ID: string(call.ID), Name: wn.Wire(call.Name), Input: input,
		})
	}
	return blocks, nil
}

// anthropicToolInput 工具调用参数 → input 对象。Messages API 的 input 是 JSON **对象**
// （不是 chat 那样的字符串），参数坏掉时当场报错——那本该是入树前就被拦下的数据。
func anthropicToolInput(raw json.RawMessage) (map[string]any, error) {
	s := strings.TrimSpace(string(raw))
	if s == "" || s == "null" {
		return map[string]any{}, nil
	}
	var obj map[string]any
	if err := json.Unmarshal([]byte(s), &obj); err != nil {
		return nil, fmt.Errorf("llm: 工具调用参数不是 JSON 对象（%s）: %w", snippet(s, 80), err)
	}
	if obj == nil {
		obj = map[string]any{}
	}
	return obj, nil
}

// toAnthropicTools 工具声明转换；空 Schema 补默认对象结构（input_schema 必填）。
// 名字经 wireNames 换成端点安全别名（D195）。
func toAnthropicTools(specs []tool.Spec, wn *wireNames) []anthropicTool {
	out := make([]anthropicTool, 0, len(specs))
	for _, s := range specs {
		params := s.Schema
		if len(params) == 0 {
			params = tool.NoArgsSchema
		}
		out = append(out, anthropicTool{Name: wn.Wire(s.Name), Description: s.Description, InputSchema: params})
	}
	return out
}

// anthropicThinkingOf 思考参数映射（D34 口径 + D175 口径四）。Messages API 没有
// `enable_thinking` 这种布尔开关，只有 `thinking` 对象，所以两件事要合成一件事：
//
//   - 键缺失（nil）且无档位 = 零字段变化，不发 thinking（交服务端默认）；
//   - 显式 false = `{"type":"disabled"}`（在「思考恒开」的新模型上这才真能关掉）；
//   - 显式 true 或给了档位 = `{"type":"enabled","budget_tokens":档位映射}`。
//     给了档位就打开思考：chat 侧的 `reasoning_effort` 本就是「要思考、强度多少」一个参数，
//     到了这边拆成开关 + 预算，不能只认布尔而对档位装没看见。
func anthropicThinkingOf(p port.Sampling) *anthropicThinking {
	if p.Thinking != nil && !*p.Thinking {
		return &anthropicThinking{Type: "disabled"}
	}
	if (p.Thinking == nil || !*p.Thinking) && p.ReasoningEffort == "" {
		return nil
	}
	budget := anthropicDefaultThinkingBudget
	if v, ok := anthropicThinkingBudgets[p.ReasoningEffort]; ok {
		budget = v
	}
	return &anthropicThinking{Type: "enabled", BudgetTokens: budget}
}

// ---------------------------------------------------------------------------
// 生成
// ---------------------------------------------------------------------------

// generateAnthropic anthropic 协议的生成入口。与 chat 的差别：鉴权头（x-api-key +
// anthropic-version）、端点（/messages），以及**没有剥离重试**（D175 口径四：自家规范，
// 参数写错就是 bug，静默剥掉只会让配置错误浮不上来）。
func (c *Client) generateAnthropic(ctx context.Context, req port.GenerateRequest) (port.Stream, error) {
	body, err := encodeAnthropicRequest(req)
	if err != nil {
		return nil, err
	}
	streamCtx, cancel := context.WithCancel(ctx)
	resp, err := c.post(streamCtx, anthropicPath, body)
	if err != nil {
		cancel()
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		code := resp.StatusCode
		sn := readSnippet(resp.Body)
		after := retryAfter(resp)
		_ = resp.Body.Close()
		cancel()
		return nil, c.statusError(anthropicPath, code, sn, after)
	}
	s := newRawStream(streamCtx, cancel, resp.Body, newWireNames(req.Tools))
	st := &anthropicStreamState{}
	s.decode = func(payload string) (port.Delta, bool, error) {
		return decodeAnthropicEvent(s, st, payload)
	}
	return s, nil
}

// ---------------------------------------------------------------------------
// 流式解析
// ---------------------------------------------------------------------------

// anthropicStreamState SSE 归集状态：usage 分两次到达（message_start 给 input、
// message_delta 给累计 output），端口增量一次只带一份 usage，故在 message_stop 合并。
type anthropicStreamState struct {
	inTokens  int
	outTokens int
}

type anthropicEvent struct {
	Type         string                     `json:"type"`
	Index        int                        `json:"index"`
	Message      *anthropicResult           `json:"message"`
	ContentBlock *anthropicBlock            `json:"content_block"`
	Delta        *anthropicEventDelta       `json:"delta"`
	Usage        *anthropicEventUsage       `json:"usage"`
	Error        *anthropicEventErrorDetail `json:"error"`
}

type anthropicResult struct {
	Usage *anthropicEventUsage `json:"usage"`
}

type anthropicBlock struct {
	Type string `json:"type"` // "text" | "thinking" | "tool_use"
	ID   string `json:"id"`
	Name string `json:"name"`
}

// anthropicEventDelta content_block_delta 的 delta / message_delta 的 delta 共用外壳
// （字段按事件类型取用，用不到的留空）。
type anthropicEventDelta struct {
	Type        string `json:"type"` // text_delta | thinking_delta | signature_delta | input_json_delta
	Text        string `json:"text"`
	Thinking    string `json:"thinking"`
	Signature   string `json:"signature"`
	PartialJSON string `json:"partial_json"`
}

type anthropicEventUsage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

type anthropicEventErrorDetail struct {
	Type    string `json:"type"`
	Message string `json:"message"`
}

// decodeAnthropicEvent 一条 Messages SSE 载荷 → 增量；ok=false = 本片无增量。
func decodeAnthropicEvent(s *stream, st *anthropicStreamState, payload string) (port.Delta, bool, error) {
	var ev anthropicEvent
	if err := json.Unmarshal([]byte(payload), &ev); err != nil {
		return port.Delta{}, false, fmt.Errorf("llm: 解析流分片: %w（片段: %s）", err, snippet(payload, 200))
	}
	switch ev.Type {
	case "message_start":
		if ev.Message != nil && ev.Message.Usage != nil {
			st.inTokens = ev.Message.Usage.InputTokens
		}
		return port.Delta{}, false, nil

	case "content_block_start":
		// 工具调用的 ID 与名字只在块开始时给出（后续只有参数增量）。
		if ev.ContentBlock != nil && ev.ContentBlock.Type == "tool_use" {
			return port.Delta{ToolCalls: []port.ToolCallDelta{{
				Index: ev.Index, ID: ev.ContentBlock.ID, Name: ev.ContentBlock.Name,
			}}}, true, nil
		}
		return port.Delta{}, false, nil

	case "content_block_delta":
		if ev.Delta == nil {
			return port.Delta{}, false, nil
		}
		switch ev.Delta.Type {
		case "text_delta":
			if ev.Delta.Text == "" {
				return port.Delta{}, false, nil
			}
			return port.Delta{Text: ev.Delta.Text}, true, nil
		case "thinking_delta":
			if ev.Delta.Thinking == "" {
				return port.Delta{}, false, nil
			}
			return port.Delta{Text: ev.Delta.Thinking, Reasoning: true}, true, nil
		case "signature_delta":
			if ev.Delta.Signature == "" {
				return port.Delta{}, false, nil
			}
			// 只带签名不带文本：D175 已允许「纯签名」留片（Anthropic 的签名在正文之后单独到达）。
			return port.Delta{Reasoning: true, ReasoningSignature: encodeAnthropicSignature(ev.Delta.Signature)}, true, nil
		case "input_json_delta":
			if ev.Delta.PartialJSON == "" {
				return port.Delta{}, false, nil
			}
			return port.Delta{ToolCalls: []port.ToolCallDelta{{
				Index: ev.Index, ArgsDelta: ev.Delta.PartialJSON,
			}}}, true, nil
		}
		return port.Delta{}, false, nil

	case "message_delta":
		if ev.Usage != nil {
			st.outTokens = ev.Usage.OutputTokens // 累计值，最后到的为准
		}
		return port.Delta{}, false, nil

	case "message_stop":
		s.finish = true
		if st.inTokens == 0 && st.outTokens == 0 {
			return port.Delta{}, false, nil
		}
		return port.Delta{Usage: &conversation.Usage{
			InputTokens: st.inTokens, OutputTokens: st.outTokens,
		}}, true, nil

	case "error":
		return port.Delta{}, false, fmt.Errorf("llm: 服务端流式错误: %s", anthropicEventMessage(ev))

	case "ping", "content_block_stop":
		return port.Delta{}, false, nil
	}
	return port.Delta{}, false, nil
}

// anthropicEventMessage 取服务端给的那句话。
func anthropicEventMessage(ev anthropicEvent) string {
	if ev.Error != nil && ev.Error.Message != "" {
		return ev.Error.Message
	}
	if ev.Error != nil && ev.Error.Type != "" {
		return ev.Error.Type
	}
	return "（服务端未给出详情）"
}

// ---------------------------------------------------------------------------
// 思考签名：服务端返回的签名随思考分片带回，续跑时原样回传（D175 口径三）
// ---------------------------------------------------------------------------

// encodeAnthropicSignature 编码思考块签名为树内不透明串：前缀 + 原样签名。
// 签名本身就是高熵 base64，无需再压一层（Responses 的 encrypted_content 才需要）。
func encodeAnthropicSignature(signature string) string {
	if signature == "" {
		return ""
	}
	return anthropicSigPrefix + signature
}

// decodeAnthropicSignature 解回签名；前缀不是本协议（如 `responses:`）或内容为空
// 一律 ok=false = 不回放（跨 provider 复制会话时的安全默认）。
func decodeAnthropicSignature(sig string) (string, bool) {
	body, found := strings.CutPrefix(sig, anthropicSigPrefix)
	if !found || body == "" {
		return "", false
	}
	return body, true
}
