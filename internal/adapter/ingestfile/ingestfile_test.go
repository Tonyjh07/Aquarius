package ingestfile

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Tonyjh07/Aquarius/internal/adapter/blobfs"
	"github.com/Tonyjh07/Aquarius/internal/domain/conversation"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

// newIngestor 附件库（临时目录）+ 文件摄取器。
func newIngestor(t *testing.T) (*Ingestor, *blobfs.Store, string) {
	t.Helper()
	dir := t.TempDir()
	blobs, err := blobfs.New(filepath.Join(dir, "attachments"))
	if err != nil {
		t.Fatalf("blobfs: %v", err)
	}
	return New(blobs), blobs, dir
}

// writeFile 写入测试文件并返回绝对路径。
func writeFile(t *testing.T, dir, name string, data []byte) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, data, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	return p
}

// refContent 读取引用的附件内容。
func refContent(t *testing.T, blobs *blobfs.Store, ref conversation.BlobRef) []byte {
	t.Helper()
	rc, err := blobs.Get(context.Background(), ref)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer rc.Close()
	data, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	return data
}

func TestAccepts(t *testing.T) {
	in := New(nil)
	for _, tc := range []struct {
		raw  port.RawInput
		want bool
	}{
		{port.RawInput{Kind: "file", File: "/abs/a.txt"}, true},
		{port.RawInput{Kind: "file", File: "  "}, false},
		{port.RawInput{Kind: "text", Text: "hi"}, false},
		{port.RawInput{Kind: "clipboard"}, false},
	} {
		if got := in.Accepts(tc.raw); got != tc.want {
			t.Fatalf("Accepts(%+v) = %v, want %v", tc.raw, got, tc.want)
		}
	}
}

// TestIngestTextFile 文本文件：PartDoc 带完整提取文本，附件已入库且内容一致。
func TestIngestTextFile(t *testing.T) {
	in, blobs, dir := newIngestor(t)
	p := writeFile(t, dir, "note.txt", []byte("会议纪要：M3 范围已定。"))

	rep, err := in.Ingest(context.Background(), port.RawInput{Kind: "file", File: p})
	if err != nil {
		t.Fatalf("ingest: %v", err)
	}
	if len(rep.Parts) != 1 || rep.Parts[0].Kind != conversation.PartDoc {
		t.Fatalf("parts = %+v", rep.Parts)
	}
	if rep.Parts[0].Text != "会议纪要：M3 范围已定。" {
		t.Fatalf("text = %q", rep.Parts[0].Text)
	}
	if rep.Parts[0].Ref == nil {
		t.Fatal("doc 应带附件引用")
	}
	if !strings.Contains(rep.Note, "note.txt") {
		t.Fatalf("note = %q", rep.Note)
	}
	if got := refContent(t, blobs, *rep.Parts[0].Ref); string(got) != "会议纪要：M3 范围已定。" {
		t.Fatalf("附件内容 = %q", got)
	}
}

// TestIngestImageFile 图片：PartImage + image/png 引用。
func TestIngestImageFile(t *testing.T) {
	in, _, dir := newIngestor(t)
	png := append([]byte("\x89PNG\r\n\x1a\n"), bytes.Repeat([]byte{0x42}, 64)...)
	p := writeFile(t, dir, "photo.png", png)

	rep, err := in.Ingest(context.Background(), port.RawInput{Kind: "file", File: p})
	if err != nil {
		t.Fatalf("ingest: %v", err)
	}
	if len(rep.Parts) != 1 || rep.Parts[0].Kind != conversation.PartImage {
		t.Fatalf("parts = %+v", rep.Parts)
	}
	if rep.Parts[0].Ref == nil || rep.Parts[0].Ref.MIME != "image/png" {
		t.Fatalf("ref = %+v, want image/png", rep.Parts[0].Ref)
	}
}

// TestIngestLargeTextTruncates 超长文本截断并标注原文路径（D10）。
func TestIngestLargeTextTruncates(t *testing.T) {
	in, _, dir := newIngestor(t)
	p := writeFile(t, dir, "big.log", bytes.Repeat([]byte("abcdefghij"), 4000)) // 40KB

	rep, err := in.Ingest(context.Background(), port.RawInput{Kind: "file", File: p})
	if err != nil {
		t.Fatalf("ingest: %v", err)
	}
	text := rep.Parts[0].Text
	if !strings.HasPrefix(text, "abcdefghij") {
		t.Fatalf("text 前缀 = %.40q", text)
	}
	if !strings.Contains(text, "已截断") || !strings.Contains(text, p) {
		t.Fatalf("缺截断标注/原文路径: %.120q", text)
	}
	if n := len([]rune(text)); n > maxExtractText+120 {
		t.Fatalf("text 长度 = %d, 超上限", n)
	}
}

// TestIngestBinaryDoc 二进制（含 NUL）：PartDoc 只留引用与 file_read 取用说明。
func TestIngestBinaryDoc(t *testing.T) {
	in, _, dir := newIngestor(t)
	p := writeFile(t, dir, "data.bin", []byte{0x50, 0x4b, 0x00, 0x01, 0x00, 0x00})

	rep, err := in.Ingest(context.Background(), port.RawInput{Kind: "file", File: p})
	if err != nil {
		t.Fatalf("ingest: %v", err)
	}
	part := rep.Parts[0]
	if part.Kind != conversation.PartDoc || part.Ref == nil {
		t.Fatalf("part = %+v", part)
	}
	if !strings.Contains(part.Text, "file_read") || !strings.Contains(part.Text, p) {
		t.Fatalf("取用说明 = %q", part.Text)
	}
}

// TestIngestRejects 非法输入：相对路径/缺失文件/未配置附件库。
func TestIngestRejects(t *testing.T) {
	in, _, dir := newIngestor(t)
	if _, err := in.Ingest(context.Background(), port.RawInput{Kind: "file", File: "rel/a.txt"}); err == nil ||
		!strings.Contains(err.Error(), "绝对路径") {
		t.Fatalf("相对路径 err = %v", err)
	}
	if _, err := in.Ingest(context.Background(), port.RawInput{
		Kind: "file", File: filepath.Join(dir, "missing.txt"),
	}); err == nil {
		t.Fatal("缺失文件应报错")
	}
	bare := &Ingestor{}
	if _, err := bare.Ingest(context.Background(), port.RawInput{Kind: "file", File: filepath.Join(dir, "x")}); err == nil ||
		!strings.Contains(err.Error(), "附件库") {
		t.Fatalf("nil blobs err = %v", err)
	}
}
