package decorate

// 决策模型装饰器（D281/D290）：确认之前先问一次小模型「这次该不该打扰用户」。
//
// 语义边界：
//   - **只处理模型发起的链**：toolrun 与 acpgate 的确认（`OriginTool` / `OriginAgent`）。
//     用户自己敲的破坏性命令（`OriginCommand`）原样透传——那不是模型行为，没有可问的
//     上下文，也不该被一个概率数改写。
//   - **失败即上交**：超时、端点不通、解析不出，一律回到内层确认器。error 绝不等价于
//     Allow——这条线比省一次点击重要得多，所以没有「拿不准就放行」的分支。
//   - **`shadow` 与 `off` 的行为逐条一致**，差别只在审计里多一行。影子模式唯一能自证
//     的地方就在这里：先跑几天对着真实点击率看，再决定要不要切成 `on`。
//
// 阈值是热配置还是启动配置：启动配置。判定用的三个数由装饰器现取结构体里的值，而
// 结构体由装配根在启动时建好；改阈值要重启。这是实验性功能的代价，写进文档了。

import (
	"context"
	"time"

	"github.com/Tonyjh07/Aquarius/internal/domain/tool"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

// DecideMode 三档开关（D290）。`off` 时整个装饰器不装配，所以装饰器只认后两档。
type DecideMode string

const (
	// DecideOff 不启用：ToolRunner 与 acpgate 都看不到这个装饰器。
	DecideOff DecideMode = "off"
	// DecideShadow 只记审计、不改行为。用来攒真实样本。
	DecideShadow DecideMode = "shadow"
	// DecideOn 按阈值分流：明显该批的直接跑，明显该拒的（若启用）直接拒，其余问人。
	DecideOn DecideMode = "on"
)

// 判定默认值（D290）。都来自 2026-11-11 本机 clef-flash 的 92 次实测：放行两条件
// 并用时该批 20/21 放行、该问与该拒零误放；拒绝侧默认关闭，因为换一种问法召回就
// 掉一半，而被误拒的恰好是发版、部署这类用户真想做的事。
const (
	// DefaultDecideTimeout 单次判定总闸。本机热态 p50 165 ms、max 442 ms，冷启动 6.6 s
	// （靠 warmup 抹平，不靠把超时放大）。
	DefaultDecideTimeout = 1500 * time.Millisecond
	// DefaultAllowRiskBelow 「明显该批」的 risk 上界（score 三档期望值，值域 0–2）。
	DefaultAllowRiskBelow = 0.5
	// DefaultAllowHumanBelow 「明显该批」的 needs_human 上界。
	DefaultAllowHumanBelow = 0.15
	// DefaultDenyHumanAbove 「明显该拒」的 needs_human 下界（仅 DenyEnabled 时生效）。
	DefaultDenyHumanAbove = 0.85
)

// DecideDenyReason 自动拒绝的原因（英文——进模型上下文，toolrun 会拼成
// `user denied <tool>: <reason>`）。与 UnattendedReason 同款三件事：被谁拒、别重试、
// 在允许的范围内继续并把待批事项列出来。
const DecideDenyReason = "a local risk check refused this call. " +
	"Do not retry it; continue with what the current permission level allows and " +
	"list anything that still needs approval."

// DecidePolicy 一组阈值与开关。零值不可用，由配置侧填好默认值再交过来。
type DecidePolicy struct {
	Mode            DecideMode
	Timeout         time.Duration
	AllowRiskBelow  float64
	AllowHumanBelow float64
	DenyEnabled     bool
	DenyHumanAbove  float64
}

// Verdict 一次判定的结论（三档之一）。审计里也用它，`shadow` 下写的就是「本来会怎么判」。
type Verdict string

const (
	VerdictAllow Verdict = "allow"
	VerdictAsk   Verdict = "ask"
	VerdictDeny  Verdict = "deny"
	// VerdictError 没判成（超时/解析失败），行为等同于 Ask。
	VerdictError Verdict = "error"
)

// Judge 按阈值分流。放行要求两个条件同时成立：单看 risk 会放过 `git reset --hard`
// （实测 0.48），单看 needs_human 又只放行 12 条。
func (p DecidePolicy) Judge(d port.Decision) Verdict {
	if d.Risk < p.AllowRiskBelow && d.NeedsHuman < p.AllowHumanBelow {
		return VerdictAllow
	}
	if p.DenyEnabled && d.NeedsHuman >= p.DenyHumanAbove {
		return VerdictDeny
	}
	return VerdictAsk
}

// DecideRecord 一次判定留给审计的痕迹。`Request` 只取定位用的几个字段，完整请求
// （含 Prompt 与参数预览）不进审计——那里已经有 kind=tool 的一行。
type DecideRecord struct {
	Mode     DecideMode
	Verdict  Verdict
	Request  port.ConfirmRequest
	Decision port.Decision
	// Scored 是否真拿到了分数（失败时为 false，三个数都是零值，别当成「风险 0」）。
	Scored bool
	Policy DecidePolicy
	Err    string
}

// DecideSink 审计落点。nil = 不记（测试与无审计装配）。
type DecideSink interface {
	Decide(rec DecideRecord)
}

// decideConfirmer 决策模型装饰器。
type decideConfirmer struct {
	inner  port.Confirmer
	dec    port.Decider
	policy DecidePolicy
	sink   DecideSink
}

// Decide 包装确认器：先问决策模型，再决定要不要走到内层。
//
// dec 为 nil 或 mode 不是 `on` / `shadow` 时原样透传（装配根按 `off` 不装配，这里的
// 判断是给「配了但建不出客户端」留的兜底）。
func Decide(inner port.Confirmer, dec port.Decider, policy DecidePolicy, sink DecideSink) port.Confirmer {
	return decideConfirmer{inner: inner, dec: dec, policy: policy, sink: sink}
}

func (c decideConfirmer) Confirm(ctx context.Context, req port.ConfirmRequest) (port.ConfirmAnswer, error) {
	if err := ctx.Err(); err != nil {
		return port.ConfirmAnswer{}, err
	}
	if c.dec == nil || !decidable(req) {
		return c.pass(ctx, req)
	}
	dctx, cancel := context.WithTimeout(ctx, c.policy.Timeout)
	dec, err := c.dec.Decide(dctx, decideRequestOf(req))
	cancel()
	if err != nil {
		// 父 ctx 取消优先：用户按了中断，这不该记成一次「没判成」再回头去问他。
		if ctx.Err() != nil {
			return port.ConfirmAnswer{}, ctx.Err()
		}
		c.record(req, port.Decision{}, VerdictError, false, err)
		return c.pass(ctx, req)
	}
	verdict := c.policy.Judge(dec)
	c.record(req, dec, verdict, true, nil)
	if c.policy.Mode != DecideOn {
		// shadow：行为与 off 逐条一致，只多了上面那一行审计。
		return c.pass(ctx, req)
	}
	switch verdict {
	case VerdictAllow:
		return port.ConfirmAnswer{Allow: true}, nil
	case VerdictDeny:
		return port.ConfirmAnswer{Allow: false, Reason: DecideDenyReason}, nil
	}
	return c.pass(ctx, req)
}

// pass 交回内层确认器；没有内层时 fail-closed。
func (c decideConfirmer) pass(ctx context.Context, req port.ConfirmRequest) (port.ConfirmAnswer, error) {
	if c.inner == nil {
		return port.ConfirmAnswer{}, errNoConfirmer
	}
	return c.inner.Confirm(ctx, req)
}

func (c decideConfirmer) record(req port.ConfirmRequest, dec port.Decision, v Verdict, scored bool, err error) {
	if c.sink == nil {
		return
	}
	c.sink.Decide(DecideRecord{
		Mode: c.policy.Mode, Verdict: v, Request: req, Decision: dec,
		Scored: scored, Policy: c.policy, Err: errText(err),
	})
}

// decidable 这次确认该不该问模型。只认模型发起的两个来源：用户自己敲的命令
// （`OriginCommand`）不在决策模型的判断域里，问了只会得到一句与上下文无关的概率。
func decidable(req port.ConfirmRequest) bool {
	switch req.Origin {
	case port.OriginTool, port.OriginAgent:
		return true
	}
	return false
}

// decideRequestOf 把确认请求折成端口的六行输入。空字段原样留空，由适配器决定怎么
// 排版——这里不再做「猜一个默认值」的事。
func decideRequestOf(req port.ConfirmRequest) port.DecideRequest {
	op := ""
	if req.HasOp {
		op = req.Op.String()
	}
	return port.DecideRequest{
		Origin: string(req.Origin),
		Tool:   req.Tool,
		Op:     op,
		Target: req.Target,
		Risk:   riskWord(req),
		Task:   req.Task,
	}
}

// riskWord 调用方申报的风险档。
//
// 只有两档可报：工具规格里的 `tool.Risk` 是「要不要确认」的开关（safe / confirm），
// 不是严重度。所以 state 里这一行实际只会出现 low 与 medium——真正判严重度的是
// `risk` 那道 score 题，这一行只是给模型一个「这一步本来就要确认」的由头。没申报时
// 按 medium 交出去：「不知道」比假装 low 安全。
func riskWord(req port.ConfirmRequest) string {
	if !req.HasRisk {
		return "medium"
	}
	if req.Risk == tool.Safe {
		return "low"
	}
	return "medium"
}
