package decorate

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/Tonyjh07/Aquarius/internal/port"
)

// defaultKeyCooldown 配额错误的缺省冷却（D173）：服务端没给 `Retry-After` 时用它。
// 取值依据：限流的恢复时间通常远大于一次退避（200ms 起），小到几秒等于把轮换降级成
// 退避重试；大到十几分钟又会把「另一把我现在就能用」这条路径白等掉。
const defaultKeyCooldown = 60 * time.Second

// KeyEntry 密钥池里的一把钥匙：展示用 label + 绑定了这把钥匙的端口实现。
// 密钥在构造期固化进 client，所以「换钥匙」在这里就是换一具 client。
type KeyEntry struct {
	Label string
	LLM   port.LLM
}

// KeyPool 同 provider 内的密钥轮换装饰器（D173/§10）。分层：装配根把重试罩在它外面、
// 它罩在 client 外面（铁律 9：装饰器只在装配根叠加）。
//
//   - 错误链含 port.ErrQuota（适配器对 429 的额外标注）→ 立刻换下一把钥匙重试，
//     **不退避**（换钥匙本来就比退避快，多账号场景下这才是用户要的语义）；
//   - 被拒的那把进入**进程内冷却**（读响应头的 `Retry-After`，缺省 60s），冷却期内
//     跳过它；冷却状态不落盘，重启清空；
//   - 其他瞬时错误（5xx/网络）**不换钥匙**——端点故障换钥匙没用，交给外层重试；
//   - 钥匙全在冷却 = 这一轮放弃（错误照旧标瞬时，交外层降级）。
//
// 「先换钥匙、再换 provider」的顺序由分层保证：本装饰器在 provider 内链上，跨 provider
// 降级（Fallback）在它外面，所以钥匙没换完就不会跨 provider。
type KeyPool struct {
	mu          sync.Mutex
	keys        []KeyEntry
	cooledUntil []time.Time
	now         func() time.Time
	cooldown    time.Duration
	onRotate    func(ctx context.Context, from, to string)
	onExhaust   func(ctx context.Context, until time.Time)
}

// NewKeyPool 构造密钥池。keys 至少两把（单把时装配根直接用裸 client——没有别的钥匙
// 可换，冷却只会把唯一的路径也堵上）。
func NewKeyPool(keys []KeyEntry) *KeyPool {
	return &KeyPool{
		keys:        keys,
		cooledUntil: make([]time.Time, len(keys)),
		now:         time.Now,
		cooldown:    defaultKeyCooldown,
	}
}

// OnRotate 注册换钥匙回调（nil = 静默）：from/to 是两把钥匙的展示名。
func (p *KeyPool) OnRotate(f func(ctx context.Context, from, to string)) *KeyPool {
	p.onRotate = f
	return p
}

// OnExhaust 注册「钥匙全在冷却」回调（nil = 静默）：until = 最早解冻时刻。
func (p *KeyPool) OnExhaust(f func(ctx context.Context, until time.Time)) *KeyPool {
	p.onExhaust = f
	return p
}

// Generate 按「未冷却的钥匙」顺序尝试：配额错误立刻换下一把，其他错误原样上抛。
func (p *KeyPool) Generate(ctx context.Context, req port.GenerateRequest) (port.Stream, error) {
	order, earliest := p.pick()
	if len(order) == 0 {
		if p.onExhaust != nil {
			p.onExhaust(ctx, earliest)
		}
		return nil, fmt.Errorf("llm: %d 把密钥都在冷却（最早 %s 解冻）: %w",
			len(p.keys), earliest.Format("15:04:05"), port.ErrTransient)
	}
	var lastErr error
	for i, idx := range order {
		stream, err := p.keys[idx].LLM.Generate(ctx, req)
		if err == nil {
			p.succeeded(idx)
			return stream, nil
		}
		lastErr = fmt.Errorf("密钥 %s: %w", p.labelAt(idx), err)
		if ctx.Err() != nil || !errors.Is(err, port.ErrQuota) {
			// 非配额错误（含 ctx 取消）：不换钥匙——端点故障换钥匙没用。
			return nil, lastErr
		}
		p.cool(idx, port.QuotaAfter(err))
		if i+1 >= len(order) {
			break
		}
		if p.onRotate != nil {
			p.onRotate(ctx, p.labelAt(idx), p.labelAt(order[i+1]))
		}
	}
	// 本轮试过的钥匙都被限流：错误链里留着 *port.Quota，上层据此降级。
	return nil, lastErr
}

// Models 委派第一把钥匙：清单拉取不属轮换面（设置窗与 /model 只关心当前 provider），
// 也不该因为某把在冷却就让「看模型清单」失败。
func (p *KeyPool) Models(ctx context.Context) ([]port.ModelInfo, error) {
	return p.keys[0].LLM.Models(ctx)
}

// CountTokens 三级计数链②透传（D26）：取第一把钥匙的精确计数（同 provider 共用
// tokenizer，取哪一把都一样）。
func (p *KeyPool) CountTokens(ctx context.Context, text string) (int, error) {
	return countVia(ctx, p.keys[0].LLM, text)
}

// pick 本轮尝试顺序（未冷却的钥匙，按配置顺序从第一把起）与最早解冻时刻。
// 全冷却时返回空顺序——「最早解冻」用于报因（用户据此知道还要等多久）。
//
// 每轮都从第一把未冷却的钥匙起，不跨轮记住起点：`keys[]` 的语义是「按序轮换」的
// 偏好顺序（主账号优先），不是负载均衡——把上一次成功的钥匙置顶会让主账号在第一次
// 偶发限流后就长期坐冷板凳。
func (p *KeyPool) pick() ([]int, time.Time) {
	p.mu.Lock()
	defer p.mu.Unlock()
	now := p.now()
	order := make([]int, 0, len(p.keys))
	var earliest time.Time
	for idx := range p.keys {
		if until := p.cooledUntil[idx]; until.After(now) {
			if earliest.IsZero() || until.Before(earliest) {
				earliest = until
			}
			continue
		}
		order = append(order, idx)
	}
	return order, earliest
}

// succeeded 清掉成功那把的冷却（它确实是好的：限流可能是短时的、也可能只是那一次
// 请求撞上了窗口）。下次仍从第一把起试。
func (p *KeyPool) succeeded(idx int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.cooledUntil[idx] = time.Time{}
}

// cool 把某把钥匙冷却到 now+d（d <= 0 用缺省冷却）。只延后、不提前：连续两次 429 时
// 第二次的 `Retry-After` 更短，不该因此把冷却缩短——那会让刚被拒的钥匙立刻又被捞出来打。
func (p *KeyPool) cool(idx int, d time.Duration) {
	if d <= 0 {
		d = p.cooldown
	}
	if d <= 0 {
		d = defaultKeyCooldown
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if until := p.now().Add(d); until.After(p.cooledUntil[idx]) {
		p.cooledUntil[idx] = until
	}
}

// labelAt 钥匙展示名：用户填的 label 优先，空则「第 N 把」。
func (p *KeyPool) labelAt(idx int) string {
	if l := strings.TrimSpace(p.keys[idx].Label); l != "" {
		return l
	}
	return fmt.Sprintf("第 %d 把", idx+1)
}
