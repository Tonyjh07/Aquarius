package app

// ptc_test.go 呈现模式（D319）的 app 侧锁定：ptc 档请求里工具面只有 run_code 一个声明，
// run_code 调用经脚本内绑定回到真实 toolrun（借真适配器：端到端验收允许，生产代码仍走端口），
// 第二轮正常收束；native 档请求形状不变由既有全量测试覆盖。

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/Tonyjh07/Aquarius/internal/adapter/starlarkrun"
	"github.com/Tonyjh07/Aquarius/internal/adapter/toolrun"
	"github.com/Tonyjh07/Aquarius/internal/domain/conversation"
	"github.com/Tonyjh07/Aquarius/internal/domain/tool"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

// ptcRunner 真 toolrun + 真 starlarkrun：内置 echo（Safe）与 run_code，呈现模式开。
func ptcRunner(t *testing.T) *toolrun.Runner {
	t.Helper()
	r := toolrun.New(toolrun.Options{
		Tools:     []port.Tool{&echoTool{}},
		PTC:       true,
		Confirmer: &scriptConfirmer{t: t, answers: []bool{true}},
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

// echoTool 回声工具：把 msg 参数原样回作 output。
type echoTool struct{}

func (e *echoTool) Spec() tool.Spec {
	return tool.Spec{
		Name:        "echo",
		Description: "echo the message back",
		Schema:      json.RawMessage(`{"type":"object","properties":{"msg":{"type":"string"}}}`),
	}
}

func (e *echoTool) Execute(_ context.Context, call tool.Call) (tool.Result, error) {
	var m struct {
		Msg string `json:"msg"`
	}
	_ = json.Unmarshal(call.Args, &m)
	return tool.Result{OK: true, Output: m.Msg}, nil
}

// TestPTCTurnSingleDeclaration ptc 档一轮带脚本的 Turn：请求面只有 run_code，
// 脚本内 echo 走真 runner，最终文本正常提交。
func TestPTCTurnSingleDeclaration(t *testing.T) {
	script := `r = tools.echo(msg="hi")` + "\n" + `return r["output"]`
	callDelta := port.Delta{ToolCalls: []port.ToolCallDelta{{
		Index: 0, ID: "call_1", Name: "run_code",
		ArgsDelta: `{"code":` + mustJSON(t, script) + `}`,
	}}}
	llm := &scriptLLM{t: t, streams: []*scriptStream{
		{steps: []scriptStep{{delta: callDelta}}},
		textStream("脚本说 hi"),
	}}
	rec := &recorder{}
	a := newAgent(t, llm, rec, Deps{Tools: ptcRunner(t)}, Config{})
	c := newConv(t)
	if err := a.Run(context.Background(), c); err != nil {
		t.Fatalf("run: %v", err)
	}
	specs := llm.requests[0].Tools
	if len(specs) != 1 || specs[0].Name != port.RunCodeTool {
		t.Fatalf("请求工具面 = %+v, want 只剩 run_code", specs)
	}
	// 脚本结果经装配回填：最后一段 assistant 文本在树里。
	last := c.Path()[len(c.Path())-1]
	var text string
	for _, p := range last.Content {
		if p.Kind == conversation.PartText {
			text += p.Text
		}
	}
	if !strings.Contains(text, "脚本说 hi") {
		t.Fatalf("收尾文本 = %q", text)
	}
	// 第一轮的 run_code 调用产出工具分片（结果 = 脚本返回值的 JSON）。
	found := false
	for _, node := range c.Snapshot().Nodes {
		for _, p := range node.Content {
			if p.Kind == conversation.PartTool && p.Tool != nil &&
				p.Tool.Name == "run_code" && p.Tool.Result != nil &&
				strings.Contains(p.Tool.Result.Output, "hi") {
				found = true
			}
		}
	}
	if !found {
		t.Fatal("树里没有脚本结果分片")
	}
}

// TestPTCConfirmPerScript run_code 是 Confirm 级：确认面读到程序全文，拒绝则结果回填失败。
func TestPTCConfirmPerScript(t *testing.T) {
	r := toolrun.New(toolrun.Options{
		Tools:     []port.Tool{&echoTool{}},
		PTC:       true,
		Confirmer: &ptcConf{deny: true},
	})
	r.Add(starlarkrun.New(starlarkrun.Deps{
		Inner: r,
		AllSpecs: func() []tool.Spec {
			specs, _ := r.AllSpecs(context.Background())
			return specs
		},
	}))
	res, err := r.Execute(context.Background(), tool.Call{
		ID: "c1", Name: "run_code", Args: json.RawMessage(`{"code":"return 1"}`),
	})
	if err != nil || res.OK || !strings.Contains(res.Err, "denied") {
		t.Fatalf("res = %+v err = %v, want 拒绝回填", res, err)
	}
}

// ptcConf 记录确认提示并按开关应答。
type ptcConf struct {
	deny bool
}

func (c *ptcConf) Confirm(_ context.Context, req port.ConfirmRequest) (port.ConfirmAnswer, error) {
	if c.deny {
		return port.ConfirmAnswer{Allow: false}, nil
	}
	return port.ConfirmAnswer{Allow: true}, nil
}

func mustJSON(t *testing.T, s string) string {
	t.Helper()
	b, err := json.Marshal(s)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return string(b)
}
