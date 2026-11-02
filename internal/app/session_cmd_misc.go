package app

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/Tonyjh07/Aquarius/internal/domain/perm"
	"github.com/Tonyjh07/Aquarius/internal/i18n"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

// execCompact 实现 /compact：压缩历史。
func (s *Session) execCompact(ctx context.Context, cmd port.Command) (string, error) {
	node, absorbed, err := s.agent.Compact(ctx, s.cur)
	if errors.Is(err, ErrNothingToCompact) {
		return i18n.T("session.compact.nothing"), nil
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return i18n.T("session.compact.canceled"), nil
	}
	if err != nil {
		return "", err
	}
	if err := s.store.Save(ctx, s.cur); err != nil {
		return "", wrapSession("err.save_conversation", err)
	}
	return i18n.Tf("session.compact.done",
		"absorbed", absorbed,
		"in", node.Usage.InputTokens, "out", node.Usage.OutputTokens), nil
}

// execUnattended 实现 /unattended [on|off]（D185 口径四）：无参 = 切换，有参 = 显式
// 设置；写回 config 成功后活开关即时生效（装饰器每次调用现取）。
// 开关语义是「越界即拒」，与 `-yes`（自动允许）相反——报告里必须说清，否则用户会
// 把它当成另一种免确认档。
func (s *Session) execUnattended(_ context.Context, args []string) (string, error) {
	if len(args) > 1 {
		return "", errors.New(i18n.T("usage.unattended"))
	}
	cur := s.unattendedOn()
	want := !cur
	if len(args) == 1 {
		switch strings.ToLower(strings.TrimSpace(args[0])) {
		case "on":
			want = true
		case "off":
			want = false
		default:
			return "", errors.New(i18n.Tf("session.unattended.usage_state", "state", onOff(cur)))
		}
	}
	if want == cur && len(args) == 1 {
		return i18n.Tf("session.unattended.already", "state", onOff(cur)), nil
	}
	if s.perms == nil {
		return "", errors.New("session: " + i18n.T("err.unattended_unwired"))
	}
	if err := s.perms.PersistUnattended(want); err != nil {
		return "", wrapSession("err.write_config", err)
	}
	if !want {
		return i18n.T("session.unattended.off"), nil
	}
	return i18n.T("session.unattended.on"), nil
}

// unattendedOn 无人值守当前是否开着；未接线 = 关（与装饰器同口径）。
func (s *Session) unattendedOn() bool { return s.perms != nil && s.perms.Unattended() }

// onOff 布尔的短写（命令回执用；两种语言各一份，见 i18n 的 common.on / common.off）。
func onOff(on bool) string {
	if on {
		return i18n.T("common.on")
	}
	return i18n.T("common.off")
}

// execPermission 实现 /permission：查看或切换权限等级。
func (s *Session) execPermission(ctx context.Context, cmd port.Command) (string, error) {
	if len(cmd.Args) == 0 {
		return s.permissionReport(), nil
	}
	if len(cmd.Args) > 1 {
		return "", errors.New(i18n.T("usage.permission"))
	}
	level, err := perm.Parse(cmd.Args[0])
	if err != nil {
		return "", err
	}
	if level == s.level {
		return i18n.Tf("session.permission.already", "level", level), nil
	}
	if s.perms == nil {
		return "", errors.New("session: " + i18n.T("err.permissions_unwired"))
	}
	if err := s.perms.PersistLevel(level); err != nil {
		return "", wrapSession("err.write_config", err)
	}
	s.level = level
	return i18n.Tf("session.permission.switched", "level", level), nil
}

// execMemory 实现 /memory：打开记忆文件。
func (s *Session) execMemory(ctx context.Context, cmd port.Command) (string, error) {
	if s.memories == nil {
		return "", errors.New("session: " + i18n.T("err.memory_editor_unwired"))
	}
	if len(cmd.Args) > 1 {
		return "", errors.New(i18n.T("usage.memory"))
	}
	name := port.GlobalMemoryDoc
	if len(cmd.Args) == 1 {
		id, err := s.resolveConversation(ctx, cmd.Args[0])
		if err != nil {
			return "", err
		}
		name = port.SessionMemoryDoc(id)
	}
	path, err := s.memories.Open(name)
	if err != nil {
		return "", wrapSession("err.open_memory", err)
	}
	return i18n.Tf("session.memory.opened", "path", path), nil
}

// execUsage 实现 /usage：用量报告。
func (s *Session) execUsage(ctx context.Context, cmd port.Command) (string, error) {
	rep, err := s.agent.UsageReport(ctx, s.cur)
	if err != nil {
		return "", wrapSession("err.estimate_usage", err)
	}
	var b strings.Builder
	pct := 100 * float64(rep.Estimate) / float64(rep.MaxCtx)
	tpct := 100 * float64(rep.CompactAt) / float64(rep.MaxCtx)
	mode := i18n.Tf("session.usage.mode_estimate", "ratio", fmt.Sprintf("%.2f", rep.Ratio))
	if rep.Exact {
		mode = i18n.T("session.usage.mode_exact")
	}
	b.WriteString(i18n.Tf("session.usage.context",
		"est", rep.Estimate, "max", rep.MaxCtx,
		"pct", fmt.Sprintf("%.1f", pct),
		"at", rep.CompactAt, "atpct", fmt.Sprintf("%.0f", tpct)))
	b.WriteByte('\n')
	b.WriteString(i18n.Tf("session.usage.counter", "mode", mode))
	b.WriteByte('\n')
	b.WriteString(i18n.Tf("session.usage.last", "in", rep.LastIn, "out", rep.LastOut))
	b.WriteByte('\n')
	b.WriteString(i18n.Tf("session.usage.total", "in", rep.SumIn, "out", rep.SumOut, "gen", rep.Gen))
	// D205：费用只在单价声明过时才有意义；未声明就整段不出现（写「$0.0000」会被读成免费）。
	if rep.CostUSD > 0 {
		b.WriteString(i18n.Tf("session.usage.cost", "usd", fmt.Sprintf("%.4f", rep.CostUSD)))
	}
	return b.String(), nil
}

// jobAdmin 命令面看到的任务面（§2.2）：/jobs 只列、看日志、终止，从不启动任务——
// 启动是模型经 job_* 工具做的事。只声明这三项，「命令面能不能偷偷起任务」就不再是
// 靠读实现才能确认的问题。
type jobAdmin interface {
	List(ctx context.Context) ([]port.Job, error)
	Logs(ctx context.Context, id port.JobID, tail int) (string, error)
	Kill(ctx context.Context, id port.JobID) error
}

// jobsReport /jobs 命令（§7.3）：list（缺省）/ logs <id> [行数] / kill <id>。
// 任务由模型经 job_* 工具启动，状态与日志同源（port.JobManager）。
func (s *Session) jobsReport(ctx context.Context, args []string) (string, error) {
	if s.jobs == nil {
		return "", errors.New("session: " + i18n.T("err.jobs_unwired"))
	}
	sub := "list"
	if len(args) > 0 {
		sub, args = args[0], args[1:]
	}
	switch sub {
	case "list":
		if len(args) != 0 {
			return "", errors.New(i18n.T("usage.jobs"))
		}
		jobs, err := s.jobs.List(ctx)
		if err != nil {
			return "", wrapSession("err.list_jobs", err)
		}
		if len(jobs) == 0 {
			return i18n.T("session.jobs.empty"), nil
		}
		var b strings.Builder
		for _, j := range jobs {
			fmt.Fprintf(&b, "%s  %-8s pid=%-7d %s  %s\n",
				j.ID, j.Status, j.PID, j.StartedAt.Format("15:04:05"), jobCommand(j.Spec))
		}
		return strings.TrimRight(b.String(), "\n"), nil

	case "logs":
		if len(args) < 1 || len(args) > 2 {
			return "", errors.New(i18n.T("usage.jobs_logs"))
		}
		id, err := s.resolveJob(ctx, args[0])
		if err != nil {
			return "", err
		}
		tail := defaultJobLogsTail
		if len(args) == 2 {
			n, cerr := strconv.Atoi(args[1])
			if cerr != nil || n <= 0 {
				return "", errors.New(i18n.T("session.jobs.bad_tail"))
			}
			tail = n
		}
		text, err := s.jobs.Logs(ctx, id, tail)
		if err != nil {
			return "", fmt.Errorf("session: %s: %w", i18n.Tf("err.read_log", "id", id), err)
		}
		if strings.TrimSpace(text) == "" {
			return i18n.Tf("session.jobs.logs_empty", "id", id), nil
		}
		return i18n.Tf("session.jobs.logs", "id", id, "n", tail, "text", text), nil

	case "kill":
		if len(args) != 1 {
			return "", errors.New(i18n.T("usage.jobs_kill"))
		}
		id, err := s.resolveJob(ctx, args[0])
		if err != nil {
			return "", err
		}
		if err := s.jobs.Kill(ctx, id); err != nil {
			return "", wrapSession("err.kill_job", err)
		}
		return i18n.Tf("session.jobs.killed", "id", id), nil

	default:
		return "", errors.New(i18n.T("usage.jobs"))
	}
}

// defaultJobLogsTail /jobs logs 缺省行数（与 job_logs 工具一致）。
const defaultJobLogsTail = 50

// resolveJob 解析任务标识（精确优先 + 唯一前缀，port 共用实现）。
func (s *Session) resolveJob(ctx context.Context, arg string) (port.JobID, error) {
	list, err := s.jobs.List(ctx)
	if err != nil {
		return "", wrapSession("err.list_jobs", err)
	}
	id, err := port.ResolveJobID(list, arg)
	if err != nil {
		return "", fmt.Errorf("session: %w %s", err, i18n.T("session.jobs.hint"))
	}
	return id, nil
}

// jobCommand JobSpec 的可读命令行（/jobs 列表展示用）。
func jobCommand(spec port.JobSpec) string {
	parts := make([]string, 0, 1+len(spec.Args))
	parts = append(parts, spec.Command)
	return strings.Join(append(parts, spec.Args...), " ")
}

// micDefaultSeconds /mic 的缺省录音时长；micMaxSeconds 与其硬顶（采集侧还会再夹一次）。
const (
	micDefaultSeconds = 60
	micMaxSeconds     = 240
)

// execMic 实现 /mic [秒数]：录音 → 转写 → 作为用户输入提交（D179）。
// 录音期间提示一次（命令在事件循环里同步跑，不给提示就像卡住了）。
func (s *Session) execMic(ctx context.Context, cmd port.Command) (string, error) {
	secs := micDefaultSeconds
	if len(cmd.Args) > 1 {
		return "", errors.New(i18n.T("usage.mic"))
	}
	if len(cmd.Args) == 1 {
		n, err := strconv.Atoi(cmd.Args[0])
		if err != nil || n < 1 || n > micMaxSeconds {
			return "", errors.New(i18n.Tf("session.mic.range", "max", micMaxSeconds))
		}
		secs = n
	}
	if s.ui != nil {
		_ = s.ui.Emit(ctx, port.NoticeEvent{
			Text: i18n.Tf("session.mic.recording", "secs", secs),
		})
	}
	return s.ingestAndRun(ctx, port.RawInput{Kind: "mic", Seconds: secs})
}

// permissionReport /permission 无参输出：当前等级 + 全档免确认矩阵（由 perm 判定推导，单一事实源）+ 特权目录。
func (s *Session) permissionReport() string {
	var b strings.Builder
	b.WriteString(i18n.Tf("session.permission.head", "level", s.level))
	b.WriteByte('\n')
	b.WriteString(i18n.Tf("session.permission.sandbox", "path", s.sandboxPath))
	b.WriteByte('\n')
	b.WriteString(i18n.T("session.permission.matrix"))
	b.WriteByte('\n')
	for _, l := range perm.Levels {
		mark := "  "
		if l == s.level {
			mark = "* "
		}
		sandbox, other, free := l.Matrix()
		toolsText := i18n.T("session.permission.tools_ask")
		if free {
			toolsText = i18n.T("session.permission.tools_free")
		}
		// 列宽交给格式串（%-12s / %-2s）：语言只换词，对齐规则不变。
		fmt.Fprintf(&b, "%s%-12s %s %s | %s %-2s | %s\n", mark, l,
			i18n.T("session.permission.privileged"), sandbox,
			i18n.T("session.permission.other"), other, toolsText)
	}
	return strings.TrimRight(b.String(), "\n")
}
