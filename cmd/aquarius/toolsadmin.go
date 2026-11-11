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
	"strconv"
	"strings"
	"sync"

	"github.com/Tonyjh07/Aquarius/internal/adapter/decorate"
	"github.com/Tonyjh07/Aquarius/internal/adapter/toolbuiltin"
	"github.com/Tonyjh07/Aquarius/internal/adapter/uigui"
	"github.com/Tonyjh07/Aquarius/internal/domain/perm"
	"github.com/Tonyjh07/Aquarius/internal/i18n"
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

// builtinToolLabel 内置工具名称（context_compact 由装配根补词）。
func builtinToolLabel(name string) string {
	if name == contextCompactName {
		return i18n.T("cmd.tools.label.context_compact")
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
	if a.w.lvl != nil {
		s.Level = a.w.lvl.Get().String()
	}
	// 例外表按来源摊开：本地工具落到各自行上，外部代理是整来源的一条；带目录的
	// （D282「以后都允许」）不参与工具行——那是另一条更窄的规则，单独列。
	perTool := map[string]string{}
	for _, ov := range a.w.policy.Get() {
		if ov.Dir != "" {
			s.DirRules = append(s.DirRules, uigui.DirRule{
				Tool: ov.Tool, Label: builtinToolLabel(ov.Tool), Dir: ov.Dir,
			})
			continue
		}
		switch ov.Origin {
		case perm.OriginTool:
			perTool[ov.Tool] = string(ov.Mode)
		case perm.OriginAgent:
			s.AgentPolicy = string(ov.Mode)
		}
	}
	// 装配早期或只装一部分的调用方（测试）可能还没接上这些面：缺的按空处理。
	disabled := map[string]bool{}
	if a.w.disabledTools != nil {
		disabled = a.w.disabledTools.snapshot()
	}
	for _, name := range builtinToolNames() {
		s.Builtins = append(s.Builtins, uigui.BuiltinToolState{
			Name: name, Label: builtinToolLabel(name), Enabled: !disabled[name],
			Policy: perTool[name], Exec: perm.IsExec(name),
		})
	}
	if a.w.host == nil {
		return s
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
		return fmt.Errorf("%s", i18n.Tf("cmd.tools.err.unknown_builtin", "name", strconv.Quote(name)))
	}
	a.w.disabledTools.setEnabled(name, enabled)
	a.w.runner.SetDisabled(a.w.disabledTools.snapshot())
	a.w.toolsRev.Add(1)
	perr := a.w.persist.persistToolsDisabled(sortedDisabled(a.w.disabledTools.snapshot()))
	if perr != nil {
		return fmt.Errorf("%s: %w", i18n.T("cmd.tools.err.persist_disabled"), perr)
	}
	return nil
}

// SetToolPolicy 单个内置工具的放行档位（D280/D282；"" = 回默认 / 跟随权限档）。
// 写回 config permissions.overrides，runner 与 acpgate 拿的都是 policyHolder，立即生效。
func (a *toolsAdmin) SetToolPolicy(name, mode string) error {
	if !containsStr(builtinToolNames(), name) {
		return fmt.Errorf("%s", i18n.Tf("cmd.tools.err.unknown_builtin", "name", strconv.Quote(name)))
	}
	return a.setPolicy(string(perm.OriginTool), name, "", mode)
}

// SetAgentPolicy 外部代理整来源的放行档位（"" = 回默认）。
func (a *toolsAdmin) SetAgentPolicy(mode string) error {
	return a.setPolicy(string(perm.OriginAgent), "", "", mode)
}

// RevokeDirPolicy 撤销一条目录级放宽（D282）：这条规则由确认框写入，界面上只有
// 工具页那一行能撤——mode 空即删除。
func (a *toolsAdmin) RevokeDirPolicy(tool, dir string) error {
	return a.setPolicy(string(perm.OriginTool), tool, dir, "")
}

// setPolicy 落盘 → 换表 → 递增 Revision（界面据此重建行）。
func (a *toolsAdmin) setPolicy(origin, tool, dir, mode string) error {
	if err := a.w.setOverride(origin, tool, dir, mode); err != nil {
		return err
	}
	a.w.toolsRev.Add(1)
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
		return "", fmt.Errorf("%s: %w", i18n.T("cmd.tools.err.temp_log_create"), err)
	}
	path := f.Name()
	if _, err := f.WriteString(text); err != nil {
		f.Close()
		_ = os.Remove(path)
		return "", fmt.Errorf("%s: %w", i18n.T("cmd.tools.err.temp_log_write"), err)
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(path)
		return "", fmt.Errorf("%s: %w", i18n.T("cmd.tools.err.temp_log_close"), err)
	}
	defer os.Remove(path) // 日志是快照：编辑完即清（想要原文走 /plugin logs 终端输出）
	argv := pickEditor(os.Getenv("VISUAL"), os.Getenv("EDITOR"), runtime.GOOS)
	cmd := exec.Command(argv[0], append(argv[1:], path)...)
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("%s: %w", i18n.Tf("cmd.tools.err.run_editor", "editor", argv[0]), err)
	}
	if name == "" {
		return i18n.T("cmd.tools.logs.opened_summary"), nil
	}
	return i18n.Tf("cmd.tools.logs.opened_named", "name", name), nil
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
	empty := i18n.T("cmd.tools.logs.empty")
	if len(rows) == 0 {
		return empty
	}
	if name != "" {
		var b strings.Builder
		b.WriteString(i18n.Tf("cmd.tools.logs.detail_head", "name", name, "last", last) + "\n")
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
		return empty
	}
	var b strings.Builder
	b.WriteString(i18n.Tf("cmd.tools.logs.summary_head", "last", last) + "\n")
	for _, server := range order {
		ag := by[server]
		line := i18n.Tf("cmd.tools.logs.summary_calls",
			"server", fmt.Sprintf("%-16s", server), "calls", ag.calls)
		if ag.errs > 0 {
			line += i18n.Tf("cmd.tools.logs.summary_errors", "errs", ag.errs)
		}
		line += i18n.Tf("cmd.tools.logs.summary_last", "ts", ag.last)
		b.WriteString(line)
		b.WriteByte('\n')
	}
	b.WriteString(i18n.T("cmd.tools.logs.summary_hint"))
	return b.String()
}
