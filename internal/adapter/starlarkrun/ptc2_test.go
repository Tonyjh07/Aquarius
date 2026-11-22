package starlarkrun

// ptc2_test.go PTC-2（D320）的形状锁定：脚本内确认去重（同参只问一次、拒绝也记）、
// distinct 确认上限回值不中止、describe 绑定不计配额、摘要日志三计数、自定义限额
// 生效且描述数字随取。借用真 toolrun 当 Inner（端到端验收允许，生产仍走端口）。

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"

	"github.com/Tonyjh07/Aquarius/internal/adapter/toolrun"
	"github.com/Tonyjh07/Aquarius/internal/domain/perm"
	"github.com/Tonyjh07/Aquarius/internal/domain/tool"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

// ptcFixture 真 toolrun + run_code：writer 是 Confirm 级（Strict 档逐次问）。外层
// run_code 本身也是一次确认（放行），writer 按 denyTool 决定放行/拒绝。
func ptcFixture(t *testing.T, denyTool string, lim Limits) (*toolrun.Runner, *countConf) {
	t.Helper()
	c := &countConf{denyTool: denyTool}
	r := toolrun.New(toolrun.Options{
		Confirmer: c,
		Level:     func() perm.Level { return perm.DefaultLevel },
		Tools: []port.Tool{
			stubFromSpec(tool.Spec{
				Name:        "writer",
				Description: "write a value",
				Schema:      json.RawMessage(`{"type":"object","properties":{"p":{"type":"integer"}}}`),
				Risk:        tool.Confirm,
			}, "written"),
		},
	})
	r.Add(New(Deps{Inner: r, AllSpecs: specsFnFromTools(r), Limits: lim}))
	return r, c
}

func specsFnFromTools(r *toolrun.Runner) func() []tool.Spec {
	return func() []tool.Spec {
		specs, _ := r.AllSpecs(context.Background())
		return specs
	}
}

func ptcScript(code string) tool.Call {
	return tool.Call{ID: "c1", Name: port.RunCodeTool, Args: json.RawMessage(`{"code":` + quote(code) + `}`)}
}

// TestPTC2DedupSameParams 脚本里同参写两次只确认一次；不同参各问各的。
func TestPTC2DedupSameParams(t *testing.T) {
	r, conf := ptcFixture(t, "", Limits{})
	res, err := r.Execute(context.Background(), ptcScript(
		`a = tools.writer(p=1)`+"\n"+`b = tools.writer(p=1)`+"\n"+`c = tools.writer(p=2)`+"\n"+`return [a["ok"], b["ok"], c["ok"]]`))
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !res.OK || res.Output != "[true,true,true]" {
		t.Fatalf("res = %+v", res)
	}
	if conf.n != 3 {
		t.Fatalf("确认次数 = %d, want 3（外层 run_code 一次 + 两个 distinct writer）", conf.n)
	}
}

// TestPTC2ConfirmLimit 超过 confirm_limit 的 distinct 确认回值，脚本继续、拿到部分结果。
func TestPTC2ConfirmLimit(t *testing.T) {
	r, conf := ptcFixture(t, "", Limits{ConfirmLimit: 2})
	res, err := r.Execute(context.Background(), ptcScript(
		`rs = []`+"\n"+`for i in range(4):`+"\n"+`    rs.append(tools.writer(p=i)["ok"])`+"\n"+`return rs`))
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	// limit=2：前 2 个 distinct 放行，第 3 个起被上限拦下（ok=false），脚本跑完。
	if !res.OK || res.Output != "[true,true,false,false]" {
		t.Fatalf("res = %+v, want 部分结果", res)
	}
	if conf.n != 3 {
		t.Fatalf("确认次数 = %d, want 3（外层一次 + 两个 distinct writer）", conf.n)
	}
}

// TestPTC2DedupDenyRemembered 拒绝也被记住：同参第二次不再弹窗（无人值守下尤其重要）。
func TestPTC2DedupDenyRemembered(t *testing.T) {
	r, conf := ptcFixture(t, "writer", Limits{})
	res, err := r.Execute(context.Background(), ptcScript(
		`a = tools.writer(p=1)`+"\n"+`b = tools.writer(p=1)`+"\n"+`return [a["ok"], b["ok"]]`))
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !res.OK || res.Output != "[false,false]" {
		t.Fatalf("res = %+v", res)
	}
	if conf.n != 2 {
		t.Fatalf("确认次数 = %d, want 2（外层一次 + writer 首问；拒绝同样去重）", conf.n)
	}
}

// TestPTC2DescribeBinding describe 给出单工具完整描述与参数表，不计调用配额。
func TestPTC2DescribeBinding(t *testing.T) {
	r, conf := ptcFixture(t, "", Limits{})
	res, err := r.Execute(context.Background(), ptcScript(
		`d = tools.describe("writer")`+"\n"+`return [d["ok"], d["params"]["p"]["type"], len(tools.describe("nope")["error"]) > 0]`))
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !res.OK || res.Output != `[true,"integer",true]` {
		t.Fatalf("res = %+v", res)
	}
	if conf.n != 1 {
		t.Fatalf("确认次数 = %d, want 1（外层一次；describe 纯读不问）", conf.n)
	}
}

// TestPTC2ScriptSummaryLog 摘要日志行带上 calls / dedup / capped；正常路径降 Debug
// （每次脚本都打 INFO 是噪音），触发确认上限才升 WARN。
func TestPTC2ScriptSummaryLog(t *testing.T) {
	var mu sync.Mutex
	var gotLv port.LogLevel
	var gotEvent, gotAct string
	var gotKv []any
	r, _ := ptcFixture(t, "", Limits{ConfirmLimit: 4}) // 不触上限，看正常路径的 debug 档
	// Log 回调经 Deps 注入到同一份 run_code（再 Add 一次覆盖注册；限额要带上，
	// 否则落缺省 8 不触发上限）。
	r.Add(New(Deps{Inner: r, AllSpecs: specsFnFromTools(r), Limits: Limits{ConfirmLimit: 4},
		Log: func(lv port.LogLevel, ctx, event, act string, kv ...any) {
			mu.Lock()
			defer mu.Unlock()
			gotLv, gotEvent, gotAct, gotKv = lv, event, act, kv
		}}))
	if _, err := r.Execute(context.Background(), ptcScript(
		`tools.writer(p=1)`+"\n"+`tools.writer(p=1)`+"\n"+`tools.writer(p=2)`+"\n"+`return "done"`)); err != nil {
		t.Fatalf("execute: %v", err)
	}
	mu.Lock()
	if gotLv != port.LogDebug || gotEvent != "log.boot.ptc_script" || gotAct != "log.act.ptc_script" {
		mu.Unlock()
		t.Fatalf("log = %s %s/%s, want debug 档摘要", gotLv, gotEvent, gotAct)
	}
	mu.Unlock()
	if len(gotKv) < 2 || gotKv[0] != "calls" || gotKv[1] != 3 {
		t.Fatalf("kv = %v, want calls=3", gotKv)
	}
}

// TestPTC2CappedSummaryWarn 确认上限触达后摘要升 WARN（用户需要看见调用为何被拒）。
func TestPTC2CappedSummaryWarn(t *testing.T) {
	var mu sync.Mutex
	var gotLv port.LogLevel
	var gotEvent string
	r, _ := ptcFixture(t, "", Limits{ConfirmLimit: 1})
	r.Add(New(Deps{Inner: r, AllSpecs: specsFnFromTools(r), Limits: Limits{ConfirmLimit: 1},
		Log: func(lv port.LogLevel, ctx, event, act string, kv ...any) {
			mu.Lock()
			defer mu.Unlock()
			gotLv, gotEvent = lv, event
		}}))
	if _, err := r.Execute(context.Background(), ptcScript(
		`tools.writer(p=1)`+"\n"+`tools.writer(p=2)`+"\n"+`tools.writer(p=3)`+"\n"+`return "done"`)); err != nil {
		t.Fatalf("execute: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if gotLv != port.LogWarn || gotEvent != "log.boot.ptc_script_capped" {
		t.Fatalf("log = %s %s, want warn 档 capped 事件", gotLv, gotEvent)
	}
}

// TestPTC2CustomLimits 自定义限额生效：配额降到 2，第三次调用配额耗尽中止脚本。
func TestPTC2CustomLimits(t *testing.T) {
	r, conf := ptcFixture(t, "", Limits{MaxCalls: 2})
	res, err := r.Execute(context.Background(), ptcScript(
		`for i in range(5):`+"\n"+`    tools.writer(p=i)`+"\n"+`return "unreachable"`))
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if res.OK || !strings.Contains(res.Err, "quota exceeded (2 calls") {
		t.Fatalf("res = %+v, want 自定义配额生效", res)
	}
	// Strict 档下前两次调用各问一次（不同参）。
	if conf.n != 3 {
		t.Fatalf("确认次数 = %d, want 3（外层一次 + 两个 distinct writer）", conf.n)
	}
}

// TestPTC2DescriptionCarriesLimits 工具描述里的预算数字随配置现取（run_code 的 Spec
// 直取——AllSpecs 不含 run_code，D319 的递归防线）。
func TestPTC2DescriptionCarriesLimits(t *testing.T) {
	r, _ := ptcFixture(t, "", Limits{})
	tc := New(Deps{Inner: r, AllSpecs: specsFnFromTools(r), Limits: Limits{MaxCalls: 4096, MaxParallel: 32}})
	desc := tc.Spec().Description
	if !strings.Contains(desc, "at most 4096 tool calls") || !strings.Contains(desc, "at most 32 concurrent") {
		t.Fatalf("描述没有随 Limits 现取:\n%s", desc)
	}
}

// countConf 记录次数的确认器：denyTool 指定的工具拒绝，其余放行（外层 run_code
// 要放行，脚本才能跑起来）。
type countConf struct {
	denyTool string
	n        int
}

func (c *countConf) Confirm(_ context.Context, req port.ConfirmRequest) (port.ConfirmAnswer, error) {
	c.n++
	return port.ConfirmAnswer{Allow: req.Tool != c.denyTool}, nil
}

// specStub 固定应答的工具替身（Inner 侧的 writer）。
type specStub struct {
	spec tool.Spec
	out  string
}

func (s *specStub) Spec() tool.Spec { return s.spec }

func (s *specStub) Execute(_ context.Context, _ tool.Call) (tool.Result, error) {
	return tool.Result{OK: true, Output: s.out}, nil
}

func stubFromSpec(spec tool.Spec, out string) port.Tool { return &specStub{spec: spec, out: out} }
