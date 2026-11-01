package port

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Tonyjh07/Aquarius/internal/domain/conversation"
	"github.com/Tonyjh07/Aquarius/internal/domain/tool"
)

// JobID 后台任务标识。
type JobID string

// JobStatus 任务状态。
type JobStatus string

const (
	JobRunning JobStatus = "running"
	JobDone    JobStatus = "done"
	JobFailed  JobStatus = "failed"
	JobKilled  JobStatus = "killed"
)

// JobSpec 后台任务启动描述。
type JobSpec struct {
	Command string            `json:"command"`
	Args    []string          `json:"args,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
	WorkDir string            `json:"work_dir,omitempty"` // 进程 cwd（默认家目录），不是"工作区"
	Timeout time.Duration     `json:"timeout,omitempty"`  // 0 = 不限时
}

// Job 后台任务运行时信息。
type Job struct {
	ID        JobID     `json:"id"`
	Spec      JobSpec   `json:"spec"`
	Status    JobStatus `json:"status"`
	PID       int       `json:"pid"`
	ExitCode  int       `json:"exit_code"`
	StartedAt time.Time `json:"started_at"`
	EndedAt   time.Time `json:"ended_at"`
}

// JobManager 后台任务端口：独立进程 + 日志落盘 ~/.aquarius/jobs/<id>.log；
// 任务表 v1 内存态（D8），任务不受会话取消影响，job_kill 显式终止（§10）。
//
// 方法按消费方拆成下面几个小接口（§2.2）：各调用方只声明它真正用到的那几个，
// 组合体 JobManager 仍是实现方与装配根的唯一契约。拆之前每个工具都拿着整张方法表，
// 于是「这个工具会不会偷偷改任务」只能靠读实现来确认。
type JobManager interface {
	JobStarter
	JobRunner
	JobReader
	JobKiller
	JobReconciler
}

// UnfinishedJob 上次退出时没有收尾的一条任务（D262）：启动对账的输入。
//
// 它不是一份报告，只是一组事实——任务当时在跑还是已经结束、日志在哪、末几行长什么样。
// 报告由内核组装（正文进模型上下文，用英文），适配器只管把事实读出来。
//
// Job.Status 到这里有两种可能：running（进程当时还在跑）或终态（跑完了，但报告没送到
// 会话就被关掉了，D264）。两者都不是正常路径，但后者至少退出码是确定的。
type UnfinishedJob struct {
	Job     Job             `json:"job"`
	Session conversation.ID `json:"session,omitempty"` // 发起会话；空 = 未绑定
	LogPath string          `json:"log_path,omitempty"`
	LogTail string          `json:"log_tail,omitempty"` // 日志末几行（已按 D41 解码为 UTF-8）
	Alive   bool            `json:"alive"`              // 进程是否仍在（按 pid 探活，不接管）
	// Deadline 本该结束的时刻（spec.Timeout>0 才有）：定时器随进程退出失效，这里只用于
	// 在对账里说明「本该在何时结束」。
	Deadline time.Time `json:"deadline,omitempty"`
}

// JobReconciler 未收尾台账（D262/D264）：只给装配根与任务收尾观察者用。
type JobReconciler interface {
	// Unfinished 取走台账里全部未收尾条目（**读即清**）：取走后不再重报，
	// 所以调用方拿到什么就得处理完什么。台账不存在返回空。
	Unfinished(ctx context.Context) ([]UnfinishedJob, error)
	// Settle 报告已送达会话（D264）：抹掉该任务的台账条目，下次启动不再提它。
	//
	// 判据是「**落地并落盘**」，不是「入队」——入队的报告随进程一起消失，那一条就得
	// 留给下次对账。所以终态与抹账分成两步：进终态只标状态，报告确认送达才删。
	Settle(ctx context.Context, id JobID) error
}

// JobStarter 后台启动（job_start；watcher 装饰器也要）。
type JobStarter interface {
	Start(ctx context.Context, spec JobSpec) (Job, error)
}

// JobRunner 同步执行（term_exec）：跑完才返回，结果直接回填。
type JobRunner interface {
	Run(ctx context.Context, spec JobSpec) (tool.Result, error)
}

// JobReader 只读查询：列表、状态、日志、等终态。四个工具与收尾观察者都只该拿这个。
type JobReader interface {
	List(ctx context.Context) ([]Job, error)
	Status(ctx context.Context, id JobID) (Job, error)
	Logs(ctx context.Context, id JobID, tail int) (string, error)
	// Wait 阻塞到任务进入终态（done/failed/killed）并返回最终快照；任务已结束则
	// 立即返回。实现方由进程收割者直接通知，**不需要调用方定时去问 Status**——
	// 「任务结束」是确定事件，不是待猜的状态（D189）。等待时长由 ctx 决定：
	// 取消即返回 ctx.Err()，端口本身不设超时。
	Wait(ctx context.Context, id JobID) (Job, error)
}

// JobKiller 显式终止（job_kill、/jobs kill；watcher 装饰器还要撤销登记）。
type JobKiller interface {
	Kill(ctx context.Context, id JobID) error
}

// ResolveJobID 按任务标识解析：精确匹配优先，其次唯一前缀；无命中或歧义返回错误。
// job_* 工具与 /jobs 命令共用（纯 DTO 函数，无外部状态）。匹配策略见 MatchPrefix。
func ResolveJobID(jobs []Job, arg string) (JobID, error) {
	arg = strings.TrimSpace(arg)
	if arg == "" {
		return "", errors.New("missing task id")
	}
	ids := make([]JobID, len(jobs))
	for i, j := range jobs {
		ids[i] = j.ID
	}
	id, hits, ok := MatchPrefix(ids, arg)
	switch {
	case ok:
		return id, nil
	case len(hits) == 0:
		return "", fmt.Errorf("no task %q", arg)
	default:
		return "", fmt.Errorf("ambiguous task id %q (matches %s); use a longer prefix", arg, strings.Join(hits, ", "))
	}
}
