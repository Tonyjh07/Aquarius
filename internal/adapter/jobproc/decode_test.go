package jobproc

import (
	"strings"
	"testing"
	"unicode/utf8"

	"golang.org/x/text/encoding/simplifiedchinese"
)

// TestDecodeOutput D41：按行探测——UTF-8 合法原样、否则 GBK 解码、
// 结构非法字节落 U+FFFD（宽松解码器天然兜底）。
func TestDecodeOutput(t *testing.T) {
	enc := simplifiedchinese.GBK.NewEncoder()
	gbk := func(s string) string {
		t.Helper()
		b, err := enc.Bytes([]byte(s))
		if err != nil {
			t.Fatalf("gbk encode %q: %v", s, err)
		}
		return string(b)
	}
	for _, c := range []struct{ name, in, want string }{
		{"空串", "", ""},
		{"纯 ASCII 原样", "dir\r\nfile.txt\r\n", "dir\r\nfile.txt\r\n"},
		{"UTF-8 中文原样不误判", "中文输出 ok\n", "中文输出 ok\n"},
		{"手写 GBK 码点", "\xd6\xd0\xce\xc4", "中文"},
		{"编码器 GBK 往返", gbk("终端输出：目录"), "终端输出：目录"},
		{"混行各按各判", "ascii\n\xd6\xd0\xce\xc4\n中文\n", "ascii\n中文\n中文\n"},
		{"GBK 行尾 CRLF 保留", "\xd6\xd0\xce\xc4\r\n", "中文\r\n"},
		{"结构非法字节 U+FFFD", "\x9b2J", "�2J"},
		{"CP936 欧元字节", "\x80A", "€A"},
		{"孤立非法前导", "\xd6", "�"},
	} {
		t.Run(c.name, func(t *testing.T) {
			if !utf8.ValidString(c.want) {
				t.Fatalf("夹具 want 非法 UTF-8: %q", c.want)
			}
			got := decodeOutput(c.in)
			if got != c.want {
				t.Fatalf("decodeOutput(% x) = %q, want %q", c.in, got, c.want)
			}
			if !utf8.ValidString(got) {
				t.Fatalf("输出非法 UTF-8: % x", got)
			}
		})
	}
}

// TestCapBufferStringDecodes Run 的字节→文本出口按 D41 解码，截断标注附在解码之后。
func TestCapBufferStringDecodes(t *testing.T) {
	b := &capBuffer{max: 8}
	b.Write([]byte("\xd6\xd0\xce\xc4")) // 4 字节 GBK 中文
	b.Write([]byte(strings.Repeat("x", 64)))
	got := b.String()
	if !strings.Contains(got, "中文") {
		t.Fatalf("未解码: %q", got)
	}
	if !strings.Contains(got, "output exceeded the capture cap") {
		t.Fatalf("缺截断标注: %q", got)
	}
	if !utf8.ValidString(got) {
		t.Fatalf("非法 UTF-8: % x", got)
	}
}
