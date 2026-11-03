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
	// Enabled 键缺失 = 启用（写 false 才停用）：老配置没有这一段时不该把代理全关掉。
	Enabled *bool `json:"enabled,omitempty"`
	Default bool  `json:"default,omitempty"`
}

// delegateOptions 解析成 acpgate.Options（含全部校验）。配置错误在启动期报，不拖到
// 第一次转交才暴露——那时用户已经等了十几分钟。
func (c *fileConfig) delegateOptions(secrets port.Secrets, confirmer port.Confirmer,
	current func() conversation.ID, logf func(format string, args ...any)) (acpgate.Options, error) {
	opts := acpgate.Options{Secrets: secrets, Confirmer: confirmer, CurrentSession: current, Logf: logf}
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
		if wd != "" && !filepath.IsAbs(wd) && !strings.HasPrefix(wd, "/") && !strings.HasPrefix(wd, "\\") {
			return opts, fmt.Errorf("%s", i18n.Tf("cmd.config.err.delegate_workdir",
				"i", i, "name", name, "workdir", a.WorkDir))
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
			WorkDir: wd, Enabled: enabled, Default: a.Default,
		})
	}
	if defaults > 1 {
		return opts, fmt.Errorf("%s", i18n.Tf("cmd.config.err.delegate_default_multi", "n", defaults))
	}
	return opts, nil
}
