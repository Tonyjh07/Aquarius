package starlarkrun

// starlarkrun_test.go run_code 工具（D319）的形状锁定：脚本包函数后顶层 for/return 合法、
// 绑定结果形状 {"ok","output"|"error"}、parallel 保序、失败全是结果字段（解析错带行号、
// 超步数、配额、不可 JSON 化）、run_code 不出现在绑定里、确认面回显程序全文。

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Tonyjh07/Aquarius/internal/domain/tool"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

// fakeRunner 内存工具面：注册表 + 固定应答；记录收到的调用。
type fakeRunner struct {
	mu    sync.Mutex
	specs []tool.Spec
	seen  []tool.Call
	outs  map[string]string // name → output
	fail  map[string]string // name → err
	delay time.Duration
}

func (f *fakeRunner) Specs(context.Context) ([]tool.Spec, error) { return f.specs, nil }

func (f *fakeRunner) Execute(ctx context.Context, call tool.Call) (tool.Result, error) {
	f.mu.Lock()
	f.seen = append(f.seen, call)
	f.mu.Unlock()
	if f.delay > 0 {
		select {
		case <-time.After(f.delay):
		case <-ctx.Done():
			return tool.Result{}, ctx.Err()
		}
	}
	if err, ok := f.fail[call.Name]; ok {
		return tool.Result{OK: false, Err: err}, nil
	}
	if out, ok := f.outs[call.Name]; ok {
		return tool.Result{OK: true, Output: out}, nil
	}
	return tool.Result{OK: true, Output: ""}, nil
}

// specsFn AllSpecs 的快照口（与 Deps 的签名同形）。
func specsFn(f *fakeRunner) func() []tool.Spec {
	return func() []tool.Spec { return f.specs }
}

func runnerFixture() *fakeRunner {
	return &fakeRunner{
		specs: []tool.Spec{
			{Name: "echo", Description: "echo the message back. Used for testing.", Schema: json.RawMessage(`{"type":"object","properties":{"msg":{"type":"string"}}}`)},
			{Name: "probe", Description: "probe a value", Schema: tool.NoArgsSchema},
			{Name: port.RunCodeTool, Description: "run_code"},
		},
		outs: map[string]string{"echo": "pong", "probe": "42"},
	}
}

func runCode(t *testing.T, inner port.ToolRunner, specs func() []tool.Spec, code string) tool.Result {
	t.Helper()
	res, err := New(Deps{Inner: inner, AllSpecs: specs}).Execute(context.Background(),
		tool.Call{ID: "c1", Name: port.RunCodeTool, Args: json.RawMessage(`{"code":` + quote(code) + `}`)})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	return res
}

func quote(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

// TestScriptReturnAndOrder 脚本返回值 JSON 化且 dict 保插入序（模型写的排序逻辑
// 不能被编码器按 key 重排——首跑实测里 lib/json 的坑）。
func TestScriptReturnAndOrder(t *testing.T) {
	res := runCode(t, runnerFixture(), specsFn(runnerFixture()), "return {\"b\": 1, \"a\": 2}")
	if !res.OK || res.Output != `{"b":1,"a":2}` {
		t.Fatalf("res = %+v, want {\"b\":1,\"a\":2}", res)
	}
}

// TestTopLevelForAndReturn 包函数让顶层 for/return 合法（D319：少一条方言失败面）。
func TestTopLevelForAndReturn(t *testing.T) {
	res := runCode(t, runnerFixture(), specsFn(runnerFixture()),
		"total = 0\nfor i in range(4):\n    total += i\nreturn total")
	if !res.OK || res.Output != "6" {
		t.Fatalf("res = %+v, want 6", res)
	}
}

// TestBindingShape 绑定收 kwargs → JSON 参数 → 同一 runtime；结果形状 {ok, output}。
func TestBindingShape(t *testing.T) {
	f := runnerFixture()
	res := runCode(t, f, specsFn(f), `r = tools.echo(msg="hi")`+"\n"+`return r["output"]`)
	if !res.OK || res.Output != `"pong"` {
		t.Fatalf("res = %+v, want \"pong\"", res)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.seen) != 1 || string(f.seen[0].Args) != `{"msg":"hi"}` {
		t.Fatalf("calls = %+v", f.seen)
	}
}

// TestBindingFailure 失败是结果字段，不抛异常：脚本分支看 ok。
func TestBindingFailure(t *testing.T) {
	f := runnerFixture()
	f.fail = map[string]string{"echo": "boom"}
	res := runCode(t, f, specsFn(f), `r = tools.echo(msg="x")`+"\n"+`return {"bad": r["ok"], "why": r["error"]}`)
	if !res.OK || res.Output != `{"bad":false,"why":"boom"}` {
		t.Fatalf("res = %+v", res)
	}
}

// TestParallelOrder parallel 按输入序返回（并发执行不乱序）。
func TestParallelOrder(t *testing.T) {
	f := runnerFixture()
	f.outs["probe"] = ""
	f.specs[1].Description = "probe returns its call echo"
	f.delay = 10 * time.Millisecond
	res := runCode(t, f, specsFn(f), `rs = tools.parallel([{"tool": "echo", "args": {"msg": "1"}}, {"tool": "echo", "args": {"msg": "2"}}, {"tool": "echo", "args": {"msg": "3"}}])`+"\n"+`return [r["output"] for r in rs]`)
	if !res.OK || res.Output != `["pong","pong","pong"]` {
		t.Fatalf("res = %+v", res)
	}
}

// TestParseErrorLine 解析错带行列号，且行号映射回原脚本（包装头占一行）。
func TestParseErrorLine(t *testing.T) {
	res := runCode(t, runnerFixture(), specsFn(runnerFixture()), "ok = = 1")
	if res.OK || !strings.Contains(res.Err, "script.star:1") {
		t.Fatalf("res = %+v, want script.star:1 报因", res)
	}
	// 第二行的语法错：包装前是第 2 行，报错也必须是 2（不是包装后的 3）。
	res = runCode(t, runnerFixture(), specsFn(runnerFixture()), "a = 1\nb = = 2")
	if res.OK || !strings.Contains(res.Err, "script.star:2") {
		t.Fatalf("res = %+v, want script.star:2", res)
	}
}

// TestStepsLimit 死循环被步数上限掐断（结果字段，不上抛）。
func TestStepsLimit(t *testing.T) {
	res := runCode(t, runnerFixture(), specsFn(runnerFixture()),
		"x = 0\nfor i in range(1000000000):\n    x += i\nreturn x")
	if res.OK || res.Err == "" {
		t.Fatalf("res = %+v, want 步数超限", res)
	}
}

// TestQuotaExceeded 单脚本调用配额（含失败计数）耗尽后中止脚本。
func TestQuotaExceeded(t *testing.T) {
	f := runnerFixture()
	res := runCode(t, f, specsFn(f), "for i in range(300):\n    tools.echo(x=i)\nreturn \"done\"")
	if res.OK || !strings.Contains(res.Err, "quota") {
		t.Fatalf("res = %+v, want 配额报因", res)
	}
}

// TestReturnNotJSONable 返回函数这类不可 JSON 化的值 → OK=false 结果字段。
func TestReturnNotJSONable(t *testing.T) {
	res := runCode(t, runnerFixture(), specsFn(runnerFixture()),
		"def f():\n    pass\nreturn f")
	if res.OK || !strings.Contains(res.Err, "JSON") {
		t.Fatalf("res = %+v", res)
	}
}

// TestRunCodeNotBound 脚本内不能再跑脚本：绑定面没有 run_code。
func TestRunCodeNotBound(t *testing.T) {
	res := runCode(t, runnerFixture(), specsFn(runnerFixture()), `return tools.run_code(code="x")`)
	if res.OK || !strings.Contains(res.Err, "run_code") {
		t.Fatalf("res = %+v", res)
	}
}

// TestMissingCode 缺 code 参数 → 明确报因（结果字段）。
func TestMissingCode(t *testing.T) {
	res, err := New(Deps{Inner: runnerFixture(), AllSpecs: specsFn(runnerFixture())}).
		Execute(context.Background(), tool.Call{ID: "c1", Name: port.RunCodeTool, Args: json.RawMessage(`{}`)})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if res.OK || !strings.Contains(res.Err, "code") {
		t.Fatalf("res = %+v", res)
	}
}

// TestCtxCancel 父 ctx 取消（Ctrl+C / 硬超时）打断解释器：取消走 error 通道上抛
// （Run 归类取消），不回 OK=false 让模型继续——D320 的口径。
func TestCtxCancel(t *testing.T) {
	f := runnerFixture()
	f.delay = 10 * time.Second // 调用在途时取消：解释器必须被打断而不是等满
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(30 * time.Millisecond)
		cancel()
	}()
	_, err := New(Deps{Inner: f, AllSpecs: specsFn(f)}).Execute(ctx,
		tool.Call{ID: "c1", Name: port.RunCodeTool,
			Args: json.RawMessage(`{"code":` + quote(`r = tools.probe()`+"\n"+`return r`) + `}`)})
	if err == nil || !strings.Contains(err.Error(), "canceled") {
		t.Fatalf("err = %v, want 取消上抛", err)
	}
}

// TestSpecDescription 工具描述：方言限制与预算数字在场、stub 含动态工具、不含 run_code。
func TestSpecDescription(t *testing.T) {
	f := runnerFixture()
	spec := New(Deps{Inner: f, AllSpecs: specsFn(f)}).Spec()
	if spec.Name != port.RunCodeTool || spec.Risk != tool.Confirm {
		t.Fatalf("spec = %+v", spec)
	}
	for _, want := range []string{"No while", "try/except", "tools.echo(", "tools.probe(", "parallel"} {
		if !strings.Contains(spec.Description, want) {
			t.Fatalf("描述缺 %q:\n%s", want, spec.Description)
		}
	}
	if strings.Contains(spec.Description, "tools.run_code(") {
		t.Fatal("描述不该给 run_code 绑定")
	}
}

// TestDescribeEchoesScript 确认面回显程序全文；超长截断标注原长。
func TestDescribeEchoesScript(t *testing.T) {
	spec := New(Deps{Inner: runnerFixture(), AllSpecs: specsFn(runnerFixture())}).Spec()
	d := spec.Describe(json.RawMessage(`{"code":"return 1"}`))
	if d != "return 1" {
		t.Fatalf("describe = %q", d)
	}
	long := strings.Repeat("x", maxDescribe+10)
	d = spec.Describe(json.RawMessage(`{"code":` + quote(long) + `}`))
	if !strings.Contains(d, "script truncated") {
		t.Fatal("超长 describe 没有截断标注")
	}
}

// TestParallelBadShape 2026-11-22 实测回归：条目抄了别家 harness 的键名（缺 "tool"）
// 时不得把空名送进 callOne（那会报七次一样的 unknown tool ""，读不出形状错在哪），
// 而是每条回一句可照改的形状错误；描述文本里也钉了完整示例。
func TestParallelBadShape(t *testing.T) {
	f := runnerFixture()
	res := runCode(t, f, specsFn(f), `rs = tools.parallel([{"recipient_name": "tools.term_exec", "parameters": {"command": "dir"}}])`+"\n"+`return rs[0]["error"]`)
	if !res.OK || !strings.Contains(res.Output, "parallel entry needs") || strings.Contains(res.Output, "unknown tool") {
		t.Fatalf("res = %+v, want 形状报因", res)
	}
	// 描述里钉住形状示例（模型照抄即对）。
	desc := New(Deps{Inner: f, AllSpecs: specsFn(f)}).Spec().Description
	for _, want := range []string{`"tool": "term_exec"`, "bare tool name", "never \"tools.term_exec\""} {
		if !strings.Contains(desc, want) {
			t.Fatalf("描述缺 %q", want)
		}
	}
}
