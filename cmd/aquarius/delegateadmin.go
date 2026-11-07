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
	"sort"
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
			Command:     ag.Command,
			Args:        ag.Args,
			Env:         ag.Env,
			WorkDir:     ag.WorkDir,
			Roots:       ag.Roots,
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
	snap.Secrets = a.secretNames()
	return snap
}

// secretNames 已存密钥的名字（env 值写 `secret:<名>` 的候选；值永不出库）。
func (a *delegateAdmin) secretNames() []string {
	if a.w.secretsBin == nil {
		return nil
	}
	entries, err := a.w.secretsBin.Entries(context.Background())
	if err != nil {
		return nil // 库解不开不是本页的错误：候选为空，键值照旧能手写
	}
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		out = append(out, e.Name)
	}
	sort.Strings(out)
	return out
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
	return a.updateAgent(idx, func(m map[string]any) {
		m["command"] = command
		setStringList(m, "args", args)
	}, func(ag *delegateAgentConfig) {
		ag.Command, ag.Args = command, args
	})
}

// Add 追加一条代理。名字与命令行的校验口径与解析期一致：缺名、重名、空命令、
// 引号未闭合都在这里拦，不留给下次启动。
//
// 写盘时不写 `enabled`：键缺失 = 启用（与手写配置同口径，少一行噪音）。
func (a *delegateAdmin) Add(name, commandLine string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return errors.New(i18n.T("cmd.delegate.err.name_required"))
	}
	if a.agentIndex(name) >= 0 {
		return errors.New(i18n.Tf("cmd.delegate.err.name_dup", "name", strconv.Quote(name)))
	}
	command, args, err := splitCommandLine(commandLine)
	if err != nil {
		return err
	}
	if command == "" {
		return errors.New(i18n.T("cmd.delegate.err.command_required"))
	}
	item := map[string]any{"name": name, "command": command}
	setStringList(item, "args", args)
	if err := a.persistAgents(func(list []any) []any { return append(list, item) }); err != nil {
		return fmt.Errorf("%s: %w", i18n.T("cmd.delegate.err.persist_config"), err)
	}
	a.w.cfg.Delegate.Agents = append(a.w.cfg.Delegate.Agents,
		delegateAgentConfig{Name: name, Command: command, Args: args})
	return nil
}

// Remove 删除一条。缺省代理也允许删：删完没有缺省是合法配置（只剩一条时自动选它）。
func (a *delegateAdmin) Remove(name string) error {
	idx := a.agentIndex(name)
	if idx < 0 {
		return errors.New(i18n.Tf("cmd.delegate.err.agent_missing", "name", strconv.Quote(name)))
	}
	err := a.persistAgents(func(list []any) []any {
		out := make([]any, 0, len(list))
		for _, it := range list {
			if m, _ := it.(map[string]any); m != nil {
				if n, _ := m["name"].(string); n == name {
					continue
				}
			}
			out = append(out, it) // 认不出的项原样留着：删一条不该顺手丢掉别人的手写内容
		}
		return out
	})
	if err != nil {
		return fmt.Errorf("%s: %w", i18n.T("cmd.delegate.err.persist_config"), err)
	}
	agents := a.w.cfg.Delegate.Agents
	a.w.cfg.Delegate.Agents = append(agents[:idx:idx], agents[idx+1:]...)
	return nil
}

// Rename 改名字。改成原名是幂等的成功（界面「保存」可能没改就按了）。
func (a *delegateAdmin) Rename(oldName, newName string) error {
	newName = strings.TrimSpace(newName)
	if newName == "" {
		return errors.New(i18n.T("cmd.delegate.err.name_required"))
	}
	idx := a.agentIndex(oldName)
	if idx < 0 {
		return errors.New(i18n.Tf("cmd.delegate.err.agent_missing", "name", strconv.Quote(oldName)))
	}
	if newName == oldName {
		return nil
	}
	if a.agentIndex(newName) >= 0 {
		return errors.New(i18n.Tf("cmd.delegate.err.name_dup", "name", strconv.Quote(newName)))
	}
	if err := a.patchAgent(oldName, func(m map[string]any) { m["name"] = newName }); err != nil {
		return err
	}
	a.w.cfg.Delegate.Agents[idx].Name = newName
	return nil
}

// SetCommand 只改可执行命令（参数不动）。
func (a *delegateAdmin) SetCommand(name, command string) error {
	command = strings.TrimSpace(command)
	if command == "" {
		return errors.New(i18n.T("cmd.delegate.err.command_required"))
	}
	idx := a.agentIndex(name)
	if idx < 0 {
		return errors.New(i18n.Tf("cmd.delegate.err.agent_missing", "name", strconv.Quote(name)))
	}
	return a.updateAgent(idx,
		func(m map[string]any) { m["command"] = command },
		func(ag *delegateAgentConfig) { ag.Command = command })
}

// SetArgs 只改参数（一行一个；空表删掉 args 键）。
func (a *delegateAdmin) SetArgs(name string, args []string) error {
	args = trimAll(args)
	idx := a.agentIndex(name)
	if idx < 0 {
		return errors.New(i18n.Tf("cmd.delegate.err.agent_missing", "name", strconv.Quote(name)))
	}
	return a.updateAgent(idx,
		func(m map[string]any) { setStringList(m, "args", args) },
		func(ag *delegateAgentConfig) { ag.Args = args })
}

// SetEnv 改环境变量表（空表删掉 env 键）。界面只写文本：`secret:<名>` 是起进程时
// 由 Secrets 解析的事，这里不碰值。
func (a *delegateAdmin) SetEnv(name string, env map[string]string) error {
	idx := a.agentIndex(name)
	if idx < 0 {
		return errors.New(i18n.Tf("cmd.delegate.err.agent_missing", "name", strconv.Quote(name)))
	}
	clean := make(map[string]string, len(env))
	for k, v := range env {
		k = strings.TrimSpace(k)
		if k == "" {
			continue
		}
		clean[k] = v
	}
	var normalized map[string]string
	if len(clean) > 0 {
		normalized = clean
	}
	return a.updateAgent(idx,
		func(m map[string]any) { setStringMap(m, "env", normalized) },
		func(ag *delegateAgentConfig) { ag.Env = normalized })
}

// SetWorkdir 改工作目录（绝对路径；空 = 删键）。
func (a *delegateAdmin) SetWorkdir(name, dir string) error {
	dir = strings.TrimSpace(dir)
	if dir != "" && !isAbsPath(dir) {
		return errors.New(i18n.Tf("cmd.delegate.err.workdir_abs", "dir", strconv.Quote(dir)))
	}
	idx := a.agentIndex(name)
	if idx < 0 {
		return errors.New(i18n.Tf("cmd.delegate.err.agent_missing", "name", strconv.Quote(name)))
	}
	return a.updateAgent(idx, func(m map[string]any) {
		if dir == "" {
			delete(m, "workdir")
			return
		}
		m["workdir"] = dir
	}, func(ag *delegateAgentConfig) { ag.WorkDir = dir })
}

// SetRoots 改根集（绝对路径，去空去重；空 = 删键）。本版只落盘：A2 申报 `fs` 之后才有人读它。
func (a *delegateAdmin) SetRoots(name string, roots []string) error {
	normalized, err := normalizeRoots(roots)
	if err != nil {
		return errors.New(i18n.Tf("cmd.delegate.err.root_abs", "root", strconv.Quote(err.Error())))
	}
	idx := a.agentIndex(name)
	if idx < 0 {
		return errors.New(i18n.Tf("cmd.delegate.err.agent_missing", "name", strconv.Quote(name)))
	}
	return a.updateAgent(idx,
		func(m map[string]any) { setStringList(m, "roots", normalized) },
		func(ag *delegateAgentConfig) { ag.Roots = normalized })
}

// updateAgent 改一条：先写盘（只动这一条），成功后再同步内存里的影子。
// 顺序固定——写盘失败时内存不改，界面看到的仍是盘上的事实。
func (a *delegateAdmin) updateAgent(idx int, edit func(map[string]any), apply func(*delegateAgentConfig)) error {
	name := a.w.cfg.Delegate.Agents[idx].Name
	if err := a.patchAgent(name, edit); err != nil {
		return err
	}
	apply(&a.w.cfg.Delegate.Agents[idx])
	return nil
}

// persistAgents 整段改写 delegate.agents（增删用；改单条走 patchAgent）。
// 回调拿到的是 `[]any`，返回什么就写什么——数组级增删本来就支持，不必另造一层。
func (a *delegateAdmin) persistAgents(edit func([]any) []any) error {
	return persistConfig(a.w.cfgPath, func(generic map[string]any) {
		sec, _ := generic["delegate"].(map[string]any)
		if sec == nil {
			sec = map[string]any{}
			generic["delegate"] = sec
		}
		list, _ := sec["agents"].([]any)
		sec["agents"] = edit(list)
	})
}

// setStringList 写一个字符串数组键：空表删键（不留 `"key": []` 噪音）。
func setStringList(m map[string]any, key string, vals []string) {
	if len(vals) == 0 {
		delete(m, key)
		return
	}
	list := make([]any, 0, len(vals))
	for _, s := range vals {
		list = append(list, s)
	}
	m[key] = list
}

// setStringMap 写一个字符串表键：nil/空表删键。
func setStringMap(m map[string]any, key string, vals map[string]string) {
	if len(vals) == 0 {
		delete(m, key)
		return
	}
	out := make(map[string]any, len(vals))
	for k, v := range vals {
		out[k] = v
	}
	m[key] = out
}

// trimAll 去掉每项首尾空白，并丢掉空项（「一行一个」的输入框常留空行）。
func trimAll(in []string) []string {
	out := make([]string, 0, len(in))
	for _, s := range in {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
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
