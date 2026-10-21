package platform

import (
	"path/filepath"
	"testing"
)

// utf16z 把字符串编成 UTF-16 码元 + NUL（对话框缓冲区的基本单位）。
func utf16z(s string) []uint16 {
	out := make([]uint16, 0, len(s)+1)
	for _, r := range s {
		switch {
		case r < 0x10000:
			out = append(out, uint16(r))
		default:
			r -= 0x10000
			out = append(out, uint16(0xD800+(r>>10)), uint16(0xDC00+(r&0x3FF)))
		}
	}
	return append(out, 0)
}

// bufOf 拼出对话框缓冲区（各段自带 NUL，末尾再补一个 = 双 NUL 终止）。
func bufOf(segs ...string) []uint16 {
	var buf []uint16
	for _, s := range segs {
		buf = append(buf, utf16z(s)...)
	}
	return append(buf, 0)
}

// TestParseMultiSelectSingle 单选形态：缓冲里只有一个完整路径——不能再拼目录。
func TestParseMultiSelectSingle(t *testing.T) {
	dir := `C:\Users\me\Documents`
	path := filepath.Join(dir, "报告.pdf")
	got := parseMultiSelect(bufOf(path))
	if len(got) != 1 || got[0] != path {
		t.Fatalf("parseMultiSelect = %q, want [%q]", got, path)
	}
}

// TestParseMultiSelectMany 多选形态：目录 + 各文件名 → 拼回完整路径（含空格与中文）。
func TestParseMultiSelectMany(t *testing.T) {
	dir := `C:\Users\me\Documents`
	buf := bufOf(dir, "报告 2026.pdf", "架构图.png", "notes.txt")
	got := parseMultiSelect(buf)
	want := []string{
		filepath.Join(dir, "报告 2026.pdf"),
		filepath.Join(dir, "架构图.png"),
		filepath.Join(dir, "notes.txt"),
	}
	if len(got) != len(want) {
		t.Fatalf("parseMultiSelect = %q, want %q", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("第 %d 项 = %q, want %q", i, got[i], want[i])
		}
	}
}

// TestParseMultiSelectCancel 取消：空缓冲 / 首个 token 为空 → nil（不弹空附件）。
func TestParseMultiSelectCancel(t *testing.T) {
	for _, buf := range [][]uint16{nil, {0, 0}, bufOf("   ")} {
		if got := parseMultiSelect(buf); len(got) != 0 {
			t.Fatalf("parseMultiSelect(%v) = %q, want 空", buf, got)
		}
	}
}

// TestParseMultiSelectSurrogate 代理对（emoji 文件名）不被拆坏。
func TestParseMultiSelectSurrogate(t *testing.T) {
	buf := bufOf(`C:\tmp`, "🎉.png")
	got := parseMultiSelect(buf)
	if len(got) != 1 || got[0] != filepath.Join(`C:\tmp`, "🎉.png") {
		t.Fatalf("parseMultiSelect = %q", got)
	}
}
