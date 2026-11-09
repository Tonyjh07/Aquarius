package uigui

import (
	"sync"

	"github.com/Tonyjh07/Aquarius/internal/port"
)

// 界面层的运行日志通道（D288）。
//
// 托盘、快捷键、品牌图标、首帧上屏、位图释放这些诊断过去直接 `fmt.Print` 到 stdout：
// 没有时间与级别，也不进日志文件；终端回显上线后同一行必须与文件里那行字字相同，
// 于是统一走 `port.Log`。上下文恒为 `ui`。
//
// UI 实例上的调用走 `u.logf`（跟着这个实例的 Options 走）；图标与羽化这两处是包级
// 单例（`logoOnce` / `featherDisabled` 一次定终身），只能经包级槽拿出口。

var (
	uiLogMu sync.RWMutex
	uiLog   port.Log
)

// setLogSink 记下本进程的日志出口（newUI 在 goroutine 启动前调用）。
func setLogSink(l port.Log) {
	uiLogMu.Lock()
	uiLog = l
	uiLogMu.Unlock()
}

// logf 包级诊断走日志：未注入 = 不记（headless 测试与未装配路径）。
func logf(lv port.LogLevel, event, act string, kv ...any) {
	uiLogMu.RLock()
	l := uiLog
	uiLogMu.RUnlock()
	if l != nil {
		l(lv, "ui", event, act, kv...)
	}
}

// logf 本实例的日志出口：托盘线程与事件循环都会调，Options 只读，无需加锁。
func (u *UI) logf(lv port.LogLevel, event, act string, kv ...any) {
	if u.opts.Log != nil {
		u.opts.Log(lv, "ui", event, act, kv...)
	}
}
