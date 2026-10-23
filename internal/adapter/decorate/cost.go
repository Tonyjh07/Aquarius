package decorate

import (
	"context"
	"errors"
	"io"

	"github.com/Tonyjh07/Aquarius/internal/port"
)

// PriceFunc 按模型名取单价（**每百万 token**，D205）：in / out 各自 0 = 未声明。
//
// 用函数而不是价格表：单价是装配根从 config 里读出来的属性，装饰器不该知道
// config 的形状；同一张表也要给 /model 展示用，两处取值必须是同一个来源。
type PriceFunc func(model string) (in, out float64)

// Cost 费用折算装饰器（D205 口径三）：适配器只把响应映射成 Usage，价格由这一层补。
//
// 位置在**每个 provider 的内链里**（降级装饰器之内）：降级会改写请求模型，成本要用
// 实际跑的那家单价——放在降级外面就只能看到 primary 的名字。
//
// 折算规则：两侧单价都声明了才算（缺一侧就不折算，不拿单侧价格猜另一侧）。
// 结果写进流末那条 Delta 的 Usage.CostUSD，随后既有的累加链路（turnbuffer /
// 会话汇总）自然生效，不另立一份统计。
type Cost struct {
	inner port.LLM
	price PriceFunc
}

// NewCost 构造费用折算装饰器；price 为 nil = 不折算（等价于不装）。
func NewCost(inner port.LLM, price PriceFunc) *Cost {
	return &Cost{inner: inner, price: price}
}

func (c *Cost) Generate(ctx context.Context, req port.GenerateRequest) (port.Stream, error) {
	s, err := c.inner.Generate(ctx, req)
	if err != nil {
		return nil, err
	}
	var in, out float64
	if c.price != nil {
		in, out = c.price(req.Model)
	}
	if in <= 0 || out <= 0 {
		return s, nil // 单价未声明 = 不显示费用（不是「免费」）
	}
	return &costStream{inner: s, in: in, out: out}, nil
}

// Models 委派内层（列表拉取不涉费用）。
func (c *Cost) Models(ctx context.Context) ([]port.ModelInfo, error) { return c.inner.Models(ctx) }

// CountTokens 三级计数链②透传（D26）：折算层不碰计数。
func (c *Cost) CountTokens(ctx context.Context, text string) (int, error) {
	return countVia(ctx, c.inner, text)
}

// costStream 把流末用量的金额补上。
type costStream struct {
	inner   port.Stream
	in, out float64 // 每百万 token 单价
}

func (s *costStream) Recv() (port.Delta, error) {
	d, err := s.inner.Recv()
	if errors.Is(err, io.EOF) || d.Usage == nil {
		return d, err
	}
	u := *d.Usage // 值拷贝：端口传入的 Usage 指针可能被适配器复用
	u.CostUSD = float64(u.InputTokens)*s.in/1e6 + float64(u.OutputTokens)*s.out/1e6
	d.Usage = &u
	return d, nil
}

func (s *costStream) Close() error { return s.inner.Close() }
