package app

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Tonyjh07/Aquarius/internal/adapter/blobfs"
	"github.com/Tonyjh07/Aquarius/internal/adapter/ingestfile"
	"github.com/Tonyjh07/Aquarius/internal/domain/conversation"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

// newRawSession 组装带附件库与文件摄取器的会话（真实 blobfs/ingestfile + 脚本 LLM）。
func newRawSession(t *testing.T, streams ...*scriptStream) (*Session, *scriptLLM, *recorder, *blobfs.Store) {
	t.Helper()
	dir := t.TempDir()
	blobs, err := blobfs.New(filepath.Join(dir, "attachments"))
	if err != nil {
		t.Fatalf("blobfs: %v", err)
	}
	rec := &recorder{}
	llm := &scriptLLM{t: t, streams: streams}
	agent := newAgent(t, llm, rec, Deps{Blobs: blobs}, Config{})
	s, err := NewSession(context.Background(), SessionDeps{
		Store:     newMemStore(),
		Agent:     agent,
		IDs:       &seqIDs{},
		Clock:     fixedClock{testTime},
		Ingestors: []port.Ingestor{ingestfile.New(blobs)},
		UI:        rec,
	})
	if err != nil {
		t.Fatalf("new session: %v", err)
	}
	return s, llm, rec, blobs
}

// findUserParts 返回首个带指定种类分片的 user 消息内容。
func findUserParts(req port.GenerateRequest, kind conversation.PartKind) []port.PromptPart {
	for _, m := range req.Messages {
		if m.Role != "user" {
			continue
		}
		for _, p := range m.Content {
			if (kind == conversation.PartImage && p.Kind == "image") ||
				(kind != conversation.PartImage && p.Kind == "text" && strings.TrimSpace(p.Text) != "") {
				return m.Content
			}
		}
	}
	return nil
}

// TestSessionRawFilePipeline M3 验收链路：文件输入 → blobfs 入库 → Part 入树 →
// 装配把图片字节 / 文档提取文本内联进请求；Note 走 NoticeEvent；标题取附件名。
func TestSessionRawFilePipeline(t *testing.T) {
	png := append([]byte("\x89PNG\r\n\x1a\n"), bytes.Repeat([]byte{0x11}, 32)...)
	doc := []byte("季度目标：M3 交付任务与多模态。")

	s, llm, rec, blobs := newRawSession(t,
		textStream("收到图片"),
		textStream("读完了"),
	)
	dir := t.TempDir()
	imgPath := filepath.Join(dir, "photo.png")
	docPath := filepath.Join(dir, "goal.txt")
	if err := os.WriteFile(imgPath, png, 0o644); err != nil {
		t.Fatalf("write png: %v", err)
	}
	if err := os.WriteFile(docPath, doc, 0o644); err != nil {
		t.Fatalf("write doc: %v", err)
	}

	// 第一轮：图片输入 → 请求内联图片字节。
	if _, err := s.Handle(context.Background(), port.UserInput{
		Raw: &port.RawInput{Kind: "file", File: imgPath},
	}); err != nil {
		t.Fatalf("handle image: %v", err)
	}
	parts := findUserParts(llm.requests[0], conversation.PartImage)
	if parts == nil {
		t.Fatalf("第一次请求缺图片分片: %+v", llm.requests[0].Messages)
	}
	found := false
	for _, p := range parts {
		if p.Kind == "image" && len(p.Data) > 0 && p.MIME == "image/png" {
			found = true
		}
	}
	if !found {
		t.Fatalf("图片字节未内联: %+v", parts)
	}
	if s.Current().Title != "photo.png" {
		t.Fatalf("title = %q, want 附件名", s.Current().Title)
	}
	// 摄取 Note 呈现为 NoticeEvent。
	if !strings.Contains(strings.Join(eventNames(rec.events), ","), "notice") {
		t.Fatalf("events = %v, want notice", eventNames(rec.events))
	}

	// 第二轮：文本文件 → 请求带提取文本；附件已入树且入库。
	if _, err := s.Handle(context.Background(), port.UserInput{
		Raw: &port.RawInput{Kind: "file", File: docPath},
	}); err != nil {
		t.Fatalf("handle doc: %v", err)
	}
	docParts := findUserParts(llm.requests[1], conversation.PartText)
	if docParts == nil {
		t.Fatalf("第二次请求缺文档文本: %+v", llm.requests[1].Messages)
	}
	hit := false
	for _, p := range docParts {
		if strings.Contains(p.Text, "季度目标") {
			hit = true
		}
	}
	if !hit {
		t.Fatalf("文档提取文本未内联: %+v", docParts)
	}
	// 树：两个 user 节点，图片引用可经附件库解析（装配内联依赖）。
	var imgRef *conversation.BlobRef
	for _, m := range s.Current().Nodes {
		if m.Role != conversation.RoleUser {
			continue
		}
		for _, p := range m.Content {
			if p.Kind == conversation.PartImage && p.Ref != nil {
				imgRef = p.Ref
			}
		}
	}
	if imgRef == nil {
		t.Fatal("树中缺图片引用")
	}
	rc, err := blobs.Get(context.Background(), *imgRef)
	if err != nil {
		t.Fatalf("附件库读取: %v", err)
	}
	_ = rc.Close()
	if err := s.Current().Validate(); err != nil {
		t.Fatalf("validate: %v", err)
	}
	// 已落盘（memStore.Save 被 runTurn 调用）。
	if got := s.Current(); got.ID == "" {
		t.Fatal("会话未保存")
	}
}

// TestSessionRawDispatchErrors 分派失败路径：无摄取器/未知 Kind/空文本均明确报因。
func TestSessionRawDispatchErrors(t *testing.T) {
	// 无摄取器。
	s, _, _ := newTestSession(t, newMemStore())
	if _, err := s.Handle(context.Background(), port.UserInput{
		Raw: &port.RawInput{Kind: "file", File: "/abs/x"},
	}); err == nil || !strings.Contains(err.Error(), "没有可用的摄取器") {
		t.Fatalf("err = %v", err)
	}
	// 未知 Kind（mic 未装，D27）。
	if _, err := s.Handle(context.Background(), port.UserInput{
		Raw: &port.RawInput{Kind: "mic"},
	}); err == nil || !strings.Contains(err.Error(), "摄取器") {
		t.Fatalf("err = %v", err)
	}
	// Kind=text 空文本。
	if _, err := s.Handle(context.Background(), port.UserInput{
		Raw: &port.RawInput{Kind: "text", Text: "   "},
	}); err == nil || !strings.Contains(err.Error(), "文本输入为空") {
		t.Fatalf("err = %v", err)
	}
}

// audioIngestor 产出"无文本分片、无附件引用"的摄取器（标题守卫复现用）。
type audioIngestor struct{}

func (audioIngestor) Accepts(port.RawInput) bool { return true }

func (audioIngestor) Ingest(context.Context, port.RawInput) (port.IngestReport, error) {
	return port.IngestReport{
		Parts: []conversation.Part{{Kind: conversation.PartAudio, Transcript: "语音转写"}},
	}, nil
}

// TestSessionRawKeepsDefaultTitle 摄取结果无文本分片/附件名时保留默认标题，
// 不得被空串抹成 ""（ingestTitle 返回空 → maybeSetTitle 守卫）。
func TestSessionRawKeepsDefaultTitle(t *testing.T) {
	store := newMemStore()
	s, llm, _ := newTestSession(t, store, textStream("好"))
	s.ingestors = []port.Ingestor{audioIngestor{}}

	if _, err := s.Handle(context.Background(), port.UserInput{
		Raw: &port.RawInput{Kind: "mic"},
	}); err != nil {
		t.Fatalf("handle: %v", err)
	}
	if s.Current().Title != defaultTitle {
		t.Fatalf("title = %q, want 保留默认标题 %q", s.Current().Title, defaultTitle)
	}
	// 转写文本按 audio 分片语义入树（模型只见 Transcript）。
	if len(llm.requests) != 1 {
		t.Fatalf("requests = %d, want 1", len(llm.requests))
	}
	var hasAudio bool
	for _, m := range s.Current().Nodes {
		for _, p := range m.Content {
			if p.Kind == conversation.PartAudio && p.Transcript == "语音转写" {
				hasAudio = true
			}
		}
	}
	if !hasAudio {
		t.Fatal("树中缺 audio 分片")
	}
}

// TestSessionRawTextDirect Kind=text 直通入树并触发 Turn（无需注册摄取器）。
func TestSessionRawTextDirect(t *testing.T) {
	store := newMemStore()
	s, llm, _ := newTestSession(t, store, textStream("好"))
	if _, err := s.Handle(context.Background(), port.UserInput{
		Raw: &port.RawInput{Kind: "text", Text: " 直达文本 "},
	}); err != nil {
		t.Fatalf("handle: %v", err)
	}
	if len(llm.requests) != 1 {
		t.Fatalf("requests = %d, want 1", len(llm.requests))
	}
	var text string
	for _, m := range s.Current().Nodes {
		if m.Role == conversation.RoleUser {
			for _, p := range m.Content {
				text += p.Text
			}
		}
	}
	if text != "直达文本" {
		t.Fatalf("user 文本 = %q", text)
	}
}
