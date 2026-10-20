package toolbuiltin

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Tonyjh07/Aquarius/internal/port"
)

// editFile 准备一个文件并返回 file_edit 工具。
func editFile(t *testing.T, content string) (port.Tool, string) {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "a.txt")
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return pick(t, New(Deps{}), "file_edit"), p
}

func readFile(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// TestFileEditReplacesUniqueOccurrence 唯一命中即替换，回填说清替换了几处。
func TestFileEditReplacesUniqueOccurrence(t *testing.T) {
	tl, p := editFile(t, "alpha\nbeta\ngamma\n")
	res, err := tl.Execute(context.Background(), call(`{"path":`+js(p)+`,"old_string":"beta","new_string":"BETA"}`))
	if err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, p); got != "alpha\nBETA\ngamma\n" {
		t.Fatalf("file = %q", got)
	}
	if !strings.Contains(res.Output, "1 replacement") {
		t.Fatalf("output = %q", res.Output)
	}
}

// TestFileEditRejectsAmbiguous 多处命中且未开 replace_all：必须报错并说明怎么办。
//
// 这是 file_edit 最重要的一条：猜"应该是哪一处"会静默改错地方，
// 而改错的文件往往还能通过编译，错误要等到很久以后才暴露。
func TestFileEditRejectsAmbiguous(t *testing.T) {
	tl, p := editFile(t, "x\nx\nx\n")
	_, err := tl.Execute(context.Background(), call(`{"path":`+js(p)+`,"old_string":"x","new_string":"y"}`))
	if err == nil || !strings.Contains(err.Error(), "appears 3 times") {
		t.Fatalf("err = %v", err)
	}
	if !strings.Contains(err.Error(), "replace_all") {
		t.Fatalf("err = %v, want 提示 replace_all", err)
	}
	if got := readFile(t, p); got != "x\nx\nx\n" {
		t.Fatalf("失败时不应改动文件: %q", got)
	}

	res, err := tl.Execute(context.Background(), call(`{"path":`+js(p)+`,"old_string":"x","new_string":"y","replace_all":true}`))
	if err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, p); got != "y\ny\ny\n" || !strings.Contains(res.Output, "3 replacement") {
		t.Fatalf("file = %q, output = %q", got, res.Output)
	}
}

// TestFileEditPreservesCRLFAndBOM 换行与 BOM 原样保留（D172 口径四）。
// 模型给 LF 时要在比对前规约，否则同一段内容会因行尾差异而"找不到"。
func TestFileEditPreservesCRLFAndBOM(t *testing.T) {
	tl, p := editFile(t, "\xef\xbb\xbfline one\r\nline two\r\nline three\r\n")
	_, err := tl.Execute(context.Background(), call(`{"path":`+js(p)+`,"old_string":"line one\nline two","new_string":"line one\nLINE TWO"}`))
	if err != nil {
		t.Fatal(err)
	}
	got := readFile(t, p)
	if !strings.HasPrefix(got, "\xef\xbb\xbf") {
		t.Fatalf("BOM 丢失: %q", got)
	}
	if !strings.Contains(got, "LINE TWO\r\n") {
		t.Fatalf("CRLF 未保留: %q", got)
	}
	if strings.Contains(got, "\n") && strings.Contains(strings.ReplaceAll(got, "\r\n", ""), "\n") {
		t.Fatalf("混入了裸 LF: %q", got)
	}
}

// TestFileEditToleratesReindent 精确匹配失败时降级为"去首尾空白逐行比对"，
// 但多处命中仍拒绝（模糊匹配只用来找位置，不用来猜）。
func TestFileEditToleratesReindent(t *testing.T) {
	tl, p := editFile(t, "func f() {\n\treturn 1\n}\n")
	_, err := tl.Execute(context.Background(), call(`{"path":`+js(p)+`,"old_string":"    return 1","new_string":"\treturn 2"}`))
	if err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, p); got != "func f() {\n\treturn 2\n}\n" {
		t.Fatalf("file = %q", got)
	}
}

// TestFileEditKeepsLFWhenFileIsLF 降级匹配不能顺手把 LF 文件改成 CRLF：
// 用户没要求、也没被告知的重写，会让整个文件的 diff 变成"每一行都改了"。
func TestFileEditKeepsLFWhenFileIsLF(t *testing.T) {
	tl, p := editFile(t, "one\ntwo\nthree\n")
	// 模型给的是 CRLF 版本，且缩进对不上（触发降级路径）。
	_, err := tl.Execute(context.Background(), call(`{"path":`+js(p)+`,"old_string":"  two\r\n","new_string":"  TWO\r\n"}`))
	if err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, p); strings.Contains(got, "\r") {
		t.Fatalf("LF 文件被写成 CRLF: %q", got)
	}
}

// TestFileEditTypographicVariants 从文档粘贴时常见的弯引号/破折号/不断行空格：
// 屏幕上几乎一样，逐字节比较必然不中，所以降到"排版归一视图"匹配。
func TestFileEditTypographicVariants(t *testing.T) {
	tl, p := editFile(t, "say \u201chello\u201d now\n")
	_, err := tl.Execute(context.Background(), call(`{"path":`+js(p)+`,"old_string":"say \"hello\" now","new_string":"say \"bye\" now"}`))
	if err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, p); got != "say \"bye\" now\n" {
		t.Fatalf("file = %q", got)
	}

	// 命中多处仍要拒绝（模糊匹配只用来找位置）。
	tl2, p2 := editFile(t, "\u2014a\u2014b\n")
	_, err = tl2.Execute(context.Background(), call(`{"path":`+js(p2)+`,"old_string":"-","new_string":"+"}`))
	if err == nil || !strings.Contains(err.Error(), "appears 2 times") {
		t.Fatalf("err = %v, want 多处命中报错", err)
	}
}

// TestFileEditErrors 空 old_string、新旧相同、未命中、二进制、目录：各自报因，不猜。
func TestFileEditErrors(t *testing.T) {
	tl, p := editFile(t, "hello\n")
	cases := []struct {
		name string
		args string
		want string
	}{
		{"empty old", `{"path":` + js(p) + `,"old_string":"","new_string":"x"}`, "must not be empty"},
		{"same", `{"path":` + js(p) + `,"old_string":"hello","new_string":"hello"}`, "identical"},
		{"missing", `{"path":` + js(p) + `,"old_string":"nope","new_string":"x"}`, "was not found"},
	}
	for _, tc := range cases {
		if _, err := tl.Execute(context.Background(), call(tc.args)); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("%s: err = %v, want %q", tc.name, err, tc.want)
		}
	}

	dir := t.TempDir()
	if _, err := tl.Execute(context.Background(), call(`{"path":`+js(dir)+`,"old_string":"a","new_string":"b"}`)); err == nil ||
		!strings.Contains(err.Error(), "is a directory") {
		t.Fatalf("dir err = %v", err)
	}
	bin := filepath.Join(dir, "b.bin")
	if err := os.WriteFile(bin, []byte{1, 0, 2}, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := tl.Execute(context.Background(), call(`{"path":`+js(bin)+`,"old_string":"a","new_string":"b"}`)); err == nil ||
		!strings.Contains(err.Error(), "binary") {
		t.Fatalf("binary err = %v", err)
	}
	if _, err := tl.Execute(context.Background(), call(`{"path":"rel.txt","old_string":"a","new_string":"b"}`)); err == nil ||
		!strings.Contains(err.Error(), "must be absolute") {
		t.Fatalf("relative err = %v", err)
	}
}
