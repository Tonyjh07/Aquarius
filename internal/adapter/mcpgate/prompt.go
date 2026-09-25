package mcpgate

import (
	"context"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/Tonyjh07/Aquarius/internal/plugin"
)

// Prompts prompts/list 快照（Dial 时缓存；/mcp:<server>:<prompt> 动态命令注册用）。
func (s *Server) Prompts(_ context.Context) ([]plugin.PromptInfo, error) {
	out := make([]plugin.PromptInfo, 0, len(s.prompts))
	for _, p := range s.prompts {
		if p == nil || p.Name == "" {
			continue
		}
		out = append(out, plugin.PromptInfo{Name: p.Name, Description: p.Description})
	}
	return out, nil
}

// RenderPrompt prompts/get → 纯文本（§6.3 暴露为 /mcp:<server>:<prompt>）。
//
// 参数映射：按 prompts/list 的声明位次对应（单参数直接落首个声明名）；
// 缺必填参数返回用法错误；多个返回消息拼接为一段文本（非文本内容留占位）。
func (s *Server) RenderPrompt(ctx context.Context, name string, args []string) (string, error) {
	arguments, _, err := s.mapArgs(name, args)
	if err != nil {
		return "", err
	}
	res, err := s.cs.GetPrompt(ctx, &mcp.GetPromptParams{Name: name, Arguments: arguments})
	if err != nil {
		return "", fmt.Errorf("prompts/get %s（插件 %s）: %w", name, s.name, err)
	}
	var b strings.Builder
	for _, m := range res.Messages {
		if m == nil || m.Content == nil {
			continue
		}
		text, ok := m.Content.(*mcp.TextContent)
		if !ok {
			if b.Len() > 0 {
				b.WriteString("\n\n")
			}
			fmt.Fprintf(&b, "[%s 非文本内容]", m.Role)
			continue
		}
		if b.Len() > 0 {
			b.WriteString("\n\n")
		}
		b.WriteString(text.Text)
	}
	if b.Len() == 0 {
		return "", fmt.Errorf("prompt %s 未产出文本内容（插件 %s）", name, s.name)
	}
	return b.String(), nil
}

// mapArgs 命令行参数 → prompts/get 的命名参数；返回 (arguments, 用法行, error)。
func (s *Server) mapArgs(name string, args []string) (map[string]string, string, error) {
	p := s.findPrompt(name)
	arguments := map[string]string{}
	if p == nil || len(p.Arguments) == 0 {
		// 未拿到声明（server 未实现 prompts/list 或该 prompt 无参）：
		// 单参作 input，多参空格拼接。
		if len(args) > 0 {
			arguments["input"] = strings.Join(args, " ")
		}
		return arguments, "", nil
	}
	usage := usageLine(p)
	if len(args) == 1 {
		arguments[p.Arguments[0].Name] = args[0]
	} else {
		for i, a := range args {
			if i >= len(p.Arguments) {
				break
			}
			arguments[p.Arguments[i].Name] = a
		}
	}
	for _, pa := range p.Arguments {
		if pa != nil && pa.Required && arguments[pa.Name] == "" {
			return nil, usage, fmt.Errorf("缺少参数 %s。用法: /mcp:%s:%s %s", pa.Name, s.name, name, usage)
		}
	}
	return arguments, usage, nil
}

// findPrompt 按名查缓存声明（无则 nil，交给 GetPrompt 自然报错）。
func (s *Server) findPrompt(name string) *mcp.Prompt {
	for _, p := range s.prompts {
		if p != nil && p.Name == name {
			return p
		}
	}
	return nil
}

// usageLine 参数用法串：必填 <name>、可选 [name]。
func usageLine(p *mcp.Prompt) string {
	parts := make([]string, 0, len(p.Arguments))
	for _, pa := range p.Arguments {
		if pa == nil {
			continue
		}
		if pa.Required {
			parts = append(parts, "<"+pa.Name+">")
		} else {
			parts = append(parts, "["+pa.Name+"]")
		}
	}
	return strings.Join(parts, " ")
}
