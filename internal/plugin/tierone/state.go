// Package tierone Tier-1 插件的宿主侧：清单校验、实例状态机与注册表
// （plugin-design.md §7）。
//
// 本包只认 pluginapi 与标准库（外加 i18n 文案表）：内核怎么把 port 接口接到插件上是
// adapter/plugingo 的事，本包不 import 任何适配器——Host 由装配根通过 NewHost 注入。
// 这条依赖方向是刻意的：状态机的测试不需要拉起整套内核服务。
package tierone

import (
	"context"
	"sync"
)

// State 一个插件实例的状态（§7 状态机）。
//
//	Registered ──> Validated ──> Stopped ──启用──> Active ──禁用──> Draining ──> Stopped
//
// 重新启用永远走「Stopped → Create 新实例 → Active」，不复用旧实例：插件不必满足
// 「Init 可重入」，全局状态随实例丢弃正是启停给用户的预期。
type State int

const (
	// StateRegistered 编译进二进制的候选，还没过清单校验。
	StateRegistered State = iota
	// StateValidated 清单通过校验，等待启停决定。
	StateValidated
	// StateStopped 停用态：enabled=false、校验失败、或已停干净。
	StateStopped
	// StateActive Start 成功，供给面可被调用。
	StateActive
	// StateDraining 禁用中：新调用被拒，等在途调用跑完再 Shutdown。
	StateDraining
)

// String 状态名（/plugin 展示与测试断言共用；英文枚举，不进文案表）。
func (s State) String() string {
	switch s {
	case StateRegistered:
		return "registered"
	case StateValidated:
		return "validated"
	case StateStopped:
		return "stopped"
	case StateActive:
		return "active"
	case StateDraining:
		return "draining"
	default:
		return "unknown"
	}
}

// Record 一个插件的对外快照（/plugin 与设置窗读这个）。值语义：拿到的是那一刻的
// 事实，不随后续状态变化更新。
type Record struct {
	Name     string
	State    State
	Provides []string
	// Err 最近一次失败的原因（校验失败、Create 失败、Start 失败、Shutdown 失败）；
	// 成功启用后清空。
	Err error
	// Guard 非 Active / Draining 时为 nil：只有活着的实例才有在途计数。
	Guard *Guard
}

// Guard 在途调用计数（Draining 靠它等在途跑完，§7 第 4 条）。
//
// 用法：适配器每个入口先 Enter，被拒就直接返回「插件已停用」；拿到许可的调用结束
// 必须 Leave（defer）。Close 之后 Enter 恒 false——这就是「进入 Draining 的瞬间摘除
// 端点」在单进程里的等价物：新调用进不来，老调用照常跑完。
type Guard struct {
	mu     sync.Mutex
	closed bool
	wg     sync.WaitGroup
}

// Enter 申请一次在途许可；false = 插件已在 Draining，调用方应直接失败。
func (g *Guard) Enter() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.closed {
		return false
	}
	g.wg.Add(1)
	return true
}

// Leave 交还在途许可。每个成功的 Enter 必须恰好配一次。
func (g *Guard) Leave() { g.wg.Done() }

// close 关闭入口。只由状态机在进入 Draining 时调用。
func (g *Guard) close() {
	g.mu.Lock()
	g.closed = true
	g.mu.Unlock()
}

// wait 等在途调用跑完或 ctx 到期。先 close 再 wait：closed 之后不会再有 Add，
// 与 WaitGroup 的「Add 必须早于 Wait」约定不冲突。
func (g *Guard) wait(ctx context.Context) error {
	done := make(chan struct{})
	go func() {
		g.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
