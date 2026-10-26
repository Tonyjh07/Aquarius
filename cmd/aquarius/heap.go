package main

import (
	"os"
	"runtime/debug"
)

// heapLimitBytes 常驻堆的软上限默认值。
//
// 口径：GUI 常驻存活堆实测约 160MB（三个窗口各持一份字体 Shaper、tokenizer 约 24MB，
// 其余是会话树与运行时结构）。不设上限时 GC 目标 = 存活堆的两倍，堆在 160↔325MB 摆动，
// 工作集因此长期偏高。上限取 256MB：GC 目标于是变成 min(上限, 存活×2)，存活堆仍有近一倍
// 的分配余量（一次长轮次不会立刻顶到上限），而超过这个量的增长会被压回。
const heapLimitBytes = 256 << 20

// applyHeapLimit 装进程级堆软上限（main 里调一次，早于装配）。
//
// 放在 main 而不是 run：run 会被端到端测试直接调用，把进程级运行时参数写在测试路径上
// 会污染同进程的其它用例。
//
// GOMEMLIMIT 显式给出时一律不覆盖——那是用户或部署方的口径。判断只能看环境变量：运行时的
// 「未设置」与 GOMEMLIMIT=off 读数相同（都是 math.MaxInt64），分不出意图。
func applyHeapLimit() {
	if os.Getenv("GOMEMLIMIT") != "" {
		return
	}
	debug.SetMemoryLimit(heapLimitBytes)
}
