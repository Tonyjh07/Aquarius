package app

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/Tonyjh07/Aquarius/internal/domain/perm"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

// execCompact 实现 /compact：压缩历史。
func (s *Session) execCompact(ctx context.Context, cmd port.Command) (string, error) {
	node, absorbed, err := s.agent.Compact(ctx, s.cur)
	if errors.Is(err, ErrNothingToCompact) {
		return "无需压缩：摘要之上没有新的历史", nil
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return "已取消压缩（会话未改动）", nil
	}
	if err != nil {
		return "", err
	}
	if err := s.store.Save(ctx, s.cur); err != nil {
		return "", fmt.Errorf("session: 保存会话: %w", err)
	}
	return fmt.Sprintf("已压缩 %d 条历史 → 1 条摘要（in=%d out=%d tokens）",
		absorbed, node.Usage.InputTokens, node.Usage.OutputTokens), nil
}

// execUnattended 实现 /unattended [on|off]（D185 口径四）：无参 = 切换，有参 = 显式
// 设置；写回 config 成功后活开关即时生效（装饰器每次调用现取）。
// 开关语义是「越界即拒」，与 `-yes`（自动允许）相反——报告里必须说清，否则用户会
// 把它当成另一种免确认档。
func (s *Session) execUnattended(_ context.Context, args []string) (string, error) {
	if len(args) > 1 {
		return "", errors.New("用法: /unattended [on|off]")
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
			return "", fmt.Errorf("用法: /unattended [on|off]（当前 %s）", onOff(cur))
		}
	}
	if want == cur && len(args) == 1 {
		return fmt.Sprintf("无人值守已是 %s", onOff(cur)), nil
	}
	if s.perms == nil {
		return "", errors.New("session: 未配置无人值守持久化，无法切换（需能写回 config）")
	}
	if err := s.perms.PersistUnattended(want); err != nil {
		return "", fmt.Errorf("session: 写回 config 失败: %w", err)
	}
	if !want {
		return "无人值守已关闭（越界操作恢复逐次确认；已写回 config）", nil
	}
	return "无人值守已开启（已写回 config，工具链路即时生效）\n" +
		"权限边界不变：模型发起的越界操作会被当场拒绝并说明，不再弹确认等人；" +
		"你自己输入的命令（删除会话等）与插件授权照常询问。与 -yes 相反——那个是自动放行。", nil
}

// unattendedOn 无人值守当前是否开着；未接线 = 关（与装饰器同口径）。
func (s *Session) unattendedOn() bool { return s.perms != nil && s.perms.Unattended() }

// onOff 布尔的中文短写（命令回执用）。
func onOff(on bool) string {
	if on {
		return "开"
	}
	return "关"
}

// execPermission 实现 /permission：查看或切换权限等级。
func (s *Session) execPermission(ctx context.Context, cmd port.Command) (string, error) {
	if len(cmd.Args) == 0 {
		return s.permissionReport(), nil
	}
	if len(cmd.Args) > 1 {
		return "", errors.New("用法: /permission [read-only|strict|permissive|full-access]")
	}
	level, err := perm.Parse(cmd.Args[0])
	if err != nil {
		return "", err
	}
	if level == s.level {
		return fmt.Sprintf("权限等级已是 %s", level), nil
	}
	if s.perms == nil {
		return "", errors.New("session: 未配置权限持久化，无法切换（需能写回 config）")
	}
	if err := s.perms.PersistLevel(level); err != nil {
		return "", fmt.Errorf("session: 写回 config 失败: %w", err)
	}
	s.level = level
	return fmt.Sprintf("权限等级已切换为 %s（已写回 config；工具链路即时生效）", level), nil
}

// execMemory 实现 /memory：打开记忆文件。
func (s *Session) execMemory(ctx context.Context, cmd port.Command) (string, error) {
	if s.memories == nil {
		return "", errors.New("session: 未配置记忆编辑器（SessionDeps.Memories）")
	}
	if len(cmd.Args) > 1 {
		return "", errors.New("用法: /memory [会话id前缀]（缺省打开全局记忆文件）")
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
		return "", fmt.Errorf("session: 打开记忆文件: %w", err)
	}
	return fmt.Sprintf("已用系统编辑器打开 %s（保存后下次读取即生效）", path), nil
}

// execUsage 实现 /usage：用量报告。
func (s *Session) execUsage(ctx context.Context, cmd port.Command) (string, error) {
	rep, err := s.agent.UsageReport(ctx, s.cur)
	if err != nil {
		return "", fmt.Errorf("session: 估算用量: %w", err)
	}
	var b strings.Builder
	pct := 100 * float64(rep.Estimate) / float64(rep.MaxCtx)
	tpct := 100 * float64(rep.CompactAt) / float64(rep.MaxCtx)
	mode := fmt.Sprintf("估算（按服务端返回的 usage 校准 ×%.2f）", rep.Ratio)
	if rep.Exact {
		mode = "精确（本地 tokenizer 计数）"
	}
	fmt.Fprintf(&b, "当前上下文:≈%d / %d tokens（%.1f%%，自动压缩阈值 %d = %.0f%%）\n",
		rep.Estimate, rep.MaxCtx, pct, rep.CompactAt, tpct)
	fmt.Fprintf(&b, "计数方式: %s\n", mode)
	fmt.Fprintf(&b, "上轮实测: in=%d out=%d tokens（服务端返回）\n", rep.LastIn, rep.LastOut)
	fmt.Fprintf(&b, "本会话累计(Path): in=%d out=%d tokens（%d 次生成）",
		rep.SumIn, rep.SumOut, rep.Gen)
	// D205：费用只在单价声明过时才有意义；未声明就整段不出现（写「$0.0000」会被读成免费）。
	if rep.CostUSD > 0 {
		fmt.Fprintf(&b, "，约 $%.4f", rep.CostUSD)
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
		return "", errors.New("session: 未配置任务管理器（SessionDeps.Jobs），/jobs 不可用")
	}
	sub := "list"
	if len(args) > 0 {
		sub, args = args[0], args[1:]
	}
	switch sub {
	case "list":
		if len(args) != 0 {
			return "", errors.New("用法: /jobs [list|logs <id> [行数]|kill <id>]")
		}
		jobs, err := s.jobs.List(ctx)
		if err != nil {
			return "", fmt.Errorf("session: 列出任务: %w", err)
		}
		if len(jobs) == 0 {
			return "（暂无后台任务；模型可用 job_start 启动）", nil
		}
		var b strings.Builder
		for _, j := range jobs {
			fmt.Fprintf(&b, "%s  %-8s pid=%-7d %s  %s\n",
				j.ID, j.Status, j.PID, j.StartedAt.Format("15:04:05"), jobCommand(j.Spec))
		}
		return strings.TrimRight(b.String(), "\n"), nil

	case "logs":
		if len(args) < 1 || len(args) > 2 {
			return "", errors.New("用法: /jobs logs <id> [行数]（缺省最后 50 行）")
		}
		id, err := s.resolveJob(ctx, args[0])
		if err != nil {
			return "", err
		}
		tail := defaultJobLogsTail
		if len(args) == 2 {
			n, cerr := strconv.Atoi(args[1])
			if cerr != nil || n <= 0 {
				return "", errors.New("/jobs logs 行数须为正整数")
			}
			tail = n
		}
		text, err := s.jobs.Logs(ctx, id, tail)
		if err != nil {
			return "", fmt.Errorf("session: 读取 %s 日志: %w", id, err)
		}
		if strings.TrimSpace(text) == "" {
			return fmt.Sprintf("任务 %s 日志为空", id), nil
		}
		return fmt.Sprintf("任务 %s 日志（末尾 %d 行）:\n%s", id, tail, text), nil

	case "kill":
		if len(args) != 1 {
			return "", errors.New("用法: /jobs kill <id>")
		}
		id, err := s.resolveJob(ctx, args[0])
		if err != nil {
			return "", err
		}
		if err := s.jobs.Kill(ctx, id); err != nil {
			return "", fmt.Errorf("session: 终止任务: %w", err)
		}
		return fmt.Sprintf("已终止 %s", id), nil

	default:
		return "", errors.New("用法: /jobs [list|logs <id> [行数]|kill <id>]")
	}
}

// defaultJobLogsTail /jobs logs 缺省行数（与 job_logs 工具一致）。
const defaultJobLogsTail = 50

// resolveJob 解析任务标识（精确优先 + 唯一前缀，port 共用实现）。
func (s *Session) resolveJob(ctx context.Context, arg string) (port.JobID, error) {
	list, err := s.jobs.List(ctx)
	if err != nil {
		return "", fmt.Errorf("session: 列出任务: %w", err)
	}
	id, err := port.ResolveJobID(list, arg)
	if err != nil {
		return "", fmt.Errorf("session: %w（/jobs 查看可用 ID）", err)
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
		return "", errors.New("用法：/mic [秒数]")
	}
	if len(cmd.Args) == 1 {
		n, err := strconv.Atoi(cmd.Args[0])
		if err != nil || n < 1 || n > micMaxSeconds {
			return "", fmt.Errorf("秒数需在 1–%d 之间", micMaxSeconds)
		}
		secs = n
	}
	if s.ui != nil {
		_ = s.ui.Emit(ctx, port.NoticeEvent{
			Text: fmt.Sprintf("正在录音，最长 %d 秒…", secs),
		})
	}
	return s.ingestAndRun(ctx, port.RawInput{Kind: "mic", Seconds: secs})
}

// permissionReport /permission 无参输出：当前等级 + 全档免确认矩阵（由 perm 判定推导，单一事实源）+ 特权目录。
func (s *Session) permissionReport() string {
	var b strings.Builder
	fmt.Fprintf(&b, "当前权限等级: %s（默认 strict）\n", s.level)
	fmt.Fprintf(&b, "特权目录: %s\n", s.sandboxPath)
	b.WriteString("免确认矩阵（矩阵之外的操作一律逐次确认）:\n")
	for _, l := range perm.Levels {
		mark := "  "
		if l == s.level {
			mark = "* "
		}
		sandbox, other, free := l.Matrix()
		tools := "工具 Confirm 逐次"
		if free {
			tools = "工具全免确认"
		}
		fmt.Fprintf(&b, "%s%-12s 特权 %s | 其他 %-2s | %s\n", mark, l, sandbox, other, tools)
	}
	return strings.TrimRight(b.String(), "\n")
}
