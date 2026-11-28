package toolbuiltin

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/Tonyjh07/Aquarius/internal/domain/tool"
)

// jsonSchema 构造参数 JSON Schema（模型可见的工具声明）。
func jsonSchema(s string) json.RawMessage { return json.RawMessage(s) }

// thinkTool 显式整理思路（Safe、no-op，§4.3）：
// 内容留在调用参数里随 assistant 节点入树，本身无副作用、不额外回填。
type thinkTool struct{}

func (t *thinkTool) Spec() tool.Spec {
	return tool.Spec{
		Name:        "think",
		Description: "Explicitly structure your thoughts before the final answer (no side effects; the content is kept in the conversation tree with the call).",
		Schema: jsonSchema(`{
			"type": "object",
			"properties": {
				"thought": {"type": "string", "description": "the thought content"}
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
	// schema 的 required 只是模型可见声明，服务端不强制——运行时兜一道：
	// 空白内容回失败结果，不给树里留一个空调用。
	if strings.TrimSpace(a.Thought) == "" {
		return tool.Result{}, errors.New("think: thought is empty")
	}
	return okResult("recorded (think is a no-op; the call is kept in the conversation tree)"), nil
}
