// Package starlarkrun 实现程序化工具调用的 run_code 工具（D319，实验性）：
// 模型写一段 Starlark 脚本，脚本内经绑定批量调工具，中间结果不进上下文，只把
// 最终结果交回。解释器是 go.starlark.net（进程内）：无 I/O 由语言保证、确定性步数
// 上限、编译增量最小（首跑通过率实测见 plan.md「程序化工具调用」节）。
//
// 三条不变量：
//   - 脚本内每次工具调用都回到同一个 tool runtime（Deps.Inner），权限矩阵、确认、
//     输出裁剪、超时与 native 档同一条链——脚本不能绕过权限；
//   - 失败一律是结果字段（OK=false + 带行列号的错误文本），不 panic、不上抛；
//   - 限额写死在常量里，并同时写进工具描述（模型看得见才会主动分批）。
package starlarkrun

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"

	"go.starlark.net/starlark"
	"go.starlark.net/starlarkstruct"

	"github.com/Tonyjh07/Aquarius/internal/domain/tool"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

// 缺省限额（D320：可配，tools.ptc 四键；缺省即 D319 首跑实测与验收用的那组）。
// 同一组数字出现在工具描述里（模型要看得见预算才会主动分批），随 Limits 现取。
const (
	// defaultMaxSteps 单脚本执行步数上限（首跑实测口径：死循环 200M 步内必被掐断）。
	defaultMaxSteps = 200_000_000
	// defaultMaxCalls 单脚本工具调用配额（含失败与参数非法——按尝试计，防止失败重试刷穿）。
	defaultMaxCalls = 256
	// defaultMaxParallel parallel 原语的并发上限。
	defaultMaxParallel = 10
	// defaultConfirmLimit 单脚本 distinct 确认上限（D320）。
	defaultConfirmLimit = 8
	// maxDescribe 确认面程序全文回显的截断点（确认弹窗要读到全文，超长标注原长）。
	maxDescribe = 8000
)

// Limits 脚本执行限额（D320，tools.ptc 配置；零值 = 全缺省）。
type Limits struct {
	MaxSteps     int
	MaxCalls     int
	MaxParallel  int
	ConfirmLimit int
}

func (l Limits) withDefaults() Limits {
	if l.MaxSteps <= 0 {
		l.MaxSteps = defaultMaxSteps
	}
	if l.MaxCalls <= 0 {
		l.MaxCalls = defaultMaxCalls
	}
	if l.MaxParallel <= 0 {
		l.MaxParallel = defaultMaxParallel
	}
	if l.ConfirmLimit <= 0 {
		l.ConfirmLimit = defaultConfirmLimit
	}
	return l
}

// Log 脚本摘要行（D320：事实落运行日志，审计不动）；nil = 不记（测试）。
type Log func(event, act string, kv ...any)

// Deps 装配选项。
type Deps struct {
	// Inner 脚本内调用的执行门面——就是注册了 run_code 的那个 toolrun.Runner
	// 自身：权限、确认、裁剪、超时全部复用（D319 决策 2）。
	Inner port.ToolRunner
	// AllSpecs 全量工具声明（toolrun.AllSpecs 的快照口），stub 生成用；含 MCP 动态
	// 工具。每次 Spec() 现取——插件刷新后下一帧声明就带上新工具。
	AllSpecs func() []tool.Spec
	// Limits 限额（tools.ptc；零值 = 全缺省）。
	Limits Limits
	// Log 运行日志回调（装配根给 w.logFrom 同款）；nil = 不记。
	Log Log
}

type ptcTool struct {
	d   Deps
	lim Limits
}

var _ port.Tool = (*ptcTool)(nil)

// New 创建 run_code 工具（装配根在 toolrun.Runner 建好后 Add 进去）。
func New(d Deps) port.Tool { return &ptcTool{d: d, lim: d.Limits.withDefaults()} }

// specParams 从工具 schema 取参数名（声明序不保证，排序后出 stub）。
func specParams(spec tool.Spec) []string {
	var s struct {
		Properties map[string]json.RawMessage `json:"properties"`
	}
	if err := json.Unmarshal(spec.Schema, &s); err != nil {
		return nil
	}
	names := make([]string, 0, len(s.Properties))
	for n := range s.Properties {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// firstSentence 描述压成一行：stub 每工具只给一句，丢细节是二段式 describe（PTC-2）的事。
func firstSentence(desc string) string {
	if i := strings.IndexByte(desc, '\n'); i >= 0 {
		desc = desc[:i]
	}
	if i := strings.Index(desc, ". "); i >= 0 {
		desc = desc[:i+1]
	}
	return strings.TrimSpace(desc)
}

// stubs 按全量声明生成每工具一行的绑定说明（模型可见，英文——D275 口径）。
func (t *ptcTool) stubs() string {
	specs := t.d.AllSpecs()
	lines := make([]string, 0, len(specs))
	for _, s := range specs {
		if s.Name == port.RunCodeTool {
			continue // 脚本内不能再跑脚本（递归既绕权限又无意义）
		}
		parts := make([]string, 0, len(specParams(s)))
		for _, p := range specParams(s) {
			parts = append(parts, p+"=...")
		}
		sig := strings.Join(parts, ", ")
		if sig == "" {
			sig = "()"
		} else {
			sig = "(" + sig + ")"
		}
		lines = append(lines, fmt.Sprintf("  tools.%s%s -> dict  # %s", s.Name, sig, firstSentence(s.Description)))
	}
	sort.Strings(lines)
	return strings.Join(lines, "\n")
}

// description 工具描述：固定头（方言限制 + 预算）+ 动态 stub 清单。英文代码字面量
// 进模型上下文（D275）；预算数字与限额常量同源（写死在这里，改一处两处一致）。
func (t *ptcTool) description() string {
	var b strings.Builder
	b.WriteString(`Run a Starlark script that calls tools in batch; intermediate results stay in the script and only the final return value goes back to the conversation.

Dialect: a subset of Python. No while, no try/except, no recursion, no top-level for/if (they are allowed inside functions — your whole program is wrapped in a function, so plain statements and return work). Loops use "for x in items". Deduplicate with dict keys, not sets.
Every tool result is a dict: {"ok": true, "output": "..."} or {"ok": false, "error": "..."}. Branch on "ok" — failed calls never raise.
Use tools.parallel to fan out; results come back in input order. Each entry is a dict with exactly two keys, "tool" (the bare tool name — "term_exec", never "tools.term_exec") and "args" (the tool's arguments dict):
  tools.parallel([
    {"tool": "term_exec", "args": {"command": "git status"}},
    {"tool": "file_read", "args": {"path": "README.md"}},
  ])
Budgets (hard): at most `)
	b.WriteString(strconv.Itoa(t.lim.MaxCalls))
	b.WriteString(` tool calls per script (counting failures), at most `)
	b.WriteString(strconv.Itoa(t.lim.MaxParallel))
	b.WriteString(` concurrent calls in parallel. Return a JSON-able value (dict/list/string/number/bool); print() lines are appended after it.

For full details of one tool (description and parameters) call tools.describe(name) — it does not count against the call budget.

Available tools:
`)
	b.WriteString(t.stubs())
	return b.String()
}

func (t *ptcTool) Spec() tool.Spec {
	return tool.Spec{
		Name:        port.RunCodeTool,
		Description: t.description(),
		Schema:      json.RawMessage(`{"type":"object","properties":{"code":{"type":"string","description":"Starlark program; its return value is the tool result"}},"required":["code"]}`),
		Risk:        tool.Confirm,
		Describe: func(args json.RawMessage) string {
			// 确认面回显程序全文（D319 决策 2：用户在程序开跑之前读到要允许什么）。
			var m struct {
				Code string `json:"code"`
			}
			if err := json.Unmarshal(args, &m); err != nil || strings.TrimSpace(m.Code) == "" {
				return ""
			}
			if r := []rune(m.Code); len(r) > maxDescribe {
				return string(r[:maxDescribe]) + fmt.Sprintf("\n...[script truncated, original %d chars]", len(r))
			}
			return m.Code
		},
	}
}

// wrap 把脚本包进函数体：顶层 for/if/return 因此合法（Go 层加一行头 + 整体缩进），
// 报错行号映射回原脚本（减一行）。
func wrap(code string) string {
	var b strings.Builder
	b.WriteString("def _run():\n")
	for _, line := range strings.Split(strings.ReplaceAll(code, "\r\n", "\n"), "\n") {
		if strings.TrimSpace(line) == "" {
			b.WriteString("\n")
			continue
		}
		b.WriteString("\t")
		b.WriteString(line)
		b.WriteString("\n")
	}
	return b.String()
}

// runtime 单次执行的活环境：共享配额、确认去重账本与输出收集。
type runtime struct {
	inner port.ToolRunner
	lim   Limits
	calls int
	scope *confirmScope
	mu    sync.Mutex
	print []string
}

// confirmScope 一次脚本执行的确认去重账本（D320）：同指纹只问一遍，distinct 询问
// 计数供 toolrun 判上限；命中复用与上限触达都计数，落脚本摘要日志。
type confirmScope struct {
	mu     sync.Mutex
	mem    map[string]scopeAnswer
	asked  int
	hits   int
	capped int
	limit  int
}

// scopeAnswer 上次确认的结论（指纹 → 放行/拒绝 + 拒绝原因）。
type scopeAnswer struct {
	allow  bool
	reason string
}

func newConfirmScope(limit int) *confirmScope {
	return &confirmScope{mem: map[string]scopeAnswer{}, limit: limit}
}

func (s *confirmScope) Recall(fp string) (bool, bool, string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ans, seen := s.mem[fp]
	if !seen {
		s.asked++ // distinct 询问计数：一个指纹只计一次
	} else {
		s.hits++
	}
	return seen, ans.allow, ans.reason
}

func (s *confirmScope) Remember(fp string, allow bool, reason string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.mem[fp] = scopeAnswer{allow: allow, reason: reason}
}

func (s *confirmScope) AskAllowed() (bool, int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.asked <= s.limit, s.limit
}

// Hits 同参复用次数；Capped 上限触达次数（摘要日志用）。
func (s *confirmScope) Hits() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.hits
}

func (s *confirmScope) CountCapped() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.capped++
}

func (s *confirmScope) Capped() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.capped
}

// bindResult 把 tool.Result 折成绑定形状 {"ok", "output"|"error"}。
func bindResult(res tool.Result) *starlark.Dict {
	d := &starlark.Dict{}
	if res.OK {
		d.SetKey(starlark.String("ok"), starlark.Bool(true))
		d.SetKey(starlark.String("output"), starlark.String(res.Output))
	} else {
		d.SetKey(starlark.String("ok"), starlark.Bool(false))
		err := res.Err
		if err == "" {
			err = "tool failed"
		}
		d.SetKey(starlark.String("error"), starlark.String(err))
	}
	return d
}

// callOne 一次脚本内调用：配额 → Inner.Execute → 绑定形状。失败不抛异常（契约：
// 分支看 ok 字段），只有配额耗尽返回错误让脚本立即中止。
func (rt *runtime) callOne(ctx context.Context, name string, args json.RawMessage) (starlark.Value, error) {
	rt.mu.Lock()
	rt.calls++
	over := rt.calls > rt.lim.MaxCalls
	rt.mu.Unlock()
	if over {
		return nil, fmt.Errorf("tool call quota exceeded (%d calls per script)", rt.lim.MaxCalls)
	}
	res, err := rt.inner.Execute(ctx, tool.Call{Name: name, Args: args})
	if err != nil {
		// 基础设施故障（父 ctx 取消、确认器缺失）按 §10 上抛，脚本立即中止。
		return nil, err
	}
	if !res.OK && strings.HasPrefix(res.Err, port.ErrConfirmLimit) {
		rt.scope.CountCapped() // 上限触达也是「按尝试计」的一部分
	}
	return bindResult(res), nil
}

// callFn 构造单个工具绑定：tools.<name>(k=v, ...) → JSON 对象 → Inner.Execute。
func callFn(ctx context.Context, rt *runtime, name string) starlark.Value {
	return starlark.NewBuiltin(name, func(t *starlark.Thread, b *starlark.Builtin,
		args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
		if len(args) > 0 {
			return nil, fmt.Errorf("%s takes keyword arguments only, got %d positional", name, len(args))
		}
		m := make(map[string]any, len(kwargs))
		for _, kv := range kwargs {
			m[string(kv[0].(starlark.String))] = plainValue(kv[1])
		}
		raw, err := json.Marshal(m)
		if err != nil {
			return nil, fmt.Errorf("%s: arguments are not JSON-able: %v", name, err)
		}
		return rt.callOne(ctx, name, raw)
	})
}

// parallelFn 数据驱动并行原语（D319 决策 4）：[{tool, args}, ...]，Go 宿主并发执行、
// 按序返回；闭包传不进来（starlark.Thread 非并发安全，脚本保持单线程）。
func parallelFn(ctx context.Context, rt *runtime) starlark.Value {
	return starlark.NewBuiltin("parallel", func(t *starlark.Thread, b *starlark.Builtin,
		args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
		var calls *starlark.List
		if err := starlark.UnpackPositionalArgs("parallel", args, kwargs, 1, &calls); err != nil {
			return nil, err
		}
		type slot struct {
			v   starlark.Value
			err error
		}
		out := make([]slot, calls.Len())
		sem := make(chan struct{}, rt.lim.MaxParallel)
		var wg sync.WaitGroup
		for i := 0; i < calls.Len(); i++ {
			entry, ok := calls.Index(i).(*starlark.Dict)
			if !ok {
				out[i] = slot{v: failDict("parallel entry must be a dict")}
				continue
			}
			name, serr := starlark.AsString(entryValue(entry, "tool"))
			// 空名也拦下（2026-11-22 实测：模型抄了别家 harness 的 recipient_name/
			// parameters 形状，缺 "tool" 键时 entryValue 回空串，漏判会把空名送进
			// callOne 报 "unknown tool \"\""——七次尝试全是同一句，读不出形状错在哪）。
			if !serr || name == "" || name == port.RunCodeTool {
				out[i] = slot{v: failDict(`parallel entry needs {"tool": <bare name>, "args": {...}} — the tool name without a "tools." prefix`)}
				continue
			}
			raw, jerr := json.Marshal(plainValue(entryValue(entry, "args")))
			if jerr != nil {
				out[i] = slot{v: failDict("parallel args are not JSON-able")}
				continue
			}
			wg.Add(1)
			sem <- struct{}{}
			go func(i int, name string, raw []byte) {
				defer wg.Done()
				defer func() { <-sem }()
				v, err := rt.callOne(ctx, name, raw)
				out[i] = slot{v: v, err: err}
			}(i, name, raw)
		}
		wg.Wait()
		list := make([]starlark.Value, 0, len(out))
		for _, s := range out {
			if s.err != nil {
				list = append(list, failDict(s.err.Error()))
				continue
			}
			list = append(list, s.v)
		}
		return starlark.NewList(list), nil
	})
}

// entryValue 取 dict 字段；缺省给空串，让 callFn 的形状检查兜底报因。
func entryValue(d *starlark.Dict, key string) starlark.Value {
	v, found, _ := d.Get(starlark.String(key))
	if !found {
		return starlark.String("")
	}
	return v
}

// describeFn 二段式目录（D320）：stub 每工具一行，细节按需展开——单个工具的完整
// 描述与参数表。纯读、无副作用，不计调用配额；未知名字回 ok=false 让模型换名再试。
func describeFn(specs []tool.Spec) starlark.Value {
	return starlark.NewBuiltin("describe", func(t *starlark.Thread, b *starlark.Builtin,
		args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
		var name string
		if err := starlark.UnpackPositionalArgs("describe", args, kwargs, 1, &name); err != nil {
			return nil, err
		}
		for _, s := range specs {
			if s.Name != name {
				continue
			}
			params := &starlark.Dict{}
			var sch struct {
				Properties map[string]struct {
					Type        string `json:"type"`
					Description string `json:"description"`
				} `json:"properties"`
			}
			if err := json.Unmarshal(s.Schema, &sch); err == nil {
				names := make([]string, 0, len(sch.Properties))
				for n := range sch.Properties {
					names = append(names, n)
				}
				sort.Strings(names)
				for _, n := range names {
					p := sch.Properties[n]
					row := &starlark.Dict{}
					row.SetKey(starlark.String("type"), starlark.String(p.Type))
					row.SetKey(starlark.String("description"), starlark.String(p.Description))
					params.SetKey(starlark.String(n), row)
				}
			}
			out := &starlark.Dict{}
			out.SetKey(starlark.String("ok"), starlark.Bool(true))
			out.SetKey(starlark.String("name"), starlark.String(s.Name))
			out.SetKey(starlark.String("description"), starlark.String(s.Description))
			out.SetKey(starlark.String("params"), params)
			return out, nil
		}
		return failDict("unknown tool: " + name), nil
	})
}

func failDict(msg string) *starlark.Dict {
	d := &starlark.Dict{}
	d.SetKey(starlark.String("ok"), starlark.Bool(false))
	d.SetKey(starlark.String("error"), starlark.String(msg))
	return d
}

// plainValue 把 Starlark 值折成可 JSON 化的 Go 值（不可化的降级成字符串表示）。
func plainValue(v starlark.Value) any {
	switch x := v.(type) {
	case starlark.NoneType:
		return nil
	case starlark.Bool:
		return bool(x)
	case starlark.Int:
		if i, ok := x.Int64(); ok {
			return i
		}
		return x.String()
	case starlark.Float:
		return float64(x)
	case starlark.String:
		return string(x)
	case *starlark.List:
		out := make([]any, 0, x.Len())
		for i := 0; i < x.Len(); i++ {
			out = append(out, plainValue(x.Index(i)))
		}
		return out
	case *starlark.Dict:
		out := map[string]any{}
		for _, item := range x.Items() {
			k, ok := item[0].(starlark.String)
			if !ok {
				continue // 非 string 键进不了 JSON 对象，丢弃
			}
			out[string(k)] = plainValue(item[1])
		}
		return out
	default:
		return x.String()
	}
}

// Execute：解析参数 → 包函数 → 受限执行 → 返回值 JSON 化。一切失败都回 OK=false。
func (t *ptcTool) Execute(ctx context.Context, call tool.Call) (tool.Result, error) {
	var m struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(call.Args, &m); err != nil || strings.TrimSpace(m.Code) == "" {
		return tool.Result{CallID: call.ID, OK: false,
			Err: "run_code needs a \"code\" argument (the Starlark program)"}, nil
	}

	// 确认去重账本（D320）：本脚本执行独立一份，经 ctx 带进 Inner.Execute——
	// toolrun 的确认点查重、判上限；native 路径没有这份账本，行为不变。
	rt := &runtime{inner: t.d.Inner, lim: t.lim, scope: newConfirmScope(t.lim.ConfirmLimit)}
	ctx = port.WithConfirmScope(ctx, rt.scope)
	pre := starlark.StringDict{
		"tools": toolsStruct(ctx, rt, t.d.AllSpecs()),
	}
	thread := &starlark.Thread{
		Name: "run_code",
		Print: func(_ *starlark.Thread, msg string) {
			rt.mu.Lock()
			rt.print = append(rt.print, msg)
			rt.mu.Unlock()
		},
	}
	thread.SetMaxExecutionSteps(uint64(t.lim.MaxSteps))
	// 墙钟/取消映射：父 ctx 取消（Ctrl+C）或 toolrun 硬超时 → 立即打断解释器。
	stop := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			thread.Cancel("canceled")
		case <-stop:
		}
	}()
	defer close(stop)

	// 包一层再编译：顶层 for/if/return 合法（D319：模型首版最常踩的就是方言边界，
	// 包掉一层少一条失败面）。报错行号减一映射回原脚本。
	src := wrap(m.Code)
	globals, err := starlark.ExecFile(thread, "script.star", []byte(src), pre)
	if err != nil {
		if ctx.Err() != nil { // 停止键/超时打断脚本：装配级取消上抛（§14），不是脚本失败
			return tool.Result{}, ctx.Err()
		}
		return t.finish(call, rt, tool.Result{CallID: call.ID, OK: false, Err: shiftLines(err.Error())}), nil
	}
	run, ok := globals["_run"]
	if !ok {
		return t.finish(call, rt, tool.Result{CallID: call.ID, OK: false, Err: "script produced no _run"}), nil
	}
	ret, err := starlark.Call(thread, run, nil, nil)
	if err != nil {
		if ctx.Err() != nil {
			return tool.Result{}, ctx.Err()
		}
		return t.finish(call, rt, tool.Result{CallID: call.ID, OK: false, Err: shiftLines(err.Error())}), nil
	}
	out, jerr := encodeOrdered(ret)
	if jerr != nil {
		return t.finish(call, rt, tool.Result{CallID: call.ID, OK: false,
			Err: fmt.Sprintf("return value is not JSON-able: %v", jerr)}), nil
	}
	if ret == starlark.None {
		out = "" // 空 return 走既有的 toolNoOutput 回填口径，不写一个裸 null
	}
	if logs := rt.snapshotPrints(); len(logs) > 0 {
		out = strings.Join(append(logs, out), "\n")
	}
	return t.finish(call, rt, tool.Result{CallID: call.ID, OK: true, Output: out}), nil
}

// finish 脚本摘要落运行日志（D320：calls / dedup / capped），结果原样返回。
func (t *ptcTool) finish(call tool.Call, rt *runtime, res tool.Result) tool.Result {
	if t.d.Log != nil {
		t.d.Log("log.boot.ptc_script", "log.act.ptc_script",
			"calls", rt.snapshotCalls(), "dedup", rt.scope.Hits(), "capped", rt.scope.Capped())
	}
	return res
}

func (rt *runtime) snapshotCalls() int {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	return rt.calls
}

func (rt *runtime) snapshotPrints() []string {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	return append([]string(nil), rt.print...)
}

// shiftLines 包装头的行号映射：报错文本里 script.star:N → N-1（首跑实测的反馈口径：
// 模型拿到的行列号要指回它写的源码）。
func shiftLines(msg string) string {
	var b strings.Builder
	for _, line := range strings.Split(msg, "\n") {
		if i := strings.Index(line, "script.star:"); i >= 0 {
			rest := line[i+len("script.star:"):]
			if j := strings.Index(rest, ":"); j >= 0 {
				if n, err := strconv.Atoi(rest[:j]); err == nil && n > 1 {
					line = line[:i] + "script.star:" + strconv.Itoa(n-1) + rest[j:]
				}
			}
		}
		b.WriteString(line)
		b.WriteString("\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

// toolsStruct 把全量工具名折成 Starlark struct 字段（脚本内 tools.<name>(...)）。
func toolsStruct(ctx context.Context, rt *runtime, specs []tool.Spec) starlark.Value {
	fields := starlark.StringDict{}
	for _, s := range specs {
		if s.Name == port.RunCodeTool {
			continue // 递归既绕权限又无意义
		}
		fields[s.Name] = callFn(ctx, rt, s.Name)
	}
	fields["parallel"] = parallelFn(ctx, rt)
	fields["describe"] = describeFn(specs)
	fields["describe"] = describeFn(specs)
	return starlarkstruct.FromStringDict(starlarkstruct.Default, fields)
}

// ---------- 返回值编码 ----------

// encodeOrdered 返回值的 JSON 化：dict 保插入序（lib/json.encode 会按键排序，
// 模型写的排序逻辑会被它掩盖——首跑实测里踩过）。
func encodeOrdered(v starlark.Value) (string, error) {
	var b strings.Builder
	if err := encVal(&b, v); err != nil {
		return "", err
	}
	return b.String(), nil
}

func encVal(b *strings.Builder, v starlark.Value) error {
	switch x := v.(type) {
	case starlark.NoneType:
		b.WriteString("null")
	case starlark.Bool:
		if bool(x) {
			b.WriteString("true")
		} else {
			b.WriteString("false")
		}
	case starlark.Int:
		b.WriteString(x.String())
	case starlark.Float:
		b.WriteString(strconv.FormatFloat(float64(x), 'g', -1, 64))
	case starlark.String:
		j, _ := json.Marshal(string(x))
		b.Write(j)
	case starlark.Tuple:
		b.WriteByte('[')
		for i, e := range x {
			if i > 0 {
				b.WriteByte(',')
			}
			if err := encVal(b, e); err != nil {
				return err
			}
		}
		b.WriteByte(']')
	case *starlark.List:
		b.WriteByte('[')
		for i := 0; i < x.Len(); i++ {
			if i > 0 {
				b.WriteByte(',')
			}
			if err := encVal(b, x.Index(i)); err != nil {
				return err
			}
		}
		b.WriteByte(']')
	case *starlark.Dict:
		b.WriteByte('{')
		for i, item := range x.Items() {
			if i > 0 {
				b.WriteByte(',')
			}
			if err := encVal(b, item[0]); err != nil {
				return err
			}
			b.WriteByte(':')
			if err := encVal(b, item[1]); err != nil {
				return err
			}
		}
		b.WriteByte('}')
	default:
		return fmt.Errorf("value of type %s is not JSON-able", v.Type())
	}
	return nil
}
