package perm

import (
	"strings"
	"testing"

	"github.com/Tonyjh07/Aquarius/internal/domain/tool"
)

// TestFileMatrix 用户矩阵（DESIGN §9 / D22）全表覆盖：四档 × {特权, 其他} × {读, 写}。
func TestFileMatrix(t *testing.T) {
	cases := []struct {
		level    Level
		inSandbox bool
		op       Op
		want     Decision
	}{
		// read-only：特权 r、其他 r —— 读全免，写全 ask。
		{ReadOnly, true, OpRead, Allow},
		{ReadOnly, false, OpRead, Allow},
		{ReadOnly, true, OpWrite, Ask},
		{ReadOnly, false, OpWrite, Ask},
		// strict：特权 rw、其他 r。
		{Strict, true, OpRead, Allow},
		{Strict, false, OpRead, Allow},
		{Strict, true, OpWrite, Allow},
		{Strict, false, OpWrite, Ask},
		// permissive：特权 rw、其他 rw。
		{Permissive, true, OpWrite, Allow},
		{Permissive, false, OpWrite, Allow},
		// full-access：全 rw。
		{FullAccess, true, OpWrite, Allow},
		{FullAccess, false, OpWrite, Allow},
	}
	for _, tc := range cases {
		if got := tc.level.File(tc.inSandbox, tc.op); got != tc.want {
			t.Errorf("File(%s, sandbox=%v, %s) = %s, want %s",
				tc.level, tc.inSandbox, tc.op, got, tc.want)
		}
	}
}

// TestToolColumn 工具列：Safe 全等级免；Confirm 仅 full-access 免、其余逐次（D22）。
func TestToolColumn(t *testing.T) {
	for _, l := range Levels {
		if got := l.Tool(tool.Safe); got != Allow {
			t.Errorf("Tool(%s, Safe) = %s, want allow", l, got)
		}
		want := Ask
		if l == FullAccess {
			want = Allow
		}
		if got := l.Tool(tool.Confirm); got != want {
			t.Errorf("Tool(%s, Confirm) = %s, want %s", l, got, want)
		}
	}
}

// TestMatrixDisplay 展示格由判定推导，与矩阵表一致。
func TestMatrixDisplay(t *testing.T) {
	cases := []struct {
		level          Level
		sandbox, other Cell
		toolsFree      bool
	}{
		{ReadOnly, CellR, CellR, false},
		{Strict, CellRW, CellR, false},
		{Permissive, CellRW, CellRW, false},
		{FullAccess, CellRW, CellRW, true},
	}
	for _, tc := range cases {
		s, o, free := tc.level.Matrix()
		if s != tc.sandbox || o != tc.other || free != tc.toolsFree {
			t.Errorf("Matrix(%s) = (%s,%s,%v), want (%s,%s,%v)",
				tc.level, s, o, free, tc.sandbox, tc.other, tc.toolsFree)
		}
	}
}

func TestParseAndDefault(t *testing.T) {
	if DefaultLevel != Strict {
		t.Fatalf("default = %s, want strict", DefaultLevel)
	}
	for _, in := range []string{"read-only", " STRICT ", "Permissive", "full-access"} {
		if _, err := Parse(in); err != nil {
			t.Errorf("Parse(%q) = %v, want ok", in, err)
		}
	}
	if _, err := Parse("root"); err == nil || !strings.Contains(err.Error(), "未知权限等级") {
		t.Fatalf("Parse(root) = %v, want 报因并列出可用等级", err)
	}
}
