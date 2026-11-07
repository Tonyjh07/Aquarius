// Package toolrun 实现 port.ToolRunner（§5.2 / D25）：
// 查找 + 权限矩阵判定 + Risk 确认 + 超时 + 结果裁剪——工具执行的统一门面。
//
// 权限判定（D22 两列分工，纯策略在 domain/perm）：
//   - 文件类：工具实现 port.FileTarget 自申报 (path, op) → level.File(inSandbox(path), op)
//     只看路径格，Risk 不叠加抬高；
//   - 执行类：未申报者 → level.Tool(spec.Risk) 只看工具列；
//   - 判定为 Ask → 经 port.Confirmer 逐次确认，拒绝回填 OK=false（§10）。
//
// 横切的重试（含退避）/审计是装配根的装饰器（D14/M4），不在本包。
package toolrun

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/Tonyjh07/Aquarius/internal/adapter/pathres"
	"github.com/Tonyjh07/Aquarius/internal/domain/perm"
	"github.com/Tonyjh07/Aquarius/internal/domain/tool"
	"github.com/Tonyjh07/Aquarius/internal/i18n"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

// Options ToolRunner 装配选项。
type Options struct {
	// Tools 初始工具集（可后经 Add 注册，如 agent 的 context_compact）。
	Tools []port.Tool
	// Disabled 内置工具禁用集（S6/D143，config tools.disabled 推导）：Specs 过滤、
	// Execute 对禁用名按 unknown 拒绝（与未注册不可区分）；可经 SetDisabled 热更新。
	Disabled map[string]bool
	// Confirmer 逐次确认；nil 且判定为 Ask 时执行报错（fail-closed，不静默放行）。
	Confirmer port.Confirmer
	// Level 当前权限等级（func 以支持 /permission 运行时切换）；nil = perm.DefaultLevel。
	Level func() perm.Level
	// SandboxPath 特权目录（D22 路径格的 inSandbox 判定基准）。
	SandboxPath string
	// Timeout 缺省单次工具执行超时（可被保留参数 timeout_sec 逐次覆盖，D38）；<=0 = 不限。
	Timeout time.Duration
	// MaxOutput 结果裁剪上限（rune）；<=0 = 不裁剪。
	MaxOutput int
}

// Runner port.ToolRunner 实现（注册表 + 判定 + 执行门面）。
// mu 保护注册表：装配根的插件刷新回调可能来自崩溃重启 goroutine，
// 与 REPL 主循环的 Specs/Execute 并发（§10；M4 接入）。
type Runner struct {
	mu     sync.Mutex
	tools  []port.Tool
	byName map[string]port.Tool
	order  []string
	// disabled 只在 mu 下整体替换、绝不原地改（SetDisabled 建新表换指针，Specs/Execute
	// 锁外读一份快照）：读的一侧不加锁就安全，靠的就是这条不变量。要改就得复制整张表，
	// `r.disabled[name] = true` 会直接变成数据竞争。
	disabled map[string]bool
	conf     port.Confirmer
	level    func() perm.Level
	sandbox  string
	timeout  time.Duration
	maxOut   int
}

var (
	_ port.ToolRunner = (*Runner)(nil)
)

// New 创建 Runner 并注册初始工具。
func New(opts Options) *Runner {
	r := &Runner{
		byName:   map[string]port.Tool{},
		disabled: map[string]bool{},
		conf:     opts.Confirmer,
		level:    opts.Level,
		sandbox:  opts.SandboxPath,
		timeout:  opts.Timeout,
		maxOut:   opts.MaxOutput,
	}
	if r.level == nil {
		r.level = func() perm.Level { return perm.DefaultLevel }
	}
	for _, t := range opts.Tools {
		r.Add(t)
	}
	for name, on := range opts.Disabled {
		if on {
			r.disabled[name] = true
		}
	}
	return r
}

// SetDisabled 热更新内置工具禁用集（S6/D143：设置窗「工具与 MCP」页开关即时生效；
// 锁内整体替换，Specs/Execute 下帧可见）。空 map = 全部启用。
func (r *Runner) SetDisabled(disabled map[string]bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.disabled = make(map[string]bool, len(disabled))
	for name, on := range disabled {
		if on {
			r.disabled[name] = true
		}
	}
}

// Disabled 当前禁用集拷贝（S6 快照对照用）。
func (r *Runner) Disabled() map[string]bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make(map[string]bool, len(r.disabled))
	for name := range r.disabled {
		out[name] = true
	}
	return out
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

// Specs 返回全部工具声明（按注册序，供装配进 Prompt；禁用集之外的，S6/D143）。
func (r *Runner) Specs(context.Context) ([]tool.Spec, error) {
	r.mu.Lock()
	tools := append([]port.Tool(nil), r.tools...)
	disabled := r.disabled
	r.mu.Unlock()
	out := make([]tool.Spec, 0, len(tools))
	for _, t := range tools {
		if disabled[t.Spec().Name] {
			continue // 禁用工具不出现在模型可见的工具面（与未注册不可区分）
		}
		out = append(out, t.Spec())
	}
	return out, nil
}

// Execute 查找 → 权限判定/确认 → 超时执行 → 结果裁剪（§5.2）。
// 未知工具/确认拒绝/超时/工具自身报错均以 Result{OK:false} 回填（§10：失败不中断 Turn，
// 模型可自行纠正）；返回 error 仅限基础设施故障（确认器缺失/报错、父 ctx 取消）——
// Agent 对这类装配级错误快速失败上抛（§14 遗留修复）。
func (r *Runner) Execute(ctx context.Context, call tool.Call) (tool.Result, error) {
	r.mu.Lock()
	t, ok := r.byName[call.Name]
	disabled := r.disabled
	r.mu.Unlock()
	if !ok || disabled[call.Name] {
		// 禁用工具按 unknown 拒绝（S6/D143：与未注册不可区分——模型拿到的工具面一致）。
		return tool.Result{CallID: call.ID, OK: false, Err: fmt.Sprintf("unknown tool %q", call.Name)}, nil
	}

	// 单次调用超时覆盖（D38）：保留参数 timeout_sec（1–port.MaxCallTimeoutSec，允许高于
	// config 缺省）；schema 自声明该参数的工具（job_start）不参与保留参数、原样下传。
	// 值域外/非整数 → OK=false 回填让模型纠正（不静默夹取，避免 0 被误读为不限时）。
	// D184：声明 Interactive 的工具（ask_user）缺省不吃超时——等人应答不是「卡住」；
	// 模型显式给了 timeout_sec 仍照给（自己要求的期限要认）。
	defTimeout := r.timeout
	if it, ok := t.(port.Interactive); ok && it.Interactive() {
		defTimeout = 0
	}
	spec := t.Spec()
	call, callTimeout, terr := applyCallTimeout(spec, call, defTimeout)
	if terr != nil {
		return tool.Result{CallID: call.ID, OK: false, Err: terr.Error()}, nil
	}

	// 权限判定：工具声明的风险档与调用目标的路径档各管一列、取更严者（D22）。
	decision, target := r.decide(ctx, t, call)
	if decision == perm.Ask {
		if r.conf == nil {
			return tool.Result{}, fmt.Errorf("tool %s requires confirmation but no Confirmer is configured", call.Name)
		}
		ans, err := r.conf.Confirm(ctx, confirmPrompt(call, target, spec))
		if err != nil {
			return tool.Result{}, fmt.Errorf("confirm %s: %w", call.Name, err)
		}
		if !ans.Allow {
			// D86：拒绝原因原样回传（用户自填文本，同用户消息口径不翻译）；200 rune
			// 截断防失控粘贴——与 confirmPrompt 的参数预览同量级。
			denied := "user denied " + call.Name
			if reason := strings.TrimSpace(ans.Reason); reason != "" {
				if r := []rune(reason); len(r) > 200 {
					reason = string(r[:200]) + "..."
				}
				denied += ": " + reason
			}
			return tool.Result{CallID: call.ID, OK: false, Err: denied}, nil
		}
	}

	// 硬超时执行（§10"工具执行同受 ctx 约束"；超时值 = Options 缺省、可被保留参数
	// timeout_sec 逐次覆盖，D38）：部分工具（阻塞在慢盘/网络盘
	// 上的文件 IO）不响应 ctx，故在旁路 goroutine 执行并 select 等待——超时立即回填
	// OK=false 返回，不再等待底层调用；泄漏的 goroutine 随系统调用自行结束，其结果
	// 写入带缓冲通道，无人接收也不阻塞。
	runCtx := ctx
	var cancel context.CancelFunc
	if callTimeout > 0 {
		runCtx, cancel = context.WithTimeout(ctx, callTimeout)
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
			return timeoutResult(call, callTimeout), nil
		}
	}
	if got.err != nil {
		// 与超时同时到达的错误：按 runCtx 判定归类（工具收到的是本次 deadline）。
		if errors.Is(runCtx.Err(), context.DeadlineExceeded) && ctx.Err() == nil {
			return timeoutResult(call, callTimeout), nil
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
		Err:    fmt.Sprintf("tool execution timed out (%s)", d),
	}
}

// applyCallTimeout 解析保留参数 timeout_sec（D38）：
//   - schema 自声明该参数的工具（job_start 的任务超时语义）不参与保留参数，原样下传缺省超时；
//   - 非整数或值域外 → 错误（回填让模型纠正，不静默夹取——0 会被误读为不限时）；
//   - 命中时参数从 Args 剥离后下传（MCP 服务端不收未知参数）。
func applyCallTimeout(spec tool.Spec, call tool.Call, def time.Duration) (tool.Call, time.Duration, error) {
	if len(call.Args) == 0 {
		return call, def, nil
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(call.Args, &m); err != nil {
		return call, def, nil // 非 JSON 对象：按原样交给工具报它自己的参数错误
	}
	raw, hit := m["timeout_sec"]
	if !hit || specDeclaresTimeout(spec) {
		return call, def, nil
	}
	var sec int
	if err := json.Unmarshal(raw, &sec); err != nil {
		return call, 0, fmt.Errorf("invalid timeout_sec (want an integer 1-%d)", port.MaxCallTimeoutSec)
	}
	if sec < 1 || sec > port.MaxCallTimeoutSec {
		return call, 0, fmt.Errorf("timeout_sec out of range (want 1-%d), got %d", port.MaxCallTimeoutSec, sec)
	}
	delete(m, "timeout_sec")
	stripped, err := json.Marshal(m)
	if err != nil {
		return call, def, nil // map[string]json.RawMessage 恒可序列化；防御兜底走缺省
	}
	call.Args = stripped
	return call, time.Duration(sec) * time.Second, nil
}

// specDeclaresTimeout 工具 schema 是否自声明 timeout_sec（D38：自有语义不被保留参数截胡）。
func specDeclaresTimeout(spec tool.Spec) bool {
	var s struct {
		Properties map[string]json.RawMessage `json:"properties"`
	}
	if err := json.Unmarshal(spec.Schema, &s); err != nil {
		return false
	}
	_, ok := s.Properties["timeout_sec"]
	return ok
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

// inSandbox 判定路径是否位于特权目录内（含边界：/a/sandbox 不覆盖 /a/sandbox2）。
//
// 比较的是**解析后的真实落点**（pathres.Resolve：大小写规整 + 逐组件展开 symlink 与
// Windows junction，含悬空链接）——sandbox 内链到外部的链接不落特权格，外部链进
// sandbox 的落点在特权格内；尚不存在的尾部按字面拼回（file_write 写新文件仍要命中）。
// 任何无法判定的情形（权限错误、链接目标读不出、层数过深）一律判为不在 sandbox
// （fail closed，宁可多问）——否则 D22「矩阵外一律逐次确认」可被链接绕过。
//
// 判据与外部代理的根集闸门共用 pathres：两份实现迟早对链接/大小写给出不同答案。
func (r *Runner) inSandbox(path string) bool {
	if r.sandbox == "" || path == "" {
		return false
	}
	abs, err := pathres.Resolve(path)
	if err != nil {
		return false
	}
	base, err := pathres.Resolve(r.sandbox)
	if err != nil {
		return false
	}
	return pathres.Within(base, abs)
}

// confirmPrompt 确认提示：带目标路径与参数预览（用户看得见要允许什么）。
//
// 工具自己给了 Describe 就用它——默认那行把原始参数截到 160 字，参数里带长文本的调用
// （delegate 的 task）会把真正要看的字段挤出屏幕。Describe 的输出**不截断**：它短是工具
// 的责任，不是这里的。
func confirmPrompt(call tool.Call, target string, spec tool.Spec) string {
	if spec.Describe != nil {
		if summary := strings.TrimSpace(spec.Describe(call.Args)); summary != "" {
			if target != "" {
				return i18n.Tf("tool.confirm.described_target", "name", call.Name, "target", target, "summary", summary)
			}
			return i18n.Tf("tool.confirm.described", "name", call.Name, "summary", summary)
		}
	}
	args := strings.Join(strings.Fields(string(call.Args)), " ")
	if r := []rune(args); len(r) > 160 {
		args = string(r[:160]) + "..."
	}
	if target != "" {
		return i18n.Tf("tool.confirm.with_target", "name", call.Name, "target", target, "args", args)
	}
	return i18n.Tf("tool.confirm.plain", "name", call.Name, "args", args)
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
	return string(r[:max]) + fmt.Sprintf("\n...[output truncated, original %d chars]", len(r))
}
