package app

import (
	"github.com/Tonyjh07/Aquarius/internal/domain/conversation"
	"github.com/Tonyjh07/Aquarius/internal/domain/perm"
)

// 命令面用到的四个具名端口（P2-4）：它们原先都是 SessionDeps 上的匿名回调，调用点
// 只有一两处，但每个都带着「nil 时这条命令报什么错」的隐含契约。收成接口后，
// 判断变成 `if s.x == nil` 一处、实现由装配根一次性提供，而不是散在每条命令里。

// PermissionSettings 权限面（`/permission` 与 `/unattended`，D22/D185）。
// 两项合成一个接口：它们写的是同一段 config（`permissions`），改的是同一条执行链，
// 拆开只会让装配根把同一个 cfgWriter 适配两遍。
type PermissionSettings interface {
	// Unattended 无人值守当前是否开着。未接线时由调用方按「关」处理，
	// 所以这里不设「没接线」的第三态。
	Unattended() bool
	// PersistUnattended 把开关写回 config；成功后由实现方同步活开关。
	PersistUnattended(on bool) error
	// PersistLevel 把权限等级写回 config；成功后由实现方同步活等级。
	PersistLevel(level perm.Level) error
}

// ThinkingSettings 原生思考面（`/think` 与 `/effort`，D34）。
// 两项都是「把值写回 config、成功后切 Agent 热态」，与 PermissionSettings 同形；
// 分成两个接口是因为消费方不同——`/think` 不碰档位，`/effort` 不碰总开关。
type ThinkingSettings interface {
	// PersistThink 把原生思考开关写回 config。
	PersistThink(on bool) error
	// PersistEffort 把推理档位写回 config（空串 = 清除该键）。
	PersistEffort(level string) error
}

// WatcherCanceller 撤销某会话的后台任务观察者（D167）：切走 / 新建 / 删除当前会话时
// 调用，保证任务完成报告不跨会话。未接线时跳过（测试或未接轮询器的装配）。
type WatcherCanceller interface {
	CancelSession(id conversation.ID)
}

// MemoryOpener 用系统编辑器打开记忆文档（D24）：返回实际打开的路径。
// 未接线时 `/memory` 报「未配置记忆编辑器」。
type MemoryOpener interface {
	Open(name string) (string, error)
}
