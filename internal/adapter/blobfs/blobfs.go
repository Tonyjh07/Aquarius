// Package blobfs 实现 port.AttachmentStore（DESIGN §4.2）：
//   - sha256 内容寻址，同内容只存一份（Put 去重）；
//   - 布局 <dir>/<hash>：磁盘只存内容字节，Name/MIME/Size 随 Part.Ref
//     存在于会话树（D17），无需元数据侧文件；
//   - GC 按 keep 引用集清扫（Prune 会话不立即删附件；装配根在启动时
//     扫描全部会话收集引用后调用）。
package blobfs

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Tonyjh07/Aquarius/internal/domain/conversation"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

var _ port.AttachmentStore = (*Store)(nil)

// staleTemp 临时文件（Put 中断残留）视为可清扫的最长存活时间。
const staleTemp = time.Hour

// Store 附件库。
type Store struct {
	dir string
}

// New 创建附件库并确保目录存在。
func New(dir string) (*Store, error) {
	if strings.TrimSpace(dir) == "" {
		return nil, errors.New("blobfs: 目录为空")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("blobfs: 创建目录 %s: %w", dir, err)
	}
	return &Store{dir: dir}, nil
}

// Put 流式写入并计算 sha256：同内容已存在则复用（去重），返回引用。
// ctx 取消即中断并清理临时文件。
func (s *Store) Put(ctx context.Context, r io.Reader, mime, name string) (conversation.BlobRef, error) {
	if err := ctx.Err(); err != nil {
		return conversation.BlobRef{}, err
	}
	tmp, err := os.CreateTemp(s.dir, ".put-*.tmp")
	if err != nil {
		return conversation.BlobRef{}, fmt.Errorf("blobfs: 建临时文件: %w", err)
	}
	tmpName := tmp.Name()
	drop := true
	defer func() {
		if drop {
			_ = os.Remove(tmpName)
		}
	}()

	h := sha256.New()
	buf := make([]byte, 32<<10)
	var total int64
	for {
		if err := ctx.Err(); err != nil {
			_ = tmp.Close()
			return conversation.BlobRef{}, err
		}
		n, rerr := r.Read(buf)
		if n > 0 {
			if _, werr := tmp.Write(buf[:n]); werr != nil {
				_ = tmp.Close()
				return conversation.BlobRef{}, fmt.Errorf("blobfs: 写入: %w", werr)
			}
			_, _ = h.Write(buf[:n])
			total += int64(n)
		}
		if errors.Is(rerr, io.EOF) {
			break
		}
		if rerr != nil {
			_ = tmp.Close()
			return conversation.BlobRef{}, fmt.Errorf("blobfs: 读取输入: %w", rerr)
		}
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		// 崩溃安全：不 Sync 就 rename 可能留下"哈希名与内容不符"的损坏文件
		//（Put 的去重与 Get 都不会再校验内容），宁可失败。
		return conversation.BlobRef{}, fmt.Errorf("blobfs: 落盘: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return conversation.BlobRef{}, fmt.Errorf("blobfs: 关闭临时文件: %w", err)
	}

	ref := conversation.BlobRef{
		Hash: hex.EncodeToString(h.Sum(nil)),
		MIME: mime,
		Name: name,
		Size: total,
	}
	final := filepath.Join(s.dir, ref.Hash)
	if _, err := os.Stat(final); err == nil {
		return ref, nil // 已存在同内容：去重，临时文件由 defer 清理
	}
	if err := os.Rename(tmpName, final); err != nil {
		// 并发 Put 同内容竞态：目标已在则视为成功。
		if _, serr := os.Stat(final); serr != nil {
			return conversation.BlobRef{}, fmt.Errorf("blobfs: 换入 %s: %w", ref.Hash, err)
		}
		return ref, nil
	}
	drop = false
	return ref, nil
}

// Get 按引用打开附件内容（调用方负责 Close）。
func (s *Store) Get(_ context.Context, ref conversation.BlobRef) (io.ReadCloser, error) {
	p, err := s.pathOf(ref)
	if err != nil {
		return nil, err
	}
	f, err := os.Open(p)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("blobfs: 附件不存在 %.12s…（%s）", ref.Hash, ref.Name)
	}
	if err != nil {
		return nil, fmt.Errorf("blobfs: 打开附件: %w", err)
	}
	return f, nil
}

// Stat 附件是否存在；非法哈希报错（防路径穿越）。
func (s *Store) Stat(_ context.Context, ref conversation.BlobRef) (bool, error) {
	p, err := s.pathOf(ref)
	if err != nil {
		return false, err
	}
	if _, err := os.Stat(p); err == nil {
		return true, nil
	} else if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	return false, fmt.Errorf("blobfs: 检查附件: %w", err)
}

// GC 清扫 keep 之外的附件与过期临时文件。keep 的键为内容哈希（BlobRef.Hash）。
// 任一输入为 keep 未覆盖的活引用由调用方负责（装配根收集全量会话后才调用）。
func (s *Store) GC(_ context.Context, keep map[string]bool) error {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return fmt.Errorf("blobfs: 列目录: %w", err)
	}
	var firstErr error
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if keep[name] {
			continue
		}
		p := filepath.Join(s.dir, name)
		if isHash(name) {
			if err := os.Remove(p); err != nil && !errors.Is(err, fs.ErrNotExist) && firstErr == nil {
				firstErr = fmt.Errorf("blobfs: 清除 %s: %w", name, err)
			}
			continue
		}
		// 非哈希名 = Put 残留临时文件：只清过期的，避免误删进行中的写入。
		if fi, err := e.Info(); err == nil && time.Since(fi.ModTime()) > staleTemp {
			_ = os.Remove(p)
		}
	}
	return firstErr
}

// pathOf 引用 → 磁盘路径；哈希必须是 64 位小写 hex（防路径穿越）。
func (s *Store) pathOf(ref conversation.BlobRef) (string, error) {
	if !isHash(ref.Hash) {
		return "", fmt.Errorf("blobfs: 非法附件哈希 %q", ref.Hash)
	}
	return filepath.Join(s.dir, ref.Hash), nil
}

// isHash 判定 sha256 十六进制小写哈希。
func isHash(name string) bool {
	if len(name) != 64 {
		return false
	}
	_, err := hex.DecodeString(name)
	return err == nil && !strings.ContainsAny(name, "ABCDEF")
}
