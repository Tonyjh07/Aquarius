package app

import (
	"context"
	"time"

	"github.com/Tonyjh07/Aquarius/internal/port"
)

// 注入投递的有限重试参数：跨轮生产者（任务收尾、观察器、转交收尾）共用同一套——
// 注入队列满（8 条）时最多再试 3 次，线性退避每档 1 秒。任务已结束时丢一条报告
// 就没人再报，所以值得等；但只等有限次，会话若持续收不下就算了。
const (
	injectAttempts  = 3
	injectRetryStep = time.Second
)

// lander 带送达回执的注入（D264）：返回 nil 表示已写成节点并写入磁盘，不只是入队。
// 单独一个接口而不是加进 port.Injector：注入端口是所有插件/工具共用的，
// 只有跨轮生产者需要回执，不该逼所有人实现该接口。
type lander interface {
	InjectAndWait(ctx context.Context, in port.Injection) error
}

// injectRetry 跨轮生产者把注入投进会话的共用投递循环。jobwatch / watcher /
// delegatewatch 三处原来是三份同构循环（常量三套、退避写法三种、日志 key 各自），
// 行为已经开始漂移（回执分支只有一处有）；收成这一个共用件后，改重试策略只改这里。
//
// 差异留在调用方：入口处的注入器缺失检查与报因（三处的日志 key 不同）、送达后的
// 回调、失败耗尽后的报因——本结构只管「取注入器 → 投 → 失败线性退避 → 耗尽」。
type injectRetry struct {
	// getInj 每次尝试前现取注入器：转交收尾在退出过程中注入器会变 nil（取到 nil
	// 即视为正在退出，静默放弃，不报日志——退出不是失败）。
	getInj func() port.Injector
	// lander 期望送达回执：实现支持 InjectAndWait 就等写盘回执，否则退回
	// 「入队即算送走」。任务收尾（jobwatch）需要回执来抹台账，其余两处不需要。
	lander bool
	// onDone 送达后的回调（观察器敲醒空闲会话用）；nil = 无。
	onDone   func()
	attempts int
	step     time.Duration
	sleep    func(ctx context.Context, d time.Duration) bool // false = ctx 已取消，放弃
}

// deliver 投递一条注入。返回是否送达；lastErr 是最后一次投递失败的原因
// （送达、ctx 取消或注入器缺失时为 nil），调用方据此决定要不要报日志。
func (r injectRetry) deliver(ctx context.Context, in port.Injection) (delivered bool, lastErr error) {
	for attempt := 0; attempt < r.attempts; attempt++ {
		if ctx.Err() != nil {
			return false, nil
		}
		inj := r.getInj()
		if inj == nil {
			return false, nil
		}
		// 有回执能力的实现就等送达，否则退回「入队即算送走」——测试替身与旧装配
		// 都没有回执，行为与改动前一致。
		var err error
		if r.lander {
			if l, ok := inj.(lander); ok {
				err = l.InjectAndWait(ctx, in)
			} else {
				err = inj.Inject(ctx, in)
			}
		} else {
			err = inj.Inject(ctx, in)
		}
		if err == nil {
			if r.onDone != nil {
				r.onDone()
			}
			return true, nil
		}
		lastErr = err
		if ctx.Err() != nil {
			return false, nil
		}
		if attempt+1 < r.attempts && !r.sleep(ctx, time.Duration(attempt+1)*r.step) {
			return false, nil
		}
	}
	return false, lastErr
}
