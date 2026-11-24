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
	"bytes"
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
	// Overrides 放行策略例外（D280）；nil = 全按矩阵。优先级：例外 > 矩阵 > 缺省 ask。
	Overrides func() perm.Overrides
	// Permit 把一条「以后都允许」写进策略表并落审计（D282）：范围由本包按
	// perm.AutoAllow 算好，写入磁盘与审计是装配根的事（装配根持有配置与审计文件）。
	// nil = 没有可写的表（测试、未装配）：本次照常放行，只是什么也不记。
	Permit func(perm.Override, port.ConfirmRequest)
	// Sensitive 硬名单的敏感根（密钥目录、配置目录、系统目录，D280 第 6 条）：
	// 目标落在这里时，即便策略表设了自动放行也照问。
	Sensitive []string
	// SandboxPath 特权目录（D22 路径格的 inSandbox 判定基准）。
	SandboxPath string
	// Timeout 缺省单次工具执行超时（可被保留参数 timeout_sec 逐次覆盖，D38）；<=0 = 不限。
	Timeout time.Duration
	// MaxOutput 结果裁剪上限（rune）；<=0 = 不裁剪。
	MaxOutput int
	// PTC 程序化工具调用的呈现模式（D319，实验性）：Specs 只出 run_code 一个声明，
	// 其余工具声明不发但执行面照常可达（脚本绑定回到 Execute）。run_code 豁免
	// disabled 清单——关掉 ptc 的正确方式是不开 mode。
	PTC bool
}

// Runner port.ToolRunner 实现（注册表 + 判定 + 执行门面）。
// mu 保护注册表：装配根的插件刷新回调可能来自崩溃重启 goroutine，
// 与 REPL 主循环的 Specs/Execute 并发（§10；M4 接入）。
type Runner struct {
	mu     sync.Mutex
	tools  []port.Tool
	byName map[string]port.Tool
	order  []string
	// sets 会话感知的工具源（D340，插件工具面）：不进 byName，因为它们的名字按会话
	// 解析、逐轮可变；Specs 时合并、Execute 时按 ctx 查找。先加的优先。
	sets []port.ToolSet
	// disabled 只在 mu 下整体替换、绝不原地改（SetDisabled 建新表换指针，Specs/Execute
	// 锁外读一份快照）：读的一侧不加锁就安全，靠的就是这条不变量。要改就得复制整张表，
	// `r.disabled[name] = true` 会直接变成数据竞争。
	disabled  map[string]bool
	conf      port.Confirmer
	level     func() perm.Level
	overrides func() perm.Overrides
	permit    func(perm.Override, port.ConfirmRequest)
	sensitive []string
	sandbox   string
	timeout   time.Duration
	maxOut    int
	ptc       bool
}

var (
	_ port.ToolRunner = (*Runner)(nil)
)

// New 创建 Runner 并注册初始工具。
func New(opts Options) *Runner {
	r := &Runner{
		byName:    map[string]port.Tool{},
		disabled:  map[string]bool{},
		conf:      opts.Confirmer,
		level:     opts.Level,
		overrides: opts.Overrides,
		permit:    opts.Permit,
		sensitive: append([]string(nil), opts.Sensitive...),
		sandbox:   opts.SandboxPath,
		timeout:   opts.Timeout,
		maxOut:    opts.MaxOutput,
		ptc:       opts.PTC,
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
// t.Spec() 在锁外取：Spec 允许回头读注册表（run_code 的声明要按全量清单生成 stub，
// D319），锁内调就是自己等自己。
func (r *Runner) Add(t port.Tool) {
	if t == nil {
		return
	}
	name := t.Spec().Name
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.byName[name]; !ok {
		r.order = append(r.order, name)
	}
	r.byName[name] = t
	r.tools = toolsOf(r.order, r.byName)
}

// AddSet 挂一个工具源（插件启用时）。源必须是指针类型：注销靠接口值相等，值类型
// 里带 map/slice 会让比较直接 panic。
func (r *Runner) AddSet(s port.ToolSet) {
	if s == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sets = append(r.sets, s)
}

// RemoveSet 摘掉一个工具源（插件停用时）。不在表里则无操作。
func (r *Runner) RemoveSet(s port.ToolSet) {
	if s == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for i, got := range r.sets {
		if got == s {
			r.sets = append(r.sets[:i], r.sets[i+1:]...)
			return
		}
	}
}

// SetSources 当前工具源快照（锁外按它逐个问，避免在插件调用期间持锁）。
func (r *Runner) SetSources() []port.ToolSet {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]port.ToolSet(nil), r.sets...)
}
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

// Specs 返回模型可见的工具声明（按注册序）。呈现模式（D319）：native 全量（禁用集之外的，
// S6/D143）；ptc 只出 run_code 一个声明——其余工具声明不发，但 Execute 照常可达（脚本绑定）。
func (r *Runner) Specs(ctx context.Context) ([]tool.Spec, error) {
	if r.ptc {
		r.mu.Lock()
		t, ok := r.byName[port.RunCodeTool]
		r.mu.Unlock()
		if !ok {
			return nil, nil
		}
		// PTC 模式下模型面只出 run_code：工具源是「按轮导航」的形态，脚本里也用不上，
		// 而且它们会让 stub 生成多算一遍全量清单。
		return []tool.Spec{t.Spec()}, nil
	}
	base, err := r.AllSpecs(ctx)
	if err != nil {
		return nil, err
	}
	sets := r.SetSources()
	if len(sets) == 0 {
		return base, nil
	}
	seen := make(map[string]bool, len(base))
	for _, s := range base {
		seen[s.Name] = true
	}
	for _, set := range sets {
		// 源的声明按 ctx 的会话算：同一个插件在不同会话能给出不同的工具面（D340）。
		specs, err := set.Specs(ctx)
		if err != nil {
			return nil, fmt.Errorf("tool set %q: %w", set.Name(), err)
		}
		for _, s := range specs {
			if seen[s.Name] {
				continue // 内置优先：同名时插件的那一条被丢弃（契约 §4 工具面）
			}
			seen[s.Name] = true
			base = append(base, s)
		}
	}
	return base, nil
}

// AllSpecs 返回全量工具声明（禁用集之外，**不含 run_code**）——呈现模式的 stub 生成
// 与设置窗管理面用。run_code 豁免 disabled、不可被脚本调用，也不该进任何管理面清单；
// 排除它还是递归防线：它的 Spec 要按这份清单生成 stub，锁内互调会绕回自身（D319）。
func (r *Runner) AllSpecs(context.Context) ([]tool.Spec, error) {
	r.mu.Lock()
	tools := append([]port.Tool(nil), r.tools...)
	disabled := r.disabled
	runCode := r.byName[port.RunCodeTool]
	r.mu.Unlock()
	out := make([]tool.Spec, 0, len(tools))
	for _, t := range tools {
		if t == runCode {
			continue
		}
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
	static := ok
	disabled := r.disabled
	r.mu.Unlock()
	if !ok {
		// 工具源按 ctx 里的会话解析（D340）：名字只在可见集合内唯一，所以不进全局表。
		// 先加的优先，与 Specs 里的「同名保留第一条」同向。
		for _, set := range r.SetSources() {
			found, serr := set.Lookup(ctx, call.Name)
			if serr != nil {
				// 源自己出错：回填给模型（它已经点开了这个名字），不中断 Turn。
				return tool.Result{CallID: call.ID, OK: false,
					Err: fmt.Sprintf("tool %q: %v", call.Name, serr)}, nil
			}
			if found != nil {
				t, ok = found, true
				break
			}
		}
	}
	// 禁用集只管静态工具（S6/D143 设置窗的逐项开关）：工具源的名字按会话逐轮变化，
	// 逐名禁用没有意义，开关是插件自己的启停。
	if !ok || (static && disabled[call.Name] && call.Name != port.RunCodeTool) {
		// 禁用工具按 unknown 拒绝（S6/D143：与未注册不可区分——模型拿到的工具面一致）；
		// run_code 豁免（D319）：脚本调用面不受 disabled 约束，呈现模式的开关是 tools.mode。
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
	decision, target, op, hasOp := r.decide(ctx, t, call)
	// Assist 在决策模型接上之前（P4）与 Ask 同路：宁可多问一句，也不静默放行。
	if decision != perm.Allow {
		// 确认去重（D320）：脚本执行携带的 scope 里同指纹（工具名 + 操作 + 目标 +
		// 规范参数）只问一遍，命中复用上次结论；distinct 询问数超上限按超限回值。
		// native 路径无 scope（nil），行为与从前逐字节相同。
		scope := port.ConfirmScopeFrom(ctx)
		if scope != nil {
			fp := confirmFingerprint(call.Name, op, target, call.Args)
			if seen, allow, reason := scope.Recall(fp); seen {
				if allow {
					return r.executeAllowed(ctx, t, call, callTimeout)
				}
				return deniedResult(call, reason), nil
			}
			if ok, limit := scope.AskAllowed(); !ok {
				return tool.Result{CallID: call.ID, OK: false,
					Err: fmt.Sprintf("%s (%d per script); reduce distinct writes or reuse one call for repeated targets", port.ErrConfirmLimit, limit)}, nil
			}
			ans, err := r.confirmCall(ctx, t, call, spec, target, op, hasOp)
			if err != nil {
				return tool.Result{}, err
			}
			scope.Remember(fp, ans.Allow, strings.TrimSpace(ans.Reason))
			if !ans.Allow {
				return deniedResult(call, ans.Reason), nil
			}
			return r.executeAllowed(ctx, t, call, callTimeout)
		}
		if r.conf == nil {
			return tool.Result{}, fmt.Errorf("tool %s requires confirmation but no Confirmer is configured", call.Name)
		}
		ans, err := r.confirmCall(ctx, t, call, spec, target, op, hasOp)
		if err != nil {
			return tool.Result{}, err
		}
		if !ans.Allow {
			return deniedResult(call, ans.Reason), nil
		}
	}
	return r.executeAllowed(ctx, t, call, callTimeout)
}

// confirmFingerprint 确认去重的指纹：工具名 + 操作 + 目标 + 规范化参数 JSON。
// 参数经 json.Compact 去空白——同一组参数换个缩进也认得是同一次。
func confirmFingerprint(name string, op perm.Op, target string, args json.RawMessage) string {
	var canon bytes.Buffer
	if err := json.Compact(&canon, args); err != nil {
		canon.Reset()
		canon.Write(args) // 非法 JSON：按原文参与指纹，交给工具自己的参数报错
	}
	return name + "|" + op.String() + "|" + target + "|" + canon.String()
}

// deniedResult 确认拒绝的结果回填：拒绝原因原样回传（用户自填文本，同用户消息规则
// 不翻译）；200 rune 截断防失控粘贴——与 confirmPrompt 的参数预览同量级（D86）。
func deniedResult(call tool.Call, reason string) tool.Result {
	denied := "user denied " + call.Name
	if reason = strings.TrimSpace(reason); reason != "" {
		if r := []rune(reason); len(r) > 200 {
			reason = string(r[:200]) + "..."
		}
		denied += ": " + reason
	}
	return tool.Result{CallID: call.ID, OK: false, Err: denied}
}

// confirmCall 确认请求 + 直选档位重判 + 「以后都允许」写表（D282）——scope 去重与
// native 两条路共用，确认语义只有一份。
func (r *Runner) confirmCall(ctx context.Context, t port.Tool, call tool.Call,
	spec tool.Spec, target string, op perm.Op, hasOp bool) (port.ConfirmAnswer, error) {
	req := port.ConfirmRequest{
		Origin:  port.OriginTool,
		Tool:    call.Name,
		Op:      op,
		HasOp:   hasOp,
		Target:  target,
		Risk:    spec.Risk,
		HasRisk: true,
		Prompt:  confirmPrompt(call, target, spec),
		// 硬名单里的目标放宽了也不会生效（decideAt 最后那一步照样降回询问），
		// 所以不提供这个钮，而不是让用户点了之后发现还在问。
		CanPersist: !perm.HardTarget(target, r.sensitive),
	}
	ans, err := r.conf.Confirm(ctx, req)
	if err != nil {
		return port.ConfirmAnswer{}, fmt.Errorf("confirm %s: %w", call.Name, err)
	}
	if lv := strings.TrimSpace(ans.Level); lv != "" {
		// D282：用户在确认框里直选了档位。UI 只上报选了哪档，放不放行在这里算——
		// 界面拿不到目标路径与 sandbox 判定，两套界面各写一遍矩阵迟早不一致。
		allowed := false
		if lvl, perr := perm.Parse(lv); perr == nil {
			d, _, _, _ := r.decideAt(ctx, t, call, lvl)
			allowed = d == perm.Allow
		}
		if allowed {
			ans = port.ConfirmAnswer{Allow: true}
		} else {
			// 新档位判 Ask（或档名解析不出）：回到确认态，这一次仍要明确回答，
			// **不替用户放行**。第二轮的 Level 不再重判——否则用户与新档位的分歧
			// 会变成一个来回弹的圈，而用户其实只想对这一次表态。
			req.Prompt = i18n.Tf("tool.confirm.level_still_asks", "level", lv) + "\n" + req.Prompt
			if ans, err = r.conf.Confirm(ctx, req); err != nil {
				return port.ConfirmAnswer{}, fmt.Errorf("confirm %s: %w", call.Name, err)
			}
			ans.Level = ""
		}
	}
	// D282「以后都允许」：写策略表是**顺带**的，本次放行与写策略表无关——用户点的是允许，
	// 写失败也不是拒绝的理由（装配根那边只记日志与审计）。
	if ans.Persist {
		r.persistAllow(req)
	}
	return ans, nil
}

// executeAllowed 硬超时执行 + 结果裁剪（权限已过：Allow、确认放行或去重复用）。
//
// 硬超时执行（§10"工具执行同受 ctx 约束"；超时值 = Options 缺省、可被保留参数
// timeout_sec 逐次覆盖，D38）：部分工具（阻塞在慢盘/网络盘
// 上的文件 IO）不响应 ctx，故在旁路 goroutine 执行并 select 等待——超时立即回填
// OK=false 返回，不再等待底层调用；泄漏的 goroutine 随系统调用自行结束，其结果
// 写入带缓冲通道，无人接收也不阻塞。
func (r *Runner) executeAllowed(ctx context.Context, t port.Tool, call tool.Call, callTimeout time.Duration) (tool.Result, error) {
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
		return call, def, nil // 非 JSON 对象：按原样交给工具报工具自己的参数错误
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

// decide 权限判定：按当前权限档判（/permission 切换后即刻生效）。
func (r *Runner) decide(ctx context.Context, t port.Tool, call tool.Call) (perm.Decision, string, perm.Op, bool) {
	return r.decideAt(ctx, t, call, r.level())
}

// persistAllow 把「以后都允许」交给装配根（D282）：范围由 perm.AutoAllow 算——执行类
// 落工具级，文件类收窄到目标所在目录。本包只提供判定上下文，写入磁盘与审计不在这里。
func (r *Runner) persistAllow(req port.ConfirmRequest) {
	if r.permit == nil {
		return
	}
	r.permit(perm.AutoAllow(perm.OriginTool, req.Tool, req.Target, req.HasOp), req)
}

// decideAt 权限判定：文件类按路径格，执行类按工具列（D22）。
// 同时把判定依据交回给调用方——确认请求要带上操作、目标与风险，让界面与策略表
// 读结构体而不是从提示文本里反解。
//
// level 显式传入而不是读 r.level()：确认框里直选档位之后要按**刚选的**那一档重判本次
// （D282），而 /permission 的那次写回是异步的（经输入通道投递），等不到写回实现。
func (r *Runner) decideAt(ctx context.Context, t port.Tool, call tool.Call, level perm.Level) (perm.Decision, string, perm.Op, bool) {
	base := level.Tool(t.Spec().Risk)
	target := ""
	var op perm.Op
	var hasOp bool
	if ft, ok := t.(port.FileTarget); ok {
		if path, fileOp, ok := ft.Target(ctx, call); ok {
			base, target, op, hasOp = level.File(r.inSandbox(path), fileOp), path, fileOp, true
		}
		// 申报失败（如参数非法）：退回执行类按 Risk 兜底，宁可多问。
	}
	decision := base
	if r.overrides != nil {
		decision = r.overrides().Apply(base, perm.OriginTool, t.Spec().Name, target)
	}
	// 硬名单（D280 第 6 条）：策略表把这一条放宽到免确认，但目标属于密钥目录、
	// 配置目录、系统目录或执行控制类文件时，仍然问一次。
	if decision == perm.Allow && perm.HardTarget(target, r.sensitive) {
		decision = perm.Ask
	}
	return decision, target, op, hasOp
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
// 工具自己给了 Describe 就用 Describe——默认那行把原始参数截到 160 字，参数里带长文本的调用
// （delegate 的 task）会把真正要看的字段挤出屏幕。Describe 的输出**不截断**：输出短是工具
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
