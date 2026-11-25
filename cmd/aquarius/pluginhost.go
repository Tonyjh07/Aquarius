package main

// pluginhost.go Tier-1 插件在装配根的接线（plugin-design.md §5/§7）：私有目录、Host 与
// 注册表。转换与状态机在 adapter/plugingo 与 plugin/tierone，这里只回答「目录在哪、
// 语言是哪一种、谁来接」——与别的适配器一样，接线在 cmd。

import (
	"context"
	"os"
	"path/filepath"

	"github.com/Tonyjh07/Aquarius/internal/adapter/plugingo"
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
