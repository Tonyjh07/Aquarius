// 会话回收站（D311）：`/rmconv` 的删除落点。
//
// trash 目录与会话目录同形——平铺、同名（`<id>.json`、`.bak*`、`.tmp`、
// `<id>.memory.md`），删除时间就是主文件的改动时间（移动不改 mtime），不建任何
// meta 记录文件。移入与还原都是同盘改名：不复制字节，失败留在原地，不会出半截状态。
package storejson

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/Tonyjh07/Aquarius/internal/domain/conversation"
	"github.com/Tonyjh07/Aquarius/internal/i18n"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

// convFileSet 一个会话在目录里的整份文件：主文件 `<id>.json` 加上同前缀的各代备份
// 与临时残片、会话记忆 `<id>.memory.md`。与 Remove 的收集口径一致——删除搬走什么，
// 回收站就搬什么，还原才完整。
func convFileSet(dir string, id conversation.ID) ([]string, bool) {
	base := string(id) + ".json"
	main := filepath.Join(dir, base)
	if _, err := os.Stat(main); err != nil {
		return nil, false // 主文件不在 = 这个 ID 在该目录没有会话
	}
	paths := []string{main}
	prefix := base + "."
	if entries, err := os.ReadDir(dir); err == nil {
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			name := e.Name()
			// 备份代（`<id>.json.bak*`）、临时残片（`<id>.json.*.tmp`）、会话记忆。
			// 前缀 `<id>.json.` 不会误伤别的会话：ID 之间没有前缀包含关系（见 path）。
			if strings.HasPrefix(name, prefix) || name == string(id)+".memory.md" {
				paths = append(paths, filepath.Join(dir, name))
			}
		}
	}
	return paths, true
}

// MoveToTrash 把会话整份文件集改名移进 trashDir（D311）。trashDir 不存在则创建；
// 目标已有同名会话（重复删除）报错不覆盖。主文件不在返回 ErrNotFound。
//
// 与 Save/Remove 共用同一把每会话锁：移入进行时既不许写入也不许删除——否则写盘的
// 换入会落在移动之后，会话「删了又回来」。
func (s *Store) MoveToTrash(ctx context.Context, trashDir string, id conversation.ID) error {
	if err := checkCtx(ctx); err != nil {
		return err
	}
	if id == "" {
		return errors.New(i18n.T("store.err.empty_id"))
	}
	if strings.TrimSpace(trashDir) == "" {
		return errors.New(i18n.T("store.err.empty_dir"))
	}
	if err := os.MkdirAll(trashDir, 0o755); err != nil {
		return fmt.Errorf("%s: %w", i18n.Tf("store.err.create_dir", "dir", trashDir), err)
	}
	if _, err := os.Stat(filepath.Join(trashDir, string(id)+".json")); err == nil {
		return errors.New(i18n.Tf("store.err.trash_exists", "id", id))
	}
	unlock := s.lockSave(s.path(id))
	defer unlock()
	paths, ok := convFileSet(s.dir, id)
	if !ok {
		return fmt.Errorf("storejson: %w: %s", ErrNotFound, id)
	}
	for _, src := range paths {
		dst := filepath.Join(trashDir, filepath.Base(src))
		if err := os.Rename(src, dst); err != nil {
			return fmt.Errorf("%s: %w", i18n.Tf("store.err.trash_move", "id", id), err)
		}
	}
	return nil
}

// RestoreFromTrash 把 trashDir 里的会话整份文件集移回会话目录（D311）。回收站没有
// 这个会话返回 ErrNotFound；会话目录已有同 ID 文件拒绝（不覆盖，见 D311 决策 3）。
//
// 锁取会话目录那把：还原进来的文件不许和写入、删除并发。
func (s *Store) RestoreFromTrash(ctx context.Context, trashDir string, id conversation.ID) error {
	if err := checkCtx(ctx); err != nil {
		return err
	}
	if id == "" {
		return errors.New(i18n.T("store.err.empty_id"))
	}
	if _, err := os.Stat(filepath.Join(trashDir, string(id)+".json")); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("storejson: %w: %s", ErrNotFound, id)
		}
		return fmt.Errorf("%s: %w", i18n.Tf("store.err.read", "id", id), err)
	}
	if _, err := os.Stat(s.path(id)); err == nil {
		return errors.New(i18n.Tf("store.err.restore_exists", "id", id))
	}
	unlock := s.lockSave(s.path(id))
	defer unlock()
	paths, ok := convFileSet(trashDir, id)
	if !ok { // 与上面的 Stat 之间文件被外部拿走了：同报 ErrNotFound
		return fmt.Errorf("storejson: %w: %s", ErrNotFound, id)
	}
	for _, src := range paths {
		dst := filepath.Join(s.dir, filepath.Base(src))
		if err := os.Rename(src, dst); err != nil {
			return fmt.Errorf("%s: %w", i18n.Tf("store.err.restore_move", "id", id), err)
		}
	}
	return nil
}

// ListTrash 列出 trashDir 里的回收站条目，按删除时间降序（最近删的在前）。
// 解不开的文件跳过不致命（与 List 同一姿态）；目录不存在 = 回收站为空，返回空表。
func (s *Store) ListTrash(ctx context.Context, trashDir string) ([]port.TrashEntry, error) {
	if err := checkCtx(ctx); err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(trashDir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("%s: %w", i18n.Tf("store.err.list", "dir", trashDir), err)
	}
	var out []port.TrashEntry
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".json") {
			continue
		}
		id := conversation.ID(strings.TrimSuffix(name, ".json"))
		data, err := os.ReadFile(filepath.Join(trashDir, name))
		if err != nil {
			continue
		}
		c, err := decode(data, id)
		if err != nil {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		out = append(out, port.TrashEntry{
			ID:        c.ID,
			Title:     c.Title,
			MessageN:  c.NodeCount() - 1, // 不含 Root 节点
			UpdatedAt: c.UpdatedAt,
			DeletedAt: info.ModTime(),
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].DeletedAt.Equal(out[j].DeletedAt) {
			return out[i].DeletedAt.After(out[j].DeletedAt)
		}
		return out[i].ID > out[j].ID
	})
	return out, nil
}

// EmptyTrash 清空回收站（`/trash empty`，D311）：删掉全部条目的整份文件集，返回
// 清掉的条目数。单份失败继续清其余的，最后聚合报因——与 SweepTrash 同一姿态。
func (s *Store) EmptyTrash(ctx context.Context, trashDir string) (int, error) {
	if err := checkCtx(ctx); err != nil {
		return 0, err
	}
	entries, err := os.ReadDir(trashDir)
	if errors.Is(err, os.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("%s: %w", i18n.Tf("store.err.list", "dir", trashDir), err)
	}
	removed := 0
	var failed []string
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".json") {
			continue
		}
		id := conversation.ID(strings.TrimSuffix(name, ".json"))
		paths, _ := convFileSet(trashDir, id)
		bad := false
		for _, p := range paths {
			if err := os.Remove(p); err != nil && !errors.Is(err, fs.ErrNotExist) {
				bad = true
				break
			}
		}
		if bad {
			failed = append(failed, string(id))
			continue
		}
		removed++
	}
	if len(failed) > 0 {
		return removed, errors.New(i18n.Tf("store.err.sweep_failed", "ids", strings.Join(failed, ", ")))
	}
	return removed, nil
}

// SweepTrash 清掉 trashDir 里主文件改动时间超过 maxAge 的条目（D311：启动清理）。
// 返回清掉的条目数；目录不存在 = 没有回收站，返回 0。单份失败继续清其余的，
// 最后聚合报因——清不动一个不该拖垮整站。
func (s *Store) SweepTrash(ctx context.Context, trashDir string, maxAge time.Duration) (int, error) {
	if err := checkCtx(ctx); err != nil {
		return 0, err
	}
	cutoff := time.Now().Add(-maxAge)
	entries, err := os.ReadDir(trashDir)
	if errors.Is(err, os.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("%s: %w", i18n.Tf("store.err.list", "dir", trashDir), err)
	}
	removed := 0
	var failed []string
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".json") {
			continue
		}
		id := conversation.ID(strings.TrimSuffix(name, ".json"))
		info, err := e.Info()
		if err != nil || info.ModTime().After(cutoff) {
			continue // 读不动的不当超龄（宁可多留，不误清）
		}
		paths, _ := convFileSet(trashDir, id)
		bad := false
		for _, p := range paths {
			if err := os.Remove(p); err != nil && !errors.Is(err, fs.ErrNotExist) {
				bad = true
				break
			}
		}
		if bad {
			failed = append(failed, string(id))
			continue
		}
		removed++
	}
	if len(failed) > 0 {
		return removed, errors.New(i18n.Tf("store.err.sweep_failed", "ids", strings.Join(failed, ", ")))
	}
	return removed, nil
}
