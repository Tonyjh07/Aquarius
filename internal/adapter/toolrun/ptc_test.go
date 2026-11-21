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
		Level:     levelFn(perm.Levels[len(perm.Levels)-1]),
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
