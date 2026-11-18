package port

import "time"

// 运行日志的注入出口（D276 / contracts §7.9）。
//
// 日志包是叶子层，适配器不许 import 它（adapter → port 的方向）：装配根把 `Log` 注入到各
// `Options` 里，适配器只交字段。放在 port 而不是各包各写一份签名，是因为五段（时间 / 级别 /
// 上下文 / 事件 / 行动）是一份契约——形状散成八份，改一处就得追八处才知道漏了谁。

// LogLevel 日志档位（D276）：四档，没有别的。取值与 `internal/logging` 的级别名一致，
// 装配根按它翻成 `logging.Level`；这里复述一遍是因为适配器不许 import 日志包。
type LogLevel string

const (
	LogDebug LogLevel = "debug"
	LogInfo  LogLevel = "info"
	LogWarn  LogLevel = "warn"
	LogError LogLevel = "error"
)

// Log 写一条运行日志：level 定档，ctx 是子系统标识（`logging.Context` 的取值，英文枚举，
// 不是文案），event / act 是 i18n key，kv 是 `名, 值` 成对（与 i18n.Tf 同一种给法）。
//
// 调用点只说**发生了什么**与**当场怎么处置**，句子在 `internal/i18n` 的 `log.*` 段；
// 这里出现用户面字符串就是把文案又搬回了代码。
type Log func(level LogLevel, ctx, event, act string, kv ...any)

// LogFile 一份运行日志的元信息（设置窗「日志」页读侧，D305 / contracts §7.13）。
type LogFile struct {
	Name string    // 文件名（不含目录）
	Size int64     // 字节数
	Mod  time.Time // 最后写入时刻
}

// LogReader 运行日志的读侧：列文件、读末尾。
//
// 与 Log 同一个理由放在 port：适配器（设置窗）不许 import 日志包，而「哪几份文件算运行
// 日志」「末尾读多少行」是写侧规则的延续，只能由装配根一处说了算。界面拿到的是文件名与
// 一段文本，不碰目录布局。
type LogReader interface {
	// Dir 运行日志目录（「打开目录」用它，界面不自己拼路径）。
	Dir() string
	// List 按时间倒序列出日志文件；目录还不存在时返回空表。
	List() ([]LogFile, error)
	// Tail 读 name 的末尾至多 lines 行。name 来自 List，不接受任意路径。
	Tail(name string, lines int) (string, error)
}
