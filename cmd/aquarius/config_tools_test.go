package main

// config_tools_test.go tools.mode（D319）的校验锁定：键缺失 = native、指针语义与
// clockMode 同口径（没写 ≠ 写了空串）、词表外启动报错。
import "testing"

func TestToolsMode(t *testing.T) {
	native := "native"
	ptc := "ptc"
	warp := "warp"
	empty := ""
	cases := []struct {
		name string
		cfg  toolsConfig
		want string
		bad  bool
	}{
		{"键缺失", toolsConfig{}, "native", false},
		{"显式 native", toolsConfig{Mode: &native}, "native", false},
		{"显式 ptc", toolsConfig{Mode: &ptc}, "ptc", false},
		{"写了空串也按词表拒", toolsConfig{Mode: &empty}, "", true},
		{"词表外", toolsConfig{Mode: &warp}, "", true},
	}
	for _, tc := range cases {
		got, err := tc.cfg.toolsMode()
		if tc.bad {
			if err == nil {
				t.Fatalf("%s: err = nil, want 报错", tc.name)
			}
			continue
		}
		if err != nil || got != tc.want {
			t.Fatalf("%s: got = %q err = %v, want %q", tc.name, got, err, tc.want)
		}
	}
}

func TestPtcLimits(t *testing.T) {
	i := func(v int) *int { return &v }
	// 缺省：四键全默认（D319 实测值）。
	lim, err := ptcConfig{}.ptcLimits()
	if err != nil || lim.MaxSteps != 200_000_000 || lim.MaxCalls != 256 ||
		lim.MaxParallel != 10 || lim.ConfirmLimit != 8 {
		t.Fatalf("lim = %+v err = %v, want 全默认", lim, err)
	}
	// 显式改大生效。
	lim, err = ptcConfig{MaxCalls: i(1024), MaxSteps: i(500_000_000)}.ptcLimits()
	if err != nil || lim.MaxCalls != 1024 || lim.MaxSteps != 500_000_000 {
		t.Fatalf("lim = %+v err = %v, want 显式值", lim, err)
	}
	// 越界 fail-fast（下界与上界各一例）。
	for _, tc := range []ptcConfig{
		{MaxCalls: i(0)},
		{MaxCalls: i(4097)},
		{MaxSteps: i(999_999)},
		{MaxSteps: i(2_000_000_001)},
		{MaxParallel: i(65)},
		{ConfirmLimit: i(0)},
	} {
		if _, err := tc.ptcLimits(); err == nil {
			t.Fatalf("%+v: want 越界报错", tc)
		}
	}
}
