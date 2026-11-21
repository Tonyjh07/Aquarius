package app

// ptc2_test.go PTC-2（D320）的 app 侧锁定：取消端到端（脚本执行中按停止键，Turn 归类
// 取消、不空等）、/code 查看与空态、无人值守下脚本内调用回值（拒绝同样被去重记住）。

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/Tonyjh07/Aquarius/internal/adapter/decorate"
	"github.com/Tonyjh07/Aquarius/internal/adapter/starlarkrun"
	"github.com/Tonyjh07/Aquarius/internal/adapter/toolrun"
	"github.com/Tonyjh07/Aquarius/internal/domain/conversation"
	"github.com/Tonyjh07/Aquarius/internal/domain/tool"
	"github.com/Tonyjh07/Aquarius/internal/i18n"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

// slowTool 慢工具（ctx 可打断）：取消端到端用。
type slowTool struct {
	delay time.Duration
}

func (s *slowTool) Spec() tool.Spec {
	return tool.Spec{Name: "slow", Description: "slow tool", Schema: tool.NoArgsSchema}
}

func (s *slowTool) Execute(ctx context.Context, _ tool.Call) (tool.Result, error) {
	select {
	case <-time.After(s.delay):
		return tool.Result{OK: true, Output: "done"}, nil
	case <-ctx.Done():
		return tool.Result{}, ctx.Err()
	}
}

// ptcSlowRunner 真 toolrun + run_code + slow：确认自动放行（脚本照常跑）。
func ptcSlowRunner(t *testing.T, delay time.Duration) *toolrun.Runner {
	t.Helper()
	r := toolrun.New(toolrun.Options{
		Tools:     []port.Tool{&slowTool{delay: delay}},
		PTC:       true,
		Confirmer: &scriptConfirmer{t: t, answers: []bool{true, true, true}},
	})
	r.Add(starlarkrun.New(starlarkrun.Deps{
		Inner: r,
		AllSpecs: func() []tool.Spec {
			specs, _ := r.AllSpecs(context.Background())
			return specs
		},
	}))
	return r
}

// TestPTC2CancelDuringScript 停止键打断脚本：Turn 快速归类取消，不空等慢工具跑满，
// 也没有第二轮的收尾文本。
func TestPTC2CancelDuringScript(t *testing.T) {
	callDelta := port.Delta{ToolCalls: []port.ToolCallDelta{{
		Index: 0, ID: "call_1", Name: "run_code",
		ArgsDelta: `{"code":` + mustJSON(t, `r = tools.slow()`+"\n"+`return r["output"]`) + `}`,
	}}}
	llm := &scriptLLM{t: t, streams: []*scriptStream{
		{steps: []scriptStep{{delta: callDelta}}},
		textStream("不该到达"),
	}}
	rec := &recorder{}
	a := newAgent(t, llm, rec, Deps{Tools: ptcSlowRunner(t, 30*time.Second)}, Config{})
	c := newConv(t)

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(100 * time.Millisecond)
		cancel()
	}()
	start := time.Now()
	if err := a.Run(ctx, c); err != nil {
		t.Fatalf("取消不应视为失败: %v", err)
	}
	if d := time.Since(start); d > 5*time.Second {
		t.Fatalf("取消耗时 %v，脚本没有被打断", d)
	}
	for _, node := range c.Snapshot().Nodes {
		for _, p := range node.Content {
			if p.Kind == conversation.PartText && strings.Contains(p.Text, "不该到达") {
				t.Fatal("取消后不应该有第二轮收尾文本")
			}
		}
	}
}

// TestPTC2CodeCommand /code：空态 → 跑过之后显示脚本全文与时刻。
func TestPTC2CodeCommand(t *testing.T) {
	s, _, _ := newBatchSession(t, "甲")
	out, err := handleCmd(s, "code")
	if err != nil {
		t.Fatalf("code: %v", err)
	}
	if out != i18n.T("code.empty") {
		t.Fatalf("out = %q, want 空态文案", out)
	}
	s.agent.recordRunCode("return 1 + 1", testTime)
	out, err = handleCmd(s, "code")
	if err != nil {
		t.Fatalf("code: %v", err)
	}
	header := i18n.Tf("code.header", "at", testTime.Local().Format("2006-01-02 15:04:05"))
	if !strings.Contains(out, "return 1 + 1") || !strings.Contains(out, header) {
		t.Fatalf("out = %q, want 脚本全文与头行 %q", out, header)
	}
}

// TestPTC2UnattendedScriptCalls 无人值守：脚本内调用全部当场回拒（ok=false），脚本
// 拿到失败结果继续；同参第二次不再问（拒绝同样被去重记住）。
func TestPTC2UnattendedScriptCalls(t *testing.T) {
	echo := &echoTool{}
	r := toolrun.New(toolrun.Options{
		Tools: []port.Tool{&writingTool{}, echo},
		PTC:   true,
		// 外层 run_code 放行（脚本照常跑），脚本内的工具调用交给无人值守拦截。
		Confirmer: allowRunCode{inner: decorate.Unattended(&scriptConfirmer{t: t}, func() bool { return true })},
	})
	r.Add(starlarkrun.New(starlarkrun.Deps{
		Inner: r,
		AllSpecs: func() []tool.Spec {
			specs, _ := r.AllSpecs(context.Background())
			return specs
		},
	}))
	res, err := r.Execute(context.Background(), tool.Call{
		ID: "c1", Name: "run_code",
		Args: json.RawMessage(`{"code":` + mustJSON(t,
			`a = tools.writing(p=1)`+"\n"+`b = tools.writing(p=1)`+"\n"+`return [a["ok"], b["ok"]]`) + `}`),
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !res.OK || res.Output != "[false,false]" {
		t.Fatalf("res = %+v, want 无人值守全部回拒", res)
	}
}

// allowRunCode 外层 run_code 放行、其余交给确认链（无人值守在其内层）。
type allowRunCode struct {
	inner port.Confirmer
}

func (a allowRunCode) Confirm(ctx context.Context, req port.ConfirmRequest) (port.ConfirmAnswer, error) {
	if req.Tool == port.RunCodeTool {
		return port.ConfirmAnswer{Allow: true}, nil
	}
	return a.inner.Confirm(ctx, req)
}

// writingTool Confirm 级写工具替身（供无人值守测试用）。
type writingTool struct{}

func (w *writingTool) Spec() tool.Spec {
	return tool.Spec{
		Name:        "writing",
		Description: "write a value",
		Schema:      json.RawMessage(`{"type":"object","properties":{"p":{"type":"integer"}}}`),
		Risk:        tool.Confirm,
	}
}

func (w *writingTool) Execute(_ context.Context, _ tool.Call) (tool.Result, error) {
	return tool.Result{OK: true, Output: "written"}, nil
}
