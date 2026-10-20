package app

import (
	"context"

	"github.com/Tonyjh07/Aquarius/internal/port"
)

// watchingJobs 是 port.JobManager 的 watcher 装饰器（D167）：Start 成功即登记，
// Kill 成功即撤销，其余方法原样转发。watcher 不挂在具体工具上，所有异步启动入口
// （job_start、未来工具/插件）共用一条注册路径，/jobs kill 也自动覆盖。
type watchingJobs struct {
	port.JobManager
	poller *Poller
}

var _ port.JobManager = (*watchingJobs)(nil)

// NewWatchingJobs 给任务管理器套 watcher；jobs 或 poller 为空时原样返回（测试/未接轮询器）。
func NewWatchingJobs(jobs port.JobManager, poller *Poller) port.JobManager {
	if jobs == nil || poller == nil {
		return jobs
	}
	return &watchingJobs{JobManager: jobs, poller: poller}
}

// Start 启动后台任务并登记 watcher（ctx 里的会话 ID 由 Poller.Watch 取走）。
func (w *watchingJobs) Start(ctx context.Context, spec port.JobSpec) (port.Job, error) {
	job, err := w.JobManager.Start(ctx, spec)
	if err != nil {
		return job, err
	}
	w.poller.Watch(ctx, job.ID, spec)
	return job, nil
}

// Kill 终止任务成功即撤销 watcher：kill 的结果已由调用方拿到，不必再补一条完成报告。
// 已自然结束等 Kill 失败的情况不撤——让 watcher 按终态如实报告。
func (w *watchingJobs) Kill(ctx context.Context, id port.JobID) error {
	err := w.JobManager.Kill(ctx, id)
	if err == nil {
		w.poller.Unwatch(id)
	}
	return err
}
