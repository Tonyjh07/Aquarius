package toolbuiltin

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestFileReadChecksBinaryBeforePaging 大二进制文件必须先被判为二进制。
//
// 回归：判定写在大小截断之后时，超过上限的二进制会先被截成文本放行，
// 几 MB 乱码进上下文——参数合法、工具「成功」，模型只能照着乱码瞎猜。
func TestFileReadChecksBinaryBeforePaging(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "big.bin")
	buf := make([]byte, 2<<20)
	buf[0] = 0 // 头部 NUL：二进制特征
	if err := os.WriteFile(bin, buf, 0o644); err != nil {
		t.Fatal(err)
	}
	r := pick(t, New(Deps{}), "file_read")
	_, err := r.Execute(context.Background(), call(`{"path":`+js(bin)+`}`))
	if err == nil || !strings.Contains(err.Error(), "binary") {
		t.Fatalf("err = %v, want a binary rejection", err)
	}
}

// TestFileReadPagesLargeText 超大文本按页返回，不再整篇截断。
//
// 回归：旧实现超过 4MB 只给开头一截、且没有续读手段；分页后必须
// 每页带行号、页尾给下一页的 offset，模型才能把长文件读完。
func TestFileReadPagesLargeText(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "big.txt")
	var b strings.Builder
	for i := 1; i <= 5000; i++ {
		b.WriteString("line content\n")
	}
	if err := os.WriteFile(p, []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	r := pick(t, New(Deps{}), "file_read")

	res, err := r.Execute(context.Background(), call(`{"path":`+js(p)+`,"limit":2}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(res.Output, "1: line content") {
		t.Fatalf("first line = %q", res.Output)
	}
	if !strings.Contains(res.Output, "continue with offset: 3") {
		t.Fatalf("no continuation marker: %q", res.Output)
	}

	res, err = r.Execute(context.Background(), call(`{"path":`+js(p)+`,"offset":4999}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(res.Output, "4999: line content") || strings.Contains(res.Output, "continue with offset") {
		t.Fatalf("tail page = %q", res.Output)
	}

	res, err = r.Execute(context.Background(), call(`{"path":`+js(p)+`,"offset":9000}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Output, "empty page") {
		t.Fatalf("past-the-end page = %q", res.Output)
	}
}
