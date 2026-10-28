package uicommon

// command_test.go 单行输入的切分：命令与正文的分派、引号算一个参数、正文不解释引号。

import (
	"strings"
	"testing"
)

// TestParseInputDispatch 以 `/` 开头是命令、其余是正文；空行给零值。
func TestParseInputDispatch(t *testing.T) {
	if got := ParseInput("  hello world  "); got.Text != "hello world" || got.Command != nil {
		t.Fatalf("正文 = %+v", got)
	}
	if got := ParseInput("   "); got.Text != "" || got.Command != nil {
		t.Fatalf("空行 = %+v, want 零值", got)
	}
	got := ParseInput("/new 项目 X")
	if got.Command == nil || got.Command.Name != "new" {
		t.Fatalf("命令 = %+v", got)
	}
	if strings.Join(got.Command.Args, "|") != "项目|X" {
		t.Fatalf("参数 = %q", got.Command.Args)
	}
}

// TestCommandFieldsQuotes 引号括起来的一段算一个参数——路径带空格靠的就是它。
func TestCommandFieldsQuotes(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{`/import "C:\我的文档\会话.aqx"`, []string{"/import", `C:\我的文档\会话.aqx`}},
		{`/export --path "C:\my docs\x.aqx"`, []string{"/export", "--path", `C:\my docs\x.aqx`}},
		{`/import '单引号 也行'`, []string{"/import", "单引号 也行"}},
		// 引号里外都能有内容（引号只影响切分，不吞掉前后文）。
		{`/import C:\a" b"c.aqx`, []string{"/import", `C:\a bc.aqx`}},
		// 不配对：剩下的整段算一个参数（交上去让命令面自己说哪里不对）。
		{`/import "没结尾`, []string{"/import", "没结尾"}},
		// 空引号是一个空参数，不是「没有参数」。
		{`/import ""`, []string{"/import", ""}},
		// 反斜杠不当转义符：Windows 路径原样保留。
		{`/import C:\Users\张三\a.aqx`, []string{"/import", `C:\Users\张三\a.aqx`}},
		// 多余空白压掉。
		{"  /import   a.aqx  ", []string{"/import", "a.aqx"}},
	}
	for _, tc := range cases {
		got := CommandFields(tc.in)
		if len(got) != len(tc.want) {
			t.Fatalf("%q = %q, want %q", tc.in, got, tc.want)
		}
		for i := range tc.want {
			if got[i] != tc.want[i] {
				t.Fatalf("%q 第 %d 段 = %q, want %q", tc.in, i, got[i], tc.want[i])
			}
		}
	}
}

// TestParseInputKeepsQuotesInText 正文里的引号原样保留：用户打的是什么，进树的就是什么。
func TestParseInputKeepsQuotesInText(t *testing.T) {
	const in = `他说"C:\path"就是这样`
	if got := ParseInput(in); got.Text != in {
		t.Fatalf("正文被改写了: %q, want %q", got.Text, in)
	}
}
