package app

import (
	"context"
	"fmt"
	"strings"
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
		return "", fmt.Errorf("effort 须为 minimal|low|medium|high|off，当前 %q", raw)
	}
}

// execThink /think [on|off]（D34）：无参显示原生思考开关与当前档位；有参先写回
// config 再切 Agent 热态（同 /permission"写回失败不动运行态"口径）。
// 语义：总开关——off 时 reasoning_effort 与 enable_thinking 一律不发（覆盖 /effort）。
func (s *Session) execThink(_ context.Context, args []string) (string, error) {
	if len(args) == 0 {
		state := "开"
		if v := s.agent.thinkState(); v != nil && !*v {
			state = "关"
		}
		out := fmt.Sprintf("原生思考: %s（/think 总开关，off 时不发 reasoning_effort 与 enable_thinking）", state)
		if s.agent.ThinkOn() {
			if e := s.agent.Effort(); e != "" {
				out += fmt.Sprintf("\n推理档位: %s（/effort；随开关发送）", e)
			} else {
				out += "\n推理档位: 未设（交服务端默认；/effort 设置）"
			}
		}
		return out, nil
	}
	if len(args) != 1 {
		return "", fmt.Errorf("用法: /think [on|off]")
	}
	want := strings.EqualFold(strings.TrimSpace(args[0]), "on")
	if !strings.EqualFold(strings.TrimSpace(args[0]), "on") && !strings.EqualFold(strings.TrimSpace(args[0]), "off") {
		return "", fmt.Errorf("用法: /think [on|off]（当前 %q）", args[0])
	}
	if cur := s.agent.thinkState(); cur != nil && *cur == want {
		return fmt.Sprintf("原生思考已是 %s", map[bool]string{true: "on", false: "off"}[want]), nil
	}
	if s.persistThink == nil {
		return "", fmt.Errorf("session: 未配置思考持久化（SessionDeps.PersistThink），/think 切换不可用")
	}
	if err := s.persistThink(want); err != nil {
		return "", fmt.Errorf("session: 写回 config: %w", err)
	}
	s.agent.setThink(&want)
	return fmt.Sprintf("原生思考已切到 %s（已写回 config，重启沿用）", map[bool]string{true: "on", false: "off"}[want]), nil
}

// execEffort /effort [档位]（D34）：无参显示当前档位；有参校验（off=清除）后写回
// config 再热切。**只存值**：是否真发由 /think 总开关决定。
func (s *Session) execEffort(_ context.Context, args []string) (string, error) {
	if len(args) == 0 {
		if e := s.agent.Effort(); e != "" {
			return fmt.Sprintf("推理档位: %s（是否发送由 /think 决定）", e), nil
		}
		return "推理档位: 未设（交服务端默认；/effort minimal|low|medium|high|off）", nil
	}
	if len(args) != 1 {
		return "", fmt.Errorf("用法: /effort [minimal|low|medium|high|off]")
	}
	v, err := ParseEffort(args[0])
	if err != nil {
		return "", err
	}
	if v == s.agent.Effort() {
		if v == "" {
			return "推理档位已清除", nil
		}
		return fmt.Sprintf("推理档位已是 %s", v), nil
	}
	if s.persistEffort == nil {
		return "", fmt.Errorf("session: 未配置档位持久化（SessionDeps.PersistEffort），/effort 切换不可用")
	}
	if err := s.persistEffort(v); err != nil {
		return "", fmt.Errorf("session: 写回 config: %w", err)
	}
	s.agent.setEffort(v)
	if v == "" {
		return "推理档位已清除（已写回 config；/think on 时不再发送 reasoning_effort）", nil
	}
	return fmt.Sprintf("推理档位已切换为 %s（已写回 config，重启沿用；是否发送由 /think 决定）", v), nil
}
