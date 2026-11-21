package toolrun

// ptc_test.go 呈现模式（D319）的行为锁定：ptc 档 Specs 只出 run_code、AllSpecs 全量、
// 未列入声明面的工具 Execute 照常可达（脚本绑定）、run_code 豁免 disabled 清单。

import (
	"context"
	"strings"
	"testing"

	"github.com/Tonyjh07/Aquarius/internal/domain/perm"
	"github.com/Tonyjh07/Aquarius/internal/domain/tool"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

func ptcRunner(t *testing.T, opts func(*Options)) *Runner {
	t.Helper()
	o := Options{
		Confirmer: &scriptConf{answers: []bool{true}},
		Level:     levelFn(perm.DefaultLevel),
	}
	opts(&o)
	return New(o)
}

// TestPTCSpecsOnlyRunCode ptc 档模型面只有 run_code；AllSpecs 是全量（管理面与 stub 用）。
func TestPTCSpecsOnlyRunCode(t *testing.T) {
	inner := execTool("probe", 0, "42")
	runCode := starlarkStub()
	r := ptcRunner(t, func(o *Options) {
		o.Tools = []port.Tool{inner, runCode}
		o.PTC = true
	})
	specs, err := r.Specs(context.Background())
	if err != nil {
		t.Fatalf("specs: %v", err)
	}
	if len(specs) != 1 || specs[0].Name != port.RunCodeTool {
		t.Fatalf("specs = %+v, want 只剩 run_code", specs)
	}
	all, err := r.AllSpecs(context.Background())
	if err != nil {
		t.Fatalf("allspecs: %v", err)
	}
	if len(all) != 1 || all[0].Name != "probe" {
		t.Fatalf("allspecs = %+v, want 1 个（不含 run_code）", all)
	}
}

// TestPTCExecuteUnlisted 脚本内调用不经声明面：未列入 Specs 的工具照常执行（D319 决策 2）。
func TestPTCExecuteUnlisted(t *testing.T) {
	inner := execTool("probe", 0, "42")
	r := ptcRunner(t, func(o *Options) {
		o.Tools = []port.Tool{inner, starlarkStub()}
		o.PTC = true
	})
	res, err := r.Execute(context.Background(), c("probe", `{}`))
	if err != nil || !res.OK || res.Output != "42" {
		t.Fatalf("res = %+v err = %v, want 未列入声明面的工具可达", res, err)
	}
}

// TestPTCRunCodeExemptDisabled run_code 豁免 disabled 清单；其余照旧按 unknown 拒。
func TestPTCRunCodeExemptDisabled(t *testing.T) {
	r := ptcRunner(t, func(o *Options) {
		o.Tools = []port.Tool{execTool("probe", 0, "42"), starlarkStub()}
		o.PTC = true
		o.Disabled = map[string]bool{"run_code": true, "probe": true}
	})
	res, err := r.Execute(context.Background(), c(port.RunCodeTool, `{"code":"return 1"}`))
	if err != nil || !res.OK {
		t.Fatalf("res = %+v err = %v, want run_code 豁免 disabled", res, err)
	}
	res, err = r.Execute(context.Background(), c("probe", `{}`))
	if err != nil || res.OK || !strings.Contains(res.Err, "unknown tool") {
		t.Fatalf("res = %+v err = %v, want probe 按 unknown 拒", res, err)
	}
}

// TestNativeSpecsUnchanged 不开 ptc：声明面是全量清单（禁用之外）——native 档的请求
// 形状由既有全量测试覆盖，这里点一例明确断言。
func TestNativeSpecsUnchanged(t *testing.T) {
	r := ptcRunner(t, func(o *Options) {
		o.Tools = []port.Tool{execTool("probe", 0, "42")}
	})
	specs, _ := r.Specs(context.Background())
	if len(specs) != 1 || specs[0].Name != "probe" {
		t.Fatalf("specs = %+v, want probe（native 全量）", specs)
	}
}

// starlarkStub 最小 run_code 替身：只提供名字与声明，行为不进本包测试。
func starlarkStub() port.Tool {
	return stubFromSpec(tool.Spec{Name: port.RunCodeTool, Risk: tool.Confirm}, "ok")
}

func stubFromSpec(spec tool.Spec, out string) port.Tool {
	return &stubTool{spec: spec, out: out}
}

// ---------------------------------------------------------------------------
// 确认去重（D320）
// ---------------------------------------------------------------------------

// scopeOf 造一个空账本挂进 ctx（starlarkrun 同款注入方式）。
func scopeOf() (context.Context, *mapScope) {
	return scopeOfLimit(8)
}

func scopeOfLimit(limit int) (context.Context, *mapScope) {
	s := &mapScope{mem: map[string]scopeRec{}, limit: limit}
	return port.WithConfirmScope(context.Background(), s), s
}

// mapScope 测试用账本（与 starlarkrun 的 confirmScope 同语义）。
type mapScope struct {
	mem   map[string]scopeRec
	asked int
	limit int
}

type scopeRec struct {
	allow  bool
	reason string
}

func (s *mapScope) Recall(fp string) (bool, bool, string) {
	r, seen := s.mem[fp]
	if !seen {
		s.asked++
	}
	return seen, r.allow, r.reason
}

func (s *mapScope) Remember(fp string, allow bool, reason string) {
	s.mem[fp] = scopeRec{allow, reason}
}

func (s *mapScope) AskAllowed() (bool, int) { return s.asked <= s.limit, s.limit }

// TestPTCDedupReusesAnswer 同参只问一遍：放行与拒绝两路都记，第二次不再过 Confirmer。
func TestPTCDedupReusesAnswer(t *testing.T) {
	for _, tc := range []struct {
		name    string
		answers []bool
		wantOK  bool
	}{
		{"放行复用", []bool{true, true}, true},
		{"拒绝复用", []bool{false, false}, false},
	} {
		conf := &scriptConf{answers: tc.answers, denyReason: "nope"}
		r := New(Options{Confirmer: conf, Level: levelFn(perm.DefaultLevel),
			Tools: []port.Tool{execTool("writer", tool.Confirm, "done")}})
		ctx, scope := scopeOf()
		call := c("writer", `{"path":"x"}`)
		r1, err := r.Execute(ctx, call)
		if err != nil {
			t.Fatalf("%s: first: %v", tc.name, err)
		}
		r2, err := r.Execute(ctx, call)
		if err != nil {
			t.Fatalf("%s: second: %v", tc.name, err)
		}
		if len(conf.asked) != 1 {
			t.Fatalf("%s: 确认次数 = %d, want 1（同参复用）", tc.name, len(conf.asked))
		}
		if r1.OK != tc.wantOK || r2.OK != tc.wantOK {
			t.Fatalf("%s: r1=%v r2=%v, want OK=%v", tc.name, r1.OK, r2.OK, tc.wantOK)
		}
		if !tc.wantOK && (!strings.Contains(r2.Err, "denied") || !strings.Contains(r2.Err, "nope")) {
			t.Fatalf("%s: 拒绝原因没有复用: %q", tc.name, r2.Err)
		}
		if scope.asked != 1 {
			t.Fatalf("%s: distinct = %d, want 1", tc.name, scope.asked)
		}
	}
}

// TestPTCDedupLimitReached distinct 询问超上限后回值（不中止脚本路径，Safe 级不受影响）。
func TestPTCDedupLimitReached(t *testing.T) {
	conf := &scriptConf{answers: []bool{true, true, true}}
	r := New(Options{Confirmer: conf, Level: levelFn(perm.DefaultLevel),
		Tools: []port.Tool{execTool("writer", tool.Confirm, "done"), execTool("probe", 0, "42")}})
	ctx, scope := scopeOfLimit(2)
	for i, args := range []string{`{"p":1}`, `{"p":2}`, `{"p":3}`} {
		res, err := r.Execute(ctx, c("writer", args))
		if err != nil {
			t.Fatalf("call %d: %v", i, err)
		}
		if i < 2 && !res.OK {
			t.Fatalf("call %d: 前 %d 次 distinct 应正常确认放行: %+v", i, i, res)
		}
		if i == 2 && (res.OK || !strings.Contains(res.Err, port.ErrConfirmLimit)) {
			t.Fatalf("call 2: res = %+v, want 上限报因", res)
		}
	}
	// Safe 级不受上限影响；scope 计数不变（不再产生新的 distinct 询问）。
	if res, err := r.Execute(ctx, c("probe", `{}`)); err != nil || !res.OK {
		t.Fatalf("probe = %+v err = %v, want 上限后 Safe 照常", res, err)
	}
	if scope.asked != 3 {
		// 被上限拦下的那次也按 distinct 尝试计入——asked 口径是「尝试过的不同请求」。
		t.Fatalf("distinct = %d, want 3（含被上限拦下的一次）", scope.asked)
	}
	if len(conf.asked) != 2 {
		t.Fatalf("确认次数 = %d, want 2", len(conf.asked))
	}
}

// TestNoScopeUnchanged 不带 scope（native 路径）：同样的重复调用逐次都问——去重只在脚本内。
func TestNoScopeUnchanged(t *testing.T) {
	conf := &scriptConf{answers: []bool{true, true}}
	r := New(Options{Confirmer: conf, Level: levelFn(perm.DefaultLevel),
		Tools: []port.Tool{execTool("writer", tool.Confirm, "done")}})
	call := c("writer", `{"path":"x"}`)
	for i := 0; i < 2; i++ {
		if _, err := r.Execute(context.Background(), call); err != nil {
			t.Fatalf("call %d: %v", i, err)
		}
	}
	if len(conf.asked) != 2 {
		t.Fatalf("确认次数 = %d, want 2（native 无去重）", len(conf.asked))
	}
}
