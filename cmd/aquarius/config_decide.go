package main

// decide 段（D290）：模型辅助决策的配置面。实验性功能——模板不生成这一段，写了才
// 生效，不写就当没有。
//
// 校验分两层：**写错了**（未知 mode、阈值越界、两档阈值互相矛盾）在启动期报错；
// **没配全**（端点或模型名缺失）只让这一档退化成 off 并留一行错，不挡启动——与
// web 那几家的口径一致：少一个可选能力不该让人进不来。

import (
	"fmt"
	"strings"
	"time"

	"github.com/Tonyjh07/Aquarius/internal/adapter/decorate"
	"github.com/Tonyjh07/Aquarius/internal/i18n"
)

// decideConfig 模型辅助决策（D290）。
type decideConfig struct {
	// Mode off | shadow | on；空 = off。
	Mode string `json:"mode,omitempty"`
	// BaseURL 端点根，带不带 /v1 都认（适配器补全）。
	BaseURL string `json:"base_url,omitempty"`
	// APIKey `secret:名` 或明文；空 = 回落 secret:AQUARIUS_DECIDE_API_KEY。
	APIKey string `json:"api_key,omitempty"`
	// Model 服务端认的模型名（clef-flash / laya 这类）。
	Model string `json:"model,omitempty"`
	// TimeoutMS 单次判定总闸；0 = 1500。
	TimeoutMS int `json:"timeout_ms,omitempty"`
	// Warmup 启动后异步预热；键缺失 = 开。本机冷启动 6.6 s，不预热则首次判定必超时。
	Warmup *bool `json:"warmup,omitempty"`
	// TaskChars 外部代理任务截断；键缺失 = 80，显式 0 = 不带。
	TaskChars *int `json:"task_chars,omitempty"`
	// Allow 「明显该批」的两个上界。
	Allow decideAllowConfig `json:"allow"`
	// Deny 「明显该拒」这一档（默认关）。
	Deny decideDenyConfig `json:"deny"`
}

// decideAllowConfig 放行侧的两个条件，必须同时成立。
type decideAllowConfig struct {
	// RiskBelow risk 上界，值域 (0, 2]；键缺失或 0 = 0.5。
	RiskBelow *float64 `json:"risk_below,omitempty"`
	// HumanBelow needs_human 上界，值域 (0, 1]；键缺失或 0 = 0.15。
	HumanBelow *float64 `json:"human_below,omitempty"`
}

// decideDenyConfig 拒绝侧。
type decideDenyConfig struct {
	// Enabled 是否启用；键缺失 = 关。
	Enabled bool `json:"enabled,omitempty"`
	// HumanAbove needs_human 下界，值域 (0, 1]；键缺失或 0 = 0.85。
	HumanAbove *float64 `json:"human_above,omitempty"`
}

// decideOptions 归一后的配置：阈值与超时都填好了，装配根直接用。
type decideOptions struct {
	Policy    decorate.DecidePolicy
	BaseURL   string
	KeyRef    string
	Model     string
	Warmup    bool
	TaskChars int
}

// decideOptions 归一并校验 decide 段（启动期一次）。未知 mode 与越界阈值是启动错误；
// 端点与模型名的缺失留给装配根判——那时才知道该不该退化成 off。
func (c *fileConfig) decideOptions() (decideOptions, error) {
	o := decideOptions{
		Policy: decorate.DecidePolicy{
			AllowRiskBelow:  decorate.DefaultAllowRiskBelow,
			AllowHumanBelow: decorate.DefaultAllowHumanBelow,
			DenyHumanAbove:  decorate.DefaultDenyHumanAbove,
			Timeout:         decorate.DefaultDecideTimeout,
		},
		BaseURL:   strings.TrimSpace(c.Decide.BaseURL),
		KeyRef:    strings.TrimSpace(c.Decide.APIKey),
		Model:     strings.TrimSpace(c.Decide.Model),
		Warmup:    c.Decide.Warmup == nil || *c.Decide.Warmup,
		TaskChars: 80,
	}
	switch mode := strings.TrimSpace(c.Decide.Mode); mode {
	case "", string(decorate.DecideOff):
		o.Policy.Mode = decorate.DecideOff
	case string(decorate.DecideShadow):
		o.Policy.Mode = decorate.DecideShadow
	case string(decorate.DecideOn):
		o.Policy.Mode = decorate.DecideOn
	default:
		return o, fmt.Errorf("%s", i18n.Tf("cmd.config.err.decide_mode",
			"mode", mode, "a", string(decorate.DecideOff),
			"b", string(decorate.DecideShadow), "c", string(decorate.DecideOn)))
	}

	if ms := c.Decide.TimeoutMS; ms != 0 {
		if ms < 200 || ms > 10000 {
			return o, fmt.Errorf("%s", i18n.Tf("cmd.config.err.decide_range",
				"field", "decide.timeout_ms", "v", ms, "min", 200, "max", 10000))
		}
		o.Policy.Timeout = time.Duration(ms) * time.Millisecond
	}
	if c.Decide.TaskChars != nil {
		n := *c.Decide.TaskChars
		if n < 0 || n > 400 {
			return o, fmt.Errorf("%s", i18n.Tf("cmd.config.err.decide_range",
				"field", "decide.task_chars", "v", n, "min", 0, "max", 400))
		}
		o.TaskChars = n
	}

	var err error
	if o.Policy.AllowRiskBelow, err = decideThreshold(c.Decide.Allow.RiskBelow,
		o.Policy.AllowRiskBelow, "decide.allow.risk_below", 2); err != nil {
		return o, err
	}
	if o.Policy.AllowHumanBelow, err = decideThreshold(c.Decide.Allow.HumanBelow,
		o.Policy.AllowHumanBelow, "decide.allow.human_below", 1); err != nil {
		return o, err
	}
	if o.Policy.DenyHumanAbove, err = decideThreshold(c.Decide.Deny.HumanAbove,
		o.Policy.DenyHumanAbove, "decide.deny.human_above", 1); err != nil {
		return o, err
	}
	o.Policy.DenyEnabled = c.Decide.Deny.Enabled
	if o.Policy.DenyEnabled && o.Policy.AllowHumanBelow > o.Policy.DenyHumanAbove {
		// 两段重叠时放行先判：一条 needs_human 0.88 的调用会被「上界 0.9」放走，
		// 拒绝档形同虚设。这种组合只可能是手误，启动期拦下来。
		return o, fmt.Errorf("%s", i18n.Tf("cmd.config.err.decide_order",
			"allow", o.Policy.AllowHumanBelow, "deny", o.Policy.DenyHumanAbove))
	}
	return o, nil
}

// decideThreshold 取一个阈值：键缺失或 0 用默认值，否则校验值域 (0, max]。
func decideThreshold(v *float64, def float64, field string, max float64) (float64, error) {
	if v == nil || *v == 0 {
		return def, nil
	}
	if *v < 0 || *v > max {
		return def, fmt.Errorf("%s", i18n.Tf("cmd.config.err.decide_range",
			"field", field, "v", *v, "min", 0, "max", max))
	}
	return *v, nil
}
