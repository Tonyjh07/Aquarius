// Package storejson 实现 port.ConversationStore：
// 一会话一 JSON 文件（<dir>/<id>.json），换代前把旧文件保留为一代 .bak（D7）。
//
// 文件是明文 JSON（个人数据主权，可用编辑器直接查看）；
// Load 会做不变量自检（Validate），主文件缺失或损坏时回退读取 .bak。
package storejson

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Tonyjh07/Aquarius/internal/domain/conversation"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

// ErrNotFound 会话文件不存在。
var ErrNotFound = errors.New("storejson: conversation not found")

var _ port.ConversationStore = (*Store)(nil)

// Store 基于文件系统的会话存储（一会话一 JSON）。
type Store struct {
	dir string
}

// New 创建存储并确保目录存在。
func New(dir string) (*Store, error) {
	if strings.TrimSpace(dir) == "" {
		return nil, errors.New("storejson: 目录不能为空")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("storejson: 创建目录 %s: %w", dir, err)
	}
	return &Store{dir: dir}, nil
}

// Save 整树序列化落盘：先写 .tmp，再把现有文件挪成 .bak，最后原子换入（D7）。
// 入树前做不变量自检，坏数据不落盘。
func (s *Store) Save(ctx context.Context, c *conversation.Conversation) error {
	if err := checkCtx(ctx); err != nil {
		return err
	}
	if c == nil {
		return errors.New("storejson: nil conversation")
	}
	if err := c.Validate(); err != nil {
		return fmt.Errorf("storejson: 保存 %s 被拒（不变量校验失败）: %w", c.ID, err)
	}
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return fmt.Errorf("storejson: 序列化 %s: %w", c.ID, err)
	}
	dst := s.path(c.ID)
	tmp := dst + ".tmp"
	if err := os.WriteFile(tmp, append(data, '\n'), 0o644); err != nil {
		return fmt.Errorf("storejson: 写入 %s: %w", tmp, err)
	}
	if _, err := os.Stat(dst); err == nil {
		// 现有文件保留一代 .bak，再换入新文件。
		if err := os.Rename(dst, dst+".bak"); err != nil {
			_ = os.Remove(tmp)
			return fmt.Errorf("storejson: 备份 %s: %w", dst, err)
		}
	}
	if err := os.Rename(tmp, dst); err != nil {
		return fmt.Errorf("storejson: 换入 %s: %w", dst, err)
	}
	return nil
}

// Load 读取整树并做不变量自检；主文件缺失或损坏时回退读取一代 .bak。
func (s *Store) Load(ctx context.Context, id conversation.ID) (*conversation.Conversation, error) {
	if err := checkCtx(ctx); err != nil {
		return nil, err
	}
	if id == "" {
		return nil, errors.New("storejson: 空会话 ID")
	}
	dst := s.path(id)
	data, err := os.ReadFile(dst)
	if err == nil {
		c, derr := decode(data, id)
		if derr == nil {
			return c, nil
		}
		// 主文件损坏：尝试一代 .bak 兜底，仍失败则报主文件的错。
		if bdata, berr := os.ReadFile(dst + ".bak"); berr == nil {
			if c, berr := decode(bdata, id); berr == nil {
				return c, nil
			}
		}
		return nil, fmt.Errorf("storejson: 读取 %s: %w", id, derr)
	}
	if !errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("storejson: 读取 %s: %w", id, err)
	}
	if bdata, berr := os.ReadFile(dst + ".bak"); berr == nil {
		if c, berr := decode(bdata, id); berr == nil {
			return c, nil
		}
	}
	return nil, fmt.Errorf("storejson: %w: %s", ErrNotFound, id)
}

// List 列出全部会话摘要，按 UpdatedAt 降序（最新在前）。
// 单个损坏文件跳过不致命（由 Load 显式暴露），.bak/.tmp 不计入。
func (s *Store) List(ctx context.Context) ([]port.ConversationSummary, error) {
	if err := checkCtx(ctx); err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return nil, fmt.Errorf("storejson: 列出 %s: %w", s.dir, err)
	}
	var out []port.ConversationSummary
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".json") {
			continue
		}
		id := conversation.ID(strings.TrimSuffix(name, ".json"))
		data, err := os.ReadFile(filepath.Join(s.dir, name))
		if err != nil {
			continue
		}
		c, err := decode(data, id)
		if err != nil {
			continue // 损坏文件跳过，不影响其余会话
		}
		out = append(out, port.ConversationSummary{
			ID:        c.ID,
			Title:     c.Title,
			MessageN:  len(c.Nodes) - 1, // 不含 Root 节点
			UpdatedAt: c.UpdatedAt,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].UpdatedAt.Equal(out[j].UpdatedAt) {
			return out[i].UpdatedAt.After(out[j].UpdatedAt)
		}
		return out[i].ID > out[j].ID
	})
	return out, nil
}

// Remove 硬删除会话文件（连同 .bak/.tmp）；什么都不存在时返回 ErrNotFound。
func (s *Store) Remove(ctx context.Context, id conversation.ID) error {
	if err := checkCtx(ctx); err != nil {
		return err
	}
	if id == "" {
		return errors.New("storejson: 空会话 ID")
	}
	dst := s.path(id)
	_, jsonErr := os.Stat(dst)
	_, bakErr := os.Stat(dst + ".bak")
	if errors.Is(jsonErr, fs.ErrNotExist) && errors.Is(bakErr, fs.ErrNotExist) {
		return fmt.Errorf("storejson: %w: %s", ErrNotFound, id)
	}
	for _, p := range []string{dst, dst + ".bak", dst + ".tmp"} {
		if err := os.Remove(p); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("storejson: 删除 %s: %w", p, err)
		}
	}
	return nil
}

// path 会话文件路径。
func (s *Store) path(id conversation.ID) string {
	return filepath.Join(s.dir, string(id)+".json")
}

// decode 反序列化 + ID 一致性检查 + 空 map 归一化 + 不变量自检（加载后自检，DESIGN §4.1）。
func decode(data []byte, id conversation.ID) (*conversation.Conversation, error) {
	var c conversation.Conversation
	if err := json.Unmarshal(data, &c); err != nil {
		return nil, fmt.Errorf("解析 JSON: %w", err)
	}
	if c.ID != id {
		return nil, fmt.Errorf("文件内容属于会话 %q 而非 %q", c.ID, id)
	}
	// D19 破坏性切换：M0 虚拟 Root 格式（无 Root 节点或 Head 为空）不兼容，明确报因引导重建。
	if root, ok := c.Nodes[conversation.MessageID(c.ID)]; !ok || root.Role != conversation.RoleRoot || c.Head == "" {
		return nil, fmt.Errorf("旧格式会话（M0 虚拟 Root，D19 破坏性切换）：与实 Root 不兼容，请删除该文件重建")
	}
	// 空 map 归一化：JSON 里的 null 反序列化为 nil map，直接写入会 panic。
	if c.Nodes == nil {
		c.Nodes = map[conversation.MessageID]conversation.Message{}
	}
	if c.Children == nil {
		c.Children = map[conversation.MessageID][]conversation.MessageID{}
	}
	if c.RevisedFrom == nil {
		c.RevisedFrom = map[conversation.MessageID]conversation.MessageID{}
	}
	if err := c.Validate(); err != nil {
		return nil, fmt.Errorf("不变量校验失败: %w", err)
	}
	return &c, nil
}

// checkCtx 校验 context 可用。
func checkCtx(ctx context.Context) error {
	if ctx == nil {
		return errors.New("storejson: nil context")
	}
	return ctx.Err()
}
