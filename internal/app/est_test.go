package app

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Tonyjh07/Aquarius/internal/domain/tool"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

// TestEstimateTokensCharsPerToken 分语种字符估算：ASCII÷4、CJK÷1.5、其他÷2。
func TestEstimateTokensCharsPerToken(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want int
	}{
		{"空串", "", 0},
		{"ASCII 40 字符", strings.Repeat("a", 40), 10},
		{"中文 15 字", strings.Repeat("好", 15), 10}, // 15/1.5
		{"混合", strings.Repeat("a", 20) + strings.Repeat("好", 6), 5 + 4},
		{"其他非 ASCII（西里尔）", strings.Repeat("ж", 4), 2},
		{"小数向上取整", "abc", 1}, // 0.75 → 1
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := estimateTokens(tc.in); got != tc.want {
				t.Fatalf("estimateTokens(%q) = %d, want %d", tc.in, got, tc.want)
			}
		})
	}
}

// TestEstimateRequestPayload 请求文本 + 每消息结构开销都计入。
func TestEstimateRequestPayload(t *testing.T) {
	req := port.GenerateRequest{
		Messages: []port.PromptMessage{
			{Role: "system", Content: []port.PromptPart{{Kind: "text", Text: strings.Repeat("s", 8)}}},
			{Role: "user", Content: []port.PromptPart{{Kind: "text", Text: strings.Repeat("u", 16)}}},
		},
	}
	payload, overhead := payloadOf(req)
	if overhead != 2*perMessageOverhead {
		t.Fatalf("overhead = %d, want %d", overhead, 2*perMessageOverhead)
	}
	if !strings.Contains(payload, strings.Repeat("s", 8)) || !strings.Contains(payload, strings.Repeat("u", 16)) {
		t.Fatalf("payload = %q", payload)
	}
	e := newEstimator(nil)
	n, exact := e.Estimate(context.Background(), req)
	if exact {
		t.Fatal("无 counter 不应精确")
	}
	if want := estimateTokens(payload) + overhead; n != want {
		t.Fatalf("n = %d, want %d（ratio 初始 1.0）", n, want)
	}
}

// fakeCounter 假精确计数器（②覆盖③）。
type fakeCounter struct {
	n   int
	err error
}

func (f *fakeCounter) CountTokens(context.Context, string) (int, error) { return f.n, f.err }

// TestEstimateCounterOverrides 精确计数实现即覆盖估算；报错回落估算。
func TestEstimateCounterOverrides(t *testing.T) {
	req := port.GenerateRequest{
		Messages: []port.PromptMessage{{Role: "user", Content: []port.PromptPart{{Kind: "text", Text: strings.Repeat("a", 400)}}}},
	}
	payload, overhead := payloadOf(req)

	e := newEstimator(&fakeCounter{n: 123})
	n, exact := e.Estimate(context.Background(), req)
	if !exact || n != 123+overhead {
		t.Fatalf("counter 路径 = %d/%v", n, exact)
	}
	_ = payload
}

// TestEstimateFallbackOnCounterError 计数器报错静默回落通用估算。
func TestEstimateFallbackOnCounterError(t *testing.T) {
	req := port.GenerateRequest{
		Messages: []port.PromptMessage{{Role: "user", Content: []port.PromptPart{{Kind: "text", Text: strings.Repeat("a", 40)}}}},
	}
	payload, overhead := payloadOf(req)
	e := newEstimator(&errCounter{})
	n, exact := e.Estimate(context.Background(), req)
	if exact {
		t.Fatal("计数失败不应标精确")
	}
	if want := estimateTokens(payload) + overhead; n != want {
		t.Fatalf("n = %d, want %d", n, want)
	}
}

type errCounter struct{}

var errBoom = errors.New("boom")

func (errCounter) CountTokens(context.Context, string) (int, error) { return 0, errBoom }

// TestCalibrate 校准比值：正常收敛、越界 clamp、无效输入不动。
func TestCalibrate(t *testing.T) {
	e := newEstimator(nil)

	e.Calibrate(100, 150)
	if e.ratio != 1.5 {
		t.Fatalf("ratio = %v, want 1.5", e.ratio)
	}
	e.Calibrate(100, 10) // 0.1 → clamp 0.5
	if e.ratio != calMin {
		t.Fatalf("ratio = %v, want %v", e.ratio, calMin)
	}
	e.Calibrate(100, 1000) // 10 → clamp 2
	if e.ratio != calMax {
		t.Fatalf("ratio = %v, want %v", e.ratio, calMax)
	}
	before := e.ratio
	e.Calibrate(0, 100)
	e.Calibrate(100, 0)
	if e.ratio != before {
		t.Fatal("无效输入不应改比值")
	}

	// 校准作用于估算结果。
	e2 := newEstimator(nil)
	e2.Calibrate(10, 20) // ×2
	req := port.GenerateRequest{
		Messages: []port.PromptMessage{{Role: "user", Content: []port.PromptPart{{Kind: "text", Text: strings.Repeat("a", 40)}}}},
	}
	raw, _ := newEstimator(nil).Estimate(context.Background(), req)
	got, _ := e2.Estimate(context.Background(), req)
	if got != raw*2 {
		t.Fatalf("校准后 = %d, want %d（raw %d ×2）", got, raw*2, raw)
	}
}

// TestTrimOldestOrphanTools P0-2 回归：任何形态下都不得留下"声明 assistant 已被裁掉"
// 的前导孤儿 tool——否则压缩失败兜底会把 Turn 打成服务端 400。
func TestTrimOldestOrphanTools(t *testing.T) {
	sys := port.PromptMessage{Role: "system", Content: []port.PromptPart{{Kind: "text", Text: "人格"}}}
	asstOf := func(n int) port.PromptMessage {
		return port.PromptMessage{
			Role:      "assistant",
			Content:   []port.PromptPart{{Kind: "text", Text: strings.Repeat("a", n)}},
			ToolCalls: []tool.Call{{ID: "c1", Name: "echo"}},
		}
	}
	toolOf := func(id string, n int) port.PromptMessage {
		return port.PromptMessage{
			Role:    "tool",
			CallID:  id,
			Content: []port.PromptPart{{Kind: "text", Text: strings.Repeat("t", n)}},
		}
	}
	noOrphan := func(t *testing.T, kept []port.PromptMessage) {
		t.Helper()
		// 首条非 system 不得是 tool（其声明者必在窗口内且位于其前）。
		for _, m := range kept {
			if m.Role == "system" {
				continue
			}
			if m.Role == "tool" {
				t.Fatalf("留下前导孤儿 tool: %+v", kept)
			}
			break
		}
	}

	e := newEstimator(nil)

	// A：assistant 巨大 → 主循环裁掉它后 est 立即达标 → 清理循环必须裁掉尾随 tool。
	kept, omitted := e.trimOldest(context.Background(),
		[]port.PromptMessage{sys, asstOf(1000), toolOf("c1", 100)}, 60)
	noOrphan(t, kept)
	if len(kept) != 1 || omitted != 2 {
		t.Fatalf("A: kept=%d omitted=%d, want 1/2（assistant+tool 都裁、仅剩 system）: %+v", len(kept), omitted, kept)
	}

	// B：target 极小、非 system 全是工具轮产物（工具循环开头的常态）——
	// 旧逻辑 nonSystem<=1 提前 break 会停在 [system, tool]（400）。
	kept, omitted = e.trimOldest(context.Background(),
		[]port.PromptMessage{sys, asstOf(100), toolOf("c1", 100), toolOf("c2", 100)}, 1)
	noOrphan(t, kept)
	if len(kept) != 1 {
		t.Fatalf("B: kept=%d, want 1（system-only 可发送）: %+v", len(kept), kept)
	}

	// C：est 一开始就低于 target，后置清理也必须移除孤儿（单 tool 打头）。
	kept, omitted = e.trimOldest(context.Background(),
		[]port.PromptMessage{sys, toolOf("c1", 100)}, 10000)
	noOrphan(t, kept)
	if len(kept) != 1 || omitted != 1 {
		t.Fatalf("C: kept=%d omitted=%d, want 1/1: %+v", len(kept), omitted, kept)
	}

	// D：assistant 与其 tool 结构成对保留时（未被裁）不得误伤。
	kept, _ = e.trimOldest(context.Background(),
		[]port.PromptMessage{sys, asstOf(10), toolOf("c1", 10)}, 100000)
	if len(kept) != 3 || kept[1].Role != "assistant" || kept[2].Role != "tool" {
		t.Fatalf("D: 不应改动: %+v", kept)
	}
}

// TestTrimOldest 保 leading system 与最近、丢中间；孤儿 tool 结果一并裁掉；至少留 1 条。
func TestTrimOldest(t *testing.T) {
	msg := func(role, text string) port.PromptMessage {
		return port.PromptMessage{Role: role, Content: []port.PromptPart{{Kind: "text", Text: text}}}
	}
	e := newEstimator(nil)

	sys := msg("system", "人格")
	// 每条 ~25 tokens（100 ASCII），target 60 → 只留得下 system + 最近 1 条。
	var msgs []port.PromptMessage
	msgs = append(msgs, sys)
	for i := 0; i < 6; i++ {
		msgs = append(msgs, msg("user", strings.Repeat("x", 100)))
	}
	kept, omitted := e.trimOldest(context.Background(), msgs, 60)
	if omitted == 0 {
		t.Fatal("应有裁剪")
	}
	if kept[0].Role != "system" {
		t.Fatalf("leading system 被裁: %+v", kept[0])
	}
	if len(kept) != 2 { // system + 1 条非 system
		t.Fatalf("kept = %d, want 2: %+v", len(kept), kept)
	}
	if kept[1].Content[0].Text != msgs[len(msgs)-1].Content[0].Text {
		t.Fatal("应保留最近一条")
	}

	// 孤儿 tool：system, assistant(calls), tool, user —— 裁掉 assistant 后 tool 必须连带裁掉。
	toolMsg := port.PromptMessage{Role: "tool", CallID: "c1", Content: []port.PromptPart{{Kind: "text", Text: strings.Repeat("t", 100)}}}
	asst := port.PromptMessage{
		Role:      "assistant",
		Content:   []port.PromptPart{{Kind: "text", Text: strings.Repeat("a", 100)}},
		ToolCalls: []tool.Call{{ID: "c1", Name: "echo"}},
	}
	msgs2 := []port.PromptMessage{sys, asst, toolMsg, msg("user", strings.Repeat("u", 100))}
	kept2, _ := e.trimOldest(context.Background(), msgs2, 60)
	for _, m := range kept2 {
		if m.Role == "tool" {
			t.Fatalf("孤儿 tool 未裁掉: %+v", kept2)
		}
	}
	if kept2[len(kept2)-1].Role != "user" {
		t.Fatalf("结尾应是 user: %+v", kept2)
	}

	// 全 system / 单条：不动。
	only := []port.PromptMessage{sys}
	if k, o := e.trimOldest(context.Background(), only, 1); len(k) != 1 || o != 0 {
		t.Fatalf("全 system: %d/%d", len(k), o)
	}
	one := []port.PromptMessage{sys, msg("user", strings.Repeat("y", 1000))}
	if k, o := e.trimOldest(context.Background(), one, 1); len(k) != 2 || o != 0 {
		t.Fatalf("至少留 1 条非 system: %d/%d", len(k), o)
	}
}

// TestTrimOldestExported D14 装配根注入入口：与 trimOldest 同约束
// （保 leading system 与最近一条、超 target 才动刀）。
func TestTrimOldestExported(t *testing.T) {
	text := func(role, s string) port.PromptMessage {
		return port.PromptMessage{Role: role, Content: []port.PromptPart{{Kind: "text", Text: s}}}
	}
	msgs := []port.PromptMessage{
		text("system", "persona"),
		text("user", strings.Repeat("长", 1000)),
		text("assistant", strings.Repeat("a", 1000)),
		text("user", "latest"),
	}
	kept, omitted := TrimOldest(context.Background(), nil, msgs, 10)
	if omitted == 0 {
		t.Fatal("应有裁剪")
	}
	if len(kept) != 2 || kept[0].Role != "system" || kept[1].Content[0].Text != "latest" {
		t.Fatalf("kept = %+v, want [system latest]", kept)
	}
	// 未超预算（target 很大）：原样返回。
	kept2, omitted2 := TrimOldest(context.Background(), nil, msgs, 1<<20)
	if omitted2 != 0 || len(kept2) != len(msgs) {
		t.Fatalf("未超预算不应裁: %d/%d", len(kept2), omitted2)
	}
}
