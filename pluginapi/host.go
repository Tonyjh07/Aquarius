package pluginapi

import (
	"context"
	"io"
)

// host.go Host：插件可用的全部内核服务（plugin-design.md §5）。
//
// 刻意少给：每个方法都是内核要长期背的兼容债。首发六项，往后只加独立接口
// （插件对 Host 句柄做 type assertion），不扩本接口——扩了会破坏所有实现方。
//
// 兼容承诺：改本接口的方法集会破坏宿主与全部 Tier-1 插件的编译。
type Host interface {
	// Logger 运行日志出口（内核一处写出，D276）。插件不自开日志文件。
	Logger() Logger
	// Secrets 按名解析 secret:<name>；只解析、不存储，明文不落到插件自己的落盘面。
	Secrets() SecretResolver
	// Blobs 内容寻址附件库：与内核共享去重语义，插件拿不到裸文件路径。
	Blobs() BlobStore
	// WorkDir 插件私有临时目录（随 profile，关机清理）。同一实例内稳定。
	WorkDir() string
	// Confirm 走内核确认链：权限矩阵、审计与无人值守语义都罩在这里。
	// 需要用户确认的动作必须走这里，插件自造确认 UI 不算数（§9 规则 4）。
	Confirm(req ConfirmRequest) (ConfirmAnswer, error)
	// Notify 用户通知出口（系统通知）。消息流内的轻量提示见 NoticePoster。
	Notify(level, text string)
}

// NoticePoster Host 的可选扩展：把轻量提示放进消息流，而不是弹系统通知。
// 用法是对 Host 句柄做 type assertion —— 宿主没实现时插件退回 Notify 的语义。
// 兼容承诺：改本接口会破坏已实现它的宿主与断言使用它的插件。
type NoticePoster interface {
	PostNotice(level, text string)
}

// LogLevel 日志档位（与内核四档一致）。取值词表冻结，新增只追加常量。
// 兼容承诺：改取值会破坏宿主的日志落盘解析与既有插件的过滤逻辑。
type LogLevel string

const (
	LogDebug LogLevel = "debug"
	LogInfo  LogLevel = "info"
	LogWarn  LogLevel = "warn"
	LogError LogLevel = "error"
)

// Logger 插件日志出口。五段里的「上下文」由宿主填成 plugin:<name>，插件改不了——
// 日志按来源归组靠这一格，插件自填会毁掉归组。
//
// event（发生了什么）与 act（当场怎么处置）由插件自写句子：插件文案不进内核文案表
// （§6 纪律 2），宿主只负责落盘格式。kv 是「名, 值」成对，名用固定英文标识。
//
// 兼容承诺：改方法签名会破坏宿主实现与全部调用日志的插件。
type Logger interface {
	Log(level LogLevel, event, act string, kv ...any)
}

// SecretResolver 按名解析密钥。名字来自配置里的 secret:<name> 引用；解析失败要
// 返回错误，不要返回空串兼作失败（空串可能是合法的密钥值）。
//
// 兼容承诺：改方法签名会破坏宿主的密钥实现与凭证插件。
type SecretResolver interface {
	Resolve(ctx context.Context, name string) (string, error)
}

// BlobStore 内容寻址附件库。同一份字节只存一份；插件得到的是 BlobRef，不是路径。
//
// 没有 GC：清扫的 keep 集来自会话树，插件交上来的 keep 会把别人引用的字节删掉
// （§3.1 映射表）。插件只管写自己产出的字节。
//
// 兼容承诺：改方法集或参数会破坏产图、产音频与读附件的插件。
type BlobStore interface {
	// Put 存一段字节，返回内容寻址引用；同内容重复 Put 得到同一个引用。
	Put(ctx context.Context, r io.Reader, mime, name string) (BlobRef, error)
	// Get 取回字节，调用方负责 Close。
	Get(ctx context.Context, ref BlobRef) (io.ReadCloser, error)
	// Stat 判断引用是否还在库里（不存在返回 false，不是错误）。
	Stat(ctx context.Context, ref BlobRef) (bool, error)
}
