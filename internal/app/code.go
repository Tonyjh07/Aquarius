package app

// code.go /code 查看最近一次 run_code 脚本（D320，只读）：程序原文在确认之前就
// 留档（agent_tools.go），被拒绝的脚本也看得到。不提供重跑——执行路径只有模型的
// 工具调用一条（过确认），命令侧重跑要再造一条执行通路（D320 否决）。

import (
	"context"
	"strings"

	"github.com/Tonyjh07/Aquarius/internal/i18n"
)

// execCode 实现 /code：无参显示最近一次脚本全文（带时刻），没跑过给空态。
func (s *Session) execCode(_ context.Context, _ []string) (string, error) {
	last := s.agent.runCodeState()
	if last == nil {
		return i18n.T("code.empty"), nil
	}
	var b strings.Builder
	b.WriteString(i18n.Tf("code.header", "at", last.At.Local().Format("2006-01-02 15:04:05")))
	b.WriteString("\n")
	b.WriteString(last.Code)
	return b.String(), nil
}
