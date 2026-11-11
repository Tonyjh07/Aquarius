package main

// decide 段的归一与校验（D290）：默认值取本机实测；越界与互相矛盾的阈值在启动期拦下。

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/Tonyjh07/Aquarius/internal/adapter/decorate"
)

// TestDecideDefaults 不写这一段 = 不启用，其余字段仍给全套默认值。
func TestDecideDefaults(t *testing.T) {
	t.Parallel()
	o, err := (&fileConfig{}).decideOptions()
	if err != nil {
		t.Fatalf("decideOptions: %v", err)
	}
	if o.Policy.Mode != decorate.DecideOff {
		t.Fatalf("mode = %q, want off", o.Policy.Mode)
	}
	if o.Policy.Timeout != 1500*time.Millisecond {
		t.Fatalf("timeout = %v", o.Policy.Timeout)
	}
	if o.Policy.AllowRiskBelow != 0.5 || o.Policy.AllowHumanBelow != 0.15 {
		t.Fatalf("放行阈值 = %v / %v", o.Policy.AllowRiskBelow, o.Policy.AllowHumanBelow)
	}
	if o.Policy.DenyEnabled || o.Policy.DenyHumanAbove != 0.85 {
		t.Fatalf("拒绝侧 = %v / %v", o.Policy.DenyEnabled, o.Policy.DenyHumanAbove)
	}
	if !o.Warmup || o.TaskChars != 80 {
		t.Fatalf("warmup / task_chars = %v / %d", o.Warmup, o.TaskChars)
	}
}

// TestDecideOverrides 三个开关与阈值都能改，改了就是改后的值。
func TestDecideOverrides(t *testing.T) {
	t.Parallel()
	f := func(v float64) *float64 { return &v }
	zero := 0
	off := false
	cfg := &fileConfig{Decide: decideConfig{
		Mode: "on", BaseURL: " http://127.0.0.1:11434 ", Model: " clef-flash ",
		TimeoutMS: 8000, Warmup: &off, TaskChars: &zero,
		Allow: decideAllowConfig{RiskBelow: f(0.7), HumanBelow: f(0.3)},
		Deny:  decideDenyConfig{Enabled: true, HumanAbove: f(0.9)},
	}}
	o, err := cfg.decideOptions()
	if err != nil {
		t.Fatalf("decideOptions: %v", err)
	}
	if o.Policy.Mode != decorate.DecideOn || o.Policy.Timeout != 8*time.Second {
		t.Fatalf("mode / timeout = %q / %v", o.Policy.Mode, o.Policy.Timeout)
	}
	if o.BaseURL != "http://127.0.0.1:11434" || o.Model != "clef-flash" {
		t.Fatalf("端点/模型 = %q / %q", o.BaseURL, o.Model)
	}
	if o.Warmup || o.TaskChars != 0 {
		t.Fatalf("warmup / task_chars = %v / %d", o.Warmup, o.TaskChars)
	}
	if o.Policy.AllowRiskBelow != 0.7 || o.Policy.AllowHumanBelow != 0.3 || o.Policy.DenyHumanAbove != 0.9 {
		t.Fatalf("阈值 = %+v", o.Policy)
	}
	if !o.Policy.DenyEnabled {
		t.Fatal("deny 应启用")
	}
}

// TestDecideRejectsBadValues 写错了就在启动期报错，不留到运行时。
func TestDecideRejectsBadValues(t *testing.T) {
	t.Parallel()
	f := func(v float64) *float64 { return &v }
	n := func(v int) *int { return &v }
	cases := []struct {
		name string
		cfg  decideConfig
		want string
	}{
		{"未知 mode", decideConfig{Mode: "auto"}, "decide.mode"},
		{"超时太短", decideConfig{TimeoutMS: 50}, "decide.timeout_ms"},
		{"超时太长", decideConfig{TimeoutMS: 60000}, "decide.timeout_ms"},
		{"任务截断越界", decideConfig{TaskChars: n(401)}, "decide.task_chars"},
		{"任务截断为负", decideConfig{TaskChars: n(-1)}, "decide.task_chars"},
		{"risk 阈值越界", decideConfig{Allow: decideAllowConfig{RiskBelow: f(2.1)}}, "decide.allow.risk_below"},
		{"human 阈值越界", decideConfig{Allow: decideAllowConfig{HumanBelow: f(1.2)}}, "decide.allow.human_below"},
		{"拒绝阈值越界", decideConfig{Deny: decideDenyConfig{HumanAbove: f(1.5)}}, "decide.deny.human_above"},
		{"两段重叠", decideConfig{
			Allow: decideAllowConfig{HumanBelow: f(0.9)},
			Deny:  decideDenyConfig{Enabled: true, HumanAbove: f(0.85)},
		}, "decide.allow.human_below"},
	}
	for _, c := range cases {
		_, err := (&fileConfig{Decide: c.cfg}).decideOptions()
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Fatalf("%s: err = %v, want 含 %q", c.name, err, c.want)
		}
	}
}

// TestDecideZeroThresholdMeansDefault 0 与省略同义（阈值 0 没有意义：那等于不放行任何
// 调用，想要这个效果应该用 mode: shadow）。
func TestDecideZeroThresholdMeansDefault(t *testing.T) {
	t.Parallel()
	zero := 0.0
	o, err := (&fileConfig{Decide: decideConfig{
		Allow: decideAllowConfig{RiskBelow: &zero, HumanBelow: &zero},
	}}).decideOptions()
	if err != nil {
		t.Fatalf("decideOptions: %v", err)
	}
	if o.Policy.AllowRiskBelow != 0.5 || o.Policy.AllowHumanBelow != 0.15 {
		t.Fatalf("阈值 = %v / %v", o.Policy.AllowRiskBelow, o.Policy.AllowHumanBelow)
	}
}

// TestDecideTemplateHasNoSection 模板不生成这一段：写了才生效，不写就当没有这个功能
// （实验性功能的默认形态是「不存在」）。
func TestDecideTemplateHasNoSection(t *testing.T) {
	t.Parallel()
	if strings.Contains(defaultConfig, `"decide"`) {
		t.Fatal("默认模板里不该出现 decide 段")
	}
	cfg := &fileConfig{}
	if err := json.Unmarshal([]byte(defaultConfig), cfg); err != nil {
		t.Fatalf("解析模板: %v", err)
	}
	o, err := cfg.decideOptions()
	if err != nil || o.Policy.Mode != decorate.DecideOff {
		t.Fatalf("模板应解析成 off: %q, %v", o.Policy.Mode, err)
	}
}

// TestDecideConfigParsedFromJSON 字段名与文档一致（写错名字等于没配，而且不会报错）。
func TestDecideConfigParsedFromJSON(t *testing.T) {
	t.Parallel()
	raw := `{"decide":{"mode":"shadow","base_url":"http://127.0.0.1:11434",` +
		`"model":"clef-flash","timeout_ms":1500,"warmup":true,` +
		`"allow":{"risk_below":0.5,"human_below":0.15},"deny":{"enabled":false,"human_above":0.85}}}`
	var cfg fileConfig
	if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
		t.Fatalf("解析: %v", err)
	}
	o, err := cfg.decideOptions()
	if err != nil {
		t.Fatalf("decideOptions: %v", err)
	}
	if o.Policy.Mode != decorate.DecideShadow || o.Model != "clef-flash" {
		t.Fatalf("mode / model = %q / %q", o.Policy.Mode, o.Model)
	}
	if o.Policy.DenyEnabled {
		t.Fatal("deny 默认应关闭")
	}
}
