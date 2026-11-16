package app

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/Tonyjh07/Aquarius/internal/domain/conversation"
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
	tally.add(usageMsg("m", 10, 5, 0.01, time.Now()))
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
	tally.add(usageMsg("m", 10, 5, 0, time.Now()))
	if tally.uncosted != 1 {
		t.Fatalf("uncosted = %d, want 1", tally.uncosted)
	}
}

// TestCostByDaySplitsDays 跨两天各一句 → 两行，最近的一天在前。
func TestCostByDaySplitsDays(t *testing.T) {
	now := time.Now()
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
	now := time.Now()
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
