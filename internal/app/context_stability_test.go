package app

// context_stability_test.go 前缀稳定性对比（D318）的行为锁定：连续两轮同会话给出未变
// 长度与占比；clock 段被排除；改人格后明显下降；跨会话与首次请求不出该行；last 语义不变。

import (
	"context"
	"strings"
	"testing"

	"github.com/Tonyjh07/Aquarius/internal/i18n"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

// stabilityReq 构造一份实发快照素材（消息 + marks）。
func stabilityReq(sysText string, userText string, clockText string) (port.GenerateRequest, []sectionMark) {
	req := port.GenerateRequest{Model: "m"}
	var marks []sectionMark
	req.Messages = append(req.Messages, port.PromptMessage{
		Role: port.PromptSystem, Content: []port.PromptPart{{Kind: port.PromptText, Text: sysText}},
	})
	marks = append(marks, sectionMark{Msg: 0, Part: 0, Kind: labelPersona})
	req.Messages = append(req.Messages, port.PromptMessage{
		Role: port.PromptUser, Content: []port.PromptPart{{Kind: port.PromptText, Text: userText}},
	})
	marks = append(marks, sectionMark{Msg: 1, Part: 0, Kind: labelUser})
	if clockText != "" {
		req.Messages = append(req.Messages, port.PromptMessage{
			Role: port.PromptSystem, Content: []port.PromptPart{{Kind: port.PromptText, Text: clockText}},
		})
		marks = append(marks, sectionMark{Msg: 2, Part: 0, Kind: labelClock})
	}
	return req, marks
}

// TestPrefixStabilityStable 连续两轮同会话：前缀到段尾未变，chars>0 且占比 100。
func TestPrefixStabilityStable(t *testing.T) {
	a := newAgent(t, &scriptLLM{t: t}, &recorder{}, Deps{}, Config{})
	req1, marks1 := stabilityReq("你是一个助手", "第一句", "Current time: day1")
	a.saveLast(testTime, "conv-1", req1, marks1)
	req2, marks2 := stabilityReq("你是一个助手", "第一句\n第二句", "Current time: day2")
	a.saveLast(testTime, "conv-1", req2, marks2)
	chars, total, ok := a.prefixStability(
		&contextSnapshot{conv: "conv-1", req: req2, marks: marks2}, a.prevSnap.Load())
	if !ok || chars <= 0 {
		t.Fatalf("ok=%v chars=%d, want 同会话可比且未变长度 >0", ok, chars)
	}
	// clock 段被排除：两轮 clock 文本完全不同，前缀应覆盖上一份快照的全文
	// （prev 除 clock 外一字未变 → 未变长度 = prev 全长）。
	prevFlat, _ := flattenSections(a.prevSnap.Load())
	if chars != len([]rune(prevFlat)) {
		t.Fatalf("chars = %d, want prev 全长 %d（clock 排除后应整段命中）", chars, len([]rune(prevFlat)))
	}
	if total < chars {
		t.Fatalf("total = %d < chars = %d", total, chars)
	}
}

// stabilityReq0 只有一段（persona）的请求素材：垫 prev 用。
func stabilityReq0(sysText string) (port.GenerateRequest, []sectionMark) {
	req := port.GenerateRequest{Model: "m"}
	req.Messages = append(req.Messages, port.PromptMessage{
		Role: port.PromptSystem, Content: []port.PromptPart{{Kind: port.PromptText, Text: sysText}},
	})
	return req, []sectionMark{{Msg: 0, Part: 0, Kind: labelPersona}}
}

// TestPrefixStabilityPersonaChange 改了人格：前缀在首段就断裂，明显下降。
func TestPrefixStabilityPersonaChange(t *testing.T) {
	a := newAgent(t, &scriptLLM{t: t}, &recorder{}, Deps{}, Config{})
	// 两轮同会话、人格不同：prev 存在（比较才可用），断点在首段。
	r0, m0 := stabilityReq0("你是一个助手")
	a.saveLast(testTime, "conv-1", r0, m0)
	req1, marks1 := stabilityReq("你是另一位助手", "第一句", "")
	a.saveLast(testTime, "conv-1", req1, marks1)
	req2, marks2 := stabilityReq("你是另一位助手", "第一句", "")
	chars, _, ok := a.prefixStability(
		&contextSnapshot{conv: "conv-1", req: req2, marks: marks2}, a.prevSnap.Load())
	if !ok || chars != 2 {
		t.Fatalf("ok=%v chars=%d, want 首段第二字即断（chars=2）", ok, chars)
	}
}

// TestPrefixStabilityCrossConversation 跨会话不比：ok=false（缺数据与数据差是两回事）。
func TestPrefixStabilityCrossConversation(t *testing.T) {
	a := newAgent(t, &scriptLLM{t: t}, &recorder{}, Deps{}, Config{})
	req1, marks1 := stabilityReq("你是一个助手", "第一句", "")
	a.saveLast(testTime, "conv-1", req1, marks1)
	req2, marks2 := stabilityReq("你是一个助手", "第一句", "")
	if _, _, ok := a.prefixStability(
		&contextSnapshot{conv: "conv-2", req: req2, marks: marks2}, a.prevSnap.Load()); ok {
		t.Fatal("跨会话不该比较")
	}
	if _, _, ok := a.prefixStability(nil, a.prevSnap.Load()); ok {
		t.Fatal("cur 缺失不该比较")
	}
}

// TestContextSummaryShowsStability 概览渲染：可比时出一行；不可比时不出现。
func TestContextSummaryShowsStability(t *testing.T) {
	a := newAgent(t, &scriptLLM{t: t}, &recorder{}, Deps{}, Config{})
	r0, m0 := stabilityReq0("垫一份 prev")
	a.saveLast(testTime, "conv-1", r0, m0)
	req1, marks1 := stabilityReq("你是一个助手", "第一句", "")
	a.saveLast(testTime, "conv-1", req1, marks1)
	req2, marks2 := stabilityReq("你是一个助手", "第一句", "")
	rep := a.reportFrom(context.Background(), req2, marks2, false, testTime)
	// live 的参照是 last（与 ContextReport 的实现同口径，D318）。
	rep.StabilityChars, rep.StabilityTotal, rep.StabilityOK = a.prefixStability(
		&contextSnapshot{conv: "conv-1", req: req2, marks: marks2}, a.lastSnap.Load())
	out := a.renderContextSummary(rep)
	if !strings.Contains(out, i18n.Tf("session.ctx.stability", "chars", rep.StabilityChars,
		"pct", "100.0")) {
		t.Fatalf("概览缺稳定性行:\n%s", out)
	}
	// 不可比（首次请求）：不出这一行。
	rep2 := a.reportFrom(context.Background(), req2, marks2, false, testTime)
	out2 := a.renderContextSummary(rep2)
	if strings.Contains(out2, i18n.Tf("session.ctx.stability", "chars", 0, "pct", "0.0")) {
		t.Fatalf("不可比时不该出稳定性行:\n%s", out2)
	}
}
