package port

import (
	"context"
	"errors"
	"time"

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
	Role    string // "system" | "user" | "assistant" | "tool"
	Content []PromptPart
	// Reasoning role=assistant 的思维链回传承载（D42）：适配器映射为 reasoning_content 等
	// 消息级字段；空 = 无思考或 config `model.echo_thinking` 关闭（装配层过滤）。
	Reasoning string
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
	Name string
	// Caps 能力位（D173）；服务端 /models 不报模态，零值 = 未知（调用方按自己的
	// 配置声明补齐，不要拿零值当「不支持」展示给用户）。
	Caps ModelCaps
	// ContextWindow 上下文窗口 tokens（D177）；0 = 未声明/未知。服务端 /models 不返回
	// 窗口，只能来自 config；0 表示「按全局预算」，不是「窗口为零」。
	ContextWindow      int
	CostIn, CostOutUSD float64 // 每千 token 单价
}

// ModelCaps 模型能力位（D173）：三项独立声明，缺省按协议给（DESIGN §8）。
//   - Tools：能否接收 function calling 的工具清单并按 schema 发起调用
//   - Reasoning：能否接受原生思考参数（reasoning_effort / enable_thinking）并产出思维链
//   - Vision：能否接收图片输入（image 内容块）
//
// 能力位只描述**模型能做什么**，不描述「协议怎么写」——后者是适配器的事。
type ModelCaps struct {
	Tools, Reasoning, Vision bool
}

// Delta 流式增量。Usage 为流末尾的用量（domain/conversation.Usage，D17）。
type Delta struct {
	Text      string
	ToolCalls []ToolCallDelta // Index 分片聚合（app 层 ToolCallAssembler）
	Usage     *conversation.Usage
	// Reasoning 思维链分片（D34 展示 / D42 入树）：Text 承载推理文本——UI 实时呈现，
	// 同时进 commit buffer 提交为 PartThinking 分片；是否回传由装配层的
	// config `model.echo_thinking` 决定（port.Delta 本身不带口径）。
	Reasoning bool
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

// ErrQuota 配额/限流哨兵（D173）：适配器把「这把密钥被限流或额度耗尽」的 429 额外
// 标成本错误，**同时仍 Is(ErrTransient)**——重试层照旧退避重试，密钥池层先换下一把
// 钥匙（换钥匙比退避更快，且多账号场景下这本来就是用户要的语义）。
var ErrQuota = errors.New("port: 密钥被限流或额度耗尽")

// Quota 带「多久之后可重试」的配额错误包装（D173 密钥池冷却）：适配器从
// Retry-After 取 After（0 = 未知，由密钥池取缺省冷却）。Is 同时命中 ErrQuota 与
// ErrTransient，故不必在调用方逐个判两遍。
type Quota struct {
	After time.Duration
	Cause error
}

func (q *Quota) Error() string {
	if q.Cause != nil {
		return q.Cause.Error()
	}
	return ErrQuota.Error()
}

func (q *Quota) Unwrap() error { return q.Cause }

// Is 让 Quota 同时命中两个哨兵：调用方判 ErrQuota 是「该换钥匙」，判 ErrTransient
// 是「值得重试」——两者对同一件事的不同处置层，必须都成立。
func (q *Quota) Is(target error) bool { return target == ErrQuota || target == ErrTransient }

// QuotaAfter 取错误链里的冷却时长（非配额错误返回 0）。
func QuotaAfter(err error) time.Duration {
	var q *Quota
	if errors.As(err, &q) {
		return q.After
	}
	return 0
}

// TokenCounter 可选精确计数能力（三级计数链②，D26）：LLM 适配器可选择性实现——
// 本地 tokenizer（config model.tokenizer 指向 tokenizer.json）或服务端 count_tokens API。
// 实现即覆盖 app 的通用估算；未实现者由 app 回落估算③，并以服务端实测 usage①自校准。
type TokenCounter interface {
	CountTokens(ctx context.Context, text string) (int, error)
}
