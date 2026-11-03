package decorate

import (
	"context"

	"github.com/Tonyjh07/Aquarius/internal/i18n"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

// errNoCounter 内层未实现 TokenCounter 时的转发错误（估算层据此回落通用估算③，D26）。
// 零宽结构体：errors.Is 照旧，Error() 每次取词（§7.2）。
type noCounterError struct{}

func (noCounterError) Error() string { return i18n.T("tool.err.no_counter") }

var errNoCounter error = noCounterError{}

// TokenCounter 可选能力转发：三个装饰器都实现 port.TokenCounter 并向下透传，
// 保证三级计数链②不因装饰器链断掉（app.New 对最外层做类型断言）。
// 内层未实现时返回错误，estimator 静默回落③。

func counterFrom(inner port.LLM) port.TokenCounter {
	if c, ok := inner.(port.TokenCounter); ok {
		return c
	}
	return nil
}

// countVia 把转发实现收敛到一个方法体。
func countVia(ctx context.Context, inner port.LLM, text string) (int, error) {
	if c := counterFrom(inner); c != nil {
		return c.CountTokens(ctx, text)
	}
	return 0, errNoCounter
}

// CountTokens 见 counterFrom（Retry）。
func (r *Retry) CountTokens(ctx context.Context, text string) (int, error) {
	return countVia(ctx, r.inner, text)
}

// CountTokens 见 counterFrom（Truncate）。
func (t *Truncate) CountTokens(ctx context.Context, text string) (int, error) {
	return countVia(ctx, t.inner, text)
}

// CountTokens 见 counterFrom（AuditLLM）。
func (l *auditLLM) CountTokens(ctx context.Context, text string) (int, error) {
	return countVia(ctx, l.inner, text)
}
