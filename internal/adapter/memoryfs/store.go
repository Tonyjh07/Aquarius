// Package memoryfs 实现 port.MemoryStore：markdown 即记忆（D11/D23）。
//
// 布局（D23）：全局记忆 = <globalPath>（memories.md 单文件）；
// 会话记忆 = <convDir>/<会话ID>.memory.md（随会话文件夹就近存放）。
// 文件是明文 markdown，用户可直接编辑，下次读取即生效；
// 文档名寻址（port.GlobalMemoryDoc / port.SessionMemoryDoc），含路径分隔符的名字一律拒绝。
package memoryfs

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Tonyjh07/Aquarius/internal/port"
)

// ErrNotFound 记忆文档不存在。
var ErrNotFound = errors.New("memoryfs: memory doc not found")

var _ port.MemoryStore = (*Store)(nil)

// Store 基于文件系统的记忆存储（全局单文件 + 会话文件）。
type Store struct {
	global  string // 全局记忆文件路径（…/memories.md）
	convDir string // 会话记忆所在目录（…/conversations）
}

// New 创建记忆存储。global 为全局记忆文件路径，convDir 为会话记忆目录（与会话树同目录）。
func New(global, convDir string) (*Store, error) {
	if strings.TrimSpace(global) == "" || strings.TrimSpace(convDir) == "" {
		return nil, errors.New("memoryfs: 路径不能为空")
	}
	return &Store{global: global, convDir: convDir}, nil

}

// path 按文档名解析到文件路径；非法名字（路径分隔符/..）与未知名字报错（防目录穿越）。
func (s *Store) path(name string) (string, error) {
	if name == port.GlobalMemoryDoc {
		return s.global, nil
	}
	// 会话文档：文件名必须恰好是 <id>.memory.md，id 不含分隔符与 ..
	base := name
	if i := strings.IndexAny(base, `/\`); i >= 0 {
		return "", fmt.Errorf("memoryfs: 非法记忆文档名 %q", name)
	}
	if base != filepath.Base(base) || strings.Contains(base, "..") {
		return "", fmt.Errorf("memoryfs: 非法记忆文档名 %q", name)
	}
	if !strings.HasSuffix(base, port.SessionMemorySuffix) {
		return "", fmt.Errorf("memoryfs: 未知记忆文档 %q（可用：%s 或 <会话ID>%s）",
			name, port.GlobalMemoryDoc, port.SessionMemorySuffix)
	}
	id := strings.TrimSuffix(base, port.SessionMemorySuffix)
	if id == "" || strings.Contains(id, "..") {
		return "", fmt.Errorf("memoryfs: 非法记忆文档名 %q", name)
	}
	return filepath.Join(s.convDir, base), nil
}

// docAt 读取一个存在的文件为 MemoryDoc；不存在返回 ok=false。
func docAt(name, file string) (port.MemoryDoc, bool, error) {
	data, err := os.ReadFile(file)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return port.MemoryDoc{}, false, nil
		}
		return port.MemoryDoc{}, false, fmt.Errorf("memoryfs: 读取 %s: %w", name, err)
	}
	fi, err := os.Stat(file)
	if err != nil {
		return port.MemoryDoc{}, false, fmt.Errorf("memoryfs: stat %s: %w", name, err)
	}
	return port.MemoryDoc{Name: name, Content: string(data), ModTime: fi.ModTime()}, true, nil
}

// Index 列出全部记忆文档（全局 + 已存在的会话文件），按名字排序；Summary 取首个非空行（截断）。
// 空文件（含尚不存在的全局文件）不占位——索引只列真实存在、有内容的文档。
func (s *Store) Index(ctx context.Context) ([]port.MemoryIndexEntry, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var out []port.MemoryIndexEntry
	add := func(name, file string) error {
		doc, ok, err := docAt(name, file)
		if err != nil || !ok {
			return err
		}
		if strings.TrimSpace(doc.Content) == "" {
			return nil // 空文件不进索引
		}
		out = append(out, port.MemoryIndexEntry{Name: name, Summary: summarize(doc.Content)})
		return nil
	}
	if err := add(port.GlobalMemoryDoc, s.global); err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(s.convDir)
	if err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("memoryfs: 列出会话记忆 %s: %w", s.convDir, err)
		}
		return out, nil // 会话目录尚未创建：只有全局项
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), port.SessionMemorySuffix) {
			continue
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if err := add(e.Name(), filepath.Join(s.convDir, e.Name())); err != nil {
			return nil, err
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// summarize 文档摘要：首个非空行、去 markdown 标题记号、按 rune 截断。
func summarize(content string) string {
	const maxRunes = 80
	for _, line := range strings.Split(content, "\n") {
		t := strings.TrimSpace(line)
		if t == "" {
			continue
		}
		t = strings.TrimLeft(t, "#>*- ")
		r := []rune(t)
		if len(r) > maxRunes {
			return string(r[:maxRunes]) + "…"
		}
		return t
	}
	return ""
}

// Read 读取记忆文档；不存在返回 ErrNotFound。
func (s *Store) Read(ctx context.Context, name string) (port.MemoryDoc, error) {
	if err := ctx.Err(); err != nil {
		return port.MemoryDoc{}, err
	}
	file, err := s.path(name)
	if err != nil {
		return port.MemoryDoc{}, err
	}
	doc, ok, err := docAt(name, file)
	if err != nil {
		return port.MemoryDoc{}, err
	}
	if !ok {
		return port.MemoryDoc{}, fmt.Errorf("%w: %s", ErrNotFound, name)
	}
	return doc, nil
}

// Write 写入记忆文档（整文件覆盖；自动创建父目录）。ModTime 忽略（落盘后回读即真值）。
func (s *Store) Write(ctx context.Context, doc port.MemoryDoc) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	file, err := s.path(doc.Name)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		return fmt.Errorf("memoryfs: 创建目录: %w", err)
	}
	tmp := file + ".tmp"
	if err := os.WriteFile(tmp, []byte(doc.Content), 0o644); err != nil {
		return fmt.Errorf("memoryfs: 写入 %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, file); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("memoryfs: 换入 %s: %w", file, err)
	}
	return nil
}

// Remove 删除记忆文档；不存在返回 ErrNotFound。
func (s *Store) Remove(ctx context.Context, name string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	file, err := s.path(name)
	if err != nil {
		return err
	}
	if err := os.Remove(file); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("%w: %s", ErrNotFound, name)
		}
		return fmt.Errorf("memoryfs: 删除 %s: %w", name, err)
	}
	return nil
}

// Search 关键词检索（大小写不敏感，D11）：命中行号与片段，单文档命中封顶防爆上下文。
func (s *Store) Search(ctx context.Context, query string) ([]port.MemoryHit, error) {
	q := strings.ToLower(strings.TrimSpace(query))
	if q == "" {
		return nil, errors.New("memoryfs: 检索词不能为空")
	}
	const maxHits = 50
	var out []port.MemoryHit
	docs, err := s.Index(ctx) // 只搜有内容的文档
	if err != nil {
		return nil, err
	}
	for _, entry := range docs {
		if len(out) >= maxHits {
			break
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		doc, err := s.Read(ctx, entry.Name)
		if err != nil {
			return nil, err
		}
		for i, line := range strings.Split(doc.Content, "\n") {
			if len(out) >= maxHits {
				break
			}
			if !strings.Contains(strings.ToLower(line), q) {
				continue
			}
			out = append(out, port.MemoryHit{
				Name:    entry.Name,
				Line:    i + 1,
				Snippet: snippet(line),
			})
		}
	}
	return out, nil
}

// snippet 单行命中片段（去首尾空白、按 rune 截断）。
func snippet(line string) string {
	const maxRunes = 160
	t := strings.TrimSpace(line)
	r := []rune(t)
	if len(r) > maxRunes {
		return string(r[:maxRunes]) + "…"
	}
	return t
}
