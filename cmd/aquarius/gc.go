package main

import (
	"context"
	"fmt"
	"io"

	"github.com/Tonyjh07/Aquarius/internal/port"
)

// collectBlobRefs 扫描全部会话树收集附件引用（启动 GC 的 keep 集，DESIGN §4.2）。
// 任一会话加载失败即返回错误——调用方据此跳过 GC（引用集不完整时清扫会误删活附件）。
func collectBlobRefs(ctx context.Context, store port.ConversationStore) (map[string]bool, error) {
	sums, err := store.List(ctx)
	if err != nil {
		return nil, fmt.Errorf("列出会话: %w", err)
	}
	keep := make(map[string]bool)
	for _, sm := range sums {
		c, err := store.Load(ctx, sm.ID)
		if err != nil {
			return nil, fmt.Errorf("加载会话 %s: %w", sm.ID, err)
		}
		for _, m := range c.Nodes {
			for _, p := range m.Content {
				if p.Ref != nil {
					keep[p.Ref.Hash] = true
				}
			}
		}
	}
	return keep, nil
}

// runAttachmentGC 启动时附件 GC：收集或清扫失败只告警不阻断启动（§4.2：Prune
// 会话不立即删附件，孤儿由这里在下次启动时清）。
func runAttachmentGC(ctx context.Context, store port.ConversationStore, blobs port.AttachmentStore, warn io.Writer) {
	keep, err := collectBlobRefs(ctx, store)
	if err != nil {
		if ctx.Err() == nil { // 启动期 Ctrl+C 属正常收尾，不告警
			fmt.Fprintf(warn, "附件 GC 已跳过（引用收集失败）: %v\n", err)
		}
		return
	}
	if err := blobs.GC(ctx, keep); err != nil && ctx.Err() == nil {
		fmt.Fprintf(warn, "附件 GC: %v\n", err)
	}
}
