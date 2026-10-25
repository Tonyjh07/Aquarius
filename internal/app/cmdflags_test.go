package app

// cmdflags_test.go 命令参数 flag 扫描（/edit、/inject、/title 三处共用）：位置口径、个数
// 上限、值型 flag、缺参报因、正文拼接。三处命令的历史口径各不相同，这里逐条钉住，防止
// 将来「统一实现」顺手把某个口径统一掉。

import (
	"strings"
	"testing"
)

// TestScanFlagsLeading 前置口径（/edit、/title）：未声明的 token 即正文起点，其后即使出现
// 已声明的 flag 也当字面文本。
func TestScanFlagsLeading(t *testing.T) {
	decls := []flagDecl{{name: "--keep"}, {name: "--part", value: true}}
	cases := []struct {
		name  string
		args  []string
		flags []cmdFlag
		rest  string
	}{
		{"无 flag", []string{"正文", "两段"}, nil, "正文 两段"},
		{"开关 + 文本", []string{"--keep", "正文"}, []cmdFlag{{name: "--keep"}}, "正文"},
		{"值型 + 文本", []string{"--part", "2", "正文"}, []cmdFlag{{name: "--part", value: "2"}}, "正文"},
		{"值型在前文本在后", []string{"--part", "0", "改", "--keep"}, []cmdFlag{{name: "--part", value: "0"}}, "改 --keep"},
		{"未知 --x 即正文起点", []string{"--nope", "--keep"}, nil, "--nope --keep"},
		{"文本里的字面 flag 不消费", []string{"请加", "--keep", "参数"}, nil, "请加 --keep 参数"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			flags, rest, err := scanFlags(tc.args, flagScan{pos: flagsLeading}, decls)
			if err != nil {
				t.Fatalf("scan: %v", err)
			}
			if len(flags) != len(tc.flags) {
				t.Fatalf("flags = %+v, want %+v", flags, tc.flags)
			}
			for i := range flags {
				if flags[i] != tc.flags[i] {
					t.Fatalf("flags[%d] = %+v, want %+v", i, flags[i], tc.flags[i])
				}
			}
			if got := joinArgs(rest); got != tc.rest {
				t.Fatalf("rest = %q, want %q", got, tc.rest)
			}
		})
	}
}

// TestScanFlagsAnywhere 任意位置口径（/inject）：选项写在正文前后都认，正文次序不变。
func TestScanFlagsAnywhere(t *testing.T) {
	decls := []flagDecl{{name: "--source", value: true}, {name: "--no-trigger"}}
	flags, rest, err := scanFlags([]string{"前置", "--source", "tool:x", "--no-trigger", "后置"},
		flagScan{pos: flagsAnywhere}, decls)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if len(flags) != 2 || flags[0].name != "--source" || flags[0].value != "tool:x" || flags[1].name != "--no-trigger" {
		t.Fatalf("flags = %+v", flags)
	}
	if got := joinArgs(rest); got != "前置 后置" {
		t.Fatalf("rest = %q, want 前置 后置", got)
	}
}

// TestScanFlagsLimit 个数上限（/title 只认首参位的一个 --id）：超出的同名片归正文。
func TestScanFlagsLimit(t *testing.T) {
	decls := []flagDecl{{name: "--id", value: true}}
	flags, rest, err := scanFlags([]string{"--id", "abc", "--id", "def"}, flagScan{pos: flagsLeading, limit: 1}, decls)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if len(flags) != 1 || flags[0].value != "abc" {
		t.Fatalf("flags = %+v, want 只认第一个 --id", flags)
	}
	if got := joinArgs(rest); got != "--id def" {
		t.Fatalf("rest = %q, want 第二个 --id 归正文", got)
	}
}

// TestScanFlagsMissingValue 值型 flag 缺参数时报出是哪个 flag（措辞留给调用方的用法句）。
func TestScanFlagsMissingValue(t *testing.T) {
	decls := []flagDecl{{name: "--source", value: true}}
	for _, args := range [][]string{{"--source"}, {"正文", "--source"}} {
		_, _, err := scanFlags(args, flagScan{pos: flagsAnywhere}, decls)
		if err == nil || !strings.Contains(err.Error(), "--source") || !strings.Contains(err.Error(), "缺参数") {
			t.Fatalf("args = %v, err = %v", args, err)
		}
	}
}

// TestJoinArgs 正文拼接：单空格、去首尾空白（"改 这个词 " 与 "改 这个词" 视为同一句）。
func TestJoinArgs(t *testing.T) {
	for _, tc := range []struct {
		in   []string
		want string
	}{
		{nil, ""},
		{[]string{"改", "这个词"}, "改 这个词"},
		{[]string{"  两头空白  "}, "两头空白"},
		{[]string{"保留", "", "中间空段"}, "保留  中间空段"},
	} {
		if got := joinArgs(tc.in); got != tc.want {
			t.Fatalf("joinArgs(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestParseIndex 序号型 flag 的值：非负整数才认。
func TestParseIndex(t *testing.T) {
	for _, tc := range []struct {
		raw string
		n   int
		ok  bool
	}{
		{"0", 0, true},
		{"3", 3, true},
		{"-1", 0, false},
		{"x", 0, false},
		{"", 0, false},
	} {
		if n, ok := parseIndex(tc.raw); n != tc.n || ok != tc.ok {
			t.Fatalf("parseIndex(%q) = %d/%v, want %d/%v", tc.raw, n, ok, tc.n, tc.ok)
		}
	}
}
