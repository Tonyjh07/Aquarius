package app

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/Tonyjh07/Aquarius/internal/domain/conversation"
	"github.com/Tonyjh07/Aquarius/internal/i18n"
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
			return "", errors.New("session: " + i18n.T("edit.attach_user_only"))
		}
	}
	// 人格（会话首 system 节点）只能转移历史：另起新分支会把整棵对话留在旧分支、Head 落到
	// 新人格上，转写区随之清空——界面上看起来像「改一句人格，对话没了」。图形界面把这一档
	// 锁成转移历史，命令行也必须给出同一道拦截，否则同一个破坏性操作在两个前端一个有锁、
	// 一个没有。
	if old, ok := s.cur.Find(id); ok && s.isPersona(old) && opt.mode != conversation.Carry {
		return "", errors.New("session: " + i18n.T("edit.persona_keep"))
	}
	parts, note, err := s.editPartsWithAttachments(ctx, id, opt)
	if err != nil {
		return "", err
	}
	m, err := s.cur.Revise(id, parts, opt.mode)
	if err != nil {
		return "", wrapSession("edit.revise", err)
	}
	if err := s.persist(ctx); err != nil {
		return "", err
	}
	// D92：Revise 已移 Head（Fresh 恒移、Carry 例外）——清屏回放新路径，
	// 转写区恒与 Head 一致（D81 口径，/goto 同款；此前只入树不回放是缺口）。
	if err := s.emitClear(ctx); err != nil {
		return "", err
	}
	if _, err := s.replayHistory(ctx, "replay.verb.revised"); err != nil {
		return "", err
	}
	desc := i18n.T("edit.branch.kept")
	switch opt.mode {
	case conversation.Carry:
		desc = i18n.T("edit.branch.moved")
	case conversation.Clone:
		desc = i18n.T("edit.branch.copied")
	}
	if note != "" {
		desc += i18n.Tf("edit.note.attach", "note", note)
	}
	// D93：编辑用户消息（Fresh 分叉）= 改写并重新生成——Head 在无回答的新节点上，
	// 直接重跑一轮（Carry 是原地改写历史，Head 随转移子树；assistant/system 修订
	// 不触发生成）。
	if opt.mode == conversation.Fresh && m.Role == conversation.RoleUser {
		if err := s.runTurn(ctx); err != nil {
			return "", err
		}
		desc += i18n.T("edit.note.regen")
	}
	return i18n.Tf("edit.revised",
		"id", id, "new", m.ID, "mode", opt.mode.String(), "desc", desc, "head", s.cur.Head), nil
}

// execRegen 实现 /regen：重新生成回答（D93 分叉口径）。
func (s *Session) execRegen(ctx context.Context, cmd port.Command) (string, error) {
	if len(cmd.Args) != 1 {
		return "", errors.New(i18n.T("usage.regen"))
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
				return "", wrapSession("edit.move_head", err)
			}
			if err := s.persist(ctx); err != nil {
				return "", err
			}
		}
	} else {
		// 已有回答：原 Parts 原样重发（Revise 内部 cloneParts，多模态分片保真）；
		// Fresh 开同父兄弟节点，旧回答子树原样留作历史分支。
		if _, err := s.cur.Revise(um.ID, um.Content, conversation.Fresh); err != nil {
			return "", wrapSession("edit.resend_user", err)
		}
		if err := s.persist(ctx); err != nil {
			return "", err
		}
	}
	// Head 已就位：清屏回放（D81 口径）后重跑一轮生成。
	if err := s.emitClear(ctx); err != nil {
		return "", err
	}
	if _, err := s.replayHistory(ctx, "replay.verb.regenerated"); err != nil {
		return "", err
	}
	if err := s.runTurn(ctx); err != nil {
		return "", err
	}
	if forked {
		return i18n.Tf("edit.regen.forked", "head", s.cur.Head), nil
	}
	return i18n.Tf("edit.regen.done", "head", s.cur.Head), nil
}

// isPersona 报告节点是否为人格快照（会话首节点）：system 角色且父节点是结构根。
// 人格可以改（D154），但只允许转移历史——见 execEdit 的拦截。
func (s *Session) isPersona(m conversation.Message) bool {
	return m.Role == conversation.RoleSystem && m.Parent == conversation.MessageID(s.cur.ID)
}

// resolveNode 把用户给出的节点标识解析为树中节点：先精确匹配，再按唯一前缀匹配
// （Root 节点 ID = 会话 ID，/goto <会话ID> 即回根）；前缀命中多个时报歧义。
// 匹配策略与任务/观察器/会话共用 port.MatchPrefix（§2.1），这里只负责中文措辞。
func (s *Session) resolveNode(arg string) (conversation.MessageID, error) {
	arg = strings.TrimSpace(arg)
	if arg == "" {
		return "", errors.New(i18n.T("edit.missing_node_id"))
	}
	id, hits, ok := port.MatchPrefix(s.cur.IDs(), arg) // 已按 ID 升序，歧义提示可读
	switch {
	case ok:
		return id, nil
	case len(hits) == 0:
		return "", errors.New(i18n.Tf("edit.node_missing", "id", fmt.Sprintf("%q", arg)))
	default:
		return "", errors.New(i18n.Tf("edit.node_ambiguous",
			"id", fmt.Sprintf("%q", arg), "n", len(hits), "hits", strings.Join(hits, ", ")))
	}
}

// upstreamUser 沿 Parent 回溯找 id 上游最近一条 user 消息（D92 /regen：
// 重发对象——id 本身是 user 消息时即其自身）；到 Root（Parent 为空）仍未遇则报错。
func (s *Session) upstreamUser(id conversation.MessageID) (conversation.Message, error) {
	for cur := id; cur != ""; {
		m, ok := s.cur.Find(cur)
		if !ok {
			return conversation.Message{}, errors.New("session: " + i18n.Tf("edit.node_not_in_tree", "id", cur))
		}
		if m.Role == conversation.RoleUser {
			return m, nil
		}
		cur = m.Parent
	}
	return conversation.Message{}, errors.New("session: " + i18n.T("edit.no_upstream_user"))
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
		return nil, "", errors.New("session: " + i18n.Tf("edit.node_not_in_tree", "id", id))
	}
	if old.Role != conversation.RoleUser {
		return nil, "", errors.New("session: " + i18n.T("edit.attach_user_only"))
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
			return nil, "", errors.New("session: " + i18n.Tf("edit.drop_range", "n", n, "total", total))
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
				return nil, "", errors.New("session: " +
					i18n.Tf("edit.attach_abs_path", "path", fmt.Sprintf("%q", p)))
			}
			raw.Attachments = append(raw.Attachments, port.Attachment{Path: p})
		}
		if len(raw.Attachments) > port.MaxAttachments {
			return nil, "", errors.New("session: " +
				i18n.Tf("edit.attach_limit_added", "max", port.MaxAttachments, "added", len(raw.Attachments)))
		}
		if len(raw.Attachments) > 0 {
			newParts, note, err := s.ingest(ctx, raw)
			if err != nil {
				return nil, "", wrapSession("edit.ingest_attach", err)
			}
			parts = append(parts, newParts...)
			added = countAttachmentParts(newParts)
			if note != "" && s.ui != nil {
				_ = s.ui.Emit(ctx, port.NoticeEvent{Text: note})
			}
		}
	}

	if len(parts) == 0 {
		return nil, "", errors.New("session: " + i18n.T("edit.empty_after_edit"))
	}
	if added+total-dropped > port.MaxAttachments {
		return nil, "", errors.New("session: " +
			i18n.Tf("edit.attach_limit_total", "max", port.MaxAttachments, "total", added+total-dropped))
	}

	var changes []string
	if added > 0 {
		changes = append(changes, i18n.Tf("edit.attach.added", "n", added))
	}
	if dropped > 0 {
		changes = append(changes, i18n.Tf("edit.attach.removed", "n", dropped))
	}
	note := ""
	if len(changes) > 0 {
		note = strings.Join(changes, i18n.T("edit.attach.join"))
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
			return nil, errors.New("session: " + i18n.Tf("edit.part_range", "n", part, "total", n+1))
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
		return "", wrapSession("err.list_conversations", err)
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
		return "", errors.New(i18n.Tf("edit.conv_missing", "id", fmt.Sprintf("%q", arg)))
	default:
		return "", errors.New(i18n.Tf("edit.conv_ambiguous",
			"id", fmt.Sprintf("%q", arg), "n", len(hits), "hits", strings.Join(hits, ", ")))
	}
}

// editUsage /edit 的用法文案（命令面与解析错误共用一份，避免两处漂移）。
func editUsage() string { return i18n.T("usage.edit") }

// parseEditArgs 解析 /edit：flag 只在紧跟 id 的位置识别（用 flagsLeading：遇到未声明的
// token（含未知「--x」）即为文本起点——字面「--keep」原样保留的旧口径不变）。--part N =
// 只替换第 N 个正文分片（0 起，D107②）；缺省 -1 = 全部正文并为一条（D107③）。D188 增
// 附件增删 flag：--attach <路径>（可重复）/ --drop N（可重复，附件分片出现序）/ --drop-all。
// GUI 结构化提交复用同一 token 序列（D97①/D107②/D188）：
// Args=[id, flag?, --part N?, --attach 路径…, --drop N…, --drop-all?, 文本?]。
func parseEditArgs(args []string) (editOptions, error) {
	return parseEditArgsWith(args, nil)
}

// editFlags /edit 认识的 flag（附件增删与分支模式都在这一段）。
var editFlags = []flagDecl{
	{name: "--keep"},
	{name: "--copy"},
	{name: "--part", value: true},
	{name: "--attach", value: true},
	{name: "--drop", value: true},
	{name: "--drop-all"},
}

// parseEditArgsWith 同上，另带结构化附件（D188 GUI 编辑态无路径的新增项）。
func parseEditArgsWith(args []string, extra []port.Attachment) (editOptions, error) {
	bad := func() (editOptions, error) { return editOptions{}, errors.New(editUsage()) }
	if len(args) == 0 {
		return bad()
	}
	if strings.HasPrefix(args[0], "--") {
		return bad() // flag 在 id 前：按用法拒绝
	}
	opt := editOptions{target: args[0], mode: conversation.Fresh, part: -1, attach: extra}
	flags, rest, err := scanFlags(args[1:], flagScan{pos: flagsLeading}, editFlags)
	if err != nil {
		return bad() // 值型 flag 缺参数
	}
	for _, f := range flags {
		switch f.name {
		case "--keep":
			opt.mode = conversation.Carry
		case "--copy":
			opt.mode = conversation.Clone
		case "--part":
			n, ok := parseIndex(f.value)
			if !ok {
				return bad() // --part 序号非法
			}
			opt.part = n
		case "--attach":
			if strings.TrimSpace(f.value) == "" {
				return bad() // --attach 缺路径
			}
			opt.attach = append(opt.attach, port.Attachment{Path: f.value})
		case "--drop":
			n, ok := parseIndex(f.value)
			if !ok {
				return bad() // --drop 序号非法
			}
			opt.drop = append(opt.drop, n)
		case "--drop-all":
			opt.dropAll = true
		}
	}
	if len(rest) < 1 {
		// 没有文本：有附件操作时合法（只改附件），否则按用法拒绝。
		if opt.hasAttachOp() {
			return opt, nil
		}
		return bad()
	}
	opt.text = joinArgs(rest)
	if opt.text == "" {
		if opt.hasAttachOp() {
			return opt, nil // 只改附件：正文原样保留
		}
		return editOptions{}, errors.New(editUsage() + i18n.T("edit.text_empty"))
	}
	return opt, nil
}
