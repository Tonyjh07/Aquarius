package conversation

import (
	"time"

	"github.com/Tonyjh07/Aquarius/internal/domain/tool"
)

// PartKind 内容分片种类。
type PartKind string

const (
	PartText  PartKind = "text"
	PartImage PartKind = "image"
	PartAudio PartKind = "audio"
	PartDoc   PartKind = "doc"
)

// BlobRef 附件引用（sha256 内容寻址），由 AttachmentStore 解析（DESIGN §4.2 / D17）。
type BlobRef struct {
	Hash string `json:"hash"`
	MIME string `json:"mime"`
	Name string `json:"name"`
	Size int64  `json:"size"`
}

// Part 消息内容的多态片段。
type Part struct {
	Kind       PartKind `json:"kind"`
	Text       string   `json:"text,omitempty"`       // Kind=text；Kind=doc 时为提取文本（截断）
	Ref        *BlobRef `json:"ref,omitempty"`        // Kind=image|audio|doc：附件引用
	Transcript string   `json:"transcript,omitempty"` // Kind=audio：ASR 转写文本（模型只见文本）
}

// Role 消息角色。
type Role string

const (
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleTool      Role = "tool"
)

// Outcome 节点终态，提交时确定（DESIGN §4.1）。
type Outcome string

const (
	OutcomeDone      Outcome = "done"
	OutcomeCancelled Outcome = "cancelled"
	OutcomeError     Outcome = "error"
)

// Usage 一次生成的用量与成本；port 的流式 Delta 直接引用本类型（D17）。
type Usage struct {
	InputTokens  int     `json:"input_tokens"`
	OutputTokens int     `json:"output_tokens"`
	CostUSD      float64 `json:"cost_usd"`
}

// Message 树节点：创建后内容只读，值语义。
//
// 结构边指针 Parent 可随 Revise Carry 边转移改写（DESIGN §4.1 / D16）；
// 其余字段（Content/ToolCalls/ToolResult/Outcome/Model/Usage/CreatedAt）一经入树永不改写。
// 角色约束：user 无调用无结果；assistant 可带 ToolCalls；tool 必带 ToolResult。
type Message struct {
	ID         MessageID    `json:"id"`
	Parent     MessageID    `json:"parent"` // "" = 顶层消息（挂在虚拟 Root 下）
	Role       Role         `json:"role"`
	Content    []Part       `json:"content,omitempty"`
	ToolCalls  []tool.Call  `json:"tool_calls,omitempty"`
	ToolResult *tool.Result `json:"tool_result,omitempty"`
	Outcome    Outcome      `json:"outcome,omitempty"`
	Model      string       `json:"model,omitempty"`
	Usage      Usage        `json:"usage"`
	CreatedAt  time.Time    `json:"created_at"`
}

// Clone 返回 m 的深拷贝（含切片与指针），用于快照比对。
func (m Message) Clone() Message {
	out := m
	out.Content = cloneParts(m.Content)
	out.ToolCalls = cloneCalls(m.ToolCalls)
	out.ToolResult = cloneResult(m.ToolResult)
	return out
}

func cloneParts(in []Part) []Part {
	if in == nil {
		return nil
	}
	out := make([]Part, len(in))
	copy(out, in)
	for i := range out {
		if in[i].Ref != nil {
			ref := *in[i].Ref
			out[i].Ref = &ref
		}
	}
	return out
}

func cloneCalls(in []tool.Call) []tool.Call {
	if in == nil {
		return nil
	}
	out := make([]tool.Call, len(in))
	copy(out, in)
	for i := range out {
		if in[i].Args != nil {
			args := make([]byte, len(in[i].Args))
			copy(args, in[i].Args)
			out[i].Args = args
		}
	}
	return out
}

func cloneResult(in *tool.Result) *tool.Result {
	if in == nil {
		return nil
	}
	out := *in
	return &out
}
