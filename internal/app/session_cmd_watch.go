package app

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/Tonyjh07/Aquarius/internal/port"
)

// watchReport /watch 命令（D190）：list（缺省）/ stop <id>。
//
// 只读与停止两条路：**声明**观察器是模型的活（watch_start），用户不该在这里手写
// 探针与判据——那等于让用户替模型调试；
// 但用户必须能看到它在盯什么、并且随时叫停。
func (s *Session) watchReport(ctx context.Context, args []string) (string, error) {
	if s.watches == nil {
		return "", errors.New("session: 未配置观察器（SessionDeps.Watches），/watch 不可用")
	}
	sub := "list"
	if len(args) > 0 {
		sub, args = args[0], args[1:]
	}
	switch sub {
	case "list":
		if len(args) != 0 {
			return "", errors.New("用法: /watch [list|stop <id>]")
		}
		list, err := s.watches.List(ctx)
		if err != nil {
			return "", fmt.Errorf("session: 列出观察器: %w", err)
		}
		if len(list) == 0 {
			return "（暂无观察器；模型可用 watch_start 声明一条）", nil
		}
		var b strings.Builder
		for _, st := range list {
			fmt.Fprintf(&b, "%s  %-7s %s\n", st.ID, st.Spec.Kind, st.Spec.Target)
			fmt.Fprintf(&b, "    %s  每 %s 一次，已查 %d 次，%s 结束\n",
				watchCriterionText(st.Spec), st.Spec.Interval, st.Checks, st.EndsAt.Format("15:04:05"))
		}
		return strings.TrimRight(b.String(), "\n"), nil

	case "stop":
		if len(args) != 1 {
			return "", errors.New("用法: /watch stop <id>")
		}
		list, err := s.watches.List(ctx)
		if err != nil {
			return "", fmt.Errorf("session: 列出观察器: %w", err)
		}
		id, err := port.ResolveWatchID(list, args[0])
		if err != nil {
			return "", fmt.Errorf("session: %w（/watch 查看可用 ID）", err)
		}
		if err := s.watches.Stop(ctx, id); err != nil {
			return "", fmt.Errorf("session: 停止观察: %w", err)
		}
		return fmt.Sprintf("已停止观察 %s", id), nil

	default:
		return "", errors.New("用法: /watch [list|stop <id>]")
	}
}

// watchCriterionText 判据的用户面措辞（D190）。
func watchCriterionText(spec port.WatchSpec) string {
	if strings.TrimSpace(spec.Match) == "" {
		return "判据：输出有变化"
	}
	return "判据：输出匹配 /" + spec.Match + "/"
}
