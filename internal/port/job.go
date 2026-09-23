package port

import (
	"context"
	"time"

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
// 任务表 v1 内存态（D8），任务不受会话取消影响，job_kill 显式终止（DESIGN §10）。
type JobManager interface {
	Start(ctx context.Context, spec JobSpec) (Job, error)       // 后台启动
	Run(ctx context.Context, spec JobSpec) (tool.Result, error) // 同步执行（term_exec）
	List(ctx context.Context) ([]Job, error)
	Status(ctx context.Context, id JobID) (Job, error)
	Logs(ctx context.Context, id JobID, tail int) (string, error)
	Kill(ctx context.Context, id JobID) error
}
