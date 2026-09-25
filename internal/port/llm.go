package port

import (
	"context"
	"errors"

	"github.com/Tonyjh07/Aquarius/internal/domain/conversation"
	"github.com/Tonyjh07/Aquarius/internal/domain/tool"
)

// PromptPart 装配后发给模型的内容片段。
type PromptPart struct {
	Kind string // "text" | "image"
	Text string
	MIME string
	Data []byte // 图片字节，由应用层经 AttachmentStore 解析后内联
}

// PromptMessage 装配后的一条模型消息。
type PromptMessage struct {
	Role      string // "system" | "user" | "assistant" | "tool"
	Content   []PromptPart
	ToolCalls []tool.Call
	CallID    string // role=tool：对应 assistant 调用的 ID
}

// Sampling 采样参数。
type Sampling struct {
	Temperature float64
	MaxTokens   int
	Stop        []string
	// ReasoningEffort 推理档位（D34；空 = 不发送，交服务端默认）。
	ReasoningEffort string
	// Thinking enable_thinking 布尔（D34；dashscope 系思考开关）；nil = 不发送。
	Thinking *bool
}

// TokenBudget 本轮生成预算（超出由装饰器层截断，D14）。
type TokenBudget struct {
	MaxOutputTokens int
	MaxCostUSD      float64
}

// GenerateRequest 一次生成请求（上下文已由 app 装配完毕）。
type GenerateRequest struct {
	Model    string
	Messages []PromptMessage
	Tools    []tool.Spec
	Params   Sampling
	Budget   TokenBudget
}

// ModelInfo 模型能力与单价声明；不支持的模态遇之明确报因（DESIGN §4.2）。
type ModelInfo struct {
	Name               string
	Vision, Audio      bool
	CostIn, CostOutUSD float64 // 每千 token 单价
}

// Delta 流式增量。Usage 为流末尾的用量（domain/conversation.Usage，D17）。
type Delta struct {
	Text      string
	ToolCalls []ToolCallDelta // Index 分片聚合（app 层 ToolCallAssembler）
	Usage     *conversation.Usage
}

// ToolCallDelta 工具调用的流式分片：同一 Index 的分片聚合为一个 tool.Call。
type ToolCallDelta struct {
	Index     int    // 调用序号
	ID        string // 首片携带
	Name      string // 首片携带
	ArgsDelta string // 参数 JSON 增量
}

// Stream 生成流：Recv 返回 io.EOF 表示正常结束。
type Stream interface {
	Recv() (Delta, error)
	Close() error // 中断生成（等价 ctx 取消）
}

// LLM 生成端口。
type LLM interface {
	// Generate 发起流式生成。契约：err != nil 时 stream 恒为 nil，调用方无需防御性 Close。
	Generate(ctx context.Context, req GenerateRequest) (Stream, error)
	Models(ctx context.Context) ([]ModelInfo, error)
}

// ErrTransient 瞬时错误哨兵（§10 装饰器重试判据）：适配器把 429/5xx/连接中断等
// 可重试错误用 %w 包进错误链；未标注的错误一律视为不可重试，ctx 取消不算瞬时。
var ErrTransient = errors.New("port: 瞬时错误（可重试）")

// TokenCounter 可选精确计数能力（三级计数链②，D26）：LLM 适配器可选择性实现——
// 本地 tokenizer（config model.tokenizer 指向 tokenizer.json）或服务端 count_tokens API。
// 实现即覆盖 app 的通用估算；未实现者由 app 回落估算③，并以服务端实测 usage①自校准。
type TokenCounter interface {
	CountTokens(ctx context.Context, text string) (int, error)
}
