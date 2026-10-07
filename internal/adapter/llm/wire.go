package llm

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/Tonyjh07/Aquarius/internal/domain/tool"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

type chatRequest struct {
	Model         string             `json:"model"`
	Messages      []chatMessage      `json:"messages"`
	Stream        bool               `json:"stream"`
	StreamOptions *chatStreamOptions `json:"stream_options,omitempty"`
	Tools         []chatTool         `json:"tools,omitempty"`
	Temperature   float64            `json:"temperature,omitempty"` // 0 = 不发送
	MaxTokens     int                `json:"max_tokens,omitempty"`  // 0 = 不发送
	Stop          []string           `json:"stop,omitempty"`
	// ReasoningEffort 推理档位（D34；空 = 不发送）。
	ReasoningEffort string `json:"reasoning_effort,omitempty"`
	// EnableThinking 思考开关（D34；dashscope 系）；nil = 不发送。
	EnableThinking *bool `json:"enable_thinking,omitempty"`
}

type chatStreamOptions struct {
	IncludeUsage bool `json:"include_usage"`
}

type chatMessage struct {
	Role       string         `json:"role"`
	Content    any            `json:"content,omitempty"` // string 或 []chatContentPart；nil 时省略
	ToolCalls  []chatToolCall `json:"tool_calls,omitempty"`
	ToolCallID string         `json:"tool_call_id,omitempty"`
	// ReasoningContent role=assistant 的思维链回传（D42，生态事实标准字段名；
	// DeepSeek 带 tools 时强制回传）。端点不认时经 omit 剥离（unsupported_params 记录）。
	ReasoningContent string `json:"reasoning_content,omitempty"`
}

type chatContentPart struct {
	Type     string        `json:"type"` // "text" | "image_url"
	Text     string        `json:"text,omitempty"`
	ImageURL *chatImageURL `json:"image_url,omitempty"`
}

type chatImageURL struct {
	URL string `json:"url"` // data:<mime>;base64,<...>
}

type chatToolCall struct {
	ID       string       `json:"id"`
	Type     string       `json:"type"` // 恒为 "function"
	Function chatFuncCall `json:"function"`
}

type chatFuncCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"` // 原始 JSON 字符串（模型产出）
}

type chatTool struct {
	Type     string       `json:"type"` // 恒为 "function"
	Function chatToolFunc `json:"function"`
}

type chatToolFunc struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters,omitempty"`
}

// encodeChatRequest 把 GenerateRequest 编码为 SSE 流式请求体。
// includeUsage=false 时省略 stream_options（兼容性回退用）；omit 命中的参数一律不发
// （D34：服务端已知不认的字段，启动注入或本次 400 记录）。nil omit 不省略。
func encodeChatRequest(req port.GenerateRequest, includeUsage bool, omit map[string]bool) ([]byte, error) {
	out := chatRequest{
		Model:    req.Model,
		Stream:   true,
		Messages: make([]chatMessage, 0, len(req.Messages)),
	}
	if includeUsage && !omit["stream_options"] {
		out.StreamOptions = &chatStreamOptions{IncludeUsage: true}
	}
	for _, m := range req.Messages {
		cm, err := toChatMessage(m, omit)
		if err != nil {
			return nil, err
		}
		if cm.Role == "" {
			continue // toChatMessage 对"空内容且无思考/无调用"的消息返回零值表示跳过
		}
		out.Messages = append(out.Messages, cm)
	}
	if len(req.Tools) > 0 {
		out.Tools = toChatTools(req.Tools)
	}
	out.Temperature = req.Params.Temperature
	out.Stop = req.Params.Stop
	if req.Params.ReasoningEffort != "" && !omit["reasoning_effort"] {
		out.ReasoningEffort = req.Params.ReasoningEffort
	}
	if req.Params.Thinking != nil && !omit["enable_thinking"] {
		out.EnableThinking = req.Params.Thinking
	}
	// 本轮生成预算优先于采样参数（DESIGN §5.1 Budget）。
	out.MaxTokens = req.Budget.MaxOutputTokens
	if out.MaxTokens == 0 {
		out.MaxTokens = req.Params.MaxTokens
	}
	data, err := json.Marshal(out)
	if err != nil {
		return nil, fmt.Errorf("llm: 编码请求体: %w", err)
	}
	return data, nil
}

// toChatMessage 转换单条消息。omit 命中的 reasoning_content 不回传（D42 剥离兜底）。
func toChatMessage(m port.PromptMessage, omit map[string]bool) (chatMessage, error) {
	cm := chatMessage{Role: m.Role}
	switch m.Role {
	case "tool":
		cm.ToolCallID = m.CallID
		cm.Content = flattenText(m.Content)
		return cm, nil
	case "assistant":
		if m.Reasoning != "" && !omit["reasoning_content"] {
			cm.ReasoningContent = m.Reasoning // D42：思维链以消息级字段回传
		}
		if len(m.ToolCalls) > 0 {
			for _, call := range m.ToolCalls {
				args := strings.TrimSpace(string(call.Args))
				if args == "" {
					args = "{}"
				}
				cm.ToolCalls = append(cm.ToolCalls, chatToolCall{
					ID:       string(call.ID),
					Type:     "function",
					Function: chatFuncCall{Name: call.Name, Arguments: args},
				})
			}
			if text := flattenText(m.Content); text != "" {
				cm.Content = text
			}
			return cm, nil
		}
	}
	content, err := buildContent(m.Content)
	if err != nil {
		return chatMessage{}, err
	}
	if s, ok := content.(string); ok && s == "" && m.Role != "tool" {
		// 真·空消息（无正文、无思考、无调用）：不发（encodeChatRequest 据 Role 空跳过）。
		// 只带思考的 assistant 保留（D42：纯思考节点的回传），content 字段省略。
		if cm.ReasoningContent == "" {
			return chatMessage{}, nil
		}
		return cm, nil
	}
	cm.Content = content
	return cm, nil
}

// buildContent 全文本 → 纯字符串（最大兼容）；含图片 → content 分片数组（data URL 内联）。
func buildContent(parts []port.PromptPart) (any, error) {
	hasImage := false
	for _, p := range parts {
		if p.Kind == "image" {
			hasImage = true
			break
		}
	}
	if !hasImage {
		return flattenText(parts), nil
	}
	out := make([]chatContentPart, 0, len(parts))
	for _, p := range parts {
		if p.Kind == "image" {
			if len(p.Data) == 0 {
				return nil, errors.New("llm: 图片片段缺少数据（AttachmentStore 未提供字节）")
			}
			mime := p.MIME
			if mime == "" {
				mime = "image/png"
			}
			out = append(out, chatContentPart{
				Type:     "image_url",
				ImageURL: &chatImageURL{URL: "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(p.Data)},
			})
			continue
		}
		out = append(out, chatContentPart{Type: "text", Text: p.Text})
	}
	return out, nil
}

// flattenText 拼接文本片段。
func flattenText(parts []port.PromptPart) string {
	var b strings.Builder
	for _, p := range parts {
		if p.Kind == "image" || p.Text == "" {
			continue
		}
		if b.Len() > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(p.Text)
	}
	return b.String()
}

// toChatTools 工具声明转换；空 Schema 补默认对象结构（部分服务强制要求 parameters）。
func toChatTools(specs []tool.Spec) []chatTool {
	out := make([]chatTool, 0, len(specs))
	for _, s := range specs {
		params := s.Schema
		if len(params) == 0 {
			params = json.RawMessage(`{"type":"object","properties":{}}`)
		}
		out = append(out, chatTool{
			Type: "function",
			Function: chatToolFunc{
				Name:        s.Name,
				Description: s.Description,
				Parameters:  params,
			},
		})
	}
	return out
}

// ---------------------------------------------------------------------------
// 流式解析
// ---------------------------------------------------------------------------
