package main

import (
	"context"
	"fmt"
	"time"

	"github.com/Tonyjh07/Aquarius/internal/adapter/storejson"
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

// trashSweepAge 回收站条目的保留期（D311）：主文件改动时间超过 30 天的启动时清掉。
// 写死不配——做成配置项等于邀请用户把这道缓冲关掉（决策第 5 条）。
const trashSweepAge = 30 * 24 * time.Hour

// runTrashGC 启动时清扫会话回收站（D311）：超龄条目整份清掉，真清掉了几条就留一行
// 日志（验收口径）；失败只告警不阻断启动，与附件 GC 同一姿态。
func (w *wiring) runTrashGC(ctx context.Context, store *storejson.Store) {
	n, err := store.SweepTrash(ctx, w.trashDir, trashSweepAge)
	if err != nil {
		if ctx.Err() == nil { // 启动期 Ctrl+C 属正常收尾，不告警
			w.note(logging.LevelWarn, logging.Trash, "log.trash.sweep_failed",
				"log.act.continue", "err", err)
		}
		if n == 0 {
			return
		}
	}
	if n > 0 && ctx.Err() == nil {
		w.log.Info(logging.Trash, "log.trash.swept", "log.act.trash_kept_30d", "n", n)
	}
}
