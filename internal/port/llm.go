package port

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/Tonyjh07/Aquarius/internal/domain/conversation"
	"github.com/Tonyjh07/Aquarius/internal/domain/tool"
)

// PromptKind 装配后内容分片的种类（D227）。与树上的 conversation.PartKind 不是同一套取值：
// 领域侧含 doc / thinking 这类「树上怎么存」的区分，投影到模型请求只剩 text 与 image
// （thinking 走消息级 Reasoning，doc 已在前置摄取阶段拍平成 text）。
type PromptKind string

const (
	PromptText  PromptKind = "text"
	PromptImage PromptKind = "image"
)

// PromptRole 装配后一条消息的角色（D227）。tool 仅为 **API 投影概念**（OpenAI 协议要求
// assistant(tool_calls)→tool 应答交错）；树上无 tool 节点，装配期由 assistant 节点的
// Tool 分片拆出（D95）。
type PromptRole string

const (
	PromptSystem    PromptRole = "system"
	PromptUser      PromptRole = "user"
	PromptAssistant PromptRole = "assistant"
	PromptTool      PromptRole = "tool"
)

// PromptPart 装配后发给模型的内容片段。
type PromptPart struct {
	Kind PromptKind
	Text string
	MIME string
	Data []byte // 图片字节，由应用层经 AttachmentStore 解析后内联
}

// PromptMessage 装配后的一条模型消息。
type PromptMessage struct {
	Role    PromptRole
	Content []PromptPart
	// Reasoning role=assistant 的思维链回传承载（D42）：适配器映射为 reasoning_content 等
	// 消息级字段；空 = 无思考或 config `model.echo_thinking` 关闭（装配层过滤）。
	Reasoning string
	// ReasoningSignature 思考块签名回传（D175）：树内 PartThinking.Signature 原样带上，
	// 适配器按协议前缀识别归属（`anthropic:` / `responses:`）——只认自家前缀、其余忽略，
	// 避免跨 provider 复制会话时把 A 家的签名发给 B 家。空 = 无签名（Chat Completions 现状）。
	ReasoningSignature string
	ToolCalls          []tool.Call
	CallID             string // role=tool：对应 assistant 调用的 ID
}

// Sampling 采样参数（D204）。
//
// **浮点参数用指针**：nil = 不发送（交服务端默认），非 nil = 照发。temperature 的 0 是
// 「要确定性输出」这个有意义的取值，用零值兼作「不发送」就永远表达不出来。整数
// （MaxTokens）仍以 0 表示不发送——0 个输出 token 没有语义。
type Sampling struct {
	Temperature *float64
	// TopP 核采样（D204）；nil = 不发送。
	TopP      *float64
	MaxTokens int      // 0 = 不发送；本轮预算 Budget.MaxOutputTokens 非零时以预算为准
	Stop      []string // 空 = 不发送
	// ReasoningEffort 推理档位（D34；空 = 不发送，交服务端默认）。
	ReasoningEffort string
	// Thinking enable_thinking 布尔（D34；dashscope 系思考开关）；nil = 不发送。
	Thinking *bool
}

// Overlay 用 o 里非 nil / 非零的字段覆盖 s（D204 逐模型覆盖用）：调用方给整链缺省，
// 模型条目只写「想改的那几项」，其余继承缺省。Stop 非空即整体替换——停止串没有
// 「部分覆盖」的语义（拼起来会得到用户没写过的组合）。
func (s Sampling) Overlay(o Sampling) Sampling {
	if o.Temperature != nil {
		s.Temperature = o.Temperature
	}
	if o.TopP != nil {
		s.TopP = o.TopP
	}
	if o.MaxTokens != 0 {
		s.MaxTokens = o.MaxTokens
	}
	if len(o.Stop) > 0 {
		s.Stop = o.Stop
	}
	if o.ReasoningEffort != "" {
		s.ReasoningEffort = o.ReasoningEffort
	}
	if o.Thinking != nil {
		s.Thinking = o.Thinking
	}
	return s
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
	ContextWindow int
	// CostIn, CostOutUSD 单价，**每百万 token**（D205，与厂商报价同口径）；0 = 未声明
	// （「没填」不等于「免费」，故不显示费用）。按一次调用的用量折算时除以 1e6。
	CostIn, CostOutUSD float64
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

// 能力位名字（D173）：配置文件 caps 数组的取值，也是探测请求的能力标识。
const (
	CapTools     = "tools"
	CapReasoning = "reasoning"
	CapVision    = "vision"
)

// CapNames 已开启的能力位名字，顺序固定为 tools / reasoning / vision
// （配置写回与界面展示共用，避免同一集合出两种顺序）。
func (c ModelCaps) CapNames() []string {
	out := make([]string, 0, 3)
	if c.Tools {
		out = append(out, CapTools)
	}
	if c.Reasoning {
		out = append(out, CapReasoning)
	}
	if c.Vision {
		out = append(out, CapVision)
	}
	return out
}

// CapsFromNames 名字集合 → 能力位；认不出的名字忽略（写错一个词不该作废整条声明，
// 也不需要为此拒绝启动——真正要用某项能力时会有明确报因）。
func CapsFromNames(names []string) ModelCaps {
	var c ModelCaps
	for _, n := range names {
		switch strings.ToLower(strings.TrimSpace(n)) {
		case CapTools:
			c.Tools = true
		case CapReasoning:
			c.Reasoning = true
		case CapVision:
			c.Vision = true
		}
	}
	return c
}

// ProbeVerdict 单项能力探测的三态结论（D174）。探不通 ≠ 不支持：只有 Supported /
// Unsupported 会被写回声明，Inconclusive 一律保持原样。
type ProbeVerdict int

const (
	ProbeInconclusive ProbeVerdict = iota // 无结论：网络/超时/5xx，或参数被静默忽略
	ProbeSupported
	ProbeUnsupported
)

// ProbeResult 单项能力探测结果（D174）。
type ProbeResult struct {
	Capability string // port.CapTools / CapReasoning / CapVision
	Verdict    ProbeVerdict
	// Reason 判定依据或没得出结论的原因（一句话）。会显示给用户，用中文；
	// 其中可能夹带服务端原文片段——那是数据，不是本项目的文案。
	Reason  string
	Elapsed time.Duration
	Usage   conversation.Usage // 服务端回报的用量；探测不强制要 usage，零值 = 未知
}

// Prober 可选能力：适配器自行构造最小请求，实测某模型是否具备某项能力（D174）。
// 判定逻辑在适配器——「怎么问」和「什么算答上来了」都是协议相关的知识。
//
// err != nil 仅表示「这次探测根本没跑起来」（模型名为空、上下文已取消等）；
// 网络/超时/服务端报错属于探测结论的一部分，走 ProbeInconclusive 返回。
type Prober interface {
	Probe(ctx context.Context, model, capability string) (ProbeResult, error)
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
	// ReasoningSignature 思考块签名（D175）：协议适配器在思考结束后一次性给出（Anthropic
	// 的 thinking block signature、Responses 的 encrypted_content），随当前轮思考缓冲
	// 并入 PartThinking.Signature。空 = 本次没有签名（多数端点）。
	ReasoningSignature string
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
