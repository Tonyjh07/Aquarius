package conversation

// usage_test.go 用量分项的累加、命中率与落盘兼容（D308）。

import (
	"encoding/json"
	"testing"
)

// TestUsageAdd 分项必须一起累加：漏一项就是账面上少一笔。
func TestUsageAdd(t *testing.T) {
	a := Usage{InputTokens: 100, OutputTokens: 10, CostUSD: 0.5}
	b := Usage{InputTokens: 200, OutputTokens: 20, CostUSD: 1.5,
		CacheRead: 150, CacheWrite: 30, Thinking: 12, Safety: 3}
	got := a.Add(b)
	want := Usage{InputTokens: 300, OutputTokens: 30, CostUSD: 2,
		CacheRead: 150, CacheWrite: 30, Thinking: 12, Safety: 3}
	if got != want {
		t.Fatalf("Add = %+v, want %+v", got, want)
	}
	if again := got.Add(Usage{}); again != got {
		t.Fatalf("加零值应不变：%+v", again)
	}
}

// TestUsageCacheHitRatio 总输入为 0 时不出 NaN；正常情形按命中量除以总输入。
func TestUsageCacheHitRatio(t *testing.T) {
	cases := []struct {
		u    Usage
		want float64
	}{
		{Usage{}, 0},
		{Usage{InputTokens: 0, CacheRead: 50}, 0},
		{Usage{InputTokens: 1000, CacheRead: 0}, 0},
		{Usage{InputTokens: 1000, CacheRead: 250}, 0.25},
		{Usage{InputTokens: 1000, CacheRead: 1000}, 1},
	}
	for _, c := range cases {
		if got := c.u.CacheHitRatio(); got != c.want {
			t.Fatalf("%+v 命中率 = %v, want %v", c.u, got, c.want)
		}
	}
}

// TestUsageJSONBackCompat 旧会话文件的 usage 只有三项，解出来分项为零值；
// 新写出的零值分项不落键（omitempty），旧读者仍能解析。
func TestUsageJSONBackCompat(t *testing.T) {
	var old Usage
	if err := json.Unmarshal([]byte(`{"input_tokens":12,"output_tokens":34,"cost_usd":0.01}`), &old); err != nil {
		t.Fatalf("解旧格式: %v", err)
	}
	if old.InputTokens != 12 || old.OutputTokens != 34 || old.CacheRead != 0 || old.Thinking != 0 {
		t.Fatalf("旧格式解出来 = %+v", old)
	}

	// 三项为零值 → 字节与旧版本一致（多一个键就会让快照比对全线漂移）
	b, err := json.Marshal(Usage{InputTokens: 12, OutputTokens: 34})
	if err != nil {
		t.Fatalf("序列化: %v", err)
	}
	if want := `{"input_tokens":12,"output_tokens":34,"cost_usd":0}`; string(b) != want {
		t.Fatalf("零值分项不应落键：%s", b)
	}

	// 有值才写，且读回来一致
	b, err = json.Marshal(Usage{InputTokens: 12, CacheRead: 8, Thinking: 2})
	if err != nil {
		t.Fatalf("序列化: %v", err)
	}
	var back Usage
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatalf("回读: %v", err)
	}
	if back.CacheRead != 8 || back.Thinking != 2 {
		t.Fatalf("分项没往返：%+v", back)
	}
}
