package llm

// openai-responses 适配器（D175 口径一/二/四）：协议形状的差异全在本文件。
// HTTP、鉴权、tokenizer、错误分类（*port.Quota / ErrTransient）、SSE 骨架与增量
// 端口语义都复用 Client（openai.go / stream.go）——「换成另一套线上格式」不该顺带
// 换掉重试、密钥池、计数这些横切能力。

import (
	"bytes"
	"compress/flate"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/Tonyjh07/Aquarius/internal/domain/conversation"
	"github.com/Tonyjh07/Aquarius/internal/domain/tool"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

const responsesPath = "/responses"

// responsesSigPrefix 思考块签名的协议前缀（D175 口径三）：只有本协议认这个前缀，
// 别家签名（`anthropic:`）一律忽略——跨 provider 复制会话时不会把 A 家的签名发给 B 家。
const responsesSigPrefix = "responses:"

// responsesSigMaxBytes 签名解压后的字节上限（防一条损坏/恶意签名把内存吃爆）。
// 服务端的加密思考内容上限是 10MB，留一倍余量。
const responsesSigMaxBytes = 32 << 20

// ---------------------------------------------------------------------------
// 请求体编码
// ---------------------------------------------------------------------------

type responsesRequest struct {
	Model  string `json:"model"`
	Input  []any  `json:"input"`
	Stream bool   `json:"stream"`
	// Store 恒为 false（本产品无状态：上下文由会话树装配，服务端不存对话）。
	// 配 include 的 reasoning.encrypted_content 才能在无状态模式下回放思考块。
	Store           bool                `json:"store"`
	Include         []string            `json:"include,omitempty"`
	Tools           []responsesTool     `json:"tools,omitempty"`
	Reasoning       *responsesReasoning `json:"reasoning,omitempty"`
	Temperature     float64             `json:"temperature,omitempty"` // 0 = 不发送
	MaxOutputTokens int                 `json:"max_output_tokens,omitempty"`
}

// responsesTool 工具声明：扁平结构（name/description/parameters 直接挂在顶层，
// 不像 chat 那样嵌一层 function）。
type responsesTool struct {
	Type        string          `json:"type"` // 恒为 "function"
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters,omitempty"`
}

// responsesReasoning 思考参数（effort 空 = 不发送）。
type responsesReasoning struct {
	Effort  string `json:"effort,omitempty"`
	Summary string `json:"summary,omitempty"` // "auto"：不然用户看不到思考
}

type responsesMessage struct {
	Type    string `json:"type"` // 恒为 "message"
	Role    string `json:"role"`
	Content []any  `json:"content"`
}

type responsesTextPart struct {
	Type string `json:"type"` // "input_text" | "output_text"
	Text string `json:"text"`
}

type responsesImagePart struct {
	Type     string `json:"type"` // 恒为 "input_image"
	ImageURL string `json:"image_url"`
}

type responsesFunctionCall struct {
	Type      string `json:"type"` // 恒为 "function_call"
	CallID    string `json:"call_id"`
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type responsesFunctionOutput struct {
	Type   string `json:"type"` // 恒为 "function_call_output"
	CallID string `json:"call_id"`
	Output string `json:"output"`
}

// responsesInputReasoning 回放的思考项：encrypted_content 原样带回（就是上一轮服务端
// 给的那串），id 可省。summary 带上我们存过的思考文本——即使服务端不认加密内容，
// 模型仍看得到它自己上一轮想了什么。
type responsesInputReasoning struct {
	Type             string                 `json:"type"` // 恒为 "reasoning"
	ID               string                 `json:"id,omitempty"`
	EncryptedContent string                 `json:"encrypted_content"`
	Summary          []responsesSummaryPart `json:"summary,omitempty"`
}

type responsesSummaryPart struct {
	Type string `json:"type"` // 恒为 "summary_text"
	Text string `json:"text"`
}

// encodeResponsesRequest 把 GenerateRequest 编码为 Responses 请求体（stream 恒为 true）。
func encodeResponsesRequest(req port.GenerateRequest) ([]byte, error) {
	out := responsesRequest{Model: req.Model, Stream: true}
	for _, m := range req.Messages {
		items, err := responsesItems(m)
		if err != nil {
			return nil, err
		}
		out.Input = append(out.Input, items...)
	}
	if len(req.Tools) > 0 {
		out.Tools = toResponsesTools(req.Tools)
	}
	out.Temperature = req.Params.Temperature
	out.MaxOutputTokens = req.Budget.MaxOutputTokens
	if out.MaxOutputTokens == 0 {
		out.MaxOutputTokens = req.Params.MaxTokens
	}
	out.Reasoning = responsesReasoningOf(req.Params)
	if out.Reasoning != nil {
		// 只有请求了思考才要加密内容：不开思考就没有 reasoning item 要回放，
		// 也就不必给服务端出这道题（少一个可能被兼容端点拒掉的字段）。
		out.Include = []string{"reasoning.encrypted_content"}
	}
	data, err := json.Marshal(out)
	if err != nil {
		return nil, fmt.Errorf("llm: 编码请求体: %w", err)
	}
	return data, nil
}

// responsesItems 一条端口消息 → 0..n 个 input item。工具调用/思考不塞进 assistant 消息的
// content，而是按 Responses 的模型各占一个 item（这是它与 chat 最大的形状差异）。
func responsesItems(m port.PromptMessage) ([]any, error) {
	switch m.Role {
	case "system", "user":
		content, err := responsesContents(m.Content, "input_text", m.Role == "user")
		if err != nil {
			return nil, err
		}
		if len(content) == 0 {
			return nil, nil
		}
		return []any{responsesMessage{Type: "message", Role: m.Role, Content: content}}, nil
	case "assistant":
		var (
			reasoning any
			items     []any
		)
		if id, enc, ok := decodeResponsesSignature(m.ReasoningSignature); ok {
			reasoning = responsesInputReasoning{
				Type:             "reasoning",
				ID:               id,
				EncryptedContent: enc,
				Summary:          responsesSummary(m.Reasoning),
			}
		}
		content, err := responsesContents(m.Content, "output_text", false)
		if err != nil {
			return nil, err
		}
		if len(content) > 0 {
			items = append(items, responsesMessage{Type: "message", Role: "assistant", Content: content})
		}
		for _, call := range m.ToolCalls {
			args := strings.TrimSpace(string(call.Args))
			if args == "" {
				args = "{}"
			}
			items = append(items, responsesFunctionCall{
				Type: "function_call", CallID: string(call.ID), Name: call.Name, Arguments: args,
			})
		}
		// 服务端要求 reasoning item 后面紧跟它产出的 assistant 消息或 function_call，
		// 才允许接下一条 user/system 消息——没有后继就不发（凭空补一条空 assistant
		// 消息是往上下文里塞假数据）。此时这条思考只能丢，签名仍留在会话树里。
		if reasoning != nil && len(items) > 0 {
			return append([]any{reasoning}, items...), nil
		}
		return items, nil
	case "tool":
		return []any{responsesFunctionOutput{
			Type: "function_call_output", CallID: m.CallID, Output: flattenText(m.Content),
		}}, nil
	}
	return nil, fmt.Errorf("llm: 未知消息角色 %q", m.Role)
}

// responsesContents 消息内容 → Responses content 分片。textType 按角色取 input_text
// （system/user）或 output_text（assistant）；allowImage=false 时遇图片直接报错——
// 图片只从 user 消息进（D169 工具产图另起一条 user 消息），出现在别处说明装配层接错线了。
func responsesContents(parts []port.PromptPart, textType string, allowImage bool) ([]any, error) {
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
			out = append(out, responsesImagePart{
				Type:     "input_image",
				ImageURL: "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(p.Data),
			})
			continue
		}
		if p.Text == "" {
			continue
		}
		out = append(out, responsesTextPart{Type: textType, Text: p.Text})
	}
	return out, nil
}

// responsesSummary 思考文本 → reasoning item 的 summary 分片。
func responsesSummary(text string) []responsesSummaryPart {
	if strings.TrimSpace(text) == "" {
		return nil
	}
	return []responsesSummaryPart{{Type: "summary_text", Text: text}}
}

// responsesReasoningOf 思考参数映射（D34 + D175 口径四）：
//
//   - `reasoning_effort` → `reasoning.effort`；
//   - `enable_thinking` 在本协议没有对应字段：显式 false = 不发 reasoning（交服务端默认），
//     显式 true 或未设置 = 转发 effort 并请服务端给摘要（否则思考不落文本、用户看不到）；
//   - 两者都没设 = 完全不发（零字段变化）。
func responsesReasoningOf(p port.Sampling) *responsesReasoning {
	if p.Thinking != nil && !*p.Thinking {
		return nil
	}
	if p.ReasoningEffort == "" && p.Thinking == nil {
		return nil
	}
	return &responsesReasoning{Effort: p.ReasoningEffort, Summary: "auto"}
}

// toResponsesTools 工具声明转换；空 Schema 补默认对象结构（部分服务强制要求 parameters）。
func toResponsesTools(specs []tool.Spec) []responsesTool {
	out := make([]responsesTool, 0, len(specs))
	for _, s := range specs {
		params := s.Schema
		if len(params) == 0 {
			params = json.RawMessage(`{"type":"object","properties":{}}`)
		}
		out = append(out, responsesTool{
			Type:        "function",
			Name:        s.Name,
			Description: s.Description,
			Parameters:  params,
		})
	}
	return out
}

// ---------------------------------------------------------------------------
// 生成
// ---------------------------------------------------------------------------

// generateResponses openai-responses 的生成入口。端口语义与 chat 一致（Recv 给增量、
// io.EOF 收流），差别只在线上格式。
//
// **没有剥离重试**（D175 口径四）：Responses 是自家规范，参数写错就是 bug——静默剥掉
// 一个字段会让配置错误永远浮不上来；chat 的兼容重试是面对「生态各写各的」的止损。
func (c *Client) generateResponses(ctx context.Context, req port.GenerateRequest) (port.Stream, error) {
	body, err := encodeResponsesRequest(req)
	if err != nil {
		return nil, err
	}
	streamCtx, cancel := context.WithCancel(ctx)
	resp, err := c.post(streamCtx, responsesPath, body)
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
		return nil, c.statusError(responsesPath, code, sn, after)
	}
	s := newRawStream(streamCtx, cancel, resp.Body)
	s.decode = s.decodeResponses
	return s, nil
}

// ---------------------------------------------------------------------------
// 流式解析
// ---------------------------------------------------------------------------

// responsesEvent SSE 载荷：只声明用得到的字段，未知事件按「无增量」跳过——服务端多
// 发几个进度事件不该让收流失败。
type responsesEvent struct {
	Type        string            `json:"type"`
	Delta       string            `json:"delta"`
	OutputIndex int               `json:"output_index"`
	Name        string            `json:"name"`      // function_call_arguments.done 带函数名
	Arguments   string            `json:"arguments"` // 同上：完整参数
	Item        *responsesOutItem `json:"item"`
	Response    *responsesResult  `json:"response"`
	Error       *responsesError   `json:"error"`
	Code        string            `json:"code"`
	Message     string            `json:"message"`
}

type responsesOutItem struct {
	Type             string `json:"type"`
	ID               string `json:"id"`
	CallID           string `json:"call_id"`
	Name             string `json:"name"`
	Arguments        string `json:"arguments"`
	EncryptedContent string `json:"encrypted_content"`
}

type responsesResult struct {
	Output []responsesOutItem `json:"output"`
	Usage  *responsesUsage    `json:"usage"`
	Error  *responsesError    `json:"error"`
}

type responsesUsage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

type responsesError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// decodeResponses 一条 Responses SSE 载荷 → 增量；ok=false = 本片无增量。
func (s *stream) decodeResponses(payload string) (port.Delta, bool, error) {
	var ev responsesEvent
	if err := json.Unmarshal([]byte(payload), &ev); err != nil {
		return port.Delta{}, false, fmt.Errorf("llm: 解析流分片: %w（片段: %s）", err, snippet(payload, 200))
	}
	switch ev.Type {
	case "response.output_text.delta":
		if ev.Delta == "" {
			return port.Delta{}, false, nil
		}
		return port.Delta{Text: ev.Delta}, true, nil

	case "response.reasoning_summary_text.delta", "response.reasoning_text.delta":
		// 两种事件名：开了 summary 走前者，端点直接流推理正文的走后者。对端口都是一个意思。
		if ev.Delta == "" {
			return port.Delta{}, false, nil
		}
		return port.Delta{Text: ev.Delta, Reasoning: true}, true, nil

	case "response.function_call_arguments.delta":
		if ev.Delta == "" {
			return port.Delta{}, false, nil
		}
		s.markArgsSeen(ev.OutputIndex)
		return port.Delta{ToolCalls: []port.ToolCallDelta{{Index: ev.OutputIndex, ArgsDelta: ev.Delta}}}, true, nil

	case "response.function_call_arguments.done":
		d, ok := s.callDelta(ev.OutputIndex, "", ev.Name, ev.Arguments)
		return d, ok, nil

	case "response.output_item.added", "response.output_item.done":
		if ev.Item == nil {
			return port.Delta{}, false, nil
		}
		switch ev.Item.Type {
		case "function_call":
			// 参数只在 done 里取：added 的参数一般是空的，交给 delta 分片。
			args := ""
			if ev.Type == "response.output_item.done" {
				args = ev.Item.Arguments
			}
			d, ok := s.callDelta(ev.OutputIndex, ev.Item.CallID, ev.Item.Name, args)
			return d, ok, nil
		case "reasoning":
			// 只有 done 的 encrypted_content 是完整的（added 可能是片段）。
			if ev.Type != "response.output_item.done" {
				return port.Delta{}, false, nil
			}
			sig := encodeResponsesSignature(ev.Item.ID, ev.Item.EncryptedContent)
			if sig == "" {
				return port.Delta{}, false, nil
			}
			s.markSigSeen(ev.OutputIndex)
			return port.Delta{Reasoning: true, ReasoningSignature: sig}, true, nil
		}
		return port.Delta{}, false, nil

	case "response.completed", "response.incomplete":
		s.finish = true // 之后不再有增量（Recv 本片发完即收流）
		d := s.finishDelta(ev.Response)
		ok := d.Usage != nil || len(d.ToolCalls) > 0 || d.ReasoningSignature != ""
		return d, ok, nil

	case "response.failed":
		return port.Delta{}, false, fmt.Errorf("llm: 服务端生成失败: %s", responsesErrorMessage(ev))

	case "error":
		return port.Delta{}, false, fmt.Errorf("llm: 服务端流式错误: %s", responsesErrorMessage(ev))
	}
	return port.Delta{}, false, nil
}

// finishDelta 收尾事件的增量：usage，外加「流里漏发的那些 item」——有些端点不发
// output_item.done 就直奔 completed，此时从 response.output 补齐参数与思考签名
// （补不上就是真的丢了，宁可少回放，也不能编一串假的）。
func (s *stream) finishDelta(r *responsesResult) port.Delta {
	var out port.Delta
	if r == nil {
		return out
	}
	for i, it := range r.Output {
		switch it.Type {
		case "function_call":
			if s.argsSeen[i] {
				continue
			}
			d, ok := s.callDelta(i, it.CallID, it.Name, it.Arguments)
			if ok {
				out.ToolCalls = append(out.ToolCalls, d.ToolCalls...)
			}
		case "reasoning":
			if s.sigSeen[i] {
				continue
			}
			if sig := encodeResponsesSignature(it.ID, it.EncryptedContent); sig != "" {
				s.markSigSeen(i)
				out.Reasoning, out.ReasoningSignature = true, sig
			}
		}
	}
	if r.Usage != nil {
		out.Usage = &conversation.Usage{InputTokens: r.Usage.InputTokens, OutputTokens: r.Usage.OutputTokens}
	}
	return out
}

// callDelta 组装一条工具增量；参数只在「这一路还没收过参数分片」时随事件带上
// （个别端点不发明细分片，只在收尾给全文）。三者皆空 = 无增量。
func (s *stream) callDelta(index int, id, name, args string) (port.Delta, bool) {
	d := port.ToolCallDelta{Index: index}
	if id != "" {
		d.ID = id
	}
	if name != "" {
		d.Name = name
	}
	if args != "" && !s.argsSeen[index] {
		s.markArgsSeen(index)
		d.ArgsDelta = args
	}
	if d.ID == "" && d.Name == "" && d.ArgsDelta == "" {
		return port.Delta{}, false
	}
	return port.Delta{ToolCalls: []port.ToolCallDelta{d}}, true
}

// responsesErrorMessage 取服务端给的那句话（几种事件位置不同，按可能性依次找）。
func responsesErrorMessage(ev responsesEvent) string {
	if ev.Error != nil && ev.Error.Message != "" {
		return ev.Error.Message
	}
	if ev.Response != nil && ev.Response.Error != nil && ev.Response.Error.Message != "" {
		return ev.Response.Error.Message
	}
	if ev.Message != "" {
		return ev.Message
	}
	if ev.Code != "" {
		return ev.Code
	}
	return "（服务端未给出详情）"
}

// ---------------------------------------------------------------------------
// 思考签名编解码（D175 口径三）
// ---------------------------------------------------------------------------

// responsesSigBody 签名解压后的负载：原样回放所需的两项。
type responsesSigBody struct {
	ID        string `json:"id,omitempty"`
	Encrypted string `json:"encrypted_content"`
}

// encodeResponsesSignature 编码思考块签名为树内不透明串：
// `responses:` + base64(flate(JSON{id, encrypted_content}))。
// 压一道是因为 encrypted_content 要随会话树落盘——原样存会把会话文件撑大一大截，
// 而它是服务端生成的高熵串，压缩率恰好很高。
// 没有 encrypted_content 就返回空串 = 不加签名：没它就回放不了，留个空壳只会误导。
func encodeResponsesSignature(id, encrypted string) string {
	if encrypted == "" {
		return ""
	}
	raw, err := json.Marshal(responsesSigBody{ID: id, Encrypted: encrypted})
	if err != nil {
		return ""
	}
	var buf bytes.Buffer
	zw, err := flate.NewWriter(&buf, flate.BestSpeed)
	if err != nil {
		return ""
	}
	if _, err := zw.Write(raw); err != nil {
		return ""
	}
	if err := zw.Close(); err != nil {
		return ""
	}
	return responsesSigPrefix + base64.StdEncoding.EncodeToString(buf.Bytes())
}

// decodeResponsesSignature 解回 id 与 encrypted_content。前缀不是本协议（如 `anthropic:`）、
// 内容损坏、超限一律 ok=false = 不回放——跨 provider 复制会话时的安全默认。
func decodeResponsesSignature(sig string) (id, encrypted string, ok bool) {
	body, found := strings.CutPrefix(sig, responsesSigPrefix)
	if !found || body == "" {
		return "", "", false
	}
	raw, err := base64.StdEncoding.DecodeString(body)
	if err != nil {
		return "", "", false
	}
	zr := flate.NewReader(bytes.NewReader(raw))
	defer zr.Close()
	plain, err := io.ReadAll(io.LimitReader(zr, responsesSigMaxBytes))
	if err != nil {
		return "", "", false
	}
	var v responsesSigBody
	if err := json.Unmarshal(plain, &v); err != nil || v.Encrypted == "" {
		return "", "", false
	}
	return v.ID, v.Encrypted, true
}
