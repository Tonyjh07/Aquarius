// Package toolrun 实现 port.ToolRunner（DESIGN §5.2 / D25）：
// 查找 + 权限矩阵判定 + Risk 确认 + 超时 + 结果裁剪——工具执行的统一门面。
//
// 权限判定（D22 两列分工，纯策略在 domain/perm）：
//   - 文件类：工具实现 port.FileTarget 自申报 (path, op) → level.File(inSandbox(path), op)
//     只看路径格，Risk 不叠加抬高；
//   - 执行类：未申报者 → level.Tool(spec.Risk) 只看工具列；
//   - 判定为 Ask → 经 port.Confirmer 逐次确认，拒绝回填 OK=false（§10）。
//
// 横切的重试/限流/审计是装配根的装饰器（D14/M4），不在本包。
package toolrun

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/Tonyjh07/Aquarius/internal/domain/perm"
	"github.com/Tonyjh07/Aquarius/internal/domain/tool"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

// Options ToolRunner 装配选项。
type Options struct {
	// Tools 初始工具集（可后经 Add 注册，如 agent 的 context_compact）。
	Tools []port.Tool
	// Confirmer 逐次确认；nil 且判定为 Ask 时执行报错（fail-closed，不静默放行）。
	Confirmer port.Confirmer
	// Level 当前权限等级（func 以支持 /permission 运行时切换）；nil = perm.DefaultLevel。
	Level func() perm.Level
	// SandboxPath 特权目录（D22 路径格的 inSandbox 判定基准）。
	SandboxPath string
	// Timeout 单次工具执行超时；<=0 = 不限。
	Timeout time.Duration
	// MaxOutput 结果裁剪上限（rune）；<=0 = 不裁剪。
	MaxOutput int
}

// Runner port.ToolRunner 实现（注册表 + 判定 + 执行门面）。
type Runner struct {
	tools   []port.Tool
	byName  map[string]port.Tool
	order   []string
	conf    port.Confirmer
	level   func() perm.Level
	sandbox string
	timeout time.Duration
	maxOut  int
}

var (
	_ port.ToolRunner = (*Runner)(nil)
)

// New 创建 Runner 并注册初始工具。
func New(opts Options) *Runner {
	r := &Runner{
		byName:  map[string]port.Tool{},
		conf:    opts.Confirmer,
		level:   opts.Level,
		sandbox: opts.SandboxPath,
		timeout: opts.Timeout,
		maxOut:  opts.MaxOutput,
	}
	if r.level == nil {
		r.level = func() perm.Level { return perm.DefaultLevel }
	}
	for _, t := range opts.Tools {
		r.Add(t)
	}
	return r
}

// Add 注册工具（同名覆盖，注册序保持首次出现位置）；装配期调用。
func (r *Runner) Add(t port.Tool) {
	if t == nil {
		return
	}
	name := t.Spec().Name
	if _, ok := r.byName[name]; !ok {
		r.order = append(r.order, name)
	}
	r.byName[name] = t
	r.tools = append(r.tools[:0:0], toolsOf(r.order, r.byName)...)
}

// toolsOf 按注册序物化当前工具列表。
func toolsOf(order []string, m map[string]port.Tool) []port.Tool {
	out := make([]port.Tool, 0, len(order))
	for _, n := range order {
		out = append(out, m[n])
	}
	return out
}

// Specs 返回全部工具声明（按注册序，供装配进 Prompt）。
func (r *Runner) Specs(context.Context) ([]tool.Spec, error) {
	out := make([]tool.Spec, 0, len(r.tools))
	for _, t := range r.tools {
		out = append(out, t.Spec())
	}
	return out, nil
}

// Execute 查找 → 权限判定/确认 → 超时执行 → 结果裁剪（DESIGN §5.2）。
// 未知工具/确认拒绝/超时均以 Result{OK:false} 回填（§10：失败不中断 Turn）；
// 返回 error 仅限基础设施故障（确认器缺失/报错、父 ctx 取消）。
func (r *Runner) Execute(ctx context.Context, call tool.Call) (tool.Result, error) {
	t, ok := r.byName[call.Name]
	if !ok {
		return tool.Result{CallID: call.ID, OK: false, Err: fmt.Sprintf("未知工具 %q", call.Name)}, nil
	}

	// 权限判定（D22 两列分工）。
	decision, target := r.decide(ctx, t, call)
	if decision == perm.Ask {
		if r.conf == nil {
			return tool.Result{}, fmt.Errorf("工具 %s 需要确认，但未配置 Confirmer", call.Name)
		}
		yes, err := r.conf.Confirm(ctx, confirmPrompt(call, target))
		if err != nil {
			return tool.Result{}, fmt.Errorf("确认 %s: %w", call.Name, err)
		}
		if !yes {
			return tool.Result{CallID: call.ID, OK: false, Err: "用户拒绝执行 " + call.Name}, nil
		}
	}

	// 超时执行（per-call 超时；父 ctx 取消原样上抛）。
	runCtx := ctx
	var cancel context.CancelFunc
	if r.timeout > 0 {
		runCtx, cancel = context.WithTimeout(ctx, r.timeout)
		defer cancel()
	}
	res, err := t.Execute(runCtx, call)
	if err != nil {
		if runCtx != ctx && errors.Is(runCtx.Err(), context.DeadlineExceeded) && ctx.Err() == nil {
			return tool.Result{
				CallID: call.ID,
				OK:     false,
				Err:    fmt.Sprintf("工具执行超时（%s）", r.timeout),
			}, nil
		}
		return tool.Result{}, err
	}
	if res.CallID == "" {
		res.CallID = call.ID
	}
	// 结果裁剪（§9：工具结果统一截断 tool_output_chars）。
	res.Output = trim(res.Output, r.maxOut)
	res.Err = trim(res.Err, r.maxOut)
	return res, nil
}

// decide 权限判定：文件类看路径格，执行类看工具列（D22）。
func (r *Runner) decide(ctx context.Context, t port.Tool, call tool.Call) (perm.Decision, string) {
	level := r.level()
	if ft, ok := t.(port.FileTarget); ok {
		if path, op, ok := ft.Target(ctx, call); ok {
			return level.File(r.inSandbox(path), op), path
		}
		// 申报失败（如参数非法）：退回执行类按 Risk 兜底，宁可多问。
	}
	return level.Tool(t.Spec().Risk), ""
}

// inSandbox 判定路径是否位于特权目录内（Abs+Clean 前缀比较；含边界：
// /a/sandbox 不覆盖 /a/sandbox2）。
func (r *Runner) inSandbox(path string) bool {
	if r.sandbox == "" || path == "" {
		return false
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return false
	}
	base, err := filepath.Abs(r.sandbox)
	if err != nil {
		return false
	}
	abs, base = filepath.Clean(abs), filepath.Clean(base)
	return abs == base || strings.HasPrefix(abs, base+string(filepath.Separator))
}

// confirmPrompt 确认提示：带目标路径与参数预览（用户看得见要允许什么）。
func confirmPrompt(call tool.Call, target string) string {
	args := strings.Join(strings.Fields(string(call.Args)), " ")
	if r := []rune(args); len(r) > 160 {
		args = string(r[:160]) + "…"
	}
	if target != "" {
		return fmt.Sprintf("允许 %s 访问 %s？参数: %s", call.Name, target, args)
	}
	return fmt.Sprintf("允许执行 %s？参数: %s", call.Name, args)
}

// trim 按 rune 截断并标注原长（空上限 = 不裁剪）。
func trim(s string, max int) string {
	if max <= 0 {
		return s
	}
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max]) + fmt.Sprintf("\n…[输出已截断，原文 %d 字符]", len(r))
}
