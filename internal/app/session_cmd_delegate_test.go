package app

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Tonyjh07/Aquarius/internal/port"
)

// fakeDelegate 脚本化转交端口（/delegate 命令与命令表测试）。
type fakeDelegate struct {
	agents  []port.DelegateInfo
	listErr error
	killed  []port.DelegateID
	killErr error
}

func (f *fakeDelegate) List(context.Context) ([]port.DelegateInfo, error) {
	return f.agents, f.listErr
}

func (f *fakeDelegate) Start(context.Context, port.DelegateSpec) (port.DelegateTask, error) {
	return port.DelegateTask{}, errors.New("not scripted")
}

func (f *fakeDelegate) Wait(context.Context, port.DelegateID) (port.DelegateRun, error) {
	return port.DelegateRun{}, errors.New("not scripted")
}

func (f *fakeDelegate) Kill(_ context.Context, id port.DelegateID) error {
	if f.killErr != nil {
		return f.killErr
	}
	f.killed = append(f.killed, id)
	return nil
}

// TestSessionDelegateList /delegate list：可用性、不可用原因、agentInfo 三者同屏，
// 用户读到的是「这个代理现在能不能用、为什么」。
func TestSessionDelegateList(t *testing.T) {
	fd := &fakeDelegate{agents: []port.DelegateInfo{
		{Name: "claude", Command: "claude-code-acp", Available: true, Title: "Claude Code", Version: "0.4.1"},
		{Name: "gemini", Command: "gemini", Note: "命令不在 PATH 上"},
	}}
	s := &Session{delegates: fd}
	out, err := s.delegateReport(context.Background(), nil)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	for _, want := range []string{"claude", "Claude Code 0.4.1", "可用", "claude-code-acp", "gemini", "命令不在 PATH 上"} {
		if !strings.Contains(out, want) {
			t.Fatalf("output = %q, want 含 %q", out, want)
		}
	}

	// 未装配 / 零代理：给出配置指引，而不是空表。
	if out, err := (&Session{}).delegateReport(context.Background(), nil); err != nil || !strings.Contains(out, "delegate.agents") {
		t.Fatalf("未配置时 = %q, err = %v", out, err)
	}
	if out, err := (&Session{delegates: &fakeDelegate{}}).delegateReport(context.Background(), nil); err != nil || !strings.Contains(out, "delegate.agents") {
		t.Fatalf("零代理时 = %q, err = %v", out, err)
	}
}

// TestSessionDelegateKill /delegate kill <id>：命令面只有叫停，没有派活。
func TestSessionDelegateKill(t *testing.T) {
	fd := &fakeDelegate{}
	s := &Session{delegates: fd}
	if msg, err := s.delegateReport(context.Background(), []string{"kill", "d1"}); err != nil || !strings.Contains(msg, "已叫停") {
		t.Fatalf("kill = %q, err = %v", msg, err)
	}
	if len(fd.killed) != 1 || fd.killed[0] != "d1" {
		t.Fatalf("killed = %v", fd.killed)
	}
	if _, err := s.delegateReport(context.Background(), []string{"kill"}); err == nil {
		t.Fatal("缺 id 应报用法")
	}
	if _, err := s.delegateReport(context.Background(), []string{"start", "x"}); err == nil {
		t.Fatal("未知子命令应报用法")
	}
}
