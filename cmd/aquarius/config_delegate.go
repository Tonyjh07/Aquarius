package main

// 外部代理转交的配置面（§5.18/D244）：`delegate.agents` 是**唯一事实源**——不内置各家
// 预设（registry 里四十多家代理的命令名、启动参数、登录方式都由它们自己决定，变得比
// 我们发版快）。这里只做解析、归一与上限校验。

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/Tonyjh07/Aquarius/internal/adapter/acpgate"
	"github.com/Tonyjh07/Aquarius/internal/domain/conversation"
	"github.com/Tonyjh07/Aquarius/internal/i18n"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

// delegateConfig config.json 的 delegate 段。
type delegateConfig struct {
	// Agents 代理清单（按声明序；`default` 最多一条）。
	Agents []delegateAgentConfig `json:"agents"`
	// TimeoutSec 单次转交总闸（秒）。键缺失 = 缺省 1800；显式 0 = 不限。
	TimeoutSec *int `json:"timeout_sec,omitempty"`
	// MaxConcurrent / MaxTotal 每会话与全局的同时转交上限；0/省略 = 端口缺省（2/4）。
	MaxConcurrent int `json:"max_concurrent,omitempty"`
	MaxTotal      int `json:"max_total,omitempty"`
}

// delegateAgentConfig 一个外部代理的声明（形状照 mcpServers：command + args + env）。
type delegateAgentConfig struct {
	Name    string            `json:"name"`
	Command string            `json:"command"`
	Args    []string          `json:"args,omitempty"`
	Env     map[string]string `json:"env,omitempty"` // 值支持 secret:<名>
	WorkDir string            `json:"workdir,omitempty"`
	// Roots 可访问目录根集（绝对路径）。v0.6.0 的 A2 拿它做 `fs/*` 越界判定的判据；
	// 本版只解析与落盘，执行侧不读（根集不是围栏，见 A3 一节）。
	Roots []string `json:"roots,omitempty"`
	// Enabled 键缺失 = 启用（写 false 才停用）：老配置没有这一段时不该把代理全关掉。
	Enabled *bool `json:"enabled,omitempty"`
	Default bool  `json:"default,omitempty"`
}

// delegateOptions 解析成 acpgate.Options（含全部校验）。配置错误在启动期报，不拖到
// 第一次转交才暴露——那时用户已经等了十几分钟。
func (c *fileConfig) delegateOptions(secrets port.Secrets, confirmer port.Confirmer,
	current func() conversation.ID, log port.Log) (acpgate.Options, error) {
	opts := acpgate.Options{Secrets: secrets, Confirmer: confirmer, CurrentSession: current, Log: log}
	d := c.Delegate

	if n := d.TimeoutSec; n != nil {
		if *n < 0 {
			return opts, fmt.Errorf("%s", i18n.Tf("cmd.config.err.delegate_timeout_neg", "n", *n))
		}
		if time.Duration(*n)*time.Second > port.MaxDelegateTimeout {
			return opts, fmt.Errorf("%s", i18n.Tf("cmd.config.err.delegate_timeout_max",
				"max", int(port.MaxDelegateTimeout/time.Second), "n", *n))
		}
		opts.Timeout = time.Duration(*n) * time.Second
	} else {
		opts.Timeout = port.DefaultDelegateTimeout
	}
	if d.MaxConcurrent < 0 || d.MaxTotal < 0 {
		return opts, fmt.Errorf("%s", i18n.T("cmd.config.err.delegate_negative"))
	}
	opts.MaxConcurrent, opts.MaxTotal = d.MaxConcurrent, d.MaxTotal

	seen := map[string]bool{}
	defaults := 0
	for i, a := range d.Agents {
		name := strings.TrimSpace(a.Name)
		if name == "" {
			return opts, fmt.Errorf("%s", i18n.Tf("cmd.config.err.delegate_name_missing", "i", i))
		}
		if seen[name] {
			return opts, fmt.Errorf("%s", i18n.Tf("cmd.config.err.delegate_name_dup",
				"i", i, "name", name))
		}
		seen[name] = true
		cmd := strings.TrimSpace(a.Command)
		if cmd == "" {
			return opts, fmt.Errorf("%s", i18n.Tf("cmd.config.err.delegate_command_missing",
				"i", i, "name", name))
		}
		wd := strings.TrimSpace(a.WorkDir)
		if wd != "" && !isAbsPath(wd) {
			return opts, fmt.Errorf("%s", i18n.Tf("cmd.config.err.delegate_workdir",
				"i", i, "name", name, "workdir", a.WorkDir))
		}
		roots, err := normalizeRoots(a.Roots)
		if err != nil {
			return opts, fmt.Errorf("%s", i18n.Tf("cmd.config.err.delegate_root",
				"i", i, "name", name, "root", err.Error()))
		}
		enabled := a.Enabled == nil || *a.Enabled
		if a.Default {
			defaults++
			if !enabled {
				return opts, fmt.Errorf("%s", i18n.Tf("cmd.config.err.delegate_default_disabled",
					"i", i, "name", name))
			}
		}
		opts.Agents = append(opts.Agents, acpgate.Agent{
			Name: name, Command: cmd, Args: a.Args, Env: a.Env,
			WorkDir: wd, Roots: roots, Enabled: enabled, Default: a.Default,
		})
	}
	if defaults > 1 {
		return opts, fmt.Errorf("%s", i18n.Tf("cmd.config.err.delegate_default_multi", "n", defaults))
	}
	return opts, nil
}

// isAbsPath 绝对路径判定：Windows 盘符与 Unix 斜杠都认（`\` 开头单独放行，
// 因为 `\share` 这类 UNC 简写在 filepath.IsAbs 下按平台不同）。
func isAbsPath(p string) bool {
	return filepath.IsAbs(p) || strings.HasPrefix(p, "/") || strings.HasPrefix(p, "\\")
}

// normalizeRoots 根集归一：去空白、去空项、去重（保序），并逐项校验绝对路径。
//
// 去重在解析期做而不在写盘期做：手写配置里重复一条不该让启动失败，但也不该让它
// 变成两条一样的判据。空项直接丢掉（多一行空行不是错误）。
func normalizeRoots(in []string) ([]string, error) {
	if len(in) == 0 {
		return nil, nil
	}
	seen := make(map[string]bool, len(in))
	out := make([]string, 0, len(in))
	for _, r := range in {
		r = strings.TrimSpace(r)
		if r == "" {
			continue
		}
		if !isAbsPath(r) {
			return nil, fmt.Errorf("%s", r)
		}
		if seen[r] {
			continue
		}
		seen[r] = true
		out = append(out, r)
	}
	if len(out) == 0 {
		return nil, nil
	}
	return out, nil
}
