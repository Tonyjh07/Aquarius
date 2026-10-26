package main

import (
	"os"
	"runtime/debug"
)

// 常驻内存的两个旋钮各自管什么，别混着读：
//
//   - gcPercent 压低堆的摆动倍数。Go 的 GC 目标 = 存活堆 × (1 + GOGC/100)。默认 100 即
//     1↔2 倍，实测真实 profile 的压力操作（展开/收起、设置翻页、历史管理拖节点/切会话）把
//     存活堆从 154MB 推到 454MB，工作集随之冲到 970MB。取 50 把摆动收到 1↔1.5 倍，代价是
//     同一段分配多一倍的 GC 次数（实测单次 ~5ms、0.5% CPU，闲置期几十秒才一次）。
//   - heapLimitBytes 只是**泄漏保险**，不参与日常调优。软上限的作用是封死「存活堆无界增长」，
//     不是压低常态峰值——空闲工作集里约 170MB 是非堆（渲染缓冲、线程栈），字体与 tokenizer
//     另占几十 MB，封堆对它们无效。设 768MB：正常负载碰不到（压力实测存活堆 454MB，GC 目标
//     按 1.5 倍约 680MB），一旦真的越过它，Go 会连续 GC 把增长压回去，而不是让进程一路涨。
//
// 上限设得太低会适得其反：GC 目标取 min(上限, 存活×1.5)，上限低于存活堆时每一项分配都触发
// GC。最初的 256MB 就是这样——低于实测存活堆 454MB，正好卡在历史管理这类重交互路径上。
const (
	gcPercent      = 50
	heapLimitBytes = 768 << 20
)

// applyMemoryPolicy 装进程级的内存策略（main 里调一次，早于装配）。
//
// 放在 main 而不是 run：run 会被端到端测试直接调用，把进程级运行时参数写在测试路径上
// 会污染同进程的其它用例。
//
// 两个旋钮各自尊重环境变量：GOMEMLIMIT 或 GOGC 显式给出时，对应的那个不覆盖——那是用户或
// 部署方的口径。判断只能看环境变量：运行时的「未设置」与 GOMEMLIMIT=off 读数相同（都是
// math.MaxInt64），分不出意图；GOGC 同理，SetGCPercent 的返回值也分不出「默认 100」与
// 「读到显式 100」。
func applyMemoryPolicy() {
	if os.Getenv("GOMEMLIMIT") == "" {
		debug.SetMemoryLimit(heapLimitBytes)
	}
	if os.Getenv("GOGC") == "" {
		debug.SetGCPercent(gcPercent)
	}
}
