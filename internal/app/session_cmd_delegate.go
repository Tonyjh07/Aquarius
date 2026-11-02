package app

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/Tonyjh07/Aquarius/internal/i18n"
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
			return "", errors.New(i18n.T("delegate.usage"))
		}
		if s.delegates == nil {
			return delegateEmptyHint(), nil
		}
		list, err := s.delegates.List(ctx)
		if err != nil {
			return "", wrapSession("delegate.err_list", err)
		}
		if len(list) == 0 {
			return delegateEmptyHint(), nil
		}
		var b strings.Builder
		for _, a := range list {
			state := i18n.T("delegate.state.available")
			switch {
			case a.Note != "":
				state = a.Note
			case !a.Available:
				state = i18n.T("delegate.state.unavailable")
			}
			name := a.Name
			if a.Title != "" {
				if a.Version != "" {
					name += i18n.Tf("delegate.name.versioned", "title", a.Title, "version", a.Version)
				} else {
					name += i18n.Tf("delegate.name.titled", "title", a.Title)
				}
			}
			fmt.Fprintf(&b, "%s  %s  %s\n", name, state, a.Command)
		}
		return strings.TrimRight(b.String(), "\n"), nil

	case "kill":
		if len(args) != 1 {
			return "", errors.New(i18n.T("delegate.usage.kill"))
		}
		if s.delegates == nil {
			return "", errors.New("session: " + i18n.T("delegate.unwired"))
		}
		id := port.DelegateID(strings.TrimSpace(args[0]))
		if id == "" {
			return "", errors.New(i18n.T("delegate.usage.kill"))
		}
		if err := s.delegates.Kill(ctx, id); err != nil {
			return "", wrapSession("delegate.err_kill", err)
		}
		return i18n.Tf("delegate.killed", "id", id), nil

	default:
		return "", errors.New(i18n.T("delegate.usage"))
	}
}

// delegateEmptyHint 零可用代理时的指引：配置是唯一事实源（不内置预设，§5.18）。
func delegateEmptyHint() string { return i18n.T("delegate.empty_hint") }
