package decorate

import (
	"context"
	"errors"
	"fmt"

	"github.com/Tonyjh07/Aquarius/internal/port"
)

// FallbackEntry 一条降级链路：provider 名 + 其 LLM 实现 + 降级请求模型。
// Model 语义（D110 修订③）：空 = 透传 req.Model（primary 用——agent 每轮注入当前
// 模型，热切换不失效）；非空 = 每次尝试改写 req.Model（降级目标 provider 用其
// models[0]——服务端模型清单互不相通，primary 的模型名在目标端往往不存在）。
type FallbackEntry struct {
	Name  string
	LLM   port.LLM
	Model string
}

// Fallback 跨 provider 降级装饰器（D110②/Q5/修订③，S5）：按序尝试 entries——
// 当前 provider 的 Generate 失败且错误链含 port.ErrTransient（同 provider 重试已在
// 内层耗尽）时降级到下一条；非瞬时错误（401/400 等配置/请求错误）与 ctx 取消立即
// 上抛。无粘态：每次 Generate 仍从 primary 起试。流中途断连不降级（err 由 Recv
// 返回，本装饰器只覆盖 Generate 发起失败——与 Retry 同边界）。
// 分层（铁律 9）：同 provider 重试在内、跨 provider 降级在外，装配根叠加。
type Fallback struct {
	entries   []FallbackEntry
	onDegrade func(from, to string) // 降级发生时回调一次（Q5：状态行一次性提示）；nil = 静默
}

// NewFallback 构造降级装饰器。entries[0] 为 primary，至少一条（单条 = 无降级面，
// 装配根应直接用裸链不包本装饰器）。
func NewFallback(entries []FallbackEntry, onDegrade func(from, to string)) *Fallback {
	return &Fallback{entries: entries, onDegrade: onDegrade}
}

// Generate 按序尝试各 provider；降级判定 = 错误链含 port.ErrTransient 且 ctx 未取消。
// 每次尝试的错误都包上 provider 名（%w 保留原链，报因可归位）。
func (f *Fallback) Generate(ctx context.Context, req port.GenerateRequest) (port.Stream, error) {
	var lastErr error
	for i, e := range f.entries {
		attempt := req
		if e.Model != "" {
			attempt.Model = e.Model
		}
		stream, err := e.LLM.Generate(ctx, attempt)
		if err == nil {
			return stream, nil
		}
		lastErr = fmt.Errorf("provider %q: %w", e.Name, err)
		more := i+1 < len(f.entries)
		if !more || !errors.Is(err, port.ErrTransient) || ctx.Err() != nil {
			return nil, lastErr
		}
		if f.onDegrade != nil {
			f.onDegrade(e.Name, f.entries[i+1].Name)
		}
	}
	return nil, lastErr // 不可达（more 守卫已返回）；防御性兜底
}

// Models 委派 primary（列表拉取不属降级面——设置窗与 /model 只关心当前 provider）。
func (f *Fallback) Models(ctx context.Context) ([]port.ModelInfo, error) {
	return f.entries[0].LLM.Models(ctx)
}

// CountTokens 三级计数链②透传（D26，同 Retry/Truncate/audit 口径）：取 primary
// 内链的精确计数；未实现时报错，estimator 静默回落估算③。
func (f *Fallback) CountTokens(ctx context.Context, text string) (int, error) {
	return countVia(ctx, f.entries[0].LLM, text)
}
