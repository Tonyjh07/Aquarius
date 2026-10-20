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
			{Name: "gpt-a", Caps: port.ModelCaps{Tools: true, Vision: true}, CostIn: 0.001, CostOutUSD: 0.002},
			{Name: "mini"},
		}, nil
	}
	s.caps = func() port.ModelCaps { return port.ModelCaps{Tools: true, Reasoning: true, Vision: true} }
	out, err := s.Handle(ctx, port.UserInput{Command: &port.Command{Name: "model"}})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	for _, want := range []string{"当前模型: test-model", "生效能力: 工具 / 思考 / 图片", "gpt-a", "[工具/图片]", "$0.001/$0.002", "mini"} {
		if !strings.Contains(out, want) {
			t.Errorf("/model 缺 %q: %s", want, out)
		}
	}
	// 未声明能力：逐项说明缺什么、去哪儿声明（服务端不报模态，用户没有别的途径知道）。
	s.caps = func() port.ModelCaps { return port.ModelCaps{} }
	out, err = s.Handle(ctx, port.UserInput{Command: &port.Command{Name: "model"}})
	if err != nil {
		t.Fatalf("list(no caps): %v", err)
	}
	if !strings.Contains(out, "生效能力: （三项均未声明）") || !strings.Contains(out, "未启用: 工具调用、思考、图片输入") {
		t.Fatalf("/model 未说明能力缺失: %s", out)
	} // 列表服务故障上抛。
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

// TestModelCommandChainAnnotation D173：降级链拉低了能力时，/model 要指名是哪一环
// 失去的——否则用户只看到「工具关了」，得一个个去猜配置。
func TestModelCommandChainAnnotation(t *testing.T) {
	s, _, _ := newTestSession(t, newMemStore())
	s.providerName = func() string { return "a" }
	s.listModels = func(context.Context) ([]port.ModelInfo, error) {
		return []port.ModelInfo{{Name: "m1"}}, nil
	}
	primary := port.ModelCaps{Tools: true, Reasoning: true, Vision: true}
	weak := port.ModelCaps{Tools: true}
	s.caps = func() port.ModelCaps { return weak }
	s.chain = func() []ModelLink {
		return []ModelLink{
			{Provider: "a", Model: "m1", Caps: primary, Primary: true},
			{Provider: "b", Model: "mb", Caps: weak},
		}
	}
	out, err := s.Handle(context.Background(), port.UserInput{Command: &port.Command{Name: "model"}})
	if err != nil {
		t.Fatalf("model: %v", err)
	}
	for _, want := range []string{
		"生效能力: 工具",
		"未启用: 思考、图片输入",
		"拉低整链的是 a/m1 失去了 思考、图片", // 逐环标注：谁本来有能力、因何没用上
		"降级链:",
		"1. a / m1 [工具 / 思考 / 图片]",
		"2. b / mb [工具]",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("/model 缺 %q:\n%s", want, out)
		}
	}
}

// TestModelCommandBudget D177：/model 要说明上下文预算的生效值，以及它是被模型窗口
// 还是全局上限压住的——压缩提前触发时用户得能看出原因。
func TestModelCommandBudget(t *testing.T) {
	s, _, _ := newTestSession(t, newMemStore())
	s.listModels = func(context.Context) ([]port.ModelInfo, error) {
		return []port.ModelInfo{{Name: "m1"}}, nil
	}
	s.caps = func() port.ModelCaps { return port.ModelCaps{Tools: true} }

	s.budget = func() ContextBudget { return ContextBudget{Effective: 64000, GlobalLimit: 64000} }
	out, err := s.Handle(context.Background(), port.UserInput{Command: &port.Command{Name: "model"}})
	if err != nil {
		t.Fatalf("model: %v", err)
	}
	if !strings.Contains(out, "上下文预算: 64k tokens（全局上限；模型未声明窗口）") {
		t.Fatalf("/model 未说明预算来源:\n%s", out)
	}

	s.budget = func() ContextBudget {
		return ContextBudget{Effective: 32000, ModelWindow: 32000, GlobalLimit: 200000}
	}
	out, err = s.Handle(context.Background(), port.UserInput{Command: &port.Command{Name: "model"}})
	if err != nil {
		t.Fatalf("model: %v", err)
	}
	if !strings.Contains(out, "模型窗口 32k 在压；全局上限 200k") {
		t.Fatalf("/model 未指出是模型窗口在压:\n%s", out)
	}

	s.budget = func() ContextBudget {
		return ContextBudget{Effective: 64000, ModelWindow: 128000, GlobalLimit: 64000}
	}
	out, err = s.Handle(context.Background(), port.UserInput{Command: &port.Command{Name: "model"}})
	if err != nil {
		t.Fatalf("model: %v", err)
	}
	if !strings.Contains(out, "上下文预算: 64k tokens（全局上限在压；模型窗口 128k）") {
		t.Fatalf("/model 未指出是全局上限在压:\n%s", out)
	}
}

// TestHumanTokens 窗口数值的紧凑写法：整 M / 整 k / 原样三档（1M 写成 1000k 没人看）。
func TestHumanTokens(t *testing.T) {
	for in, want := range map[int]string{
		1000000: "1M", 200000: "200k", 128000: "128k", 64000: "64k", 32000: "32k", 0: "0", 1500: "1500",
	} {
		if got := humanTokens(in); got != want {
			t.Errorf("humanTokens(%d) = %q, want %q", in, got, want)
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
