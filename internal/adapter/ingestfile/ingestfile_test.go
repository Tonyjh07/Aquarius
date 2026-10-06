package ingestfile

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gomutex/godocx"
	"github.com/xuri/excelize/v2"

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

// TestIngestWithCaption 随附文本并入（D104② 暂存随文发）：raw.Text 非空 → 首分片
// PartText、其后附件分片；空文本不并（原样）。
func TestIngestWithCaption(t *testing.T) {
	in, blobs, dir := newIngestor(t)
	p := writeFile(t, dir, "a.txt", []byte("正文内容"))

	rep, err := in.Ingest(context.Background(), port.RawInput{Kind: "file", File: p, Text: "总结一下这个文件"})
	if err != nil {
		t.Fatalf("ingest: %v", err)
	}
	if len(rep.Parts) != 2 {
		t.Fatalf("parts = %d, want 2（文本+文档）", len(rep.Parts))
	}
	if rep.Parts[0].Kind != conversation.PartText || rep.Parts[0].Text != "总结一下这个文件" {
		t.Fatalf("首分片 = %+v, want PartText(随附文本)", rep.Parts[0])
	}
	if rep.Parts[1].Kind != conversation.PartDoc || rep.Parts[1].Ref == nil {
		t.Fatalf("次分片 = %+v, want PartDoc", rep.Parts[1])
	}
	if got := refContent(t, blobs, *rep.Parts[1].Ref); string(got) != "正文内容" {
		t.Fatalf("附件内容 = %q", got)
	}

	// 空文本：不并（单分片，原行为）。
	rep, err = in.Ingest(context.Background(), port.RawInput{Kind: "file", File: p})
	if err != nil {
		t.Fatalf("ingest: %v", err)
	}
	if len(rep.Parts) != 1 || rep.Parts[0].Kind != conversation.PartDoc {
		t.Fatalf("空文本 parts = %+v, want 单 PartDoc", rep.Parts)
	}
}

// ---- D105 常见文档转文本（样本测试内自造，零 fixture 入库）----

// minimalPDF 生成最小合法 PDF（单页 Helvetica 文本，xref 偏移程序内计算）。
func minimalPDF() []byte {
	const stream = "BT /F1 24 Tf 72 720 Td (Hello Aquarius PDF) Tj ET"
	bodies := []string{
		"<< /Type /Catalog /Pages 2 0 R >>",
		"<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
		"<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Resources << /Font << /F1 5 0 R >> >> /Contents 4 0 R >>",
		fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", len(stream), stream),
		"<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>",
	}
	var buf bytes.Buffer
	buf.WriteString("%PDF-1.4\n")
	offsets := make([]int, 0, len(bodies))
	for i, o := range bodies {
		offsets = append(offsets, buf.Len())
		fmt.Fprintf(&buf, "%d 0 obj\n%s\nendobj\n", i+1, o)
	}
	xref := buf.Len()
	fmt.Fprintf(&buf, "xref\n0 %d\n0000000000 65535 f \n", len(bodies)+1)
	for _, off := range offsets {
		fmt.Fprintf(&buf, "%010d 00000 n \n", off)
	}
	fmt.Fprintf(&buf, "trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(bodies)+1, xref)
	return buf.Bytes()
}

// partText 组装后返回 PartDoc 的提取文本。
func partText(t *testing.T, rep port.IngestReport) string {
	t.Helper()
	if len(rep.Parts) == 0 || rep.Parts[0].Kind != conversation.PartDoc {
		t.Fatalf("parts = %+v, want PartDoc", rep.Parts)
	}
	return rep.Parts[0].Text
}

func TestIngestPDFExtract(t *testing.T) {
	in, _, dir := newIngestor(t)
	p := writeFile(t, dir, "a.pdf", minimalPDF())
	rep, err := in.Ingest(context.Background(), port.RawInput{Kind: "file", File: p})
	if err != nil {
		t.Fatalf("ingest: %v", err)
	}
	if txt := partText(t, rep); !strings.Contains(txt, "Hello Aquarius PDF") {
		t.Fatalf("pdf 提取文本 = %q", txt)
	}
}

func TestIngestDOCXExtract(t *testing.T) {
	in, _, _ := newIngestor(t)
	p := buildDOCX(t, "Hello Aquarius DOCX", "第二段落")
	rep, err := in.Ingest(context.Background(), port.RawInput{Kind: "file", File: p})
	if err != nil {
		t.Fatalf("ingest: %v", err)
	}
	txt := partText(t, rep)
	if !strings.Contains(txt, "Hello Aquarius DOCX") || !strings.Contains(txt, "第二段落") {
		t.Fatalf("docx 提取文本 = %q", txt)
	}
}

// buildDOCX 用 godocx 生成样本 docx（round-trip：库写 → 库读，D105）。
func buildDOCX(t *testing.T, paras ...string) string {
	t.Helper()
	root, err := godocx.NewDocument()
	if err != nil {
		t.Fatalf("godocx new: %v", err)
	}
	for _, para := range paras {
		root.AddParagraph(para)
	}
	p := filepath.Join(t.TempDir(), "sample.docx")
	if err := root.SaveTo(p); err != nil {
		t.Fatalf("godocx save: %v", err)
	}
	return p
}

// TestNormalizePDFBreaks PDF 版式碎片归一（D105 后果③）：段内单换行折空格、空行保留。
func TestNormalizePDFBreaks(t *testing.T) {
	in := "第\n12\n章\n\n宏观热力学\n\n\n平衡态"
	want := "第 12 章\n\n宏观热力学\n\n平衡态"
	if got := normalizePDFBreaks(in); got != want {
		t.Fatalf("= %q, want %q", got, want)
	}
}

func TestIngestXLSXExtract(t *testing.T) {
	in, _, dir := newIngestor(t)
	f := excelize.NewFile()
	defer f.Close()
	f.SetCellValue("Sheet1", "A1", "Hello Aquarius XLSX")
	f.SetCellValue("Sheet1", "A2", 42)
	if _, err := f.NewSheet("数据"); err != nil {
		t.Fatalf("new sheet: %v", err)
	}
	f.SetCellValue("数据", "B1", "中文表")
	var buf bytes.Buffer
	if _, err := f.WriteTo(&buf); err != nil {
		t.Fatalf("write xlsx: %v", err)
	}
	p := writeFile(t, dir, "a.xlsx", buf.Bytes())

	rep, err := in.Ingest(context.Background(), port.RawInput{Kind: "file", File: p})
	if err != nil {
		t.Fatalf("ingest: %v", err)
	}
	txt := partText(t, rep)
	for _, want := range []string{"〔工作表 Sheet1〕", "Hello Aquarius XLSX", "42", "〔工作表 数据〕", "中文表"} {
		if !strings.Contains(txt, want) {
			t.Fatalf("xlsx 提取文本缺 %q:\n%s", want, txt)
		}
	}
}

func TestIngestHTMLExtract(t *testing.T) {
	in, _, dir := newIngestor(t)
	p := writeFile(t, dir, "a.html", []byte(
		`<!doctype html><html><head><title>测试页</title><style>.x{color:red}</style></head>`+
			`<body><h1>Hello Aquarius HTML</h1><script>alert(1)</script><p>正文段落。</p></body></html>`))
	rep, err := in.Ingest(context.Background(), port.RawInput{Kind: "file", File: p})
	if err != nil {
		t.Fatalf("ingest: %v", err)
	}
	txt := partText(t, rep)
	if !strings.Contains(txt, "Hello Aquarius HTML") || !strings.Contains(txt, "正文段落。") {
		t.Fatalf("html 提取文本 = %q", txt)
	}
	if strings.Contains(txt, "alert") || strings.Contains(txt, "color:red") {
		t.Fatalf("script/style 未剥离: %q", txt)
	}
}

func TestIngestPDFCorruptFallback(t *testing.T) {
	in, _, dir := newIngestor(t)
	p := writeFile(t, dir, "broken.pdf", []byte("%PDF-1.4 垃圾数据，没有对象"))
	rep, err := in.Ingest(context.Background(), port.RawInput{Kind: "file", File: p})
	if err != nil {
		t.Fatalf("解析失败不应整体报错（D105④ 回落占位）: %v", err)
	}
	txt := partText(t, rep)
	if !strings.Contains(txt, "解析失败") || !strings.Contains(txt, "file_read") {
		t.Fatalf("回落占位文案 = %q", txt)
	}
}

// TestIngestDeferredFormats pptx/rtf 首批缓（D105）：不解析、回落二进制占位（rtf 不把
// 控制词当正文提取）。
func TestIngestDeferredFormats(t *testing.T) {
	in, _, dir := newIngestor(t)
	for _, tc := range []struct{ name, content string }{
		{"a.pptx", "PK\x03\x04 幻灯片容器"},
		{"a.rtf", `{\rtf1\ansi Hello {\b RTF}}`},
	} {
		p := writeFile(t, dir, tc.name, []byte(tc.content))
		rep, err := in.Ingest(context.Background(), port.RawInput{Kind: "file", File: p})
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		txt := partText(t, rep)
		if !strings.Contains(txt, "二进制内容未提取") {
			t.Fatalf("%s 应回落二进制占位，got %q", tc.name, txt)
		}
	}
}

// TestIngestDocTruncation 提取文本截断（D105③）：超 maxDocText 加截断标注。
func TestIngestDocTruncation(t *testing.T) {
	old := maxDocText
	maxDocText = 200
	t.Cleanup(func() { maxDocText = old })

	in, _, dir := newIngestor(t)
	p := writeFile(t, dir, "big.html", []byte("<html><body><p>"+strings.Repeat("长文本内容。", 100)+"</p></body></html>"))
	rep, err := in.Ingest(context.Background(), port.RawInput{Kind: "file", File: p})
	if err != nil {
		t.Fatalf("ingest: %v", err)
	}
	txt := partText(t, rep)
	if !strings.Contains(txt, "提取文本已截断") {
		t.Fatalf("截断标注缺失: %q", txt)
	}
}

// TestIngestDocOversize 文件超 maxIngestFile（D105③）：不解析、回落占位。
func TestIngestDocOversize(t *testing.T) {
	old := maxIngestFile
	maxIngestFile = 16
	t.Cleanup(func() { maxIngestFile = old })

	in, _, dir := newIngestor(t)
	p := writeFile(t, dir, "big.pdf", minimalPDF())
	rep, err := in.Ingest(context.Background(), port.RawInput{Kind: "file", File: p})
	if err != nil {
		t.Fatalf("ingest: %v", err)
	}
	if txt := partText(t, rep); !strings.Contains(txt, "解析失败") {
		t.Fatalf("超限应回落占位: %q", txt)
	}
}
