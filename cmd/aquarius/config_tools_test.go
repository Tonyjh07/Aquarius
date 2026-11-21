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
