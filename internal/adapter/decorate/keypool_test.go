package decorate

// 密钥池轮换（D173/S11-4）的契约测试：先换钥匙、逐把冷却、端点故障不换钥匙、
// 全冷却交外层。

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/Tonyjh07/Aquarius/internal/port"
)

// quota 构造配额错误（适配器 429 的标注形态）：同时 Is(ErrQuota) 与 Is(ErrTransient)。
func quota(after time.Duration) error {
	return &port.Quota{After: after, Cause: port.ErrTransient}
}

// keyPoolFor 造两把钥匙的池子，把时钟换成可控的（冷却断言不依赖真实时间）。
func keyPoolFor(a, b *fakeLLM) (*KeyPool, *time.Time) {
	now := time.Now()
	p := NewKeyPool([]KeyEntry{{Label: "甲", LLM: a}, {Label: "乙", LLM: b}})
	p.now = func() time.Time { return now }
	return p, &now
}

// 第一把 429、第二把成功：立刻换钥匙（不退避）、被拒的那把进冷却、下次不再打它。
func TestKeyPoolRotatesOnQuota(t *testing.T) {
	a := &fakeLLM{genErrs: []error{quota(30 * time.Second)}}
	b := &fakeLLM{}
	p, now := keyPoolFor(a, b)
	var rotated []string
	p.OnRotate(func(_ context.Context, from, to string) { rotated = append(rotated, from+"→"+to) })

	s, err := p.Generate(context.Background(), port.GenerateRequest{Model: "m"})
	if err != nil || s == nil {
		t.Fatalf("generate = %v, %v, want 成功", s, err)
	}
	if a.calls != 1 || b.calls != 1 {
		t.Fatalf("calls = 甲 %d / 乙 %d, want 1/1", a.calls, b.calls)
	}
	if len(rotated) != 1 || rotated[0] != "甲→乙" {
		t.Fatalf("rotated = %v, want [甲→乙]", rotated)
	}

	// 下一轮：甲仍在冷却，直接打乙（不该再浪费一次 429）。
	a.genErrs = nil
	if _, err := p.Generate(context.Background(), port.GenerateRequest{Model: "m"}); err != nil {
		t.Fatalf("second generate: %v", err)
	}
	if a.calls != 1 {
		t.Fatalf("冷却期内的钥匙不该再被调用：甲 calls = %d", a.calls)
	}

	// 冷却到期后再打一次：钟拨过 30s，甲又可用了。
	*now = now.Add(31 * time.Second)
	if _, err := p.Generate(context.Background(), port.GenerateRequest{Model: "m"}); err != nil {
		t.Fatalf("third generate: %v", err)
	}
	if a.calls != 2 {
		t.Fatalf("冷却到期后甲应重新可用：calls = %d", a.calls)
	}
}

// 全在冷却：不调用任何钥匙、上抛瞬时错误（交外层降级），并回调最早解冻时刻。
func TestKeyPoolAllCooledFailsTransient(t *testing.T) {
	a := &fakeLLM{genErrs: []error{quota(time.Minute)}}
	b := &fakeLLM{genErrs: []error{quota(time.Minute)}}
	p, now := keyPoolFor(a, b)
	var exhausted time.Time
	p.OnExhaust(func(_ context.Context, until time.Time) { exhausted = until })

	_, err := p.Generate(context.Background(), port.GenerateRequest{})
	if err == nil {
		t.Fatal("want error（两把都被限流）")
	}
	if !errors.Is(err, port.ErrTransient) {
		t.Fatalf("err = %v, want 标瞬时交外层降级", err)
	}
	if a.calls != 1 || b.calls != 1 {
		t.Fatalf("calls = %d/%d, want 各一次", a.calls, b.calls)
	}

	// 再次调用：两把都在冷却，一把都不该被调用，直接报「都在冷却」。
	a.calls, b.calls = 0, 0
	_, err = p.Generate(context.Background(), port.GenerateRequest{})
	if err == nil || a.calls != 0 || b.calls != 0 {
		t.Fatalf("全冷却时应直接失败：err=%v calls=%d/%d", err, a.calls, b.calls)
	}
	if exhausted.IsZero() || !exhausted.After(*now) {
		t.Fatalf("exhausted = %v, want 未来时刻", exhausted)
	}
}

// 非配额瞬时错误（5xx/网络）不换钥匙：端点故障换钥匙没用。
func TestKeyPoolKeepsKeyOnNonQuotaTransient(t *testing.T) {
	a := &fakeLLM{genErrs: []error{transient("503")}}
	b := &fakeLLM{}
	p, _ := keyPoolFor(a, b)

	_, err := p.Generate(context.Background(), port.GenerateRequest{})
	if err == nil || !errors.Is(err, port.ErrTransient) {
		t.Fatalf("err = %v, want 瞬时", err)
	}
	if a.calls != 1 || b.calls != 0 {
		t.Fatalf("calls = 甲 %d / 乙 %d, want 1/0（不换钥匙）", a.calls, b.calls)
	}
}

// 401 等非瞬时错误不换钥匙，且错误原样上抛（配置问题不该被轮换掩盖）。
func TestKeyPoolDoesNotRotateOnFatal(t *testing.T) {
	fatal := errors.New("401 unauthorized")
	a := &fakeLLM{genErrs: []error{fatal}}
	b := &fakeLLM{}
	p, _ := keyPoolFor(a, b)

	_, err := p.Generate(context.Background(), port.GenerateRequest{})
	if !errors.Is(err, fatal) {
		t.Fatalf("err = %v, want 原样含配置错误", err)
	}
	if b.calls != 0 {
		t.Fatalf("乙 calls = %d, want 0", b.calls)
	}
}

// 冷却只延后不提前：同一把钥匙已有冷却时，更短的 Retry-After 不得把它提前解冻。
// （正常路径下一轮请求会跳过冷却中的钥匙，所以这条守卫只在边界上生效——防的是
// 将来有人复用 cool 时把刚被拒的钥匙又捞出来。）
func TestKeyPoolCooldownNeverShortens(t *testing.T) {
	p, now := keyPoolFor(&fakeLLM{}, &fakeLLM{})
	p.cool(0, 5*time.Minute)
	p.cool(0, 10*time.Second) // 更短：不该缩短

	if _, earliest := p.pick(); !earliest.Equal(now.Add(5 * time.Minute)) {
		t.Fatalf("earliest = %v, want %v（短 Retry-After 不缩短冷却）", earliest, now.Add(5*time.Minute))
	}
	// 更长的可以延后。
	p.cool(0, time.Hour)
	if _, earliest := p.pick(); !earliest.Equal(now.Add(time.Hour)) {
		t.Fatalf("earliest = %v, want %v（更长的冷却应生效）", earliest, now.Add(time.Hour))
	}
}

// 无 Retry-After（QuotaAfter = 0）用缺省冷却，不是「不冷却」。
func TestKeyPoolDefaultCooldown(t *testing.T) {
	a := &fakeLLM{genErrs: []error{quota(0)}}
	b := &fakeLLM{}
	p, now := keyPoolFor(a, b)

	if _, err := p.Generate(context.Background(), port.GenerateRequest{}); err != nil {
		t.Fatalf("generate: %v", err)
	}
	// 甲应在缺省冷却期内；立刻再调一次不该碰它。
	if _, err := p.Generate(context.Background(), port.GenerateRequest{}); err != nil {
		t.Fatalf("second generate: %v", err)
	}
	if a.calls != 1 {
		t.Fatalf("甲 calls = %d, want 1（缺省冷却生效）", a.calls)
	}
	_ = now
}

// Models 委派第一把钥匙：某把冷却不影响看清单。
func TestKeyPoolModelsDelegatesFirst(t *testing.T) {
	a := &fakeLLM{genErrs: []error{quota(time.Minute)}}
	b := &fakeLLM{}
	p, _ := keyPoolFor(a, b)
	if _, _ = p.Generate(context.Background(), port.GenerateRequest{}); a.calls != 1 {
		t.Fatalf("前置失败")
	}
	ms, err := p.Models(context.Background())
	if err != nil || len(ms) != 1 {
		t.Fatalf("models = %v, %v", ms, err)
	}
	if a.models != 1 {
		t.Fatalf("Models 应委派第一把：甲 models = %d", a.models)
	}
}

// CountTokens 经池转发到第一把（计数链②不因轮换层断掉）。
func TestKeyPoolCounterForward(t *testing.T) {
	a := &countingLLM{}
	b := &countingLLM{}
	p := NewKeyPool([]KeyEntry{{Label: "甲", LLM: a}, {Label: "乙", LLM: b}})
	c, ok := any(p).(port.TokenCounter)
	if !ok {
		t.Fatal("KeyPool 应实现 TokenCounter 转发")
	}
	if n, err := c.CountTokens(context.Background(), "x"); err != nil || n != 42 || a.counts != 1 {
		t.Fatalf("count = %d, %v（甲 counts=%d）", n, err, a.counts)
	}
	// 内层未实现时上报错误（估算层回落③）。
	plain := NewKeyPool([]KeyEntry{{LLM: &fakeLLM{}}, {LLM: &fakeLLM{}}})
	if _, err := any(plain).(port.TokenCounter).CountTokens(context.Background(), "x"); err == nil {
		t.Fatal("内层无计数器时应报错回落估算")
	}
}

// 三把钥匙：前两把都被限流才轮到第三把；成功那把成为下次起点。
func TestKeyPoolSkipsMultipleCooled(t *testing.T) {
	a := &fakeLLM{genErrs: []error{quota(time.Minute)}}
	b := &fakeLLM{genErrs: []error{quota(time.Minute)}}
	c := &fakeLLM{}
	p := NewKeyPool([]KeyEntry{{Label: "甲", LLM: a}, {Label: "乙", LLM: b}, {Label: "丙", LLM: c}})

	if _, err := p.Generate(context.Background(), port.GenerateRequest{}); err != nil {
		t.Fatalf("generate: %v", err)
	}
	if a.calls != 1 || b.calls != 1 || c.calls != 1 {
		t.Fatalf("calls = %d/%d/%d, want 各一次", a.calls, b.calls, c.calls)
	}
	// 下一轮：甲乙都在冷却，只剩丙。
	a.calls, b.calls, c.calls = 0, 0, 0
	if _, err := p.Generate(context.Background(), port.GenerateRequest{}); err != nil {
		t.Fatalf("second generate: %v", err)
	}
	if a.calls != 0 || b.calls != 0 || c.calls != 1 {
		t.Fatalf("calls = %d/%d/%d, want 0/0/1", a.calls, b.calls, c.calls)
	}
}

func TestKeyPoolLabelFallback(t *testing.T) {
	p := NewKeyPool([]KeyEntry{{LLM: &fakeLLM{}}, {LLM: &fakeLLM{}}})
	if got := p.labelAt(0); got != "第 1 把" {
		t.Fatalf("labelAt(0) = %q", got)
	}
	if got := p.labelAt(1); got != "第 2 把" {
		t.Fatalf("labelAt(1) = %q", got)
	}
	_ = fmt.Sprintf
}
