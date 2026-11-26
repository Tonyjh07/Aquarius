package app

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/Tonyjh07/Aquarius/internal/i18n"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

// PluginInfo /plugin list 的一行（§7.3）：宿主快照的 app 侧投影——
// app 只定义消费面，不 import 插件宿主内部包（同 D13 边界）。
type PluginInfo struct {
	Name      string
	Source    string // 「config」 | 「plugins」（声明来源，D31）| 「binary」（编译进二进制）
	Transport string // "stdio" | "streamable-http" | "in-process"
	// Provides 供给面（Tier-1 才有：memory / tool / llm …；Tier-2 为空）。
	Provides     []string
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

// PluginViewSpec 一个可打开的面板（/plugin view 用；app 只认这三个字段）。
type PluginViewSpec struct {
	Plugin string
	ID     string
	Title  string
}

// PluginAdmin 插件管理面（§7.3 /plugin；装配根把宿主适配到此接口，M4）。
type PluginAdmin interface {
	// Plugins 全部插件快照（含解析失败的条目）。
	Plugins() []PluginInfo
	// Views 某个插件声明的面板（只含有面板面的插件；停用的插件返回空）。
	Views(name string) ([]PluginViewSpec, error)
	// Enable 启用并连接（含 capability 首用授权流）；未声明的插件报错。
	Enable(ctx context.Context, name string) error
	// Disable 停用并断开；未声明的插件报错。
	Disable(name string) error
	// Logs 插件调用日志（S6/D143）：tail 审计日志的 kind=tool + mcp:<name>: 行；
	// name 空 = 各插件最近调用摘要（行数上限由实现方控制）。
	Logs(name string) (string, error)
}

// execPlugin /plugin [list|enable <name>|disable <name>]（§7.3 / D31）。
func (s *Session) execPlugin(ctx context.Context, args []string) (string, error) {
	if s.plugins == nil {
		return "", errors.New("session: " + i18n.T("plugin.unwired"))
	}
	sub := "list"
	if len(args) > 0 {
		sub = args[0]
	}
	switch sub {
	case "list", "":
		if len(args) > 1 {
			return "", errors.New(i18n.T("plugin.usage.list"))
		}
		out := formatPlugins(s.plugins.Plugins())
		if dyn := s.dynamicNames(); len(dyn) > 0 {
			out += i18n.T("plugin.dynamic_head") + strings.Join(dyn, "\n  /")
		}
		return out, nil
	case "enable":
		if len(args) != 2 {
			return "", errors.New(i18n.T("plugin.usage.enable"))
		}
		if err := s.plugins.Enable(ctx, args[1]); err != nil {
			return "", err
		}
		return enableResult(s.plugins.Plugins(), args[1]), nil
	case "disable":
		if len(args) != 2 {
			return "", errors.New(i18n.T("plugin.usage.disable"))
		}
		if err := s.plugins.Disable(args[1]); err != nil {
			return "", err
		}
		return i18n.Tf("plugin.disabled", "name", args[1]), nil
	// S6/D143：tail audit.log 的插件调用行（无参 = 各插件最近调用摘要）
	case "logs":
		if len(args) > 2 {
			return "", errors.New(i18n.T("plugin.usage.logs"))
		}
		name := ""
		if len(args) == 2 {
			name = args[1]
		}
		out, err := s.plugins.Logs(name)
		if err != nil {
			return "", err
		}
		return out, nil
	case "view":
		return s.openPluginView(ctx, args[1:])
	default:
		return "", errors.New(i18n.T("plugin.usage"))
	}
}

// openPluginView /plugin view <插件> [<面板ID>]：把「打开哪个面板」交给前端。
//
// 命令这一层只做选择与报因，不渲染：组件树归前端（GUI 开次窗、TUI 渲染成文本），事件里
// 只有目标。省略面板 ID 而插件只有一个面板时直接打开，多个则列出来让人挑——猜一个打开是
// 最坏的选择，用户看到的会是自己没要的那块。
func (s *Session) openPluginView(ctx context.Context, args []string) (string, error) {
	if len(args) < 1 || len(args) > 2 {
		return "", errors.New(i18n.T("plugin.usage.view"))
	}
	name := args[0]
	views, err := s.plugins.Views(name)
	if err != nil {
		return "", err
	}
	if len(views) == 0 {
		return "", errors.New(i18n.Tf("plugin.view.empty", "name", name))
	}
	var pick PluginViewSpec
	switch {
	case len(args) == 2:
		found := false
		for _, v := range views {
			if v.ID == args[1] {
				pick, found = v, true
				break
			}
		}
		if !found {
			return "", errors.New(i18n.Tf("plugin.view.empty", "name", name))
		}
	case len(views) == 1:
		pick = views[0]
	default:
		var b strings.Builder
		b.WriteString(i18n.Tf("plugin.view.ambiguous", "name", name))
		for _, v := range views {
			b.WriteString("\n  " + v.ID + "  " + v.Title)
		}
		return b.String(), nil
	}
	if s.ui == nil {
		return "", errors.New("session: " + i18n.T("agent.err_no_presenter"))
	}
	if err := s.ui.Emit(ctx, port.OpenViewEvent{Plugin: pick.Plugin, ViewID: pick.ID}); err != nil {
		return "", err
	}
	return i18n.Tf("plugin.view.opened", "title", pick.Title), nil
}

// enableResult 启用结果行（含授权被拒等非 ready 终态的报因）。
func enableResult(list []PluginInfo, name string) string {
	for _, p := range list {
		if p.Name != name {
			continue
		}
		line := i18n.Tf("plugin.enabled_status", "name", name, "status", p.Status)
		if p.LastErr != "" {
			line += i18n.Tf("plugin.enabled_note", "error", p.LastErr)
		}
		return line
	}
	return i18n.Tf("plugin.enabled", "name", name)
}

// formatPlugins /plugin list 表格：状态/来源/传输/能力授权/调用统计/重启与报因。
func formatPlugins(list []PluginInfo) string {
	if len(list) == 0 {
		return i18n.T("plugin.list.empty")
	}
	var b strings.Builder
	b.WriteString(i18n.Tf("plugin.list.head", "n", strconv.Itoa(len(list))))
	b.WriteByte('\n')
	for _, p := range list {
		fmt.Fprintf(&b, "  [%s] %-16s %s/%s", p.Status, p.Name, orDash(p.Source), orDash(p.Transport))
		if caps := capLabels(p); caps != "" {
			b.WriteString(i18n.Tf("plugin.list.caps", "caps", caps))
		}
		if len(p.Provides) > 0 {
			b.WriteString(i18n.Tf("plugin.list.provides", "list", strings.Join(p.Provides, ",")))
		}
		if p.Calls > 0 {
			b.WriteString(i18n.Tf("plugin.list.calls", "calls", strconv.Itoa(p.Calls)))
			if p.Errors > 0 {
				b.WriteString(i18n.Tf("plugin.list.errors", "errors", strconv.Itoa(p.Errors)))
			}
		}
		if p.Restarts > 0 {
			b.WriteString(i18n.Tf("plugin.list.restarts", "restarts", strconv.Itoa(p.Restarts)))
		}
		if p.LastErr != "" {
			b.WriteString(i18n.Tf("plugin.list.last_error", "error", p.LastErr))
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
		mark := i18n.T("plugin.cap.ungranted")
		for _, g := range p.Granted {
			if g == c {
				mark = i18n.T("plugin.cap.granted")
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
