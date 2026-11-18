package app

// doctor.go `/doctor`（§7.14/D306）：把七处「能不能用」一次问清。
//
// 检查本身在装配根——配置路径、密钥库的口令链、目录布局、卷标都只有那里知道。这一层
// 只定义结论的形状与排版：装配根给「哪一项、什么状态、一句现象、一句修法」，命令面负责
// 让它在会话里读得下去（与 D276 同一条方向，app 不 import 那些适配器）。

import (
	"context"
	"errors"
	"strings"

	"github.com/Tonyjh07/Aquarius/internal/i18n"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

// HealthState 一项体检的结论。
//
// 只有三态没有「跳过」：跳过的项写「未接入」并记注意，比多一个状态少一处漏判——
// 一个「不知道」混在结论里，读的人会当成没问题。
type HealthState int

const (
	// HealthOK 现在能用。
	HealthOK HealthState = iota
	// HealthWarn 还能用，但离出事不远（阈值见 §7.14）。
	HealthWarn
	// HealthFail 现在就不能用。
	HealthFail
)

// HealthCheck 一项体检结论。
type HealthCheck struct {
	// ID 稳定标识（config / secrets / sessions / mcp / logs / exports / disk）：
	// 命令面按它取这一项的名字，装配根按它决定查什么。未知 ID 原样露出（§7.8 同规则：
	// 宁可看见一个丑标识，也不要一片空白）。
	ID string
	// State 三态结论。
	State HealthState
	// Detail 一句现象：正常时写关键数字（几个文件、多少可用），异常时写具体原因。
	Detail string
	// Fix 一句修法，只在非正常时有意义：改哪个文件、跑哪条命令、要不要重启。
	// 不猜用户意图，也不替他改文件。
	Fix string
}

// HealthProbe 体检面（装配根实现，D306）。
type HealthProbe interface {
	// Probe 跑一遍七项检查并返回结论。只读：不发请求、不连 MCP、不写盘。
	Probe(ctx context.Context) []HealthCheck
}

// execDoctor `/doctor`：跑一遍体检并把结论排版。
func (s *Session) execDoctor(ctx context.Context, cmd port.Command) (string, error) {
	if len(cmd.Args) > 0 {
		return "", errors.New(i18n.T("session.doctor.usage_err"))
	}
	if s.doctor == nil {
		return "", errors.New(i18n.T("session.doctor.unwired"))
	}
	return renderDoctor(s.doctor.Probe(ctx)), nil
}

// renderDoctor 排版：全绿只回一行，有问题时七项全列（D306 决策 3）。
func renderDoctor(checks []HealthCheck) string {
	bad := 0
	for _, c := range checks {
		if c.State != HealthOK {
			bad++
		}
	}
	if bad == 0 {
		return i18n.Tf("session.doctor.clean", "n", len(checks))
	}
	var b strings.Builder
	b.WriteString(i18n.Tf("session.doctor.head", "n", len(checks), "bad", bad))
	for _, c := range checks {
		b.WriteByte('\n')
		b.WriteString(i18n.Tf("session.doctor.item",
			"state", healthStateLabel(c.State),
			"name", healthCheckName(c.ID),
			"detail", c.Detail))
		if c.State != HealthOK && c.Fix != "" {
			b.WriteByte('\n')
			b.WriteString(i18n.Tf("session.doctor.fix", "fix", c.Fix))
		}
	}
	return b.String()
}

// healthStateLabel 三态的界面词。越界值按异常算——说「没问题」的代价比说「有问题」大。
func healthStateLabel(st HealthState) string {
	switch st {
	case HealthOK:
		return i18n.T("session.doctor.state.ok")
	case HealthWarn:
		return i18n.T("session.doctor.state.warn")
	}
	return i18n.T("session.doctor.state.fail")
}

// healthCheckName 检查项的名字。未登记的 ID 原样露出（见 HealthCheck.ID）。
func healthCheckName(id string) string {
	switch id {
	case "config":
		return i18n.T("session.doctor.name.config")
	case "secrets":
		return i18n.T("session.doctor.name.secrets")
	case "sessions":
		return i18n.T("session.doctor.name.sessions")
	case "mcp":
		return i18n.T("session.doctor.name.mcp")
	case "logs":
		return i18n.T("session.doctor.name.logs")
	case "exports":
		return i18n.T("session.doctor.name.exports")
	case "disk":
		return i18n.T("session.doctor.name.disk")
	}
	return id
}
