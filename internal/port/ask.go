package port

import "context"

// 提问端口（D184）：内核反向问人的第二条通道，与 Confirmer 并列。
// 确认面答的是「准不准做」（布尔 + 原因），提问面答的是「选哪个」
// （多题 × 多选 × 自由文本）——语义不重叠，故不合并成一个接口。

// AskOption 一个问题的一个选项。
type AskOption struct {
	Label       string
	Description string
}

// AskQuestion 一道题。
type AskQuestion struct {
	Header   string      // 短标题（页签与进度段用）
	Question string      // 题干
	Options  []AskOption // 0–9 项；0 项 = 纯自由输入
	Multiple bool        // 多选
}

// AskRequest 一次提问（1–4 题）。「自己输入」项由 UI 自动追加，不在此列。
type AskRequest struct {
	Questions []AskQuestion
}

// AskAnswer 提问结果。Answers 与 Questions 等长，逐题给选中项
// （自由输入的原样成一个元素）；未答 = 空切片。
type AskAnswer struct {
	Answers   [][]string
	Dismissed bool // 「忽略」= 放弃全部（不是跳过本题）
	// Away 无人值守：提问时用户不在（-yes / 无人值守开关 / 非交互前端）。
	// 与 Dismissed 分开：前者是事实（没人可问），后者是决定（用户选了「你自己看着办」），
	// 回填给模型的文案与期望都不同。
	Away bool
}

// Asker 向用户提问（ask_user 工具的实现面）。
// ctx 取消 = 用户中断本轮（GUI 的 Esc / 停止键），实现方应返回 ctx.Err()
// 而不是 Dismissed——「我不想现在答」与「别问了，你自己看着办」是两件事。
type Asker interface {
	Ask(ctx context.Context, req AskRequest) (AskAnswer, error)
}
