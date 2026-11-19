package llm

// usage_test.go 三家协议的用量分项映射（D308）
//
// 每个用例只喂一段 usage 载荷，断言落到 conversation.Usage 的哪几个字段。
// 键名差异是这里的主体：缓存命中 OpenAI 报 details.cached_tokens、DeepSeek 报
// prompt_cache_hit_tokens；推理 token 有 reasoning_tokens 与 thinking_tokens 两种写法。

import (
	"testing"

	"github.com/Tonyjh07/Aquarius/internal/domain/conversation"
)

// TestChatUsageDetails OpenAI 兼容 chat：两种键名各测一次，确认都能取到。
func TestChatUsageDetails(t *testing.T) {
	cases := []struct {
		name string
		body string
		want conversation.Usage
	}{
		{
			name: "OpenAI details 形态",
			body: `{"prompt_tokens":1000,"completion_tokens":200,
				"prompt_tokens_details":{"cached_tokens":800},
				"completion_tokens_details":{"reasoning_tokens":150}}`,
			want: conversation.Usage{InputTokens: 1000, OutputTokens: 200, CacheRead: 800, Thinking: 150},
		},
		{
			name: "DeepSeek 平铺形态",
			body: `{"prompt_tokens":1000,"completion_tokens":200,
				"prompt_cache_hit_tokens":600,"prompt_cache_miss_tokens":400,
				"output_tokens_details":{"thinking_tokens":120},"safety_tokens":7}`,
			want: conversation.Usage{InputTokens: 1000, OutputTokens: 200, CacheRead: 600, Thinking: 120, Safety: 7},
		},
		{
			name: "只有三项的老端点",
			body: `{"prompt_tokens":500,"completion_tokens":60}`,
			want: conversation.Usage{InputTokens: 500, OutputTokens: 60},
		},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			d, ok, err := decodeChatPayload(`{"choices":[],"usage":` + c.body + `}`)
			if err != nil {
				t.Fatalf("decode: %v", err)
			}
			if !ok || d.Usage == nil {
				t.Fatalf("应给出用量分片：ok=%v usage=%+v", ok, d.Usage)
			}
			if *d.Usage != c.want {
				t.Fatalf("usage = %+v, want %+v", *d.Usage, c.want)
			}
		})
	}
}

// TestResponsesUsageDetails Responses：缓存命中在 input_tokens_details，推理在
// output_tokens_details；input_tokens 本来就含命中量，不做折算。
func TestResponsesUsageDetails(t *testing.T) {
	u := responsesUsage{InputTokens: 1200, OutputTokens: 300}
	u.InputTokensDetails = &struct {
		CachedTokens int `json:"cached_tokens"`
	}{CachedTokens: 900}
	u.OutputTokensDetails = &struct {
		ReasoningTokens int `json:"reasoning_tokens"`
	}{ReasoningTokens: 180}

	got := u.toUsage()
	want := conversation.Usage{InputTokens: 1200, OutputTokens: 300, CacheRead: 900, Thinking: 180}
	if got != want {
		t.Fatalf("usage = %+v, want %+v", got, want)
	}
	if pct := got.CacheHitRatio(); pct < 0.749 || pct > 0.751 {
		t.Fatalf("命中率 = %v, want 0.75", pct)
	}
}

// TestAnthropicUsageCache Anthropic 把输入拆三段报。断言 InputTokens 被折成总输入
// （三段之和），否则「命中率 = CacheRead / InputTokens」在两家协议上算出来的口径不一致。
func TestAnthropicUsageCache(t *testing.T) {
	s := &stream{}
	st := &anthropicStreamState{}
	start := `{"type":"message_start","message":{"id":"m","usage":{"input_tokens":10,` +
		`"cache_read_input_tokens":90,"cache_creation_input_tokens":5,"output_tokens":1}}}`
	if _, _, err := decodeAnthropicEvent(s, st, start); err != nil {
		t.Fatalf("message_start: %v", err)
	}
	if _, _, err := decodeAnthropicEvent(s, st, `{"type":"message_delta","usage":{"output_tokens":42}}`); err != nil {
		t.Fatalf("message_delta: %v", err)
	}
	d, _, err := decodeAnthropicEvent(s, st, `{"type":"message_stop"}`)
	if err != nil {
		t.Fatalf("message_stop: %v", err)
	}
	if d.Usage == nil {
		t.Fatal("message_stop 应给出用量")
	}
	want := conversation.Usage{InputTokens: 105, OutputTokens: 42, CacheRead: 90, CacheWrite: 5}
	if *d.Usage != want {
		t.Fatalf("usage = %+v, want %+v", *d.Usage, want)
	}
}

// TestAnthropicUsageWithoutCache 没开缓存的响应：InputTokens 就是 input_tokens 原值。
func TestAnthropicUsageWithoutCache(t *testing.T) {
	s := &stream{}
	st := &anthropicStreamState{}
	start := `{"type":"message_start","message":{"id":"m","usage":{"input_tokens":11,"output_tokens":1}}}`
	if _, _, err := decodeAnthropicEvent(s, st, start); err != nil {
		t.Fatalf("message_start: %v", err)
	}
	d, _, err := decodeAnthropicEvent(s, st, `{"type":"message_stop"}`)
	if err != nil {
		t.Fatalf("message_stop: %v", err)
	}
	if d.Usage == nil || d.Usage.InputTokens != 11 || d.Usage.CacheRead != 0 || d.Usage.CacheWrite != 0 {
		t.Fatalf("usage = %+v, want input=11 无缓存分项", d.Usage)
	}
}
