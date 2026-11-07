package toolbuiltin

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/Tonyjh07/Aquarius/internal/domain/tool"
	"github.com/Tonyjh07/Aquarius/internal/i18n"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

// delegateTool 把一件复杂任务**整个**交给外部 ACP 代理（§5.18/D244）。
//
// 它派完活立刻返回（D261）：转交跑十几到几十分钟，同步形态等于把这一轮钉在子代理上，
// 会话切不走、别的活一件干不了。要本轮就拿到结果的是 delegate_wait。
type delegateTool struct {
	jobs port.DelegateRunner
	list []port.DelegateInfo
}

func (t *delegateTool) Spec() tool.Spec {
	names := make([]string, 0, len(t.list))
	var lines []string
	for _, a := range t.list {
		names = append(names, a.Name)
		line := "- " + a.Name
		if a.Title != "" {
			line += " (" + a.Title + ")"
		}
		lines = append(lines, line)
	}
	desc := "Hand a whole complex task to an external ACP agent and return a task id immediately. " +
		"The agent runs as its own process with its own file and command access, so approving a delegation " +
		"means approving that program acting on its own judgement. Its report arrives later as a message in " +
		"this session; do NOT poll it. Use delegate_wait if you need the result in this turn, delegate_kill to stop it. " +
		"Available agents:\n" + strings.Join(lines, "\n")
	return tool.Spec{
		Name:        "delegate",
		Description: desc,
		Schema: jsonSchema(fmt.Sprintf(`{
			"type": "object",
			"properties": {
				"agent": {"type": "string", "enum": [%s], "description": "which agent (omit when only one is configured)"},
				"task": {"type": "string", "description": "the task description, sent as the agent's first user message (at most %d characters)"},
				"workdir": {"type": "string", "description": "working directory for the agent process (absolute path; omit to use the agent's configured workdir or the home directory)"},
				"roots": {"type": "array", "items": {"type": "string"}, "description": "extra directories this task needs beyond workdir (absolute paths). Omit to stay inside workdir only. Every extra root widens what the agent may touch, so add one only when the task genuinely needs it and say why in the task"}
			},
			"required": ["task"]
		}`, quoteList(names), port.MaxDelegateTaskChars)),
		Risk:     tool.Confirm,
		Describe: delegateDescribe,
	}
}

func (t *delegateTool) Execute(ctx context.Context, call tool.Call) (tool.Result, error) {
	if err := ctx.Err(); err != nil {
		return tool.Result{}, err
	}
	if t.jobs == nil {
		return tool.Result{}, fmt.Errorf("no external agent is configured (delegate.agents)")
	}
	var a struct {
		Agent   string   `json:"agent"`
		Task    string   `json:"task"`
		WorkDir string   `json:"workdir"`
		Roots   []string `json:"roots"`
	}
	if err := decodeArgs(call, &a); err != nil {
		return tool.Result{}, err
	}
	task := strings.TrimSpace(a.Task)
	if task == "" {
		return tool.Result{}, fmt.Errorf("missing task")
	}
	wd := strings.TrimSpace(a.WorkDir)
	if wd != "" && !absWorkDir(wd) {
		return tool.Result{}, fmt.Errorf("workdir must be an absolute path (omit it to use the agent's configured workdir), got %q", a.WorkDir)
	}
	roots, err := port.NormalizeRoots(a.Roots)
	if err != nil {
		return tool.Result{}, fmt.Errorf("delegate: %w", err)
	}
	got, err := t.jobs.Start(ctx, port.DelegateSpec{
		Agent: strings.TrimSpace(a.Agent), Task: task, WorkDir: wd, Roots: roots,
	})
	if err != nil {
		return tool.Result{}, fmt.Errorf("delegate: %w", err)
	}
	return okResult(fmt.Sprintf(
		"delegated to %s as %s; the report will arrive as a message in this session when it finishes. "+
			"Use delegate_wait %s if you need it in this turn; delegate_kill %s stops it.",
		got.Agent, got.ID, got.ID, got.ID)), nil
}

// delegateWait 等一次转交收尾（Safe，Interactive）：“本轮就要结果“ 时用。
type delegateWait struct{ jobs port.DelegateRunner }

func (t *delegateWait) Spec() tool.Spec {
	return tool.Spec{
		Name: "delegate_wait",
		Description: "Wait until a delegation reaches a final state and return the external agent's report plus a " +
			"one-line summary (stop reason, tool calls, changed files, duration). Without timeout_sec it waits until " +
			"the delegation ends (the user can still interrupt); with it, a timeout returns 'still running' and the " +
			"delegation keeps going.",
		Schema: jsonSchema(fmt.Sprintf(`{
			"type": "object",
			"properties": {
				"id": {"type": "string", "description": "delegation task id returned by delegate"},
				"timeout_sec": {"type": "integer", "minimum": 0, "maximum": %d, "description": "how long to wait at most, in seconds (omitted or 0 = until it ends; at most %d)"}
			},
			"required": ["id"]
		}`, maxJobTimeoutSec, maxJobTimeoutSec)),
		Risk: tool.Safe,
	}
}

// Interactive 不吃默认工具超时（同 job_wait）：等转交收尾由 timeout_sec 与用户中断决定。
func (t *delegateWait) Interactive() bool { return true }

func (t *delegateWait) Execute(ctx context.Context, call tool.Call) (tool.Result, error) {
	if err := ctx.Err(); err != nil {
		return tool.Result{}, err
	}
	if t.jobs == nil {
		return tool.Result{}, fmt.Errorf("no external agent is configured (delegate.agents)")
	}
	var a struct {
		ID         string `json:"id"`
		TimeoutSec int    `json:"timeout_sec"`
	}
	if err := decodeArgs(call, &a); err != nil {
		return tool.Result{}, err
	}
	id := port.DelegateID(strings.TrimSpace(a.ID))
	if id == "" {
		return tool.Result{}, fmt.Errorf("missing id")
	}
	if a.TimeoutSec < 0 {
		return tool.Result{}, fmt.Errorf("timeout_sec must be non-negative (0/omitted = wait until it ends), got %d", a.TimeoutSec)
	}
	if a.TimeoutSec > maxJobTimeoutSec {
		return tool.Result{}, fmt.Errorf("timeout_sec exceeds the upper bound (<= %d seconds), got %d", maxJobTimeoutSec, a.TimeoutSec)
	}
	wctx := ctx
	if a.TimeoutSec > 0 {
		var cancel context.CancelFunc
		wctx, cancel = context.WithTimeout(ctx, time.Duration(a.TimeoutSec)*time.Second)
		defer cancel()
	}
	run, err := t.jobs.Wait(wctx, id)
	if err != nil {
		if ctx.Err() != nil {
			return tool.Result{}, ctx.Err()
		}
		if wctx.Err() != nil {
			return okResult(fmt.Sprintf("delegation %s is still running after waiting %ds; it will be reported when it "+
				"finishes (delegate_kill %s stops it)", id, a.TimeoutSec, id)), nil
		}
		return tool.Result{}, fmt.Errorf("wait for delegation: %w", err)
	}
	return okResult(port.DelegateReportText(id, run)), nil
}

// delegateKill 叫停一次转交（Safe：派活本身已过确认，叫停只影响我们自己起的那次）。
type delegateKill struct{ jobs port.DelegateRunner }

func (t *delegateKill) Spec() tool.Spec {
	return tool.Spec{
		Name:        "delegate_kill",
		Description: "Stop a running delegation (sends session/cancel, then terminates the process group if the agent does not acknowledge).",
		Schema: jsonSchema(`{
			"type": "object",
			"properties": {"id": {"type": "string", "description": "delegation task id"}},
			"required": ["id"]
		}`),
		Risk: tool.Safe,
	}
}

func (t *delegateKill) Execute(ctx context.Context, call tool.Call) (tool.Result, error) {
	if err := ctx.Err(); err != nil {
		return tool.Result{}, err
	}
	if t.jobs == nil {
		return tool.Result{}, fmt.Errorf("no external agent is configured (delegate.agents)")
	}
	var a struct {
		ID string `json:"id"`
	}
	if err := decodeArgs(call, &a); err != nil {
		return tool.Result{}, err
	}
	id := port.DelegateID(strings.TrimSpace(a.ID))
	if id == "" {
		return tool.Result{}, fmt.Errorf("missing id")
	}
	if err := t.jobs.Kill(ctx, id); err != nil {
		return tool.Result{}, fmt.Errorf("stop delegation: %w", err)
	}
	return okResult("stopped " + string(id)), nil
}

// quoteList 把名字列表编成 JSON 字符串字面量列表（schema enum 用）。
func quoteList(names []string) string {
	parts := make([]string, len(names))
	for i, n := range names {
		parts[i] = fmt.Sprintf("%q", n)
	}
	return strings.Join(parts, ", ")
}

// delegateDescribe 确认摘要把「代理 / 范围 / 任务」摊在用户面前。
//
// 为什么不用默认提示：它把原始参数压成一行截到 160 字，task 一长，workdir 与 roots 就滑出
// 屏幕——而这两个字段正是用户唯一能判断「该不该批」的东西。所以范围全列、不截断，任务
// 只留开头：任务写不清楚是模型的问题，范围看不见是界面的问题。
func delegateDescribe(args json.RawMessage) string {
	var a struct {
		Agent   string   `json:"agent"`
		Task    string   `json:"task"`
		WorkDir string   `json:"workdir"`
		Roots   []string `json:"roots"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return "" // 参数读不懂就交给默认提示，别在这儿编一句半截的
	}
	agent := strings.TrimSpace(a.Agent)
	if agent == "" {
		agent = i18n.T("tool.delegate.describe.auto")
	}
	scope := i18n.T("tool.delegate.describe.scope_default")
	if wd := strings.TrimSpace(a.WorkDir); wd != "" {
		scope = i18n.Tf("tool.delegate.describe.scope", "dir", wd)
	}
	// 额外根全列：这里少写一个，用户就少看见一条越界许可。
	if roots, err := port.NormalizeRoots(a.Roots); err == nil && len(roots) > 0 {
		scope = i18n.Tf("tool.delegate.describe.scope_plus", "scope", scope,
			"roots", strings.Join(roots, " / "))
	}
	return i18n.Tf("tool.delegate.describe.line", "agent", agent, "scope", scope,
		"task", clipTaskLine(a.Task))
}

// clipTaskLine 任务摘要：折成一行，超 80 字截断（够认出是哪件活）。
func clipTaskLine(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > 80 {
		return string(r[:80]) + "..."
	}
	return s
}
