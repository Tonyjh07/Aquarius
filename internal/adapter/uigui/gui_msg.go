package uigui

// 事件循环的桥接消息与分派：uiMsg 各类型、apply 与 drainInbox。

import (
	"strings"

	"github.com/Tonyjh07/Aquarius/internal/domain/conversation"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

// uiMsg 桥接消息（事件循环消费）。
type uiMsg any

// 事件循环与外部世界的桥接消息。
type (
	// eventMsg Turn 事件（Emit 投递）。
	eventMsg struct{ ev port.Event }
	// sayMsg 纯文本行（Say 投递）。
	sayMsg struct{ text string }
	// inputMsg 注入一行提交（headless 测试与外部输入泵用；窗口侧走 model.submit）。
	inputMsg struct{ text string }
	// editMsg 进入编辑态（D92/D97/D107 气泡右键编辑三方式经 shell 菜单分发投递）：目标
	// 节点 + 预填原文 + 修订方式（zero = Fresh）+ 正文分片序号（D107②，-1 = 缺省合段）；
	// D188 另带目标节点的已有附件快照（数据面 = port.TreeView.NodeAttachments）。
	// 提交/Esc 的生命周期见 model.editTarget 与 cancelEdit。
	editMsg struct {
		id   conversation.MessageID
		text string
		mode conversation.KeepMode
		part int
		atts []port.AttachmentView
	}
	// copyMsg 写剪贴板请求（D92 气泡菜单复制项经 shell 线程分发投递）：记账到
	// pendingCopy，下一帧经 gtx.Execute(clipboard.WriteCmd) 落盘（剪贴板写入必须在
	// Gio 帧上下文执行，shell 线程不可直达）。
	copyMsg struct{ text string }
	// quoteMsg 引用请求（D98 气泡菜单「引用」经 shell 线程分发投递）：文本口径与复制
	// 一致（选区优先/整条渲染文本，D98①），Gio 侧经 quoteInto 追加引用块进输入框。
	quoteMsg struct{ text string }
	// attachMsg 文件选择框 / 拖拽的结果（D104/D186：shell 或平台线程 →
	// Gio inbox → 暂存列表；chip 呈现，发送随 Enter 走 Raw）。
	attachMsg struct{ paths []string }
	// pasteMsg 剪贴板贴图结果（D187）：ref = 已入附件库的引用，thumb = 缩略图
	// （nil = 解码超预算/失败，chip 回落回形针）；err / tooLarge / full 三选一 = 失败。
	pasteMsg struct {
		ref      *conversation.BlobRef
		thumb    *thumbSrc
		err      error
		tooLarge int64
		full     bool
	}
	// thumbMsg 路径附件的缩略图就绪（D187）：key = 暂存去重键（同一次输入的同一文件）。
	thumbMsg struct {
		key   string
		thumb *thumbSrc
	}
	// askMsg 提问请求（D184：ask_user 经 port.Asker 投递，输入栏原地展开成面板）。
	askMsg struct {
		req   port.AskRequest
		reply chan port.AskAnswer
	}
	// askDoneMsg 提问面板收尾（Ask 侧因取消/关窗自行收场时投递，撤下没人等的面板）。
	askDoneMsg struct{}
	// confirmMsg 确认请求（Confirm 投递）。
	confirmMsg struct {
		prompt string
		reply  chan port.ConfirmAnswer
	}
	// confirmResultMsg Confirm 侧自行得出应答后的确认态收尾（关态 + 记转写）。
	// share 非空 = 次窗（历史窗确认弹窗）的应答，其指针身份须与当前确认一致才生效（D157）。
	confirmResultMsg struct {
		ans   port.ConfirmAnswer
		share *confirmShare
	}
	// drainMsg 排空标记（Close 投递：处理到它即代表此前 post 全部落进状态机）。
	drainMsg struct{ done chan struct{} }
	// eofMsg 输入流结束（关窗/外部泵投递；err 非空 = 读取错误，Next 原样上抛）。
	eofMsg struct{ err error }
	// quitMsg 结束事件循环（Close 在 drain 之后投递）。
	quitMsg struct{}
	// showExpandMsg 呼出（托盘/快捷键从隐藏唤起）：展开输入栏 + 焦点入栏（§15.1）。
	showExpandMsg struct{}
	// toggleExpandMsg 快捷键在窗口可见时：展开 ↔ 收起互切（§15.1）。
	toggleExpandMsg struct{}
	// themeMsg 主题档切换（设置窗保存/启动校正；mode = system|light|dark）。
	themeMsg struct{ mode string }
	// sysThemeMsg 系统深浅变化（主窗 WM_SETTINGCHANGE 广播 → 仅 system 档重解析）。
	sysThemeMsg struct{}
	// voiceMsg 语音键结果（D179）：text = 转写文本（回填输入框、不自动发送）；
	// err 非空 = 录音/转写失败（状态行提示）。
	voiceMsg struct {
		text string
		err  error
	}
)

// apply 把桥接消息应用到状态机（仅事件循环 goroutine 调用）；false = 循环应退出。
func (u *UI) apply(msg uiMsg) bool {
	switch m := msg.(type) {
	case eventMsg:
		u.m.handleEvent(m.ev)
	case sayMsg:
		u.m.say(m.text)
	case inputMsg:
		u.m.submit(m.text)
	case editMsg:
		// D92/D97 编辑态入口：预填原文 + 修订方式 + 焦点入编辑框（同唤出口径——layout
		// 次帧执行 FocusCmd）；提交/Esc 的收尾见 model.submit 与 cancelEdit。
		// D106 修订⑷：多行原文先展开再预填（SingleLine=false 下 SetText 保真——
		// 原顺序会在进入编辑态就压平换行）。
		u.m.editTarget = m.id
		u.m.editMode = m.mode
		u.m.editPart = m.part // D107②：编辑目标正文分片序号（提交转 --part token）
		// D188：进编辑态先清掉上一次的删除标记，再挂上目标节点的已有附件；已在暂存的
		// 新附件保留（它们就在 chip 排里，用户看得见，提交时一并加进这次修订）。
		u.m.editAtts = m.atts
		u.m.editDropped = nil
		if strings.Contains(m.text, "\n") || len(m.atts) > 0 {
			u.setExpanded(true)
		}
		u.editor.SetText(m.text)
		u.focusPending = true
	case copyMsg:
		u.pendingCopy = m.text // D92：记账，frame 内 flushCopy 落剪贴板
	case quoteMsg:
		// D98 引用："> " 引用块追加进输入框 + 焦点入框；不进编辑态（editTarget 不动，
		// 引用是起草新输入、非修订历史）。
		u.editor.SetText(quoteInto(u.editor.Text(), m.text))
		u.focusPending = true
	case attachMsg:
		// D186 多附件暂存：一次选中多个就都暂存（超上限的后几项逐条提示）；已有同名项
		// 不重复加。焦点入框便于接着写问题；发送随 Enter 走 Raw（model.submit 暂存分支）。
		for _, p := range m.paths {
			u.stagePath(p)
		}
		u.focusPending = true
	case pasteMsg:
		u.finishPaste(m)
	case thumbMsg:
		// 缩略图迟到也是常态（用户已经删掉那条附件）：按去重键找，找不到就丢。
		for i := range u.m.stagedFiles {
			if u.m.stagedFiles[i].key() == m.key {
				u.m.stagedFiles[i].thumb = m.thumb
				break
			}
		}
	case imgMsg:
		// D207：转写区缩略图就绪（内容寻址键；迟到与重投都按首份为准）。
		u.applyImg(m)
	case askMsg:
		u.m.startAsk(m.req, m.reply)
	case askDoneMsg:
		u.m.endAsk()
		if u.expanded {
			u.setExpanded(false)
		}
	case confirmMsg:
		u.m.startConfirm(m.prompt, m.reply)
	case confirmResultMsg:
		// D157：带快照身份的应答（历史窗弹窗）只对**同一次**确认生效——迟到应答丢弃，
		// 不得误答刚好新起的下一条确认。无身份（Confirm 侧自行收尾）照旧直接应答。
		if m.share != nil && (u.m.confirm == nil || u.m.confirm.share != m.share) {
			break
		}
		u.m.replyConfirm(m.ans)
	case eofMsg:
		u.signalEOF(m.err)
	case drainMsg:
		close(m.done)
	case showExpandMsg:
		u.beginExpand() // 呼出 = 召回 + 展开（D50 召回、D54 动画）
	case toggleExpandMsg:
		u.toggleExpand() // 互切；动画中反向续跑（D54）
	case themeMsg:
		u.applyTheme(m.mode) // 热生效（下一帧重绘，§15.4；事件循环 goroutine 独占）
	case sysThemeMsg:
		if u.themeMode == "system" {
			u.applyTheme("system") // 仅 system 档跟随；固定档忽略广播
		}
	case voiceMsg:
		u.finishVoice(m.text, m.err)
	case quitMsg:
		return false
	}
	return true
}

// drainInbox 全量排空收件箱（每事件先应用再渲染，§15.5）；false = 循环应退出。
func (u *UI) drainInbox() bool {
	for {
		select {
		case msg := <-u.inbox:
			if !u.apply(msg) {
				return false
			}
		default:
			return true
		}
	}
}
