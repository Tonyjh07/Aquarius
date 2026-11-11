package app

import (
	"sort"
	"strings"

	"github.com/Tonyjh07/Aquarius/internal/domain/conversation"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

// 世界书激活（§5.17/D294）：把「这段对话提到了什么」与「卡里怎么写的」对上，命中的条目
// 拼成一段进本轮请求。整份逻辑是纯函数——吃条目、扫描文本与预算，出不进树的一段文字，
// 所以它可以在测试里逐条断言，也不依赖会话或磁盘。
//
// 三条口径都在这里落地：只扫用户与助手的正文（工具回填里偶然出现的词不该激活整卡设定）、
// 常驻条目跳过（它们已经随 persona 每轮都在）、装不下就整条不激活（截断后条目语义会变形）。

// 世界书缺省口径：扫描最近几条消息、允许占上下文预算的多少（§5.17/D294）。
const (
	// DefaultLoreScanDepth 扫描最近多少条用户/助手消息。
	DefaultLoreScanDepth = 4
	// DefaultLoreBudgetRatio 世界书能占上下文预算的比例。
	DefaultLoreBudgetRatio = 0.15
	// loreSectionSep 命中条目之间的分隔。
	loreSectionSep = "\n\n"
)

// LoreOptions 世界书激活口径（config `characters.lore` 解析后的结果）。
type LoreOptions struct {
	Enabled     bool
	ScanDepth   int     // <=0 取 DefaultLoreScanDepth
	BudgetRatio float64 // <=0 取 DefaultLoreBudgetRatio
}

// withDefaults 补齐零值：配置省略的键按缺省走，而不是「深度 0 = 一条都不扫」。
func (o LoreOptions) withDefaults() LoreOptions {
	if o.ScanDepth <= 0 {
		o.ScanDepth = DefaultLoreScanDepth
	}
	if o.BudgetRatio <= 0 {
		o.BudgetRatio = DefaultLoreBudgetRatio
	}
	return o
}

// LoreActivation 一次装配的激活结果。
type LoreActivation struct {
	// Entries 真正进上下文的条目，按 Order 降序（= 请求里的排列顺序）。
	Entries []port.BookEntry
	// Text 拼好的正文；空 = 这一段不发。
	Text string
	// Tokens 这段正文的估算 token。
	Tokens int
	// Hits 命中的条数（含被预算挡掉的）。
	Hits int
	// Dropped 命中但装不下的条数。
	Dropped int
	// Scanned 参与检索的条数（enabled 且非 constant）。
	Scanned int
}

// empty 是否没有任何条目进上下文。
func (a LoreActivation) empty() bool { return len(a.Entries) == 0 }

// ActivateLore 按关键词激活世界书条目（§5.17/D294）。
//
// entries 是卡里的全量条目（含常驻与禁用的，本函数自己筛）；scan 是最近若干条用户/助手
// 正文；maxCtx 是上下文预算 tokens，预算 = maxCtx × BudgetRatio。expand 做宏展开（`keys`
// 里的 `{{user}}` 不展开就会拿字面量去匹配），nil = 原样。
//
// 排序用「Order 降序、同 Order 保持卡内顺序」的稳定排序：集合不变时输出逐字节不变，
// 不会因为 map 遍历顺序或命中先后让前缀抖动。
func ActivateLore(entries []port.BookEntry, scan []string, opt LoreOptions, maxCtx int, expand func(string) string) LoreActivation {
	opt = opt.withDefaults()
	var act LoreActivation
	if !opt.Enabled {
		return act
	}
	if expand == nil {
		expand = func(s string) string { return s }
	}
	haystack := strings.Join(scan, "\n")
	if strings.TrimSpace(haystack) == "" {
		return act
	}
	// 大小写折叠一次算好：逐条逐键 ToLower 会让命中判定变成 O(条目 × 键 × 文本长度)。
	lowered := strings.ToLower(haystack)

	type hit struct {
		entry  port.BookEntry
		body   string
		tokens int
		index  int // 卡内序号：同 Order 时定序用
	}
	var hits []hit
	for i, e := range entries {
		if !e.Enabled || e.Constant {
			continue
		}
		act.Scanned++
		if !loreMatch(e, haystack, lowered, expand) {
			continue
		}
		body := strings.TrimSpace(expand(e.Content))
		if body == "" {
			continue // 命中但正文为空：没有可注入的东西，不计入命中
		}
		act.Hits++
		text := loreSection(e, body)
		hits = append(hits, hit{entry: e, body: body, tokens: estimateTokens(text), index: i})
	}
	if len(hits) == 0 {
		return act
	}
	sort.SliceStable(hits, func(i, j int) bool {
		if hits[i].entry.Order != hits[j].entry.Order {
			return hits[i].entry.Order > hits[j].entry.Order
		}
		return hits[i].index < hits[j].index
	})

	budget := int(float64(maxCtx) * opt.BudgetRatio)
	var parts []string
	for _, h := range hits {
		if budget <= 0 || act.Tokens+h.tokens > budget {
			act.Dropped++
			continue
		}
		parts = append(parts, loreSection(h.entry, h.body))
		act.Entries = append(act.Entries, h.entry)
		act.Tokens += h.tokens
	}
	act.Text = strings.Join(parts, loreSectionSep)
	return act
}

// loreMatch 任一主触发词出现在扫描文本里即命中；空触发词跳过（空串恒命中，等于全开）。
func loreMatch(e port.BookEntry, haystack, lowered string, expand func(string) string) bool {
	for _, k := range e.Keys {
		needle := strings.TrimSpace(expand(k))
		if needle == "" {
			continue
		}
		if e.CaseSensitive {
			if strings.Contains(haystack, needle) {
				return true
			}
			continue
		}
		if strings.Contains(lowered, strings.ToLower(needle)) {
			return true
		}
	}
	return false
}

// loreSection 一条命中条目的排版：有标题就加 `## <标题>` 小节头，没有就只放正文。
func loreSection(e port.BookEntry, body string) string {
	if t := strings.TrimSpace(e.Comment); t != "" {
		return "## " + t + "\n" + body
	}
	return body
}

// loreScanTexts 取最近 depth 条用户与助手消息的正文（D294），按时间顺序返回。
//
// 只取这两类角色的**正文**：system 是设定，注入节点是宿主补的话，工具输出是外部数据
// ——让它们参与命中判定，等于让一份命令输出激活整卡设定。思考分片同样不算：模型想过
// 什么不是「对话提到了什么」。音频的转写算正文（那确实是用户说的话），文档正文不算
// （附件很长，会把命中淹没在无关文字里）。
func loreScanTexts(path []conversation.Message, depth int) []string {
	if depth <= 0 {
		return nil
	}
	var out []string
	for i := len(path) - 1; i >= 0 && len(out) < depth; i-- {
		m := path[i]
		if m.Role != conversation.RoleUser && m.Role != conversation.RoleAssistant {
			continue
		}
		if s := loreText(m); s != "" {
			out = append(out, s)
		}
	}
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out
}

// loreText 一条消息参与匹配的正文：文字分片 + 音频转写。
func loreText(m conversation.Message) string {
	var b strings.Builder
	for _, p := range m.Content {
		switch p.Kind {
		case conversation.PartText:
			if s := strings.TrimSpace(p.Text); s != "" {
				b.WriteString(s)
				b.WriteByte('\n')
			}
		case conversation.PartAudio:
			if s := strings.TrimSpace(p.Transcript); s != "" {
				b.WriteString(s)
				b.WriteByte('\n')
			}
		}
	}
	return strings.TrimSpace(b.String())
}
