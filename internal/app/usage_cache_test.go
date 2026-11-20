package app

// usage_cache_test.go 用量分项的展示面（D308）：/usage 的一行、/cost 的命中率后缀。
//
// 断言一律从 i18n 模板里取前后缀，不写字面量——文案改了用例跟着走，也不触 CJK 闸门。

import (
	"context"
	"strings"
	"testing"

	"github.com/Tonyjh07/Aquarius/internal/domain/conversation"
	"github.com/Tonyjh07/Aquarius/internal/i18n"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

// tmplPrefix 返回占位符之前的文案前缀，用于判断该文案是否出现过。
func tmplPrefix(key, slot string) string {
	return strings.Split(i18n.T(key), "{"+slot+"}")[0]
}

// appendCachedAssistant 往会话末尾挂一个有缓存命中的助手节点。
func appendCachedAssistant(t *testing.T, s *Session, u conversation.Usage) {
	t.Helper()
	m := conversation.Message{
		ID:        "cached-1",
		Parent:    s.cur.Head,
		Role:      conversation.RoleAssistant,
		Content:   []conversation.Part{{Kind: conversation.PartText, Text: "ok"}},
		Usage:     u,
		CreatedAt: testTime,
	}
	if err := s.cur.AppendCommitted(m); err != nil {
		t.Fatalf("append: %v", err)
	}
}

// TestUsageShowsCacheLine 端点报过缓存时 /usage 多一行，命中率按总输入算。
func TestUsageShowsCacheLine(t *testing.T) {
	s, _ := newLoreSession(t, "hello")
	appendCachedAssistant(t, s, conversation.Usage{
		InputTokens: 1000, OutputTokens: 100, CacheRead: 750, CacheWrite: 20, Thinking: 40,
	})
	out, err := s.execUsage(context.Background(), port.Command{Name: "usage"})
	if err != nil {
		t.Fatalf("execUsage: %v", err)
	}
	want := i18n.Tf("session.usage.cache", "read", "750", "write", "20", "pct", "75")
	if !strings.Contains(out, want) {
		t.Fatalf("/usage 缺缓存一行（找 %q）:\n%s", want, out)
	}
	// 推理与审核在同一行里；审核为 0 也照写，两个分项是一组。
	split := i18n.Tf("session.usage.split", "thinking", "40", "safety", "0")
	if !strings.Contains(out, split) {
		t.Fatalf("/usage 缺分项一行（找 %q）:\n%s", split, out)
	}
}

// TestUsageOmitsCacheLineWhenUnreported 端点没报分项时不出这两行——零值不是「命中 0」。
func TestUsageOmitsCacheLineWhenUnreported(t *testing.T) {
	s, _ := newLoreSession(t, "hello")
	appendCachedAssistant(t, s, conversation.Usage{InputTokens: 1000, OutputTokens: 100})
	out, err := s.execUsage(context.Background(), port.Command{Name: "usage"})
	if err != nil {
		t.Fatalf("execUsage: %v", err)
	}
	if p := tmplPrefix("session.usage.cache", "read"); strings.Contains(out, p) {
		t.Fatalf("没报分项却打了缓存一行（找 %q）:\n%s", p, out)
	}
	if p := tmplPrefix("session.usage.split", "thinking"); strings.Contains(out, p) {
		t.Fatalf("没报分项却打了分项一行（找 %q）:\n%s", p, out)
	}
}

// TestCostRowShowsCache 分组账在真有命中时给命中率；没有命中的组不加那半句。
func TestCostRowShowsCache(t *testing.T) {
	hit := conversation.Message{
		Role:  conversation.RoleAssistant,
		Model: "m",
		Usage: conversation.Usage{InputTokens: 800, OutputTokens: 40, CacheRead: 600},
	}
	plain := conversation.Message{
		Role:  conversation.RoleAssistant,
		Model: "p",
		Usage: conversation.Usage{InputTokens: 200, OutputTokens: 10},
	}
	keys, tallies := costByModel([]conversation.Message{hit, plain})
	out := renderCostRows("head", keys, tallies)
	if !strings.Contains(out, i18n.Tf("session.cost.cache", "pct", "75")) {
		t.Fatalf("有命中的组缺命中率:\n%s", out)
	}
	if n := strings.Count(out, tmplPrefix("session.cost.cache", "pct")); n != 1 {
		t.Fatalf("命中率出现 %d 次，want 1（整组 1000 输入里 600 命中，另一组没有）:\n%s", n, out)
	}
}
