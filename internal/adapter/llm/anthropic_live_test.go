package llm

// anthropic_live_test.go 三协议兼容回归（plan.md「DeepSeek 全家桶」的测试资产条目）：
// DeepSeek 把自家模型挂在了 Anthropic Messages 协议端点（/anthropic/v1/messages）上，
// 把它当「第二家真后端」打一遍 anthropic 适配器，验证对非 Anthropic 厂商的兼容性。
// 与本包其他测试不同，这条**起真实网络**——所以默认跳过，只有显式给 key 才跑：
//
//	AQUARIUS_ANTHROPIC_LIVE_KEY      必填（DeepSeek API key，或任何 Messages 兼容后端的 key）
//	AQUARIUS_ANTHROPIC_LIVE_BASE_URL 选填（默认 DeepSeek 的 https://api.deepseek.com/anthropic/v1）
//	AQUARIUS_ANTHROPIC_LIVE_MODEL    选填（默认 deepseek-chat）
//
// 断言刻意保持最小：流式收得到正文、usage 分项齐全、错误分类不炸。别的厂商行为差异
// （模型名、端点细节）都留给环境变量，不在断言里钉死。

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/Tonyjh07/Aquarius/internal/port"
)

const anthropicLiveDefaultBase = "https://api.deepseek.com/anthropic/v1"

func TestAnthropicLiveBackend(t *testing.T) {
	key := os.Getenv("AQUARIUS_ANTHROPIC_LIVE_KEY")
	if key == "" {
		t.Skip("未设 AQUARIUS_ANTHROPIC_LIVE_KEY，跳过真后端兼容回归（不起真实网络）")
	}
	base := os.Getenv("AQUARIUS_ANTHROPIC_LIVE_BASE_URL")
	if base == "" {
		base = anthropicLiveDefaultBase
	}
	model := os.Getenv("AQUARIUS_ANTHROPIC_LIVE_MODEL")
	if model == "" {
		model = "deepseek-chat"
	}

	c := anthropicClient(t, base, key)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	s, err := c.Generate(ctx, port.GenerateRequest{
		Model: model,
		Messages: []port.PromptMessage{{
			Role:    port.PromptUser,
			Content: []port.PromptPart{{Kind: port.PromptText, Text: "Reply with exactly: ok"}},
		}},
	})
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	defer s.Close()

	text, _, _, _, usage := collectResponses(t, s)
	if text == "" {
		t.Fatal("真后端应流出非空正文")
	}
	if usage == nil || usage.InputTokens <= 0 || usage.OutputTokens <= 0 {
		t.Fatalf("usage 应有分项: %+v", usage)
	}
}
