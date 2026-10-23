package uicommon

import (
	"strings"
	"testing"

	"github.com/Tonyjh07/Aquarius/internal/domain/conversation"
)

// TestSanitizeControlStripsSequences 序列级剥离：CSI、OSC 的两种收尾、其余两字符转义、
// 截断序列、结尾裸 ESC、C1、非法 UTF-8。
func TestSanitizeControlStripsSequences(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"a\x1b[31mb", "ab"},
		{"a\x1b]0;title\x07b", "ab"},
		{"a\x1b]0;title\x1b\\b", "ab"}, // ST 收尾：反斜杠必须一并吞掉
		{"a\x1bMb", "ab"},
		{"截断\x1b[3", "截断"},
		{"尾部\x1b", "尾部"},
		{"a\u0085b", "ab"},     // 合法 UTF-8 的 C1（U+0085）
		{"a\xffb", "ab"},       // 非法字节 → RuneError
		{"a\x00b\x7fc", "abc"}, // NUL 与 DEL
	}
	for _, tc := range cases {
		if got := SanitizeControl(tc.in, KeepLayout); got != tc.want {
			t.Errorf("SanitizeControl(% x, KeepLayout) = % q, want % q", []byte(tc.in), got, tc.want)
		}
	}
}

// TestSanitizeControlLayout 换行与制表只在 KeepLayout 下保留。
func TestSanitizeControlLayout(t *testing.T) {
	const in = "行1\n行2\t制表"
	if got := SanitizeControl(in, KeepLayout); got != in {
		t.Errorf("KeepLayout 应保留换行与制表: %q", got)
	}
	if got := SanitizeControl(in, DropLayout); got != "行1行2制表" {
		t.Errorf("DropLayout 应连换行制表一并剥掉: %q", got)
	}
}

// TestImageModes 图片占位的三种策略各自的行为。
func TestImageModes(t *testing.T) {
	withRef := conversation.Part{
		Kind: conversation.PartImage,
		Ref:  &conversation.BlobRef{Hash: "abc123", Name: "shot.png"},
	}
	noHash := conversation.Part{
		Kind: conversation.PartImage,
		Ref:  &conversation.BlobRef{Name: "shot.png"},
	}
	bare := conversation.Part{Kind: conversation.PartImage}

	cases := []struct {
		name string
		mode ImageMode
		part conversation.Part
		want string
	}{
		{"默认始终占位", ImageAlways, withRef, "〔图片〕"},
		{"终端无引用也占位", ImageAlways, bare, "〔图片〕"},
		{"GUI 有引用不占位", ImageWhenUnaddressable, withRef, ""},
		{"GUI 无哈希留占位", ImageWhenUnaddressable, noHash, "〔图片〕"},
		{"GUI 无引用留占位", ImageWhenUnaddressable, bare, "〔图片〕"},
		{"TUI 带上文件名", ImageNamed, withRef, "〔图片：shot.png〕"},
		{"TUI 无引用用泛称", ImageNamed, bare, "〔图片〕"},
	}
	for _, tc := range cases {
		var b strings.Builder
		AppendPartText(&b, tc.part, tc.mode)
		if got := b.String(); got != tc.want {
			t.Errorf("%s: 得到 %q, want %q", tc.name, got, tc.want)
		}
	}
}

// TestPartsTextSkipsThinkingAndTool 思考/工具分片不进正文（各自单独成块）。
func TestPartsTextSkipsThinkingAndTool(t *testing.T) {
	parts := []conversation.Part{
		{Kind: conversation.PartText, Text: "正文"},
		{Kind: conversation.PartThinking, Text: "思考"},
		{Kind: conversation.PartTool, Text: "工具"},
		{Kind: conversation.PartDoc, Text: "·文档"},
	}
	if got := PartsText(parts, ImageAlways); got != "正文·文档" {
		t.Errorf("PartsText = %q, want %q", got, "正文·文档")
	}
}

// TestPartsTextSanitizesFinal 拼接结果整体剥离控制序列。
func TestPartsTextSanitizesFinal(t *testing.T) {
	parts := []conversation.Part{
		{Kind: conversation.PartText, Text: "前\x1b[2J"},
		{Kind: conversation.PartText, Text: "后"},
	}
	if got := PartsText(parts, ImageAlways); got != "前后" {
		t.Errorf("PartsText = %q, want %q", got, "前后")
	}
}
