package decorate

import (
	"context"

	"github.com/Tonyjh07/Aquarius/internal/port"
)

// FitFunc 硬保底裁剪：把请求裁到预算内，返回（可能裁剪后的）请求与省略条数；
// 未超预算时原样返回、省略为 0。实现由 app 导出（app.TrimOldest）注入（D14/§7.1）。
type FitFunc func(ctx context.Context, req port.GenerateRequest) (port.GenerateRequest, int)

// Truncate 超预算硬保底截断装饰器（D14/§7.1）：Generate 前先 Fit 裁到
// max_context_tokens 之下；发生省略时回调 notice（装配据此发 NoticeEvent）。
// Models 透传。
type Truncate struct {
	inner  port.LLM
	fit    FitFunc
	notice func(omitted int)
}

// NewTruncate 构造；notice 可为 nil（不提示）。
func NewTruncate(inner port.LLM, fit FitFunc, notice func(omitted int)) *Truncate {
	return &Truncate{inner: inner, fit: fit, notice: notice}
}

// Generate 先裁后发（裁剪失败不阻断：fit 契约不返回错误）。
func (t *Truncate) Generate(ctx context.Context, req port.GenerateRequest) (port.Stream, error) {
	req, omitted := t.fit(ctx, req)
	if omitted > 0 && t.notice != nil {
		t.notice(omitted)
	}
	return t.inner.Generate(ctx, req)
}

// Models 透传。
func (t *Truncate) Models(ctx context.Context) ([]port.ModelInfo, error) { return t.inner.Models(ctx) }
