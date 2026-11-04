package main

import (
	"context"
	"fmt"

	"github.com/Tonyjh07/Aquarius/internal/i18n"
	"github.com/Tonyjh07/Aquarius/internal/logging"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

// collectBlobRefs 扫描全部会话树收集附件引用（启动 GC 的 keep 集，§4.2）。
// 任一会话加载失败即返回错误——调用方据此跳过 GC（引用集不完整时清扫会误删活附件）。
func collectBlobRefs(ctx context.Context, store port.ConversationStore) (map[string]bool, error) {
	sums, err := store.List(ctx)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", i18n.T("cmd.asm.err.list_sessions"), err)
	}
	keep := make(map[string]bool)
	for _, sm := range sums {
		c, err := store.Load(ctx, sm.ID)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", i18n.Tf("cmd.asm.err.load_session", "id", sm.ID), err)
		}
		for _, m := range c.Snapshot().Nodes {
			for _, p := range m.Content {
				if p.Ref != nil {
					keep[p.Ref.Hash] = true
				}
				// 工具产出的图片（D169）：字节同样在附件库，引用挂在工具结果里。
				// 漏扫这里，下一次启动 GC 会把刚读过的图片删掉，而会话树里的
				// 引用还在——模型再被问起同一张图时会拿到「附件不存在」。
				if p.Tool != nil && p.Tool.Result != nil {
					for _, img := range p.Tool.Result.Images {
						keep[img.Hash] = true
					}
				}
			}
		}
	}
	return keep, nil
}

// runAttachmentGC 启动时附件 GC：收集或清扫失败只告警不阻断启动（§4.2：Prune
// 会话不立即删附件，孤儿由这里在下次启动时清）。
func (w *wiring) runAttachmentGC(ctx context.Context, store port.ConversationStore, blobs port.AttachmentStore) {
	keep, err := collectBlobRefs(ctx, store)
	if err != nil {
		if ctx.Err() == nil { // 启动期 Ctrl+C 属正常收尾，不告警
			w.note(logging.LevelWarn, logging.Attach, "log.attach.gc_skipped",
				"log.act.skip", "err", err)
		}
		return
	}
	if err := blobs.GC(ctx, keep); err != nil && ctx.Err() == nil {
		w.note(logging.LevelWarn, logging.Attach, "log.attach.gc_failed",
			"log.act.continue", "err", err)
	}
}
