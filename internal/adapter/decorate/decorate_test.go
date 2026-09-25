package decorate

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Tonyjh07/Aquarius/internal/domain/conversation"
	"github.com/Tonyjh07/Aquarius/internal/domain/tool"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

// ---------------------------------------------------------------------------
// 测试替身
// ---------------------------------------------------------------------------

// fakeStream 按步回放的生成流；步走尽返回 io.EOF。
type fakeStream struct {
	steps []fakeStep
	i     int
}

type fakeStep struct {
	d   port.Delta
	err error
}

func (s *fakeStream) Recv() (port.Delta, error) {
	if s.i >= len(s.steps) {
		return port.Delta{}, io.EOF
	}
	st := s.steps[s.i]
	s.i++
	return st.d, st.err
}

func (s *fakeStream) Close() error { return nil }

// fakeLLM 逐次脚本化的 LLM 替身。
type fakeLLM struct {
	genErrs []error     // 第 n 次 Generate 的错误（nil = 成功）
	stream  port.Stream // 成功时返回的流
	calls   int
	models  int
}

func (l *fakeLLM) Generate(context.Context, port.GenerateRequest) (port.Stream, error) {
	i := l.calls
	l.calls++
	if i < len(l.genErrs) && l.genErrs[i] != nil {
		return nil, l.genErrs[i]
	}
	if l.stream == nil {
		l.stream = &fakeStream{}
	}
	return l.stream, nil
}

func (l *fakeLLM) Models(context.Context) ([]port.ModelInfo, error) {
	l.models++
	return []port.ModelInfo{{Name: "m"}}, nil
}

// countingLLM 额外实现 TokenCounter（三级计数链②转发测试）。
type countingLLM struct {
	fakeLLM
	counts int
}

func (l *countingLLM) CountTokens(context.Context, string) (int, error) {
	l.counts++
	return 42, nil
}

// transient 构造瞬时错误（适配器标注形态）。
func transient(msg string) error { return fmt.Errorf("%s: %w", msg, port.ErrTransient) }

// fakeRunner 简单 ToolRunner 替身。
type fakeRunner struct {
	res   tool.Result
	err   error
	calls int
	name  string
}

func (r *fakeRunner) Specs(context.Context) ([]tool.Spec, error) { return nil, nil }

func (r *fakeRunner) Execute(_ context.Context, call tool.Call) (tool.Result, error) {
	r.calls++
	r.name = call.Name
	return r.res, r.err
}

// ---------------------------------------------------------------------------
// Retry
// ---------------------------------------------------------------------------

// TestRetryTransientThenSuccess 瞬时错误限次退避后成功：3 次尝试、退避 200→400ms。
func TestRetryTransientThenSuccess(t *testing.T) {
	inner := &fakeLLM{
		genErrs: []error{transient("dial"), transient("503"), nil},
		stream:  &fakeStream{steps: []fakeStep{{d: port.Delta{Text: "ok"}}}},
	}
	r := NewRetry(inner)
	var delays []time.Duration
	r.sleep = func(_ context.Context, d time.Duration) error {
		delays = append(delays, d)
		return nil
	}

	s, err := r.Generate(context.Background(), port.GenerateRequest{Model: "m"})
	if err != nil || s == nil {
		t.Fatalf("generate = %v, %v, want 成功", s, err)
	}
	if inner.calls != 3 {
		t.Fatalf("calls = %d, want 3", inner.calls)
	}
	if len(delays) != 2 || delays[0] != 200*time.Millisecond || delays[1] != 400*time.Millisecond {
		t.Fatalf("delays = %v, want [200ms 400ms]", delays)
	}
}

// TestRetryExhaustsAttempts 连续瞬时错误：尝试达上限后原样上抛最后一次错误。
func TestRetryExhaustsAttempts(t *testing.T) {
	last := transient("429")
	inner := &fakeLLM{genErrs: []error{transient("a"), transient("b"), last}}
	r := NewRetry(inner)
	r.sleep = func(context.Context, time.Duration) error { return nil }

	_, err := r.Generate(context.Background(), port.GenerateRequest{})
	if !errors.Is(err, port.ErrTransient) {
		t.Fatalf("err = %v, want 保留瞬时错误链", err)
	}
	if inner.calls != 3 {
		t.Fatalf("calls = %d, want 3（达上限）", inner.calls)
	}
}

// TestRetryNonTransientNoRetry 非瞬时错误不重试（一次即上抛、不退避）。
func TestRetryNonTransientNoRetry(t *testing.T) {
	plain := errors.New("invalid request")
	inner := &fakeLLM{genErrs: []error{plain}}
	r := NewRetry(inner)
	r.sleep = func(context.Context, time.Duration) error {
		t.Fatal("非瞬时错误不应退避")
		return nil
	}

	if _, err := r.Generate(context.Background(), port.GenerateRequest{}); !errors.Is(err, plain) {
		t.Fatalf("err = %v, want 原错误", err)
	}
	if inner.calls != 1 {
		t.Fatalf("calls = %d, want 1", inner.calls)
	}
}

// TestRetryCtxCanceledNoRetry ctx 已取消：即使错误标瞬时也不重试（§10）。
func TestRetryCtxCanceledNoRetry(t *testing.T) {
	inner := &fakeLLM{genErrs: []error{transient("dial")}}
	r := NewRetry(inner)
	r.sleep = func(context.Context, time.Duration) error { return nil }

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := r.Generate(ctx, port.GenerateRequest{}); err == nil {
		t.Fatal("want error")
	}
	if inner.calls != 1 {
		t.Fatalf("calls = %d, want 1", inner.calls)
	}
}

// TestRetrySleepInterrupted 退避期间被 ctx 取消：上抛最后一次生成错误。
func TestRetrySleepInterrupted(t *testing.T) {
	inner := &fakeLLM{genErrs: []error{transient("a"), transient("b")}}
	r := NewRetry(inner)
	r.sleep = func(context.Context, time.Duration) error { return context.Canceled }

	_, err := r.Generate(context.Background(), port.GenerateRequest{})
	if !errors.Is(err, port.ErrTransient) {
		t.Fatalf("err = %v, want 最后一次生成错误", err)
	}
	if inner.calls != 1 {
		t.Fatalf("calls = %d, want 1（退避失败即停）", inner.calls)
	}
}

// TestRetryModelsAndCounterForward Models 透传；TokenCounter 经链转发（D26 计数链②）。
func TestRetryModelsAndCounterForward(t *testing.T) {
	inner := &countingLLM{}
	r := NewRetry(inner)

	if ms, err := r.Models(context.Background()); err != nil || len(ms) != 1 || inner.models != 1 {
		t.Fatalf("models = %v, %v（calls=%d）", ms, err, inner.models)
	}
	c, ok := any(r).(port.TokenCounter)
	if !ok {
		t.Fatal("Retry 应实现 TokenCounter 转发")
	}
	if n, err := c.CountTokens(context.Background(), "x"); err != nil || n != 42 || inner.counts != 1 {
		t.Fatalf("count = %d, %v（calls=%d）", n, err, inner.counts)
	}

	plain := NewRetry(&fakeLLM{})
	if _, err := any(plain).(port.TokenCounter).CountTokens(context.Background(), "x"); err == nil {
		t.Fatal("内层无计数器时应报错回落③")
	}
}

// ---------------------------------------------------------------------------
// Truncate
// ---------------------------------------------------------------------------

// TestTruncateFitsAndNotices 裁剪发生：内层收到裁后请求、notice 收到省略条数。
func TestTruncateFitsAndNotices(t *testing.T) {
	trimmed := []port.PromptMessage{{Role: "system", Content: []port.PromptPart{{Kind: "text", Text: "kept"}}}}
	var noticed []int
	var got []port.PromptMessage
	inner := &captureLLM{
		onGenerate: func(req port.GenerateRequest) { got = req.Messages },
	}
	tr := NewTruncate(inner, func(_ context.Context, req port.GenerateRequest) (port.GenerateRequest, int) {
		req.Messages = trimmed
		return req, 3
	}, func(n int) { noticed = append(noticed, n) })

	if _, err := tr.Generate(context.Background(), port.GenerateRequest{}); err != nil {
		t.Fatalf("generate: %v", err)
	}
	if len(noticed) != 1 || noticed[0] != 3 {
		t.Fatalf("noticed = %v, want [3]", noticed)
	}
	if len(got) != 1 || got[0].Content[0].Text != "kept" {
		t.Fatalf("inner got = %+v, want 裁后请求", got)
	}
}

// TestTruncateNoNoticeWhenUnderBudget 未超预算：不通知、请求原样。
func TestTruncateNoNoticeWhenUnderBudget(t *testing.T) {
	var got []port.PromptMessage
	inner := &captureLLM{
		onGenerate: func(req port.GenerateRequest) { got = req.Messages },
	}
	orig := []port.PromptMessage{{Role: "user", Content: []port.PromptPart{{Kind: "text", Text: "hi"}}}}
	tr := NewTruncate(inner, func(_ context.Context, req port.GenerateRequest) (port.GenerateRequest, int) {
		return req, 0
	}, func(int) { t.Fatal("未超预算不应通知") })

	if _, err := tr.Generate(context.Background(), port.GenerateRequest{Messages: orig}); err != nil {
		t.Fatalf("generate: %v", err)
	}
	if len(got) != 1 || got[0].Content[0].Text != "hi" {
		t.Fatalf("got = %+v, want 原样", got)
	}
}

// captureLLM 捕获到达内层的请求。
type captureLLM struct {
	fakeLLM
	onGenerate func(port.GenerateRequest)
}

func (l *captureLLM) Generate(_ context.Context, req port.GenerateRequest) (port.Stream, error) {
	if l.onGenerate != nil {
		l.onGenerate(req)
	}
	return l.fakeLLM.Generate(context.Background(), req)
}

// ---------------------------------------------------------------------------
// Audit
// ---------------------------------------------------------------------------

// readLines 读审计文件并逐行解析。
func readLines(t *testing.T, path string) []auditLine {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var out []auditLine
	for _, ln := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if ln == "" {
			continue
		}
		var l auditLine
		if err := json.Unmarshal([]byte(ln), &l); err != nil {
			t.Fatalf("解析审计行 %q: %v", ln, err)
		}
		out = append(out, l)
	}
	return out
}

// TestAuditLLMStreamLine 成功流：EOF 后记一行（ok=true、流末 usage），Close 不重复记。
func TestAuditLLMStreamLine(t *testing.T) {
	a, err := NewAudit(filepath.Join(t.TempDir(), "audit.log"), 0)
	if err != nil {
		t.Fatalf("new audit: %v", err)
	}
	defer a.Close()

	inner := &fakeLLM{stream: &fakeStream{steps: []fakeStep{
		{d: port.Delta{Text: "hi"}},
		{d: port.Delta{Usage: &conversation.Usage{InputTokens: 11, OutputTokens: 7}}},
	}}}
	l := AuditLLM(inner, a)
	s, err := l.Generate(context.Background(), port.GenerateRequest{
		Model:    "m",
		Messages: []port.PromptMessage{{Role: "user"}, {Role: "assistant"}},
	})
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	for {
		if _, err := s.Recv(); err != nil {
			break
		}
	}
	_ = s.Close()

	lines := readLines(t, a.path)
	if len(lines) != 1 {
		t.Fatalf("lines = %d, want 1（EOF+Close 只记一次）", len(lines))
	}
	l0 := lines[0]
	if l0.Kind != "llm" || !l0.OK || l0.Model != "m" || l0.Msgs != 2 ||
		l0.In != 11 || l0.Out != 7 || l0.TS == "" || l0.DurMS < 0 {
		t.Fatalf("line = %+v", l0)
	}
}

// TestAuditLLMGenerateError 发起失败立即记一行 ok=false（含错误文本）。
func TestAuditLLMGenerateError(t *testing.T) {
	a, err := NewAudit(filepath.Join(t.TempDir(), "audit.log"), 0)
	if err != nil {
		t.Fatalf("new audit: %v", err)
	}
	defer a.Close()

	inner := &fakeLLM{genErrs: []error{errors.New("boom")}}
	l := AuditLLM(inner, a)
	if _, err := l.Generate(context.Background(), port.GenerateRequest{Model: "m"}); err == nil {
		t.Fatal("want error")
	}
	lines := readLines(t, a.path)
	if len(lines) != 1 || lines[0].OK || !strings.Contains(lines[0].Err, "boom") {
		t.Fatalf("lines = %+v", lines)
	}
}

// TestAuditLLMInterruptedStream 未见 EOF 的关闭：记 ok=false（closed）。
func TestAuditLLMInterruptedStream(t *testing.T) {
	a, err := NewAudit(filepath.Join(t.TempDir(), "audit.log"), 0)
	if err != nil {
		t.Fatalf("new audit: %v", err)
	}
	defer a.Close()

	inner := &fakeLLM{stream: &fakeStream{steps: []fakeStep{{d: port.Delta{Text: "half"}}}}}
	l := AuditLLM(inner, a)
	s, err := l.Generate(context.Background(), port.GenerateRequest{Model: "m"})
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	_, _ = s.Recv() // 半途关闭：无 EOF
	_ = s.Close()

	lines := readLines(t, a.path)
	if len(lines) != 1 || lines[0].OK || !strings.Contains(lines[0].Err, "closed") {
		t.Fatalf("lines = %+v", lines)
	}
}

// TestAuditToolLines 工具调用：OK/OK=false/硬错误三种结果各记一行并透传。
func TestAuditToolLines(t *testing.T) {
	a, err := NewAudit(filepath.Join(t.TempDir(), "audit.log"), 0)
	if err != nil {
		t.Fatalf("new audit: %v", err)
	}
	defer a.Close()

	cases := []struct {
		res  tool.Result
		err  error
		want bool
		errS string
	}{
		{res: tool.Result{OK: true, Output: "x"}, want: true},
		{res: tool.Result{OK: false, Err: "denied"}, want: false, errS: "denied"},
		{res: tool.Result{}, err: errors.New("boom"), want: false, errS: "boom"},
	}
	for _, tc := range cases {
		inner := &fakeRunner{res: tc.res, err: tc.err}
		r := AuditTool(inner, a)
		got, gotErr := r.Execute(context.Background(), tool.Call{Name: "file_read"})
		if (gotErr != nil) != (tc.err != nil) || got.OK != tc.res.OK {
			t.Fatalf("passthrough 失败: %+v, %v", got, gotErr)
		}
	}
	lines := readLines(t, a.path)
	if len(lines) != 3 {
		t.Fatalf("lines = %d, want 3", len(lines))
	}
	for i, tc := range cases {
		if lines[i].Kind != "tool" || lines[i].Name != "file_read" || lines[i].OK != tc.want {
			t.Fatalf("line[%d] = %+v", i, lines[i])
		}
		if tc.errS != "" && lines[i].Err != tc.errS {
			t.Fatalf("line[%d].Err = %q, want %q", i, lines[i].Err, tc.errS)
		}
	}
}

// TestAuditRotation 超限轮转：旧代落到 audit.log.1，新文件从空继续。
func TestAuditRotation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit.log")
	a, err := NewAudit(path, 100) // 小上限强制轮转（单行约 85B < 100，超限即转）
	if err != nil {
		t.Fatalf("new audit: %v", err)
	}
	defer a.Close()

	for i := 0; i < 5; i++ {
		if err := a.record(auditLine{TS: tsNow(), Kind: "tool", Name: "n", DurMS: 1, OK: true}); err != nil {
			t.Fatalf("record %d: %v", i, err)
		}
	}
	if _, err := os.Stat(path + ".1"); err != nil {
		t.Fatalf("应有轮转代 audit.log.1: %v", err)
	}
	if st, err := os.Stat(path); err != nil || st.Size() == 0 {
		t.Fatalf("当前代应非空: %v", err)
	}
	if st, _ := os.Stat(path + ".1"); st.Size() > 100 {
		t.Fatalf("轮转代超限: %d", st.Size())
	}
}

// TestAuditClosedAfterClose Close 后写入静默失败（审计不打断对话）。
func TestAuditClosedAfterClose(t *testing.T) {
	a, err := NewAudit(filepath.Join(t.TempDir(), "audit.log"), 0)
	if err != nil {
		t.Fatalf("new audit: %v", err)
	}
	if err := a.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	if err := a.record(auditLine{Kind: "llm"}); !errors.Is(err, errAuditClosed) {
		t.Fatalf("err = %v, want errAuditClosed", err)
	}
	if err := a.Close(); err != nil {
		t.Fatalf("二次 close 应幂等: %v", err)
	}
}
