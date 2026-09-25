package app

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/Tonyjh07/Aquarius/internal/port"
)

// execModel /model [name]（DESIGN §7.3，D32）：
// 无参 = 当前模型 + LLM.Models() 可用列表（能力/单价标注）；
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
		return formatModels(s.agent.modelName(), models), nil
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

// formatModels /model 无参输出：当前模型行 + 可用清单（能力/单价标注）。
func formatModels(current string, models []port.ModelInfo) string {
	var b strings.Builder
	fmt.Fprintf(&b, "当前模型: %s\n", current)
	if len(models) == 0 {
		return b.String() + "（模型服务未返回可用列表，可直接 /model <name> 切换）"
	}
	fmt.Fprintf(&b, "可用模型（%d）:\n", len(models))
	for _, m := range models {
		fmt.Fprintf(&b, "  %s", m.Name)
		var marks []string
		if m.Vision {
			marks = append(marks, "视觉")
		}
		if m.Audio {
			marks = append(marks, "音频")
		}
		if len(marks) > 0 {
			fmt.Fprintf(&b, " [%s]", strings.Join(marks, "/"))
		}
		if m.CostIn > 0 || m.CostOutUSD > 0 {
			fmt.Fprintf(&b, " $%.3f/$%.3f 每千token", m.CostIn, m.CostOutUSD)
		}
		b.WriteByte('\n')
	}
	return strings.TrimRight(b.String(), "\n")
}
