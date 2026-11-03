package mcpgate

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/Tonyjh07/Aquarius/internal/i18n"
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
		return "", fmt.Errorf("prompts/get %s (plugin %s): %w", name, s.name, err)
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
			fmt.Fprintf(&b, "[%s non-text content]", m.Role)
			continue
		}
		if b.Len() > 0 {
			b.WriteString("\n\n")
		}
		b.WriteString(text.Text)
	}
	if b.Len() == 0 {
		return "", errors.New(i18n.Tf("mcp.err.prompt_empty", "name", name, "plugin", s.name))
	}
	return b.String(), nil
}

// mapArgs 命令行参数 → prompts/get 的命名参数；返回 (arguments, 用法行, error)。
// 审查修复的三处边界（prompts/list 是不可信输入）：
//   - 声明数组含 nil 不得解引用（否则 REPL 直接 panic）；
//   - 单声明参数遇多词时整体拼接，不静默丢词；
//   - 超出声明数的参数报用法错误，不静默截断；未声明参数的 prompt 拒收参数。
//
// server 未实现 prompts/list（findPrompt 为 nil）时无法校验，参数整体拼入
// input 交服务端裁决。
func (s *Server) mapArgs(name string, args []string) (map[string]string, string, error) {
	p := s.findPrompt(name)
	if p == nil {
		arguments := map[string]string{}
		if len(args) > 0 {
			arguments["input"] = strings.Join(args, " ")
		}
		return arguments, "", nil
	}
	valid := make([]*mcp.PromptArgument, 0, len(p.Arguments))
	for _, pa := range p.Arguments {
		if pa != nil {
			valid = append(valid, pa)
		}
	}
	usage := usageLine(p)
	if len(valid) == 0 {
		if len(args) > 0 {
			return nil, usage, errors.New(i18n.Tf("mcp.err.prompt_no_args",
				"name", name, "got", len(args), "plugin", s.name, "prompt", name, "usage", usage))
		}
		return map[string]string{}, "", nil
	}

	arguments := map[string]string{}
	switch {
	case len(valid) == 1:
		arguments[valid[0].Name] = strings.Join(args, " ") // 单参数：多词整体送入，不丢词
	case len(args) > len(valid):
		return nil, usage, errors.New(i18n.Tf("mcp.err.prompt_too_many",
			"name", name, "want", len(valid), "got", len(args), "plugin", s.name, "prompt", name, "usage", usage))
	default:
		for i, a := range args {
			arguments[valid[i].Name] = a
		}
	}
	for _, pa := range valid {
		if pa.Required && arguments[pa.Name] == "" {
			return nil, usage, errors.New(i18n.Tf("mcp.err.prompt_missing_arg",
				"arg", pa.Name, "plugin", s.name, "prompt", name, "usage", usage))
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
