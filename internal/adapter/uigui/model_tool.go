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
// `inCh`（壳内不旁路内核，同 D72/D73 菜单口径），区别仅在不回显——按钮不是用户输入，
// 转写里写一行 `/goto <ulid>` 是噪音。返回 false = 输入缓冲满（点击丢弃，调用方给提示）。
//
// 但**返回文本仍要归处**（D265）：记下命令行，结果回来时归进命令折叠条——否则它会以
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
func (m *model) startConfirm(req port.ConfirmRequest, reply chan port.ConfirmAnswer) {
	prompt := sanitizeControl(req.Prompt)
	m.confirm = &pendingConfirm{prompt: prompt, reply: reply, req: req}
	// D157：发布跨窗投影（历史窗据此画同步确认弹窗；窗口未开则只存快照）。
	m.confirm.share = &confirmShare{prompt: prompt}
	m.u.publishConfirm(m.confirm.share)
	m.u.reasonEd.SetText("")  // D86：原因框清空待填
	m.u.reasonFocus = true    // 焦点让入原因框（主编辑器确认态隐藏，D63 一次性口径）
	m.u.levelPickOpen = false // D282：换一次确认 = 档位列表收起（旧列表不该跟到新问句上）
	if i := m.reopenChipFor(req.Tool); i >= 0 {
		// D282：同一件请求又回来了（选档位后被判 Ask）——沿用那块 chip，只换问句。
		m.blocks[i].chip.confirmQ = prompt
		m.blocks[i].chip.confirmA = ""
		m.confirmChip = i
		return
	}
	if i := m.openChipFor(req.Tool); i >= 0 {
		m.blocks[i].chip.confirmQ = prompt
		m.confirmChip = i
		return
	}
	m.confirmChip = -1
	m.add(blockPlain, prompt)
}

// reopenChipFor 刚答过又立刻回来的同一次确认（D282 直选档位 → 判 Ask → 回到确认态）：
// 只看最新一块，且它必须留过一版问答。不跨块向前找——并行调用里找错 chip 会把问句
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
func (m *model) replyConfirm(ans port.ConfirmAnswer) {
	if m.confirm == nil {
		return
	}
	reply, prompt := m.confirm.reply, m.confirm.prompt
	if m.confirmChip >= 0 && m.confirmChip < len(m.blocks) {
		if c := m.blocks[m.confirmChip].chip; c != nil && c.confirmQ != "" {
			c.confirmA = confirmEcho(ans) // D86：允许 / 拒绝 / 拒绝：<原因>
			prompt = ""                   // chip 路径留痕在展开体，不再加文本行
		}
	}
	m.confirm = nil
	m.confirmChip = -1
	m.u.levelPickOpen = false // D282：答完即收档位列表，不留一张浮卡在下一轮上
	m.u.publishConfirm(nil)   // D157：收尾即撤下跨窗投影（历史窗下一帧收起弹窗）
	if prompt != "" {
		m.add(blockPlain, prompt+" → "+confirmEcho(ans))
	}
	select {
	case reply <- ans:
	default:
	}
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
			// 允许一模一样，下次不再来问时他会以为哪里坏了。
			return i18n.T("ui.confirm.always_echo")
		}
		return i18n.T("ui.common.allow")
	}
	if r := sanitizeControl(strings.TrimSpace(ans.Reason)); r != "" {
		return i18n.Tf("ui.confirm.deny_with_reason", "reason", r)
	}
	return i18n.T("ui.common.deny")
}

// flushThink 把进行中的思维链落为定稿思考块（D34/§15.3：定稿带耗时秒数）。
func (m *model) flushThink() {
	if m.think.Len() == 0 {
		return
	}
	secs := 0
	if !m.thinkAt.IsZero() {
		secs = int(time.Since(m.thinkAt).Round(time.Second).Seconds())
	}
	b := block{kind: blockThinking, text: m.think.String(), secs: secs}
	m.blocks = append(m.blocks, b)
	m.think.Reset()
	m.thinkAt = time.Time{}
}

// resetDraft 清流式草稿。
func (m *model) resetDraft() {
	m.draft.Reset()
	m.drafting = false
}

// add 追加定稿块。思考块缺省 secs=-1（回放无耗时；flushThink 自行填）。
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
		b.secs = -1
	}
	m.blocks = append(m.blocks, b)
}

// addInject 追加注入块（D166）：来源/触发只作头部展示（D150 的落地事实）。
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

// appendPartText 把一个分片的呈现文本写入 b（partsText/textSegments 共用口径）。
func appendPartText(b *strings.Builder, p conversation.Part) {
	uicommon.AppendPartText(b, p, uicommon.ImageWhenUnaddressable)
}

// addUsage 用量累加。
func addUsage(a, b conversation.Usage) conversation.Usage {
	a.InputTokens += b.InputTokens
	a.OutputTokens += b.OutputTokens
	a.CostUSD += b.CostUSD
	return a
}

// sanitizeControl 剥除转义序列与控制字符（保留 \n\t；§9 不可信数据只渲染）。
// 实现在 internal/adapter/uicommon，四个消费方共用。
func sanitizeControl(s string) string {
	return uicommon.SanitizeControl(s, uicommon.KeepLayout)
}
