package main

// plugincmd.go 命令面的合流处。
//
// 会话的动态命令表只有整体替换一个写入口（Session.SetDynamicCommands），而现在有两个来源：
// Tier-2 的 MCP prompts（`/mcp:<server>:<prompt>`）与 Tier-1 插件的 CommandProvider。两边各写
// 一次就会互相覆盖——「启用一个插件，MCP 的命令全没了」这种错还很难在测试里撞见，因为两个
// 来源的启停时机不同。所以都在这里落账，合并之后再交出去一次。
//
// 优先级：MCP 先到先得，Tier-1 撞名直接不出列（与静态命令优先的规则同向——同名命令只留一条，
// 否则补全浮层里两个看起来一样的东西、选哪个却只走一条）。

import (
	"context"
	"errors"
	"sync"

	"github.com/Tonyjh07/Aquarius/internal/i18n"
	"github.com/Tonyjh07/Aquarius/internal/logging"
	"github.com/Tonyjh07/Aquarius/internal/port"
	v1 "github.com/Tonyjh07/Aquarius/pluginapi"

	"github.com/Tonyjh07/Aquarius/internal/app"
)

// cmdSink 合并结果的落点（生产上是 *app.Session；抽成接口只为让合流逻辑能单测）。
type cmdSink interface {
	SetDynamicCommands(map[string]app.CommandHandler, map[string]port.CommandInfo)
}

// cmdTables 两个来源的命令表与合并逻辑。
type cmdTables struct {
	mu   sync.Mutex
	sess func() cmdSink // 会话装配后期才有值；取不到就只落账不提交

	mcp     map[string]app.CommandHandler
	mcpMeta map[string]port.CommandInfo
	t1      map[string]app.CommandHandler
	t1Meta  map[string]port.CommandInfo
}

func newCmdTables(sess func() cmdSink) *cmdTables {
	return &cmdTables{sess: sess}
}

// setMCP 换掉 Tier-2 来源（MCP 每次刷新整体重算，这里也就整体换）。
func (t *cmdTables) setMCP(cmds map[string]app.CommandHandler, meta map[string]port.CommandInfo) {
	t.mu.Lock()
	t.mcp, t.mcpMeta = cmds, meta
	t.mu.Unlock()
	t.flush()
}

// setTierOne 换掉 Tier-1 来源（按当前已启用插件整体重建）。
func (t *cmdTables) setTierOne(cmds map[string]app.CommandHandler, meta map[string]port.CommandInfo) {
	t.mu.Lock()
	t.t1, t.t1Meta = cmds, meta
	t.mu.Unlock()
	t.flush()
}

// flush 合并两份并提交给会话。会话还没建好时什么都不做——真正提交会由后一次 flush 补上
// （插件启停与 MCP 刷新都是低频的，等一次不亏）。
func (t *cmdTables) flush() {
	s := t.sess()
	if s == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()

	out := make(map[string]app.CommandHandler, len(t.mcp)+len(t.t1))
	meta := make(map[string]port.CommandInfo, len(t.mcpMeta)+len(t.t1Meta))
	for name, h := range t.mcp {
		out[name] = h
	}
	for name, ci := range t.mcpMeta {
		meta[name] = ci
	}
	for name, h := range t.t1 {
		if _, dup := out[name]; dup {
			continue // MCP 抢先：同名只留一条，见文件头
		}
		out[name] = h
	}
	for name, ci := range t.t1Meta {
		if _, dup := meta[name]; dup {
			continue
		}
		meta[name] = ci
	}
	s.SetDynamicCommands(out, meta)
}

// rebuildTierOneCommands 按「当前处于 Active 且实现了 CommandProvider 的插件」重建 Tier-1
// 命令表。整体重建而不是增量：插件数是个位数量级，重建一次比维护增删账本便宜，也不会
// 出现「停用后残留一条命令」这种账本漂移。
//
// 撞内置命令的名字在这里就被挡掉（不整插件拒载）：命令名冲突不影响工具面，把一个能用的
// 插件整个关掉太重；记一条 warn 让 /plugin 的日志能解释「为什么我的 /mem 没出现」。
func (w *wiring) rebuildTierOneCommands() {
	if w.cmdTables == nil || w.plugins1 == nil {
		return
	}
	cmds := make(map[string]app.CommandHandler)
	meta := make(map[string]port.CommandInfo)
	taken := builtinCommandNames()

	for _, rec := range w.plugins1.List() {
		inst, _, ok := w.plugins1.Active(rec.Name)
		if !ok {
			continue
		}
		cp, ok := inst.(v1.CommandProvider)
		if !ok {
			continue
		}
		for _, spec := range cp.Commands() {
			for _, cmdName := range spec.Names {
				if cmdName == "" {
					continue
				}
				if taken[cmdName] {
					w.note(logging.LevelWarn, logging.Plugin, "log.plugin.t1_cmd_taken",
						"log.act.keep_builtin", "plugin", rec.Name, "command", cmdName)
					continue
				}
				if _, dup := cmds[cmdName]; dup {
					w.note(logging.LevelWarn, logging.Plugin, "log.plugin.t1_cmd_dup",
						"log.act.keep_first", "plugin", rec.Name, "command", cmdName)
					continue
				}
				cmds[cmdName] = w.tierOneCommandHandler(rec.Name, cmdName)
				meta[cmdName] = tierOneCommandInfo(spec, cmdName)
			}
		}
	}
	w.cmdTables.setTierOne(cmds, meta)
}

// tierOneCommandHandler 一条 Tier-1 命令的分派闭包。
//
// 每次调用都重新取实例：命令表在启用时建好，而插件可能在两次调用之间被停用又启用（拿到
// 的是新实例）。取到实例后用 Guard 记在途——停用流程会等在途跑完，不会在插件 Shutdown
// 之后还有人调它的方法。
func (w *wiring) tierOneCommandHandler(pluginName, cmdName string) app.CommandHandler {
	return func(ctx context.Context, args []string) (string, error) {
		inst, guard, ok := w.plugins1.Active(pluginName)
		if !ok {
			return "", errors.New(i18n.Tf("plugin.t1.cmd_inactive", "name", pluginName))
		}
		if guard != nil {
			if !guard.Enter() {
				return "", errors.New(i18n.Tf("plugin.t1.cmd_draining", "name", pluginName))
			}
			defer guard.Leave()
		}
		cp, ok := inst.(v1.CommandProvider)
		if !ok {
			return "", errors.New(i18n.Tf("plugin.t1.cmd_unsupported", "name", pluginName))
		}
		return cp.Run(v1SessionCtx(ctx), cmdName, args)
	}
}

// v1SessionCtx 把内核侧的会话身份搬到插件侧（两套 ctx 键不通用，转换只在这一处做）。
func v1SessionCtx(ctx context.Context) context.Context {
	if id, ok := port.SessionIDFrom(ctx); ok {
		return v1.WithSession(ctx, string(id))
	}
	return ctx
}

// tierOneCommandInfo 声明 → 端口形状。文案是插件自己的（不进内核 i18n 表，plugin-guide.md §4）。
func tierOneCommandInfo(spec v1.CommandSpec, name string) port.CommandInfo {
	params := make([]port.CommandParam, 0, len(spec.Params))
	for _, p := range spec.Params {
		params = append(params, port.CommandParam{
			Name:       p.Name,
			Desc:       p.Desc,
			Kind:       port.ParamKind(p.Kind),
			Values:     append([]string(nil), p.Values...),
			ValueName:  p.ValueName,
			Repeatable: p.Repeatable,
		})
	}
	return port.CommandInfo{
		Names:  []string{name},
		Usage:  spec.Usage,
		Desc:   spec.Desc,
		Params: params,
	}
}

// builtinCommandNames 内置命令占用的名字集合（含别名）。动态命令与静态同名时分派不到
// （静态优先），列出去只会让人点到一个跑不到的东西。
func builtinCommandNames() map[string]bool {
	catalog := app.CommandCatalog()
	out := make(map[string]bool, len(catalog))
	for _, ci := range catalog {
		for _, n := range ci.Names {
			out[n] = true
		}
	}
	return out
}
