package app

import (
	"context"
	"strings"
	"testing"

	"github.com/Tonyjh07/Aquarius/internal/domain/conversation"
)

// TestCompactPromptInitial 首次压缩提示词：模板 + 规则 + 只总结言行 + 禁续写/工具。
func TestCompactPromptInitial(t *testing.T) {
	p := compactPrompt(false)
	for _, want := range []string{
		"another agent to resume the work",
		"## Objective", "## Requirements", "## Decisions",
		"### Completed", "### Active", "### Blocked",
		"## Next Move", "## Relevant Files", "## Important Context",
		"persona/system configuration",
		"Do not continue the task or call tools.",
		"Do not mention the summary process",
	} {
		if !strings.Contains(p, want) {
			t.Errorf("首次提示词缺 %q", want)
		}
	}
	if strings.Contains(p, "consolidated summary") {
		t.Error("首次提示词不应含合并更新语义")
	}
}

// TestCompactPromptUpdate 链式压缩提示词：合并为一份、新历史优先、对齐工作状态与下一步。
func TestCompactPromptUpdate(t *testing.T) {
	p := compactPrompt(true)
	for _, want := range []string{
		"consolidated summary",
		"Newer history always takes precedence",
		"Reconcile Work State and Next Move",
		"Return only the updated Markdown sections",
		"## Objective", // 合并态同样带模板与规则
		"Do not mention the summary process",
	} {
		if !strings.Contains(p, want) {
			t.Errorf("合并更新提示词缺 %q", want)
		}
	}
}

// TestHasSummarySection 小节校验：## 与 ### 标题都算、容忍空白、无标题的散文不算。
func TestHasSummarySection(t *testing.T) {
	cases := []struct {
		name string
		text string
		want bool
	}{
		{"顶级小节", "## Objective\n- 清理池塘", true},
		{"子小节", "  ### Blocked  \n- 需要水泵", true},
		{"带前言", "Summary:\n## Next Move\n1. x", true},
		{"单井号不算", "# Objective\n- x", false},
		{"散文", "这是一段没有小节标题的摘要。", false},
		{"空", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := hasSummarySection(tc.text); got != tc.want {
				t.Fatalf("hasSummarySection(%q) = %v, want %v", tc.text, got, tc.want)
			}
		})
	}
}

// TestAgentCompactRetriesOnTemplateMismatch 输出缺模板小节 → 带提醒重试一次：
// 第二次合格则入树，两次生成的 Usage 累计，第二个请求带重试提醒并发 NoticeEvent。
func TestAgentCompactRetriesOnTemplateMismatch(t *testing.T) {
	const good = "## Objective\n- 清理池塘"
	llm := &scriptLLM{t: t, streams: []*scriptStream{
		withUsage(textStream("没按模板写的散文"), conversation.Usage{InputTokens: 10, OutputTokens: 5}),
		withUsage(textStream(good), conversation.Usage{InputTokens: 20, OutputTokens: 7}),
	}}
	rec := &recorder{}
	a := newAgent(t, llm, rec, Deps{}, Config{})
	c := newConv(t)

	node, _, err := a.Compact(context.Background(), c)
	if err != nil {
		t.Fatalf("compact: %v", err)
	}
	if node.Content[0].Text != good {
		t.Fatalf("node text = %q, want %q", node.Content[0].Text, good)
	}
	if node.Usage.InputTokens != 30 || node.Usage.OutputTokens != 12 {
		t.Fatalf("usage = %+v, want 两次生成累加 {30 12}", node.Usage)
	}
	if len(llm.requests) != 2 {
		t.Fatalf("requests = %d, want 2", len(llm.requests))
	}
	if txt := reqText(llm.requests[1]); !strings.Contains(txt, "required summary template") {
		t.Fatalf("重试请求缺提醒: %.200s", txt)
	}
	if !hasNotice(rec.events, "重试") {
		t.Fatalf("缺重试 NoticeEvent: %v", eventNames(rec.events))
	}
	if err := c.Validate(); err != nil {
		t.Fatalf("validate: %v", err)
	}
}

// TestAgentCompactFailsAfterRetry 两次输出都不合模板：报"未匹配模板"、树无损、不发 Committed。
func TestAgentCompactFailsAfterRetry(t *testing.T) {
	llm := &scriptLLM{t: t, streams: []*scriptStream{
		textStream("散文一"), textStream("散文二"),
	}}
	rec := &recorder{}
	a := newAgent(t, llm, rec, Deps{}, Config{})
	c := newConv(t)
	prevHead := c.Head

	_, _, err := a.Compact(context.Background(), c)
	if err == nil || !strings.Contains(err.Error(), "summary template") {
		t.Fatalf("err = %v, want 未匹配摘要模板", err)
	}
	if c.Head != prevHead {
		t.Fatalf("head = %s, want 树无损 %s", c.Head, prevHead)
	}
	if len(llm.requests) != 2 {
		t.Fatalf("requests = %d, want 2", len(llm.requests))
	}
	for _, name := range eventNames(rec.events) {
		if name == "committed" {
			t.Fatalf("失败不应发 Committed: %v", eventNames(rec.events))
		}
	}
}

// TestAgentCompactEmptyOutputRetriesThenFails 空输出同样先重试一次，两次皆空报"模型返回为空"。
func TestAgentCompactEmptyOutputRetriesThenFails(t *testing.T) {
	llm := &scriptLLM{t: t, streams: []*scriptStream{textStream(), textStream()}}
	rec := &recorder{}
	a := newAgent(t, llm, rec, Deps{}, Config{})
	c := newConv(t)
	prevHead := c.Head

	_, _, err := a.Compact(context.Background(), c)
	if err == nil || !strings.Contains(err.Error(), "model returned empty output") {
		t.Fatalf("err = %v, want 模型返回为空", err)
	}
	if len(llm.requests) != 2 {
		t.Fatalf("requests = %d, want 2", len(llm.requests))
	}
	if !hasNotice(rec.events, "重试") {
		t.Fatalf("缺重试 NoticeEvent: %v", eventNames(rec.events))
	}
	if c.Head != prevHead {
		t.Fatalf("head = %s, want 树无损 %s", c.Head, prevHead)
	}
}

// TestAgentCompactUpdatesExistingSummary 链式压缩（水位处已有旧摘要）→ 合并更新提示词：
// 输入只带摘要之后的历史，提示词含合并语义。
func TestAgentCompactUpdatesExistingSummary(t *testing.T) {
	const merged = "## Objective\n- 合并后的摘要"
	llm := &scriptLLM{t: t, streams: []*scriptStream{textStream(merged)}}
	rec := &recorder{}
	a := newAgent(t, llm, rec, Deps{}, Config{})
	c := newConv(t) // root + user "hi"
	commitNode(t, c, c.Head, conversation.RoleSystem, "旧摘要")
	if _, err := c.Append(conversation.RoleUser, []conversation.Part{
		{Kind: conversation.PartText, Text: "新的追问"},
	}); err != nil {
		t.Fatal(err)
	}

	node, _, err := a.Compact(context.Background(), c)
	if err != nil {
		t.Fatalf("compact: %v", err)
	}
	if node.Content[0].Text != merged {
		t.Fatalf("node text = %q", node.Content[0].Text)
	}
	if len(llm.requests) != 1 {
		t.Fatalf("requests = %d, want 1", len(llm.requests))
	}
	txt := reqText(llm.requests[0])
	for _, want := range []string{"consolidated summary", "旧摘要", "新的追问"} {
		if !strings.Contains(txt, want) {
			t.Fatalf("合并请求缺 %q: %.300s", want, txt)
		}
	}
	if strings.Contains(txt, "resume the work") {
		t.Fatal("已有旧摘要时不应走首次压缩提示词")
	}
	// 水位生效：再装配只剩新摘要与追问之后的结构。
	msgs, err := assemblePath(context.Background(), c.Path(), nil, false)
	if err != nil {
		t.Fatalf("assemble: %v", err)
	}
	if len(msgs) != 1 || msgs[0].Content[0].Text != merged {
		t.Fatalf("msgs = %+v, want 水位裁剪后仅 [新摘要]", msgs)
	}
}
