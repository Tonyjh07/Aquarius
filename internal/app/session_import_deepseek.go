package app

// session_import_deepseek.go `/import` 的 deepseek 整库分支（D321）。
//
// 与四档互通格式的差别全在量级与重复性：2274 个会话一次进内存不可行、官方只能全量导出
// 所以重导是常态。因此这一档：清单先行（不带 --all/--pick 只列）、100 条一块导入落盘
// （块内失败只回滚本块、Ctrl-C 留下的是已完成的整块）、sidecar 记「源会话 → 已导入的
// 会话」，同 id 且 updated_at 未变跳过。人格边界写在格式表的 LossNote 里（回执的保真行
// 会带出来），这里不重复。

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/Tonyjh07/Aquarius/internal/domain/conversation"
	"github.com/Tonyjh07/Aquarius/internal/i18n"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

// deepseekChunk 一块导入的会话数：官方全量包一次全导会占住内存与 UI，100 条一块既给
// 进度行也给可中断点。
const deepseekChunk = 100

// srcRecord sidecar 的一项：源会话 id → 导入成的会话与源里当时的 updated_at。
type srcRecord struct {
	ConversationID string `json:"conversation_id"`
	UpdatedAt      string `json:"updated_at"`
}

// loadSrcLedger 读 sidecar；文件不存在或损坏都当空表——这份文件不入库，坏了只是重导一遍。
func loadSrcLedger(path string) map[string]srcRecord {
	body, err := os.ReadFile(path)
	if err != nil {
		return map[string]srcRecord{}
	}
	var out map[string]srcRecord
	if json.Unmarshal(body, &out) != nil {
		return map[string]srcRecord{}
	}
	if out == nil {
		out = map[string]srcRecord{}
	}
	return out
}

// saveSrcLedger 原子写 sidecar（写临时文件再换名，中断不留半截）。
func saveSrcLedger(path string, ledger map[string]srcRecord) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return wrapSession("err.mkdir_export", err)
	}
	body, err := json.MarshalIndent(ledger, "", "  ")
	if err != nil {
		return wrapSession("err.import_ledger_write", err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, body, 0o644); err != nil {
		return wrapSession("err.import_ledger_write", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return wrapSession("err.import_ledger_write", err)
	}
	return nil
}

// execImportDeepSeek deepseek 整库导入（execImport 认出格式后转到这里）。
func (s *Session) execImportDeepSeek(ctx context.Context, path string, format port.ExchangeFormat, pick []int, all, omitFiles bool) (string, error) {
	if all && len(pick) > 0 {
		return "", errors.New(i18n.T("exchange.all_and_pick"))
	}
	idx, ok := s.importer.(port.ExchangeIndexer)
	if !ok {
		return "", errors.New(i18n.T("exchange.importer_missing"))
	}
	entries, err := idx.Index(ctx, path)
	if err != nil {
		return "", wrapSession("err.import_failed", err)
	}
	if !all && len(pick) == 0 {
		// 清单先行：塞 2274 条进左栏不该是一条命令的默认结果，让用户先看有什么再挑。
		return dsListing(entries), nil
	}
	// 选中的清单序号（--pick 已由调用方解好；越界交给下面的取值处报因）。
	want := pick
	if all {
		want = make([]int, len(entries))
		for i := range want {
			want[i] = i + 1
		}
	}
	byIndex := make(map[int]port.ExchangeEntry, len(entries))
	for _, e := range entries {
		byIndex[e.Index] = e
	}
	// sidecar 去重：同 id 且 updated_at 未变的跳过。变了导成新会话（不做增量合并）。
	var ledger map[string]srcRecord
	ledgerPath := ""
	if s.importSourcesDir != "" {
		ledgerPath = filepath.Join(s.importSourcesDir, "deepseek.json")
		ledger = loadSrcLedger(ledgerPath)
	}
	var (
		picks    []int
		skipped  int
		skipNote bool
	)
	for _, i := range want {
		e, ok := byIndex[i]
		if !ok {
			return "", wrapSession("err.import_failed",
				errors.New(i18n.Tf("exchange.bad_pick", "value", fmt.Sprintf("%d", i))))
		}
		if ledgerPath != "" {
			if rec, seen := ledger[e.ID]; seen && rec.UpdatedAt == dsLedgerStamp(e.UpdatedAt) {
				skipped++
				continue
			}
		} else if e.UpdatedAt.IsZero() {
			// 没接 sidecar：去重不成立，但不能静默装作会去重——只导一次的路径不受影响。
			skipNote = true
		}
		picks = append(picks, i)
	}
	if len(picks) == 0 {
		return dsSkipReceipt(skipped), nil
	}
	opts := port.ImportOptions{OmitFiles: omitFiles}
	// 分块导入：块内失败只回滚本块，Ctrl-C 留下的是已完成的整块（sidecar 已写）。
	imported := make([]*conversation.Conversation, 0, len(picks))
	var (
		totalNodes int64
		allEntries []port.ExchangeEntry
		counts     = map[string]int{}
	)
	for start := 0; start < len(picks); start += deepseekChunk {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		end := start + deepseekChunk
		if end > len(picks) {
			end = len(picks)
		}
		chunk := picks[start:end]
		f, err := os.Open(path)
		if err != nil {
			return "", wrapSession("err.open_file", err)
		}
		trees, rep, err := s.importer.Import(ctx, f, format, chunk, opts)
		f.Close()
		if err != nil {
			return "", wrapSession("err.import_failed", err)
		}
		saved := make([]string, 0, len(trees))
		for i, c := range trees {
			c, err := remapConversationID(c, s.ids.ConversationID())
			if err != nil {
				return "", wrapSession("err.assign_import_id", err)
			}
			if err := s.store.Save(ctx, c); err != nil {
				// 回滚本块已写入磁盘的：用户要知道哪几个进来了，半截状态比没导入更糟。
				for _, id := range saved {
					_ = s.store.Remove(ctx, conversation.ID(id))
				}
				return "", wrapSession("err.save_import", err)
			}
			saved = append(saved, string(c.ID))
			if ledgerPath != "" && i < len(rep.Entries) {
				ledger[rep.Entries[i].ID] = srcRecord{
					ConversationID: string(c.ID),
					UpdatedAt:      dsLedgerStamp(rep.Entries[i].UpdatedAt),
				}
			}
			imported = append(imported, c)
		}
		totalNodes += int64(rep.Nodes)
		allEntries = append(allEntries, rep.Entries...)
		for k, v := range rep.Counts {
			counts[k] += v
		}
		if ledgerPath != "" {
			if err := saveSrcLedger(ledgerPath, ledger); err != nil {
				return "", err
			}
		}
		// 进度行（Notice 通道）：大导入不能让用户对着黑屏猜跑没跑。
		if s.ui != nil && end < len(picks) {
			_ = s.ui.Emit(ctx, port.NoticeEvent{Text: i18n.Tf("exchange.import.progress",
				"done", len(imported), "total", len(picks))})
		}
	}
	if len(imported) == 0 {
		return "", errors.New(i18n.T("exchange.empty_import"))
	}
	s.cur = imported[0]
	// 会话已写入磁盘并切过来：从这里往下失败不能把已完成的事实报成失败（与主路径同一口径）。
	if err := s.emitClear(ctx); err != nil {
		return "", wrapSession("exchange.refresh_failed", err)
	}
	if _, err := s.replayHistory(ctx, "replay.verb.imported"); err != nil {
		return "", wrapSession("exchange.replay_failed", err)
	}
	rep := port.ExchangeReport{Format: format.Name, Nodes: int(totalNodes), Entries: allEntries}
	return dsImportReceipt(path, format, rep, counts, skipped, skipNote), nil
}

// dsListing 清单先行：列出有什么（按清单序 = updated_at 倒序），不带导入动作。
// 太长的清单只展示头部，尾部记数——清单是给人挑的，不是给人读的。
func dsListing(entries []port.ExchangeEntry) string {
	const show = 50
	var b strings.Builder
	fmt.Fprintf(&b, "%s\n", i18n.Tf("exchange.import.listing.head", "count", len(entries)))
	for i, e := range entries {
		if i >= show {
			fmt.Fprintf(&b, "%s\n", i18n.Tf("exchange.import.listing.more", "count", len(entries)-show))
			break
		}
		fmt.Fprintf(&b, "%s\n", i18n.Tf("exchange.import.listing.item",
			"index", e.Index, "date", dsDay(e.UpdatedAt), "title", entryTitle(e), "count", e.Nodes))
	}
	return strings.TrimRight(b.String(), "\n")
}

// dsDay 源里 updated_at 的日期部分（清单认人用；零值给空）。
func dsDay(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Local().Format("2006-01-02")
}

// dsLedgerStamp sidecar 里的时间戳（UTC RFC3339 纳秒；零值给空串）。
func dsLedgerStamp(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339Nano)
}

// dsSkipReceipt 一个都没导（全部与上次相同）：说清「为什么什么都没发生」。
func dsSkipReceipt(skipped int) string {
	return i18n.Tf("exchange.import.all_skipped", "count", skipped)
}

// dsImportReceipt deepseek 的导入回执：主行复用 importReceipt（清单条目 / 保真行都在那里），
// 计数行与跳过行另起（那些是这一档特有的事实）。
func dsImportReceipt(path string, f port.ExchangeFormat, rep port.ExchangeReport, counts map[string]int, skipped int, skipNote bool) string {
	out := importReceipt(path, f, rep)
	var extra strings.Builder
	if skipped > 0 {
		fmt.Fprintf(&extra, "%s\n", i18n.Tf("exchange.import.skipped", "count", skipped))
	}
	if skipNote {
		fmt.Fprintf(&extra, "%s\n", i18n.T("exchange.import.no_ledger_note"))
	}
	for _, line := range countLines(counts) {
		fmt.Fprintf(&extra, "%s\n", line)
	}
	if extra.Len() == 0 {
		return out
	}
	// 主回执已经 TrimRight；把附加行接在丢项段之前会打乱它的版式，直接接在尾部最稳。
	return out + "\n" + strings.TrimRight(extra.String(), "\n")
}

// countLines 计数表 → 回执行（复用适配器的措辞键：kind 与 Dropped 同源）。
func countLines(counts map[string]int) []string {
	if len(counts) == 0 {
		return nil
	}
	keys := make([]string, 0, len(counts))
	for k := range counts {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]string, 0, len(keys))
	for _, k := range keys {
		out = append(out, i18n.Tf("exchange.io.dropped.line", "kind", k, "count", counts[k]))
	}
	return out
}
