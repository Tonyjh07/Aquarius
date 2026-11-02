package app

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/Tonyjh07/Aquarius/internal/i18n"
)

// EffortLevels /effort 合法档位（D34）；"off" 表示清除（不发送）。
var EffortLevels = []string{"minimal", "low", "medium", "high"}

// ParseEffort 校验档位：off → ""（清除态），枚举外报错。装配根启动校验与
// /effort 命令共用同一口径（单一事实源）。
func ParseEffort(raw string) (string, error) {
	s := strings.ToLower(strings.TrimSpace(raw))
	switch s {
	case "off":
		return "", nil
	case "minimal", "low", "medium", "high":
		return s, nil
	default:
		return "", errors.New(i18n.Tf("think.effort_invalid", "value", fmt.Sprintf("%q", raw)))
	}
}

// execThink /think [on|off]（D34）：无参显示原生思考开关与当前档位；有参先写回
// config 再切 Agent 热态（同 /permission"写回失败不动运行态"口径）。
// 语义：总开关——off 时 reasoning_effort 与 enable_thinking 一律不发（覆盖 /effort）。
// D173：整条链没有思考能力时，即便开着也不发送，无参显示里要说明——否则用户会
// 以为「设了档位却没生效」是 bug。
func (s *Session) execThink(_ context.Context, args []string) (string, error) {
	// reasoningOn 当前链是否声明了思考能力（未注入 = 不判断，按老口径走）。
	reasoningOn := func() bool {
		st := s.status()
		return st.Caps == nil || st.Caps.Reasoning
	}
	if len(args) == 0 {
		out := i18n.Tf("think.state", "state", onOff(s.agent.ThinkOn()))
		if !reasoningOn() {
			out += "\n" + i18n.T("think.no_reasoning")
			return out, nil
		}
		if s.agent.ThinkOn() {
			if e := s.agent.Effort(); e != "" {
				out += "\n" + i18n.Tf("think.effort_line", "effort", e)
			} else {
				out += "\n" + i18n.T("think.effort_unset")
			}
		}
		return out, nil
	}
	if len(args) != 1 {
		return "", errors.New(i18n.T("usage.think"))
	}
	want := strings.EqualFold(strings.TrimSpace(args[0]), "on")
	if !strings.EqualFold(strings.TrimSpace(args[0]), "on") && !strings.EqualFold(strings.TrimSpace(args[0]), "off") {
		return "", errors.New(i18n.Tf("think.usage_state", "value", fmt.Sprintf("%q", args[0])))
	}
	// 开关的回执说的是命令里那个词（on / off），不是展示口径的「开 / 关」：
	// 用户敲的是哪个词，回执就照哪个词说。
	token := map[bool]string{true: "on", false: "off"}[want]
	if cur := s.agent.thinkState(); cur != nil && *cur == want {
		return i18n.Tf("think.already", "state", token), nil
	}
	if s.thinking == nil {
		return "", errors.New("session: " + i18n.T("think.unwired"))
	}
	if err := s.thinking.PersistThink(want); err != nil {
		return "", wrapSession("agent.err_persist", err)
	}
	s.agent.setThink(&want)
	out := i18n.Tf("think.switched", "state", token)
	if want && !reasoningOn() {
		out += "\n" + i18n.T("think.warn_no_reasoning")
	}
	return out, nil
}

// execEffort /effort [档位]（D34）：无参显示当前档位；有参校验（off=清除）后写回
// config 再热切。**只存值**：是否真发由 /think 总开关决定。
func (s *Session) execEffort(_ context.Context, args []string) (string, error) {
	if len(args) == 0 {
		if e := s.agent.Effort(); e != "" {
			return i18n.Tf("think.effort_only", "effort", e), nil
		}
		return i18n.T("think.effort_unset_usage"), nil
	}
	if len(args) != 1 {
		return "", errors.New(i18n.T("usage.effort"))
	}
	v, err := ParseEffort(args[0])
	if err != nil {
		return "", err
	}
	if v == s.agent.Effort() {
		if v == "" {
			return i18n.T("think.effort_cleared"), nil
		}
		return i18n.Tf("think.effort_already", "effort", v), nil
	}
	if s.thinking == nil {
		return "", errors.New("session: " + i18n.T("think.effort_unwired"))
	}
	if err := s.thinking.PersistEffort(v); err != nil {
		return "", wrapSession("agent.err_persist", err)
	}
	s.agent.setEffort(v)
	note := ""
	if st := s.status(); st.Caps != nil && !st.Caps.Reasoning {
		note = "\n" + i18n.T("think.effort_warn")
	}
	if v == "" {
		return i18n.T("think.effort_cleared_note"), nil
	}
	return i18n.Tf("think.effort_switched", "effort", v, "note", note), nil
}
