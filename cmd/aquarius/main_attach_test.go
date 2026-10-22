package main

// main_attach_test.go 启动附件 GC：按全量会话引用保活、清扫孤儿；引用收集
// 失败时跳过清扫并告警。

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Tonyjh07/Aquarius/internal/adapter/blobfs"
	"github.com/Tonyjh07/Aquarius/internal/adapter/storejson"
	"github.com/Tonyjh07/Aquarius/internal/domain/conversation"
	"github.com/Tonyjh07/Aquarius/internal/domain/tool"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

// TestRunAttachmentGC 启动附件 GC：按全量会话引用保活、清扫孤儿；
// 引用收集失败（store 报错）时跳过清扫并告警——宁可漏清不误删。
func TestRunAttachmentGC(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	store, err := storejson.New(filepath.Join(dir, "profiles", "default", "conversations"))
	if err != nil {
		t.Fatalf("storejson: %v", err)
	}
	blobs, err := blobfs.New(filepath.Join(dir, "profiles", "default", "attachments"))
	if err != nil {
		t.Fatalf("blobfs: %v", err)
	}
	// 会话带一个图片引用。
	c := conversation.New("conv1", "t")
	ref, err := blobs.Put(ctx, bytes.NewReader([]byte("live")), "image/png", "live.png")
	if err != nil {
		t.Fatalf("put live: %v", err)
	}
	if err := c.AppendCommitted(conversation.Message{
		ID: "m1", Parent: conversation.MessageID("conv1"), Role: conversation.RoleSystem,
		Content:   []conversation.Part{{Kind: conversation.PartImage, Ref: &ref}},
		CreatedAt: time.Now(),
	}); err != nil {
		t.Fatalf("append: %v", err)
	}
	if err := store.Save(ctx, c); err != nil {
		t.Fatalf("save: %v", err)
	}
	orphan, err := blobs.Put(ctx, bytes.NewReader([]byte("dead")), "image/png", "dead.png")
	if err != nil {
		t.Fatalf("put orphan: %v", err)
	}

	// 工具产出的图片（D169）：引用挂在工具结果里，与 Part.Ref 同属活引用。
	// 漏扫这里 → 下次启动把刚读过的图片删掉，而会话树里的引用还在。
	toolImg, err := blobs.Put(ctx, bytes.NewReader([]byte("shot")), "image/png", "shot.png")
	if err != nil {
		t.Fatalf("put tool image: %v", err)
	}
	if err := c.AppendCommitted(conversation.Message{
		ID: "m2", Parent: "m1", Role: conversation.RoleAssistant,
		Content: []conversation.Part{{Kind: conversation.PartTool, Tool: &conversation.ToolPart{
			CallID: "c1", Name: "file_read",
			Result: &tool.Result{CallID: "c1", OK: true, Output: "attached shot.png",
				Images: []tool.ImageRef{{Hash: toolImg.Hash, MIME: toolImg.MIME, Name: toolImg.Name, Size: toolImg.Size}}},
		}}},
		CreatedAt: time.Now(),
	}); err != nil {
		t.Fatalf("append tool: %v", err)
	}
	if err := store.Save(ctx, c); err != nil {
		t.Fatalf("save: %v", err)
	}

	keep, err := collectBlobRefs(ctx, store)
	if err != nil || !keep[ref.Hash] {
		t.Fatalf("keep = %v, err = %v", keep, err)
	}
	if !keep[toolImg.Hash] {
		t.Fatal("工具结果里的图片引用应进 keep")
	}
	if keep[orphan.Hash] {
		t.Fatal("孤儿不应进 keep")
	}
	var warn bytes.Buffer
	runAttachmentGC(ctx, store, blobs, &warn)
	if ok, _ := blobs.Stat(ctx, ref); !ok {
		t.Fatal("被引用附件不应被清")
	}
	if ok, _ := blobs.Stat(ctx, toolImg); !ok {
		t.Fatal("工具结果引用的图片不应被清")
	}
	if ok, _ := blobs.Stat(ctx, orphan); ok {
		t.Fatal("孤儿附件应被清")
	}

	// 收集失败 → 跳过清扫并告警。
	orphan2, err := blobs.Put(ctx, bytes.NewReader([]byte("dead2")), "image/png", "dead2.png")
	if err != nil {
		t.Fatalf("put orphan2: %v", err)
	}
	warn.Reset()
	runAttachmentGC(ctx, failingStore{}, blobs, &warn)
	if !strings.Contains(warn.String(), "已跳过") {
		t.Fatalf("warn = %q, want 已跳过", warn.String())
	}
	if ok, _ := blobs.Stat(ctx, orphan2); !ok {
		t.Fatal("收集失败时不应清扫")
	}
}

// TestRunAttachmentGCQuietOnCancel 启动期 ctx 取消：跳过清扫且不告警（干净收尾，
// ctx 提前到装配段创建后 Ctrl+C 不产生误导性错误输出）。
func TestRunAttachmentGCQuietOnCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	blobs, err := blobfs.New(t.TempDir())
	if err != nil {
		t.Fatalf("blobfs: %v", err)
	}
	var warn bytes.Buffer
	runAttachmentGC(ctx, failingStore{}, blobs, &warn)
	if warn.Len() != 0 {
		t.Fatalf("取消不应告警: %q", warn.String())
	}
}

// failingStore 只让 List 报错的 ConversationStore 替身。
type failingStore struct{ port.ConversationStore }

func (failingStore) List(context.Context) ([]port.ConversationSummary, error) {
	return nil, errors.New("boom")
}
