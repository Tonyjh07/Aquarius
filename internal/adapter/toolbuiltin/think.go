package toolbuiltin

import (
	"context"
	"encoding/json"

	"github.com/Tonyjh07/Aquarius/internal/domain/tool"
)

// jsonSchema 构造参数 JSON Schema（模型可见的工具声明）。
func jsonSchema(s string) json.RawMessage { return json.RawMessage(s) }

// thinkTool 显式整理思路（Safe、no-op，DESIGN §4.3）：
// 内容留在调用参数里随 assistant 节点入树，本身无副作用、不额外回填。
type thinkTool struct{}

func (t *thinkTool) Spec() tool.Spec {
	return tool.Spec{
		Name:        "think",
		Description: "在给出最终回答前显式整理思路（无副作用；内容会随调用记录在对话树中）。",
		Schema: jsonSchema(`{
			"type": "object",
			"properties": {
				"thought": {"type": "string", "description": "思考内容"}
			},
			"required": ["thought"]
		}`),
		Risk: tool.Safe,
	}
}

func (t *thinkTool) Execute(_ context.Context, call tool.Call) (tool.Result, error) {
	var a struct {
		Thought string `json:"thought"`
	}
	if err := decodeArgs(call, &a); err != nil {
		return tool.Result{}, err
	}
	return okResult("已记录思考（think 为 no-op，仅随调用入树）"), nil
}
