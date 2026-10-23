package decorate

import (
	"context"
	"errors"
	"io"
	"testing"

	"github.com/Tonyjh07/Aquarius/internal/domain/conversation"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

// pricedLLM 记录请求、回放固定流的替身（费用折算层测试用）。
type pricedLLM struct {
	model  string
	req    port.GenerateRequest
	stream port.Stream
	err    error
}

func (l *pricedLLM) Generate(_ context.Context, req port.GenerateRequest) (port.Stream, error) {
	l.model = req.Model
	l.req = req
	if l.err != nil {
		return nil, l.err
	}
	return l.stream, nil
}

func (l *pricedLLM) Models(context.Context) ([]port.ModelInfo, error) { return nil, nil }

// TestCostFillsUsage 单价声明了就按当轮用量折算（D205）：in/out 各自除以 1e6。
func TestCostFillsUsage(t *testing.T) {
	inner := &pricedLLM{stream: &fakeStream{steps: []fakeStep{
		{d: port.Delta{Text: "hi"}},
		{d: port.Delta{Usage: &conversation.Usage{InputTokens: 1000, OutputTokens: 500}}},
	}}}
	c := NewCost(inner, func(model string) (float64, float64) {
		if model != "m1" {
			t.Fatalf("单价查询的模型名 = %q", model)
		}
		return 3, 15 // 每百万 token
	})
	s, err := c.Generate(context.Background(), port.GenerateRequest{Model: "m1"})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	var usage *conversation.Usage
	for {
		d, err := s.Recv()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatalf("Recv: %v", err)
		}
		if d.Usage != nil {
			usage = d.Usage
		}
	}
	if usage == nil {
		t.Fatal("流末应带用量")
	}
	want := 1000.0*3/1e6 + 500.0*15/1e6
	if diff := usage.CostUSD - want; diff > 1e-9 || diff < -1e-9 {
		t.Fatalf("CostUSD = %v, want %v", usage.CostUSD, want)
	}
}

// TestCostSkippedWhenPriceMissing 单价未声明 = 不折算（不写 0 冒充「免费」）。
func TestCostSkippedWhenPriceMissing(t *testing.T) {
	for _, tc := range []struct {
		name     string
		in, out  float64
		priceNil bool
	}{
		{name: "只声明输入价", in: 3},
		{name: "只声明输出价", out: 15},
		{name: "完全未声明", priceNil: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			inner := &pricedLLM{stream: &fakeStream{steps: []fakeStep{
				{d: port.Delta{Usage: &conversation.Usage{InputTokens: 1000, OutputTokens: 500}}},
			}}}
			var price PriceFunc
			if !tc.priceNil {
				in, out := tc.in, tc.out
				price = func(string) (float64, float64) { return in, out }
			}
			s, err := NewCost(inner, price).Generate(context.Background(), port.GenerateRequest{Model: "m1"})
			if err != nil {
				t.Fatalf("Generate: %v", err)
			}
			d, err := s.Recv()
			if err != nil {
				t.Fatalf("Recv: %v", err)
			}
			if d.Usage == nil || d.Usage.CostUSD != 0 {
				t.Fatalf("缺价时不该折算: %+v", d.Usage)
			}
		})
	}
}

// TestCostDoesNotMutateUpstreamUsage 折算写的是副本：适配器复用同一个 Usage 指针时
// 不能被改写（同一份用量可能还要给审计/校准用）。
func TestCostDoesNotMutateUpstreamUsage(t *testing.T) {
	shared := &conversation.Usage{InputTokens: 10, OutputTokens: 20}
	inner := &pricedLLM{stream: &fakeStream{steps: []fakeStep{{d: port.Delta{Usage: shared}}}}}
	s, err := NewCost(inner, func(string) (float64, float64) { return 1, 2 }).
		Generate(context.Background(), port.GenerateRequest{Model: "m1"})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if _, err := s.Recv(); err != nil {
		t.Fatalf("Recv: %v", err)
	}
	if shared.CostUSD != 0 {
		t.Fatalf("上游 Usage 被改写了: %+v", shared)
	}
}

// TestFallbackSwapsSampling 降级到目标 provider 时采样参数一并换成它自己那份（D204）：
// 模型名在装饰器里才确定，参数得跟着走；思考三件套（会话级）保持不动。
func TestFallbackSwapsSampling(t *testing.T) {
	on := true
	primary := &fakeLLM{genErrs: []error{transient("429")}}
	backup := &pricedLLM{stream: &fakeStream{}}
	target := port.Sampling{Temperature: fptr(0.2), TopP: fptr(0.5), MaxTokens: 64, Stop: []string{"END"}}
	fb := NewFallback([]FallbackEntry{
		{Name: "p0", LLM: primary},
		{Name: "p1", LLM: backup, Model: "gm", Sampling: &target},
	}, nil)
	got, err := fb.Generate(context.Background(), port.GenerateRequest{
		Model: "primary-model",
		Params: port.Sampling{Temperature: fptr(0.9), MaxTokens: 4096,
			ReasoningEffort: "high", Thinking: &on},
	})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	_ = got.Close()
	if backup.model != "gm" {
		t.Fatalf("模型名未改写: %q", backup.model)
	}
	if backup.req.Params.Temperature == nil || *backup.req.Params.Temperature != 0.2 ||
		backup.req.Params.TopP == nil || *backup.req.Params.TopP != 0.5 ||
		backup.req.Params.MaxTokens != 64 || len(backup.req.Params.Stop) != 1 {
		t.Fatalf("采样参数未换成降级目标那份: %+v", backup.req.Params)
	}
	if backup.req.Params.ReasoningEffort != "high" || backup.req.Params.Thinking == nil ||
		!*backup.req.Params.Thinking {
		t.Fatalf("思考三件套不该被降级改掉: %+v", backup.req.Params)
	}
}

// fptr 采样参数指针形（D204）。
func fptr(v float64) *float64 { return &v }
