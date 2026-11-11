package port

import (
	"context"
	"time"
)

// 决策模型端口（D281/D290）：把「这次要不要打扰用户」交给一次前向的概率，而不是
// 让模型生成一段话。输出空间封闭——被注入也只能把概率推向错的一边，吐不出「你该
// 做什么」。
//
// 与 Confirmer 的分工：默认确认器是终端/界面，装饰器决定**要不要走到它**。端口只给
// 分数，不给结论；三档分流在装饰器里按配置算，换模型重标阈值时不必动适配器。

// DecideRequest 一次判定的输入。state 由适配器拼成固定六行（origin / tool / op /
// target / risk / task），这里只给原文，怎么排版是适配器的事。
type DecideRequest struct {
	Origin string // toolrun | acpgate
	Tool   string
	Op     string // 空 = 不涉及文件操作
	Target string
	Risk   string // 调用方粗判：low / medium / high
	Task   string // 外部代理声称的任务，已按 decide.task_chars 截断
}

// Decision 一次判定的输出：三题的分数，原样交回，不在这里做取舍。
//
// ShouldAllow 只进审计、不参与判定——实测它与 NeedsHuman 的 Pearson 系数 −0.93，
// 留着是为阴影期后重标定。
type Decision struct {
	ShouldAllow float64
	Risk        float64 // score 三档的期望值，值域 0–2
	NeedsHuman  float64
	Latency     time.Duration
	Model       string
}

// Decider 问一次决策模型。实现方负责「问一次、解析、返回」，不设超时（超时由装饰器
// 按 decide.timeout_ms 设在 ctx 上），也不重试——重试只会把一次超时拖成两次。
//
// error 表示**没判成**（超时、非 2xx、JSON 不合形状、题型不被认），调用方一律交回
// 原确认。error 绝不等价于 Allow。
type Decider interface {
	Decide(ctx context.Context, req DecideRequest) (Decision, error)
}
