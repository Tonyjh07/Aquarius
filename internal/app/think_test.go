package app

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Tonyjh07/Aquarius/internal/port"
)

// thinkCmd 直发一条命令。
func thinkCmd(t *testing.T, s *Session, name string, args ...string) (string, error) {
	t.Helper()
	return s.Handle(context.Background(), port.UserInput{Command: &port.Command{Name: name, Args: args}})
}

// TestSessionThinkEffortCommands /think /effort（D34）：无参显示、切换写回、
// 重复短路、写回失败不动运行态、未配置持久化报错、effort 枚举校验、/help 收录。
func TestSessionThinkEffortCommands(t *testing.T) {
	store := newMemStore()
	var thinks []bool
	var efforts []string
	s, _, _ := newTestSession(t, store)
	s.persistThink = func(on bool) error { thinks = append(thinks, on); return nil }
	s.persistEffort = func(l string) error { efforts = append(efforts, l); return nil }

	// 无参：缺省开、档位未设。
	out, err := thinkCmd(t, s, "think")
	if err != nil || !strings.Contains(out, "开") || !strings.Contains(out, "未设") {
		t.Fatalf("think report = %q, %v", out, err)
	}
	out, err = thinkCmd(t, s, "effort")
	if err != nil || !strings.Contains(out, "未设") {
		t.Fatalf("effort report = %q, %v", out, err)
	}

	// 非法参数拒绝且不写回。
	if _, err := thinkCmd(t, s, "think", "maybe"); err == nil {
		t.Fatal("非法 think 参数应报错")
	}
	if _, err := thinkCmd(t, s, "effort", "ultra"); err == nil {
		t.Fatal("非法 effort 档位应报错")
	}
	if len(thinks) != 0 || len(efforts) != 0 {
		t.Fatalf("非法参数不应写回: %v %v", thinks, efforts)
	}

	// /effort 切换 + 短路。
	out, err = thinkCmd(t, s, "effort", "HIGH")
	if err != nil || !strings.Contains(out, "high") {
		t.Fatalf("effort switch = %q, %v", out, err)
	}
	if len(efforts) != 1 || efforts[0] != "high" || s.agent.Effort() != "high" {
		t.Fatalf("efforts = %v, agent = %q", efforts, s.agent.Effort())
	}
	if out, err = thinkCmd(t, s, "effort", "high"); err != nil || !strings.Contains(out, "已是") {
		t.Fatalf("重复档位应短路: %q, %v", out, err)
	}
	if len(efforts) != 1 {
		t.Fatalf("efforts = %v, want 不重复写回", efforts)
	}

	// /think off：写回 + 热切；报告反映关态且不再列档位行。
	out, err = thinkCmd(t, s, "think", "off")
	if err != nil || !strings.Contains(out, "off") {
		t.Fatalf("think off = %q, %v", out, err)
	}
	if len(thinks) != 1 || thinks[0] || s.agent.ThinkOn() {
		t.Fatalf("thinks = %v, agent on = %v", thinks, s.agent.ThinkOn())
	}
	out, _ = thinkCmd(t, s, "think")
	if !strings.Contains(out, "关") || strings.Contains(out, "推理档位") {
		t.Fatalf("off 后报告应只显示关态: %q", out)
	}
	if out, err = thinkCmd(t, s, "think", "off"); err != nil || !strings.Contains(out, "已是") {
		t.Fatalf("重复开关应短路: %q, %v", out, err)
	}
	if len(thinks) != 1 {
		t.Fatalf("thinks = %v, want 不重复写回", thinks)
	}

	// /think on 恢复。
	if _, err = thinkCmd(t, s, "think", "on"); err != nil {
		t.Fatalf("think on: %v", err)
	}
	if len(thinks) != 2 || !thinks[1] || !s.agent.ThinkOn() {
		t.Fatalf("thinks = %v", thinks)
	}

	// 写回失败：报错且运行态不动（同 /permission 口径）。
	s2, _, _ := newTestSession(t, newMemStore())
	s2.persistThink = func(bool) error { return errors.New("disk full") }
	if _, err := thinkCmd(t, s2, "think", "off"); err == nil || !strings.Contains(err.Error(), "disk full") {
		t.Fatalf("err = %v, want 写回失败上抛", err)
	}
	if !s2.agent.ThinkOn() {
		t.Fatal("写回失败不应切运行态")
	}

	// 未配置持久化：拒绝切换。
	s3, _, _ := newTestSession(t, newMemStore())
	if _, err := thinkCmd(t, s3, "think", "off"); err == nil || !strings.Contains(err.Error(), "未配置") {
		t.Fatalf("err = %v, want 未配置报错", err)
	}
	if _, err := thinkCmd(t, s3, "effort", "low"); err == nil || !strings.Contains(err.Error(), "未配置") {
		t.Fatalf("err = %v, want 未配置报错", err)
	}

	// /help 收录。
	help, err := thinkCmd(t, s, "help")
	if err != nil || !strings.Contains(help, "/think") || !strings.Contains(help, "/effort") {
		t.Fatalf("help = %q, %v", help, err)
	}
}

// TestBuildRequestReasoningInjection D34 注入矩阵：三态 think × effort 档位
// 决定 reasoning_effort / enable_thinking 是否随请求发送。
func TestBuildRequestReasoningInjection(t *testing.T) {
	s, llm, _ := newTestSession(t, newMemStore(),
		textStream("1"), textStream("2"), textStream("3"), textStream("4"))
	turn := func() port.Sampling {
		t.Helper()
		if _, err := s.Handle(context.Background(), port.UserInput{Text: "hi"}); err != nil {
			t.Fatalf("turn: %v", err)
		}
		reqs := llm.requests
		return reqs[len(reqs)-1].Params
	}

	// 缺省态：effort 未设 → 零字段。
	p := turn()
	if p.Thinking != nil || p.ReasoningEffort != "" {
		t.Fatalf("缺省态 = %v %q, want 零字段", p.Thinking, p.ReasoningEffort)
	}

	// 键缺失 + effort 档位：发档位、不发布尔（保持零字段变化）。
	s.agent.setEffort("high")
	p = turn()
	if p.Thinking != nil || p.ReasoningEffort != "high" {
		t.Fatalf("缺省+档位 = %v %q, want nil/high", p.Thinking, p.ReasoningEffort)
	}

	// 显式关：发布尔 false、effort 被覆盖（不发）。
	off := false
	s.agent.setThink(&off)
	p = turn()
	if p.Thinking == nil || *p.Thinking || p.ReasoningEffort != "" {
		t.Fatalf("显式关 = %v %q, want false/空", p.Thinking, p.ReasoningEffort)
	}

	// 显式开：发布尔 true + 档位照发。
	on := true
	s.agent.setThink(&on)
	s.agent.setEffort("low")
	p = turn()
	if p.Thinking == nil || !*p.Thinking || p.ReasoningEffort != "low" {
		t.Fatalf("显式开 = %v %q, want true/low", p.Thinking, p.ReasoningEffort)
	}
}
