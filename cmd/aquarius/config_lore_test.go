package main

// 世界书激活段的归一与校验（§5.17/D294）：缺省开，扫描深度与预算比例越界在启动期拦下。

import (
	"testing"
)

// TestLoreDefaults 不写这一段 = 开；0 表示「没写」，交给 app 层补缺省。
func TestLoreDefaults(t *testing.T) {
	t.Parallel()
	o, err := (&fileConfig{}).loreOptions()
	if err != nil {
		t.Fatalf("loreOptions: %v", err)
	}
	if !o.Enabled {
		t.Fatal("缺省应当是开的")
	}
	if o.ScanDepth != 0 || o.BudgetRatio != 0 {
		t.Fatalf("省略的键应当是 0（交给缺省）: %+v", o)
	}
}

// TestLoreOverrides 三个键都能改；显式关就是关。
func TestLoreOverrides(t *testing.T) {
	t.Parallel()
	on, off := true, false
	cases := []struct {
		name string
		cfg  loreConfig
		want bool
	}{
		{"显式开", loreConfig{Enabled: &on}, true},
		{"显式关", loreConfig{Enabled: &off}, false},
		{"边界值", loreConfig{ScanDepth: 100, BudgetRatio: 1}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			o, err := (&fileConfig{Characters: charactersConfig{Lore: tc.cfg}}).loreOptions()
			if err != nil {
				t.Fatalf("loreOptions: %v", err)
			}
			if o.Enabled != tc.want {
				t.Fatalf("enabled = %v, want %v", o.Enabled, tc.want)
			}
		})
	}
	o, err := (&fileConfig{Characters: charactersConfig{
		Lore: loreConfig{ScanDepth: 7, BudgetRatio: 0.4},
	}}).loreOptions()
	if err != nil {
		t.Fatalf("loreOptions: %v", err)
	}
	if o.ScanDepth != 7 || o.BudgetRatio != 0.4 {
		t.Fatalf("override = %+v", o)
	}
}

// TestLoreRangeRejected 越界值启动即报错：负数或过大都会让激活器静默失效，不该放过。
func TestLoreRangeRejected(t *testing.T) {
	t.Parallel()
	bad := []loreConfig{
		{ScanDepth: -1},
		{ScanDepth: 101},
		{BudgetRatio: -0.1},
		{BudgetRatio: 1.01},
	}
	for _, c := range bad {
		if _, err := (&fileConfig{Characters: charactersConfig{Lore: c}}).loreOptions(); err == nil {
			t.Fatalf("越界值未报错: %+v", c)
		}
	}
}
