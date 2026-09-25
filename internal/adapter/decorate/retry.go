// Package decorate 端口装饰器（DESIGN §10 / D14）：横切能力（重试、硬保底截断、审计）
// 以端口包装器实现，全部在装配根（cmd/aquarius）叠加，不进插件 API、不散落业务代码。
// 依赖 internal/port 与 domain（conversation/tool 经端口类型引用）+ 标准库；
// 装饰器不 import app——裁剪等 app 能力经闭包注入。
package decorate

import (
	"context"
	"errors"
	"time"

	"github.com/Tonyjh07/Aquarius/internal/port"
)

// 默认重试参数（§10"限次 + 退避"；DESIGN 未给配置键，取常量）。
const (
	defaultAttempts = 3                      // 总尝试次数（含首次）
	defaultBaseWait = 200 * time.Millisecond // 首次退避，之后指数 ×2
)

// Retry LLM 瞬时错误重试装饰器（§10）：只重试错误链含 port.ErrTransient 的
// Generate 失败，限次 + 指数退避；ctx 取消与非瞬时错误立即上抛。Models 透传。
type Retry struct {
	inner    port.LLM
	attempts int
	base     time.Duration
	sleep    func(ctx context.Context, d time.Duration) error
}

// NewRetry 默认参数构造（3 次尝试、200ms 起指数退避）。
func NewRetry(inner port.LLM) *Retry {
	return &Retry{
		inner:    inner,
		attempts: defaultAttempts,
		base:     defaultBaseWait,
		sleep:    sleepCtx,
	}
}

// Generate 见类型注释；最后一次瞬时错误原样上抛（保留错误链供上层判因）。
func (r *Retry) Generate(ctx context.Context, req port.GenerateRequest) (port.Stream, error) {
	for attempt := 1; ; attempt++ {
		stream, err := r.inner.Generate(ctx, req)
		if err == nil {
			return stream, nil
		}
		if !errors.Is(err, port.ErrTransient) || attempt >= r.attempts || ctx.Err() != nil {
			return nil, err
		}
		if serr := r.sleep(ctx, r.base<<uint(attempt-1)); serr != nil {
			return nil, err // 退避期间被取消：上抛最后一次生成错误（ctx 状态由主循环收尾）
		}
	}
}

// Models 透传（列表拉取不属瞬时重试面）。
func (r *Retry) Models(ctx context.Context) ([]port.ModelInfo, error) { return r.inner.Models(ctx) }

// sleepCtx 可被 ctx 提前唤醒的 sleep。
func sleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
