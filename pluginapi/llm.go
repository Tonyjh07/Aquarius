package pluginapi

import (
	"context"
	"time"
)

// llm.go 模型供给面（plugin-design.md §4）：一个插件接一家新厂商时要实现的全部形状。
// 请求已经由内核装配完毕——插件只负责「按协议发出去、把增量读回来」，不碰会话树。

// PromptKind 发给模型的内容分片种类。词表冻结：text 与 image 是正文两种形态，
// file 是服务端文件引用（带 FileID，正文留空）。
// 兼容承诺：改取值会破坏宿主装配层与模型插件的编译。
type PromptKind string

const (
	PromptText  PromptKind = "text"
	PromptImage PromptKind = "image"
	PromptFile  PromptKind = "file"
)

// PromptRole 一条消息的角色。tool 只是协议投影概念：树上没有 tool 节点，
// 由带工具调用的 assistant 消息拆出。词表冻结。
// 兼容承诺：改取值会破坏模型插件的请求构造。
type PromptRole string

const (
	PromptSystem    PromptRole = "system"
	PromptUser      PromptRole = "user"
	PromptAssistant PromptRole = "assistant"
	PromptTool      PromptRole = "tool"
)

// PromptPart 一个内容分片。Data 是已解析的图片字节（不是路径也不是 URL）——
// 取字节要去附件库，这一步内核做完再交给插件。
// 兼容承诺：改字段会破坏模型插件的请求构造与装配层的转换。
type PromptPart struct {
	Kind   PromptKind
	Text   string
	MIME   string
	Data   []byte
	FileID string // Kind=file：服务端文件引用，正文留空
}

// PromptMessage 一条模型消息。Reasoning 与 ReasoningSignature 是思考回传的两条通道：
// 前者是文本（是否回传由内核配置决定，插件照发即可），后者是透传签名（只认自家前缀，
// 其他来源的签名内核已在装配时剔除）。
// 兼容承诺：改字段会破坏模型插件与装配层的转换。
type PromptMessage struct {
	Role               PromptRole
	Content            []PromptPart
	Reasoning          string
	ReasoningSignature string
	ToolCalls          []ToolCall // role=assistant：本轮发起的调用
	CallID             string     // role=tool：对应上次 assistant 调用的 ID
	ServerBlocks       [][]byte   // 服务端工具块原文：原样保真、逐轮回传
}

// Sampling 采样参数。浮点用指针：nil = 不发送（交服务端默认），而 temperature 的 0
// 是「要确定性输出」这个有意义的取值，零值兼作「不发送」就表达不出来。整数
// MaxTokens 仍用 0 表示不发送——0 个输出 token 没有语义。
// 兼容承诺：改字段会破坏模型插件与逐模型覆盖的转换。
type Sampling struct {
	Temperature     *float64
	TopP            *float64
	MaxTokens       int
	Stop            []string
	ReasoningEffort string
	Thinking        *bool
}

// TokenBudget 本轮生成预算；超限由内核装饰器截断，插件不参与扣减。
// 兼容承诺：改字段会破坏模型插件与预算装配的转换。
type TokenBudget struct {
	MaxOutputTokens int
	MaxCostUSD      float64
}

// GenerateRequest 一次生成请求。ServerTools 非空时 Tools 必须为空——协议里服务端工具
// 与本地 function tools 不能同时声明（服务端会以 400 拒绝）。
// 兼容承诺：改字段会破坏模型插件的请求构造与内核装配。
type GenerateRequest struct {
	Model       string
	Messages    []PromptMessage
	Tools       []ToolSpec
	Params      Sampling
	Budget      TokenBudget
	Title       bool     // 要求服务端顺带生成会话标题（只在首轮打开）
	ServerTools []string // 本轮声明的服务端工具名
}

// Stream 一次流式生成的读端。契约：Generate 返回 err 时 stream 恒为 nil，
// 调用方不需要防御性地 Close。
//
// 兼容承诺：改方法集会破坏全部模型插件与内核的收流路径。
type Stream interface {
	// Recv 读下一个增量；流结束时返回 io.EOF 或包装了 io.EOF 的错误。
	Recv() (Delta, error)
	// Close 中断生成，等价于取消 ctx。可重复调用。
	Close() error
}

// LanguageModel 模型供给面。
// 兼容承诺：改方法集或签名会破坏所有声明 provides: llm 的插件。
type LanguageModel interface {
	Generate(ctx context.Context, req GenerateRequest) (Stream, error)
	Models(ctx context.Context) ([]ModelInfo, error)
}

// TokenCounter 可选扩展：精确计数，覆盖内核的估算链（内核按 type assertion 探测）。
// 兼容承诺：改签名会破坏实现了计数能力的插件。
type TokenCounter interface {
	CountTokens(ctx context.Context, text string) (int, error)
}

// ProbeVerdict 单项能力探测的三态结论。探不通 ≠ 不支持：只有 Supported 与
// Unsupported 会被写回声明，Inconclusive 保持原样。
// 兼容承诺：改取值会破坏宿主的探测结论落盘。
type ProbeVerdict int

const (
	ProbeInconclusive ProbeVerdict = iota
	ProbeSupported
	ProbeUnsupported
)

// ProbeResult 单项能力探测结论。Reason 是给用户看的一句话（判定依据，或没得出结论的
// 原因），可能夹带服务端原文片段——那是数据，不是文案。
// 兼容承诺：改字段会破坏宿主的探测结果展示与写回。
type ProbeResult struct {
	Capability string
	Verdict    ProbeVerdict
	Reason     string
	Elapsed    time.Duration
	Usage      Usage
}

// Prober 可选扩展：自行构造最小请求实测某模型是否具备某项能力（内核按 type assertion 探测）。
// 「怎么问」与「什么算答上来了」都是协议相关的知识，所以判定留在插件侧。
// 兼容承诺：改签名会破坏实现了探测能力的插件。
type Prober interface {
	Probe(ctx context.Context, model, capability string) (ProbeResult, error)
}
