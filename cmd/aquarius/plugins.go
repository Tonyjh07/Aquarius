package main

import (
	"context"
	"errors"
	"sort"
	"sync"

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
// 同步到 ToolRunner 与 Session 动态命令表。可能来自崩溃重启 goroutine——
// 内部串行（refreshMu），Runner 与 Session 各自加锁。
type pluginSurfaces struct {
	mu         sync.Mutex
	runner     toolRunnerAdder
	session    **app.Session // 装配后期才赋值（先建 host 再建 session）
	ready      func() map[string]plugin.Session
	callCtx    context.Context
	logf       func(string, ...any)
	registered map[string][]string // server → 已注册工具名（停用时移除）
}

// toolRunnerAdder Runner 的最小装配面（Add/Remove；main 侧的具体类型）。
type toolRunnerAdder interface {
	Add(t port.Tool)
	Remove(name string)
}

// refresh 全量同步工具与动态命令（幂等）。
func (p *pluginSurfaces) refresh() {
	p.mu.Lock()
	defer p.mu.Unlock()
	ready := p.ready()
	names := make([]string, 0, len(ready))
	for n := range ready {
		names = append(names, n)
	}
	sort.Strings(names)

	// 工具：就绪 server 逐一同步（Add 同名覆盖），不再就绪的按记录移除。
	for _, name := range names {
		sess := ready[name]
		tools, err := sess.Tools(p.callCtx)
		if err != nil {
			p.logf("[plugin] %s tools/list: %v", name, err)
			continue
		}
		registered := make([]string, 0, len(tools))
		for _, t := range tools {
			p.runner.Add(t)
			registered = append(registered, t.Spec().Name)
		}
		p.registered[name] = registered
	}
	for name, toolNames := range p.registered {
		if _, ok := ready[name]; !ok {
			for _, tn := range toolNames {
				p.runner.Remove(tn)
			}
			delete(p.registered, name)
		}
	}

	// 动态命令 /mcp:<server>:<prompt>（渲染后作为用户输入走完整 Turn）。
	if *p.session == nil {
		return // session 尚未建好（Start 前的回调）
	}
	sess := *p.session
	dyn := make(map[string]app.CommandHandler)
	for _, name := range names {
		sess := sess
		prompts, err := ready[name].Prompts(p.callCtx)
		if err != nil {
			p.logf("[plugin] %s prompts/list: %v", name, err)
			continue
		}
		for _, pi := range prompts {
			promptName := pi.Name
			render := ready[name]
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
