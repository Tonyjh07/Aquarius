package app

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/Tonyjh07/Aquarius/internal/domain/conversation"
	"github.com/Tonyjh07/Aquarius/internal/i18n"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

// execNew 实现 /new：新建会话。
func (s *Session) execNew(ctx context.Context, cmd port.Command) (string, error) {
	// 新建会话会切走当前会话：树编辑未退出时走同一个出口（D258）。
	if note, ok, err := s.exitTreeEditForSwitch(ctx); err != nil {
		return "", err
	} else if !ok {
		return note, nil
	}
	title := joinArgs(cmd.Args)
	if title == "" {
		title = defaultTitle()
	}
	c, err := s.newConversation(title)
	if err != nil {
		return "", err
	}
	if err := s.store.Save(ctx, c); err != nil {
		return "", wrapSession("err.new_conversation", err)
	}
	s.cur = c
	if err := s.emitClear(ctx); err != nil { // 新会话从空白开始（D75，修订 D72 后果④）
		return "", err
	}
	// 待发清单不随会话带走（D272）：明确丢弃 + 一行提示，与「不静默丢东西」的既有口径一致。
	if note := s.dropPendingNote(); note != "" {
		return i18n.Tf("tree.new.done", "id", c.ID) + note, nil
	}
	return i18n.Tf("tree.new.done", "id", c.ID), nil
}

// execList 实现 /list：列出会话。
func (s *Session) execList(ctx context.Context, cmd port.Command) (string, error) {
	sums, err := s.store.List(ctx)
	if err != nil {
		return "", wrapSession("err.list_conversations", err)
	}
	if len(sums) == 0 {
		return i18n.T("tree.list.empty"), nil
	}
	var b strings.Builder
	for _, sm := range sums {
		mark := " "
		if sm.ID == s.cur.ID {
			mark = "*"
		}
		fmt.Fprintf(&b, "%s %s  %s  %s\n", mark, sm.ID, i18n.Tf("tree.list.count", "n", sm.MessageN), sm.Title)
	}
	return strings.TrimRight(b.String(), "\n"), nil
}

// execSwitch 实现 /switch：切换会话（D75 清屏回放口径）。
func (s *Session) execSwitch(ctx context.Context, cmd port.Command) (string, error) {
	if len(cmd.Args) != 1 {
		return "", errors.New(i18n.T("usage.switch"))
	}
	id, err := s.resolveConversation(ctx, cmd.Args[0])
	if err != nil {
		return "", err
	}
	// 树编辑会话未退出：切走等于离开编辑——同一个出口（D258）。有改动就问保存/放弃/取消，
	// 取消就不切（否则「切一下看看」会把没保存的改动悬在半空）。
	if note, ok, err := s.exitTreeEditForSwitch(ctx); err != nil {
		return "", err
	} else if !ok {
		return note, nil
	}
	// 切前落盘当前会话：/edit 等只改内存的命令必须带上，否则切走即丢（D75）。
	if err := s.store.Save(ctx, s.cur); err != nil {
		return "", wrapSession("err.save_current_conversation", err)
	}
	if id != s.cur.ID {
		next, err := s.store.Load(ctx, id)
		if err != nil {
			return "", fmt.Errorf("session: %s: %w", i18n.Tf("err.load_conversation", "id", id), err)
		}
		s.cur = next
	}
	// 清屏后回放目标会话可见历史（目标即当前时等价重载，D75）。
	if err := s.emitClear(ctx); err != nil {
		return "", err
	}
	if _, err := s.replayHistory(ctx, "replay.verb.switched"); err != nil {
		return "", err
	}
	if note := s.dropPendingNote(); note != "" { // D272：切走即丢，留一行提示
		return i18n.Tf("tree.switch.done", "id", s.cur.ID) + note, nil
	}
	return i18n.Tf("tree.switch.done", "id", s.cur.ID), nil
}

// titleFlags /title 认识的 flag；只认一个（第二次出现归标题文本，旧口径）。
var titleFlags = []flagDecl{{name: "--id", value: true}}

// titleUsage /title 的用法文案（缺 id、缺参共用一份）。
func titleUsage() string { return i18n.T("usage.title") }

// execTitle 实现 /title：查看或修改会话标题——`--id <前缀>` 改**非当前**会话（D120⑥）。
// 形态：/title [--id <id前缀>] [<标题>]；--id 仅在**首参位**识别（未知 "--x" 视为标题文本，
// 同 /edit 的 flag 口径）；缺省作用于当前会话。
func (s *Session) execTitle(ctx context.Context, cmd port.Command) (string, error) {
	target := s.cur
	flags, rest, err := scanFlags(cmd.Args, flagScan{pos: flagsLeading, limit: 1}, titleFlags)
	if err != nil {
		return "", errors.New(titleUsage())
	}
	if len(flags) > 0 {
		id, err := s.resolveConversation(ctx, flags[0].value)
		if err != nil {
			return "", err
		}
		if id != s.cur.ID { // 目标非当前：载入副本改标题落盘，**不动 Head、不动 s.cur**
			c, err := s.store.Load(ctx, id)
			if err != nil {
				return "", fmt.Errorf("session: %s: %w", i18n.Tf("err.load_conversation", "id", id), err)
			}
			target = c
		}
	}
	if len(rest) == 0 {
		return i18n.Tf("tree.title.show", "id", target.ID, "title", target.Title), nil
	}
	title := joinArgs(rest)
	target.Title = title
	if err := s.store.Save(ctx, target); err != nil {
		return "", wrapSession("err.save_title", err)
	}
	if target.ID != s.cur.ID {
		return i18n.Tf("tree.title.set_other", "id", target.ID, "title", title), nil
	}
	return i18n.Tf("tree.title.set", "title", title), nil
}

// execRmconv 实现 /rmconv：删除**整个会话**（区别于 /rm 删消息节点，D120⑥；二次确认）。
//
// 支持多个 ID（`/rmconv <id> [<id>…]`，各支持唯一前缀）：**先全部解析、再一次确认、最后才动库**
// ——有一个解析不出就一个都不删（半截删除比不删更坏：用户不知道哪几个还在）。删到当前会话时
// 自动继任（最近更新的其余会话、且跳过本批要删的；一个不剩则新建空会话）并清屏回放（D75/D81）。
// 单个 ID 的用法与回执与今天逐字相同。
func (s *Session) execRmconv(ctx context.Context, cmd port.Command) (string, error) {
	if len(cmd.Args) == 0 {
		return "", errors.New(i18n.T("usage.rmconv"))
	}
	targets := make([]conversation.ID, 0, len(cmd.Args))
	seen := make(map[conversation.ID]bool, len(cmd.Args))
	for _, raw := range cmd.Args {
		id, err := s.resolveConversation(ctx, raw)
		if err != nil {
			return "", err
		}
		if seen[id] {
			continue
		}
		seen[id] = true
		targets = append(targets, id)
	}
	if s.confirmer == nil {
		return "", errors.New("session: " + i18n.T("tree.rmconv.no_confirmer"))
	}
	titles := make([]string, len(targets))
	for i, id := range targets {
		titles[i] = s.conversationTitle(ctx, id)
	}
	prompt := rmconvPrompt(targets, titles)
	ans, err := s.confirmer.Confirm(ctx, port.ConfirmRequest{Origin: port.OriginCommand, Prompt: prompt})
	if err != nil {
		return "", wrapSession("err.confirm_delete_conversation", err)
	}
	if !ans.Allow {
		if len(targets) == 1 {
			return i18n.Tf("tree.rmconv.canceled", "id", targets[0]), nil
		}
		return i18n.Tf("tree.rmconv.canceled_many", "n", len(targets)), nil
	}
	deletingCurrent := false
	for _, id := range targets {
		if id == s.cur.ID {
			deletingCurrent = true
		}
		// 删会话是 watcher 唯一该撤销的时刻（另一处是关停）：会话没了，报告无处可落。
		s.cancelWatchersFor(id)
		if err := s.store.Remove(ctx, id); err != nil {
			return "", wrapSession("err.delete_conversation", err)
		}
	}
	if !deletingCurrent {
		if len(targets) == 1 {
			return i18n.Tf("tree.rmconv.deleted", "id", targets[0], "title", titles[0]), nil
		}
		return i18n.Tf("tree.rmconv.deleted_many", "n", len(targets), "titles", targetTitles(titles)), nil
	}
	// 删的是当前会话：继任 + 落盘 + 清屏回放（转写区恒与 Head 一致，D75/D81）。
	next, err := s.successorConversation(ctx, seen)
	if err != nil {
		return "", err
	}
	s.cur = next
	if err := s.store.Save(ctx, s.cur); err != nil {
		return "", wrapSession("err.save_successor", err)
	}
	if err := s.emitClear(ctx); err != nil {
		return "", err
	}
	if _, err := s.replayHistory(ctx, "replay.verb.switched"); err != nil {
		return "", err
	}
	suffix := s.dropPendingNote() // D272：删掉当前会话 = 切走，待发随之丢弃
	if len(targets) == 1 {
		return i18n.Tf("tree.rmconv.deleted_current", "id", targets[0], "title", titles[0], "next", s.cur.ID) + suffix, nil
	}
	return i18n.Tf("tree.rmconv.deleted_current_many",
		"n", len(targets), "titles", targetTitles(titles), "next", s.cur.ID) + suffix, nil
}

// rmconvPrompt 删除确认句：单个与多批两种说法，多批时列标题让用户确认删的是哪几个。
func rmconvPrompt(ids []conversation.ID, titles []string) string {
	if len(ids) == 1 {
		return i18n.Tf("tree.rmconv.prompt", "id", ids[0], "title", titles[0])
	}
	var b strings.Builder
	b.WriteString(i18n.Tf("tree.rmconv.prompt_many", "n", len(ids)))
	for i, id := range ids {
		fmt.Fprintf(&b, "\n%s", i18n.Tf("tree.rmconv.prompt_item", "id", id, "title", titles[i]))
	}
	return b.String()
}

// targetTitles 批量删除回执里的标题清单（最多列 3 个，其余记数）。
func targetTitles(titles []string) string {
	return summarizeTitles(titles)
}

// summarizeTitles 标题清单的用户面写法（最多列 3 个，其余记数）。
func summarizeTitles(titles []string) string {
	const show = 3
	sep := i18n.T("tree.titles.sep")
	if len(titles) <= show {
		return strings.Join(titles, sep)
	}
	return i18n.Tf("tree.titles.more", "names", strings.Join(titles[:show], sep), "n", len(titles))
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
//
// skip 是本批要删的其余会话：不跳过它们就会继任到一个刚被删掉的 ID（下一次保存又把它写回来，
// 还带着旧内容），用户看到的是「删了又回来」。
func (s *Session) successorConversation(ctx context.Context, skip map[conversation.ID]bool) (*conversation.Conversation, error) {
	sums, err := s.store.List(ctx)
	if err != nil {
		return nil, wrapSession("err.list_conversations", err)
	}
	for _, sm := range sums {
		if skip[sm.ID] {
			continue
		}
		c, err := s.store.Load(ctx, sm.ID)
		if err != nil {
			return nil, fmt.Errorf("session: %s: %w", i18n.Tf("err.load_conversation", "id", sm.ID), err)
		}
		return c, nil
	}
	return s.newConversation(defaultTitle())
}

// execGoto 实现 /goto：移动 Head（D81 清屏回放口径）。
func (s *Session) execGoto(ctx context.Context, cmd port.Command) (string, error) {
	if len(cmd.Args) != 1 {
		return "", errors.New(i18n.T("usage.goto"))
	}
	id, err := s.resolveNode(cmd.Args[0])
	if err != nil {
		return "", err
	}
	if err := s.cur.Checkout(id); err != nil {
		return "", wrapSession("err.move_head", err)
	}
	if err := s.persist(ctx); err != nil {
		return "", err
	}
	// D81：移 Head 后清屏 + 回放新路径——转写区恒与 Head 一致（此前不回放是缺口：
	// 切分支后界面仍停在旧分支）。口径与 /switch（D75）一致。
	if err := s.emitClear(ctx); err != nil {
		return "", err
	}
	if _, err := s.replayHistory(ctx, "replay.verb.branch_switched"); err != nil {
		return "", err
	}
	return i18n.Tf("tree.goto.done", "id", id), nil
}

// execBranch 实现 /branch：分支视图。
func (s *Session) execBranch(ctx context.Context, cmd port.Command) (string, error) {
	if len(cmd.Args) > 1 {
		return "", errors.New(i18n.T("usage.branch"))
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
	fmt.Fprintf(&b, "%s\n", i18n.Tf("tree.branch.current", "line", branchLine(s.cur, self)))
	sibsLabel := i18n.T("tree.branch.siblings")
	if isRoot {
		sibsLabel = i18n.T("tree.branch.top")
	}
	sibs := s.cur.Branches(id)
	if len(sibs) == 0 {
		b.WriteString(i18n.Tf("tree.branch.none", "label", sibsLabel))
	} else {
		b.WriteString(i18n.Tf("tree.branch.siblings_count", "label", sibsLabel, "n", len(sibs)))
		for _, m := range sibs {
			b.WriteString("\n  " + branchLine(s.cur, m))
		}
	}
	if !isRoot {
		kids := s.cur.ChildIDs(id)
		if len(kids) == 0 {
			b.WriteString("\n" + i18n.T("tree.branch.children_none"))
		} else {
			b.WriteString("\n" + i18n.Tf("tree.branch.children_count", "n", len(kids)))
			for _, kid := range kids {
				if m, ok := s.cur.Find(kid); ok {
					b.WriteString("\n  " + branchLine(s.cur, m))
				}
			}
		}
	}
	return b.String(), nil
}

// execRm 实现 /rm：剪掉节点子树（二次确认）。
func (s *Session) execRm(ctx context.Context, cmd port.Command) (string, error) {
	if len(cmd.Args) != 1 {
		return "", errors.New(i18n.T("usage.rm"))
	}
	id, err := s.resolveNode(cmd.Args[0])
	if err != nil {
		return "", err
	}
	m, _ := s.cur.Find(id)
	if m.Role == conversation.RoleRoot {
		return "", errors.New("session: " + i18n.T("tree.rm.root")) // 先于确认拒绝，不消费确认应答
	}
	if s.confirmer == nil {
		return "", errors.New("session: " + i18n.T("tree.rm.no_confirmer"))
	}
	size := s.cur.SubtreeSize(id)
	prompt := i18n.Tf("tree.rm.prompt", "id", id, "n", size)
	// 剪除会连带清掉指向子树内旧节点的溯源边：新版本在子树外时，它会失去「修订自谁」的
	// 记录（图上版本虚线退回顺接实线）。删之前说明，不让人删完才发现。
	droppedEditions := s.cur.DroppedEditions(id)
	if droppedEditions > 0 {
		prompt += i18n.Tf("tree.rm.prompt_editions", "n", droppedEditions)
	}
	ans, err := s.confirmer.Confirm(ctx, port.ConfirmRequest{Origin: port.OriginCommand, Prompt: prompt})
	if err != nil {
		return "", wrapSession("err.confirm_delete", err)
	}
	if !ans.Allow {
		return i18n.Tf("tree.rm.canceled", "id", id), nil
	}
	before := s.cur.NodeCount()
	if err := s.cur.Prune(id); err != nil {
		return "", wrapSession("err.delete", err)
	}
	removed := before - s.cur.NodeCount() // 实际删除数 = 子树大小（D95 后 Prune 恰删子树，无连带）
	if err := s.persist(ctx); err != nil {
		// 删除绝不"半生效"：落盘失败即回滚到最近一次成功保存的状态。
		if c, lerr := s.store.Load(ctx, s.cur.ID); lerr == nil {
			s.cur = c
			return "", wrapSession("err.rm_persist_rollback", err)
		}
		return "", wrapSession("err.rm_persist_stuck", err)
	}
	line := i18n.Tf("tree.rm.done", "id", id, "n", removed, "head", s.cur.Head)
	if droppedEditions > 0 {
		line += i18n.Tf("tree.rm.done_editions", "n", droppedEditions)
	}
	return line, nil
}

// branchLine 渲染一行分支摘要：<id> [角色] 内容预览 [← Head] [（修订自 <id>）]。
// 版本链查询走 domain 的 Edition 访问器，不直接读 map。
func branchLine(c *conversation.Conversation, m conversation.Message) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s [%s] %s", m.ID, m.Role, nodeSummary(m))
	if m.ID == c.Head {
		b.WriteString(" " + i18n.T("tree.branch.head"))
	}
	if old, ok := c.Edition(m.ID); ok {
		fmt.Fprintf(&b, "%s", i18n.Tf("tree.branch.revised", "id", old))
	}
	return b.String()
}

// nodeSummary 节点内容的单行预览（分支列表/回放树用）。
// 内容与工具输出均为不可信数据，只渲染不执行（§9）。
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
			s = i18n.Tf("tree.node.tool_call", "names", strings.Join(names, ", "))
		}
	}
	if s == "" {
		for _, p := range m.Content {
			if p.Kind == conversation.PartTool && p.Tool != nil && p.Tool.Result != nil {
				if p.Tool.Result.OK {
					s = strings.TrimSpace(p.Tool.Result.Output)
				} else {
					s = i18n.Tf("tree.node.error", "text", p.Tool.Result.Err)
				}
				break
			}
		}
	}
	if s == "" {
		return i18n.T("tree.node.empty")
	}
	s = strings.Join(strings.Fields(s), " ") // 压成单行
	if r := []rune(s); len(r) > maxRunes {
		return string(r[:maxRunes]) + "…"
	}
	return s
}
