package app

import (
	"context"
	"errors"
	"strconv"
	"strings"

	"github.com/Tonyjh07/Aquarius/internal/i18n"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

// session_cmd_audit.go `/audit`：审计日志的查看口。
//
//	/audit                 最近 20 行
//	/audit -n 50           最近 50 行
//	/audit --tool file_read  只看该工具的记录
//
// 两个开关可同时用，顺序不限。命令只读：审计的写入口在适配器装饰器里，命令面
// 不提供清空与改写——「看得见」是这条命令的全部职责。
func (s *Session) execAudit(ctx context.Context, cmd port.Command) (string, error) {
	if s.auditPath == "" {
		return "", errors.New(i18n.T("session.audit.no_path"))
	}
	limit, tool := auditDefaultLines, ""
	args := cmd.Args
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--tool":
			if i+1 >= len(args) {
				return "", errors.New(i18n.T("usage.audit"))
			}
			tool = args[i+1]
			i++
		case strings.HasPrefix(a, "--tool="):
			tool = strings.TrimPrefix(a, "--tool=")
		case a == "-n":
			if i+1 >= len(args) {
				return "", errors.New(i18n.T("usage.audit"))
			}
			n, err := strconv.Atoi(args[i+1])
			if err != nil || n < 1 {
				return "", errors.New(i18n.T("usage.audit"))
			}
			limit = n
			i++
		case strings.HasPrefix(a, "-n"):
			n, err := strconv.Atoi(strings.TrimPrefix(a, "-n"))
			if err != nil || n < 1 {
				return "", errors.New(i18n.T("usage.audit"))
			}
			limit = n
		default:
			return "", errors.New(i18n.T("usage.audit"))
		}
	}
	rows, total, err := readAudit(s.auditPath)
	if err != nil {
		return "", err
	}
	return renderAudit(pickAuditRows(rows, limit, tool), total, tool), nil
}
