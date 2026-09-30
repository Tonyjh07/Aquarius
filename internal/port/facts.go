package port

// SessionFacts 会话展示事实快照（D82/§7.6，前置 B）：logo tooltip 的只读数据面。
// 值语义、只读——由 app 侧在会话 goroutine 计算后原子发布（发布点同树快照 D80：
// 构造完成 + 每次 Handle 返回前），UI 事件循环 goroutine 无锁读（§15.5 同口径）。
type SessionFacts struct {
	// Title 当前会话标题（首条消息摘要，缺省「新会话」）。
	Title string
	// ConvID 当前会话 ID（展示用前缀；string 口径——不参与命令回传，见 §7.6）。
	ConvID string
	// CtxTokens 当前上下文占用 tokens（②精确/③估算，三级计数链 D26，与 /usage 同源）。
	CtxTokens int
	// CtxExact CtxTokens 是否精确计数（适配器 TokenCounter 生效）。
	CtxExact bool
	// CtxMax 上下文预算 tokens（分母 = limits.max_context_tokens，Q6）。
	CtxMax int
	// SumIn / SumOut 当前 Path 内累计实测 tokens（服务端返回）。
	SumIn  int
	SumOut int
	// LastIn / LastOut 最近一次实测 tokens（均为零 = 尚无生成）。
	LastIn  int
	LastOut int
}
