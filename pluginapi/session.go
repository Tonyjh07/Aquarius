package pluginapi

import "context"

// session.go 会话身份的传递方式（plugin-design.md §5「会话身份」）。
//
// 身份不进值类型，走 ctx：`ToolCall`、`CommandSpec`、`ViewAction` 都是一次调用的形状，
// 往里加会话字段等于给每个面各开一个来源，迟早有两处不一致。ctx 只有一个来源，
// 宿主在每个插件入口注入一次，插件在入口读一次。
//
// 分工：宿主用 WithSession 写，插件用 SessionFrom 读。键类型不导出，两边不会撞。
// 这不是安全边界（Tier-1 无进程隔离），是数据形状纪律。

type sessionKey struct{}

// WithSession 宿主专用：把当前会话身份放进 ctx。空串返回原 ctx——「没有会话」就是
// 不注入，插件读到 ok=false，不必区分「空串的会话」与「没有会话」。
//
// 宿主必须在每个插件入口注入：工具执行、命令执行、面板渲染与动作、记忆注入。
// 漏一个，插件在那个入口就看不到会话——这类遗漏不会报错，只会让插件悄悄退回全局。
//
// 兼容承诺：改签名会破坏宿主的注入点与所有读会话的插件。
func WithSession(ctx context.Context, id string) context.Context {
	if id == "" {
		return ctx
	}
	return context.WithValue(ctx, sessionKey{}, id)
}

// SessionFrom 读取宿主注入的会话身份。ok=false = 当前调用没有会话上下文
// （会话列表预览、启动探针、非会话触发的命令）。
//
// 插件不得把 ok=false 当成「全局操作」的同义词——没有会话时该拒绝就拒绝，
// 全局记忆只是其中一种合法选择，不是缺省值。
//
// 兼容承诺：改签名会破坏所有读会话的插件。
func SessionFrom(ctx context.Context) (string, bool) {
	id, ok := ctx.Value(sessionKey{}).(string)
	if !ok || id == "" {
		return "", false
	}
	return id, true
}
