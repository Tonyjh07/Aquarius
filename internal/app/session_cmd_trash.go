package app

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Tonyjh07/Aquarius/internal/domain/conversation"
	"github.com/Tonyjh07/Aquarius/internal/i18n"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

// TrashStore 会话回收站能力（消费方小接口，D311）：内置 storejson 实现。
// 可选能力——store 没实现或 TrashDir 未接入时，删除退回硬删（port.Remove）、
// /trash 报未接入。回收站条目的类型定义在 port.TrashEntry 供两边共用。
type TrashStore interface {
	MoveToTrash(ctx context.Context, trashDir string, id conversation.ID) error
	ListTrash(ctx context.Context, trashDir string) ([]port.TrashEntry, error)
	RestoreFromTrash(ctx context.Context, trashDir string, id conversation.ID) error
	EmptyTrash(ctx context.Context, trashDir string) (int, error)
}

// trashStore 取回收站能力；未接入（TrashDir 为空或 store 不实现）返回 nil。
func (s *Session) trashStore() TrashStore {
	if s.trashDir == "" {
		return nil
	}
	ts, _ := s.store.(TrashStore)
	return ts
}

// execTrash 实现 /trash：会话回收站（D311）。形态：
//   - 无参：列出（ID、标题、消息数、删除时间，按删除时间降序）；
//   - restore <id>…：还原回原 ID（可多个、支持唯一前缀，不确认——恢复性动作错了再删）；
//   - empty：清空，二次确认（一次删光没有回头路）。
func (s *Session) execTrash(ctx context.Context, cmd port.Command) (string, error) {
	ts := s.trashStore()
	if ts == nil {
		return "", errors.New("session: " + i18n.T("tree.trash.unavailable"))
	}
	if len(cmd.Args) == 0 {
		return s.trashList(ctx, ts)
	}
	switch cmd.Args[0] {
	case "restore":
		if len(cmd.Args) < 2 {
			return "", errors.New(i18n.T("usage.trash"))
		}
		return s.trashRestore(ctx, ts, cmd.Args[1:])
	case "empty":
		return s.trashEmpty(ctx, ts)
	default:
		return "", errors.New(i18n.T("usage.trash"))
	}
}

// trashList 实现 /trash 无参形态。
func (s *Session) trashList(ctx context.Context, ts TrashStore) (string, error) {
	entries, err := ts.ListTrash(ctx, s.trashDir)
	if err != nil {
		return "", wrapSession("err.list_trash", err)
	}
	if len(entries) == 0 {
		return i18n.T("tree.trash.empty"), nil
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s\n", i18n.Tf("tree.trash.header", "n", len(entries)))
	for _, e := range entries {
		fmt.Fprintf(&b, "%s\n", i18n.Tf("tree.trash.item",
			"id", e.ID, "title", e.Title, "n", e.MessageN,
			"when", e.DeletedAt.Format(time.DateTime)))
	}
	return strings.TrimRight(b.String(), "\n"), nil
}

// trashRestore 实现 /trash restore：还原回原 ID。还原不需要确认，但要先全部解析
// ——有一个前缀认不出就一个都不动（与 /rmconv 的批量口径一致）。
func (s *Session) trashRestore(ctx context.Context, ts TrashStore, raws []string) (string, error) {
	entries, err := ts.ListTrash(ctx, s.trashDir)
	if err != nil {
		return "", wrapSession("err.list_trash", err)
	}
	byID := make(map[conversation.ID]port.TrashEntry, len(entries))
	for _, e := range entries {
		byID[e.ID] = e
	}
	targets := make([]conversation.ID, 0, len(raws))
	seen := make(map[conversation.ID]bool, len(raws))
	for _, raw := range raws {
		id, err := resolveFromEntries(byID, raw)
		if err != nil {
			return "", err
		}
		if seen[id] {
			continue
		}
		seen[id] = true
		targets = append(targets, id)
	}
	restored := make([]string, 0, len(targets))
	for _, id := range targets {
		if err := ts.RestoreFromTrash(ctx, s.trashDir, id); err != nil {
			return "", wrapSession("err.restore_conversation", err)
		}
		restored = append(restored, string(id))
	}
	if len(targets) == 1 {
		e := byID[targets[0]]
		return i18n.Tf("tree.trash.restored", "id", targets[0], "title", e.Title), nil
	}
	return i18n.Tf("tree.trash.restored_many", "n", len(targets), "ids", strings.Join(restored, ", ")), nil
}

// resolveFromEntries 在回收站条目里按 ID（精确或唯一前缀）解析。
// 回收站里的会话不在 store.List 里，/rmconv 用的前缀解析看不见它们，这里自己认。
func resolveFromEntries(byID map[conversation.ID]port.TrashEntry, raw string) (conversation.ID, error) {
	if _, ok := byID[conversation.ID(raw)]; ok {
		return conversation.ID(raw), nil
	}
	var hit conversation.ID
	for id := range byID {
		if strings.HasPrefix(string(id), raw) {
			if hit != "" {
				return "", fmt.Errorf("session: %s", i18n.Tf("tree.trash.ambiguous", "raw", raw))
			}
			hit = id
		}
	}
	if hit == "" {
		return "", fmt.Errorf("session: %s", i18n.Tf("tree.trash.not_found", "raw", raw))
	}
	return hit, nil
}

// trashEmpty 实现 /trash empty：清空回收站（二次确认）。
func (s *Session) trashEmpty(ctx context.Context, ts TrashStore) (string, error) {
	entries, err := ts.ListTrash(ctx, s.trashDir)
	if err != nil {
		return "", wrapSession("err.list_trash", err)
	}
	if len(entries) == 0 {
		return i18n.T("tree.trash.empty"), nil
	}
	if s.confirmer == nil {
		return "", errors.New("session: " + i18n.T("tree.trash.no_confirmer"))
	}
	prompt := i18n.Tf("tree.trash.empty_confirm", "n", len(entries))
	ans, err := s.confirmer.Confirm(ctx, port.ConfirmRequest{Origin: port.OriginCommand, Prompt: prompt})
	if err != nil {
		return "", wrapSession("err.confirm_empty_trash", err)
	}
	if !ans.Allow {
		return i18n.T("tree.trash.canceled"), nil
	}
	n, err := ts.EmptyTrash(ctx, s.trashDir)
	if err != nil {
		return "", wrapSession("err.empty_trash", err)
	}
	return i18n.Tf("tree.trash.emptied", "n", n), nil
}
