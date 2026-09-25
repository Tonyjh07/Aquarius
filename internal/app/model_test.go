package app

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Tonyjh07/Aquarius/internal/domain/conversation"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

// TestSessionModelCommand /model（D32）：无参列当前+可用清单（能力/单价标注），
// 有参先写回 config 再热切换；用法与未配置依赖报因。
func TestSessionModelCommand(t *testing.T) {
	store := newMemStore()
	s, _, _ := newTestSession(t, store)
	ctx := context.Background()

	// 未配置 ListModels。
	if _, err := s.Handle(ctx, port.UserInput{Command: &port.Command{Name: "model"}}); err == nil ||
		!strings.Contains(err.Error(), "未配置模型服务") {
		t.Fatalf("err = %v, want 未配置模型服务", err)
	}

	s.listModels = func(context.Context) ([]port.ModelInfo, error) {
		return []port.ModelInfo{
			{Name: "gpt-a", Vision: true, CostIn: 0.001, CostOutUSD: 0.002},
			{Name: "mini"},
		}, nil
	}
	out, err := s.Handle(ctx, port.UserInput{Command: &port.Command{Name: "model"}})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	for _, want := range []string{"当前模型: test-model", "gpt-a", "[视觉]", "$0.001/$0.002", "mini"} {
		if !strings.Contains(out, want) {
			t.Errorf("/model 缺 %q: %s", want, out)
		}
	}
	// 列表服务故障上抛。
	s.listModels = func(context.Context) ([]port.ModelInfo, error) { return nil, errors.New("404") }
	if _, err := s.Handle(ctx, port.UserInput{Command: &port.Command{Name: "model"}}); err == nil ||
		!strings.Contains(err.Error(), "404") {
		t.Fatalf("err = %v, want 404 上抛", err)
	}

	// 未配置写回。
	if _, err := s.Handle(ctx, port.UserInput{Command: &port.Command{Name: "model", Args: []string{"m2"}}}); err == nil ||
		!strings.Contains(err.Error(), "未配置模型写回") {
		t.Fatalf("err = %v, want 未配置模型写回", err)
	}

	// 切换：先写回、后热切换。
	var persisted string
	s.persistModel = func(name string) error { persisted = name; return nil }
	out, err = s.Handle(ctx, port.UserInput{Command: &port.Command{Name: "model", Args: []string{"gpt-x"}}})
	if err != nil || !strings.Contains(out, "已切换模型 → gpt-x") {
		t.Fatalf("switch = %q, %v", out, err)
	}
	if persisted != "gpt-x" || s.agent.modelName() != "gpt-x" {
		t.Fatalf("persisted = %q, agent = %q", persisted, s.agent.modelName())
	}

	// 写回失败不动运行态（同 persistLevel 口径）。
	s.persistModel = func(string) error { return errors.New("disk full") }
	if _, err := s.Handle(ctx, port.UserInput{Command: &port.Command{Name: "model", Args: []string{"m3"}}}); err == nil ||
		!strings.Contains(err.Error(), "disk full") {
		t.Fatalf("err = %v, want disk full", err)
	}
	if s.agent.modelName() != "gpt-x" {
		t.Fatalf("写回失败不应切换: %s", s.agent.modelName())
	}

	// 用法错误。
	for _, args := range [][]string{{"a", "b"}, {" "}} {
		if _, err := s.Handle(ctx, port.UserInput{Command: &port.Command{Name: "model", Args: args}}); err == nil ||
			!strings.Contains(err.Error(), "用法") {
			t.Fatalf("args=%v err=%v, want 用法", args, err)
		}
	}
}

// TestAgentModelHotSwitch /model 热切换后下一次生成请求即用新模型（D32）。
func TestAgentModelHotSwitch(t *testing.T) {
	llm := &scriptLLM{t: t, streams: []*scriptStream{
		textStream("r1"),
		textStream("r2"),
	}}
	rec := &recorder{}
	a := newAgent(t, llm, rec, Deps{}, Config{})
	c := newConv(t)
	ctx := context.Background()

	if err := a.Run(ctx, c); err != nil {
		t.Fatalf("run1: %v", err)
	}
	a.setModel("gpt-switched")
	if _, err := c.Append(conversation.RoleUser, []conversation.Part{
		{Kind: conversation.PartText, Text: "再来一次"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := a.Run(ctx, c); err != nil {
		t.Fatalf("run2: %v", err)
	}
	if got := llm.requests[0].Model; got != "test-model" {
		t.Fatalf("req0 model = %q, want test-model", got)
	}
	if got := llm.requests[1].Model; got != "gpt-switched" {
		t.Fatalf("req1 model = %q, want gpt-switched", got)
	}
	// 提交节点也记新模型。
	path := c.Path()
	last := path[len(path)-1]
	if last.Role == conversation.RoleAssistant && last.Model != "gpt-switched" {
		t.Fatalf("node model = %q, want gpt-switched", last.Model)
	}
}
