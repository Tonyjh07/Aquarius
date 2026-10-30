package uigui

// 一轮对话的发出侧：replay 之后的提交路径、待发附件的暂存与编辑态附件操作。

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/Tonyjh07/Aquarius/internal/domain/conversation"
	"github.com/Tonyjh07/Aquarius/internal/domain/tool"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

// replay 历史节点回放（D40/§7.4）：已提交节点按角色一次性定稿渲染；无草稿/流式
// 过程，也不累计用量（回放是展示，不是新一轮提交）。D95 Turn 粒度：逐分片序渲染
// ——思考分片各自成块、正文按 tool 边界分段成气泡、chip 夹段间，与实时一致。
func (m *model) replay(msg conversation.Message) {
	text := partsText(msg.Content)
	switch msg.Role {
	case conversation.RoleUser:
		// D207：纯图消息（有附件无文字）也要成块——否则图整条消失。
		imgs := nodeImages(msg.Content)
		if strings.TrimSpace(text) != "" || len(imgs) > 0 {
			m.blocks = append(m.blocks, block{
				kind: blockUser, text: text, id: msg.ID, part: 0, imgs: imgs,
			})
		}
	case conversation.RoleAssistant:
		start := len(m.blocks)
		var seg strings.Builder
		var segImgs []imgRef
		// D107④：段序盖章——segStart = 本段首个 PartText 在 Content 中的序号（0 起），
		// 编辑回传 --part 用；flushSeg 产出块即盖。
		textOrd, segStart := -1, -1
		flushSeg := func() {
			s := sanitizeControl(seg.String())
			if strings.TrimSpace(s) != "" || len(segImgs) > 0 {
				// 段先不带 ID：末段统一盖。**不用 addMsg**——它按空文本丢弃，
				// 纯图段（有图无字）会整段消失（D207）。
				m.blocks = append(m.blocks, block{kind: blockAssistant, text: s, part: segStart, imgs: segImgs})
			}
			seg.Reset()
			segImgs = nil
			segStart = -1
		}
		for _, p := range msg.Content {
			switch p.Kind {
			case conversation.PartThinking:
				flushSeg()
				if strings.TrimSpace(p.Text) != "" {
					m.addMsg(blockThinking, p.Text, msg.ID) // 回放无耗时 → secs 置 -1；D100② 直盖节点 ID
				}
			case conversation.PartTool:
				flushSeg()
				if p.Tool == nil {
					continue
				}
				m.addToolCall(tool.Call{ID: tool.CallID(p.Tool.CallID), Name: p.Tool.Name, Args: p.Tool.Args})
				if p.Tool.Result != nil {
					m.attachToolResult(*p.Tool.Result) // D67：按 CallID 并入调用 chip
				}
			default:
				if p.Kind == conversation.PartText {
					textOrd++
					if segStart == -1 {
						segStart = textOrd
					}
				}
				if p.Kind == conversation.PartImage {
					if r, ok := imageRefOf(p); ok {
						segImgs = append(segImgs, r)
						continue
					}
				}
				appendPartText(&seg, p)
			}
		}
		flushSeg()
		// 非 done 终态：标记行并入最后一个正文气泡（与实时 commit 同口径）；
		// 无正文段（空内容的取消/错误）单独给标记块。
		// 空 Outcome 不打标记（D107 补：旧版 Revise 产的修订节点零值——标记会渲染成 `[]`）。
		if msg.Outcome != "" && msg.Outcome != conversation.OutcomeDone {
			marked := false
			for i := len(m.blocks) - 1; i >= start; i-- {
				if m.blocks[i].kind == blockAssistant {
					m.blocks[i].text = fmt.Sprintf("[%s]\n%s", msg.Outcome, m.blocks[i].text)
					marked = true
					break
				}
			}
			if !marked {
				m.addMsg(blockAssistant, fmt.Sprintf("[%s]", msg.Outcome), "")
			}
		}
		// 最后一个正文气泡盖节点 ID（D81 分叉条每 Turn 一个挂点）；
		// 无正文段（纯工具/思考轮）走 D89 锚点回填。
		stamped := false
		for i := len(m.blocks) - 1; i >= start; i-- {
			if m.blocks[i].kind == blockAssistant {
				m.blocks[i].id = msg.ID
				stamped = true
				break
			}
		}
		if !stamped {
			m.stampAssistantAnchor(msg, start) // D89：纯工具/思考轮锚点（见函数注释）
		}
	case conversation.RoleSystem:
		if strings.TrimSpace(text) != "" {
			m.add(blockSystem, text)
		}
	case conversation.RoleInject:
		m.addInject(text, msg.Source, msg.Trigger) // D166：暗色卡片，不参与 user 盖章/分叉
	default: // root 等不该出现在回放区间
	}
}

// submit 提交一行：确认态优先应答，提问态优先作答，编辑态走结构化 /edit，空行忽略，
// 其余先投递再入转写。
// 投递失败（缓冲满）时明确标注"未执行"——不制造"已执行"错觉（uitui 审查修复同款）。
func (m *model) submit(text string) {
	if m.ask != nil {
		// D184：提问面板持键盘时，键入行不参与会话——按面板语义解释（编号 / 文字答案）。
		m.submitAsk(text)
		return
	}
	if m.confirm != nil {
		m.replyConfirm(port.ConfirmAnswer{Allow: isYes(text)})
		return
	}
	line := strings.TrimSpace(text)
	// D186 暂存随文发：有暂存附件 → Raw 提交（文本可空 = 仅附件，暂存清空）；编辑态不
	// 走这条（D188：编辑态的新增附件要随 /edit 走 Command.Attachments）。
	if m.editTarget == "" && len(m.stagedFiles) > 0 {
		m.submitRaw(m.stagedFiles, line)
		return
	}
	// D188：编辑态有附件操作时文本可空（只改附件）——空提交不再等于取消。
	if line == "" && !m.editHasAttachOp() {
		return
	}
	// D92/D97 编辑态：提交 = 结构化 /edit 命令——Args 直达不经斜杠解析（parseEditArgs
	// 的 Join 对单元素是恒等，多行文本保真）；修订方式转为 flag token（--keep=Carry/
	// --copy=Clone，与 typed 路径同解析，D97①）；不回显 blockUser，修订结果由 /edit 的
	// 清屏回放呈现（D81 口径：转写区恒与 Head 一致）。
	if m.editTarget != "" {
		id := m.editTarget
		mode := m.editMode
		part := m.editPart
		args := []string{string(id)}
		switch mode {
		case conversation.Carry:
			args = append(args, "--keep")
		case conversation.Clone:
			args = append(args, "--copy")
		}
		if part >= 0 {
			args = append(args, "--part", strconv.Itoa(part)) // D107②：精确替换该正文分片
		}
		// D188：删掉的已有附件 → --drop N（序号口径 = 节点内附件分片出现序，升序稳定）；
		// 新增附件一律走 Command.Attachments——**顺序即 chip 排顺序**（含路径项：路径也
		// 走结构化载荷，混合贴图与选文件时才不会因为"两条通道"把次序打乱；`--attach`
		// 仍是纯文本界面（repl/TUI）的入口）。
		for _, idx := range m.editDroppedIndexes() {
			args = append(args, "--drop", strconv.Itoa(idx))
		}
		added := make([]port.Attachment, 0, len(m.stagedFiles))
		for _, a := range m.stagedFiles {
			added = append(added, a.attachment())
		}
		if line != "" {
			args = append(args, line) // 空 = 只改附件（内核据此保留原正文）
		}
		cmd := port.Command{Name: "edit", Args: args, Attachments: added}
		staged := m.stagedFiles
		// 无论投递成败，编辑态都已收尾（失败只提示，不把用户留在半截状态里）。
		m.editTarget = ""
		m.editMode = conversation.Fresh
		m.editPart = -1
		m.editAtts = nil
		m.editDropped = nil
		m.stagedFiles = nil
		select {
		case m.u.inCh <- port.UserInput{Command: &cmd}:
		default:
			// 缓冲满：暂存的附件还回去（挑文件比打字贵），编辑态照常收尾——同 submitRaw
			// 「失败不吞附件」的口径。
			m.stagedFiles = staged
			m.add(blockNotice, "[notice] 输入缓冲已满，此行未执行")
		}
		return
	}
	select {
	case m.u.inCh <- parseInput(line):
		m.add(blockUser, line)
	default:
		m.add(blockUser, line)
		m.add(blockNotice, "[notice] 输入缓冲已满，此行未执行")
	}
}

// submitRaw 投递 Raw 摄取输入（D186 多附件）：附件列表 + 已输入文本一并进摄取管线；
// 用户节点无 CommittedEvent（Agent 只广播 Turn/system 节点）→ 本地回显 blockUser
// （文本 + 附件名示意——与回放的摄取分片呈现存在口径差，D104③ 后果①）。
// 缓冲满：恢复暂存 + notice（编辑框已清、文本丢失，D104 后果③）。
func (m *model) submitRaw(atts []stagedAtt, text string) {
	if len(atts) == 0 {
		return
	}
	raw := port.RawInput{Kind: "file", Text: text}
	for _, a := range atts {
		raw.Attachments = append(raw.Attachments, a.attachment())
	}
	select {
	case m.u.inCh <- port.UserInput{Raw: &raw}:
		m.stagedFiles = nil // 提交即清暂存（D186）
	default:
		m.add(blockNotice, "[notice] 输入缓冲已满，附件未发送")
		return
	}
	echo := text
	if echo != "" {
		echo += " "
	}
	// D207：图片附件不再以文字占位回显——它们以缩略图入块（imgs），回显文本只列
	// 非图片附件（文档等仍需要名字，否则提交后到回放前看不出附了什么）。
	names := make([]string, 0, len(atts))
	var imgs []imgRef
	for _, a := range atts {
		if r, ok := imgRefOfAtt(a); ok {
			imgs = append(imgs, r)
			continue
		}
		names = append(names, a.displayName())
	}
	if len(names) == 1 {
		echo += "〔附件：" + names[0] + "〕"
	} else if len(names) > 1 {
		echo += fmt.Sprintf("〔附件 %d 个：%s〕", len(names), strings.Join(names, "、"))
	}
	m.blocks = append(m.blocks, block{kind: blockUser, text: strings.TrimSpace(echo), id: "", part: -1, imgs: imgs})
}

// stageAttach 暂存一项附件（D186）：已在列表里则不重复添加（同一文件附两遍只会白占
// 上下文）；超过单条上限后拒收并提示——报错文案由端口常量生成。
func (m *model) stageAttach(path string) {
	if strings.TrimSpace(path) == "" {
		return
	}
	m.stageAtt(stagedAtt{path: path})
}

// stageBlob 暂存一项已入库附件（D187 粘贴图片，字节已在库里）。
func (m *model) stageBlob(ref conversation.BlobRef) {
	m.stageAtt(stagedAtt{blob: &ref})
}

// stageAtt 暂存展开的公共段：去重 + 上限。
func (m *model) stageAtt(att stagedAtt) {
	key := att.key()
	for _, a := range m.stagedFiles {
		if a.key() == key {
			return
		}
	}
	if len(m.stagedFiles) >= port.MaxAttachments {
		m.add(blockNotice, fmt.Sprintf("[notice] 一条消息最多 %d 个附件，已忽略 %s",
			port.MaxAttachments, att.displayName()))
		return
	}
	m.stagedFiles = append(m.stagedFiles, att)
}

// unstageAttach 从暂存列表移除第 i 项（chip 点击，D186）。
func (m *model) unstageAttach(i int) {
	if i < 0 || i >= len(m.stagedFiles) {
		return
	}
	m.stagedFiles = append(m.stagedFiles[:i], m.stagedFiles[i+1:]...)
}

// clearAttach 清空暂存（收起态汇总 chip 点击）。D188：编辑态顺带把已有附件全部标记删除
// ——收起态那个汇总 chip 的语义是「全部清掉」，编辑态下保持同一手感（Esc 可整体退出，
// 再点「+」逐项恢复）。
func (m *model) clearAttach() {
	m.stagedFiles = nil
	if m.editTarget != "" {
		m.dropAllEditAtts()
	}
}

// toggleEditAtt 编辑态已有附件的「删除/恢复」切换（D188）：标记只作用于本次修订，不动
// 附件库、也不改旧节点。
func (m *model) toggleEditAtt(index int) {
	if m.editDropped == nil {
		m.editDropped = map[int]bool{}
	}
	if m.editDropped[index] {
		delete(m.editDropped, index)
		return
	}
	m.editDropped[index] = true
}

// dropAllEditAtts 标记删除编辑态的全部已有附件（D188）。
func (m *model) dropAllEditAtts() {
	if len(m.editAtts) == 0 {
		return
	}
	if m.editDropped == nil {
		m.editDropped = map[int]bool{}
	}
	for _, att := range m.editAtts {
		m.editDropped[att.Index] = true
	}
}

// editDroppedIndexes 已标记删除的附件序号，升序（Args token 顺序稳定，便于测试断言）。
func (m *model) editDroppedIndexes() []int {
	out := make([]int, 0, len(m.editDropped))
	for idx := range m.editDropped {
		out = append(out, idx)
	}
	sort.Ints(out)
	return out
}

// editHasAttachOp 编辑态是否带附件操作（D188：有操作时文本可空 = 只改附件）。
func (m *model) editHasAttachOp() bool {
	return len(m.editDropped) > 0 || len(m.stagedFiles) > 0
}
