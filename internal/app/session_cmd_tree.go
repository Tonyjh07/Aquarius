package app

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/Tonyjh07/Aquarius/internal/domain/conversation"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

// execNew 实现 /new：新建会话。
func (s *Session) execNew(ctx context.Context, cmd port.Command) (string, error) {
	title := strings.TrimSpace(strings.Join(cmd.Args, " "))
	if title == "" {
		title = defaultTitle
	}
	c, err := s.newConversation(title)
	if err != nil {
		return "", err
	}
	if err := s.store.Save(ctx, c); err != nil {
		return "", fmt.Errorf("session: 新建会话: %w", err)
	}
	s.cur = c
	if err := s.emitClear(ctx); err != nil { // 新会话从空白开始（D75，修订 D72 后果④）
		return "", err
	}
	return fmt.Sprintf("已新建会话 %s", c.ID), nil
}

// execList 实现 /list：列出会话。
func (s *Session) execList(ctx context.Context, cmd port.Command) (string, error) {
	sums, err := s.store.List(ctx)
	if err != nil {
		return "", fmt.Errorf("session: 列出会话: %w", err)
	}
	if len(sums) == 0 {
		return "（暂无会话）", nil
	}
	var b strings.Builder
	for _, sm := range sums {
		mark := " "
		if sm.ID == s.cur.ID {
			mark = "*"
		}
		fmt.Fprintf(&b, "%s %s  %d条  %s\n", mark, sm.ID, sm.MessageN, sm.Title)
	}
	return strings.TrimRight(b.String(), "\n"), nil
}

// execSwitch 实现 /switch：切换会话（D75 清屏回放口径）。
func (s *Session) execSwitch(ctx context.Context, cmd port.Command) (string, error) {
	if len(cmd.Args) != 1 {
		return "", errors.New("用法: /switch <id前缀>（/list 查看可用会话，支持唯一前缀）")
	}
	id, err := s.resolveConversation(ctx, cmd.Args[0])
	if err != nil {
		return "", err
	}
	// 切前落盘当前会话：/edit 等只改内存的命令必须带上，否则切走即丢（D75）。
	if err := s.store.Save(ctx, s.cur); err != nil {
		return "", fmt.Errorf("session: 保存当前会话: %w", err)
	}
	if id != s.cur.ID {
		next, err := s.store.Load(ctx, id)
		if err != nil {
			return "", fmt.Errorf("session: 载入会话 %s: %w", id, err)
		}
		s.cur = next
	}
	// 清屏后回放目标会话可见历史（目标即当前时等价重载，D75）。
	if err := s.emitClear(ctx); err != nil {
		return "", err
	}
	if _, err := s.replayHistory(ctx, "已切换到"); err != nil {
		return "", err
	}
	return fmt.Sprintf("已切换到会话 %s", s.cur.ID), nil
}

// execTitle 实现 /title：查看或修改会话标题——`--id <前缀>` 改**非当前**会话（D120⑥）。
// 形态：/title [--id <id前缀>] [<标题>]；--id 仅在**首参位**识别（未知 "--x" 视为标题文本，
// 同 /edit 的 flag 口径）；缺省作用于当前会话。
func (s *Session) execTitle(ctx context.Context, cmd port.Command) (string, error) {
	args := cmd.Args
	target := s.cur
	if len(args) > 0 && args[0] == "--id" {
		if len(args) < 2 {
			return "", errors.New("用法: /title [--id <id前缀>] [<标题>]（--id 改非当前会话标题，/list 查看）")
		}
		id, err := s.resolveConversation(ctx, args[1])
		if err != nil {
			return "", err
		}
		args = args[2:]
		if id != s.cur.ID { // 目标非当前：载入副本改标题落盘，**不动 Head、不动 s.cur**
			c, err := s.store.Load(ctx, id)
			if err != nil {
				return "", fmt.Errorf("session: 载入会话 %s: %w", id, err)
			}
			target = c
		}
	}
	if len(args) == 0 {
		return fmt.Sprintf("%s 标题: %s", target.ID, target.Title), nil
	}
	title := strings.TrimSpace(strings.Join(args, " "))
	target.Title = title
	if err := s.store.Save(ctx, target); err != nil {
		return "", fmt.Errorf("session: 保存标题: %w", err)
	}
	if target.ID != s.cur.ID {
		return fmt.Sprintf("会话 %s 标题已改为: %s", target.ID, title), nil
	}
	return fmt.Sprintf("标题已改为: %s", title), nil
}

// execRmconv 实现 /rmconv：删除**整个会话**（区别于 /rm 删消息节点，D120⑥；二次确认）。
// 删当前会话时自动继任（最近更新的其余会话；一个不剩则新建空会话）并清屏回放（D75/D81 口径）。
func (s *Session) execRmconv(ctx context.Context, cmd port.Command) (string, error) {
	if len(cmd.Args) != 1 {
		return "", errors.New("用法: /rmconv <id前缀>（删除整个会话，二次确认；/list 查看可用会话）")
	}
	id, err := s.resolveConversation(ctx, cmd.Args[0])
	if err != nil {
		return "", err
	}
	if s.confirmer == nil {
		return "", errors.New("session: 未配置确认器（SessionDeps.Confirmer），/rmconv 需要二次确认")
	}
	title := s.conversationTitle(ctx, id)
	prompt := fmt.Sprintf("确认删除会话 %s（%s）？整会话连同其消息树一并删除，此操作不可恢复", id, title)
	ans, err := s.confirmer.Confirm(ctx, prompt)
	if err != nil {
		return "", fmt.Errorf("session: 确认删除会话: %w", err)
	}
	if !ans.Allow {
		return fmt.Sprintf("已取消删除会话 %s", id), nil
	}
	deletingCurrent := id == s.cur.ID
	if err := s.store.Remove(ctx, id); err != nil {
		return "", fmt.Errorf("session: 删除会话: %w", err)
	}
	if !deletingCurrent {
		return fmt.Sprintf("已删除会话 %s（%s）", id, title), nil
	}
	// 删的是当前会话：继任 + 落盘 + 清屏回放（转写区恒与 Head 一致，D75/D81）。
	next, err := s.successorConversation(ctx, id)
	if err != nil {
		return "", err
	}
	s.cur = next
	if err := s.store.Save(ctx, s.cur); err != nil {
		return "", fmt.Errorf("session: 保存继任会话: %w", err)
	}
	if err := s.emitClear(ctx); err != nil {
		return "", err
	}
	if _, err := s.replayHistory(ctx, "已切换到"); err != nil {
		return "", err
	}
	return fmt.Sprintf("已删除当前会话 %s（%s），已切换到 %s", id, title, s.cur.ID), nil
}

// conversationTitle 取会话标题（当前会话直读内存；否则查列表，取不到回退 ID）。
func (s *Session) conversationTitle(ctx context.Context, id conversation.ID) string {
	if id == s.cur.ID {
		return s.cur.Title
	}
	if sums, err := s.store.List(ctx); err == nil {
		for _, sm := range sums {
			if sm.ID == id {
				return sm.Title
			}
		}
	}
	return string(id)
}

// successorConversation 当前会话被删后的继任会话：最近更新的其余会话（List 按时间降序）；
// 一个不剩则新建空会话（persona 首节点）。
func (s *Session) successorConversation(ctx context.Context, removed conversation.ID) (*conversation.Conversation, error) {
	sums, err := s.store.List(ctx)
	if err != nil {
		return nil, fmt.Errorf("session: 列出会话: %w", err)
	}
	for _, sm := range sums {
		if sm.ID == removed {
			continue
		}
		c, err := s.store.Load(ctx, sm.ID)
		if err != nil {
			return nil, fmt.Errorf("session: 载入会话 %s: %w", sm.ID, err)
		}
		return c, nil
	}
	return s.newConversation(defaultTitle)
}

// execGoto 实现 /goto：移动 Head（D81 清屏回放口径）。
func (s *Session) execGoto(ctx context.Context, cmd port.Command) (string, error) {
	if len(cmd.Args) != 1 {
		return "", errors.New("用法: /goto <id>（id 可用 /branch 查看，支持唯一前缀）")
	}
	id, err := s.resolveNode(cmd.Args[0])
	if err != nil {
		return "", err
	}
	if err := s.cur.Checkout(id); err != nil {
		return "", fmt.Errorf("session: 移动 Head: %w", err)
	}
	if err := s.persist(ctx); err != nil {
		return "", err
	}
	// D81：移 Head 后清屏 + 回放新路径——转写区恒与 Head 一致（此前不回放是缺口：
	// 切分支后界面仍停在旧分支）。口径与 /switch（D75）一致。
	if err := s.emitClear(ctx); err != nil {
		return "", err
	}
	if _, err := s.replayHistory(ctx, "已切换分支至"); err != nil {
		return "", err
	}
	return fmt.Sprintf("Head → %s", id), nil
}

// execBranch 实现 /branch：分支视图。
func (s *Session) execBranch(ctx context.Context, cmd port.Command) (string, error) {
	if len(cmd.Args) > 1 {
		return "", errors.New("用法: /branch [id]（缺省取当前 Head）")
	}
	id := s.cur.Head
	if len(cmd.Args) == 1 {
		var err error
		if id, err = s.resolveNode(cmd.Args[0]); err != nil {
			return "", err
		}
	}
	self, ok := s.cur.Find(id)
	if !ok {
		return "", fmt.Errorf("session: %w", conversation.ErrNotFound)
	}
	// 展示自身 + 同级分叉（新旧版本对比）+ 下级（逐层下钻可发现深层 id）；
	// Root 的"同级"按 D15 即其孩子，改标"顶层消息"且不重复列出下级。
	isRoot := self.Role == conversation.RoleRoot
	var b strings.Builder
	fmt.Fprintf(&b, "当前: %s\n", branchLine(self, s.cur.Head, s.cur.RevisedFrom))
	sibsLabel := "同级分叉"
	if isRoot {
		sibsLabel = "顶层消息"
	}
	sibs := s.cur.Branches(id)
	if len(sibs) == 0 {
		fmt.Fprintf(&b, "%s: 无", sibsLabel)
	} else {
		fmt.Fprintf(&b, "%s（%d 条，不含自身）:", sibsLabel, len(sibs))
		for _, m := range sibs {
			b.WriteString("\n  " + branchLine(m, s.cur.Head, s.cur.RevisedFrom))
		}
	}
	if !isRoot {
		kids := s.cur.Children[id]
		if len(kids) == 0 {
			b.WriteString("\n下级: 无")
		} else {
			fmt.Fprintf(&b, "\n下级（%d 条）:", len(kids))
			for _, kid := range kids {
				if m, ok := s.cur.Find(kid); ok {
					b.WriteString("\n  " + branchLine(m, s.cur.Head, s.cur.RevisedFrom))
				}
			}
		}
	}
	return b.String(), nil
}

// execRm 实现 /rm：剪掉节点子树（二次确认）。
func (s *Session) execRm(ctx context.Context, cmd port.Command) (string, error) {
	if len(cmd.Args) != 1 {
		return "", errors.New("用法: /rm <id>（剪掉该节点及整棵子树，二次确认）")
	}
	id, err := s.resolveNode(cmd.Args[0])
	if err != nil {
		return "", err
	}
	m, _ := s.cur.Find(id)
	if m.Role == conversation.RoleRoot {
		return "", errors.New("session: root 即会话，不可删除（D19）") // 先于确认拒绝，不消费确认应答
	}
	if s.confirmer == nil {
		return "", errors.New("session: 未配置确认器（SessionDeps.Confirmer），/rm 需要二次确认")
	}
	size := subtreeSize(s.cur, id)
	prompt := fmt.Sprintf("确认删除 %s 及其子树（至少 %d 条节点）？此操作不可恢复", id, size)
	ans, err := s.confirmer.Confirm(ctx, prompt)
	if err != nil {
		return "", fmt.Errorf("session: 确认删除: %w", err)
	}
	if !ans.Allow {
		return fmt.Sprintf("已取消删除 %s", id), nil
	}
	before := len(s.cur.Nodes)
	if err := s.cur.Prune(id); err != nil {
		return "", fmt.Errorf("session: 删除: %w", err)
	}
	removed := before - len(s.cur.Nodes) // 实际删除数 = 子树大小（D95 后 Prune 恰删子树，无连带）
	if err := s.persist(ctx); err != nil {
		// 删除绝不"半生效"：落盘失败即回滚到最近一次成功保存的状态。
		if c, lerr := s.store.Load(ctx, s.cur.ID); lerr == nil {
			s.cur = c
			return "", fmt.Errorf("session: 落盘失败，删除已回滚（会话树未改动）: %w", err)
		}
		return "", fmt.Errorf("session: 落盘失败且无法回滚，删除仅存在于内存（下次成功保存会写盘，请谨慎继续）: %w", err)
	}
	return fmt.Sprintf("已删除 %s 子树（%d 条节点；Head → %s）", id, removed, s.cur.Head), nil
}

// subtreeSize 统计 id 及其整棵子树的节点数（/rm 确认提示用；树无环，遍历必终止）。
func subtreeSize(c *conversation.Conversation, id conversation.MessageID) int {
	n := 0
	var walk func(conversation.MessageID)
	walk = func(x conversation.MessageID) {
		n++
		for _, ch := range c.Children[x] {
			walk(ch)
		}
	}
	walk(id)
	return n
}

// branchLine 渲染一行分支摘要：<id> [角色] 内容预览 [← Head] [（修订自 <id>）]。
func branchLine(m conversation.Message, head conversation.MessageID, revisedFrom map[conversation.MessageID]conversation.MessageID) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s [%s] %s", m.ID, m.Role, nodeSummary(m))
	if m.ID == head {
		b.WriteString(" ← Head")
	}
	if old, ok := revisedFrom[m.ID]; ok {
		fmt.Fprintf(&b, "（修订自 %s）", old)
	}
	return b.String()
}

// nodeSummary 节点内容的单行预览（分支列表/回放树用）。
// 内容与工具输出均为不可信数据，只渲染不执行（DESIGN §9）。
// 预览取**正文口径**：跳过思考分片（D42——思考恒在正文之前，不筛会占满预览、掩盖版本差异；
// 实时与回放的完整展示仍含思考）。
func nodeSummary(m conversation.Message) string {
	const maxRunes = 40
	var parts []string
	for _, p := range m.Content {
		if p.Kind == conversation.PartThinking {
			continue
		}
		if s := strings.TrimSpace(p.Text); s != "" {
			parts = append(parts, s)
		}
	}
	s := strings.Join(parts, " ")
	// 工具分片预览（D95）：先列调用名；无正文时结果节点兜底显示输出/错误。
	if s == "" {
		var names []string
		for _, p := range m.Content {
			if p.Kind == conversation.PartTool && p.Tool != nil {
				names = append(names, p.Tool.Name)
			}
		}
		if len(names) > 0 {
			s = "（调用 " + strings.Join(names, ", ") + "）"
		}
	}
	if s == "" {
		for _, p := range m.Content {
			if p.Kind == conversation.PartTool && p.Tool != nil && p.Tool.Result != nil {
				if p.Tool.Result.OK {
					s = strings.TrimSpace(p.Tool.Result.Output)
				} else {
					s = "错误: " + p.Tool.Result.Err
				}
				break
			}
		}
	}
	if s == "" {
		return "（空）"
	}
	s = strings.Join(strings.Fields(s), " ") // 压成单行
	if r := []rune(s); len(r) > maxRunes {
		return string(r[:maxRunes]) + "…"
	}
	return s
}
