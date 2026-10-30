// Package storejson 实现 port.ConversationStore：
// 一会话一 JSON 文件（<dir>/<id>.json），换代前把旧文件轮成备份代（默认一代 .bak，
// 可按 config `backup.keep` 保留 N 代，D7/D254）。
//
// 文件是明文 JSON（个人数据主权，可用编辑器直接查看）；
// Load 会做不变量自检（Validate），主文件缺失或损坏时按新到旧回退各代备份。
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
	"time"

	"github.com/Tonyjh07/Aquarius/internal/domain/conversation"
	"github.com/Tonyjh07/Aquarius/internal/domain/tool"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

// ErrNotFound 会话文件不存在。
var ErrNotFound = errors.New("storejson: conversation not found")

// ErrLegacyFormat 旧格式会话（不兼容的历史格式）：Load 显式报因、List 聚合上报，
// 绝不静默跳过、不做自动迁移。已有两种：M0 虚拟 Root（D19）、独立 tool 节点（D95 前半部分）。
var ErrLegacyFormat = errors.New("storejson: 发现旧格式会话")

var _ port.ConversationStore = (*Store)(nil)

// DefaultKeep 未显式配置时保留的备份代数（装配根按 config `backup.keep` 覆盖，D254）。
const DefaultKeep = 1

// Store 基于文件系统的会话存储（一会话一 JSON）。
type Store struct {
	dir string
	// keep 保留的 .bak 代数（>=1）：第 1 代为历史名 `<id>.json.bak`，更旧的为
	// `<id>.json.bak.<i-1>`。写盘时轮转，读盘时按新到旧回退（D254）。
	keep int
}

// New 创建存储并确保目录存在（备份保留一代，等价于历史行为）。
func New(dir string) (*Store, error) {
	return NewWithKeep(dir, DefaultKeep)
}

// NewWithKeep 同 New，但指定保留的备份代数（keep < 1 归一为 DefaultKeep）。
func NewWithKeep(dir string, keep int) (*Store, error) {
	if strings.TrimSpace(dir) == "" {
		return nil, errors.New("storejson: 目录不能为空")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("storejson: 创建目录 %s: %w", dir, err)
	}
	if keep < 1 {
		keep = DefaultKeep
	}
	return &Store{dir: dir, keep: keep}, nil
}

// backupPath 第 i 代备份路径（i 从 1 起）：第 1 代沿用历史名 `<dst>.bak`，
// 更旧的代为 `<dst>.bak.<i-1>`。
func backupPath(dst string, i int) string {
	if i <= 1 {
		return dst + ".bak"
	}
	return fmt.Sprintf("%s.bak.%d", dst, i-1)
}

// rotate 把现文件 dst 轮成第 1 代备份，并把既有各代整体后移一格，最老的一代出局。
// 只在 dst 可完整解析时调用（见 Save 的保存前完整性校验）。
func (s *Store) rotate(dst string) error {
	keep := s.keep
	if keep >= 2 {
		if err := removeIfExists(backupPath(dst, keep)); err != nil {
			return err
		}
	}
	for i := keep - 1; i >= 1; i-- {
		if err := renameIfExists(backupPath(dst, i), backupPath(dst, i+1)); err != nil {
			return err
		}
	}
	return os.Rename(dst, backupPath(dst, 1))
}

// removeIfExists / renameIfExists 备份轮转的小工具：源/目标不在不算错。
func removeIfExists(p string) error {
	if err := os.Remove(p); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("storejson: 清理 %s: %w", p, err)
	}
	return nil
}

func renameIfExists(from, to string) error {
	if _, err := os.Stat(from); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("storejson: 轮转 %s: %w", from, err)
	}
	if err := os.Rename(from, to); err != nil {
		return fmt.Errorf("storejson: 轮转 %s → %s: %w", from, to, err)
	}
	return nil
}

// Save 整树序列化落盘：先写 .tmp，再把现有文件轮成备份代，最后原子换入（D7/D254）。
// 轮转**只在现文件能完整解析时进行**（保存前完整性校验）——坏文件不许挤掉更旧的
// 可用备份。入树前做不变量自检，坏数据不落盘。
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
	data, err := json.MarshalIndent(encode(c), "", "  ")
	if err != nil {
		return fmt.Errorf("storejson: 序列化 %s: %w", c.ID, err)
	}
	dst := s.path(c.ID)
	tmp := dst + ".tmp"
	if err := os.WriteFile(tmp, append(data, '\n'), 0o644); err != nil {
		return fmt.Errorf("storejson: 写入 %s: %w", tmp, err)
	}
	if _, err := os.Stat(dst); err == nil && s.rotatable(dst) {
		if err := s.rotate(dst); err != nil {
			_ = os.Remove(tmp)
			return fmt.Errorf("storejson: 备份 %s: %w", dst, err)
		}
	}
	if err := os.Rename(tmp, dst); err != nil {
		return fmt.Errorf("storejson: 换入 %s: %w", dst, err)
	}
	return nil
}

// rotatable 现文件是否值得轮成备份：能读且能完整解析（不变量自检通过，D254）。
// 读不动或解不开（含旧格式）一律视为坏，跳过轮转。
func (s *Store) rotatable(dst string) bool {
	data, err := os.ReadFile(dst)
	if err != nil {
		return false
	}
	id := conversation.ID(strings.TrimSuffix(filepath.Base(dst), ".json"))
	_, err = decode(data, id)
	return err == nil
}

// Load 读取整树并做不变量自检；主文件缺失或损坏时按新到旧回退各代备份（D254）。
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
		// 主文件损坏：按新到旧回退各代备份，仍失败则报主文件的错。
		if c, ok := s.loadBackup(id); ok {
			return c, nil
		}
		return nil, fmt.Errorf("storejson: 读取 %s: %w", id, derr)
	}
	if !errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("storejson: 读取 %s: %w", id, err)
	}
	if c, ok := s.loadBackup(id); ok {
		return c, nil
	}
	return nil, fmt.Errorf("storejson: %w: %s", ErrNotFound, id)
}

// loadBackup 按新到旧尝试各代备份：第 1 代是历史名 `<id>.json.bak`，更旧的代为
// `<id>.json.bak.<i-1>`（D254）。命中第一个能完整解析的即返回。
func (s *Store) loadBackup(id conversation.ID) (*conversation.Conversation, bool) {
	dst := s.path(id)
	for i := 1; i <= s.keep; i++ {
		data, err := os.ReadFile(backupPath(dst, i))
		if err != nil {
			continue
		}
		if c, err := decode(data, id); err == nil {
			return c, true
		}
	}
	return nil, false
}

// List 列出全部会话摘要，按 UpdatedAt 降序（最新在前）。
// 单个损坏文件跳过不致命（由 Load 显式暴露），.bak/.tmp 不计入；
// M0 旧格式会话（D19）不静默跳过——聚合报因列出具体 ID，避免升级后旧数据隐形、
// NewSession 无提示地另起新会话。
func (s *Store) List(ctx context.Context) ([]port.ConversationSummary, error) {
	if err := checkCtx(ctx); err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return nil, fmt.Errorf("storejson: 列出 %s: %w", s.dir, err)
	}
	var out []port.ConversationSummary
	var legacy []string
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
			if errors.Is(err, ErrLegacyFormat) {
				legacy = append(legacy, string(id))
			}
			continue // 损坏文件跳过，不影响其余会话
		}
		out = append(out, port.ConversationSummary{
			ID:        c.ID,
			Title:     c.Title,
			MessageN:  c.NodeCount() - 1, // 不含 Root 节点
			UpdatedAt: c.UpdatedAt,
			Character: c.Character,
		})
	}
	if len(legacy) > 0 {
		return nil, fmt.Errorf("%w: %s——请删除对应 .json 文件后重试", ErrLegacyFormat, strings.Join(legacy, ", "))
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].UpdatedAt.Equal(out[j].UpdatedAt) {
			return out[i].UpdatedAt.After(out[j].UpdatedAt)
		}
		return out[i].ID > out[j].ID
	})
	return out, nil
}

// Remove 硬删除会话文件（连同各代 .bak 与 .tmp，含 keep 变化后遗留的更旧代）；
// 什么都不存在时返回 ErrNotFound。
func (s *Store) Remove(ctx context.Context, id conversation.ID) error {
	if err := checkCtx(ctx); err != nil {
		return err
	}
	if id == "" {
		return errors.New("storejson: 空会话 ID")
	}
	dst := s.path(id)
	paths := []string{dst, dst + ".tmp"}
	// 备份代按前缀扫目录收集：keep 调小后可能留有更高代的旧文件（D254）。
	prefix := filepath.Base(dst) + ".bak"
	if entries, err := os.ReadDir(s.dir); err == nil {
		for _, e := range entries {
			if !e.IsDir() && strings.HasPrefix(e.Name(), prefix) {
				paths = append(paths, filepath.Join(s.dir, e.Name()))
			}
		}
	}
	found := false
	for _, p := range paths {
		if _, err := os.Stat(p); err == nil {
			found = true
			break
		}
	}
	if !found {
		return fmt.Errorf("storejson: %w: %s", ErrNotFound, id)
	}
	for _, p := range paths {
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

// EncodeTree 把整棵树编码成落盘用的 JSON 字节（缩进形式，与磁盘上的会话文件同形）。
//
// 导出复用这一份形状而不是另造一套「归档专用」的树：同一棵树在磁盘上和在归档里长一样，
// 出问题时用编辑器打开任意一边都能读，也免得两份 wire 各自漂移（D242）。
func EncodeTree(c *conversation.Conversation) ([]byte, error) {
	if c == nil {
		return nil, errors.New("storejson: nil conversation")
	}
	if err := c.Validate(); err != nil {
		return nil, fmt.Errorf("storejson: 编码 %s 被拒（不变量校验失败）: %w", c.ID, err)
	}
	data, err := json.MarshalIndent(encode(c), "", "  ")
	if err != nil {
		return nil, fmt.Errorf("storejson: 序列化 %s: %w", c.ID, err)
	}
	return data, nil
}

// DecodeTree 解析整棵树（会话 ID 取自文件内容）并做完备的不变量自检。
//
// 与 Load 同一条路径：旧格式显式拒绝、残缺结构一律报因，不「尽力修复」。文件来自别处时
// 更是如此——外部文件是不可信输入（D242）。
//
// **不做输入上限**：整份数据已经在内存里，这个函数只负责解析与自检。限流是调用方的事——
// 从流里读的时候先卡字节数（`aqx` 侧是归档总字节 256 MB + `tree.json` 64 MB 两道），
// 别把未限流的外部数据直接喂进来。
func DecodeTree(data []byte) (*conversation.Conversation, error) {
	var w wireTree
	if err := json.Unmarshal(data, &w); err != nil {
		return nil, fmt.Errorf("解析 JSON: %w", err)
	}
	return decodeTree(w)
}

// wireTree 磁盘格式（本适配器自己的 DTO）。领域的三张结构表私有之后，这里必须自己定义
// 「磁盘上长什么样」：字段名、嵌套形状、兼容策略都归适配器。领域只提供
// Conversation.Snapshot / conversation.Restore 这一对搬运契约。
//
// 与改造前的形状保持一致（仍然有 nodes/children/revised_from 三个键），所以已有文件
// 与新文件可以互相读取——wire 格式变了才需要迁移，这次只是把定义搬到适配器里。
type wireTree struct {
	ID          conversation.ID                                     `json:"id"`
	Title       string                                              `json:"title"`
	Head        conversation.MessageID                              `json:"head"`
	Character   string                                              `json:"character,omitempty"`
	Tail        string                                              `json:"tail,omitempty"`
	Nodes       map[conversation.MessageID]wireNode                 `json:"nodes"`
	Children    map[conversation.MessageID][]conversation.MessageID `json:"children"`
	RevisedFrom map[conversation.MessageID]conversation.MessageID   `json:"revised_from"`
	CreatedAt   time.Time                                           `json:"created_at"`
	UpdatedAt   time.Time                                           `json:"updated_at"`
}

// wireNode 磁盘上的节点：D95 之前的遗留字段在此显式承接（见 legacyTree 的说明）。
//
// 普通反序列化会把未知字段**静默丢弃**——残留 tool 角色节点的文件还能靠自检失败暴露，
// 纯 tool_calls 文件则会「看起来正常」地丢数据。所以影子字段必须在这里有名有姓。
type wireNode struct {
	conversation.Message
	ToolCalls  []tool.Call  `json:"tool_calls,omitempty"`
	ToolResult *tool.Result `json:"tool_result,omitempty"`
}

// encode 领域快照 → wire 格式。
func encode(c *conversation.Conversation) wireTree {
	s := c.Snapshot()
	w := wireTree{
		ID:          s.ID,
		Title:       s.Title,
		Head:        s.Head,
		Character:   s.Character,
		Tail:        s.Tail,
		Nodes:       make(map[conversation.MessageID]wireNode, len(s.Nodes)),
		Children:    make(map[conversation.MessageID][]conversation.MessageID, len(s.Edges)),
		RevisedFrom: make(map[conversation.MessageID]conversation.MessageID, len(s.RevisedFrom)),
		CreatedAt:   s.CreatedAt,
		UpdatedAt:   s.UpdatedAt,
	}
	for _, m := range s.Nodes {
		w.Nodes[m.ID] = wireNode{Message: m}
	}
	for _, e := range s.Edges {
		w.Children[e.Parent] = e.Childs
	}
	for _, ed := range s.RevisedFrom {
		w.RevisedFrom[ed.New] = ed.Old
	}
	return w
}

// decode 反序列化 + 旧格式显式识别（D19/D95）+ ID 一致性检查 + 不变量自检。
func decode(data []byte, id conversation.ID) (*conversation.Conversation, error) {
	var w wireTree
	if err := json.Unmarshal(data, &w); err != nil {
		return nil, fmt.Errorf("解析 JSON: %w", err)
	}
	if w.ID != id {
		return nil, fmt.Errorf("文件内容属于会话 %q 而非 %q", w.ID, id)
	}
	return decodeTree(w)
}

// decodeTree wire 结构 → 领域树：旧格式显式识别（D19/D95）+ 不变量自检。
// Load 与 DecodeTree 共用这一份，识别与校验口径不会分叉。
func decodeTree(w wireTree) (*conversation.Conversation, error) {
	// D19 破坏性切换：M0 虚拟 Root 格式（无 Root 节点或 Head 为空）不兼容，明确报因引导重建。
	root, ok := w.Nodes[conversation.MessageID(w.ID)]
	if !ok || root.Role != conversation.RoleRoot || w.Head == "" {
		return nil, fmt.Errorf("%w：缺少会话根节点或已损坏，请删除该文件重建", ErrLegacyFormat)
	}
	// D95 破坏性切换：独立 tool 节点 / 遗留 tool_calls·tool_result 字段。
	for nid, n := range w.Nodes {
		if n.Message.Role == conversation.Role("tool") || len(n.ToolCalls) > 0 || n.ToolResult != nil {
			return nil, fmt.Errorf("%w：旧版工具节点格式：%q 及其节点无法加载，请删除或手工处置", ErrLegacyFormat, nid)
		}
	}
	s := conversation.Snapshot{
		ID:          w.ID,
		Title:       w.Title,
		Head:        w.Head,
		Character:   w.Character,
		Tail:        w.Tail,
		Nodes:       make([]conversation.Message, 0, len(w.Nodes)),
		Edges:       make([]conversation.SnapshotEdge, 0, len(w.Children)),
		RevisedFrom: make([]conversation.SnapshotEdition, 0, len(w.RevisedFrom)),
		CreatedAt:   w.CreatedAt,
		UpdatedAt:   w.UpdatedAt,
	}
	for _, n := range w.Nodes {
		s.Nodes = append(s.Nodes, n.Message)
	}
	for parent, kids := range w.Children {
		s.Edges = append(s.Edges, conversation.SnapshotEdge{Parent: parent, Childs: kids})
	}
	for newID, oldID := range w.RevisedFrom {
		s.RevisedFrom = append(s.RevisedFrom, conversation.SnapshotEdition{New: newID, Old: oldID})
	}
	c, err := conversation.Restore(s)
	if err != nil {
		return nil, fmt.Errorf("不变量校验失败: %w", err)
	}
	return c, nil
}

// checkCtx 校验 context 可用。
func checkCtx(ctx context.Context) error {
	if ctx == nil {
		return errors.New("storejson: nil context")
	}
	return ctx.Err()
}
