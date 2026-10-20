package app

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/Tonyjh07/Aquarius/internal/port"
)

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
		return formatModels(s.providerName, s.agent.modelName(), models, s.caps), nil
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
// 生效能力行（D173 整链取与）+ 可用清单（能力/单价标注）。
func formatModels(provider func() string, current string, models []port.ModelInfo, caps func() port.ModelCaps) string {
	var b strings.Builder
	if provider != nil && provider() != "" {
		fmt.Fprintf(&b, "当前 provider: %s\n", provider())
	}
	fmt.Fprintf(&b, "当前模型: %s\n", current)
	// 服务端不报模态，能力只来自配置；不显示的话，用户只能靠"发张图试试"来发现它关着。
	if caps != nil {
		c := caps()
		b.WriteString("生效能力: " + formatCaps(c) + "\n")
		var off []string
		if !c.Tools {
			off = append(off, "工具调用")
		}
		if !c.Reasoning {
			off = append(off, "思考")
		}
		if !c.Vision {
			off = append(off, "图片输入")
		}
		if len(off) > 0 {
			fmt.Fprintf(&b, "  未启用: %s（在 model.providers[].models[].caps 里声明；若只是降级链上的某一环缺，也会拉低整链）\n",
				strings.Join(off, "、"))
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
