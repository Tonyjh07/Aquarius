package decorate

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Tonyjh07/Aquarius/internal/port"
)

// recordingLLM 记录每次 Generate 收到的 req.Model（降级模型改写断言用）。
type recordingLLM struct {
	fakeLLM
	models []string
}

func (l *recordingLLM) Generate(ctx context.Context, req port.GenerateRequest) (port.Stream, error) {
	l.models = append(l.models, req.Model)
	return l.fakeLLM.Generate(ctx, req)
}

// TestFallbackTransientDegrades Q5/D110 修订③主路径：primary 瞬时错误 → 降级到
// 下一 provider 成功；降级请求模型改写为其 models[0]，primary 透传 req.Model。
func TestFallbackTransientDegrades(t *testing.T) {
	primary := &recordingLLM{fakeLLM: fakeLLM{genErrs: []error{transient("429")}}}
	backup := &recordingLLM{fakeLLM: fakeLLM{
		genErrs: []error{nil},
		stream:  &fakeStream{steps: []fakeStep{{d: port.Delta{Text: "ok"}}}},
	}}
	var degraded []string
	fb := NewFallback([]FallbackEntry{
		{Name: "p0", LLM: primary}, // Model 空 = 透传
		{Name: "p1", LLM: backup, Model: "gm"},
	}, func(from, to string) { degraded = append(degraded, from+"->"+to) })

	stream, err := fb.Generate(context.Background(), port.GenerateRequest{Model: "current"})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	defer stream.Close()
	if primary.calls != 1 || backup.calls != 1 {
		t.Fatalf("calls = %d/%d, want 1/1", primary.calls, backup.calls)
	}
	if got := primary.models[0]; got != "current" {
		t.Fatalf("primary req.Model = %q, want 透传 current", got)
	}
	if got := backup.models[0]; got != "gm" {
		t.Fatalf("fallback req.Model = %q, want 改写为 models[0] gm", got)
	}
	if len(degraded) != 1 || degraded[0] != "p0->p1" {
		t.Fatalf("degraded = %v, want [p0->p1]", degraded)
	}
}

// TestFallbackNonTransientNoDegrade 401/400 等非瞬时错误直接上抛（Q5：配置错误
// 必须暴露），不降级、不回调。
func TestFallbackNonTransientNoDegrade(t *testing.T) {
	primary := &fakeLLM{genErrs: []error{errors.New("401 unauthorized")}}
	backup := &fakeLLM{}
	called := false
	fb := NewFallback([]FallbackEntry{
		{Name: "p0", LLM: primary}, {Name: "p1", LLM: backup, Model: "gm"},
	}, func(from, to string) { called = true })

	_, err := fb.Generate(context.Background(), port.GenerateRequest{})
	if err == nil || !errors.Is(err, primary.genErrs[0]) {
		t.Fatalf("err = %v, want 原错误上抛", err)
	}
	if !strings.Contains(err.Error(), "p0") {
		t.Fatalf("err 应带 provider 归位: %v", err)
	}
	if backup.calls != 0 || called {
		t.Fatalf("非瞬时错误不应降级（backup.calls=%d, onDegrade=%v）", backup.calls, called)
	}
}

// TestFallbackAllFailTransient 全链瞬时失败：按序走完、一次性回调每跳、最后一个
// 错误上抛（带 provider 归位）。
func TestFallbackAllFailTransient(t *testing.T) {
	p0 := &fakeLLM{genErrs: []error{transient("a")}}
	p1 := &fakeLLM{genErrs: []error{transient("b")}}
	p2 := &fakeLLM{genErrs: []error{transient("c")}}
	var hops []string
	fb := NewFallback([]FallbackEntry{
		{Name: "p0", LLM: p0}, {Name: "p1", LLM: p1}, {Name: "p2", LLM: p2},
	}, func(from, to string) { hops = append(hops, from+"->"+to) })

	_, err := fb.Generate(context.Background(), port.GenerateRequest{})
	if err == nil || !errors.Is(err, port.ErrTransient) {
		t.Fatalf("err = %v, want 瞬时错误链保留", err)
	}
	if !strings.Contains(err.Error(), "p2") {
		t.Fatalf("err 应归位到最后 provider: %v", err)
	}
	if p0.calls != 1 || p1.calls != 1 || p2.calls != 1 {
		t.Fatalf("calls = %d/%d/%d, want 各 1（顺序尝试）", p0.calls, p1.calls, p2.calls)
	}
	if len(hops) != 2 || hops[0] != "p0->p1" || hops[1] != "p1->p2" {
		t.Fatalf("hops = %v, want [p0->p1 p1->p2]", hops)
	}
}

// TestFallbackCtxCancelNoDegrade ctx 取消后不降级（取消语义优先于降级）。
func TestFallbackCtxCancelNoDegrade(t *testing.T) {
	primary := &fakeLLM{genErrs: []error{transient("429")}}
	backup := &fakeLLM{}
	called := false
	fb := NewFallback([]FallbackEntry{
		{Name: "p0", LLM: primary}, {Name: "p1", LLM: backup, Model: "gm"},
	}, func(from, to string) { called = true })
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := fb.Generate(ctx, port.GenerateRequest{})
	if err == nil || !errors.Is(err, port.ErrTransient) {
		t.Fatalf("err = %v, want 瞬时错误上抛", err)
	}
	if backup.calls != 0 || called {
		t.Fatalf("ctx 取消不应降级（backup.calls=%d, onDegrade=%v）", backup.calls, called)
	}
}

// TestFallbackOverRetry 分层组合（铁律 9）：同 provider 重试在内耗尽后才降级——
// 内层 3 次瞬时错误走尽，Fallback 才切到下一 provider。
func TestFallbackOverRetry(t *testing.T) {
	primary := &fakeLLM{genErrs: []error{transient("1"), transient("2"), transient("3")}}
	backup := &fakeLLM{genErrs: []error{nil}, stream: &fakeStream{}}
	zero := func(context.Context, time.Duration) error { return nil }
	inner := &Retry{inner: primary, attempts: 3, base: 0, sleep: zero}
	fb := NewFallback([]FallbackEntry{
		{Name: "p0", LLM: inner}, {Name: "p1", LLM: backup, Model: "gm"},
	}, nil)
	if _, err := fb.Generate(context.Background(), port.GenerateRequest{}); err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if primary.calls != 3 || backup.calls != 1 {
		t.Fatalf("calls = %d/%d, want 3/1（重试耗尽才降级）", primary.calls, backup.calls)
	}
}

// TestFallbackModelsDelegates Models 委派 primary（降级面外）。
func TestFallbackModelsDelegates(t *testing.T) {
	primary := &fakeLLM{}
	backup := &fakeLLM{}
	fb := NewFallback([]FallbackEntry{
		{Name: "p0", LLM: primary}, {Name: "p1", LLM: backup},
	}, nil)
	if _, err := fb.Models(context.Background()); err != nil {
		t.Fatalf("Models: %v", err)
	}
	if primary.models != 1 || backup.models != 0 {
		t.Fatalf("models = %d/%d, want 1/0（委派 primary）", primary.models, backup.models)
	}
}

// TestFallbackCountTokensDelegates 三级计数链②透传：primary 实现即转发；
// 未实现报错（estimator 静默回落估算③）。
func TestFallbackCountTokensDelegates(t *testing.T) {
	counting := &countingLLM{}
	fb := NewFallback([]FallbackEntry{{Name: "p0", LLM: counting}}, nil)
	if n, err := fb.CountTokens(context.Background(), "x"); err != nil || n != 42 {
		t.Fatalf("CountTokens = %d, %v, want 42, nil", n, err)
	}
	plain := NewFallback([]FallbackEntry{{Name: "p0", LLM: &fakeLLM{}}}, nil)
	if _, err := plain.CountTokens(context.Background(), "x"); !errors.Is(err, errNoCounter) {
		t.Fatalf("err = %v, want errNoCounter", err)
	}
}
