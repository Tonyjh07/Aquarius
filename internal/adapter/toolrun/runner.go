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
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
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
// mu 保护注册表：装配根的插件刷新回调可能来自崩溃重启 goroutine，
// 与 REPL 主循环的 Specs/Execute 并发（§10；M4 接入）。
type Runner struct {
	mu      sync.Mutex
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

// Add 注册工具（同名覆盖，注册序保持首次出现位置）；装配期与插件刷新期均可调。
func (r *Runner) Add(t port.Tool) {
	if t == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	name := t.Spec().Name
	if _, ok := r.byName[name]; !ok {
		r.order = append(r.order, name)
	}
	r.byName[name] = t
	r.tools = toolsOf(r.order, r.byName)
}

// Remove 注销工具（插件停用时按名移除；不存在则无操作）。
func (r *Runner) Remove(name string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.byName[name]; !ok {
		return
	}
	delete(r.byName, name)
	for i, n := range r.order {
		if n == name {
			r.order = append(r.order[:i], r.order[i+1:]...)
			break
		}
	}
	r.tools = toolsOf(r.order, r.byName)
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
	r.mu.Lock()
	tools := append([]port.Tool(nil), r.tools...)
	r.mu.Unlock()
	out := make([]tool.Spec, 0, len(tools))
	for _, t := range tools {
		out = append(out, t.Spec())
	}
	return out, nil
}

// Execute 查找 → 权限判定/确认 → 超时执行 → 结果裁剪（DESIGN §5.2）。
// 未知工具/确认拒绝/超时/工具自身报错均以 Result{OK:false} 回填（§10：失败不中断 Turn，
// 模型可自行纠正）；返回 error 仅限基础设施故障（确认器缺失/报错、父 ctx 取消）——
// Agent 对这类装配级错误快速失败上抛（§14 遗留修复）。
func (r *Runner) Execute(ctx context.Context, call tool.Call) (tool.Result, error) {
	r.mu.Lock()
	t, ok := r.byName[call.Name]
	r.mu.Unlock()
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

	// 硬超时执行（DESIGN §10"工具执行同受 ctx 约束"）：部分工具（阻塞在慢盘/网络盘
	// 上的文件 IO）不响应 ctx，故在旁路 goroutine 执行并 select 等待——超时立即回填
	// OK=false 返回，不再等待底层调用；泄漏的 goroutine 随系统调用自行结束，其结果
	// 写入带缓冲通道，无人接收也不阻塞。
	runCtx := ctx
	var cancel context.CancelFunc
	if r.timeout > 0 {
		runCtx, cancel = context.WithTimeout(ctx, r.timeout)
		defer cancel()
	}
	type outcome struct {
		res tool.Result
		err error
	}
	done := make(chan outcome, 1)
	go func() {
		res, err := t.Execute(runCtx, call)
		done <- outcome{res, err}
	}()
	var got outcome
	select {
	case got = <-done:
	case <-runCtx.Done():
		// 竞态：结果恰已就绪则优先采用（避免把刚好成功的调用误判为超时/取消）。
		select {
		case got = <-done:
		default:
			if ctx.Err() != nil { // 父 ctx 取消（Ctrl+C）原样上抛
				return tool.Result{}, ctx.Err()
			}
			return timeoutResult(call, r.timeout), nil
		}
	}
	if got.err != nil {
		// 与超时同时到达的错误：按 runCtx 判定归类（工具收到的是本次 deadline）。
		if errors.Is(runCtx.Err(), context.DeadlineExceeded) && ctx.Err() == nil {
			return timeoutResult(call, r.timeout), nil
		}
		if ctx.Err() != nil {
			// 父 ctx 取消（Ctrl+C）：装配级错误原样上抛，Agent 快速中止 Turn。
			return tool.Result{}, ctx.Err()
		}
		// 工具自身报错（参数非法/启动失败等模型可纠正的问题）：按 §10 转
		// OK=false 照常回填让模型自行纠正——error 通道只留给基础设施故障
		// （确认器缺失/报错、父 ctx 取消），Agent 据此快速失败（§14）。
		// 错误文本同样过裁剪（§9：工具结果统一受 tool_output_chars 约束）。
		return tool.Result{CallID: call.ID, OK: false, Err: trim(got.err.Error(), r.maxOut)}, nil
	}
	res := got.res
	if res.CallID == "" {
		res.CallID = call.ID
	}
	// 结果裁剪（§9：工具结果统一截断 tool_output_chars）。
	res.Output = trim(res.Output, r.maxOut)
	res.Err = trim(res.Err, r.maxOut)
	return res, nil
}

// timeoutResult 超时的结果回填（§10：超时是工具级失败，不中断 Turn）。
func timeoutResult(call tool.Call, d time.Duration) tool.Result {
	return tool.Result{
		CallID: call.ID,
		OK:     false,
		Err:    fmt.Sprintf("工具执行超时（%s）", d),
	}
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
// 判定前先做大小写规整（evalExisting），再逐组件解析全部链接组件
// （evalPath：符号链接与 Windows 目录 junction，含悬空链接）——
// sandbox 内链到外部的链接不落特权格，外部链进 sandbox 的落点在特权格内；
// 尚不存在的尾部按字面拼回（file_write 写新文件仍要命中）。
// 任何无法判定的情形（权限错误、链接目标读不出、层数过深）一律判为不在
// sandbox（fail-closed，宁可多问）——否则 D22"矩阵外一律逐次确认"可被链接绕过。
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
	abs, err = evalPath(evalExisting(abs))
	if err != nil {
		return false
	}
	base, err = evalPath(evalExisting(base))
	if err != nil {
		return false
	}
	abs, base = filepath.Clean(abs), filepath.Clean(base)
	return abs == base || strings.HasPrefix(abs, base+string(filepath.Separator))
}

// maxLinkDepth 链接展开层数上限（防循环/爆栈；超出返回错误即 fail-closed）。
const maxLinkDepth = 8

// evalPath 逐组件解析路径中的全部链接（symlink 与 Windows 目录 junction）：
// 命中链接即展开为目标组件重新入列（绝对目标重置到根；相对目标基于链接父目录），
// 真不存在的组件把剩余尾部按字面拼回（新建文件场景），无法判定时报错。
// 入参须为绝对化路径；组件可能因链接展开被重复处理，故以层数上限兜底。
func evalPath(p string) (string, error) {
	vol := filepath.VolumeName(p)
	sep := string(filepath.Separator)
	rest := strings.TrimPrefix(p[len(vol):], sep)
	var names []string
	if rest != "" {
		names = strings.Split(rest, sep)
	}
	out := vol + sep
	depth := 0
	for len(names) > 0 {
		name := names[0]
		names = names[1:]
		if name == "" || name == "." {
			continue
		}
		next := filepath.Join(out, name)
		link, err := isSymlink(next)
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				// 组件不存在：其后不可能再有可达组件，剩余按字面拼回。
				out = next
				for _, tail := range names {
					out = filepath.Join(out, tail)
				}
				return out, nil
			}
			return "", fmt.Errorf("解析 %s: %w", next, err) // 权限等：无法判定 → fail-closed
		}
		if !link {
			out = next
			continue
		}
		depth++
		if depth > maxLinkDepth {
			return "", fmt.Errorf("链接层数超过 %d（疑似循环）: %s", maxLinkDepth, p)
		}
		target, err := os.Readlink(next)
		if err != nil {
			return "", fmt.Errorf("读取链接目标 %s: %w", next, err)
		}
		if !filepath.IsAbs(target) {
			target = filepath.Join(out, target) // 相对目标基于链接的父目录
		}
		target = filepath.Clean(target)
		tvol := filepath.VolumeName(target)
		troot := tvol + sep
		out = troot
		if trest := strings.TrimPrefix(target[len(troot):], sep); trest != "" {
			names = append(strings.Split(trest, sep), names...)
		}
	}
	return out, nil
}

// evalExisting 大小写/分隔符规整：对最长存在前缀求 EvalSymlinks（Windows 上会
// 规范成磁盘实际大小写），尚不存在的尾部原样拼回。只做规整——链接（含 junction
// 与悬空链接）的解析由 evalPath 负责，本函数解析失败时原样返回。
func evalExisting(p string) string {
	orig := p
	var tail []string
	for {
		resolved, err := filepath.EvalSymlinks(p)
		if err == nil {
			return filepath.Join(append([]string{resolved}, tail...)...)
		}
		parent := filepath.Dir(p)
		if parent == p {
			return orig // 到根仍解析不了：保持原样
		}
		tail = append([]string{filepath.Base(p)}, tail...)
		p = parent
	}
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
