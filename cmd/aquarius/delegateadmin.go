package main

// 设置窗「外部代理」页的管理面（D270）：实现 uigui.DelegateAdmin。
//
// 口径三条：
//  1. **页面显示的是配置 + 现算的 PATH 探测**，不是运行中的转交管理器。转交管理器按启动
//     时的配置装配（命令、参数、env、上限），热改它等于在别人的进程还在跑时换掉它的身份；
//     所以这里改完一律「重启生效」，页面也照着新配置显示——而不是显示一份跑着但已过期的事实。
//  2. 写回走 persistConfig 的泛键改写（与技能目录同一路），只动 `delegate.agents` 里那一条。
//  3. 校验只做「写下去会让下次启动报错」的那两条：缺省代理不能停用、停用的代理不能设为缺省
//     ——它们是 config 解析期的硬约束，放进界面拦比让用户下次启动吃一个 fatal 好。

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"

	"github.com/Tonyjh07/Aquarius/internal/adapter/uigui"
	"github.com/Tonyjh07/Aquarius/internal/i18n"
)

// delegateAdmin uigui.DelegateAdmin 的装配根实现。
type delegateAdmin struct{ w *wiring }

var _ uigui.DelegateAdmin = (*delegateAdmin)(nil)

// delegateAvailable 命令能不能执行（绝对路径也认：LookPath 会直接检查它）。
func delegateAvailable(command string) bool {
	if strings.TrimSpace(command) == "" {
		return false
	}
	_, err := exec.LookPath(command)
	return err == nil
}

// Snapshot 清单：配置事实 + 现探可用性 + 上次握手学到的标题与版本。
func (a *delegateAdmin) Snapshot() uigui.DelegateSnapshot {
	var snap uigui.DelegateSnapshot
	learned := map[string][2]string{}
	if a.w.delegateMgr != nil {
		if list, err := a.w.delegateMgr.List(context.Background()); err == nil {
			for _, li := range list {
				learned[li.Name] = [2]string{li.Title, li.Version}
			}
		}
	}
	for _, ag := range a.w.cfg.Delegate.Agents {
		enabled := ag.Enabled == nil || *ag.Enabled
		st := uigui.DelegateAgentState{
			Name:        ag.Name,
			CommandLine: commandLineOf(ag.Command, ag.Args),
			Enabled:     enabled,
			Default:     ag.Default,
		}
		if tv, ok := learned[ag.Name]; ok {
			st.Title, st.Version = tv[0], tv[1]
		}
		switch {
		case !enabled:
			st.Note = i18n.T("cmd.delegate.note.disabled")
		case strings.TrimSpace(ag.Command) == "":
			st.Note = i18n.T("cmd.delegate.note.no_command")
		case !delegateAvailable(ag.Command):
			st.Note = i18n.Tf("cmd.delegate.note.not_on_path", "command", ag.Command)
		default:
			st.Available = true
		}
		snap.Agents = append(snap.Agents, st)
	}
	if len(snap.Agents) == 0 {
		snap.Note = i18n.T("cmd.delegate.note.empty")
	}
	return snap
}

// SetCommandLine 改命令行：`command args...`，按空白切分，双引号包住含空白的参数。
func (a *delegateAdmin) SetCommandLine(name, commandLine string) error {
	command, args, err := splitCommandLine(commandLine)
	if err != nil {
		return err
	}
	if command == "" {
		return errors.New(i18n.T("cmd.delegate.err.command_required"))
	}
	idx := a.agentIndex(name)
	if idx < 0 {
		return errors.New(i18n.Tf("cmd.delegate.err.agent_missing_stale", "name", strconv.Quote(name)))
	}
	if err := a.patchAgent(name, func(m map[string]any) {
		m["command"] = command
		if len(args) == 0 {
			delete(m, "args")
		} else {
			list := make([]any, 0, len(args))
			for _, s := range args {
				list = append(list, s)
			}
			m["args"] = list
		}
	}); err != nil {
		return err
	}
	a.w.cfg.Delegate.Agents[idx].Command = command
	a.w.cfg.Delegate.Agents[idx].Args = args
	return nil
}

// SetEnabled 启停一个代理。
func (a *delegateAdmin) SetEnabled(name string, enabled bool) error {
	idx := a.agentIndex(name)
	if idx < 0 {
		return errors.New(i18n.Tf("cmd.delegate.err.agent_missing", "name", strconv.Quote(name)))
	}
	cur := a.w.cfg.Delegate.Agents[idx]
	if !enabled && cur.Default {
		return errors.New(i18n.T("cmd.delegate.err.default_disable"))
	}
	if err := a.patchAgent(name, func(m map[string]any) {
		if enabled {
			// 键缺失 = 启用：写 true 反而是噪音，但写成显式 true 比删键更容易读。
			m["enabled"] = true
			return
		}
		m["enabled"] = false
	}); err != nil {
		return err
	}
	on := enabled
	a.w.cfg.Delegate.Agents[idx].Enabled = &on
	return nil
}

// SetDefault 指定缺省代理（同一条上唯一为 true；其余显式清掉）。
func (a *delegateAdmin) SetDefault(name string) error {
	idx := a.agentIndex(name)
	if idx < 0 {
		return errors.New(i18n.Tf("cmd.delegate.err.agent_missing", "name", strconv.Quote(name)))
	}
	cur := a.w.cfg.Delegate.Agents[idx]
	if cur.Enabled != nil && !*cur.Enabled {
		return errors.New(i18n.T("cmd.delegate.err.disabled_default"))
	}
	if err := persistConfig(a.w.cfgPath, func(generic map[string]any) {
		sec, _ := generic["delegate"].(map[string]any)
		if sec == nil {
			return
		}
		list, _ := sec["agents"].([]any)
		for _, it := range list {
			m, _ := it.(map[string]any)
			if m == nil {
				continue
			}
			n, _ := m["name"].(string)
			if n == name {
				m["default"] = true
				continue
			}
			delete(m, "default")
		}
	}); err != nil {
		return fmt.Errorf("%s: %w", i18n.T("cmd.delegate.err.persist_config"), err)
	}
	for i := range a.w.cfg.Delegate.Agents {
		a.w.cfg.Delegate.Agents[i].Default = a.w.cfg.Delegate.Agents[i].Name == name
	}
	return nil
}

// agentIndex 名字 → 配置下标（找不到 −1）。
func (a *delegateAdmin) agentIndex(name string) int {
	for i, ag := range a.w.cfg.Delegate.Agents {
		if ag.Name == name {
			return i
		}
	}
	return -1
}

// patchAgent 改写 config 里的一条代理（其余条目原样保留）。
func (a *delegateAdmin) patchAgent(name string, edit func(map[string]any)) error {
	found := false
	err := persistConfig(a.w.cfgPath, func(generic map[string]any) {
		sec, _ := generic["delegate"].(map[string]any)
		if sec == nil {
			return
		}
		list, _ := sec["agents"].([]any)
		for _, it := range list {
			m, _ := it.(map[string]any)
			if m == nil {
				continue
			}
			if n, _ := m["name"].(string); n == name {
				edit(m)
				found = true
			}
		}
	})
	if err != nil {
		return fmt.Errorf("%s: %w", i18n.T("cmd.delegate.err.persist_config"), err)
	}
	if !found {
		return errors.New(i18n.Tf("cmd.delegate.err.not_in_config", "name", strconv.Quote(name)))
	}
	return nil
}

// commandLineOf 命令行展示形态：参数含空白时加双引号（与 /delegate list 同口径）。
func commandLineOf(command string, args []string) string {
	parts := make([]string, 0, 1+len(args))
	if cmd := strings.TrimSpace(command); cmd != "" {
		parts = append(parts, quoteArg(cmd))
	}
	for _, a := range args {
		parts = append(parts, quoteArg(a))
	}
	return strings.Join(parts, " ")
}

// quoteArg 需要时给参数加双引号（内部的双引号转义）。
func quoteArg(s string) string {
	if s == "" {
		return `""`
	}
	if !strings.ContainsAny(s, " \t\"") {
		return s
	}
	return `"` + strings.ReplaceAll(s, `"`, `\"`) + `"`
}

// splitCommandLine 把一行命令拆成 command + args（支持双引号与转义）。
//
// 只认双引号：这是配置里唯一能表达的引号，单引号在 Windows 命令行里本来也不是引号。
// 反斜杠只在**引号内**且后面跟着 `"` 或 `\` 时才是转义——否则 `"C:\Program Files\x.exe"`
// 会把路径里的反斜杠吃掉（Windows 路径全是反斜杠，这条不是边角）。
func splitCommandLine(line string) (string, []string, error) {
	var (
		out     []string
		cur     strings.Builder
		inQuote bool
		started bool
	)
	runes := []rune(line)
	flush := func() {
		if !started {
			return
		}
		out = append(out, cur.String())
		cur.Reset()
		started = false
	}
	for i := 0; i < len(runes); i++ {
		r := runes[i]
		switch {
		case r == '\\' && inQuote && i+1 < len(runes) && (runes[i+1] == '"' || runes[i+1] == '\\'):
			cur.WriteRune(runes[i+1])
			started = true
			i++
		case r == '"':
			inQuote = !inQuote
			started = true
		case (r == ' ' || r == '\t') && !inQuote:
			flush()
		default:
			cur.WriteRune(r)
			started = true
		}
	}
	if inQuote {
		return "", nil, errors.New(i18n.T("cmd.delegate.err.unclosed_quote"))
	}
	flush()
	if len(out) == 0 {
		return "", nil, nil
	}
	return out[0], out[1:], nil
}
