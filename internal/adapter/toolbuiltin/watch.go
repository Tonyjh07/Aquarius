package toolbuiltin

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/Tonyjh07/Aquarius/internal/domain/tool"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

// 观察器工具（D190）：模型声明「看什么、怎么算命中、多久看一次、什么时候放弃」，
// 宿主按节奏取样，命中才把结果塞回会话。确定会结束的任务不用它——那是 job_wait。
const (
	watchDefaultIntervalSec = 30
	watchDefaultMaxChecks   = 120
	watchDefaultDeadlineSec = 3600
	watchMinIntervalSec     = 5
	watchMaxIntervalSec     = 3600
	watchMaxDeadlineSec     = 30 * 24 * 3600
	watchMaxChecksBound     = 10000
)

// watchStart 声明一条观察器。
type watchStart struct{ watches port.WatchManager }

func (t *watchStart) Spec() tool.Spec {
	return tool.Spec{
		Name: "watch_start",
		Description: "Watch something that does not finish on its own - a CI status, a log line, a file appearing - and report back when it " +
			"matches. The host probes on a fixed interval (no backoff) and injects one notice when the criterion matches; " +
			"nothing is injected while it does not. For tasks that finish on their own (builds, test runs) use job_start/job_wait instead. " +
			"Requires user confirmation, so it is refused in unattended mode.",
		Schema: jsonSchema(fmt.Sprintf(`{
			"type": "object",
			"properties": {
				"kind": {"type": "string", "enum": ["command", "file", "http"], "description": "what to probe: run a command, read a file, or GET a URL"},
				"target": {"type": "string", "description": "command line, file path, or URL"},
				"match": {"type": "string", "description": "regular expression matched against the probe output; omit to report when the output changes"},
				"notify": {"type": "string", "enum": ["match", "change"], "description": "match = report once and stop (default); change = report each time the criterion becomes true again and keep watching"},
				"interval_sec": {"type": "integer", "minimum": %d, "maximum": %d, "description": "seconds between probes (%d-%d, default %d)"},
				"max_checks": {"type": "integer", "minimum": 1, "maximum": %d, "description": "give up after this many probes (default %d, max %d)"},
				"deadline_sec": {"type": "integer", "minimum": 1, "maximum": %d, "description": "give up after this long in seconds (default %d, max %d)"},
				"workdir": {"type": "string", "description": "working directory for kind=command (optional)"}
			},
			"required": ["kind", "target"]
		}`, watchMinIntervalSec, watchMaxIntervalSec, watchMinIntervalSec, watchMaxIntervalSec, watchDefaultIntervalSec,
			watchMaxChecksBound, watchDefaultMaxChecks, watchMaxChecksBound,
			watchMaxDeadlineSec, watchDefaultDeadlineSec, watchMaxDeadlineSec)),
		Risk: tool.Confirm,
	}
}

func (t *watchStart) Execute(ctx context.Context, call tool.Call) (tool.Result, error) {
	if err := ctx.Err(); err != nil {
		return tool.Result{}, err
	}
	if t.watches == nil {
		return tool.Result{}, fmt.Errorf("watch_start: observations are not available in this build")
	}
	var a struct {
		Kind        string `json:"kind"`
		Target      string `json:"target"`
		Match       string `json:"match"`
		Notify      string `json:"notify"`
		IntervalSec int    `json:"interval_sec"`
		MaxChecks   int    `json:"max_checks"`
		DeadlineSec int    `json:"deadline_sec"`
		WorkDir     string `json:"workdir"`
	}
	if err := decodeArgs(call, &a); err != nil {
		return tool.Result{}, err
	}
	kind := port.WatchKind(strings.ToLower(strings.TrimSpace(a.Kind)))
	switch kind {
	case port.WatchCommand, port.WatchFile, port.WatchHTTP:
	default:
		return tool.Result{}, fmt.Errorf("kind must be command, file or http, got %q", a.Kind)
	}
	target := strings.TrimSpace(a.Target)
	if target == "" {
		return tool.Result{}, fmt.Errorf("missing target")
	}
	var notify port.WatchNotify
	switch port.WatchNotify(strings.ToLower(strings.TrimSpace(a.Notify))) {
	case "":
		notify = port.WatchNotifyOnce
	case port.WatchNotifyOnce, port.WatchNotifyChange:
		notify = port.WatchNotify(strings.ToLower(strings.TrimSpace(a.Notify)))
	default:
		return tool.Result{}, fmt.Errorf("notify must be match or change, got %q", a.Notify)
	}
	if a.IntervalSec == 0 {
		a.IntervalSec = watchDefaultIntervalSec
	}
	if a.IntervalSec < watchMinIntervalSec || a.IntervalSec > watchMaxIntervalSec {
		return tool.Result{}, fmt.Errorf("interval_sec must be %d-%d, got %d", watchMinIntervalSec, watchMaxIntervalSec, a.IntervalSec)
	}
	if a.MaxChecks == 0 {
		a.MaxChecks = watchDefaultMaxChecks
	}
	if a.MaxChecks < 1 || a.MaxChecks > watchMaxChecksBound {
		return tool.Result{}, fmt.Errorf("max_checks must be 1-%d, got %d", watchMaxChecksBound, a.MaxChecks)
	}
	if a.DeadlineSec == 0 {
		a.DeadlineSec = watchDefaultDeadlineSec
	}
	if a.DeadlineSec < 1 || a.DeadlineSec > watchMaxDeadlineSec {
		return tool.Result{}, fmt.Errorf("deadline_sec must be 1-%d, got %d", watchMaxDeadlineSec, a.DeadlineSec)
	}
	if kind != port.WatchCommand && strings.TrimSpace(a.WorkDir) != "" {
		return tool.Result{}, fmt.Errorf("workdir only applies to kind=command")
	}

	st, err := t.watches.Start(ctx, port.WatchSpec{
		Kind:      kind,
		Target:    target,
		Match:     strings.TrimSpace(a.Match),
		Notify:    notify,
		Interval:  time.Duration(a.IntervalSec) * time.Second,
		MaxChecks: a.MaxChecks,
		Deadline:  time.Duration(a.DeadlineSec) * time.Second,
		WorkDir:   strings.TrimSpace(a.WorkDir),
	})
	if err != nil {
		return tool.Result{}, fmt.Errorf("watch_start: %w", err)
	}
	return okResult(watchStartText(st)), nil
}

// watchStartText 回执：确认声明成立，并说明命中前不会有任何输入。
func watchStartText(st port.WatchState) string {
	crit := fmt.Sprintf("output matches /%s/", st.Spec.Match)
	if st.Spec.Match == "" {
		crit = "output changes"
	}
	return fmt.Sprintf("%s watching %s %q every %s (criterion: %s; %s; up to %d checks or until %s). "+
		"No notice is injected while it does not match; stop it with watch_stop if it is no longer needed.",
		st.ID, st.Spec.Kind, st.Spec.Target, st.Spec.Interval, crit, notifyText(st.Spec.Notify),
		st.Spec.MaxChecks, st.EndsAt.Format("15:04:05"))
}

// notifyText 通知口径的用户可读说明（进模型上下文，英文）。
func notifyText(n port.WatchNotify) string {
	if n == port.WatchNotifyChange {
		return "report each time it becomes true again and keep watching"
	}
	return "report once and stop"
}

// watchList 列出观察器。
type watchList struct{ watches port.WatchManager }

func (t *watchList) Spec() tool.Spec {
	return tool.Spec{
		Name:        "watch_list",
		Description: "List running observations with their probe, criterion, interval and how many checks have run.",
		Schema:      tool.NoArgsSchema,
		Risk:        tool.Safe,
	}
}

func (t *watchList) Execute(ctx context.Context, call tool.Call) (tool.Result, error) {
	if err := ctx.Err(); err != nil {
		return tool.Result{}, err
	}
	if t.watches == nil {
		return tool.Result{}, fmt.Errorf("watch_list: observations are not available in this build")
	}
	list, err := t.watches.List(ctx)
	if err != nil {
		return tool.Result{}, fmt.Errorf("watch_list: %w", err)
	}
	if len(list) == 0 {
		return okResult("no observations running"), nil
	}
	var b strings.Builder
	for _, st := range list {
		crit := "output changes"
		if st.Spec.Match != "" {
			crit = "matches /" + st.Spec.Match + "/"
		}
		fmt.Fprintf(&b, "%s  %-7s %q  %s  every %s  checks=%d  ends=%s\n",
			st.ID, st.Spec.Kind, st.Spec.Target, crit, st.Spec.Interval,
			st.Checks, st.EndsAt.Format("15:04:05"))
	}
	return okResult(strings.TrimRight(b.String(), "\n")), nil
}

// watchStop 停止一条观察器。
type watchStop struct{ watches port.WatchManager }

func (t *watchStop) Spec() tool.Spec {
	return tool.Spec{
		Name:        "watch_stop",
		Description: "Stop an observation by ID. Nothing is reported for a stopped observation.",
		Schema: jsonSchema(`{
			"type": "object",
			"properties": {"id": {"type": "string", "description": "observation ID from watch_start or watch_list"}},
			"required": ["id"]
		}`),
		Risk: tool.Safe,
	}
}

func (t *watchStop) Execute(ctx context.Context, call tool.Call) (tool.Result, error) {
	if err := ctx.Err(); err != nil {
		return tool.Result{}, err
	}
	if t.watches == nil {
		return tool.Result{}, fmt.Errorf("watch_stop: observations are not available in this build")
	}
	var a struct {
		ID string `json:"id"`
	}
	if err := decodeArgs(call, &a); err != nil {
		return tool.Result{}, err
	}
	arg := strings.TrimSpace(a.ID)
	if arg == "" {
		return tool.Result{}, fmt.Errorf("missing id")
	}
	list, err := t.watches.List(ctx)
	if err != nil {
		return tool.Result{}, fmt.Errorf("watch_stop: %w", err)
	}
	id, err := port.ResolveWatchID(list, arg)
	if err != nil {
		return tool.Result{}, err
	}
	if err := t.watches.Stop(ctx, id); err != nil {
		return tool.Result{}, fmt.Errorf("watch_stop: %w", err)
	}
	return okResult("stopped " + string(id)), nil
}
