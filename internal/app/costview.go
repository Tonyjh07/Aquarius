package app

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/Tonyjh07/Aquarius/internal/domain/conversation"
	"github.com/Tonyjh07/Aquarius/internal/i18n"
)

// costview.go `/cost` 的聚合与渲染（D302）：把**已经记在会话树上**的用量读成账。
//
// 不另起台账：每个 assistant 节点的 Usage 里已有 in/out 与 cost_usd（D205 由适配器按
// 单价折算），跨会话聚合只是再读一遍会话文件。多一份台账就多一处会与实际不符的事实。
//
// 三个视角回答三个不同的问题：
//   - session：这次对话花了多少（与 /usage 的会话累计同源，因此必然一致）
//   - model：各家模型各花了多少（跨全部会话——换模型比较单价才有意义）
//   - day：每天花了多少（跨全部会话，按节点创建时间分天）

// costSince day 视角只看这个窗口内改过的会话文件：更老的会话不必读盘，
// 而「每天花多少」这个问题不会问到上个月。
const costSince = 30 * 24 * time.Hour

// costDays 显示多少个有记录的日子（更多的日子用 -n 语义没必要：看趋势就够）。
const costDays = 14

// costTally 一组用量。
type costTally struct {
	in, out   int
	cacheRead int // 命中缓存的输入 tokens（D308；含在 in 里）
	usd       float64
	calls     int
	uncosted  int // 有 token 但 cost_usd 为 0 的调用数（未声明单价，未折算）
}

// add 累计一次生成；没有 token 的节点（root/system/user）不算一次调用。
func (t *costTally) add(m conversation.Message) {
	if m.Usage.InputTokens == 0 && m.Usage.OutputTokens == 0 {
		return
	}
	t.in += m.Usage.InputTokens
	t.out += m.Usage.OutputTokens
	t.cacheRead += m.Usage.CacheRead
	t.usd += m.Usage.CostUSD
	t.calls++
	if m.Usage.CostUSD == 0 {
		t.uncosted++
	}
}

// cachePct 输入侧的缓存命中占比（0–100 的整数）；没有输入时返回 0。
func (t *costTally) cachePct() int {
	if t.in <= 0 || t.cacheRead <= 0 {
		return 0
	}
	return int(float64(t.cacheRead)*100/float64(t.in) + 0.5)
}

// allMessages 读全部会话的**全部节点**（含放弃分支——放弃分支也真花过钱）。
//
// 只读 UpdatedAt 在窗口内的会话：更老的会话不可能有新节点，而一次全量读盘在百会话量级
// 也只是「一两秒」的量级，没必要为此建索引。
func (s *Session) allMessages(ctx context.Context) ([]conversation.Message, error) {
	list, err := s.store.List(ctx)
	if err != nil {
		return nil, wrapSession("err.cost_list", err)
	}
	since := s.clock.Now().Add(-costSince)
	var out []conversation.Message
	for _, sum := range list {
		if sum.UpdatedAt.Before(since) {
			continue
		}
		c, lerr := s.store.Load(ctx, sum.ID)
		if lerr != nil {
			continue // 单个会话读不出来不该让整份账失败（坏文件是另一件事）
		}
		out = append(out, c.Messages()...)
	}
	return out, nil
}

// costByModel 按模型分组（空模型归到一个占位名：旧数据里可能没有 model 字段）。
func costByModel(msgs []conversation.Message) ([]string, map[string]*costTally) {
	tallies := map[string]*costTally{}
	for _, m := range msgs {
		if m.Usage.InputTokens == 0 && m.Usage.OutputTokens == 0 {
			continue
		}
		name := m.Model
		if name == "" {
			name = "?"
		}
		t := tallies[name]
		if t == nil {
			t = &costTally{}
			tallies[name] = t
		}
		t.add(m)
	}
	keys := make([]string, 0, len(tallies))
	for k := range tallies {
		keys = append(keys, k)
	}
	// 花费降序：第一行就是最费钱的那家；同额按输入 token 降序，避免顺序随 map 迭代抖动。
	sort.Slice(keys, func(i, j int) bool {
		a, b := tallies[keys[i]], tallies[keys[j]]
		if a.usd != b.usd {
			return a.usd > b.usd
		}
		return a.in > b.in
	})
	return keys, tallies
}

// costByDay 按节点创建时间的本地日期分组，返回日期降序（最近的一天在最上面）。
func costByDay(msgs []conversation.Message) ([]string, map[string]*costTally) {
	tallies := map[string]*costTally{}
	for _, m := range msgs {
		if m.Usage.InputTokens == 0 && m.Usage.OutputTokens == 0 {
			continue
		}
		key := m.CreatedAt.Local().Format("2006-01-02")
		t := tallies[key]
		if t == nil {
			t = &costTally{}
			tallies[key] = t
		}
		t.add(m)
	}
	keys := make([]string, 0, len(tallies))
	for k := range tallies {
		keys = append(keys, k)
	}
	sort.Sort(sort.Reverse(sort.StringSlice(keys))) // 日期字符串可直接比大小
	if len(keys) > costDays {
		keys = keys[:costDays]
	}
	return keys, tallies
}

// renderCostSession 当前会话的一行账（与 /usage 同源，因此数字必然一致）。
func renderCostSession(rep UsageReport) string {
	var b strings.Builder
	b.WriteString(i18n.T("session.cost.head_session"))
	b.WriteByte('\n')
	b.WriteString(i18n.Tf("session.cost.row_session",
		"n", fmt.Sprint(rep.Gen), "in", fmtTokens(rep.SumIn), "out", fmtTokens(rep.SumOut),
		"usd", fmtUSD(rep.CostUSD)))
	if pct := rep.CachePct(); pct > 0 {
		b.WriteString(i18n.Tf("session.cost.cache", "pct", fmt.Sprint(pct)))
	}
	return b.String()
}

// renderCostRows 分组账：每组一行，末尾补「未折算」说明（只在真有未折算调用时）。
func renderCostRows(head string, keys []string, tallies map[string]*costTally) string {
	var b strings.Builder
	b.WriteString(head)
	if len(keys) == 0 {
		b.WriteByte('\n')
		b.WriteString(i18n.T("session.cost.empty"))
		return b.String()
	}
	for _, k := range keys {
		t := tallies[k]
		b.WriteByte('\n')
		b.WriteString(i18n.Tf("session.cost.row", "key", k,
			"in", fmtTokens(t.in), "out", fmtTokens(t.out),
			"usd", costPrice(t), "n", fmt.Sprint(t.calls)))
		// 命中率只在真有过命中时补：一行账里多两个「0%」不会让人更明白。
		if pct := t.cachePct(); pct > 0 {
			b.WriteString(i18n.Tf("session.cost.cache", "pct", fmt.Sprint(pct)))
		}
		// 部分未折算才补注脚：整组都没有单价时金额位已经写着「未折算」了。
		if t.uncosted > 0 && t.usd > 0 {
			b.WriteString(i18n.Tf("session.cost.uncosted", "n", fmt.Sprint(t.uncosted)))
		}
	}
	return b.String()
}

// fmtTokens token 数：五位数以上折成 k，账目用整数没必要精确到个位。
func fmtTokens(n int) string {
	if n >= 10000 {
		return fmt.Sprintf("%.1fk", float64(n)/1000)
	}
	return fmt.Sprint(n)
}

// costPrice 金额位：整组没有一个单价时写「未折算」而不是 $0.0000——后者会被读成免费
// （与 D205「未声明单价就整段不出现」同一个理由）。
func costPrice(t *costTally) string {
	if t.usd == 0 && t.uncosted > 0 {
		return i18n.T("session.cost.no_price")
	}
	return fmtUSD(t.usd)
}

// fmtUSD 金额：四位小数（一次调用的花费通常是这个量级）。
func fmtUSD(v float64) string {
	return fmt.Sprintf("$%.4f", v)
}
