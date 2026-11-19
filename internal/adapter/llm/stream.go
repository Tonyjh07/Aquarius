package llm

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/Tonyjh07/Aquarius/internal/domain/conversation"
	"github.com/Tonyjh07/Aquarius/internal/i18n"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

// stream SSE 生成流：逐行解析 data: 分片，[DONE]、连接关闭、或解码器宣告结束
// （Responses 的 response.completed）都算正常收流（io.EOF）。
//
// 三套协议共用这条骨架：SSE 的「行怎么读」与「流何时结束」是同一件事，差异全在
// decode——把一条 data 载荷映射成端口增量（D175 规则一）。
type stream struct {
	ctx     context.Context
	cancel  context.CancelFunc
	body    io.ReadCloser
	scanner *bufio.Scanner
	// decode 单条 data 载荷 → 增量；ok=false = 本片无增量（进度/边界事件）。
	// 返回 error 即中止收流（服务端流式错误、分片解析失败）。
	decode func(payload string) (port.Delta, bool, error)
	// finish 解码器宣告「此后收流结束」（Responses 的 response.completed 之后不再有增量）。
	finish bool
	done   bool
	// argsSeen / sigSeen 按输出序号记住「这一路的工具参数 / 思考签名已经拿过」，
	// 供收尾事件（output_item.done、response.completed）补发缺漏时去重。
	argsSeen map[int]bool
	sigSeen  map[int]bool
	// names 端点别名 → 内部工具名（D195）：回传的调用先换算，再交给上层派发。
	names *wireNames
	// trace 原始响应留档（§7.1）；nil = 不采。收流结束时恰回调一次。
	trace *traceRec
}

// newRawStream 建流但不设解码器（调用方随后自行指定，见 responses.go）。
func newRawStream(ctx context.Context, cancel context.CancelFunc, body io.ReadCloser, names *wireNames) *stream {
	sc := bufio.NewScanner(body)
	sc.Buffer(make([]byte, 0, 64<<10), 16<<20) // 单条 SSE 分片上限 16MB
	return &stream{ctx: ctx, cancel: cancel, body: body, scanner: sc, names: names}
}

// newStream chat 协议的生成流（默认解码器）。
func newStream(ctx context.Context, cancel context.CancelFunc, body io.ReadCloser, names *wireNames) *stream {
	s := newRawStream(ctx, cancel, body, names)
	s.decode = decodeChatPayload
	return s
}

// Recv 返回下一个增量；io.EOF 表示正常结束。
func (s *stream) Recv() (port.Delta, error) {
	if s.done {
		return port.Delta{}, io.EOF
	}
	for s.scanner.Scan() {
		line := strings.TrimSpace(s.scanner.Text())
		if line == "" || strings.HasPrefix(line, ":") {
			continue // 空行/注释
		}
		if !strings.HasPrefix(line, "data:") {
			continue // event:/id: 等行忽略
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if payload == "[DONE]" {
			s.done = true
			s.endTrace(port.WireEndDone)
			return port.Delta{}, io.EOF
		}
		d, ok, err := s.decode(payload)
		if err != nil {
			s.endTrace(port.WireEndError)
			return port.Delta{}, err
		}
		if !ok {
			if s.finish {
				s.done = true
				s.endTrace(port.WireEndDone)
				return port.Delta{}, io.EOF
			}
			continue // 无增量分片
		}
		if s.finish {
			s.done = true // 本片增量照发，但流到此为止
			s.endTrace(port.WireEndDone)
		}
		return s.localize(d), nil
	}
	s.done = true
	if err := s.scanner.Err(); err != nil {
		// 父 ctx 取消优先报告 ctx 错误（连接层错误对调用方无意义）。
		if cerr := s.ctx.Err(); cerr != nil {
			s.endTrace(port.WireEndCancelled)
			return port.Delta{}, cerr
		}
		s.endTrace(port.WireEndError)
		return port.Delta{}, fmt.Errorf("%s: %w", i18n.T("llm.err.read_stream"), err)
	}
	// 未见 [DONE] 即断流：多数兼容服务直接关连接，视作正常结束——但对排查而言
	// 「没等到结束标记」和「按协议收尾」是两件事，留档里分开记。
	s.endTrace(port.WireEndEOF)
	return port.Delta{}, io.EOF
}

// attachTrace 挂上原始响应留档（收流结束时交出）。
func (s *stream) attachTrace(tr *traceRec) { s.trace = tr }

// endTrace 结束留档（nil 安全；重复调用由 traceRec 自己挡）。
func (s *stream) endTrace(end string) {
	if s.trace != nil {
		s.trace.finish(end)
	}
}

// Close 中断生成（等价 ctx 取消）。
func (s *stream) Close() error {
	s.done = true
	s.cancel()
	s.endTrace(port.WireEndCancelled) // 已正常收尾的不会被改写（once）
	return s.body.Close()
}

// localize 回传的工具调用名 → 内部名（D195）：声明发的是端点别名，进会话树/派发
// 必须换回 `mcp:<server>:<tool>`。
func (s *stream) localize(d port.Delta) port.Delta {
	if s.names == nil || len(d.ToolCalls) == 0 {
		return d
	}
	for i := range d.ToolCalls {
		d.ToolCalls[i].Name = s.names.Local(d.ToolCalls[i].Name)
	}
	return d
}

// markArgsSeen / markSigSeen 记忆某输出序号已收过内容（懒建 map）。
func (s *stream) markArgsSeen(index int) {
	if s.argsSeen == nil {
		s.argsSeen = make(map[int]bool)
	}
	s.argsSeen[index] = true
}

func (s *stream) markSigSeen(index int) {
	if s.sigSeen == nil {
		s.sigSeen = make(map[int]bool)
	}
	s.sigSeen[index] = true
}

// decodeChatPayload chat.completion.chunk → 增量（D175 规则一：SSE 骨架共用，载荷解析
// 各协议一份）。无增量的分片（finish_reason 等）返回 ok=false。
func decodeChatPayload(payload string) (port.Delta, bool, error) {
	var chunk chatChunk
	if err := json.Unmarshal([]byte(payload), &chunk); err != nil {
		return port.Delta{}, false, fmt.Errorf("%s: %w", i18n.Tf("llm.err.stream_decode", "snippet", snippet(payload, 200)), err)
	}
	if chunk.Error != nil {
		return port.Delta{}, false, errors.New(i18n.Tf("llm.err.stream_error", "message", chunk.Error.Message))
	}
	d := chunk.toDelta()
	if d.Text == "" && len(d.ToolCalls) == 0 && d.Usage == nil {
		return port.Delta{}, false, nil
	}
	return d, true, nil
}

// chatChunk SSE data 载荷（OpenAI chat.completion.chunk）。
type chatChunk struct {
	Choices []chatChoice `json:"choices"`
	Usage   *chatUsage   `json:"usage"`
	Error   *chatError   `json:"error"`
}

type chatChoice struct {
	Delta struct {
		Content string `json:"content"`
		// ReasoningContent / Reasoning 思维链的两种常见键名（deepseek、qwen/dashscope、
		// mimo 等 OpenAI 兼容端，D34）；与 content 同分片出现时 content 优先。
		ReasoningContent string          `json:"reasoning_content"`
		Reasoning        string          `json:"reasoning"`
		ToolCalls        []chatCallDelta `json:"tool_calls"`
	} `json:"delta"`
	FinishReason string `json:"finish_reason"`
}

type chatCallDelta struct {
	Index    int    `json:"index"`
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

// chatUsage chat.completion 的 usage（D308 分项）。
//
// 同一个键名在各家 OpenAI 兼容端上并不统一：缓存命中 OpenAI 报
// prompt_tokens_details.cached_tokens、DeepSeek 报 prompt_cache_hit_tokens，推理 token
// 则有 completion_tokens_details.reasoning_tokens 与 output_tokens_details.thinking_tokens
// 两种写法。两种都收，谁先报用谁。
type chatUsage struct {
	PromptTokens         int `json:"prompt_tokens"`
	CompletionTokens     int `json:"completion_tokens"`
	PromptCacheHitTokens int `json:"prompt_cache_hit_tokens"`
	PromptTokensDetails  *struct {
		CachedTokens int `json:"cached_tokens"`
	} `json:"prompt_tokens_details"`
	CompletionTokensDetails *struct {
		ReasoningTokens int `json:"reasoning_tokens"`
	} `json:"completion_tokens_details"`
	OutputTokensDetails *struct {
		ThinkingTokens  int `json:"thinking_tokens"`
		ReasoningTokens int `json:"reasoning_tokens"`
	} `json:"output_tokens_details"`
	SafetyTokens int `json:"safety_tokens"`
}

// toUsage 映射到树上的用量。InputTokens 用 prompt_tokens 原值：OpenAI 系本来就含命中量。
func (u chatUsage) toUsage() conversation.Usage {
	out := conversation.Usage{InputTokens: u.PromptTokens, OutputTokens: u.CompletionTokens}
	if u.PromptTokensDetails != nil {
		out.CacheRead = u.PromptTokensDetails.CachedTokens
	}
	if out.CacheRead == 0 {
		out.CacheRead = u.PromptCacheHitTokens
	}
	if u.CompletionTokensDetails != nil {
		out.Thinking = u.CompletionTokensDetails.ReasoningTokens
	}
	if u.OutputTokensDetails != nil {
		if out.Thinking == 0 {
			out.Thinking = u.OutputTokensDetails.ThinkingTokens
		}
		if out.Thinking == 0 {
			out.Thinking = u.OutputTokensDetails.ReasoningTokens
		}
	}
	out.Safety = u.SafetyTokens
	return out
}

type chatError struct {
	Message string `json:"message"`
}

// toDelta 映射为端口增量（Index 分片聚合交给 app 层）。
func (ch chatChunk) toDelta() port.Delta {
	var d port.Delta
	if len(ch.Choices) > 0 {
		delta := ch.Choices[0].Delta
		switch {
		case delta.Content != "":
			d.Text = delta.Content // 正文优先：与思维链同片时推理丢弃（实践中不共存）
		case delta.ReasoningContent != "":
			d.Text, d.Reasoning = delta.ReasoningContent, true
		case delta.Reasoning != "":
			d.Text, d.Reasoning = delta.Reasoning, true
		}
		for _, tc := range delta.ToolCalls {
			d.ToolCalls = append(d.ToolCalls, port.ToolCallDelta{
				Index:     tc.Index,
				ID:        tc.ID,
				Name:      tc.Function.Name,
				ArgsDelta: tc.Function.Arguments,
			})
		}
	}
	if ch.Usage != nil {
		u := ch.Usage.toUsage()
		d.Usage = &u
	}
	return d
}
