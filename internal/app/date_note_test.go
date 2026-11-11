package app

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/Tonyjh07/Aquarius/internal/domain/conversation"
)

// at 造一个本机时区的时间点（日期按本机算，用 UTC 造会在跨时区跑测试时差一天）。
func at(month time.Month, day, hour int) time.Time {
	return time.Date(2026, month, day, hour, 0, 0, 0, time.Local)
}

// appendAt 按指定时间往树上追加一个节点（domain 的 Append 用 time.Now，这里要钉死日期）。
func appendAt(t *testing.T, c *conversation.Conversation, role conversation.Role, text string, when time.Time) {
	t.Helper()
	m := conversation.Message{
		ID:        conversation.NewMessageID(),
		Parent:    c.Head,
		Role:      role,
		Content:   []conversation.Part{{Kind: conversation.PartText, Text: text}},
		CreatedAt: when,
	}
	if err := c.AppendCommitted(m); err != nil {
		t.Fatalf("append %s: %v", role, err)
	}
}

// datedConv 造一个「创建于 11-09 晚」的会话：persona 首节点 + 一问一答，同一晚。
func datedConv(t *testing.T) *conversation.Conversation {
	t.Helper()
	c := conversation.New(conversation.ID("conv-date"), "跨日期")
	appendAt(t, c, conversation.RoleSystem, "persona, current date: 2026-11-09", at(11, 9, 21))
	appendAt(t, c, conversation.RoleUser, "hi", at(11, 9, 21))
	appendAt(t, c, conversation.RoleAssistant, "hello", at(11, 9, 22))
	return c
}

// dateAgent 造一个时钟钉在 now 的 Agent（带收集器 Presenter）。
func dateAgent(t *testing.T, now time.Time, rec *recorder) *Agent {
	t.Helper()
	a, err := New(Deps{
		LLM:   &scriptLLM{t: t},
		UI:    rec,
		IDs:   &seqIDs{},
		Clock: fixedClock{now},
	}, Config{Model: "test-model"})
	if err != nil {
		t.Fatalf("new agent: %v", err)
	}
	return a
}

// headDate 当前 Head 的时间（判断有没有落节点）。
func headDate(c *conversation.Conversation) time.Time {
	path := c.Path()
	return path[len(path)-1].CreatedAt
}

// TestNoteDateChangeWithinOneDay 同一天不落节点：这条说明只在日期真的变了时出现，
// 不是每句话前都补一句时间戳。
func TestNoteDateChangeWithinOneDay(t *testing.T) {
	c := datedConv(t)
	rec := &recorder{}
	a := dateAgent(t, at(11, 9, 23), rec)
	before := c.Head

	a.noteDateChange(context.Background(), c)

	if c.Head != before {
		t.Fatalf("同一天落了节点: head %q -> %q", before, c.Head)
	}
	if len(rec.events) != 0 {
		t.Fatalf("同一天发了事件: %v", eventNames(rec.events))
	}
}

// TestNoteDateChangeCrossesMidnight 跨过午夜：落一条注入节点，挂在上一条消息之后，
// 内容给出新日期并点明旧日期，来源是日期、触发为否（它不该自己起一轮）。
func TestNoteDateChangeCrossesMidnight(t *testing.T) {
	c := datedConv(t)
	rec := &recorder{}
	a := dateAgent(t, at(11, 10, 9), rec)
	prev := c.Head

	a.noteDateChange(context.Background(), c)

	if c.Head == prev {
		t.Fatal("跨日期没有落节点")
	}
	path := c.Path()
	node := path[len(path)-1]
	if node.Role != conversation.RoleInject || node.Parent != prev {
		t.Fatalf("节点 = %+v（角色/父节点不对）", node)
	}
	if node.Source != "clock" || node.Trust != conversation.TrustHost || node.Trigger {
		t.Fatalf("节点来源/信任/触发 = %q / %q / %v", node.Source, node.Trust, node.Trigger)
	}
	text := node.Content[0].Text
	if !strings.Contains(text, "2026-11-10") || !strings.Contains(text, "2026-11-09") {
		t.Fatalf("正文缺新/旧日期: %q", text)
	}
	if names := eventNames(rec.events); len(names) != 1 || names[0] != "history" {
		t.Fatalf("事件 = %v, want 仅一条 history（补记录，不当定稿广播）", names)
	}
}

// TestNoteDateChangeOncePerDay 一天只一条：落完之后 Head 就是今天，再调一次不该再落。
func TestNoteDateChangeOncePerDay(t *testing.T) {
	c := datedConv(t)
	rec := &recorder{}
	a := dateAgent(t, at(11, 10, 9), rec)

	a.noteDateChange(context.Background(), c)
	after := c.Head
	a.noteDateChange(context.Background(), c)

	if c.Head != after {
		t.Fatalf("同一天落了第二条: head %q -> %q", after, c.Head)
	}
	if names := eventNames(rec.events); len(names) != 1 {
		t.Fatalf("事件 = %v, want 1", names)
	}
}

// TestNoteDateChangePerNewDay 第二天再开口又是新的一天：再落一条。
func TestNoteDateChangePerNewDay(t *testing.T) {
	c := datedConv(t)
	a := dateAgent(t, at(11, 10, 9), &recorder{})
	a.noteDateChange(context.Background(), c)
	appendAt(t, c, conversation.RoleUser, "d2", at(11, 10, 9))

	a2 := dateAgent(t, at(11, 12, 8), &recorder{})
	a2.noteDateChange(context.Background(), c)

	path := c.Path()
	var notes int
	for _, m := range path {
		if m.Role == conversation.RoleInject && m.Source == "clock" {
			notes++
		}
	}
	if notes != 2 {
		t.Fatalf("日期说明 = %d 条, want 2", notes)
	}
}

// TestNoteDateChangeSkipsZeroTimeHead 没有时间的 Head（未提交缓冲一类的合成节点）不当成
// 日期变化：拿零值去比，每句话前都会凭空多一条说明。
func TestNoteDateChangeSkipsZeroTimeHead(t *testing.T) {
	c := datedConv(t)
	appendAt(t, c, conversation.RoleAssistant, "no time", time.Time{})
	rec := &recorder{}
	a := dateAgent(t, at(11, 10, 9), rec)

	a.noteDateChange(context.Background(), c)

	if names := eventNames(rec.events); len(names) != 0 {
		t.Fatalf("事件 = %v, want 无", names)
	}
	if !headDate(c).IsZero() {
		t.Fatal("不该落节点")
	}
}
