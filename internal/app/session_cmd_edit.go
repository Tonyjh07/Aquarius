package app

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/Tonyjh07/Aquarius/internal/domain/conversation"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

// execEdit 实现 /edit：Revise 修订节点（D92/D93 语义见实现内注释）。
func (s *Session) execEdit(ctx context.Context, cmd port.Command) (string, error) {
	target, mode, part, text, err := parseEditArgs(cmd.Args)
	if err != nil {
		return "", err
	}
	id, err := s.resolveNode(target)
	if err != nil {
		return "", err
	}
	parts, err := s.editParts(id, text, part)
	if err != nil {
		return "", err
	}
	m, err := s.cur.Revise(id, parts, mode)
	if err != nil {
		return "", fmt.Errorf("session: 修订: %w", err)
	}
	if err := s.persist(ctx); err != nil {
		return "", err
	}
	// D92：Revise 已移 Head（Fresh 恒移、Carry 例外）——清屏回放新路径，
	// 转写区恒与 Head 一致（D81 口径，/goto 同款；此前只入树不回放是缺口）。
	if err := s.emitClear(ctx); err != nil {
		return "", err
	}
	if _, err := s.replayHistory(ctx, "已修订"); err != nil {
		return "", err
	}
	note := "旧分支保留"
	switch mode {
	case conversation.Carry:
		note = "后续历史已转移"
	case conversation.Clone:
		note = "后续历史已复制"
	}
	// D93：编辑用户消息（Fresh 分叉）= 改写并重新生成——Head 在无回答的新节点上，
	// 直接重跑一轮（Carry 是原地改写历史，Head 随转移子树；assistant/system 修订
	// 不触发生成）。
	if mode == conversation.Fresh && m.Role == conversation.RoleUser {
		if err := s.runTurn(ctx); err != nil {
			return "", err
		}
		note += "，已重新生成回答"
	}
	return fmt.Sprintf("已修订 %s → %s（%s，%s；Head → %s）", id, m.ID, mode, note, s.cur.Head), nil
}

// execRegen 实现 /regen：重新生成回答（D93 分叉口径）。
func (s *Session) execRegen(ctx context.Context, cmd port.Command) (string, error) {
	if len(cmd.Args) != 1 {
		return "", errors.New("用法: /regen <id>（id 可为用户或助手消息，支持唯一前缀）")
	}
	id, err := s.resolveNode(cmd.Args[0])
	if err != nil {
		return "", err
	}
	um, err := s.upstreamUser(id)
	if err != nil {
		return "", err
	}
	forked := true
	if len(s.cur.Children[um.ID]) == 0 {
		// D93：上游用户消息尚无任何回答（编辑 Fresh 分叉后/空轮次）——Revise 只会
		// 造出同文本的冗余兄弟分支；Head 不在其上则移过去，直接生成本回答。
		forked = false
		if s.cur.Head != um.ID {
			if err := s.cur.Checkout(um.ID); err != nil {
				return "", fmt.Errorf("session: 移动 Head: %w", err)
			}
			if err := s.persist(ctx); err != nil {
				return "", err
			}
		}
	} else {
		// 已有回答：原 Parts 原样重发（Revise 内部 cloneParts，多模态分片保真）；
		// Fresh 开同父兄弟节点，旧回答子树原样留作历史分支。
		if _, err := s.cur.Revise(um.ID, um.Content, conversation.Fresh); err != nil {
			return "", fmt.Errorf("session: 重发用户消息: %w", err)
		}
		if err := s.persist(ctx); err != nil {
			return "", err
		}
	}
	// Head 已就位：清屏回放（D81 口径）后重跑一轮生成。
	if err := s.emitClear(ctx); err != nil {
		return "", err
	}
	if _, err := s.replayHistory(ctx, "已重新生成"); err != nil {
		return "", err
	}
	if err := s.runTurn(ctx); err != nil {
		return "", err
	}
	if forked {
		return fmt.Sprintf("已重新生成（旧回答保留为分支；Head → %s）", s.cur.Head), nil
	}
	return fmt.Sprintf("已重新生成（Head → %s）", s.cur.Head), nil
}

// resolveNode 把用户给出的节点标识解析为树中节点：先精确匹配，再按唯一前缀匹配
// （Root 节点 ID = 会话 ID，/goto <会话ID> 即回根）；前缀命中多个时报歧义。
func (s *Session) resolveNode(arg string) (conversation.MessageID, error) {
	arg = strings.TrimSpace(arg)
	if arg == "" {
		return "", errors.New("缺少节点 id")
	}
	if _, ok := s.cur.Find(conversation.MessageID(arg)); ok {
		return conversation.MessageID(arg), nil
	}
	var hits []conversation.MessageID
	for id := range s.cur.Nodes {
		if strings.HasPrefix(string(id), arg) {
			hits = append(hits, id)
		}
	}
	switch len(hits) {
	case 0:
		return "", fmt.Errorf("会话树中没有节点 %q（/branch 可查看同级 id）", arg)
	case 1:
		return hits[0], nil
	default:
		sort.Slice(hits, func(i, j int) bool { return hits[i] < hits[j] })
		ids := make([]string, len(hits))
		for i, h := range hits {
			ids[i] = string(h)
		}
		return "", fmt.Errorf("节点标识 %q 有歧义（命中 %d 个: %s），请加长前缀", arg, len(hits), strings.Join(ids, ", "))
	}
}

// upstreamUser 沿 Parent 回溯找 id 上游最近一条 user 消息（D92 /regen：
// 重发对象——id 本身是 user 消息时即其自身）；到 Root（Parent == ""）仍未遇则报错。
func (s *Session) upstreamUser(id conversation.MessageID) (conversation.Message, error) {
	for cur := id; cur != ""; {
		m, ok := s.cur.Find(cur)
		if !ok {
			return conversation.Message{}, fmt.Errorf("session: 节点 %s 不在会话树中", cur)
		}
		if m.Role == conversation.RoleUser {
			return m, nil
		}
		cur = m.Parent
	}
	return conversation.Message{}, errors.New("session: 该消息上游没有用户消息，无法重新生成")
}

// editParts 构造 /edit 的修订内容（D97⑤）：user 节点保留非文本分片（附件 Ref 原序
// 原值），仅把文本分片并为一替换为新文本（新文本落在首个文本分片的位置）——修复
// "改文字把附件留在旧分支"；其余角色整节点替换（D95 后果⑥：Turn 的 thinking/tool
// 分片不可被编辑文本撕裂）。目标不在树中时返回纯文本（Revise 会再报错）。
// editParts 修订内容分片（D107）：非正文分片（思考/工具/附件 Ref）原序原值保留，只动正文。
//   - part >= 0：精确替换第 part 个正文分片（0 起，D107②——GUI 编辑气泡带段序，多正文段
//     Turn 只动被编辑的那段）；越界报错。
//   - part < 0：全部正文分片并为一条新文本、置于首个正文分片位置（typed 路径无段上下文；
//     user 附件保真规则推广到全部角色，D107③——取代 D95 后果⑥ 的整节点替换）。
//
// 节点不存在时回退单文本分片（Revise 随后报节点不存在）。
func (s *Session) editParts(id conversation.MessageID, text string, part int) ([]conversation.Part, error) {
	fresh := conversation.Part{Kind: conversation.PartText, Text: text}
	old, ok := s.cur.Find(id)
	if !ok {
		return []conversation.Part{fresh}, nil
	}
	if part >= 0 {
		n := -1
		out := make([]conversation.Part, 0, len(old.Content))
		for _, p := range old.Content {
			if p.Kind == conversation.PartText {
				n++
				if n == part {
					out = append(out, fresh)
					continue
				}
			}
			out = append(out, p)
		}
		if n < part {
			return nil, fmt.Errorf("session: 正文分片 %d 越界（该节点共 %d 个正文分片）", part, n+1)
		}
		return out, nil
	}
	parts := make([]conversation.Part, 0, len(old.Content)+1)
	inserted := false
	for _, p := range old.Content {
		if p.Kind == conversation.PartText {
			if !inserted {
				parts = append(parts, fresh)
				inserted = true
			}
			continue
		}
		parts = append(parts, p)
	}
	if !inserted {
		parts = append(parts, fresh)
	}
	return parts, nil
}

// resolveConversation 按会话 ID（精确或唯一前缀）解析会话（/memory [会话id] 用）；
// 前缀命中多个时报歧义（会话列表按时间降序，命中序确定）。
func (s *Session) resolveConversation(ctx context.Context, arg string) (conversation.ID, error) {
	arg = strings.TrimSpace(arg)
	if arg == "" {
		return s.cur.ID, nil
	}
	list, err := s.store.List(ctx)
	if err != nil {
		return "", fmt.Errorf("session: 列出会话: %w", err)
	}
	var hits []conversation.ID
	for _, sm := range list {
		if string(sm.ID) == arg {
			return sm.ID, nil
		}
		if strings.HasPrefix(string(sm.ID), arg) {
			hits = append(hits, sm.ID)
		}
	}
	switch len(hits) {
	case 0:
		return "", fmt.Errorf("没有会话 %q（/list 查看可用会话）", arg)
	case 1:
		return hits[0], nil
	default:
		ids := make([]string, len(hits))
		for i, h := range hits {
			ids[i] = string(h)
		}
		return "", fmt.Errorf("会话标识 %q 有歧义（命中 %d 个: %s），请加长前缀", arg, len(hits), strings.Join(ids, ", "))
	}
}

// parseEditArgs 解析 /edit <id> [--keep|--copy] [--part N] <文本>：flag 只在紧跟 id 的
// 位置识别（已知 flag 逐个消费，未知 "--x" 视为文本起点——字面 "--keep" 原样保留的旧
// 口径不变），返回（目标, 模式, 正文分片序号, 文本）。--part N = 只替换第 N 个正文分片
// （0 起，D107②）；缺省 -1 = 全部正文并为一条（D107③）。GUI 结构化提交复用同一 token
// 序列（D97①/D107②）：Args=[id, flag?, ("--part", N)?, 文本]。
func parseEditArgs(args []string) (string, conversation.KeepMode, int, string, error) {
	usage := errors.New("用法: /edit <id> [--keep|--copy] [--part N] <文本>（缺省 Fresh 开新分支；--keep 转移后续历史；--copy 复制后续历史；--part N 只改第 N 个正文分片，0 起）")
	if len(args) == 0 {
		return "", conversation.Fresh, -1, "", usage
	}
	if strings.HasPrefix(args[0], "--") {
		return "", conversation.Fresh, -1, "", usage // flag 在 id 前：按用法拒绝
	}
	mode, part := conversation.Fresh, -1
	id, rest := args[0], args[1:]
	for len(rest) > 0 && strings.HasPrefix(rest[0], "--") {
		switch rest[0] {
		case "--keep":
			mode = conversation.Carry
			rest = rest[1:]
		case "--copy":
			mode = conversation.Clone
			rest = rest[1:]
		case "--part":
			if len(rest) < 2 {
				return "", conversation.Fresh, -1, "", usage // --part 缺序号
			}
			n, err := strconv.Atoi(rest[1])
			if err != nil || n < 0 {
				return "", conversation.Fresh, -1, "", usage // --part 序号非法
			}
			part = n
			rest = rest[2:]
		default:
			// 未知 "--x" = 文本以它开头（旧口径：只有已知 flag 被消费）。
			goto text
		}
	}
text:
	if len(rest) < 1 {
		return "", mode, part, "", usage
	}
	text := strings.TrimSpace(strings.Join(rest, " "))
	if text == "" {
		return "", mode, part, "", errors.New("用法: /edit <id> [--keep|--copy] [--part N] <文本>（修订文本不可为空）")
	}
	return id, mode, part, text, nil
}
