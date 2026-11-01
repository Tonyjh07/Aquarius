package app

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/Tonyjh07/Aquarius/internal/port"
)

// delegateAdmin 命令面看到的转交面（§5.18）：只列清单、叫停，**从不派活**——派活是
// 模型经 delegate 工具做的事（与 job_* 同一分工）。只声明这两项，「/delegate 能不能
// 自己起一个代理」就不再是靠读实现才能确认的问题。
type delegateAdmin interface {
	port.DelegateLister
	Kill(ctx context.Context, id port.DelegateID) error
}

// delegateReport /delegate 命令（§5.18）：list（缺省）/ kill <id>。
func (s *Session) delegateReport(ctx context.Context, args []string) (string, error) {
	sub := "list"
	if len(args) > 0 {
		sub, args = args[0], args[1:]
	}
	switch sub {
	case "list":
		if len(args) != 0 {
			return "", errors.New("用法: /delegate [list|kill <id>]")
		}
		if s.delegates == nil {
			return delegateEmptyHint, nil
		}
		list, err := s.delegates.List(ctx)
		if err != nil {
			return "", fmt.Errorf("session: 列出外部代理: %w", err)
		}
		if len(list) == 0 {
			return delegateEmptyHint, nil
		}
		var b strings.Builder
		for _, a := range list {
			state := "可用"
			switch {
			case a.Note != "":
				state = a.Note
			case !a.Available:
				state = "不可用"
			}
			name := a.Name
			if a.Title != "" {
				name += "（" + a.Title
				if a.Version != "" {
					name += " " + a.Version
				}
				name += "）"
			}
			fmt.Fprintf(&b, "%s  %s  %s\n", name, state, a.Command)
		}
		return strings.TrimRight(b.String(), "\n"), nil

	case "kill":
		if len(args) != 1 {
			return "", errors.New("用法: /delegate kill <id>")
		}
		if s.delegates == nil {
			return "", errors.New("session: 未配置外部代理（SessionDeps.Delegate），/delegate 不可用")
		}
		id := port.DelegateID(strings.TrimSpace(args[0]))
		if id == "" {
			return "", errors.New("用法: /delegate kill <id>")
		}
		if err := s.delegates.Kill(ctx, id); err != nil {
			return "", fmt.Errorf("session: 终止转交: %w", err)
		}
		return fmt.Sprintf("已叫停转交 %s", id), nil

	default:
		return "", errors.New("用法: /delegate [list|kill <id>]")
	}
}

// delegateEmptyHint 零可用代理时的指引：配置是唯一事实源（不内置预设，§5.18）。
const delegateEmptyHint = "未配置任何外部代理。在 config.json 的 delegate.agents 里写一条（command + 可选 args/env/workdir），" +
	"并确认该命令在 PATH 上；模型这时不会有 delegate 工具。"
