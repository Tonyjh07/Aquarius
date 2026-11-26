package main

// pluginhost.go Tier-1 插件在装配根的接线（plugin-design.md §5/§7）：私有目录、Host 与
// 注册表。转换与状态机在 adapter/plugingo 与 plugin/tierone，这里只回答「目录在哪、
// 语言是哪一种、谁来接」——与别的适配器一样，接线在 cmd。

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/Tonyjh07/Aquarius/internal/adapter/plugingo"
	"github.com/Tonyjh07/Aquarius/internal/app"
	"github.com/Tonyjh07/Aquarius/internal/i18n"
	"github.com/Tonyjh07/Aquarius/internal/logging"
	"github.com/Tonyjh07/Aquarius/internal/plugin/tierone"
	"github.com/Tonyjh07/Aquarius/internal/port"
	v1 "github.com/Tonyjh07/Aquarius/pluginapi"
)

// pluginDataDir 插件的持久目录。故意不与 Tier-2 的 `plugins/<name>/` 同层：那个目录装的是
// 用户丢进来的声明（`plugin.json`），删声明是「不用这个 server 了」；数据是插件自己的库，
// 两者混在一处会让人以为删声明就该连数据一起删。
func pluginDataDir(profileDir, name string) string {
	return filepath.Join(profileDir, "plugin-data", name)
}

// pluginWorkDir 插件的临时目录。每次新建实例前清空：清「上一次进程留下的中间产物」比在
// 关机时清可靠——崩溃退出没有关机钩子，启动时清是唯一不依赖退出路径的做法。目录在 profile
// 下，由内核独占，删它不会碰到用户文件。
func (w *wiring) pluginWorkDir(name string) (string, error) {
	dir := filepath.Join(w.profileDir, "plugin-tmp", name)
	if err := os.RemoveAll(dir); err != nil {
		return "", err
	}
	return dir, os.MkdirAll(dir, 0o755)
}

// assembleTierOne 造 Tier-1 注册表并跑启动校验（§7 第 2 条）。禁用状态的插件不产生副作用：
// 这里只校验清单，实例在 enable 时才造。
func (w *wiring) assembleTierOne(confirmer port.Confirmer, builtin []port.Tool) {
	w.plugins1 = tierone.New(tierone.Options{
		Factories: w.pluginFactories,
		NewHost:   w.pluginHostFor(confirmer),
		Taken:     w.takenNames(builtin),
		Log:       w.logFrom,
	})
}

// pluginHostFor 造 Host 的工厂：每个插件一份目录、一次语言快照（D341）。
//
// 目录建不出来时给空串而不是让 Create 失败——插件契约里空串就是「宿主没给持久目录，
// 自己降级成不留存」。为一个创建不出来的目录把整个插件拒载，代价比降级大。
func (w *wiring) pluginHostFor(confirmer port.Confirmer) func(string) v1.Host {
	locale := string(w.lang)
	return func(name string) v1.Host {
		data := pluginDataDir(w.profileDir, name)
		if err := os.MkdirAll(data, 0o755); err != nil {
			w.note(logging.LevelWarn, logging.Plugin, "log.plugin.t1_dir_failed",
				"log.act.degraded_no_persist", "name", name, "dir", data, "err", err)
			data = ""
		}
		work, err := w.pluginWorkDir(name)
		if err != nil {
			w.note(logging.LevelWarn, logging.Plugin, "log.plugin.t1_dir_failed",
				"log.act.degraded_no_persist", "name", name, "dir", work, "err", err)
			work = ""
		}
		return plugingo.NewHost(plugingo.HostOptions{
			Plugin:    name,
			Log:       w.logFrom,
			Secrets:   w.secrets,
			Blobs:     w.blobs,
			DataDir:   data,
			WorkDir:   work,
			Locale:    locale,
			Confirmer: confirmer,
		})
	}
}

// takenNames 名字占用判定（§7 第 2 条）：内置工具与 config 里的 MCP server 名都算。
// 目录发现来的 server 启动时才扫，这里查不到——它们与 Tier-1 撞名由注册表启用时的
// 同一份判定兜住，不在这里抢跑。
func (w *wiring) takenNames(builtin []port.Tool) func(string) bool {
	names := make(map[string]bool, len(builtin))
	for _, t := range builtin {
		names[t.Spec().Name] = true
	}
	return func(name string) bool {
		if names[name] {
			return true
		}
		_, ok := w.cfg.MCPServers[name]
		return ok
	}
}

// closeTierOne 关停全部 Tier-1 插件（§7 第 5 条）。注册表为 nil 时什么也不做：装配失败
// 的路径上也要能安全 defer。
func (w *wiring) closeTierOne() {
	if w.plugins1 == nil {
		return
	}
	if err := w.plugins1.Close(context.Background()); err != nil {
		w.note(logging.LevelWarn, logging.Plugin, "log.plugin.t1_shutdown_failed",
			"log.act.continue", "err", err)
	}
}

// ---------- /plugin 操作面（§7「操作面」） ----------

// tierOneAdmin 把 Tier-1 注册表投影成 /plugin 的消费面（与 Tier-2 的 hostAdmin 同一份
// 接口，由 pluginAdminAll 合并）。启停立即生效、不重启——状态机与工具源都在进程内。
type tierOneAdmin struct{ w *wiring }

var _ app.PluginAdmin = tierOneAdmin{}

// Plugins 注册表快照 → app 投影。Source 用 binary 表示「编译进二进制」：它没有配置文件
// 可发现，也就不该显示成 config 或 plugins。
func (a tierOneAdmin) Plugins() []app.PluginInfo {
	if a.w.plugins1 == nil {
		return nil
	}
	recs := a.w.plugins1.List()
	out := make([]app.PluginInfo, 0, len(recs))
	for _, r := range recs {
		pi := app.PluginInfo{
			Name:      r.Name,
			Source:    "binary",
			Transport: "in-process",
			Provides:  r.Provides,
			Enabled:   r.State == tierone.StateActive,
			Status:    r.State.String(),
		}
		if r.Err != nil {
			pi.LastErr = r.Err.Error()
		}
		out = append(out, pi)
	}
	return out
}

// Enable 启用并把工具源挂进 Runner（§7 第 3 条：工具源在 Active 之后挂）。
func (a tierOneAdmin) Enable(ctx context.Context, name string) error {
	return a.w.setTierOne(ctx, name, true, true)
}

// Disable 停用：先摘工具源再停实例，与启用的顺序相反。摘源之后新调用进不来，在途调用
// 由注册表的 Draining 等它跑完（§7 第 4 条）。
func (a tierOneAdmin) Disable(name string) error {
	return a.w.setTierOne(context.Background(), name, false, true)
}

// Logs Tier-1 的调用台账还没接：工具调用记在审计日志的工具名下，而插件可以任意命名工具，
// 从名字反查不到所属插件。给一句实话，不去猜。
func (a tierOneAdmin) Logs(string) (string, error) {
	return i18n.T("plugin.t1.logs_note"), nil
}

// has 名字是否属于 Tier-1：组合面据此决定把请求交给哪一边。
func (a tierOneAdmin) has(name string) bool {
	if a.w.plugins1 == nil {
		return false
	}
	_, ok := a.w.plugins1.Record(name)
	return ok
}

// pluginAdminAll /plugin 的组合面：编译进二进制的插件与 MCP server 共用一条命令，
// 用户不需要知道两套生命周期在内部是两套机制。
type pluginAdminAll struct {
	mcp hostAdmin
	t1  tierOneAdmin
}

var _ app.PluginAdmin = pluginAdminAll{}

func (a pluginAdminAll) Plugins() []app.PluginInfo {
	return append(a.mcp.Plugins(), a.t1.Plugins()...)
}

func (a pluginAdminAll) Enable(ctx context.Context, name string) error {
	if a.t1.has(name) {
		return a.t1.Enable(ctx, name)
	}
	return a.mcp.Enable(ctx, name)
}

func (a pluginAdminAll) Disable(name string) error {
	if a.t1.has(name) {
		return a.t1.Disable(name)
	}
	return a.mcp.Disable(name)
}

func (a pluginAdminAll) Logs(name string) (string, error) {
	if a.t1.has(name) {
		return a.t1.Logs(name)
	}
	return a.mcp.Logs(name)
}

// setTierOne 启停的唯一入口（命令面与启动期共用）。persist 区分两种来源：用户操作要写回
// config，启动期恢复不写——否则一次启动失败会把用户的启用意图改成停用。
func (w *wiring) setTierOne(ctx context.Context, name string, on, persist bool) error {
	if w.plugins1 == nil {
		return errors.New(i18n.T("plugin.t1.unwired"))
	}
	if on {
		if err := w.plugins1.SetEnabled(ctx, name, true); err != nil {
			return err
		}
		w.attachToolSet(name)
	} else {
		w.detachToolSet(name)
		if err := w.plugins1.SetEnabled(ctx, name, false); err != nil {
			return err
		}
	}
	if !persist {
		return nil
	}
	if err := w.persist.persistPluginEnabled(name, on); err != nil {
		return fmt.Errorf("%s: %w", i18n.T("plugin.t1.persist_failed"), err)
	}
	return nil
}

// attachToolSet 把插件的工具面接成工具源（D340）：清单按轮问、按会话解析，所以挂的是源
// 而不是一次性注册的条目。声明了 tool 但实例没实现的情况在启用自检里已被拒载，这里不再
// 报错——拿不到 ToolProvider 就只是没有工具面。
func (w *wiring) attachToolSet(name string) {
	inst, _, ok := w.plugins1.Active(name)
	if !ok {
		return
	}
	tp, ok := inst.(v1.ToolProvider)
	if !ok {
		return
	}
	if w.pluginSets == nil {
		w.pluginSets = make(map[string]*plugingo.ToolSet, 1)
	}
	if _, dup := w.pluginSets[name]; dup {
		return
	}
	set := plugingo.NewToolSet(name, tp)
	w.pluginSets[name] = set
	w.runner.AddSet(set)
}

// detachToolSet 摘掉工具源（幂等）。
func (w *wiring) detachToolSet(name string) {
	set, ok := w.pluginSets[name]
	if !ok {
		return
	}
	w.runner.RemoveSet(set)
	delete(w.pluginSets, name)
}

// startTierOne 启动期按 config 恢复启停（键缺失 = 启用，与 Tier-2 的 D31 语义一致）。
// 单插件失败只记一行、继续下一个：软启动规则。
func (w *wiring) startTierOne(ctx context.Context) {
	if w.plugins1 == nil {
		return
	}
	for _, rec := range w.plugins1.List() {
		if rec.Err != nil || !w.cfg.Plugins[rec.Name].IsEnabled() {
			continue
		}
		if err := w.setTierOne(ctx, rec.Name, true, false); err != nil {
			w.note(logging.LevelWarn, logging.Plugin, "log.plugin.t1_refused",
				"log.act.plugin_off", "name", rec.Name, "err", err)
		}
	}
}
