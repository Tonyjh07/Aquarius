package main

// pluginhost.go Tier-1 插件在装配根的接线（plugin-guide.md §5/§10）：私有目录、Host 与
// 注册表。转换与状态机在 adapter/plugingo 与 plugin/tierone，这里只回答「目录在哪、
// 语言是哪一种、谁来接」——与别的适配器一样，接线在 cmd。

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Tonyjh07/Aquarius/internal/adapter/plugingo"
	"github.com/Tonyjh07/Aquarius/internal/adapter/uigui"
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

// pluginRegistry 现取 Tier-1 注册表。UI 与 TUI 都在插件宿主之前构造，交给它们的
// 只能是这个函数：把 w.plugins1 的值拷进 Options 会拷到 nil（面板与设置页永远为空）。
func (w *wiring) pluginRegistry() *tierone.Registry { return w.plugins1 }

// assembleTierOne 造 Tier-1 注册表并跑启动校验（§7 第 2 条）。禁用状态的插件不产生副作用：
// 这里只校验清单，实例在 enable 时才造。
func (w *wiring) assembleTierOne(confirmer port.Confirmer, builtin []port.Tool) {
	w.plugins1 = tierone.New(tierone.Options{
		// 外部插件只从这一处进来（pluginext.go）：默认构建为空，验收构建由 -overlay
		// 换成带插件的实现。装配根不认识任何具体插件。
		Factories: externalFactories(),
		NewHost:   w.pluginHostFor(confirmer),
		Taken:     w.takenNames(builtin),
		Log:       w.logFrom,
	})
}

// pluginHostFor 造 Host 的工厂：每个插件一份目录、一次语言快照（D341）。
//
// 目录建不出来时给空串而不是让 Create 失败——插件契约里空串就是「宿主没给持久目录，
// 自己降级成不留存」。为一个创建不出来的目录把整个插件拒载，代价比降级大。
func (w *wiring) pluginHostFor(confirmer port.Confirmer) func(v1.Manifest) v1.Host {
	locale := string(w.lang)
	return func(m v1.Manifest) v1.Host {
		name := m.Name
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
		opts := plugingo.HostOptions{
			Plugin:    name,
			Log:       w.logFrom,
			Secrets:   w.secrets,
			Blobs:     w.blobs,
			DataDir:   data,
			WorkDir:   work,
			Locale:    locale,
			Confirmer: confirmer,
			// D352：环境快照按需现取（方法值绑定 w，读的是当时的 w.ui 与主题）。
			Environ: w.uiEnvironment,
		}
		// 迁移素材只给记忆插件（D337 决策 5）：它是内置记忆的交接面，别的插件没有理由读它。
		// 判据用清单快照而不是反查注册表：此刻注册表正锁在 enable 里（见 Options.NewHost）。
		if hasPort(m.Provides, tierone.PortMemory) {
			opts.LegacyMemory = w.legacyMemorySnapshot
		}
		return plugingo.NewHost(opts)
	}
}

// legacyMemorySnapshot 内置记忆的一次性只读快照：全局一篇 + 每个有内容的会话各一篇。
// 只读不搬——搬不搬、搬之前要不要确认，由插件自己决定（D337 决策 5）。
//
// 单篇读失败就跳过：迁移是一次性动作，为一篇坏文件让整次迁移失败，用户得不到任何东西。
func (w *wiring) legacyMemorySnapshot(ctx context.Context) (v1.LegacyMemorySnapshot, error) {
	var snap v1.LegacyMemorySnapshot
	if w.mem == nil {
		return snap, nil
	}
	if doc, err := w.mem.Read(ctx, port.GlobalMemoryDoc); err == nil {
		snap.Global = v1.LegacyGlobalMemory{Name: port.GlobalMemoryDoc, Content: doc.Content}
	} else if !errors.Is(err, port.ErrMemoryNotFound) {
		return snap, err
	}
	entries, err := w.mem.Index(ctx)
	if err != nil {
		return snap, err
	}
	for _, e := range entries {
		sid, ok := strings.CutSuffix(e.Name, port.SessionMemorySuffix)
		if !ok {
			continue // 只认会话记忆文档（全局已在上面读过）
		}
		doc, err := w.mem.Read(ctx, e.Name)
		if err != nil || strings.TrimSpace(doc.Content) == "" {
			continue
		}
		snap.Sessions = append(snap.Sessions, v1.LegacySessionMemory{Name: e.Name, Session: sid, Content: doc.Content})
	}
	sort.Slice(snap.Sessions, func(i, j int) bool { return snap.Sessions[i].Session < snap.Sessions[j].Session })
	return snap, nil
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
	// 插件都关了，命令表也要空：留下一条指向已停用插件的名字，用户点到只会得到报错。
	if w.cmdTables != nil {
		w.cmdTables.setTierOne(nil, nil)
	}
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

var (
	_ app.PluginAdmin   = tierOneAdmin{}
	_ uigui.PluginAdmin = tierOneAdmin{}
)

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

// Snapshot 设置窗插件页的清单（uigui.PluginAdmin）。与 Plugins 是同一份快照的两种投影：
// app 那边给命令面用（Source/Transport 字段），界面这边只关心名字、状态与开关。
func (a tierOneAdmin) Snapshot() []uigui.PluginState {
	reg := a.w.pluginRegistry()
	if reg == nil {
		return nil
	}
	recs := reg.List()
	out := make([]uigui.PluginState, 0, len(recs))
	for _, r := range recs {
		st := uigui.PluginState{
			Name:     r.Name,
			State:    r.State.String(),
			Enabled:  r.State == tierone.StateActive,
			Provides: append([]string(nil), r.Provides...),
		}
		if r.Err != nil {
			st.Err = r.Err.Error()
		}
		out = append(out, st)
	}
	return out
}

// SetEnabled 设置窗的开关（用户动作 → 写回 config，重启后保持）。
func (a tierOneAdmin) SetEnabled(name string, on bool) error {
	if a.w == nil || a.w.pluginRegistry() == nil {
		return errors.New(i18n.T("plugin.t1.unwired"))
	}
	return a.w.setTierOne(context.Background(), name, on, true)
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

// Views 某个插件声明的面板（D346）。只认已启用的实例——停用的插件没有实例可以问，而
// 「声明在手但打不开」对用户毫无用处，不如直接报「没启用」。
func (a tierOneAdmin) Views(name string) ([]app.PluginViewSpec, error) {
	if a.w.plugins1 == nil {
		return nil, errors.New(i18n.T("plugin.t1.unwired"))
	}
	if _, ok := a.w.plugins1.Record(name); !ok {
		return nil, errors.New(i18n.Tf("plugin.t1.not_found", "name", name))
	}
	inst, _, active := a.w.plugins1.Active(name)
	if !active {
		return nil, errors.New(i18n.Tf("plugin.t1.view_inactive", "name", name))
	}
	vp, ok := inst.(v1.ViewProvider)
	if !ok {
		return nil, nil
	}
	specs := vp.Views()
	out := make([]app.PluginViewSpec, 0, len(specs))
	for _, v := range specs {
		out = append(out, app.PluginViewSpec{Plugin: name, ID: v.ID, Title: v.Title})
	}
	return out, nil
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

// Views 面板只有 Tier-1 有（MCP server 没有界面面），所以这里不给 mcp 留分支：名字不
// 属于 Tier-1 就是「没有这个插件」。
func (a pluginAdminAll) Views(name string) ([]app.PluginViewSpec, error) {
	return a.t1.Views(name)
}

// setTierOne 启停的唯一入口（命令面与启动期共用）。persist 区分两种来源：用户操作要写回
// config，启动期恢复不写——否则一次启动失败会把用户的启用意图改成停用。
func (w *wiring) setTierOne(ctx context.Context, name string, on, persist bool) error {
	if w.plugins1 == nil {
		return errors.New(i18n.T("plugin.t1.unwired"))
	}
	// 声明了 provides: memory 的插件在启用期间要让内置记忆面退出。判断看清单而不是实例：
	// 启用的第一步就要腾名字（D340），那时实例还没造出来。
	memPlugin := w.memRoute != nil && w.providesMemory(name)
	if on {
		if memPlugin {
			w.memRoute.Arm(name) // 先撤内置四工具与 /memory，插件的 memory_* 才不会撞名
			w.syncMemorySurfaces()
		}
		if err := w.plugins1.SetEnabled(ctx, name, true); err != nil {
			if memPlugin { // 没起来就还原：内置记忆面不能因为一次失败的启用而消失
				w.memRoute.Release(name)
				w.syncMemorySurfaces()
			}
			return err
		}
		w.attachToolSet(name)
		if memPlugin {
			w.takeMemory(name)
			w.syncMemorySurfaces()
		}
	} else {
		w.detachToolSet(name)
		if err := w.plugins1.SetEnabled(ctx, name, false); err != nil {
			return err
		}
		// 面板跟着走（plugin-guide.md §6）：留着上一帧等于让用户对着一个已经死掉的
		// 插件的界面点按钮。关窗失败不影响停用——那是界面层的事，记不记都在日志里。
		if w.ui != nil {
			_ = w.ui.Emit(ctx, port.CloseViewEvent{Plugin: name})
		}
		if memPlugin { // 停用之后再交还：先让插件的 Recall 彻底退出，再恢复内置面
			w.memRoute.Release(name)
			w.syncMemorySurfaces()
		}
	}
	// 命令面跟着启停走：整体按当前 Active 集合重建（工具源是增删式，命令表只能整换）。
	w.rebuildTierOneCommands()
	if !persist {
		return nil
	}
	if err := w.persist.persistPluginEnabled(name, on); err != nil {
		return fmt.Errorf("%s: %w", i18n.T("plugin.t1.persist_failed"), err)
	}
	return nil
}

// hasPort 清单里是否声明了某个供给面。
func hasPort(provides []string, want string) bool {
	for _, p := range provides {
		if p == want {
			return true
		}
	}
	return false
}

// providesMemory 清单里是否声明了记忆供给面。
func (w *wiring) providesMemory(name string) bool {
	if w.plugins1 == nil {
		return false
	}
	rec, ok := w.plugins1.Record(name)
	if !ok {
		return false
	}
	return hasPort(rec.Provides, tierone.PortMemory)
}

// takeMemory 插件起来之后接管注入面。实例必须实现 Memory——`provides` 自检已经在启用时
// 拦过这一类，这里拿不到就是没得接，保持内置面不变。
func (w *wiring) takeMemory(name string) {
	inst, _, ok := w.plugins1.Active(name)
	if !ok {
		return
	}
	m, ok := inst.(v1.Memory)
	if !ok {
		return
	}
	w.memRoute.Take(name, plugingo.NewMemory(m))
	w.note(logging.LevelInfo, logging.Plugin, "log.plugin.t1_memory_takeover",
		"log.act.ready", "name", name)
}

// applyDisabled 把「用户禁用集 ∪ 记忆接管集」推给 Runner。接管集不进 disabledHolder：
// 那一份会写回 config 的 tools.disabled，用户没关过的工具不该出现在他的配置里。
func (w *wiring) applyDisabled() {
	if w.runner == nil || w.disabledTools == nil {
		return
	}
	set := w.disabledTools.snapshot()
	if w.memRoute != nil && w.memRoute.Blocking() {
		for _, n := range memoryToolNames {
			set[n] = true
		}
	}
	w.runner.SetDisabled(set)
}

// syncMemorySurfaces 内置记忆面的可见性跟随接管状态（D337 决策 3）：接管期间四工具从
// 模型面消失、/memory 从命令清单与分派里消失。记忆交还后原样回来，不需要重启。
func (w *wiring) syncMemorySurfaces() {
	w.applyDisabled()
	if w.sess != nil {
		var hidden []string
		if w.memRoute != nil && w.memRoute.Blocking() {
			hidden = []string{"memory"}
		}
		w.sess.SetHiddenCommands(hidden)
	}
	w.toolsRev.Add(1)
}

// isMemoryTool 是否是内置记忆工具（接管期间要退出、且名字要让给插件的那四个）。
func isMemoryTool(name string) bool {
	for _, n := range memoryToolNames {
		if n == name {
			return true
		}
	}
	return false
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
