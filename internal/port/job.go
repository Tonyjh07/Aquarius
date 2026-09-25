package port

import (
	"context"
	"fmt"
	"strings"
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

// ResolveJobID 按任务标识解析：精确匹配优先，其次唯一前缀；无命中或歧义返回错误。
// job_* 工具与 /jobs 命令共用（纯 DTO 函数，无外部状态）。
func ResolveJobID(jobs []Job, arg string) (JobID, error) {
	arg = strings.TrimSpace(arg)
	if arg == "" {
		return "", fmt.Errorf("缺少任务 id")
	}
	var hits []JobID
	for _, j := range jobs {
		switch {
		case string(j.ID) == arg:
			return j.ID, nil
		case strings.HasPrefix(string(j.ID), arg):
			hits = append(hits, j.ID)
		}
	}
	switch len(hits) {
	case 0:
		return "", fmt.Errorf("没有任务 %q", arg)
	case 1:
		return hits[0], nil
	default:
		ids := make([]string, len(hits))
		for i, h := range hits {
			ids[i] = string(h)
		}
		return "", fmt.Errorf("任务标识 %q 有歧义（命中 %s），请加长", arg, strings.Join(ids, ", "))
	}
}
