package main

import (
	"context"
	"errors"
	"sort"
	"sync"
	"time"

	"github.com/Tonyjh07/Aquarius/internal/app"
	"github.com/Tonyjh07/Aquarius/internal/plugin"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

// mergedMemory 主记忆存储 + 插件 resources 只读投影（§6.3/D31）：资源并入索引与
// memory_* 的读取面，写/删恒走主存储（与记忆系统的边界见 DESIGN §3）。
// extras 经闭包现取（插件启停即时生效），按服务器名排序保证索引顺序稳定。
type mergedMemory struct {
	primary port.MemoryStore
	extras  func() []port.MemoryStore
	logf    func(string, ...any)
}

var _ port.MemoryStore = (*mergedMemory)(nil)

// Index 主索引 + 各插件投影（投影失败只记日志，不拖垮主索引）。
func (m *mergedMemory) Index(ctx context.Context) ([]port.MemoryIndexEntry, error) {
	out, err := m.primary.Index(ctx)
	if err != nil {
		return nil, err
	}
	for _, e := range m.extras() {
		entries, err := e.Index(ctx)
		if err != nil {
			m.logf("[plugin] 资源索引失败: %v", err)
			continue
		}
		out = append(out, entries...)
	}
	return out, nil
}

// Read 主存储优先；未命中时查插件投影（按 URI 寻址）。
func (m *mergedMemory) Read(ctx context.Context, name string) (port.MemoryDoc, error) {
	doc, err := m.primary.Read(ctx, name)
	if err == nil {
		return doc, nil
	}
	if !errors.Is(err, port.ErrMemoryNotFound) {
		return port.MemoryDoc{}, err
	}
	for _, e := range m.extras() {
		if doc, rerr := e.Read(ctx, name); rerr == nil {
			return doc, nil
		}
	}
	return port.MemoryDoc{}, err
}

// Write 恒走主存储（插件资源只读，§6.3）。
func (m *mergedMemory) Write(ctx context.Context, doc port.MemoryDoc) error {
	return m.primary.Write(ctx, doc)
}

// Remove 恒走主存储。
func (m *mergedMemory) Remove(ctx context.Context, name string) error {
	return m.primary.Remove(ctx, name)
}

// Search 主存储 + 插件投影的关键词命中（按名去重，主存储优先）。
func (m *mergedMemory) Search(ctx context.Context, query string) ([]port.MemoryHit, error) {
	hits, err := m.primary.Search(ctx, query)
	if err != nil {
		return nil, err
	}
	seen := make(map[string]bool, len(hits))
	for _, h := range hits {
		seen[h.Name] = true
	}
	for _, e := range m.extras() {
		extra, err := e.Search(ctx, query)
		if err != nil {
			m.logf("[plugin] 资源检索失败: %v", err)
			continue
		}
		for _, h := range extra {
			if !seen[h.Name] {
				seen[h.Name] = true
				hits = append(hits, h)
			}
		}
	}
	return hits, nil
}

// hostAdmin 把 plugin.Host 适配为 app.PluginAdmin（D13：app 只见消费面接口）。
type hostAdmin struct{ h *plugin.Host }

var _ app.PluginAdmin = hostAdmin{}

// Plugins 宿主快照 → app 侧投影。
func (a hostAdmin) Plugins() []app.PluginInfo {
	list := a.h.List()
	out := make([]app.PluginInfo, 0, len(list))
	for _, i := range list {
		out = append(out, app.PluginInfo{
			Name:         i.Name,
			Source:       i.Source,
			Transport:    i.Transport,
			Capabilities: i.Capabilities,
			Granted:      i.Granted,
			Risk:         i.Risk,
			Enabled:      i.Enabled,
			Status:       string(i.Status),
			Restarts:     i.Restarts,
			LastErr:      i.LastErr,
			Calls:        i.Stats.Calls,
			Errors:       i.Stats.Errors,
			LastMS:       i.Stats.LastMS,
		})
	}
	return out
}

// Enable 启用（含 capability 授权流）。
func (a hostAdmin) Enable(ctx context.Context, name string) error { return a.h.Enable(ctx, name) }

// Disable 停用并断开。
func (a hostAdmin) Disable(name string) error { return a.h.Disable(name) }

// pluginSurfaces 插件面刷新器（宿主 OnChange 回调）：把就绪会话的 tools/prompts
// 同步到 ToolRunner 与 Session 动态命令表。可能来自崩溃重启 goroutine——内部串行
// （refreshMu），但 I/O 一律锁外做并带超时（审查修复：持锁做网络会卡死 REPL 的
// enable/disable，卡死的 server 会挂起整个面）；工具按集合 diff 增删（审查修复：
// server 侧缩表/改名残留的旧工具此前再也摘不掉）；prompts 拉取失败保留上次成功表
// （与工具"失败保留旧登记"语义一致）。
type pluginSurfaces struct {
	mu         sync.Mutex
	runner     toolRunnerAdder
	session    **app.Session // 装配后期才赋值（先建 host 再建 session）
	ready      func() map[string]plugin.Session
	callCtx    context.Context
	ioTimeout  time.Duration // 单次 tools/prompts 拉取上限（<=0 = 默认 10s）
	logf       func(string, ...any)
	registered map[string][]string            // server → 已注册工具名
	prompts    map[string][]plugin.PromptInfo // server → 上次成功的 prompt 清单
}

// toolRunnerAdder Runner 的最小装配面（Add/Remove；main 侧的具体类型）。
type toolRunnerAdder interface {
	Add(t port.Tool)
	Remove(name string)
}

// refreshedSnapshot 锁外拉取的单 server 结果。
type refreshedSnapshot struct {
	name      string
	tools     []port.Tool
	toolNames []string
	prompts   []plugin.PromptInfo
	promptsOK bool
}

// refresh 幂等同步：锁外带超时拉取 → 锁内 diff 提交。
func (p *pluginSurfaces) refresh() {
	ready := p.ready()
	names := sortedStoreNames(ready)
	timeout := p.ioTimeout
	if timeout <= 0 {
		timeout = 10 * time.Second
	}

	// 阶段一（锁外，网络）：拉取失败者本轮不动（保留旧工具登记）。
	snaps := make([]refreshedSnapshot, 0, len(names))
	for _, name := range names {
		tctx, cancel := context.WithTimeout(p.callCtx, timeout)
		tools, err := ready[name].Tools(tctx)
		cancel()
		if err != nil {
			p.logf("[plugin] %s tools/list: %v", name, err)
			continue
		}
		pctx, cancel := context.WithTimeout(p.callCtx, timeout)
		prompts, perr := ready[name].Prompts(pctx)
		cancel()
		if perr != nil {
			p.logf("[plugin] %s prompts/list: %v", name, perr)
		}
		sn := refreshedSnapshot{name: name, tools: tools, prompts: prompts, promptsOK: perr == nil}
		for _, t := range tools {
			sn.toolNames = append(sn.toolNames, t.Spec().Name)
		}
		snaps = append(snaps, sn)
	}

	p.mu.Lock()
	defer p.mu.Unlock()
	if p.registered == nil {
		p.registered = map[string][]string{}
	}
	if p.prompts == nil {
		p.prompts = map[string][]plugin.PromptInfo{}
	}
	live := make(map[string]bool, len(names))
	for _, n := range names {
		live[n] = true
	}

	// 阶段二（锁内提交）：就绪 server 的工具集合 diff（新增/覆盖 → Add；消失 → Remove）。
	for _, sn := range snaps {
		newSet := make(map[string]bool, len(sn.toolNames))
		for _, tn := range sn.toolNames {
			newSet[tn] = true
		}
		for _, old := range p.registered[sn.name] {
			if !newSet[old] {
				p.runner.Remove(old)
			}
		}
		for _, t := range sn.tools {
			p.runner.Add(t)
		}
		p.registered[sn.name] = sn.toolNames
		if sn.promptsOK {
			p.prompts[sn.name] = sn.prompts
		}
	}
	// 整机下线：清工具与 prompt 簿。
	for name, toolNames := range p.registered {
		if !live[name] {
			for _, tn := range toolNames {
				p.runner.Remove(tn)
			}
			delete(p.registered, name)
			delete(p.prompts, name)
		}
	}

	// 动态命令 /mcp:<server>:<prompt>（渲染后作为用户输入走完整 Turn）；
	// prompt 清单取"上次成功"，本轮拉取失败不清空。
	if *p.session == nil {
		return // session 尚未建好（Start 前的回调）
	}
	sess := *p.session
	dyn := make(map[string]app.CommandHandler)
	for _, name := range names {
		sess := sess
		render := ready[name]
		for _, pi := range p.prompts[name] {
			promptName := pi.Name
			dyn["mcp:"+name+":"+promptName] = func(ctx context.Context, args []string) (string, error) {
				text, err := render.RenderPrompt(ctx, promptName, args)
				if err != nil {
					return "", err
				}
				return sess.Handle(ctx, port.UserInput{Text: text})
			}
		}
	}
	sess.SetDynamicCommands(dyn)
}

// sortedStoreNames 取就绪服务器名（排序，extras 闭包用——索引顺序稳定）。
func sortedStoreNames(ready map[string]plugin.Session) []string {
	names := make([]string, 0, len(ready))
	for n := range ready {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}
