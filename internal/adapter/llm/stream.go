package llm

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/Tonyjh07/Aquarius/internal/domain/conversation"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

// stream SSE 生成流：逐行解析 data: 分片，[DONE] 或连接关闭 = 正常结束（io.EOF）。
type stream struct {
	ctx     context.Context
	cancel  context.CancelFunc
	body    io.ReadCloser
	scanner *bufio.Scanner
	done    bool
}

func newStream(ctx context.Context, cancel context.CancelFunc, body io.ReadCloser) *stream {
	sc := bufio.NewScanner(body)
	sc.Buffer(make([]byte, 0, 64<<10), 16<<20) // 单条 SSE 分片上限 16MB
	return &stream{ctx: ctx, cancel: cancel, body: body, scanner: sc}
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
			return port.Delta{}, io.EOF
		}
		var chunk chatChunk
		if err := json.Unmarshal([]byte(payload), &chunk); err != nil {
			return port.Delta{}, fmt.Errorf("llm: 解析流分片: %w（片段: %s）", err, snippet(payload, 200))
		}
		if chunk.Error != nil {
			return port.Delta{}, fmt.Errorf("llm: 服务端流式错误: %s", chunk.Error.Message)
		}
		d := chunk.toDelta()
		if d.Text == "" && len(d.ToolCalls) == 0 && d.Usage == nil {
			continue // finish_reason 等无增量分片
		}
		return d, nil
	}
	s.done = true
	if err := s.scanner.Err(); err != nil {
		// 父 ctx 取消优先报告 ctx 错误（连接层错误对调用方无意义）。
		if cerr := s.ctx.Err(); cerr != nil {
			return port.Delta{}, cerr
		}
		return port.Delta{}, fmt.Errorf("llm: 读取生成流: %w", err)
	}
	// 未见 [DONE] 即断流：多数兼容服务直接关连接，视作正常结束。
	return port.Delta{}, io.EOF
}

// Close 中断生成（等价 ctx 取消）。
func (s *stream) Close() error {
	s.done = true
	s.cancel()
	return s.body.Close()
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

type chatUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
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
		u := conversation.Usage{
			InputTokens:  ch.Usage.PromptTokens,
			OutputTokens: ch.Usage.CompletionTokens,
		}
		d.Usage = &u
	}
	return d
}
