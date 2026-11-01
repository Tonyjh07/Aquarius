package app

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/Tonyjh07/Aquarius/internal/domain/conversation"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

// ReportUnfinished 启动对账（D262）：把上次退出时没有收尾的后台任务补成一条事实注入。
//
// 它补的是**事实**不是报告——「当时在跑」或「已经结束而退出码未知」，附日志路径与末几行。
// 不认领活进程：Wait 依赖内存里的 done 通道与 cmd.Process，重启后都拿不到，按 pid 附着
// 还会撞上 pid 复用。没人监督的进程该怎么办由用户决定（日志在盘上，pid 也写在正文里）。
//
// 落地走与任务报告同一条路：目标会话是当前会话就挂当前 Head，否则异地落地到它自己
// 的树上；目标已被删除则丢弃并记一条运行日志。启动作业在 UI 起来之前，所以这里是
// 同步落地而不是入队——队列要等人来排空，而用户启动后可能一句话都不说。
func (s *Session) ReportUnfinished(ctx context.Context, items []port.UnfinishedJob) error {
	if len(items) == 0 {
		return nil
	}
	landed := false
	for _, it := range items {
		item := pendingInjection{
			source:  "job:" + string(it.Job.ID),
			trust:   conversation.TrustHost, // 状态/pid/路径是宿主事实；日志段已在正文里围栏
			parts:   []conversation.Part{{Kind: conversation.PartText, Text: unfinishedReport(it)}},
			session: it.Session,
		}
		if item.session != "" && item.session != s.cur.ID {
			if err := s.landElsewhere(ctx, item); err != nil {
				return err
			}
			continue
		}
		if _, err := s.landInjection(item, false); err != nil {
			return err
		}
		landed = true
	}
	if landed {
		return s.persist(ctx)
	}
	return nil
}

// unfinishedReport 组装对账正文（进模型上下文，英文；日志段是不可信进程输出，单独围栏）。
func unfinishedReport(it port.UnfinishedJob) string {
	var b strings.Builder
	b.WriteString("A background task had not finished when Aquarius last exited.\n")
	fmt.Fprintf(&b, "Task: %s\n", it.Job.ID)
	if cmd := jobCommandLine(it.Job.Spec); cmd != "" {
		fmt.Fprintf(&b, "Command: %s\n", cmd)
	}
	if !it.Job.StartedAt.IsZero() {
		fmt.Fprintf(&b, "Started: %s", it.Job.StartedAt.Format(time.RFC3339))
		if ran := time.Since(it.Job.StartedAt); ran > 0 {
			fmt.Fprintf(&b, " (%.0fs before this startup)", ran.Seconds())
		}
		b.WriteString("\n")
	}
	switch {
	case it.Alive:
		fmt.Fprintf(&b, "State: it may still be running as PID %d — nothing supervises it any more.\n", it.Job.PID)
	case it.Job.Status != port.JobRunning && it.Job.Status != "":
		// 跑完了、报告没送到（D264）：结论是确定的，只是没进过上下文。
		fmt.Fprintf(&b, "State: it finished (%s, exit code %d) but the completion report never reached this session.\n",
			it.Job.Status, it.Job.ExitCode)
	default:
		b.WriteString("State: it has already exited; the exit code was never recorded.\n")
	}
	if it.LogPath != "" {
		fmt.Fprintf(&b, "Log: %s\n", it.LogPath)
	}
	if !it.Deadline.IsZero() {
		fmt.Fprintf(&b, "Deadline: it was due to stop by %s.\n", it.Deadline.Format(time.RFC3339))
	}
	tail := strings.TrimRight(it.LogTail, "\n")
	if strings.TrimSpace(tail) == "" {
		b.WriteString("The log is empty.")
		return b.String()
	}
	b.WriteString("Last log lines:\n")
	fmt.Fprintf(&b, "<injected-context source=\"%s\">\n", escapeFenceAttr("job:"+string(it.Job.ID)))
	b.WriteString(neutralizeFence(tail))
	b.WriteString("\n</injected-context>")
	return b.String()
}

// jobCommandLine 命令行的展示形态（参数含空白时加引号，同 jobproc 的日志头口径）。
func jobCommandLine(spec port.JobSpec) string {
	parts := make([]string, 0, 1+len(spec.Args))
	if cmd := strings.TrimSpace(spec.Command); cmd != "" {
		parts = append(parts, cmd)
	}
	for _, a := range spec.Args {
		if strings.ContainsAny(a, " \t\"") {
			parts = append(parts, fmt.Sprintf("%q", a))
			continue
		}
		parts = append(parts, a)
	}
	return strings.Join(parts, " ")
}

// ReportUnfinishedDelegates 转交的启动对账（D269）：上次退出时没有收尾的转交，补一条事实。
//
// 与任务对账同一路：目标会话是当前会话就挂当前 Head，否则异地落地；目标已删除则丢弃并
// 记日志。正文按「外部」信任级注入——任务描述来自用户，但报错段是子代理自己写的，
// 混在一段里只能按最低的那一档处理。
func (s *Session) ReportUnfinishedDelegates(ctx context.Context, items []port.UnfinishedDelegate) error {
	if len(items) == 0 {
		return nil
	}
	landed := false
	for _, it := range items {
		item := pendingInjection{
			source:  "delegate:" + it.Agent,
			trust:   conversation.TrustExternal,
			parts:   []conversation.Part{{Kind: conversation.PartText, Text: unfinishedDelegateReport(it)}},
			session: it.Session,
		}
		if item.session != "" && item.session != s.cur.ID {
			if err := s.landElsewhere(ctx, item); err != nil {
				return err
			}
			continue
		}
		if _, err := s.landInjection(item, false); err != nil {
			return err
		}
		landed = true
	}
	if landed {
		return s.persist(ctx)
	}
	return nil
}

// unfinishedDelegateReport 组装转交对账正文（进模型上下文，英文）。
func unfinishedDelegateReport(it port.UnfinishedDelegate) string {
	var b strings.Builder
	b.WriteString("A delegation had not finished when Aquarius last exited.\n")
	fmt.Fprintf(&b, "Delegation: %s (agent %s)\n", it.ID, it.Agent)
	if it.WorkDir != "" {
		fmt.Fprintf(&b, "Work dir: %s\n", it.WorkDir)
	}
	if !it.StartedAt.IsZero() {
		fmt.Fprintf(&b, "Started: %s", it.StartedAt.Format(time.RFC3339))
		if ran := time.Since(it.StartedAt); ran > 0 {
			fmt.Fprintf(&b, " (%.0fs before this startup)", ran.Seconds())
		}
		b.WriteString("\n")
	}
	switch {
	case it.EndedAt.IsZero():
		b.WriteString("State: it was still running when the app exited; the agent process was terminated, " +
			"so its report never arrived and no work after that point can be trusted as finished.\n")
	case it.Stop == "killed":
		b.WriteString("State: it was stopped on request, but that happened while the app was closing " +
			"and the report never reached this session.\n")
	default:
		stop := it.Stop
		if stop == "" {
			stop = "unknown"
		}
		fmt.Fprintf(&b, "State: it finished (stop=%s) but the report never reached this session.\n", stop)
	}
	if errText := strings.TrimSpace(it.Error); errText != "" {
		fmt.Fprintf(&b, "It reported: %s\n", errText)
	}
	if task := strings.TrimSpace(it.Task); task != "" {
		b.WriteString("The task it was given:\n")
		fmt.Fprintf(&b, "<injected-context source=\"%s\">\n", escapeFenceAttr("delegate:"+it.Agent))
		b.WriteString(neutralizeFence(task))
		b.WriteString("\n</injected-context>")
	}
	b.WriteString("\nNothing supervises it any more: if this work still matters, hand it over again.")
	return b.String()
}
