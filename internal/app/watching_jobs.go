package app

import (
	"context"

	"github.com/Tonyjh07/Aquarius/internal/port"
)

// watchingJobs 是 port.JobManager 的 watcher 装饰器（D189）：Start 成功即登记，
// Kill 成功即撤销，Wait 顺手预领终态报告，其余方法原样转发。watcher 不挂在具体工具上，
// 所有异步启动入口（job_start、未来工具/插件）共用一条注册路径，/jobs kill 也自动覆盖。
type watchingJobs struct {
	port.JobManager
	watcher *JobWatcher
}

var _ port.JobManager = (*watchingJobs)(nil)

// NewWatchingJobs 给任务管理器套 watcher；jobs 或 watcher 为空时原样返回（测试/未接观察者）。
func NewWatchingJobs(jobs port.JobManager, watcher *JobWatcher) port.JobManager {
	if jobs == nil || watcher == nil {
		return jobs
	}
	return &watchingJobs{JobManager: jobs, watcher: watcher}
}

// Start 启动后台任务并登记 watcher（ctx 里的会话 ID 由 JobWatcher.Watch 取走）。
func (w *watchingJobs) Start(ctx context.Context, spec port.JobSpec) (port.Job, error) {
	job, err := w.JobManager.Start(ctx, spec)
	if err != nil {
		return job, err
	}
	w.watcher.Watch(ctx, job.ID, spec)
	return job, nil
}

// Kill 终止任务成功即撤销 watcher：kill 的结果已由调用方拿到，不必再补一条完成报告。
// 已自然结束等 Kill 失败的情况不撤——让 watcher 按终态如实报告。
func (w *watchingJobs) Kill(ctx context.Context, id port.JobID) error {
	err := w.JobManager.Kill(ctx, id)
	if err == nil {
		w.watcher.Unwatch(id)
	}
	return err
}

// Wait 等终态，并把这次终态报告**预领**给调用方（D189）：工具侧拿到结果就地回填给
// 模型，watcher 不必再注入一条内容相同的报告（否则模型会看到两遍同一件事）。
// 没等到（超时/取消）则归还，watcher 照常汇报。
func (w *watchingJobs) Wait(ctx context.Context, id port.JobID) (port.Job, error) {
	release := w.watcher.Claim(id)
	j, err := w.JobManager.Wait(ctx, id)
	if err != nil {
		release()
		return j, err
	}
	return j, nil
}
