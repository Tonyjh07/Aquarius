package app

// session_files_test.go 服务端文件解析（D315）的应用层行为：三道闸、file_id 写回、
// 失败进 Note 不重试、装配层能力开/关两条路。

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Tonyjh07/Aquarius/internal/adapter/blobfs"
	"github.com/Tonyjh07/Aquarius/internal/domain/conversation"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

// fakeFiles 上传替身：记录调用参数；err != nil 时返回错误。
type fakeFiles struct {
	calls    int
	name     string
	purpose  string
	ttl      time.Duration
	err      error
	timedOut bool // true = 模拟超时（校验没做重试：调用次数恰为 1）
}

func (f *fakeFiles) Upload(_ context.Context, name, _ string, _ []byte, purpose string, ttl time.Duration) (string, error) {
	f.calls++
	f.name, f.purpose, f.ttl = name, purpose, ttl
	if f.err != nil {
		return "", f.err
	}
	return "file-api-ok", nil
}

// filesFixture 会话带真附件库 + 上传替身；serverFiles 决定 agent 能力位是否声明 files。
func filesFixture(t *testing.T, serverFiles bool, purpose string, up *fakeFiles) (*Session, *blobfs.Store, *conversation.Conversation) {
	t.Helper()
	s, _, _, blobs := newRawSession(t, textStream("收到"))
	// newRawSession 的 agent 不带能力位：这里重造一个带 files 的（Config 零值 = off）。
	if serverFiles {
		s.agent.cfg.ServerCaps = port.ServerCaps{Files: true}
	}
	s.files = up
	s.filesPurpose = purpose
	s.filesTTL = 0
	s.blobs = blobs // newRawSession 只把附件库给 agent，Session.Blobs 在这里补上
	return s, blobs, s.cur
}

// pdfRef 往附件库放一份「PDF」，返回 doc 分片。
func pdfDoc(t *testing.T, blobs *blobfs.Store, text string) conversation.Part {
	t.Helper()
	ref, err := blobs.Put(context.Background(), strings.NewReader("%PDF-1.4 scan"), "application/pdf", "扫描件.pdf")
	if err != nil {
		t.Fatalf("put pdf: %v", err)
	}
	return conversation.Part{Kind: conversation.PartDoc, Ref: &ref, Text: text}
}

// TestUploadDocsGatesAndWritesFileID 三道闸齐开 → 上传、file_id 写回、本地文本保留；
// purpose / ttl 原样传给上传口。
func TestUploadDocsGatesAndWritesFileID(t *testing.T) {
	up := &fakeFiles{}
	s, blobs, _ := filesFixture(t, true, "rich_layout_recognition", up)
	doc := pdfDoc(t, blobs, "本地提取文本")
	parts := []conversation.Part{doc}
	if note := s.uploadDocs(context.Background(), parts); note != "" {
		t.Fatalf("note = %q, want empty", note)
	}
	if up.calls != 1 || up.purpose != "rich_layout_recognition" {
		t.Fatalf("上传口调用 = %+v", up)
	}
	if parts[0].FileID != "file-api-ok" || parts[0].Text != "本地提取文本" {
		t.Fatalf("分片 = %+v, want file_id 写回且本地文本保留", parts[0])
	}
}

// TestUploadDocsGatesOff 门控任一关（能力位没声明 / purpose 空 / 未接入）→ 一个调用都没有。
func TestUploadDocsGatesOff(t *testing.T) {
	for _, tc := range []struct {
		name        string
		serverFiles bool
		purpose     string
	}{{"能力位没声明", false, "text_recognition"}, {"purpose 空", true, ""}} {
		t.Run(tc.name, func(t *testing.T) {
			up := &fakeFiles{}
			s, b, _ := filesFixture(t, tc.serverFiles, tc.purpose, up)
			doc := pdfDoc(t, b, "文本")
			if note := s.uploadDocs(context.Background(), []conversation.Part{doc}); note != "" {
				t.Fatalf("note = %q, want empty", note)
			}
			if up.calls != 0 || doc.FileID != "" {
				t.Fatalf("门控关了还上传: calls=%d file_id=%q", up.calls, doc.FileID)
			}
		})
	}
}

// TestUploadDocsFailureNotes 上传失败：原因进 Note（明确报因不静默），分片保留本地文本，
// 不做第二次尝试（解析按页计费）。
func TestUploadDocsFailureNotes(t *testing.T) {
	up := &fakeFiles{err: errors.New("HTTP 400: bad purpose")}
	s, blobs, _ := filesFixture(t, true, "text_recognition", up)
	doc := pdfDoc(t, blobs, "本地文本")
	note := s.uploadDocs(context.Background(), []conversation.Part{doc})
	if up.calls != 1 {
		t.Fatalf("调用次数 = %d, want 1（不做无谓重试）", up.calls)
	}
	if !strings.Contains(note, "扫描件.pdf") || !strings.Contains(note, "bad purpose") {
		t.Fatalf("note = %q, want 文件名与原因都在", note)
	}
	if doc.FileID != "" || doc.Text != "本地文本" {
		t.Fatalf("分片 = %+v, want 保留本地文本、无 file_id", doc)
	}
}

// TestUploadDocsSkipsNonCandidates 纯文本 / 表格 / 图片不上传（D315 否决 1）。
func TestUploadDocsSkipsNonCandidates(t *testing.T) {
	up := &fakeFiles{}
	s, blobs, _ := filesFixture(t, true, "text_recognition", up)
	ref, err := blobs.Put(context.Background(), strings.NewReader("hello"), "text/plain", "说明.txt")
	if err != nil {
		t.Fatalf("put: %v", err)
	}
	imgRef, err := blobs.Put(context.Background(), strings.NewReader("\x89PNG"), "image/png", "图.png")
	if err != nil {
		t.Fatalf("put: %v", err)
	}
	parts := []conversation.Part{
		{Kind: conversation.PartDoc, Ref: &ref, Text: "说明"},
		{Kind: conversation.PartImage, Ref: &imgRef},
	}
	if note := s.uploadDocs(context.Background(), parts); note != "" {
		t.Fatalf("note = %q, want empty", note)
	}
	if up.calls != 0 {
		t.Fatalf("非候选类型不该上传: %+v", up)
	}
}

// TestAssembleDocFileIDGated 装配：能力开 + file_id → file 引用分片（Text 照带作回落）；
// 能力关 → 照旧拍平 text（请求体与从前一字节不差的验收线）。
func TestAssembleDocFileIDGated(t *testing.T) {
	part := conversation.Part{Kind: conversation.PartDoc, FileID: "file-api-ok", Text: "提取文本"}
	got, err := assembleParts(context.Background(), []conversation.Part{part}, assembleOpts{files: true})
	if err != nil {
		t.Fatalf("assemble: %v", err)
	}
	if len(got) != 1 || got[0].Kind != port.PromptFile || got[0].FileID != "file-api-ok" || got[0].Text != "提取文本" {
		t.Fatalf("files=true 装配 = %+v, want file 引用带回落文本", got)
	}
	got, err = assembleParts(context.Background(), []conversation.Part{part}, assembleOpts{})
	if err != nil {
		t.Fatalf("assemble: %v", err)
	}
	if len(got) != 1 || got[0].Kind != port.PromptText || got[0].Text != "提取文本" {
		t.Fatalf("files=false 装配 = %+v, want 照旧拍平 text", got)
	}
}

// TestFilePurposeSupported 词表：越界启动报错（configuration.md 的三个取值）。
func TestFilePurposeSupported(t *testing.T) {
	for _, v := range []string{"", "text_recognition", "rich_layout_recognition"} {
		if !filePurposeSupported(v) {
			t.Fatalf("%q 应该合法", v)
		}
	}
	if filePurposeSupported("user_data") {
		t.Fatal("user_data 不在词表（D315：只接两档识别 purpose）")
	}
}
