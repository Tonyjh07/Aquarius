package pluginapi

import "context"

// plugin.go 生命周期（plugin-guide.md §7）：工厂 + 实例。
//
// 形态选了工厂而不是单例：每次启用造一个新实例，插件不必满足「初始化可重入」，
// 代价是作者要理解全局状态随实例丢弃——这正是启停给用户的预期。
//
// 供给面方法不挂在 Instance 上：宿主按 Manifest.Provides 对实例做 type assertion
// （声明 llm 就必须实现 LanguageModel），一个插件可以同时供给多个面，而 Instance
// 只管生与死。声明与实现不符在启动校验期拒载，不会拖到第一次调用。

// Factory 一个插件的入口：清单 + 造实例。编译期由宿主装配的列表持有，
// 本包不设全局注册表（§7「注册表归宿主」）。
//
// 兼容承诺：改本接口的方法集会破坏所有 Tier-1 插件的编译。
type Factory interface {
	// Manifest 插件清单：启动时逐项校验（api 版本、provides 自检、名字冲突）。
	// 必须是不依赖实例状态的纯函数——校验发生在 Create 之前。
	Manifest() Manifest
	// Create 造一个新实例。h 由宿主提供，实例被 Shutdown 后宿主把该句柄置失效。
	// 返回错误 = 这次启用失败，宿主按「拒载」处理（状态与报因进 /plugin），
	// 不做半装载。
	Create(h Host) (Instance, error)
}

// Instance 一个已创建的插件实例。同一实例上 Start 至多成功一次；
// 再次启用会造新实例，不复用旧实例。
//
// 兼容承诺：改本接口的方法集会破坏所有 Tier-1 插件的编译。
type Instance interface {
	// Start 完成初始化并可被调用。契约：要快（同步返回），重活异步做或首次调用时懒做；
	// 返回错误 = 实例不可用，宿主按拒载处理。
	Start() error
	// Shutdown 收尾。任何时刻都可能被调用（包括供给面方法执行中），因此实现必须认
	// 传入 ctx 的取消；返回后不得再有任何副作用（goroutine 收净、文件句柄落定）。
	Shutdown(ctx context.Context) error
}
