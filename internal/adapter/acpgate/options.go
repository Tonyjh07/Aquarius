package acpgate

// 会话配置选项的应用（D287）：建会话之后、发任务之前，把配置里写死的选项设上。
//
// 值一律按代理广告的 **value** 精确匹配。两家的格式本来就不同——kimi 用配置里的模型键
// `gw/deepseek-v4.1-flash`，opencode 用 `providerID/modelID`——这两种值都拼不出来，也不该猜。
// 代理广告的 `name` 更不能拿来匹配：kimi 的 name 是另一个字符串
// （`trae/deepseek-v4.1-flash`，那是 config.toml 里 `model` 字段的值，不是 ACP 广告的 value）。
//
// 顺序固定：model → thought_level → mode → 其余按 id 升序。kimi 的 thinking 合法值跟着
// 当前模型走（`supportEfforts` 随模型变），先切模型再设 thinking 才有得选；而 Go 的 map
// 无序，不定顺序就会随机失败。

import (
	"context"
	"fmt"
	"sort"
	"strings"

	acp "github.com/coder/acp-go-sdk"

	"github.com/Tonyjh07/Aquarius/internal/port"
)

// optionListLimit 错误信息里最多列多少个候选值。opencode 一次广告二十多个模型，
// 全列出来会把报告淹掉；超出的只说还剩多少个。
const optionListLimit = 20

// applySessionOptions 按固定顺序把 want 里的选项设上，返回最新的配置快照。
//
// 每一项都独立校验、独立报错：多个选项时只说「设置失败」，用户没法定位是哪一项。
// 每次设置后用响应里的 configOptions 覆盖快照——后一项的候选可能由前一项决定。
func applySessionOptions(ctx context.Context, conn *acp.ClientSideConnection, sid acp.SessionId,
	snapshot []acp.SessionConfigOption, want map[string]string) ([]acp.SessionConfigOption, error) {
	for _, id := range orderOptionIDs(snapshot, want) {
		next, err := applyOneOption(ctx, conn, sid, snapshot, id, want[id])
		if err != nil {
			return snapshot, err
		}
		snapshot = next
	}
	return snapshot, nil
}

// applyOneOption 设一项：找到选项 → 校验候选 → 已经是这个值就跳过 → 设 → 读回确认。
func applyOneOption(ctx context.Context, conn *acp.ClientSideConnection, sid acp.SessionId,
	snapshot []acp.SessionConfigOption, id, want string) ([]acp.SessionConfigOption, error) {
	opt := findOption(snapshot, id)
	if opt == nil {
		return snapshot, fmt.Errorf("the agent has no session option %q; it offers %s",
			id, optionIDs(snapshot))
	}
	if opt.Select == nil {
		return snapshot, fmt.Errorf("session option %q is a on/off switch; this build only sets options that pick from a list", id)
	}
	values := selectValues(opt.Select)
	if !containsString(values, want) {
		return snapshot, fmt.Errorf("session option %q cannot be %q. The value has to be one the agent advertises, which may differ from the name its own interface shows; it offers %s",
			id, want, valueList(values))
	}
	// 已经是这个值就不发请求：省一次往返，也避开「同一值再设一次」在某些代理上的副作用。
	if string(opt.Select.CurrentValue) == want {
		return snapshot, nil
	}

	resp, err := conn.SetSessionConfigOption(ctx, acp.SetSessionConfigOptionRequest{
		ValueId: &acp.SetSessionConfigOptionValueId{
			SessionId: sid,
			ConfigId:  acp.SessionConfigId(id),
			Value:     acp.SessionConfigValueId(want),
		},
	})
	if err != nil {
		// 代理自己的话最有用：kimi 会说「这个模型没在 config.toml 里配」，
		// opencode 会说「model not found」。转述一层只会更糊。
		return snapshot, fmt.Errorf("cannot set session option %q: %w", id, err)
	}
	next := resp.ConfigOptions
	if next == nil {
		return snapshot, fmt.Errorf("the agent did not report its session options after setting %q, so the change cannot be confirmed", id)
	}
	// 只认读回的值：候选里有这一项不等于设得上（opencode 就广告过设不上的模型）。
	got := findOption(next, id)
	if got == nil || got.Select == nil || string(got.Select.CurrentValue) != want {
		return next, fmt.Errorf("session option %q was asked to be %q but the agent reports %s",
			id, want, reportedValue(got))
	}
	return next, nil
}

// optionsReport 报告用的选项清单。model 恒在（没配就写对方的默认值，否则读报告的人
// 不知道该用什么复现这份结论），其余只列配置指定过的项；没配的不列是噪音。
//
// 值取快照里最后回报的那一份，不是请求的那一份：代理自己改过也要如实写。
func optionsReport(configured map[string]string, snapshot []acp.SessionConfigOption) []port.OptionValue {
	ids := make([]string, 0, len(configured)+1)
	ids = append(ids, "model")
	rest := make([]string, 0, len(configured))
	for id := range configured {
		if id != "model" {
			rest = append(rest, id)
		}
	}
	sort.Strings(rest)
	ids = append(ids, rest...)

	out := make([]port.OptionValue, 0, len(ids))
	for _, id := range ids {
		opt := findOption(snapshot, id)
		if opt == nil || opt.Select == nil {
			continue // 该代理没有这一项（或它不是选择型）：没发生的事不进报告
		}
		// Set 看的是「现值与配置值一致」，不是「配置里有这一项」：设置失败时报告仍会被
		// 交出去（D268 要保住已收到的正文），那时现值还是对方的默认值，写成「已配置」
		// 会让读报告的人以为用上了。
		cur := string(opt.Select.CurrentValue)
		want, set := configured[id]
		out = append(out, port.OptionValue{Name: id, Value: cur, Set: set && want == cur})
	}
	return out
}

// orderOptionIDs 按固定顺序排要设的项。
func orderOptionIDs(snapshot []acp.SessionConfigOption, want map[string]string) []string {
	ids := make([]string, 0, len(want))
	for id := range want {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool {
		ri, rj := optionRank(snapshot, ids[i]), optionRank(snapshot, ids[j])
		if ri != rj {
			return ri < rj
		}
		return ids[i] < ids[j]
	})
	return ids
}

// optionRank 一项的执行次序：模型先行，然后是依赖模型的 thought_level，再是模式，
// 其余（含快照里没有的 id）殿后——认不出的项排在哪里都不影响前面两项的依赖。
func optionRank(snapshot []acp.SessionConfigOption, id string) int {
	opt := findOption(snapshot, id)
	if opt == nil || opt.Select == nil || opt.Select.Category == nil {
		return 3
	}
	switch *opt.Select.Category {
	case acp.SessionConfigOptionCategoryModel:
		return 0
	case acp.SessionConfigOptionCategoryThoughtLevel:
		return 1
	case acp.SessionConfigOptionCategoryMode:
		return 2
	}
	return 3
}

// findOption 按 id 找选项（select 与 switch 都算找到：调用方要能区分「没有」与
// 「有但不是选项列表」）。
func findOption(snapshot []acp.SessionConfigOption, id string) *acp.SessionConfigOption {
	for i := range snapshot {
		if snapshot[i].Select != nil && string(snapshot[i].Select.Id) == id {
			return &snapshot[i]
		}
		if snapshot[i].Boolean != nil && string(snapshot[i].Boolean.Id) == id {
			return &snapshot[i]
		}
	}
	return nil
}

// selectValues 展平候选值。协议给了两种形状（不分组的列表、分组后的列表），
// 展平后才能做一次查找。
func selectValues(o *acp.SessionConfigOptionSelect) []string {
	var out []string
	if u := o.Options.Ungrouped; u != nil {
		for _, v := range *u {
			out = append(out, string(v.Value))
		}
	}
	if g := o.Options.Grouped; g != nil {
		for _, grp := range *g {
			for _, v := range grp.Options {
				out = append(out, string(v.Value))
			}
		}
	}
	return out
}

// optionIDs 该代理有哪些配置项（没有时给一句能读懂的话，而不是一个空括号）。
func optionIDs(snapshot []acp.SessionConfigOption) string {
	var ids []string
	for i := range snapshot {
		switch {
		case snapshot[i].Select != nil:
			ids = append(ids, string(snapshot[i].Select.Id))
		case snapshot[i].Boolean != nil:
			ids = append(ids, string(snapshot[i].Boolean.Id))
		}
	}
	if len(ids) == 0 {
		return "none"
	}
	return quoteJoin(ids)
}

// valueList 候选值的一行写法：最多列 optionListLimit 个，其余只说还有多少个。
func valueList(values []string) string {
	if len(values) == 0 {
		return "none"
	}
	n := len(values)
	if n > optionListLimit {
		n = optionListLimit
	}
	s := quoteJoin(values[:n])
	if len(values) > n {
		s += fmt.Sprintf(" and %d more", len(values)-n)
	}
	return s
}

// reportedValue 代理回报的当前值（找不到这一项时说清是这种情形）。
func reportedValue(opt *acp.SessionConfigOption) string {
	if opt == nil {
		return "no such option"
	}
	if opt.Select == nil {
		return "a different kind of option"
	}
	return fmt.Sprintf("%q", string(opt.Select.CurrentValue))
}

func quoteJoin(items []string) string {
	q := make([]string, 0, len(items))
	for _, s := range items {
		q = append(q, fmt.Sprintf("%q", s))
	}
	return strings.Join(q, ", ")
}

func containsString(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}
