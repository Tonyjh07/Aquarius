package app

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/Tonyjh07/Aquarius/internal/i18n"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

// watchReport /watch 命令（D190）：list（缺省）/ stop <id>。
//
// 只读与停止两条路：**声明**观察器是模型的活（watch_start），用户不该在这里手写
// 探针与判据——那等于让用户替模型调试；
// 但用户必须能看到它在盯什么、并且随时叫停。
func (s *Session) watchReport(ctx context.Context, args []string) (string, error) {
	if s.watches == nil {
		return "", errors.New("session: " + i18n.T("watch.unwired"))
	}
	sub := "list"
	if len(args) > 0 {
		sub, args = args[0], args[1:]
	}
	switch sub {
	case "list":
		if len(args) != 0 {
			return "", errors.New(i18n.T("watch.usage"))
		}
		list, err := s.watches.List(ctx)
		if err != nil {
			return "", wrapSession("watch.err_list", err)
		}
		if len(list) == 0 {
			return i18n.T("watch.list.empty"), nil
		}
		var b strings.Builder
		for _, st := range list {
			fmt.Fprintf(&b, "%s  %-7s %s\n", st.ID, st.Spec.Kind, st.Spec.Target)
			b.WriteString(i18n.Tf("watch.list.item",
				"criterion", watchCriterionText(st.Spec),
				"interval", st.Spec.Interval,
				"checks", strconv.Itoa(st.Checks),
				"ends", st.EndsAt.Format("15:04:05")))
			b.WriteByte('\n')
		}
		return strings.TrimRight(b.String(), "\n"), nil

	case "stop":
		if len(args) != 1 {
			return "", errors.New(i18n.T("watch.usage.stop"))
		}
		list, err := s.watches.List(ctx)
		if err != nil {
			return "", wrapSession("watch.err_list", err)
		}
		id, err := port.ResolveWatchID(list, args[0])
		if err != nil {
			return "", fmt.Errorf("session: %w%s", err, i18n.T("watch.see_ids"))
		}
		if err := s.watches.Stop(ctx, id); err != nil {
			return "", wrapSession("watch.err_stop", err)
		}
		return i18n.Tf("watch.stopped", "id", id), nil

	default:
		return "", errors.New(i18n.T("watch.usage"))
	}
}

// watchCriterionText 判据的用户面措辞（D190）。
func watchCriterionText(spec port.WatchSpec) string {
	if strings.TrimSpace(spec.Match) == "" {
		return i18n.T("watch.criterion.changed")
	}
	return i18n.Tf("watch.criterion.match", "pattern", spec.Match)
}
