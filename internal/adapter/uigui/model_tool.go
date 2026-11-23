package uigui

// 工具分片与确认回路：命令投递、chip 增删与结果回填、确认问答与回显。

import (
	"strings"
	"time"

	"github.com/Tonyjh07/Aquarius/internal/adapter/uicommon"
	"github.com/Tonyjh07/Aquarius/internal/domain/conversation"
	"github.com/Tonyjh07/Aquarius/internal/domain/tool"
	"github.com/Tonyjh07/Aquarius/internal/i18n"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

// submitCommand 投递一条命令且**不入转写块**（D81 分叉按钮）：与 model.submit 同走
// `inCh`（壳内不旁路内核，同 D72/D73 菜单规则），区别仅在不回显——按钮不是用户输入，
// 转写里写一行 `/goto <ulid>` 是噪音。返回 false = 输入缓冲满（点击丢弃，调用方给提示）。
//
// 但**返回文本仍要归处**（D265）：记下命令行，结果回来时归进命令折叠条——否则结果会以
// 纯文本行的样子落在一轮回答后面，与助手气泡同底板，看着像模型说的话。
func (m *model) submitCommand(line string) bool {
	select {
	case m.u.inCh <- parseInput(line):
		m.pendingCmd = line
		return true
	default:
		return false
	}
}

// addToolCall 新开一个工具调用 chip（D67：调用/确认/结果合并，默认折叠）。
func (m *model) addToolCall(call tool.Call) {
	m.blocks = append(m.blocks, block{kind: blockTool, chip: &toolChip{
		id:   call.ID,
		name: call.Name,
		args: sanitizeControl(string(call.Args)),
	}})
}

// attachToolResult 按 CallID 回填最新匹配 chip（D67；并行调用交错不错配）；
// 找不到（历史截断等）则新开结果 chip 兜底。
func (m *model) attachToolResult(r tool.Result) {
	for i := len(m.blocks) - 1; i >= 0; i-- {
		c := m.blocks[i].chip
		if c == nil || c.id != r.CallID {
			continue
		}
		c.done, c.ok = true, r.OK
		if r.OK {
			c.result = sanitizeControl(r.Output)
		} else {
			c.result = sanitizeControl(r.Err)
		}
		c.images = append(c.images, r.Images...) // D207：工具产出的图进 chip 展开体
		return
	}
	m.blocks = append(m.blocks, block{kind: blockTool, chip: &toolChip{
		done:   true,
		ok:     r.OK,
		result: sanitizeControl(resultText(r)),
		images: append([]tool.ImageRef(nil), r.Images...),
	}})
}

// resultText 取结果文本（ok = 输出，!ok = 错误）。
func resultText(r tool.Result) string {
	if r.OK {
		return r.Output
	}
	return r.Err
}

// closeOpenChips 收口所有未完成 chip（commit 时：取消/异常收尾没有结果事件，D67）。
func (m *model) closeOpenChips() {
	for i := range m.blocks {
		if c := m.blocks[i].chip; c != nil && !c.done {
			c.done = true
			c.ok = false
			c.result = i18n.T("ui.chat.tool_not_returned")
		}
	}
}

// startConfirm 打开确认态（Confirm 投递；输入栏切按钮组，§15.2）。
// 工具权限确认（问句含工具名且最新 chip 未完成）并入该 chip（D67）；
// 其余（/rm 二次确认等）保持独立文本行。
// 已有确认在等应答时，新来的排进队列（并行调用会同时举手），不顶掉当前这条。
func (m *model) startConfirm(req port.ConfirmRequest, reply chan port.ConfirmAnswer) {
	pc := &pendingConfirm{
		prompt: sanitizeControl(req.Prompt),
		reply:  reply,
		req:    req,
		chip:   -1,
		bi:     -1,
	}
	pc.line = confirmLine(pc.prompt)
	// 归属先认下来：排队期间块序不变，轮到它时按这个序回填问答。
	if i := m.reopenChipFor(req.Tool); i >= 0 {
		// D282：同一件请求又回来了（选档位后被判 Ask）——沿用那块 chip，只换问句。
		pc.chip = i
		m.blocks[i].chip.confirmQ = pc.prompt
		m.blocks[i].chip.confirmA = ""
	} else if i := m.openChipFor(req.Tool); i >= 0 {
		pc.chip = i
		m.blocks[i].chip.confirmQ = pc.prompt
	}
	if m.confirm != nil { // 前一条还等着回答：排队，问句与按钮等轮到它再换
		m.confirmQueue = append(m.confirmQueue, pc)
		return
	}
	m.activateConfirm(pc)
}

// activateConfirm 把一条确认推到前台（输入栏按钮组、原因框、跨窗投影、转写行）。
func (m *model) activateConfirm(pc *pendingConfirm) {
	m.confirm = pc
	m.confirmChip = pc.chip
	// D157：发布跨窗投影（历史窗据此画同步确认弹窗；窗口未开则只存快照）。
	pc.share = &confirmShare{prompt: pc.prompt}
	m.u.publishConfirm(pc.share)
	m.u.reasonEd.SetText("")  // D86：原因框清空待填
	m.u.reasonFocus = true    // 焦点让入原因框（主编辑器确认态隐藏，D63 一次性规则）
	m.u.levelPickOpen = false // D282：换一次确认 = 档位列表收起（旧列表不该跟到新问句上）
	if pc.chip < 0 {
		// 没有 chip 承载（脚本内并行调用、命令确认）：并进确认折叠条，收起只报数与成败。
		pc.bi, pc.ci = m.addConfirmEntry(pc.line)
	}
}

// confirmLine 转写行的单行问句：只取首行，空白压平，超过 80 rune 截断。
// 上限按「一行放得下」定：问句里塞着整段参数，一次并行发几条就能把转写区顶满。
// 被截掉的参数在对应工具 chip 里仍看得见（脚本内调用看 run_code 的参数），
// 这一行只要能认出是哪一次调用。
func confirmLine(prompt string) string {
	s := prompt
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > 80 {
		s = string(r[:80]) + "…"
	}
	return s
}

// reopenChipFor 刚答过又立刻回来的同一次确认（D282 直选档位 → 判 Ask → 回到确认态）：
// 只看最新一块，且最新一块必须留过一版问答。不跨块向前找——并行调用里找错 chip 会把问句
// 贴到另一次调用上。
func (m *model) reopenChipFor(name string) int {
	if name == "" || len(m.blocks) == 0 {
		return -1
	}
	i := len(m.blocks) - 1
	c := m.blocks[i].chip
	if c == nil || c.done || c.name != name || c.confirmQ == "" {
		return -1
	}
	return i
}

// openChipFor 确认归属的最新未完成 chip（D67）：读请求里的工具名精确匹配。
// 命令确认（/rm、插件授权）没有工具名，不并入，保持独立文本行。
func (m *model) openChipFor(name string) int {
	if name == "" {
		return -1
	}
	for i := len(m.blocks) - 1; i >= 0; i-- {
		c := m.blocks[i].chip
		if c == nil {
			continue
		}
		if !c.done && c.confirmQ == "" && c.name == name {
			return i
		}
		return -1
	}
	return -1
}

// replyConfirm 应答进行中的确认（缓冲 1 + default：取消后迟到的应答不阻塞）。
// chip 确认把问答留痕在 chip 内（D67）；其余保持文本行回显。两条路径都必须把应答
// 送回 runner（否则权限等待永不解除）。
// 队列里还有下一条时直接接上：并行调用的确认逐条问，不把后面的丢在待确认里没人问。
func (m *model) replyConfirm(ans port.ConfirmAnswer) {
	if m.confirm == nil {
		return
	}
	pc := m.confirm
	if pc.chip >= 0 && pc.chip < len(m.blocks) {
		if c := m.blocks[pc.chip].chip; c != nil && c.confirmQ != "" {
			c.confirmA = confirmEcho(ans) // D86：允许 / 拒绝 / 拒绝：<原因>
		}
	}
	m.confirm = nil
	m.confirmChip = -1
	m.u.levelPickOpen = false // D282：答完即收档位列表，不留一张浮卡在下一轮上
	if pc.chip < 0 {
		m.echoConfirmEntry(pc, ans)
	}
	if next := m.takeConfirm(); next != nil {
		m.activateConfirm(next) // 投影直接换成下一条，中间不撤（否则历史窗会闪一下空弹窗）
	} else {
		m.u.publishConfirm(nil) // D157：收尾即撤下跨窗投影（历史窗下一帧收起弹窗）
	}
	select {
	case pc.reply <- ans:
	default:
	}
}

// takeConfirm 取队列里的下一条确认（无则 nil，并收掉空队列）。
func (m *model) takeConfirm() *pendingConfirm {
	for len(m.confirmQueue) > 0 {
		pc := m.confirmQueue[0]
		m.confirmQueue = m.confirmQueue[1:]
		if pc != nil {
			return pc
		}
	}
	m.confirmQueue = nil
	return nil
}

// dropConfirms 丢掉进行中与排队的确认（轮次收尾：没有工具在等答复了）。
// 只清界面状态，不回射应答——这些 Confirm 的 ctx 已随本轮结束，给谁答都晚了。
// 前台上那条已在确认条里落过项：改写成「未作答」，否则那一行会永远挂在「待答 1」上。
func (m *model) dropConfirms() {
	if m.confirm == nil && len(m.confirmQueue) == 0 {
		return
	}
	if pc := m.confirm; pc != nil && pc.chip < 0 && pc.bi >= 0 && pc.bi < len(m.blocks) {
		if es := m.blocks[pc.bi].confirms; pc.ci >= 0 && pc.ci < len(es) && es[pc.ci].a == "" {
			es[pc.ci].a = i18n.T("ui.confirm.strip_unanswered")
			es[pc.ci].silent = true
		}
	}
	m.confirm = nil
	m.confirmChip = -1
	m.confirmQueue = nil
	m.u.levelPickOpen = false
	m.u.publishConfirm(nil)
}

// confirmEcho 应答回显文案（D86/D282）：改档位 / 允许 / 拒绝 / 拒绝：<原因>（原因消毒
// 同问句；Allow 时忽略原因）。chip confirmA 与 plain 行回显共用。
func confirmEcho(ans port.ConfirmAnswer) string {
	if lv := strings.TrimSpace(ans.Level); lv != "" {
		// 只记「选了哪档」：放没放行由 runner 按新档重判，可能紧接着又弹一次确认。
		return i18n.Tf("ui.confirm.level_echo", "level", lv)
	}
	if ans.Allow {
		if ans.Persist {
			// D282：点了「以后都允许」——回显要把这半句带上，否则用户看到的与点普通的
			// 允许一模一样，下次不再来问时用户会以为哪里坏了。
			return i18n.T("ui.confirm.always_echo")
		}
		return i18n.T("ui.common.allow")
	}
	if r := sanitizeControl(strings.TrimSpace(ans.Reason)); r != "" {
		return i18n.Tf("ui.confirm.deny_with_reason", "reason", r)
	}
	return i18n.T("ui.common.deny")
}

// flushThink 把进行中的思维链落为定稿思考块（D34/§15.3：定稿带耗时毫秒）。
func (m *model) flushThink() {
	if m.think.Len() == 0 {
		return
	}
	ms := 0
	if !m.thinkAt.IsZero() {
		ms = int(time.Since(m.thinkAt).Milliseconds())
	}
	b := block{kind: blockThinking, text: m.think.String(), ms: ms}
	m.blocks = append(m.blocks, b)
	m.think.Reset()
	m.thinkAt = time.Time{}
}

// resetDraft 清流式草稿。
func (m *model) resetDraft() {
	m.draft.Reset()
	m.drafting = false
}

// add 追加定稿块。思考块缺省 ms=-1（回放无耗时；flushThink 自行填）。
func (m *model) add(kind blockKind, text string) {
	m.addMsg(kind, text, "")
}

// addMsg 追加定稿块并记下其所源节点（D81：UI 据此在气泡下方画分叉编号；
// 空 id = 不参与分叉呈现，如命令输出/notice/思考块）。
func (m *model) addMsg(kind blockKind, text string, id conversation.MessageID) {
	if strings.TrimSpace(text) == "" {
		return
	}
	b := block{kind: kind, text: text, id: id, part: -1}
	if kind == blockThinking {
		b.ms = -1
	}
	m.blocks = append(m.blocks, b)
}

// addInject 追加注入块（D166）：来源/触发只作头部展示（D150 的实现事实）。
// 空正文的注入不落块——没有可读内容，头部单独成行只会制造噪声。
func (m *model) addInject(text, source string, trigger bool) {
	if strings.TrimSpace(text) == "" {
		return
	}
	m.blocks = append(m.blocks, block{
		kind: blockInject, text: text, source: source, trigger: trigger, part: -1,
	})
}

// partsText 节点文本：文本分片直连，其余留占位（§4.2 多模态呈现 MVP）；
// 出口统一剥控制序列（提交内容是不可信模型输出，§9）。
// 思考分片不并入正文——回放路径单独渲染为思考块（D42）；
// 工具分片不并入正文——回放路径单独渲染为 chip（D95）。
// 实现与终端/TUI 共用（uicommon）；图片由真缩略图承载，正文只在无引用时留占位（D207）。
func partsText(parts []conversation.Part) string {
	return uicommon.PartsText(parts, uicommon.ImageWhenUnaddressable)
}

// appendPartText 把一个分片的呈现文本写入 b（partsText/textSegments 共用规则）。
func appendPartText(b *strings.Builder, p conversation.Part) {
	uicommon.AppendPartText(b, p, uicommon.ImageWhenUnaddressable)
}

// addUsage 用量累加（分项一起加，见 conversation.Usage.Add）。
func addUsage(a, b conversation.Usage) conversation.Usage { return a.Add(b) }

// sanitizeControl 剥除转义序列与控制字符（保留 \n\t；§9 不可信数据只渲染）。
// 实现在 internal/adapter/uicommon，四个消费方共用。
func sanitizeControl(s string) string {
	return uicommon.SanitizeControl(s, uicommon.KeepLayout)
}
