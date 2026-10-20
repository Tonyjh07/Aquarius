package toolbuiltin

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestFileReadChecksBinaryBeforeTruncating 大二进制文件必须先被判为二进制。
// 回归：判定写在 4MB 截断之后时，超过上限的二进制会先被截成文本放行，
// 4MB 乱码进上下文——参数合法、工具「成功」，模型只会照着乱码瞎猜。
func TestFileReadChecksBinaryBeforeTruncating(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "big.bin")
	// 头部 NUL + 超过 maxFileRead 的填充，确保「先截断后判定」的实现会放行。
	buf := make([]byte, maxFileRead+1024)
	buf[0] = 0
	if err := os.WriteFile(bin, buf, 0o644); err != nil {
		t.Fatal(err)
	}
	r := pick(t, New(nil, nil, nil, ""), "file_read")
	_, err := r.Execute(context.Background(), call(`{"path":`+js(bin)+`}`))
	if err == nil || !strings.Contains(err.Error(), "binary") {
		t.Fatalf("err = %v, want a binary rejection", err)
	}
}
