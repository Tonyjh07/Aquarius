package pluginapi

import "time"

// value.go 稳定值类型层（plugin-design.md §3）：语义等价、字段冻结的公共词汇。
// 三条规则——按对外语义定义（不照抄内核内部语义）；内部实现细节不进；透传不解读的
// 字段一律 []byte 并写明「原样保真、逐轮回传」义务。每个导出类型注释带一条兼容承诺句
// （改这个类型破坏谁的二进制）。防腐映射点列表在 docs/plugin-design.md §3.1，
// plugingo 转换器照单核对。

// ---------- 防腐通用词汇 ----------

// BlobRef 附件引用（内容寻址）：字节在内核附件库，插件只携带领取所需的最小事实。
// 兼容承诺：改字段会破坏所有声明供 给附件相关能力的插件与 plugingo 转换器的二进制。
type BlobRef struct {
	Hash string // 内容寻址键（原样携带，不解读）
	MIME string
	Name string
	Size int64
}

// ---------- 通用风险与档位（词表冻结，两边常量值必须一致） ----------

// Risk 工具风险档。取值词表冻结：新增档位走新常量追加，不改已有值。
type Risk string

const (
	// RiskSafe 免确认档。
	RiskSafe Risk = "safe"
	// RiskConfirm 逐次确认档（走内核确认链，插件不得自造确认 UI）。
	RiskConfirm Risk = "confirm"
)

// Op 文件操作的字符串投影：内核侧 perm.Op 是 int 枚举，iota 顺序不是契约，
// v1 只冻结这两个词与空值（空 = 不涉及文件操作）。取值词表冻结。
type Op string

const (
	// OpRead 读。
	OpRead Op = "read"
	// OpWrite 写。
	OpWrite Op = "write"
)

// ---------- 用量与模型 ----------

// Usage 一次调用的 token 用量。InputTokens 是总输入（含缓存读写两段）——这是
// 对外口径，内核内部折算细节不影响本字段；CostUSD 不进 v1，成本折算归内核。
// 兼容承诺：改字段会破坏模型类插件（LanguageModel）与用量展示的防腐转换。
type Usage struct {
	InputTokens  int
	OutputTokens int
	// CacheRead, CacheWrite 缓存命中与写入的 token 数（计入总输入）；0 = 端点未报。
	CacheRead, CacheWrite int
	// Thinking 推理（思维链）token 数；0 = 端点未报。
	Thinking int
	// Safety 审核 token 数（少数端点）；0 = 未报。
	Safety int
}

// ModelCaps 模型能力位：三项独立声明，描述「模型能做什么」。零值 = 未知
// （未知不是不支持，调用方不得拿零值当「不支持」展示）。取值词表冻结。
// 兼容承诺：改位语义会破坏模型类插件的声明链与能力探测。
type ModelCaps struct {
	Tools     bool // 能接收工具清单并按 schema 发起调用
	Reasoning bool // 能接受原生思考参数并产出思维链
	Vision    bool // 能接收图片输入
}

// 能力位名字（配置数组与探测请求共用）。
const (
	CapTools     = "tools"
	CapReasoning = "reasoning"
	CapVision    = "vision"
)

// ModelInfo 模型能力与单价声明。单价每百万 token（与厂商报价同规则），0 = 未声明
// （「没填」不等于「免费」）。ContextWindow 0 = 未声明（按全局预算，不是窗口为零）。
// 兼容承诺：改字段会破坏模型类插件清单与降级链装配的转换。
type ModelInfo struct {
	Name          string
	Caps          ModelCaps
	ContextWindow int
	CostIn        float64 // 每百万输入 token
	CostOutUSD    float64 // 每百万输出 token
}

// ---------- 流式增量（模型侧） ----------

// Delta 流式增量：一段生成过程的最小播报单元。Usage 只在流末尾给一次（其余 Delta 为 nil）。
// ReasoningSignature 与 ServerBlock.Raw 是透传字节——原样保真、逐轮回传，不是理解。
// 兼容承诺：改字段会破坏模型类插件与装配层的转换；新增字段只加末尾并注释零值语义。
type Delta struct {
	Text      string
	ToolCalls []ToolCallDelta
	Usage     *Usage // 流末尾的用量；nil = 本增量不带用量
	// Reasoning 为 true 时 Text 承载思维链文本（展示与入树规则归内核）。
	Reasoning bool
	// ReasoningSignature 思考块签名（Anthropic 的 signature、Responses 的
	// encrypted_content 等）：透传字节，原样保真逐轮回传。空 = 本次没有签名。
	ReasoningSignature []byte
	// Title 服务端生成的会话标题，整串一次到达。空 = 本次没拿到（正常回落，不是错误）。
	Title string
	// Server 服务端工具块（如服务端读链接），整块一次到达；nil = 本增量不带。
	Server *ServerBlock
}

// ToolCallDelta 工具调用的流式分片：同一 Index 的分片聚合为一个完整调用（聚合归内核）。
// 兼容承诺：改字段破坏模型类插件与调用聚合的转换。
type ToolCallDelta struct {
	Index     int
	ID        string // 首片携带
	Name      string // 首片携带
	ArgsDelta string // 参数 JSON 增量
}

// ServerBlock 一个服务端工具块的投影。展示字段是从块里挑出来的；回传一律用 Raw——
// 协议要求「原样」，任何字段级重新编码都可能丢掉服务端认的东西。Raw 原样保真、逐轮回传，
// 插件不解码也不重编码。兼容承诺：改字段破坏涉及服务端工具块的模型插件转换。
type ServerBlock struct {
	Tool  string
	Kind  string // 块类别；回传时服务端靠该字段认块
	URL   string // 展示用
	Title string // 展示用；仅结果块有
	Raw   []byte // 原文：原样保真、逐轮回传
}

// ---------- 工具 ----------

// ToolSpec 工具声明。Schema 是 JSON Schema 的原文，透传不解读（内核按 Schema 向模型声明工具）。
// 兼容承诺：改字段破坏工具类插件与注册表的转换。
type ToolSpec struct {
	Name        string
	Description string
	Schema      []byte // JSON Schema 原文：透传不解读
	Risk        Risk
}

// ToolCall 一次工具调用。Args 是参数 JSON 原文，透传不解读。
// 兼容承诺：改字段破坏工具类插件与执行链的转换。
type ToolCall struct {
	ID   string
	Name string
	Args []byte // 参数 JSON 原文：透传不解读
}

// ImageRef 一张产出的图片引用：字节进附件库，这里只留寻址所需的最小事实。
// 兼容承诺：改字段破坏产出图片的工具插件转换。
type ImageRef struct {
	Hash string
	MIME string
	Name string
	Size int64
}

// ToolResult 一次工具调用的结果。失败时 Err 填给模型看的失败原因；OK=true 时 Err 留空。
// Fallback 是「建议改走别的路径」的目标地址（空 = 结果可用）。
// 兼容承诺：改字段破坏工具类插件与结果回填的转换。
type ToolResult struct {
	OK       bool
	Output   string
	Err      string
	Images   []ImageRef
	Fallback string
}

// ---------- 确认（走内核确认链） ----------

// ConfirmRequest 一次确认请求。无任务标识字段——确认器收到的只有「什么操作、什么
// 目标、什么风险」（并发语义见 docs/contracts.md §5.2）。Prompt 是已拼好的用户面文本；
// HasOp / HasRisk 区分「读」与「不涉及」「safe」与「不适用」。
// 兼容承诺：改字段破坏需要确认的插件与确认链 UI 的转换。
type ConfirmRequest struct {
	Origin     string // 来源标识（如插件名），审计归组用
	Tool       string // 工具或动作名
	Op         Op     // 文件操作；HasOp=false 时不涉及文件
	HasOp      bool
	Target     string // 目标路径、命令或其他标识
	Risk       Risk   // HasRisk=false 时 Risk 不适用
	HasRisk    bool
	Task       string // 这一趟的任务摘要（首 80 字）
	Prompt     string // 已拼好的用户面文本，界面直接展示
	CanPersist bool   // 这次确认允许提供「以后都允许」；false = 界面不给这个钮
}

// ConfirmAnswer 确认应答。拒绝可带原因（用户自填文本，原样附上不翻译）；Allow=true 时
// Reason 留空。Level 非空 = 用户直选了权限档，按那一档重判本次（此时 Allow 不参与判定）。
// Persist = 用户点了「以后都允许」。
// 兼容承诺：改字段破坏需要确认的插件与确认链 UI 的转换。
type ConfirmAnswer struct {
	Allow   bool
	Reason  string
	Level   string
	Persist bool
}

// 记忆的值类型不在这里：记忆没有「文档」这一层词汇（plugin-design.md §4），
// 只有 MemoryRequest / MemoryContext / LegacyMemorySnapshot 三件，见 memory.go。

// ---------- 语音 ----------

// SpeechSegment 一段语音：裸 PCM16 小端、单声道、16kHz。At 是这段在整次录音里的
// 起点，只用于展示与排错，不进模型上下文。PCM 原样字节。
// 兼容承诺：改字段破坏语音类插件与采集链的转换。
type SpeechSegment struct {
	PCM []byte
	At  time.Duration
}

// ---------- 摄取与输出 ----------

// 附件相关上限（与内核同值，报错文案由常量生成，不各写一个数字）。
const (
	MaxAttachments = 8        // 单条消息附件数上限
	MaxImageBytes  = 20 << 20 // 单张图片字节上限
)

// Attachment 一项待摄取附件：Path（文件路径）与 Blob（已入库字节）二选一。
// 兼容承诺：改字段破坏摄取类插件与输入链的转换。
type Attachment struct {
	Path string
	Blob *BlobRef
}

// RawInput 原始输入（文本 / 文件 / 剪贴板 / 麦克风）。File 与 Blob 是单附件简写
// （等价于 Attachments 各带一项），可与 Attachments 同时出现。
// 兼容承诺：改字段破坏摄取类插件与输入链的转换。
type RawInput struct {
	Kind        string // "text" | "file" | "clipboard" | "mic"；词表冻结
	Text        string
	File        string // 单文件简写
	Blob        *BlobRef
	Attachments []Attachment
	// Seconds mic 录音时长；0 = 缺省 60 秒。
	Seconds int
}

// Part 正文投影：摄取与输出两个面共用的内容分片。按对外语义重述——工具分片不进 v1
// （对外播报与摄取只用正文，工具往返是内核协作面）；Signature 与服务端块原文是透传字节，
// 原样保真逐轮回传。Kind 词表冻结。
// 兼容承诺：改字段破坏摄取类与输出类插件的转换。
type Part struct {
	Kind PartKind
	Text string // 正文 / 提取文本 / 思考文本（按 Kind）
	Ref  *BlobRef
	// Transcript 语音分片的转写文本（模型只见文本）。
	Transcript string
	// Signature 思考块签名：透传字节，原样保真逐轮回传。空 = 无回传要求。
	Signature []byte
	// ServerRaw 服务端工具块的原文：透传字节。空 = 非服务端块。
	ServerRaw []byte
}

// PartKind 内容分片种类（词表冻结；新增只追加常量）。
type PartKind string

const (
	PartText     PartKind = "text"
	PartImage    PartKind = "image"
	PartAudio    PartKind = "audio"
	PartDoc      PartKind = "doc"
	PartThinking PartKind = "thinking"
	PartServer   PartKind = "server"
)

// IngestReport 摄取结果：可入消息的正文分片 + 备注。
// 兼容承诺：改字段破坏摄取类插件与输入链的转换。
type IngestReport struct {
	Parts []Part
	Note  string
}

// OutputRequest 输出请求：一条已提交消息的正文分片。思考分片可能在内
// （对外播报正文——适配器自行筛选需要的种类）。
// 兼容承诺：改字段破坏输出类插件与输出扇出的转换。
type OutputRequest struct {
	MessageID string
	Role      string // 会话树节点角色（词表随内核冻结）
	Parts     []Part
}
