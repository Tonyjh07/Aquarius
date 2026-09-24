package app

import (
	"context"
	"math"
	"strings"
	"unicode"

	"github.com/Tonyjh07/Aquarius/internal/port"
)

// 三级 token 计数链（DESIGN §7.1 轨2 / D26）：
//   ① 已发生的 → 服务端实测 usage（节点已记，/usage 直接汇总，不经本估算器）
//   ② 未发送的 → 适配器精确计数 port.TokenCounter（本地 tokenizer / count API），
//      实现即覆盖③；失败自然回落
//   ③ 通用估算（本文件）+ 服务端实测 usage 自校准（Calibrate 修正比值）
//
// 结构开销（消息角色分隔、模板定界符）按每条消息常数计入——业界通行做法
// （OpenAI cookbook 同法）；不实现 chat_template 渲染（D26 否决项）。

// 结构与估算常数（DESIGN D26）。
const (
	perMessageOverhead = 4 // 每条消息的结构开销（角色/模板定界符，token）
	calMin, calMax     = 0.5, 2.0
)

// defaultCompactThreshold / defaultMaxContextTokens Config 零值时的默认
// （DESIGN §8 limits.compact_threshold=0.7、max_context_tokens=64000）。
const (
	defaultCompactThreshold = 0.7
	defaultMaxContextTokens = 64000
)

// estimator 三级计数链的②③两层（①在树上，不经这里）。
type estimator struct {
	counter port.TokenCounter // ② 可选：适配器精确计数（覆盖估算）
	ratio   float64           // ③ 实测/估算 校准比值（仅作用于通用估算），clamp [0.5, 2]
}

// newEstimator 构造估算器；counter 可为 nil（纯估算）。
func newEstimator(counter port.TokenCounter) *estimator {
	return &estimator{counter: counter, ratio: 1.0}
}

// Estimate 估算一次请求的 token 占用，返回 (tokens, 是否精确计数②)。
// ②报错时静默回落③（计数失败不阻断对话）。
func (e *estimator) Estimate(ctx context.Context, req port.GenerateRequest) (int, bool) {
	payload, overhead := payloadOf(req)
	if e.counter != nil {
		if n, err := e.counter.CountTokens(ctx, payload); err == nil {
			return n + overhead, true
		}
	}
	n := estimateTokens(payload) + overhead
	return int(math.Ceil(float64(n) * e.ratio)), false
}

// Calibrate 用服务端实测 prompt tokens 校准③的估算比值（raw = 发送该请求时的原始估算）。
// raw<=0 或异常比值越界时收敛到 clamp 边界，保证估算始终落在合理带内。
func (e *estimator) Calibrate(raw, actual int) {
	if raw <= 0 || actual <= 0 {
		return
	}
	r := float64(actual) / float64(raw)
	switch {
	case r < calMin:
		r = calMin
	case r > calMax:
		r = calMax
	}
	e.ratio = r
}

// payloadOf 拼接请求的可计数文本（消息内容 + 工具调用参数 + 工具声明），
// 返回文本与结构开销（每消息常数；图片字节不计入——按占位粗估已含在常数里）。
func payloadOf(req port.GenerateRequest) (string, int) {
	var b strings.Builder
	for _, m := range req.Messages {
		for _, p := range m.Content {
			b.WriteString(p.Text)
			b.WriteByte('\n')
		}
		for _, call := range m.ToolCalls {
			b.Write(call.Args)
			b.WriteByte('\n')
		}
	}
	for _, s := range req.Tools {
		b.WriteString(s.Name)
		b.WriteByte('\n')
		b.WriteString(s.Description)
		b.WriteByte('\n')
		b.Write(s.Schema)
		b.WriteByte('\n')
	}
	return b.String(), perMessageOverhead * len(req.Messages)
}

// estimateTokens 通用字符估算（D26 ③）：ASCII ≈4 字符/token（OpenAI 官方口径）、
// CJK ≈1.5 字符/token、其余非 ASCII ≈2 字符/token。中文语料偏保守（宁早压缩勿撞上限）。
func estimateTokens(text string) int {
	var ascii, cjk, other int
	for _, r := range text {
		switch {
		case r < unicode.MaxASCII:
			ascii++
		case isCJK(r):
			cjk++
		default:
			other++
		}
	}
	f := float64(ascii)/4 + float64(cjk)/1.5 + float64(other)/2
	return int(math.Ceil(f))
}

// isCJK 汉字/假名/谚文判定（估算分语种用）。
func isCJK(r rune) bool {
	return unicode.Is(unicode.Han, r) ||
		unicode.Is(unicode.Hiragana, r) ||
		unicode.Is(unicode.Katakana, r) ||
		unicode.Is(unicode.Hangul, r)
}

// trimOldest 从最旧处裁剪消息直到估算低于 target（D21：压缩失败回退最旧裁剪——
// 保 leading system（persona/摘要）与最近消息、丢中间；至少保留 1 条非 system 消息，
// 且不得把"声明工具调用的 assistant"裁掉后留下孤儿 tool 结果）。返回 (保留序列, 省略条数)。
func (e *estimator) trimOldest(ctx context.Context, msgs []port.PromptMessage, target int) ([]port.PromptMessage, int) {
	lead := 0
	for lead < len(msgs) && msgs[lead].Role == "system" {
		lead++
	}
	if lead >= len(msgs) { // 全是 system：无可裁
		return msgs, 0
	}
	kept := append([]port.PromptMessage(nil), msgs...)
	omitted := 0
	nonSystem := func(ks []port.PromptMessage) int {
		n := 0
		for _, m := range ks[lead:] {
			if m.Role != "system" {
				n++
			}
		}
		return n
	}
	for {
		est, _ := e.Estimate(ctx, port.GenerateRequest{Messages: kept})
		if est < target || nonSystem(kept) <= 1 {
			break
		}
		kept = append(kept[:lead:lead], kept[lead+1:]...)
		omitted++
	}
	// 孤儿 tool 结果清理：前导 tool 消息的声明 assistant 已被裁掉（或本就在水位之上）。
	for nonSystem(kept) > 1 && lead < len(kept) && kept[lead].Role == "tool" {
		kept = append(kept[:lead:lead], kept[lead+1:]...)
		omitted++
	}
	return kept, omitted
}
