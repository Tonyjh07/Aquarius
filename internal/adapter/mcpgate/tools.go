package mcpgate

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/Tonyjh07/Aquarius/internal/domain/tool"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

// Tool port.Tool 投影：模型只见 mcp:<server>:<tool> 一个整体名（§6.3），
// MCP 原名只在 Execute 时还原。风险取 server 声明的 risk（§9 矩阵按工具列判定）。
type Tool struct {
	server *Server
	orig   string // MCP 侧原工具名
	spec   tool.Spec
}

var _ port.Tool = (*Tool)(nil)

// Spec 模型可见声明（Name 已加前缀）。
func (t *Tool) Spec() tool.Spec { return t.spec }

// Tools 拉取 tools/list 并投影为 port.Tool 列表。
func (s *Server) Tools(ctx context.Context) ([]port.Tool, error) {
	list, err := s.cs.ListTools(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("tools/list（插件 %s）: %w", s.name, err)
	}
	out := make([]port.Tool, 0, len(list.Tools))
	for _, mt := range list.Tools {
		if mt.Name == "" {
			continue
		}
		spec := tool.Spec{
			Name:        "mcp:" + s.name + ":" + mt.Name,
			Description: mt.Description,
			Schema:      schemaBytes(mt.InputSchema),
			Risk:        s.risk,
		}
		if strings.TrimSpace(spec.Description) == "" {
			spec.Description = fmt.Sprintf("MCP 插件 %s 提供的工具 %s", s.name, mt.Name)
		}
		out = append(out, &Tool{server: s, orig: mt.Name, spec: spec})
	}
	return out, nil
}

// schemaBytes InputSchema（client 侧为 map[string]any）→ 合法 JSON Schema 字节；
// 缺失/不可序列化时退回空对象 schema（模型仍可发起无参调用）。
func schemaBytes(schema any) []byte {
	if schema == nil {
		return []byte(`{"type":"object"}`)
	}
	b, err := json.Marshal(schema)
	if err != nil || len(b) == 0 {
		return []byte(`{"type":"object"}`)
	}
	return b
}

// Execute tools/call：错误按 §10 经 runner 翻译——超时/取消判归其类，
// 其余（协议错、服务端工具错）以 error 上交由 runner 转 OK:false 回填模型。
func (t *Tool) Execute(ctx context.Context, call tool.Call) (tool.Result, error) {
	start := time.Now()
	var args map[string]any
	if len(bytes.TrimSpace(call.Args)) > 0 {
		if err := json.Unmarshal(call.Args, &args); err != nil {
			t.server.recordCall(time.Since(start), false)
			return tool.Result{}, fmt.Errorf("参数不是 JSON 对象: %w", err)
		}
	}
	res, err := t.server.cs.CallTool(ctx, &mcp.CallToolParams{Name: t.orig, Arguments: args})
	if err != nil {
		t.server.recordCall(time.Since(start), false)
		return tool.Result{}, fmt.Errorf("MCP 调用失败（%s）: %w", t.spec.Name, err)
	}
	text := contentText(res.Content)
	if res.IsError {
		// §6.3：服务端声明的工具级错误（IsError）→ 让模型看见并自纠。
		t.server.recordCall(time.Since(start), false)
		if strings.TrimSpace(text) == "" {
			text = "工具报告失败（未附带信息）"
		}
		return tool.Result{}, toolFailure(text)
	}
	t.server.recordCall(time.Since(start), true)
	return tool.Result{OK: true, Output: text}, nil
}

// toolFailure 构造工具级失败错误（runner 转 OK:false 回填模型）。
func toolFailure(msg string) error { return errors.New(msg) }

// contentText MCP 内容 → 结果文本：文本直连，其余类型只留占位标注
// （§9：不可信数据只渲染不执行，二进制内容不外泄进上下文）。
func contentText(contents []mcp.Content) string {
	var b strings.Builder
	for i, c := range contents {
		if i > 0 {
			b.WriteByte('\n')
		}
		switch v := c.(type) {
		case *mcp.TextContent:
			b.WriteString(v.Text)
		case nil:
			b.WriteString("[空内容]")
		default:
			fmt.Fprintf(&b, "[%T]", c)
		}
	}
	return b.String()
}
