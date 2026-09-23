// Package tool 定义工具调用的领域值对象：Spec（工具规格）、Call（模型发起的调用）、Result（执行回填）。
//
// 本包属于领域层：零第三方依赖、不引用任何端口；被 conversation（tool 消息承载调用与结果）
// 与 port（Tool/ToolRunner 契约）共同引用。值对象创建后只读。
package tool

import "encoding/json"

// CallID 工具调用标识，由模型流式分片聚合（app 层 ToolCallAssembler）后确定。
// 在整棵树内唯一；tool 结果节点以它做"存在性引用"（DESIGN §4.1 不变量 2）。
type CallID string

// Risk 工具风险等级；与 capability 权限判定取更严者（DESIGN §9）。
type Risk int

const (
	// Safe 无需确认即可执行。
	Safe Risk = iota
	// Confirm 执行前须经 Confirmer 逐次确认（写文件、执行命令等）。
	Confirm
)

// String 实现 fmt.Stringer。
func (r Risk) String() string {
	switch r {
	case Safe:
		return "safe"
	case Confirm:
		return "confirm"
	default:
		return "unknown"
	}
}

// Spec 工具规格，同时是装入 Prompt 的工具声明。
type Spec struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Schema      json.RawMessage `json:"schema,omitempty"` // 参数的 JSON Schema
	Risk        Risk            `json:"risk"`
}

// Call 模型发起的一次工具调用。Args 为模型产出的原始 JSON 参数对象，创建后只读。
type Call struct {
	ID   CallID          `json:"id"`
	Name string          `json:"name"`
	Args json.RawMessage `json:"args,omitempty"`
}

// Result 一次工具执行的回填结果，由 tool 角色节点承载。
// 失败照常回填（OK=false），让模型自行决定重试或改道（DESIGN §10）；
// Output/Err 均为不可信数据，只渲染不执行（DESIGN §9）。
type Result struct {
	CallID CallID `json:"call_id"`
	OK     bool   `json:"ok"`
	Output string `json:"output,omitempty"` // 结果文本，已按 tool_output_chars 裁剪
	Err    string `json:"err,omitempty"`    // 失败原因文本
}
