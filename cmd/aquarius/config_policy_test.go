package main

import (
	"strings"
	"testing"

	"github.com/Tonyjh07/Aquarius/internal/domain/perm"
)

// TestParsePolicyOverrides 例外表的配置解析：合法条目原样转成 perm 表，
// 拼错的一律报错（静默跳过会让用户以为已经设过）。
func TestParsePolicyOverrides(t *testing.T) {
	got, err := parsePolicyOverrides([]policyOverride{
		{Origin: "toolrun", Tool: " file_write ", Mode: "ask"},
		{Origin: "ACPGATE", Mode: "allow"},
	})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("条数 = %d, want 2", len(got))
	}
	if got[0].Origin != perm.OriginTool || got[0].Tool != "file_write" || got[0].Mode != perm.ModeAsk {
		t.Fatalf("第一条 = %+v", got[0])
	}
	if got[1].Origin != perm.OriginAgent || got[1].Tool != "" || got[1].Mode != perm.ModeAllow {
		t.Fatalf("第二条 = %+v", got[1])
	}

	if empty, err := parsePolicyOverrides(nil); err != nil || empty != nil {
		t.Fatalf("空表 = %v/%v, want nil/nil", empty, err)
	}

	bad := []struct {
		name string
		in   []policyOverride
		want string
	}{
		{"未知档位", []policyOverride{{Origin: "toolrun", Tool: "a", Mode: "deny"}}, "deny"},
		{"未知来源", []policyOverride{{Origin: "ui", Mode: "ask"}}, "ui"},
		{"toolrun 缺工具名", []policyOverride{{Origin: "toolrun", Mode: "ask"}}, "tool"},
	}
	for _, tc := range bad {
		_, err := parsePolicyOverrides(tc.in)
		if err == nil {
			t.Fatalf("%s: 应报错", tc.name)
		}
		if !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("%s: err = %v（缺 %q）", tc.name, err, tc.want)
		}
	}
}
