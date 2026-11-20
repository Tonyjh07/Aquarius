package llm

// anthropic 适配器（D175 规则一/二/三/四）：Anthropic Messages API 的线上格式。
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
	"github.com/Tonyjh07/Aquarius/internal/i18n"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

const anthropicPath = "/messages"

// anthropicVersion 必需的 API 版本头（Messages API 要求显式声明版本）。
const anthropicVersion = "2023-06-01"

// anthropicSigPrefix 思考块签名的协议前缀（D175 规则三）：只有本协议认它。
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
	Tools     []any              `json:"tools,omitempty"`
	Stream    bool               `json:"stream"`
	// Temperature 采样温度（nil = 不发送，交服务端默认 1.0；0 会照发，D204）。
	// Messages API 只接受 0..1，超范围在启动校验时报因。
	Temperature *float64 `json:"temperature,omitempty"`
	// TopP 核采样（D204）；nil = 不发送（开扩展思考时该协议不接受，适配器置 nil）。
	TopP     *float64 `json:"top_p,omitempty"`
	StopSeqs []string `json:"stop_sequences,omitempty"`
	// Thinking 扩展思考开关（省略 = 交服务端默认）。
	Thinking *anthropicThinking `json:"thinking,omitempty"`
	// Title 会话标题开关（D309）；nil = 不发送。
	Title *capToggle `json:"title,omitempty"`
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
		return nil, errors.New(i18n.T("llm.err.no_messages"))
	}
	// Messages API 要求首条是 user（system 走顶层，不占消息位）。
	if msgs[0].Role != "user" {
		return nil, errors.New(i18n.T("llm.err.leading_assistant"))
	}
	out := anthropicRequest{
		Model:       req.Model,
		System:      system,
		Messages:    msgs,
		Tools:       toAnthropicTools(req.Tools, wn, req.ServerTools),
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
	if req.Title {
		out.Title = &capToggle{Enabled: true} // D309：只在装配层开的那一轮出现
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
		return nil, fmt.Errorf("%s: %w", i18n.T("llm.err.encode_body"), err)
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
		case port.PromptSystem:
			blocks, err := anthropicBlocks(m.Content, false)
			if err != nil {
				return nil, nil, err
			}
			for _, b := range blocks {
				tb, ok := b.(anthropicText)
				if !ok {
					return nil, nil, errors.New(i18n.T("llm.err.system_image"))
				}
				system = append(system, tb)
			}
		case port.PromptUser:
			blocks, err := anthropicBlocks(m.Content, true)
			if err != nil {
				return nil, nil, err
			}
			push("user", blocks...)
		case port.PromptTool:
			// tool 结果没有独立角色，只能作为 user 消息里的 tool_result 块。
			push("user", anthropicToolResult{
				Type: "tool_result", ToolUseID: m.CallID, Content: flattenText(m.Content),
			})
		case port.PromptAssistant:
			blocks, err := anthropicAssistantBlocks(m, wn)
			if err != nil {
				return nil, nil, err
			}
			push("assistant", blocks...)
		default:
			return nil, nil, errors.New(i18n.Tf("llm.err.unknown_role", "role", m.Role))
		}
	}
	return out, system, nil
}

// anthropicBlocks 消息内容 → 内容块（文本 + 图片）。allowImage=false 时图片报错：
// 图片只从 user 消息进（D169 工具产图另起一条 user 消息），出现在别处说明装配接错线。
func anthropicBlocks(parts []port.PromptPart, allowImage bool) ([]any, error) {
	out := make([]any, 0, len(parts))
	for _, p := range parts {
		if p.Kind == port.PromptImage {
			if !allowImage {
				return nil, errors.New(i18n.T("llm.err.role_image"))
			}
			if len(p.Data) == 0 {
				return nil, errors.New(i18n.T("llm.err.image_no_data"))
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
// 思考 → 服务端块 → 正文 → 工具调用：思考块必须在其 assistant 消息的开头且原样（含签名）；
// 服务端块（D310）在原响应里也排在正文之前，且必须紧跟同一轮的思考、在正文之前回传，
// 服务端才认得出「这轮读过哪个网页」。
func anthropicAssistantBlocks(m port.PromptMessage, wn *wireNames) ([]any, error) {
	var blocks []any
	if sig, ok := decodeAnthropicSignature(m.ReasoningSignature); ok && strings.TrimSpace(m.Reasoning) != "" {
		blocks = append(blocks, anthropicThinkingBlock{
			Type: "thinking", Thinking: m.Reasoning, Signature: sig,
		})
	}
	for _, raw := range m.ServerBlocks {
		if len(raw) > 0 {
			blocks = append(blocks, raw) // 原样：signature 在块内，解码再编码只会丢东西
		}
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
		return nil, fmt.Errorf("%s: %w", i18n.Tf("llm.err.tool_args_not_object", "snippet", snippet(s, 80)), err)
	}
	if obj == nil {
		obj = map[string]any{}
	}
	return obj, nil
}

// toAnthropicTools 工具声明转换；空 Schema 补默认对象结构（input_schema 必填）。
// 名字经 wireNames 换成端点安全别名（D195）。
//
// 返回 []any 是因为服务端工具（D310）与 function tool 的形状不同：服务端工具只要
// `{"type":"deepseek_read_url","name":"deepseek_read_url"}`，没有 schema。两者在协议里
// 不能共存，调用方据此二选一。
func toAnthropicTools(specs []tool.Spec, wn *wireNames, serverTools []string) []any {
	out := make([]any, 0, len(specs)+len(serverTools))
	for _, s := range specs {
		params := s.Schema
		if len(params) == 0 {
			params = tool.NoArgsSchema
		}
		out = append(out, anthropicTool{Name: wn.Wire(s.Name), Description: s.Description, InputSchema: params})
	}
	for _, name := range serverTools {
		if typ := anthropicServerToolType(name); typ != "" {
			out = append(out, anthropicServerTool{Type: typ, Name: typ})
		}
	}
	return out
}

// anthropicServerTool 服务端工具声明（D310）：type 与 name 同值，没有 schema。
type anthropicServerTool struct {
	Type string `json:"type"`
	Name string `json:"name"`
}

// anthropicServerToolType 能力名 → 服务端工具名；认不出（含本协议没有的能力）返回空。
// Messages 这边目前只有阅读链接一项：服务端搜索与树模型正面冲突（plan 有记），不发。
func anthropicServerToolType(capability string) string {
	if capability == port.ServerCapReadURL {
		return "deepseek_read_url"
	}
	return ""
}

// anthropicThinkingOf 思考参数映射（D34 规则 + D175 规则四）。Messages API 没有
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
// anthropic-version）、端点（/messages），以及**没有剥离重试**（D175 规则四：自家规范，
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
	tr := c.startTrace(anthropicPath, req.Model, resp)
	if resp.StatusCode != http.StatusOK {
		code := resp.StatusCode
		sn := tr.readBody(resp.Body)
		after := retryAfter(resp)
		_ = resp.Body.Close()
		cancel()
		tr.finish(port.WireEndStatus)
		c.noteCapFrom400(code, sn, req)        // D307：400 点名 title 时记一笔，此后不再发
		c.noteServerToolFrom400(code, sn, req) // D310：服务端工具被拒同样记账
		return nil, c.statusError(anthropicPath, code, sn, after)
	}
	s := newRawStream(streamCtx, cancel, &tracedBody{ReadCloser: resp.Body, tr: tr}, newWireNames(req.Tools))
	s.attachTrace(tr)
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
	inTokens   int
	outTokens  int
	cacheRead  int
	cacheWrite int
}

type anthropicEvent struct {
	Type    string           `json:"type"`
	Index   int              `json:"index"`
	Message *anthropicResult `json:"message"`
	// ContentBlock 保留原文（D310）：服务端工具块要原样回传，解成结构体再编回去
	// 可能丢掉服务端认的字段（signature 尤其）。
	ContentBlock json.RawMessage            `json:"content_block"`
	Delta        *anthropicEventDelta       `json:"delta"`
	Usage        *anthropicEventUsage       `json:"usage"`
	Error        *anthropicEventErrorDetail `json:"error"`
}

type anthropicResult struct {
	Usage *anthropicEventUsage `json:"usage"`
	// Title 非流式形态的会话标题位置（D309）。
	Title string `json:"title"`
}

// anthropicBlock content_block 里本适配器要认的字段（服务端块另走 anthropicServerBlockOf）。
type anthropicBlock struct {
	Type string `json:"type"` // "text" | "thinking" | "tool_use" | "server_tool_use" | "web_fetch_tool_result"
	ID   string `json:"id"`
	Name string `json:"name"`
}

// anthropicServerBlockOf 把 content_block 原文识别为服务端工具块（D310）：
// 调用块（server_tool_use）与结果块（web_fetch_tool_result）。其它类型返回 nil。
func anthropicServerBlockOf(raw json.RawMessage) *port.ServerBlock {
	if len(raw) == 0 {
		return nil
	}
	var head struct {
		Type  string `json:"type"`
		Kind  string `json:"kind"`
		Name  string `json:"name"`
		Input struct {
			URL string `json:"url"`
		} `json:"input"`
		Content struct {
			Type    string `json:"type"`
			URL     string `json:"url"`
			Content struct {
				Title string `json:"title"`
			} `json:"content"`
		} `json:"content"`
	}
	if err := json.Unmarshal(raw, &head); err != nil {
		return nil
	}
	switch head.Type {
	case "server_tool_use":
		// 只留阅读链接这一种：调用块里没有 signature，展示也不需要 action 之外的字段。
		return &port.ServerBlock{Tool: head.Name, Kind: head.Kind, URL: head.Input.URL, Raw: raw}
	case "web_fetch_tool_result":
		return &port.ServerBlock{Tool: port.ServerCapReadURL, Kind: head.Kind, URL: head.Content.URL,
			Title: head.Content.Content.Title, Raw: raw}
	case "web_search_tool_result":
		// 搜索工具本轮不声明（plan 有记：与树模型冲突），收到就说明端点自作主张，丢弃。
		return nil
	}
	return nil
}

// anthropicEventDelta content_block_delta 的 delta / message_delta 的 delta 共用外壳
// （字段按事件类型取用，用不到的留空）。
type anthropicEventDelta struct {
	Type        string `json:"type"` // text_delta | thinking_delta | signature_delta | input_json_delta
	Text        string `json:"text"`
	Thinking    string `json:"thinking"`
	Signature   string `json:"signature"`
	PartialJSON string `json:"partial_json"`
	// Title 会话标题（D309，message_delta 的 delta 里一次给全）。
	Title string `json:"title"`
}

// anthropicEventUsage Messages 的 usage 块。Anthropic 把输入拆成三段报：input_tokens
// input_tokens 只算未命中缓存的输入，缓存命中与缓存写入分别报在另两个字段里（D308）。
type anthropicEventUsage struct {
	InputTokens              int `json:"input_tokens"`
	OutputTokens             int `json:"output_tokens"`
	CacheReadInputTokens     int `json:"cache_read_input_tokens"`
	CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
}

type anthropicEventErrorDetail struct {
	Type    string `json:"type"`
	Message string `json:"message"`
}

// decodeAnthropicEvent 一条 Messages SSE 载荷 → 增量；ok=false = 本片无增量。
func decodeAnthropicEvent(s *stream, st *anthropicStreamState, payload string) (port.Delta, bool, error) {
	var ev anthropicEvent
	if err := json.Unmarshal([]byte(payload), &ev); err != nil {
		return port.Delta{}, false, fmt.Errorf("%s: %w", i18n.Tf("llm.err.stream_decode", "snippet", snippet(payload, 200)), err)
	}
	switch ev.Type {
	case "message_start":
		if ev.Message != nil && ev.Message.Usage != nil {
			u := ev.Message.Usage
			// InputTokens 折成总输入：Anthropic 的 input_tokens 不含缓存那两段，直接拿这个字段
			// 会让「命中率」和「总输入」在两套口径之间对不上（D308）。
			st.inTokens = u.InputTokens + u.CacheReadInputTokens + u.CacheCreationInputTokens
			st.cacheRead, st.cacheWrite = u.CacheReadInputTokens, u.CacheCreationInputTokens
		}
		if ev.Message != nil && ev.Message.Title != "" {
			return port.Delta{Title: ev.Message.Title}, true, nil
		}
		return port.Delta{}, false, nil

	case "content_block_start":
		// 服务端工具块（D310）整块一次性下发：认出就原样交给 app（树的 PartServer）。
		if sb := anthropicServerBlockOf(ev.ContentBlock); sb != nil {
			return port.Delta{Server: sb}, true, nil
		}
		// 工具调用的 ID 与名字只在块开始时给出（后续只有参数增量）。
		var cb anthropicBlock
		if len(ev.ContentBlock) > 0 {
			_ = json.Unmarshal(ev.ContentBlock, &cb)
		}
		if cb.Type == "tool_use" {
			return port.Delta{ToolCalls: []port.ToolCallDelta{{
				Index: ev.Index, ID: cb.ID, Name: cb.Name,
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
		// 会话标题（D309）随 message_delta 的 delta 一次给全；与 usage 同片，两者都要带。
		if ev.Delta != nil && ev.Delta.Title != "" {
			return port.Delta{Title: ev.Delta.Title}, true, nil
		}
		return port.Delta{}, false, nil

	case "message_stop":
		s.finish = true
		if st.inTokens == 0 && st.outTokens == 0 {
			return port.Delta{}, false, nil
		}
		return port.Delta{Usage: &conversation.Usage{
			InputTokens: st.inTokens, OutputTokens: st.outTokens,
			CacheRead: st.cacheRead, CacheWrite: st.cacheWrite,
		}}, true, nil

	case "error":
		return port.Delta{}, false, errors.New(i18n.Tf("llm.err.stream_error", "message", anthropicEventMessage(ev)))

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
	return i18n.T("llm.detail.no_detail")
}

// ---------------------------------------------------------------------------
// 思考签名：服务端返回的签名随思考分片带回，续跑时原样回传（D175 规则三）
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
