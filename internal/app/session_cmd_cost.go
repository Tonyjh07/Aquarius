package app

import (
	"context"
	"errors"
	"fmt"

	"github.com/Tonyjh07/Aquarius/internal/i18n"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

// session_cmd_cost.go `/cost`（D302）：用量与花费的三种视角。
//
//	/cost            当前会话（= session）
//	/cost model      按模型（跨全部会话）
//	/cost day        按天（跨全部会话，最近 30 天内改过的会话文件）
//
// 只读：数据全部来自会话树上的 Usage，命令面不写任何东西，也不新增台账文件。
func (s *Session) execCost(ctx context.Context, cmd port.Command) (string, error) {
	view := "session"
	switch {
	case len(cmd.Args) == 0:
	case len(cmd.Args) == 1:
		view = cmd.Args[0]
	default:
		return "", errors.New(i18n.T("usage.cost"))
	}
	switch view {
	case "session":
		rep, err := s.agent.UsageReport(ctx, s.cur)
		if err != nil {
			return "", wrapSession("err.estimate_usage", err)
		}
		return renderCostSession(rep), nil
	case "model":
		msgs, err := s.allMessages(ctx)
		if err != nil {
			return "", err
		}
		keys, tallies := costByModel(msgs)
		return renderCostRows(i18n.T("session.cost.head_model"), keys, tallies), nil
	case "day":
		msgs, err := s.allMessages(ctx)
		if err != nil {
			return "", err
		}
		keys, tallies := costByDay(msgs)
		return renderCostRows(i18n.Tf("session.cost.head_day", "days", fmt.Sprint(costDays)), keys, tallies), nil
	default:
		return "", errors.New(i18n.T("usage.cost"))
	}
}
