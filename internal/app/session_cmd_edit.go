package app

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/Tonyjh07/Aquarius/internal/domain/conversation"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

// editOptions /edit 的参数（D188 起含附件增删）：flag 区紧跟 id，文本可空（有附件操作时
// = 只改附件不动正文）。
type editOptions struct {
	target  string
	mode    conversation.KeepMode
	part    int               // 正文分片序号（-1 = 全部正文并为一条，D107③）
	text    string            // 正文新文本（空 = 有附件操作时保持原正文）
	attach  []port.Attachment // 新增附件（Path 或已入库 Blob），按出现序追加到末尾
	drop    []int             // --drop 的附件序号（节点内附件分片的 0 基出现序）
	dropAll bool              // --drop-all
}

// hasAttachOp 是否带附件操作（决定空文本是否合法，以及是否走"只改附件"分支）。
func (o editOptions) hasAttachOp() bool {
	return len(o.attach) > 0 || len(o.drop) > 0 || o.dropAll
}

// execEdit 实现 /edit：Revise 修订节点（D92/D93 语义见实现内注释）。
func (s *Session) execEdit(ctx context.Context, cmd port.Command) (string, error) {
	// D188：结构化附件（GUI 粘贴图片等无路径的新增项）并进同一 flag 区——须在解析前
	// 并入，否则「只贴一张图、正文留空」会被当成"空文本"拒绝。
	opt, err := parseEditArgsWith(cmd.Args, cmd.Attachments)
	if err != nil {
		return "", err
	}
	id, err := s.resolveNode(opt.target)
	if err != nil {
		return "", err
	}
	// D188 口径四：附件增删只对用户消息开放——附件语义只属于用户输入。
	if opt.hasAttachOp() {
		old, ok := s.cur.Find(id)
		if ok && old.Role != conversation.RoleUser {
			return "", errors.New("session: 附件增删只适用于用户消息")
		}
	}
	parts, note, err := s.editPartsWithAttachments(ctx, id, opt)
	if err != nil {
		return "", err
	}
	m, err := s.cur.Revise(id, parts, opt.mode)
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
	desc := "旧分支保留"
	switch opt.mode {
	case conversation.Carry:
		desc = "后续历史已转移"
	case conversation.Clone:
		desc = "后续历史已复制"
	}
	if note != "" {
		desc += "，" + note
	}
	// D93：编辑用户消息（Fresh 分叉）= 改写并重新生成——Head 在无回答的新节点上，
	// 直接重跑一轮（Carry 是原地改写历史，Head 随转移子树；assistant/system 修订
	// 不触发生成）。
	if opt.mode == conversation.Fresh && m.Role == conversation.RoleUser {
		if err := s.runTurn(ctx); err != nil {
			return "", err
		}
		desc += "，已重新生成回答"
	}
	return fmt.Sprintf("已修订 %s → %s（%s，%s；Head → %s）", id, m.ID, opt.mode, desc, s.cur.Head), nil
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
	if len(s.cur.ChildIDs(um.ID)) == 0 {
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
// 匹配策略与任务/观察器/会话共用 port.MatchPrefix（§2.1），这里只负责中文措辞。
func (s *Session) resolveNode(arg string) (conversation.MessageID, error) {
	arg = strings.TrimSpace(arg)
	if arg == "" {
		return "", errors.New("缺少节点 id")
	}
	id, hits, ok := port.MatchPrefix(s.cur.IDs(), arg) // 已按 ID 升序，歧义提示可读
	switch {
	case ok:
		return id, nil
	case len(hits) == 0:
		return "", fmt.Errorf("会话树中没有节点 %q（/branch 可查看同级 id）", arg)
	default:
		return "", fmt.Errorf("节点标识 %q 有歧义（命中 %d 个: %s），请加长前缀", arg, len(hits), strings.Join(hits, ", "))
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

// editPartsWithAttachments 在 editParts 的基础上叠加附件增删（D188）：
//   - 文本为空且有附件操作 = 只改附件不动正文（对 user 节点取原分片，对其它角色报错，
//     已在 execEdit 拦住）；
//   - `--drop` 按节点内附件分片出现序删除（只影响新节点，不动附件库）；
//   - `--drop-all` 删掉全部附件分片；
//   - `--attach` 逐项经摄取管线生成新分片，按出现序**追加到末尾**；
//   - 改完至少剩一个分片（正文或附件），否则报错。
//
// 返回的第二值是给用户看的回执片段（无附件操作时为空）。
func (s *Session) editPartsWithAttachments(ctx context.Context, id conversation.MessageID, opt editOptions) ([]conversation.Part, string, error) {
	if !opt.hasAttachOp() {
		parts, err := s.editParts(id, opt.text, opt.part)
		return parts, "", err
	}
	old, ok := s.cur.Find(id)
	if !ok {
		return nil, "", fmt.Errorf("session: 节点 %s 不在会话树中", id)
	}
	if old.Role != conversation.RoleUser {
		return nil, "", errors.New("session: 附件增删只适用于用户消息")
	}

	var (
		parts []conversation.Part
		err   error
	)
	if strings.TrimSpace(opt.text) == "" {
		parts = append([]conversation.Part(nil), old.Content...) // 只改附件：正文原样
	} else {
		if parts, err = s.editParts(id, opt.text, opt.part); err != nil {
			return nil, "", err
		}
	}

	// 删除：序号口径 = 节点内附件分片的出现序；先统一去重，再一次性剔除。
	drop := map[int]bool{}
	for _, n := range opt.drop {
		drop[n] = true
	}
	total := 0
	for _, p := range old.Content {
		if isAttachmentPart(p.Kind) {
			total++
		}
	}
	for n := range drop {
		if n < 0 || n >= total {
			return nil, "", fmt.Errorf("session: 附件序号 %d 越界（该节点共 %d 个附件）", n, total)
		}
	}
	dropped := 0
	if opt.dropAll || len(drop) > 0 {
		out := make([]conversation.Part, 0, len(parts))
		idx := 0
		for _, p := range parts {
			if isAttachmentPart(p.Kind) {
				if opt.dropAll || drop[idx] {
					dropped++
					idx++
					continue
				}
				idx++
			}
			out = append(out, p)
		}
		parts = out
	}

	// 新增：走既有摄取管线（附件先入库再分片），失败即整体失败——编辑是显式动作，
	// 这里不做"部分成功"（与发消息时的宽容口径不同：那时用户已经写完问题）。
	added := 0
	if len(opt.attach) > 0 {
		raw := port.RawInput{Kind: "file"}
		for _, att := range opt.attach {
			if att.Blob != nil {
				raw.Attachments = append(raw.Attachments, att) // 已入库（GUI 贴图）
				continue
			}
			p := strings.TrimSpace(att.Path)
			if p == "" {
				continue
			}
			if !filepath.IsAbs(p) {
				return nil, "", fmt.Errorf("session: 附件路径必须是绝对路径（本产品无工作区概念）：%q", p)
			}
			raw.Attachments = append(raw.Attachments, port.Attachment{Path: p})
		}
		if len(raw.Attachments) > port.MaxAttachments {
			return nil, "", fmt.Errorf("session: 一条消息最多 %d 个附件（本次新增 %d 个）",
				port.MaxAttachments, len(raw.Attachments))
		}
		if len(raw.Attachments) > 0 {
			newParts, note, err := s.ingest(ctx, raw)
			if err != nil {
				return nil, "", fmt.Errorf("session: 摄取新附件: %w", err)
			}
			parts = append(parts, newParts...)
			added = countAttachmentParts(newParts)
			if note != "" && s.ui != nil {
				_ = s.ui.Emit(ctx, port.NoticeEvent{Text: note})
			}
		}
	}

	if len(parts) == 0 {
		return nil, "", errors.New("session: 修订后至少要有正文或一个附件（本次把内容删空了）")
	}
	if added+total-dropped > port.MaxAttachments {
		return nil, "", fmt.Errorf("session: 一条消息最多 %d 个附件（修订后会有 %d 个）",
			port.MaxAttachments, added+total-dropped)
	}

	var changes []string
	if added > 0 {
		changes = append(changes, fmt.Sprintf("新增 %d 个附件", added))
	}
	if dropped > 0 {
		changes = append(changes, fmt.Sprintf("移除 %d 个附件", dropped))
	}
	note := ""
	if len(changes) > 0 {
		note = strings.Join(changes, "、")
	}
	return parts, note, nil
}

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

// countAttachmentParts 数一组分片里的附件分片数（摄取可能同时产出正文分片，不能拿
// 总片数当附件数）。
func countAttachmentParts(parts []conversation.Part) int {
	n := 0
	for _, p := range parts {
		if isAttachmentPart(p.Kind) {
			n++
		}
	}
	return n
}

// resolveConversation 按会话 ID（精确或唯一前缀）解析会话（/memory [会话id] 用）；
// 前缀命中多个时报歧义（会话列表按时间降序，命中序确定）。
// 匹配策略与任务/观察器/节点共用 port.MatchPrefix（§2.1），这里只负责中文措辞。
func (s *Session) resolveConversation(ctx context.Context, arg string) (conversation.ID, error) {
	arg = strings.TrimSpace(arg)
	if arg == "" {
		return s.cur.ID, nil
	}
	list, err := s.store.List(ctx)
	if err != nil {
		return "", fmt.Errorf("session: 列出会话: %w", err)
	}
	ids := make([]conversation.ID, len(list))
	for i, sm := range list {
		ids[i] = sm.ID
	}
	id, hits, ok := port.MatchPrefix(ids, arg)
	switch {
	case ok:
		return id, nil
	case len(hits) == 0:
		return "", fmt.Errorf("没有会话 %q（/list 查看可用会话）", arg)
	default:
		return "", fmt.Errorf("会话标识 %q 有歧义（命中 %d 个: %s），请加长前缀", arg, len(hits), strings.Join(hits, ", "))
	}
}

// editUsage /edit 的用法文案（命令面与解析错误共用一份，避免两处漂移）。
const editUsage = "用法: /edit <id> [--keep|--copy] [--part N] [--attach <路径>]... [--drop N]... [--drop-all] [<文本>]（缺省 Fresh 开新分支；--keep 转移后续历史；--copy 复制后续历史；--part N 只改第 N 个正文分片，0 起；--attach 追加附件；--drop N 删除第 N 个附件，0 起；带附件操作时文本可省 = 只改附件）"

// parseEditArgs 解析 /edit：flag 只在紧跟 id 的位置识别（已知 flag 逐个消费，未知 "--x"
// 视为文本起点——字面 "--keep" 原样保留的旧口径不变）。--part N = 只替换第 N 个正文分片
// （0 起，D107②）；缺省 -1 = 全部正文并为一条（D107③）。D188 增附件增删 flag：
// --attach <路径>（可重复）/ --drop N（可重复，附件分片出现序）/ --drop-all。
// GUI 结构化提交复用同一 token 序列（D97①/D107②/D188）：
// Args=[id, flag?, ("--part", N)?, ("--attach", 路径)…, ("--drop", N)…, "--drop-all"?, 文本?]。
func parseEditArgs(args []string) (editOptions, error) {
	return parseEditArgsWith(args, nil)
}

// parseEditArgsWith 同上，另带结构化附件（D188 GUI 编辑态无路径的新增项）。
func parseEditArgsWith(args []string, extra []port.Attachment) (editOptions, error) {
	bad := func() (editOptions, error) { return editOptions{}, errors.New(editUsage) }
	if len(args) == 0 {
		return bad()
	}
	if strings.HasPrefix(args[0], "--") {
		return bad() // flag 在 id 前：按用法拒绝
	}
	opt := editOptions{target: args[0], mode: conversation.Fresh, part: -1, attach: extra}
	rest := args[1:]
	for len(rest) > 0 && strings.HasPrefix(rest[0], "--") {
		switch rest[0] {
		case "--keep":
			opt.mode = conversation.Carry
			rest = rest[1:]
		case "--copy":
			opt.mode = conversation.Clone
			rest = rest[1:]
		case "--part":
			if len(rest) < 2 {
				return bad() // --part 缺序号
			}
			n, err := strconv.Atoi(rest[1])
			if err != nil || n < 0 {
				return bad() // --part 序号非法
			}
			opt.part = n
			rest = rest[2:]
		case "--attach":
			if len(rest) < 2 || strings.TrimSpace(rest[1]) == "" {
				return bad() // --attach 缺路径
			}
			opt.attach = append(opt.attach, port.Attachment{Path: rest[1]})
			rest = rest[2:]
		case "--drop":
			if len(rest) < 2 {
				return bad() // --drop 缺序号
			}
			n, err := strconv.Atoi(rest[1])
			if err != nil || n < 0 {
				return bad() // --drop 序号非法
			}
			opt.drop = append(opt.drop, n)
			rest = rest[2:]
		case "--drop-all":
			opt.dropAll = true
			rest = rest[1:]
		default:
			// 未知 "--x" = 文本以它开头（旧口径：只有已知 flag 被消费）。
			goto text
		}
	}
text:
	if len(rest) < 1 {
		// 没有文本：有附件操作时合法（只改附件），否则按用法拒绝。
		if opt.hasAttachOp() {
			return opt, nil
		}
		return bad()
	}
	opt.text = strings.TrimSpace(strings.Join(rest, " "))
	if opt.text == "" {
		if opt.hasAttachOp() {
			return opt, nil // 只改附件：正文原样保留
		}
		return editOptions{}, errors.New(editUsage + "（修订文本不可为空）")
	}
	return opt, nil
}
