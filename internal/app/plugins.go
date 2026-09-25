package app

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
)

// PluginInfo /plugin list 的一行（DESIGN §7.3）：宿主快照的 app 侧投影——
// app 只定义消费面，不 import 插件宿主内部包（同 D13 边界）。
type PluginInfo struct {
	Name         string
	Source       string // "config" | "plugins"（声明来源，D31）
	Transport    string // "stdio" | "streamable-http"
	Capabilities []string
	Granted      []string // 已授权能力（config plugins.<name>.granted）
	Risk         string
	Enabled      bool
	Status       string // plugin.Status 的字符串形式
	Restarts     int    // 本次运行的崩溃重启次数
	LastErr      string
	Calls        int // 工具调用统计（§6.4 #5）
	Errors       int
	LastMS       int64
}

// PluginAdmin 插件管理面（§7.3 /plugin；装配根把宿主适配到此接口，M4）。
type PluginAdmin interface {
	// Plugins 全部插件快照（含解析失败的条目）。
	Plugins() []PluginInfo
	// Enable 启用并连接（含 capability 首用授权流）；未声明的插件报错。
	Enable(ctx context.Context, name string) error
	// Disable 停用并断开；未声明的插件报错。
	Disable(name string) error
}

// execPlugin /plugin [list|enable <name>|disable <name>]（§7.3 / D31）。
func (s *Session) execPlugin(ctx context.Context, args []string) (string, error) {
	if s.plugins == nil {
		return "", errors.New("session: 未配置插件宿主（SessionDeps.Plugins），/plugin 不可用")
	}
	sub := "list"
	if len(args) > 0 {
		sub = args[0]
	}
	switch sub {
	case "list", "":
		if len(args) > 1 {
			return "", errors.New("用法: /plugin list")
		}
		out := formatPlugins(s.plugins.Plugins())
		if dyn := s.dynamicNames(); len(dyn) > 0 {
			out += "\n动态命令（MCP prompts）:\n  /" + strings.Join(dyn, "\n  /")
		}
		return out, nil
	case "enable":
		if len(args) != 2 {
			return "", errors.New("用法: /plugin enable <name>")
		}
		if err := s.plugins.Enable(ctx, args[1]); err != nil {
			return "", err
		}
		return enableResult(s.plugins.Plugins(), args[1]), nil
	case "disable":
		if len(args) != 2 {
			return "", errors.New("用法: /plugin disable <name>")
		}
		if err := s.plugins.Disable(args[1]); err != nil {
			return "", err
		}
		return fmt.Sprintf("已停用 %s", args[1]), nil
	default:
		return "", errors.New("用法: /plugin [list|enable <name>|disable <name>]")
	}
}

// enableResult 启用结果行（含授权被拒等非 ready 终态的报因）。
func enableResult(list []PluginInfo, name string) string {
	for _, p := range list {
		if p.Name != name {
			continue
		}
		line := fmt.Sprintf("已启用 %s → %s", name, p.Status)
		if p.LastErr != "" {
			line += "（" + p.LastErr + "）"
		}
		return line
	}
	return fmt.Sprintf("已启用 %s", name)
}

// formatPlugins /plugin list 表格：状态/来源/传输/能力授权/调用统计/重启与报因。
func formatPlugins(list []PluginInfo) string {
	if len(list) == 0 {
		return "没有已声明的插件（config mcpServers 或 ~/.aquarius/plugins/*/plugin.json）"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "插件（%d 个）:\n", len(list))
	for _, p := range list {
		fmt.Fprintf(&b, "  [%s] %-16s %s/%s", p.Status, p.Name, orDash(p.Source), orDash(p.Transport))
		if caps := capLabels(p); caps != "" {
			fmt.Fprintf(&b, " 能力:%s", caps)
		}
		if p.Calls > 0 {
			fmt.Fprintf(&b, " 调用%d", p.Calls)
			if p.Errors > 0 {
				fmt.Fprintf(&b, ", 失败%d", p.Errors)
			}
		}
		if p.Restarts > 0 {
			fmt.Fprintf(&b, " 重启%d", p.Restarts)
		}
		if p.LastErr != "" {
			fmt.Fprintf(&b, " 错误:%s", p.LastErr)
		}
		b.WriteByte('\n')
	}
	return strings.TrimRight(b.String(), "\n")
}

// dynamicNames 动态命令名（mcp: 前缀，排序加斜杠；/plugin list 展示发现面）。
func (s *Session) dynamicNames() []string {
	s.dynMu.Lock()
	defer s.dynMu.Unlock()
	out := make([]string, 0, len(s.dynamic))
	for name := range s.dynamic {
		if strings.HasPrefix(name, "mcp:") {
			out = append(out, "/"+name)
		}
	}
	sort.Strings(out)
	return out
}

// capLabels 能力标注：已授权 `(已授权)`、未授权 `(未授权)`；无能力返回空串。
func capLabels(p PluginInfo) string {
	if len(p.Capabilities) == 0 {
		return ""
	}
	parts := make([]string, len(p.Capabilities))
	for i, c := range p.Capabilities {
		mark := "(未授权)"
		for _, g := range p.Granted {
			if g == c {
				mark = "(已授权)"
				break
			}
		}
		parts[i] = c + mark
	}
	return strings.Join(parts, ",")
}

// orDash 空值显示为 "-"。
func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
