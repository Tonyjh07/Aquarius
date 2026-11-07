package main

import (
	"encoding/json"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Tonyjh07/Aquarius/internal/port"
)

// delegateConf 从 JSON 片段解析配置（只关心 delegate 段）。
func delegateConf(t *testing.T, raw string) *fileConfig {
	t.Helper()
	var cfg fileConfig
	if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
		t.Fatalf("解析配置: %v", err)
	}
	return &cfg
}

// TestDelegateConfigDefaults 键缺失走缺省：总闸 1800、并发 2/4、enabled 缺失 = 启用。
func TestDelegateConfigDefaults(t *testing.T) {
	t.Parallel()
	cfg := delegateConf(t, `{"delegate":{"agents":[{"name":"a","command":"acp"}]}}`)
	opts, err := cfg.delegateOptions(nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("options: %v", err)
	}
	if opts.Timeout != port.DefaultDelegateTimeout {
		t.Fatalf("timeout = %v, want %v", opts.Timeout, port.DefaultDelegateTimeout)
	}
	if len(opts.Agents) != 1 || !opts.Agents[0].Enabled {
		t.Fatalf("agents = %+v（enabled 缺失应视为启用）", opts.Agents)
	}

	// 显式 0 = 不限；并发上限显式给值照用。
	cfg = delegateConf(t, `{"delegate":{"timeout_sec":0,"max_concurrent":1,"max_total":2,
		"agents":[{"name":"a","command":"acp","enabled":false}]}}`)
	opts, err = cfg.delegateOptions(nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("options: %v", err)
	}
	if opts.Timeout != 0 {
		t.Fatalf("timeout = %v, want 0（不限）", opts.Timeout)
	}
	if opts.MaxConcurrent != 1 || opts.MaxTotal != 2 {
		t.Fatalf("并发 = %d/%d", opts.MaxConcurrent, opts.MaxTotal)
	}
	if opts.Agents[0].Enabled {
		t.Fatal("enabled=false 应停用")
	}
	// 越界读次数：缺省不写 = nil（由 acpgate 取端口的 8）；显式给值照用（含 0 与 -1）。
	if opts.OutsideReadAsks != nil {
		t.Fatalf("越界读次数缺省应为 nil，got %d", *opts.OutsideReadAsks)
	}
	for _, n := range []int{0, 3, port.DelegateOutsideReadUnlimited} {
		cfg = delegateConf(t, `{"delegate":{"outside_read_max":`+strconv.Itoa(n)+`}}`)
		opts, err = cfg.delegateOptions(nil, nil, nil, nil)
		if err != nil {
			t.Fatalf("outside_read_max=%d: %v", n, err)
		}
		if opts.OutsideReadAsks == nil || *opts.OutsideReadAsks != n {
			t.Fatalf("outside_read_max = %v, want %d", opts.OutsideReadAsks, n)
		}
	}
}

// TestDelegateConfigValidation 配置错误在启动期报，不拖到第一次转交才暴露。
func TestDelegateConfigValidation(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		raw  string
		want string
	}{
		{"缺 name", `{"delegate":{"agents":[{"command":"x"}]}}`, "缺 name"},
		{"重名", `{"delegate":{"agents":[{"name":"a","command":"x"},{"name":"a","command":"y"}]}}`, "重复"},
		{"缺 command", `{"delegate":{"agents":[{"name":"a"}]}}`, "缺 command"},
		{"相对 workdir", `{"delegate":{"agents":[{"name":"a","command":"x","workdir":"rel"}]}}`, "绝对路径"},
		{"两条 default", `{"delegate":{"agents":[{"name":"a","command":"x","default":true},{"name":"b","command":"y","default":true}]}}`, "default"},
		{"default 却停用", `{"delegate":{"agents":[{"name":"a","command":"x","default":true,"enabled":false}]}}`, "enabled=false"},
		{"总闸超上限", `{"delegate":{"timeout_sec":86401}}`, "上限"},
		{"总闸为负", `{"delegate":{"timeout_sec":-1}}`, "不能为负"},
		{"并发为负", `{"delegate":{"max_total":-1}}`, "不能为负"},
		{"越界读次数太小", `{"delegate":{"outside_read_max":-2}}`, "最小是 -1"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := delegateConf(t, c.raw).delegateOptions(nil, nil, nil, nil)
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("err = %v, want 含 %q", err, c.want)
			}
		})
	}

	// 上限边界：86400 秒合法。
	cfg := delegateConf(t, `{"delegate":{"timeout_sec":86400}}`)
	opts, err := cfg.delegateOptions(nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("86400 应合法: %v", err)
	}
	if opts.Timeout != 24*time.Hour {
		t.Fatalf("timeout = %v", opts.Timeout)
	}
}

// TestDefaultConfigCarriesDelegateSection 模板里带上这一段（空清单）：用户照着填就能用，
// 不必去翻文档才知道键名。
func TestDefaultConfigCarriesDelegateSection(t *testing.T) {
	t.Parallel()
	if !strings.Contains(defaultConfig, `"delegate"`) {
		t.Fatal("默认配置缺 delegate 段")
	}
	cfg := delegateConf(t, defaultConfig)
	opts, err := cfg.delegateOptions(nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("模板应能解析: %v", err)
	}
	if len(opts.Agents) != 0 {
		t.Fatalf("模板不该预置代理: %+v", opts.Agents)
	}
}
