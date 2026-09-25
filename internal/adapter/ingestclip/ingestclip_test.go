package ingestclip

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Tonyjh07/Aquarius/internal/adapter/blobfs"
	"github.com/Tonyjh07/Aquarius/internal/domain/conversation"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

func TestAccepts(t *testing.T) {
	in := New(nil)
	for _, tc := range []struct {
		raw  port.RawInput
		want bool
	}{
		{port.RawInput{Kind: "clipboard", Blob: &conversation.BlobRef{Hash: strings.Repeat("a", 64)}}, true},
		{port.RawInput{Kind: "clipboard", Text: "  hi "}, true},
		{port.RawInput{Kind: "clipboard"}, false},
		{port.RawInput{Kind: "file", File: "/a"}, false},
	} {
		if got := in.Accepts(tc.raw); got != tc.want {
			t.Fatalf("Accepts(%+v) = %v, want %v", tc.raw, got, tc.want)
		}
	}
}

// TestIngestClipboardImage 已入库图片 → PartImage，附件校验通过。
func TestIngestClipboardImage(t *testing.T) {
	blobs, err := blobfs.New(filepath.Join(t.TempDir(), "attachments"))
	if err != nil {
		t.Fatalf("blobfs: %v", err)
	}
	ref, err := blobs.Put(context.Background(), bytes.NewReader([]byte("PNG")), "image/png", "clip.png")
	if err != nil {
		t.Fatalf("put: %v", err)
	}
	in := New(blobs)

	rep, err := in.Ingest(context.Background(), port.RawInput{Kind: "clipboard", Blob: &ref})
	if err != nil {
		t.Fatalf("ingest: %v", err)
	}
	if len(rep.Parts) != 1 || rep.Parts[0].Kind != conversation.PartImage ||
		rep.Parts[0].Ref == nil || rep.Parts[0].Ref.Hash != ref.Hash {
		t.Fatalf("parts = %+v", rep.Parts)
	}
	if !strings.Contains(rep.Note, "clip.png") {
		t.Fatalf("note = %q", rep.Note)
	}
}

// TestIngestClipboardText 纯文本 → PartText 内联。
func TestIngestClipboardText(t *testing.T) {
	in := New(nil)
	rep, err := in.Ingest(context.Background(), port.RawInput{Kind: "clipboard", Text: "  粘贴的文本  "})
	if err != nil {
		t.Fatalf("ingest: %v", err)
	}
	if len(rep.Parts) != 1 || rep.Parts[0].Kind != conversation.PartText ||
		rep.Parts[0].Text != "粘贴的文本" {
		t.Fatalf("parts = %+v", rep.Parts)
	}
}

// TestIngestClipboardMissingBlob 未入库附件报错（不静默吞）。
func TestIngestClipboardMissingBlob(t *testing.T) {
	blobs, err := blobfs.New(filepath.Join(t.TempDir(), "attachments"))
	if err != nil {
		t.Fatalf("blobfs: %v", err)
	}
	in := New(blobs)
	missing := conversation.BlobRef{Hash: strings.Repeat("b", 64), MIME: "image/png", Name: "x.png"}
	if _, err := in.Ingest(context.Background(), port.RawInput{Kind: "clipboard", Blob: &missing}); err == nil ||
		!strings.Contains(err.Error(), "附件不存在") {
		t.Fatalf("err = %v", err)
	}
	// nil blobs 且带 Blob 同样报错。
	bare := New(nil)
	if _, err := bare.Ingest(context.Background(), port.RawInput{Kind: "clipboard", Blob: &missing}); err == nil ||
		!strings.Contains(err.Error(), "附件库") {
		t.Fatalf("err = %v", err)
	}
}

// TestIngestClipboardNonImage 非图片附件按文档处理并给取用说明。
func TestIngestClipboardNonImage(t *testing.T) {
	blobs, err := blobfs.New(filepath.Join(t.TempDir(), "attachments"))
	if err != nil {
		t.Fatalf("blobfs: %v", err)
	}
	ref, err := blobs.Put(context.Background(), bytes.NewReader([]byte("PK")), "application/zip", "a.zip")
	if err != nil {
		t.Fatalf("put: %v", err)
	}
	in := New(blobs)
	rep, err := in.Ingest(context.Background(), port.RawInput{Kind: "clipboard", Blob: &ref})
	if err != nil {
		t.Fatalf("ingest: %v", err)
	}
	if rep.Parts[0].Kind != conversation.PartDoc || rep.Parts[0].Ref == nil {
		t.Fatalf("parts = %+v", rep.Parts)
	}
}
