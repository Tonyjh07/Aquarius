package app

// date_note.go 跨日期的日期说明（D292）。
//
// persona 快照里的 `@env.date` 是会话创建那天（D37），会话跨过午夜之后它就是过期事实：
// 模型第二天接着聊，读到的仍是昨天。这里在用户开口之前落一条注入节点把日期改正过来。
//
// 为什么落树、而不是装配时插一条合成 system：这条说明要能在转写区里被看见——和工具注入
// 同一个呈现面（独立一块、头部写来源与触发状态）。装配期合成的东西对用户不可见，用户就
// 无从判断模型到底知不知道今天几号。

import (
	"context"
	"fmt"
	"time"

	"github.com/Tonyjh07/Aquarius/internal/domain/conversation"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

// dateNoteSource 日期说明节点的来源（展示用）：不匹配触发默认表里的任何前缀，所以它
// 只落地、不会自己起一轮。
const dateNoteSource = "clock"

// localDay 节点所在的本机日期（`YYYY-MM-DD`）；零值时间返回空串（未提交的 Turn 缓冲里
// 的合成节点没有时间，不能拿它当"日期变了"的依据）。
func localDay(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Local().Format("2006-01-02")
}

// noteDateChange 在当前 Head 的日期与今天不同时，落一条日期说明节点（挂在 Head 上）。
//
// 判据只看「Head 是哪天的」：一天最多一条，第二天再开口自然会再落一条。失败一律吞掉——
// 这条是辅助信息，为它挡下用户这句话或整轮生成不划算；树真出问题时后续那次 Append 会报。
func (a *Agent) noteDateChange(ctx context.Context, c *conversation.Conversation) {
	today := localDay(a.clock.Now())
	path := c.Path()
	if today == "" || len(path) == 0 {
		return
	}
	lastDay := localDay(path[len(path)-1].CreatedAt)
	if lastDay == "" || lastDay == today {
		return
	}
	node := conversation.Message{
		ID:        a.ids.MessageID(),
		Parent:    c.Head,
		Role:      conversation.RoleInject,
		Content:   []conversation.Part{{Kind: conversation.PartText, Text: fmt.Sprintf(dateChangeNoteFmt, today, lastDay)}},
		Source:    dateNoteSource,
		Trust:     conversation.TrustHost,
		CreatedAt: a.clock.Now(),
	}
	if err := c.AppendCommitted(node); err != nil {
		return
	}
	// HistoryEvent 而不是 CommittedEvent：它只是往转写区补一块记录，不是一次「有节点定稿」
	// 的信号（输出装饰器不响应，与注入排空同一个口径）。
	_ = a.ui.Emit(ctx, port.HistoryEvent{Message: node})
}
