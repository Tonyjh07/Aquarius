package app

// clock_test.go 时间感知（D317）的行为锁定：off 不加消息（请求体与旧形状一致）、
// live 每轮末尾恰一条戳（时刻递增、第二轮起带上一批耗时）、tree 给历史 user 补各自
// 时刻（修订换新、克隆留旧）、/clock 查看与切换、/context 出现 clock 段。

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/Tonyjh07/Aquarius/internal/domain/conversation"
	"github.com/Tonyjh07/Aquarius/internal/domain/tool"
	"github.com/Tonyjh07/Aquarius/internal/i18n"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

// stepClock 受控时钟：每次 Now 前进 step。
type stepClock struct {
	t    time.Time
	step time.Duration
}

func (c *stepClock) Now() time.Time { t := c.t; c.t = c.t.Add(c.step); return t }

// clockAgent 造一个指定档位的 Agent（脚本 LLM；clk 传受控时钟）。
func clockAgent(t *testing.T, mode string, llm *scriptLLM, clk port.Clock) *Agent {
	t.Helper()
	return newAgent(t, llm, &recorder{}, Deps{Clock: clk, Tools: testToolRunner()}, Config{ClockMode: mode})
}

// clockConv 空会话 + 一条 user（CreatedAt = 固定时刻）。
func clockConv(t *testing.T, a *Agent) *conversation.Conversation {
	t.Helper()
	return newConv(t)
}

// testToolRunner 一个回声工具（脚本 runner，call_1 → pong）。
func testToolRunner() *fakeRunner {
	return &fakeRunner{
		specs:   []tool.Spec{{Name: "think", Description: "思考"}},
		results: map[tool.CallID]tool.Result{"call_1": {CallID: "call_1", OK: true, Output: "想完了"}},
	}
}

// TestClockOffKeepsRequestShape off：请求里没有 clock 消息（现有 agent 测试的全量
// 回归即「旧形状逐字节相同」的网，这里再点一例明确断言）。
func TestClockOffKeepsRequestShape(t *testing.T) {
	llm := &scriptLLM{t: t, streams: []*scriptStream{textStream("好")}}
	a := clockAgent(t, "", llm, fixedClock{testTime}) // 零值 = off
	c := clockConv(t, a)
	if err := a.Run(context.Background(), c); err != nil {
		t.Fatalf("run: %v", err)
	}
	for _, m := range llm.requests[0].Messages {
		if strings.Contains(msgText(m), "Current time:") {
			t.Fatalf("off 档不该有时刻戳: %v", m)
		}
	}
}

// TestClockLiveOneStampPerRound live：一次带工具的 Turn（两轮请求）每轮末尾恰一条戳，
// 时刻递增，第二轮带上一批工具耗时。
func TestClockLiveOneStampPerRound(t *testing.T) {
	callDelta := port.Delta{ToolCalls: []port.ToolCallDelta{{
		Index: 0, ID: "call_1", Name: "think", ArgsDelta: `{"draft":"想一想"}`,
	}}}
	llm := &scriptLLM{t: t, streams: []*scriptStream{
		{steps: []scriptStep{{delta: callDelta}}},
		textStream("答完了"),
	}}
	a := clockAgent(t, modeClockLive, llm, &stepClock{t: testTime, step: 65 * time.Second})
	c := clockConv(t, a)
	if err := a.Run(context.Background(), c); err != nil {
		t.Fatalf("run: %v", err)
	}
	if len(llm.requests) < 2 {
		t.Fatalf("请求轮数 = %d, want ≥2（工具 Turn）", len(llm.requests))
	}
	var stamps []string
	for i, req := range llm.requests {
		var got []string
		for _, m := range req.Messages {
			if m.Role == port.PromptSystem && strings.Contains(msgText(m), "Current time:") {
				got = append(got, msgText(m))
			}
		}
		if len(got) != 1 {
			t.Fatalf("第 %d 轮时刻戳数 = %d, want 恰 1：%v", i, len(got), got)
		}
		stamps = append(stamps, got[0])
	}
	// 时刻随轮递增（受控时钟每步 65s；每轮内 Now 被取多次，只断言单调不减）。
	if !strings.Contains(stamps[1], "Current time:") || stamps[1] == stamps[0] ||
		strings.Index(stamps[1], "Current time: 2026-01-02 ") != strings.Index(stamps[0], "Current time: 2026-01-02 ") {
		t.Fatalf("时刻没有随轮变化: %q → %q", stamps[0], stamps[1])
	}
	if !strings.Contains(stamps[1], "This turn has been running for") ||
		!strings.Contains(stamps[1], "1m") {
		t.Fatalf("第二轮戳缺本 Turn 已跑时长: %q", stamps[1])
	}
	// 第二轮带上一批工具耗时（think 工具名）；第一轮没有。
	if !strings.Contains(stamps[1], "Previous tool batch: think ") {
		t.Fatalf("第二轮戳缺上一批耗时: %q", stamps[1])
	}
	if strings.Contains(stamps[0], "Previous tool batch") {
		t.Fatalf("第一轮不该有耗时: %q", stamps[0])
	}
}

// TestClockTreeAnnotatesUsers tree：历史 user 消息末尾有各自时刻；树里的正文不被污染
// （注记只在装配期——修订自然换新、克隆保留旧值，都是节点 CreatedAt 的既有语义）。
func TestClockTreeAnnotatesUsers(t *testing.T) {
	llm := &scriptLLM{t: t, streams: []*scriptStream{textStream("好")}}
	a := clockAgent(t, modeClockTree, llm, fixedClock{testTime})
	c := clockConv(t, a)
	if _, err := c.Append(conversation.RoleUser, []conversation.Part{{Kind: conversation.PartText, Text: "第一句"}}); err != nil {
		t.Fatalf("append: %v", err)
	}
	if _, err := c.Revise(c.Head, []conversation.Part{{Kind: conversation.PartText, Text: "第一句（改）"}}, conversation.Clone); err != nil {
		t.Fatalf("clone: %v", err)
	}
	if _, err := c.Append(conversation.RoleUser, []conversation.Part{{Kind: conversation.PartText, Text: "第二句"}}); err != nil {
		t.Fatalf("append2: %v", err)
	}
	req, err := a.buildRequest(context.Background(), c, nil)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	var withTime, without int
	for _, m := range req.Messages {
		if m.Role != port.PromptUser {
			continue
		}
		if strings.Contains(msgText(m), "[At:") {
			withTime++
		} else {
			without++
		}
	}
	if withTime != 3 || without != 0 {
		t.Fatalf("tree 注记 = %d 有 / %d 无, want 3/0", withTime, without)
	}
	var plain int
	for _, m := range c.Snapshot().Nodes {
		for _, p := range m.Content {
			if p.Kind == conversation.PartText && strings.Contains(p.Text, "[At:") {
				plain++
			}
		}
	}
	if plain != 0 {
		t.Fatalf("树内正文被写入时刻 %d 处——注记必须只在装配期", plain)
	}
}

// TestClockCommand /clock 查看与切换；非法值报用法。
func TestClockCommand(t *testing.T) {
	s, _, _ := newBatchSession(t, "甲")
	out, err := handleCmd(s, "clock")
	if err != nil {
		t.Fatalf("clock: %v", err)
	}
	if !strings.Contains(out, "live") {
		t.Fatalf("out = %q, want 显示缺省档 live", out)
	}
	if _, err := handleCmd(s, "clock", "tree"); err != nil {
		t.Fatalf("switch: %v", err)
	}
	if s.agent.clockState() != modeClockTree {
		t.Fatalf("档位 = %q, want tree", s.agent.clockState())
	}
	if _, err := handleCmd(s, "clock", "warp"); err == nil ||
		!strings.Contains(err.Error(), i18n.T("usage.clock")) {
		t.Fatalf("err = %v, want 用法文案", err)
	}
}

// TestContextShowsClockSection /context 的 sectionMark 里 clock 段在场（token 计入由
// 既有计数覆盖——clock 消息就是普通 system 消息）。
func TestContextShowsClockSection(t *testing.T) {
	llm := &scriptLLM{t: t, streams: []*scriptStream{textStream("好")}}
	a := clockAgent(t, modeClockLive, llm, fixedClock{testTime})
	c := clockConv(t, a)
	_, marks, err := a.buildRequestTracked(context.Background(), c, nil)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	var found bool
	for _, mk := range marks {
		if mk.Kind == labelClock {
			found = true
		}
	}
	if !found {
		t.Fatalf("sectionMark 缺 clock 段: %+v", marks)
	}
}
