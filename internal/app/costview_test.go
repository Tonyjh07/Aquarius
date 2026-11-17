package app

import (
	"context"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Tonyjh07/Aquarius/internal/domain/conversation"
	"github.com/Tonyjh07/Aquarius/internal/i18n"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

// usageMsg 造一个带用量的 assistant 节点（只填视图读的字段）。
func usageMsg(model string, in, out int, usd float64, at time.Time) conversation.Message {
	return conversation.Message{
		Role:      conversation.RoleAssistant,
		Model:     model,
		Usage:     conversation.Usage{InputTokens: in, OutputTokens: out, CostUSD: usd},
		CreatedAt: at,
	}
}

// TestCostTallySkipsEmptyUsage 没有 token 的节点不算一次调用（root/system/user 都在树里）。
func TestCostTallySkipsEmptyUsage(t *testing.T) {
	var tally costTally
	tally.add(conversation.Message{Role: conversation.RoleUser})
	tally.add(usageMsg("m", 10, 5, 0.01, testTime))
	if tally.calls != 1 || tally.in != 10 || tally.out != 5 {
		t.Fatalf("tally = %+v, want one call with 10/5", tally)
	}
	if tally.uncosted != 0 {
		t.Fatalf("uncosted = %d, want 0 when a price was converted", tally.uncosted)
	}
}

// TestCostTallyMarksUncosted 有 token 但没折算成钱：计入 uncosted，不能当成「免费」。
func TestCostTallyMarksUncosted(t *testing.T) {
	var tally costTally
	tally.add(usageMsg("m", 10, 5, 0, testTime))
	if tally.uncosted != 1 {
		t.Fatalf("uncosted = %d, want 1", tally.uncosted)
	}
}

// TestCostByDaySplitsDays 跨两天各一句 → 两行，最近的一天在前。
func TestCostByDaySplitsDays(t *testing.T) {
	now := testTime
	msgs := []conversation.Message{
		usageMsg("m", 100, 10, 0.02, now.AddDate(0, 0, -1)),
		usageMsg("m", 200, 20, 0.03, now),
	}
	keys, tallies := costByDay(msgs)
	if len(keys) != 2 {
		t.Fatalf("keys = %v, want two days", keys)
	}
	if keys[0] != now.Local().Format("2006-01-02") {
		t.Fatalf("first key = %s, want today", keys[0])
	}
	if got := tallies[keys[0]]; got.in != 200 {
		t.Fatalf("today tally = %+v, want in 200", got)
	}
}

// TestCostByModelGroups 按模型分组，花费高的在前；没有 model 字段的归到占位名。
func TestCostByModelGroups(t *testing.T) {
	now := testTime
	msgs := []conversation.Message{
		usageMsg("cheap", 1000, 100, 0.01, now),
		usageMsg("pricey", 1000, 100, 0.50, now),
		usageMsg("", 500, 50, 0.02, now),
	}
	keys, tallies := costByModel(msgs)
	if len(keys) != 3 || keys[0] != "pricey" {
		t.Fatalf("keys = %v, want pricey first", keys)
	}
	if tallies["?"] == nil {
		t.Fatalf("keys = %v, want a placeholder bucket for the empty model", keys)
	}
}

// TestFmtTokens k 折叠只在大数上生效：四位数原样，免得小账看起来像四舍五入过。
func TestFmtTokens(t *testing.T) {
	cases := map[int]string{0: "0", 999: "999", 9999: "9999", 10000: "10.0k", 12345: "12.3k"}
	for n, want := range cases {
		if got := fmtTokens(n); got != want {
			t.Fatalf("fmtTokens(%d) = %q, want %q", n, got, want)
		}
	}
}

// TestExecCostUsageErrors 多余的参数与未知视角都报用法。
func TestExecCostUsageErrors(t *testing.T) {
	s, _, _ := newTestSession(t, newMemStore())
	for _, args := range [][]string{{"nope"}, {"session", "model"}} {
		if _, err := s.execCost(context.Background(), port.Command{Args: args}); err == nil {
			t.Fatalf("args %v: want a usage error", args)
		}
	}
}

// TestExecCostSessionEmpty 空会话给一行零账，不是空字符串。
func TestExecCostSessionEmpty(t *testing.T) {
	s, _, _ := newTestSession(t, newMemStore())
	out, err := s.execCost(context.Background(), port.Command{})
	if err != nil {
		t.Fatalf("exec cost: %v", err)
	}
	if !strings.Contains(out, "$0.0000") {
		t.Fatalf("empty output = %q", out)
	}
}

// costConv 造一个带用量的会话（UpdatedAt 显式给，便于验 30 天窗口）。
func costConv(id conversation.ID, updated time.Time, msgs ...conversation.Message) *conversation.Conversation {
	c := conversation.New(id, string(id))
	for i, m := range msgs {
		if m.ID == "" {
			m.ID = conversation.MessageID(string(id) + "-n" + strconv.Itoa(i))
		}
		m.Parent = c.Head
		if err := c.AppendCommitted(m); err != nil {
			panic(err)
		}
	}
	// 追加会把 UpdatedAt 拨到当下，因此窗口用的那个时间必须在追加之后写回。
	c.UpdatedAt = updated
	return c
}

// TestCostAllMessagesSkipsStaleConversations 30 天窗口按 UpdatedAt 卡：更老的会话整份不读。
// 这条边界两侧都要验——只验「旧的被排除」会把阈值写成 0 天也放过。
func TestCostAllMessagesSkipsStaleConversations(t *testing.T) {
	store := newMemStore()
	store.convs["fresh"] = costConv("fresh", testTime.AddDate(0, 0, -1),
		usageMsg("m", 100, 10, 0.02, testTime.AddDate(0, 0, -1)))
	store.convs["stale"] = costConv("stale", testTime.AddDate(0, 0, -31),
		usageMsg("m", 900, 90, 5, testTime.AddDate(0, 0, -31)))
	s, _, _ := newTestSession(t, store)

	msgs, err := s.allMessages(context.Background())
	if err != nil {
		t.Fatalf("allMessages: %v", err)
	}
	var in int
	for _, m := range msgs {
		in += m.Usage.InputTokens
	}
	if in != 100 {
		t.Fatalf("in = %d, want only the fresh conversation's 100", in)
	}
}

// TestCostAllMessagesKeepsBoundary 刚好 30 天前仍然读：判据是 `Before`，不是 `<=`。
func TestCostAllMessagesKeepsBoundary(t *testing.T) {
	store := newMemStore()
	at := testTime.Add(-costSince)
	store.convs["edge"] = costConv("edge", at, usageMsg("m", 7, 3, 0.01, at))
	s, _, _ := newTestSession(t, store)

	msgs, err := s.allMessages(context.Background())
	if err != nil {
		t.Fatalf("allMessages: %v", err)
	}
	var in int
	for _, m := range msgs {
		in += m.Usage.InputTokens
	}
	if in != 7 {
		t.Fatalf("in = %d, want the boundary conversation kept", in)
	}
}

// TestExecCostModelAndDay 两个跨会话视角真跑一遍：/cost model 按花费降序，/cost day 按日期
// 降序且受窗口约束。此前只有 session 视角被调用过，这两条命令的渲染与筛选没有任何用例。
func TestExecCostModelAndDay(t *testing.T) {
	store := newMemStore()
	store.convs["a"] = costConv("a", testTime, usageMsg("cheap", 100, 10, 0.02, testTime))
	store.convs["b"] = costConv("b", testTime.AddDate(0, 0, -1),
		usageMsg("pricey", 100, 10, 0.50, testTime.AddDate(0, 0, -1)))
	store.convs["c"] = costConv("c", testTime.AddDate(0, 0, -31),
		usageMsg("gone", 100, 10, 9, testTime.AddDate(0, 0, -31)))
	s, _, _ := newTestSession(t, store)
	ctx := context.Background()

	out, err := s.execCost(ctx, port.Command{Args: []string{"model"}})
	if err != nil {
		t.Fatalf("cost model: %v", err)
	}
	if !strings.Contains(out, "pricey") || !strings.Contains(out, "$0.5000") {
		t.Fatalf("model view = %q", out)
	}
	if i, j := strings.Index(out, "pricey"), strings.Index(out, "cheap"); i < 0 || j < 0 || i > j {
		t.Fatalf("model view 未按花费降序: %q", out)
	}
	if strings.Contains(out, "gone") {
		t.Fatalf("model view 读进了 30 天前的会话: %q", out)
	}

	out, err = s.execCost(ctx, port.Command{Args: []string{"day"}})
	if err != nil {
		t.Fatalf("cost day: %v", err)
	}
	today := testTime.Local().Format("2006-01-02")
	yesterday := testTime.AddDate(0, 0, -1).Local().Format("2006-01-02")
	if !strings.Contains(out, today) || !strings.Contains(out, yesterday) {
		t.Fatalf("day view = %q, want both days", out)
	}
	if strings.Index(out, today) > strings.Index(out, yesterday) {
		t.Fatalf("day view 未按日期降序: %q", out)
	}
}

// TestCostByDayTruncates 超过 costDays 个日期只留最近的：截断发生在排序之后，留下的必须是
// 最近的那些天，而不是 map 迭代先遇到的那些。
func TestCostByDayTruncates(t *testing.T) {
	var msgs []conversation.Message
	for i := 0; i < costDays+3; i++ {
		at := testTime.AddDate(0, 0, -i)
		msgs = append(msgs, usageMsg("m", 10, 1, 0.01, at))
	}
	keys, _ := costByDay(msgs)
	if len(keys) != costDays {
		t.Fatalf("keys = %d, want %d", len(keys), costDays)
	}
	if keys[0] != testTime.Local().Format("2006-01-02") {
		t.Fatalf("first key = %s, want today", keys[0])
	}
	if keys[len(keys)-1] != testTime.AddDate(0, 0, -(costDays-1)).Local().Format("2006-01-02") {
		t.Fatalf("last key = %s, want the %d-th day back", keys[len(keys)-1], costDays-1)
	}
}

// TestCostUncostedRendersPlaceholder 整组没有单价时金额位写「未折算」，不印 $0.0000。
func TestCostUncostedRendersPlaceholder(t *testing.T) {
	keys, tallies := costByModel([]conversation.Message{usageMsg("free?", 10, 5, 0, testTime)})
	out := renderCostRows("head", keys, tallies)
	if !strings.Contains(out, i18n.T("session.cost.no_price")) {
		t.Fatalf("uncosted row = %q, want the placeholder", out)
	}
	if strings.Contains(out, "$0.0000") {
		t.Fatalf("uncosted row 印了 $0.0000，会被读成免费: %q", out)
	}
}
