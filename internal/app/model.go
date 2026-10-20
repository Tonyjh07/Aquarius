package app

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/Tonyjh07/Aquarius/internal/port"
)

// ModelLink 降级链上的一环（D173：`/model` 逐项标注用）。
type ModelLink struct {
	Provider, Model string
	Caps            port.ModelCaps
	Primary         bool
}

// execModel /model [name]（DESIGN §7.3，D32）：
// 无参 = 当前 provider（D173）+ 当前模型 + 生效能力 + LLM.Models() 可用列表（能力/单价标注）；
// 有参 = 先写回 config（同 /permission 的原子写回模式）再 Agent 内热切换。
func (s *Session) execModel(ctx context.Context, args []string) (string, error) {
	if len(args) == 0 {
		if s.listModels == nil {
			return "", errors.New("session: 未配置模型服务（SessionDeps.ListModels），/model 不可用")
		}
		models, err := s.listModels(ctx)
		if err != nil {
			return "", fmt.Errorf("session: 列出可用模型: %w", err)
		}
		return formatModels(s.providerName, s.agent.modelName(), models, s.caps, s.chain), nil
	}
	if len(args) != 1 || strings.TrimSpace(args[0]) == "" {
		return "", errors.New("用法: /model [name]")
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

// formatModels /model 无参输出：provider 行（D173，未注入时省略）+ 当前模型行 +
// 生效能力行（D173 整链取与）+ 降级链逐环标注（哪一环拉低了能力）+ 可用清单
// （能力/单价标注）。
func formatModels(provider func() string, current string, models []port.ModelInfo,
	caps func() port.ModelCaps, chain func() []ModelLink) string {
	var b strings.Builder
	if provider != nil && provider() != "" {
		fmt.Fprintf(&b, "当前 provider: %s\n", provider())
	}
	fmt.Fprintf(&b, "当前模型: %s\n", current)
	// 服务端不报模态，能力只来自配置；不显示的话，用户只能靠"发张图试试"来发现它关着。
	var effective port.ModelCaps
	haveCaps := false
	if caps != nil {
		effective = caps()
		haveCaps = true
		b.WriteString("生效能力: " + formatCaps(effective) + "\n")
	}
	links := []ModelLink(nil)
	if chain != nil {
		links = chain()
	}
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
			fmt.Fprintf(&b, " $%.3f/$%.3f 每千token", m.CostIn, m.CostOutUSD)
		}
		b.WriteByte('\n')
	}
	return strings.TrimRight(b.String(), "\n")
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
