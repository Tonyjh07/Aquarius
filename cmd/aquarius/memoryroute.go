package main

// memoryroute.go 记忆挂载点的运行期切换（D337）：默认回答者是内置文档记忆，记忆插件
// 启用后每轮改问插件，停用后交还内置。启停即时生效、不重启——两个回答者都在进程内。
//
// 为什么是「路由」而不是启动时选一个：接管与否取决于用户此刻启用了哪个插件，
// 而那件事随时会变（/plugin enable）。装配面只拿一个 port.MemorySource，
// 切换在它背后发生，Agent 不知道记忆换了主人。

import (
	"context"
	"sync"

	"github.com/Tonyjh07/Aquarius/internal/domain/conversation"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

// memoryToolNames 接管期间退出的内置记忆工具（D337 决策 3）。名字腾出来给插件用：
// 模型侧的提示词、习惯与既有对话里对 memory_search 的引用都不用改，迁移成本几乎为零。
var memoryToolNames = []string{"memory_list", "memory_read", "memory_search", "memory_write"}

// memoryRoute 记忆挂载点的路由器。三种状态并存：
//
//	owner == ""          → 内置文档记忆回答（常态）
//	owner != "", src nil → 已接管但插件还没起来（腾名字的窗口期，见 D340 装配顺序）
//	owner != "", src != nil → 插件回答
type memoryRoute struct {
	mu      sync.RWMutex
	builtin port.MemorySource
	owner   string
	src     port.MemorySource
	log     port.Log
}

func newMemoryRoute(builtin port.MemorySource, log port.Log) *memoryRoute {
	return &memoryRoute{builtin: builtin, log: log}
}

var _ port.MemorySource = (*memoryRoute)(nil)

// Recall 问当前回答者要本轮的记忆。插件失败只记一行并返回空块——记忆坏了不该挡住对话，
// 与内置实现读取失败的姿态一致。
func (m *memoryRoute) Recall(ctx context.Context, id conversation.ID, q port.MemoryQuery) (port.MemoryBlock, error) {
	m.mu.RLock()
	src, owner, builtin := m.src, m.owner, m.builtin
	m.mu.RUnlock()
	if src == nil {
		if builtin == nil {
			return port.MemoryBlock{}, nil
		}
		return builtin.Recall(ctx, id, q)
	}
	blk, err := src.Recall(ctx, id, q)
	if err != nil {
		if m.log != nil {
			m.log(port.LogWarn, "plugin", "log.plugin.t1_memory_failed",
				"log.act.skip", "name", owner, "err", err)
		}
		return port.MemoryBlock{}, err
	}
	// 细目由路由统一标：插件不该关心 /context 里怎么显示，它只回答「注入什么」。
	blk.Detail = "plugin:" + owner
	return blk, nil
}

// Arm 腾出内置记忆面（撤四工具、隐 /memory），但还不接回答者。启用一个声明
// provides: memory 的插件前必须先做：否则插件的 memory_* 会撞上仍被认为「被占用」的
// 内置名字而被拒载（D340 装配顺序）。
func (m *memoryRoute) Arm(owner string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.owner != "" {
		return // 已有接管者：这一份多半会被拒，别动已经生效的接管
	}
	m.owner, m.src = owner, nil
}

// Take 接管：此后每轮改问 src。只有已经 Arm 过的接管者能接。
func (m *memoryRoute) Take(owner string, src port.MemorySource) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.owner != "" && m.owner != owner {
		return // 别人的接管，不接
	}
	m.owner, m.src = owner, src
}

// Release 交还内置记忆面。只有当前接管者能交还——插件 A 停用不该把插件 B 的接管撤掉。
func (m *memoryRoute) Release(owner string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.owner != owner {
		return
	}
	m.owner, m.src = "", nil
}

// Owner 当前接管者（"" = 内置记忆自己在用）。工具禁用集、/memory 是否隐藏、
// MCP resources 是否投影记忆，都看这一个判断。
func (m *memoryRoute) Owner() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.owner
}

// Blocking 内置记忆面此刻是否处于退出状态（四工具与 /memory 都该消失）。
func (m *memoryRoute) Blocking() bool { return m.Owner() != "" }
