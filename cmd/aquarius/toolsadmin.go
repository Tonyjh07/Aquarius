package main

// 工具与 MCP 管理面装配根侧（S6/D143）：实现 uigui.ToolsAdmin（消费方定义接口）——
// 内置工具启停（热生效，config tools.disabled）+ 插件启停/授权/信任/日志。动作由
// 设置窗**异步**调用（Host.Enable 拨号上限 15s、信任/能力确认弹窗在主窗——次窗同步
// 调必冻窗），本文件方法只做「改内存 → 落盘 → 递增 Revision」。host/runner 由
// wiring 后绑定（newUI 早于宿主构造；方法仅在设置窗打开后调用，彼时已就绪）。

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"

	"github.com/Tonyjh07/Aquarius/internal/adapter/decorate"
	"github.com/Tonyjh07/Aquarius/internal/adapter/toolbuiltin"
	"github.com/Tonyjh07/Aquarius/internal/adapter/uigui"
)

// pluginLogLines 日志尾行上限（/plugin logs 与设置窗同源）。
const pluginLogLines = 200

// contextCompactName context_compact 工具名（app.ContextCompactTool 装配期补注册；
// 工具页清单与禁用集在此补上）。
const contextCompactName = "context_compact"

// disabledHolder 内置工具禁用集的运行态持有（S6/D143）：启动时从 config 推导，
// 设置窗开关热更新（runner.SetDisabled 即时生效）；config 只在动开关时写键
// （推导态不写盘——「首次动开关才落键」）。
type disabledHolder struct {
	mu  sync.Mutex
	set map[string]bool
}

func newDisabledHolder(set map[string]bool) *disabledHolder {
	return &disabledHolder{set: set}
}

// snapshot 当前禁用集拷贝。
func (d *disabledHolder) snapshot() map[string]bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := make(map[string]bool, len(d.set))
	for name := range d.set {
		out[name] = true
	}
	return out
}

// setEnabled 设单个工具的可见性（on = 从禁用集移除）。
func (d *disabledHolder) setEnabled(name string, on bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if on {
		delete(d.set, name)
	} else {
		d.set[name] = true
	}
}

// sortedDisabled 禁用名排序（写盘确定性）。
func sortedDisabled(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for name := range m {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// builtinDisabled 启动期内置工具禁用集推导（S6/D143）：config tools.disabled 键缺失
// → 默认推导（兼容 model.think_tool=true 则空集，否则 ["think"]——D34 现行为）；
// 键存在 = 显式清单。仅内存推导，首次动开关才落键。
func (w *wiring) builtinDisabled() map[string]bool {
	if w.cfg.Tools.Disabled == nil {
		if w.cfg.Model.ThinkTool { // 兼容读取：老配置显式开了草稿工具 → 维持可见
			return nil
		}
		return map[string]bool{"think": true}
	}
	m := make(map[string]bool, len(w.cfg.Tools.Disabled))
	for _, n := range w.cfg.Tools.Disabled {
		m[n] = true
	}
	return m
}

// builtinToolNames 内置工具名 + context_compact（工具页清单与未知名校验共用）。
func builtinToolNames() []string {
	return append(toolbuiltin.Names(), contextCompactName)
}

// builtinToolLabel 内置工具中文词表（context_compact 由装配根补词）。
func builtinToolLabel(name string) string {
	if name == contextCompactName {
		return "自动上下文压缩"
	}
	return toolbuiltin.DisplayName(name)
}

// toolsAdmin uigui.ToolsAdmin 的装配根实现。
type toolsAdmin struct {
	w *wiring
}

var _ uigui.ToolsAdmin = (*toolsAdmin)(nil)

// Snapshot 原子快照：内置清单（词表 + 禁用态）+ 插件清单（host.List 投影）。
func (a *toolsAdmin) Snapshot() uigui.ToolsSnapshot {
	s := uigui.ToolsSnapshot{Revision: a.w.toolsRev.Load()}
	disabled := a.w.disabledTools.snapshot()
	for _, name := range builtinToolNames() {
		s.Builtins = append(s.Builtins, uigui.BuiltinToolState{
			Name: name, Label: builtinToolLabel(name), Enabled: !disabled[name],
		})
	}
	for _, p := range a.w.host.List() {
		s.Plugins = append(s.Plugins, uigui.PluginToolState{
			Name: p.Name, Source: p.Source, Transport: p.Transport,
			Status: string(p.Status), Enabled: p.Enabled, Risk: p.Risk,
			Capabilities: p.Capabilities, Granted: p.Granted, Trusted: p.Trusted,
			Restarts: p.Restarts, LastErr: p.LastErr,
			Calls: p.Stats.Calls, Errors: p.Stats.Errors, LastMS: p.Stats.LastMS,
		})
	}
	return s
}

// SetBuiltinEnabled 内置工具启停：热更新 runner 禁用集 + 写回 config tools.disabled。
func (a *toolsAdmin) SetBuiltinEnabled(name string, enabled bool) error {
	if !containsStr(builtinToolNames(), name) {
		return fmt.Errorf("未知的内置工具 %q", name)
	}
	a.w.disabledTools.setEnabled(name, enabled)
	a.w.runner.SetDisabled(a.w.disabledTools.snapshot())
	a.w.toolsRev.Add(1)
	perr := a.w.persist.persistToolsDisabled(sortedDisabled(a.w.disabledTools.snapshot()))
	if perr != nil {
		return fmt.Errorf("已热生效，但写回 config tools.disabled 失败: %w", perr)
	}
	return nil
}

// SetPluginEnabled 插件启停（Enable 含信任门与能力授权流——弹主窗确认）。
func (a *toolsAdmin) SetPluginEnabled(name string, enabled bool) error {
	if enabled {
		return a.w.host.Enable(context.Background(), name)
	}
	return a.w.host.Disable(name)
}

// GrantCap 手动预授权（免一次确认写入 granted）。
func (a *toolsAdmin) GrantCap(name string, cap string) error {
	return a.w.host.GrantCap(name, cap)
}

// RevokeGrant 撤销能力（同时停用插件 + 清 granted；再启用重走确认）。
func (a *toolsAdmin) RevokeGrant(name string, cap string) error {
	return a.w.host.RevokeGrant(name, cap)
}

// RevokeTrust 撤销插件目录来源插件的启动信任（清指纹 + 断开）。
func (a *toolsAdmin) RevokeTrust(name string) error {
	return a.w.host.RevokeTrust(name)
}

// Logs 插件调用日志：渲染后写临时文件并打开系统编辑器（复用 /memory 的 pickEditor）；
// name 空 = 各插件最近调用摘要。返回状态文案。
func (a *toolsAdmin) Logs(name string) (string, error) {
	text, err := tailPluginLogs(a.w.profileDir, name, pluginLogLines)
	if err != nil {
		return "", err
	}
	f, err := os.CreateTemp("", "aquarius-plugin-logs-*.txt")
	if err != nil {
		return "", fmt.Errorf("创建临时日志文件: %w", err)
	}
	path := f.Name()
	if _, err := f.WriteString(text); err != nil {
		f.Close()
		_ = os.Remove(path)
		return "", fmt.Errorf("写入临时日志文件: %w", err)
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(path)
		return "", fmt.Errorf("关闭临时日志文件: %w", err)
	}
	defer os.Remove(path) // 日志是快照：编辑完即清（想要原文走 /plugin logs 终端输出）
	argv := pickEditor(os.Getenv("VISUAL"), os.Getenv("EDITOR"), runtime.GOOS)
	cmd := exec.Command(argv[0], append(argv[1:], path)...)
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("运行编辑器 %s: %w", argv[0], err)
	}
	if name == "" {
		return "已打开插件日志摘要（系统编辑器）", nil
	}
	return fmt.Sprintf("已打开插件日志：%s（系统编辑器）", name), nil
}

// containsStr 顺序查找（小清单）。
func containsStr(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// tailPluginLogs 审计日志尾部插件调用行（S6/D143）：name 空 = 各插件最近调用摘要；
// 带名 = 该插件最近调用行（行数上限 last）。
func tailPluginLogs(profileDir, name string, last int) (string, error) {
	rows, err := decorate.TailToolCalls(filepath.Join(profileDir, "audit.log"), name, last)
	if err != nil {
		return "", err
	}
	return formatPluginLogs(rows, name, last), nil
}

// mcpServerOf 工具名 "mcp:<server>:<tool>" → server。
func mcpServerOf(toolName string) (string, bool) {
	if !strings.HasPrefix(toolName, "mcp:") {
		return "", false
	}
	rest := strings.TrimPrefix(toolName, "mcp:")
	i := strings.IndexByte(rest, ':')
	if i < 0 {
		return "", false
	}
	return rest[:i], true
}

// formatPluginLogs 格式化：带名 = 逐行（新→旧）；无参 = 各插件摘要。
func formatPluginLogs(rows []decorate.ToolCallLine, name string, last int) string {
	if len(rows) == 0 {
		return "（尚无插件调用记录）"
	}
	if name != "" {
		var b strings.Builder
		fmt.Fprintf(&b, "插件 %s 最近调用（最多 %d 条，新→旧）:\n", name, last)
		for i := len(rows) - 1; i >= 0; i-- {
			r := rows[i]
			mark := "ok"
			if !r.OK {
				mark = "fail"
				if r.Err != "" {
					mark += " " + r.Err
				}
			}
			fmt.Fprintf(&b, "  %s %s %dms %s\n", r.TS, r.Name, r.DurMS, mark)
		}
		return strings.TrimRight(b.String(), "\n")
	}
	type agg struct {
		calls, errs int
		last        string
	}
	by := map[string]*agg{}
	var order []string
	for _, r := range rows {
		server, ok := mcpServerOf(r.Name)
		if !ok {
			continue
		}
		ag := by[server]
		if ag == nil {
			ag = &agg{}
			by[server] = ag
			order = append(order, server)
		}
		ag.calls++
		if !r.OK {
			ag.errs++
		}
		ag.last = r.TS
	}
	sort.Strings(order)
	if len(order) == 0 {
		return "（尚无插件调用记录）"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "插件调用摘要（最近 %d 条内）:\n", last)
	for _, server := range order {
		ag := by[server]
		line := fmt.Sprintf("  %-16s 调用%d", server, ag.calls)
		if ag.errs > 0 {
			line += fmt.Sprintf(", 失败%d", ag.errs)
		}
		line += " · 最近 " + ag.last
		b.WriteString(line)
		b.WriteByte('\n')
	}
	b.WriteString("详细：/plugin logs <name> 或设置窗「打开日志」")
	return b.String()
}
