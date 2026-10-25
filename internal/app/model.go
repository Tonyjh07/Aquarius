package app

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Tonyjh07/Aquarius/internal/port"
)

// ModelLink 降级链上的一环（D173：`/model` 逐项标注用）。
type ModelLink struct {
	Provider, Model string
	Caps            port.ModelCaps
	Primary         bool
}

// ModelStatus 当前模型服务的静态口径快照（D173/D177：`/model` 与 `/think` 的展示依据）。
// 四项都是**启动期确定、运行态不热切**的事实（切换模型 = 重启生效），所以一次取回，
// 而不是拆成四个回调各问一次。
type ModelStatus struct {
	// Provider 当前生效的 provider 名；空 = 未声明（省略该行）。
	Provider string
	// Caps 整条降级链的生效能力（整链取与）。nil = 装配根未声明能力——
	// 这时展示面改从 Chain 推导，与「服务端不报模态、能力只来自配置」同一口径。
	Caps *port.ModelCaps
	// Chain 降级链逐环快照；空 = 未声明（省略链路与「谁拉低了整链」两段）。
	Chain []ModelLink
	// Budget 上下文预算口径；零值 = 未声明（Effective 为 0 时该行不显示）。
	Budget ContextBudget
}

// ModelCatalog 模型服务面（`/model` 的读侧）：可用清单 + 静态口径快照。
// 装配根把配置与 LLM 适配器适配到此接口；nil 时 `/model` 报「未配置模型服务」。
type ModelCatalog interface {
	// Models 服务端可用模型列表（经配置补能力位与单价）。
	Models(ctx context.Context) ([]port.ModelInfo, error)
	// Current 当前生效 provider/能力/降级链/预算的静态快照。
	Current() ModelStatus
}

// ModelProbe 能力探测面（`/model probe`，D174）：真请求、真花钱，故只由显式命令触发。
// 与 ModelCatalog 分开：读侧是纯查询，探测是带副作用的用户动作（同 Skills / SkillAdmin 的分法）。
type ModelProbe interface {
	// Probe 探测指定模型的能力（model 空 = 当前模型），判定明确的位写回配置。
	Probe(ctx context.Context, model string) (ProbeReport, error)
}

// ContextBudget 上下文预算的可读口径（D177：`/model` 展示用）。三处消费者（压缩阈值、
// /usage 占用、硬保底截断）用的是同一个 Effective，这里只解释它从哪儿来。
type ContextBudget struct {
	// Effective 实际生效的预算 tokens。
	Effective int
	// ModelWindow 当前模型声明的窗口；0 = 未声明（预算完全来自全局上限）。
	ModelWindow int
	// GlobalLimit 全局上限（limits.max_context_tokens）。
	GlobalLimit int
}

// ProbeReport 一次能力探测的结果（D174）：逐项结论 + 是否写回了配置。
// 探测请求的构造与判定在适配器（协议相关），这里只负责定位目标与呈现。
type ProbeReport struct {
	Provider string
	Model    string
	Results  []port.ProbeResult
	// Written 本次是否把判定明确的几位写回了配置（true = 重启后生效）。
	Written bool
}

// status 当前模型服务的静态口径快照；模型服务未接线时返回零值（各项都不显示，
// 与「未声明就省略该行」同一口径）。
func (s *Session) status() ModelStatus {
	if s.models == nil {
		return ModelStatus{}
	}
	return s.models.Current()
}

// execModel /model [name] | /model probe [name]（DESIGN §7.3，D32/D174）：
// 无参 = 当前 provider（D173）+ 当前模型 + 生效能力 + 上下文窗口（D177）+
// LLM.Models() 可用列表（能力/单价标注）；
// 有参 = 先写回 config（同 /permission 的原子写回模式）再 Agent 内热切换；
// probe 子命令 = 实测能力并写回判定明确的结果（D174）。
func (s *Session) execModel(ctx context.Context, args []string) (string, error) {
	if len(args) > 0 && strings.EqualFold(strings.TrimSpace(args[0]), "probe") {
		return s.execProbe(ctx, args[1:])
	}
	if len(args) == 0 {
		if s.models == nil {
			return "", errors.New("session: 未配置模型服务（SessionDeps.Models），/model 不可用")
		}
		models, err := s.models.Models(ctx)
		if err != nil {
			return "", fmt.Errorf("session: 列出可用模型: %w", err)
		}
		return formatModels(s.agent.modelName(), s.models.Current(), models), nil
	}
	if len(args) != 1 || strings.TrimSpace(args[0]) == "" {
		return "", errors.New("用法: /model [name] 或 /model probe [name]")
	}
	name := strings.TrimSpace(args[0])
	if s.persistModel == nil {
		return "", errors.New("session: 未配置模型写回（SessionDeps.PersistModel），/model 切换不可用")
	}
	// 先持久化再热切换（同 persistLevel 口径：写回失败不动运行态）。
	if err := s.persistModel(name); err != nil {
		return "", fmt.Errorf("session: 写回 config: %w", err)
	}
	s.agent.setModel(name)
	return fmt.Sprintf("已切换模型 → %s（已写回 config，重启沿用）", name), nil
}

// formatModels /model 无参输出：provider 行（D173，未声明时省略）+ 当前模型行 +
// 上下文预算行（D177）+ 生效能力行（D173 整链取与）+ 降级链逐环标注（哪一环拉低了
// 能力）+ 可用清单（能力/单价标注）。
func formatModels(current string, st ModelStatus, models []port.ModelInfo) string {
	var b strings.Builder
	if st.Provider != "" {
		fmt.Fprintf(&b, "当前 provider: %s\n", st.Provider)
	}
	fmt.Fprintf(&b, "当前模型: %s\n", current)
	// 上下文窗口（D177）：预算 = min(模型窗口, 全局上限)。不显示的话，用户看到压缩
	// 提前触发只会以为是 bug——窗口比全局小时必须说明是模型侧在压。
	if line := formatBudget(st.Budget); line != "" {
		b.WriteString(line + "\n")
	}
	// 服务端不报模态，能力只来自配置；不显示的话，用户只能靠"发张图试试"来发现它关着。
	var effective port.ModelCaps
	haveCaps := false
	if st.Caps != nil {
		effective = *st.Caps
		haveCaps = true
		b.WriteString("生效能力: " + formatCaps(effective) + "\n")
	}
	links := st.Chain
	// 逐环标注（D173）：链上任一模型缺某项，整链就按缺该项处理；不指出是哪一环，
	// 用户只能一个个去猜（「加一条弱模型」的代价必须是可见的）。
	if !haveCaps && len(links) > 0 {
		effective = links[0].Caps
		for _, l := range links[1:] {
			effective.Tools = effective.Tools && l.Caps.Tools
			effective.Reasoning = effective.Reasoning && l.Caps.Reasoning
			effective.Vision = effective.Vision && l.Caps.Vision
		}
		haveCaps = true
	}
	if haveCaps {
		var off []string
		if !effective.Tools {
			off = append(off, "工具调用")
		}
		if !effective.Reasoning {
			off = append(off, "思考")
		}
		if !effective.Vision {
			off = append(off, "图片输入")
		}
		if len(off) > 0 {
			line := fmt.Sprintf("  未启用: %s（在 model.providers[].models[].caps 里声明", strings.Join(off, "、"))
			if culprits := narrowLinks(links, effective); len(culprits) > 0 {
				line += "；拉低整链的是 " + strings.Join(culprits, "、")
			}
			b.WriteString(line + "）\n")
		}
	}
	if len(links) > 1 {
		b.WriteString("降级链:\n")
		for i, l := range links {
			fmt.Fprintf(&b, "  %d. %s / %s [%s]\n", i+1, l.Provider, l.Model, formatCaps(l.Caps))
		}
	}
	if len(models) == 0 {
		return b.String() + "（模型服务未返回可用列表，可直接 /model <name> 切换）"
	}
	fmt.Fprintf(&b, "可用模型（%d）:\n", len(models))
	for _, m := range models {
		fmt.Fprintf(&b, "  %s", m.Name)
		// 服务端列表不带能力位（零值 = 未知），只在确实声明了才标注。
		if marks := capsMarks(m.Caps); len(marks) > 0 {
			fmt.Fprintf(&b, " [%s]", strings.Join(marks, "/"))
		}
		if m.CostIn > 0 || m.CostOutUSD > 0 {
			fmt.Fprintf(&b, " $%g/$%g 每百万token", m.CostIn, m.CostOutUSD)
		}
		b.WriteByte('\n')
	}
	return strings.TrimRight(b.String(), "\n")
}

// execProbe /model probe [name]（D174 口径三）：手动发起的能力探测。
// 探测是真请求、真花钱，所以绝不自动跑；结果只写回「判定明确」的位。
func (s *Session) execProbe(ctx context.Context, args []string) (string, error) {
	if s.prober == nil {
		return "", errors.New("session: 未配置能力探测（SessionDeps.Prober），/model probe 不可用")
	}
	if len(args) > 1 {
		return "", errors.New("用法: /model probe [模型名]（省略 = 当前模型）")
	}
	name := ""
	if len(args) == 1 {
		name = strings.TrimSpace(args[0])
	}
	report, err := s.prober.Probe(ctx, name)
	out := formatProbe(report)
	if err != nil {
		if out != "" {
			return out, fmt.Errorf("session: 探测: %w", err)
		}
		return "", fmt.Errorf("session: 探测: %w", err)
	}
	return out, nil
}

// formatProbe 探测结果（D174）：三项一行一项，写清判定与依据；没结论的要说明「未改动」，
// 否则用户会以为「探测过了 = 支持」。
func formatProbe(r ProbeReport) string {
	if r.Model == "" && len(r.Results) == 0 {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "能力探测 %s / %s\n", r.Provider, r.Model)
	for _, res := range r.Results {
		label := probeLabel(res.Capability)
		fmt.Fprintf(&b, "  %s: %s", label, probeVerdictLabel(res.Verdict))
		if res.Reason != "" {
			fmt.Fprintf(&b, "（%s）", res.Reason)
		}
		fmt.Fprintf(&b, " · %s\n", shortDuration(res.Elapsed))
		if res.Verdict == port.ProbeSupported && res.Usage.OutputTokens > 0 {
			// 探测会花真钱，用量要让人看得到。
			fmt.Fprintf(&b, "      用量 %d→%d tokens\n", res.Usage.InputTokens, res.Usage.OutputTokens)
		}
	}
	if r.Written {
		b.WriteString("已按判定明确的项写回配置，重启后生效（无结论的项保持原声明）")
	} else {
		b.WriteString("未改动配置（没有判定明确的项，或结果与现有声明一致）")
	}
	return b.String()
}

// probeLabel 能力名 → 界面用词（与 /model 的能力行同口径）。
func probeLabel(capability string) string {
	switch capability {
	case port.CapTools:
		return "工具调用"
	case port.CapReasoning:
		return "思考"
	case port.CapVision:
		return "图片输入"
	}
	return capability
}

// probeVerdictLabel 三态判定 → 中文。
func probeVerdictLabel(v port.ProbeVerdict) string {
	switch v {
	case port.ProbeSupported:
		return "支持"
	case port.ProbeUnsupported:
		return "不支持"
	default:
		return "无结论"
	}
}

// shortDuration 耗时（探测是人工操作，秒级足够；零值不显示）。
func shortDuration(d time.Duration) string {
	if d <= 0 {
		return "耗时未知"
	}
	return fmt.Sprintf("%.1fs", d.Seconds())
}

// narrowLinks 链上哪些环节比整链生效能力更宽（即它们自己有能力、被别的环节拖下来
// 才没用上）。空 = 没有环被拉低。
func narrowLinks(links []ModelLink, effective port.ModelCaps) []string {
	var out []string
	for _, l := range links {
		var lost []string
		if l.Caps.Tools && !effective.Tools {
			lost = append(lost, "工具")
		}
		if l.Caps.Reasoning && !effective.Reasoning {
			lost = append(lost, "思考")
		}
		if l.Caps.Vision && !effective.Vision {
			lost = append(lost, "图片")
		}
		if len(lost) > 0 {
			out = append(out, fmt.Sprintf("%s/%s 失去了 %s", l.Provider, l.Model, strings.Join(lost, "、")))
		}
	}
	return out
}

// formatBudget 上下文预算行（D177）：写清生效值，以及是谁决定的。
func formatBudget(cb ContextBudget) string {
	if cb.Effective <= 0 {
		return ""
	}
	switch {
	case cb.ModelWindow <= 0:
		return fmt.Sprintf("上下文预算: %s tokens（全局上限；模型未声明窗口）", humanTokens(cb.Effective))
	case cb.ModelWindow <= cb.GlobalLimit || cb.GlobalLimit <= 0:
		return fmt.Sprintf("上下文预算: %s tokens（模型窗口 %s 在压；全局上限 %s）",
			humanTokens(cb.Effective), humanTokens(cb.ModelWindow), humanTokens(cb.GlobalLimit))
	default:
		return fmt.Sprintf("上下文预算: %s tokens（全局上限在压；模型窗口 %s）",
			humanTokens(cb.Effective), humanTokens(cb.ModelWindow))
	}
}

// humanTokens token 数 → 「1M / 64k」这类紧凑写法（整除才缩，否则原样）。
func humanTokens(n int) string {
	if n >= 1000000 && n%1000000 == 0 {
		return fmt.Sprintf("%dM", n/1000000)
	}
	if n >= 1000 && n%1000 == 0 {
		return fmt.Sprintf("%dk", n/1000)
	}
	return fmt.Sprint(n)
}

// formatCaps 能力位 → 「工具/思考/图片」或「（三项均未声明）」。
func formatCaps(c port.ModelCaps) string {
	marks := capsMarks(c)
	if len(marks) == 0 {
		return "（三项均未声明）"
	}
	return strings.Join(marks, " / ")
}

// capsMarks 能力位 → 中文标记列表（顺序固定：工具、思考、图片）。
func capsMarks(c port.ModelCaps) []string {
	var marks []string
	if c.Tools {
		marks = append(marks, "工具")
	}
	if c.Reasoning {
		marks = append(marks, "思考")
	}
	if c.Vision {
		marks = append(marks, "图片")
	}
	return marks
}
