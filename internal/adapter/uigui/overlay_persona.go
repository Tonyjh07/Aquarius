package uigui

// overlay_persona.go 遮罩「默认模板」页签（D154）：人格编辑区的**遮罩宿主**。
//
// 编辑区本体是 promptEditor（prompt_editor.go）——与设置窗「系统提示词」页同一份实现，
// 这里只负责三件事：
//  1. 把它画进遮罩卡片（同一个 rowLayout 版面，滚动容器换成裸 layout.List：
//     卡片里的常驻滚动条看着像排版故障，且模板页的模块网格本身就要占满宽）；
//  2. 页脚两钮的语义——「保存模板」写回 config 的人格两键（只对新会话生效），
//     「应用到本会话」按当前模板重新展开成最终 system 文本、经 /edit --keep 写回本会话；
//  3. 模块编辑弹窗画在本卡片之上（模态只作用于卡片：页脚在这层之下）。
//
// 「保存模板」为什么只写人格两键：遮罩与设置窗可以同时开着，若在这里发一份**全量**
// SettingsPatch（表单里其他字段都是打开时的快照），刚在设置窗改过的主题/限额会被旧值
// 盖回去。人格编辑只需要 system_prompt + prompts。

import (
	"strings"

	"gioui.org/io/key"
	"gioui.org/layout"
	"gioui.org/widget/material"

	"github.com/Tonyjh07/Aquarius/internal/domain/conversation"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

// overlayTemplateBody 模板页签内容：人格编辑区的行表 + 滚动 + 模块编辑弹窗。
func overlayTemplateBody(gtx layout.Context, th *material.Theme, ov *overlayState) layout.Dimensions {
	f := ov.prompt
	if f == nil {
		l := material.Body2(th, "模板编辑区不可用（未接线）")
		l.Color = paperOf().text3
		return l.Layout(gtx)
	}
	if ov.focusSink {
		ov.focusSink = false
		gtx.Execute(key.FocusCmd{Tag: &ov.tag})
	}
	rows := f.rows()
	return layout.Stack{}.Layout(gtx,
		layout.Stacked(func(gtx layout.Context) layout.Dimensions {
			return f.overlayList.Layout(gtx, len(rows), func(gtx layout.Context, i int) layout.Dimensions {
				return rowLayout(gtx, rows, i, func(gtx layout.Context) layout.Dimensions {
					dims, ok := f.rowBody(gtx, th, rows, i)
					if !ok {
						return layout.Dimensions{}
					}
					return dims
				})
			})
		}),
		// 模块编辑弹窗：画在卡片内容之上（此时 gtx 就是卡片盒，遮罩自然只盖卡片）。
		layout.Expanded(func(gtx layout.Context) layout.Dimensions {
			dims, _ := f.moduleEditorFrame(gtx, th)
			return dims
		}),
	)
}

// overlayTplStatus 模板页脚的状态行：反馈优先，否则给一句「改的是谁」。
func overlayTplStatus(gtx layout.Context, th *material.Theme, ov *overlayState) layout.Dimensions {
	text, col := ov.tplStatus, paperOf().text3
	if text == "" {
		text = "保存 = 以后新建会话的默认人格（重启后应用）；应用到本会话 = 按此模板改写本会话人格。"
	}
	if ov.tplErr {
		col = paperOf().danger
	}
	l := material.Caption(th, text)
	l.Color = col
	l.MaxLines = 2
	return l.Layout(gtx)
}

// saveTemplate 保存模板（D154）：写回 config 的 system_prompt + prompts 两键。
//
// 走 ApplyPromptTemplate 这条窄回调（而不是全量 ApplySettings）：遮罩与设置窗可以
// 同时开着，全量 patch 会拿本表单打开时的旧快照盖掉对方刚改的值。
func (ov *overlayState) saveTemplate(u *UI) {
	if ov.prompt == nil {
		return
	}
	if u.opts.ApplyPromptTemplate == nil {
		ov.tplErr, ov.tplStatus = true, "模板未接线（只读）"
		return
	}
	err := u.opts.ApplyPromptTemplate(PromptTemplatePatch{
		SystemPrompt: ov.prompt.sysPrompt.Text(),
		Modules:      ov.prompt.promptViews(),
		Persona:      ov.prompt.promptRefList(),
	})
	if err != nil {
		ov.tplErr = true
		ov.tplStatus = "保存失败：" + err.Error()
		return
	}
	ov.tplErr = false
	ov.tplStatus = "模板已保存（只对新会话生效，重启后应用）"
}

// applyTemplate 应用到本会话（D154）：按当前（可能未保存的）模板展开最终 system 文本，
// 经 `/edit <persona> --keep <文本>` 写回本会话的人格节点——两种模式之间唯一的桥。
//
// 没接线预览函数就报错而不是静默回退：回退到「保存过的模板」会让用户以为改的那几行
// 生效了。
func (ov *overlayState) applyTemplate(u *UI, st *historyState) {
	if ov.prompt == nil {
		return
	}
	if u.opts.PreviewPrompts == nil {
		ov.tplErr, ov.tplStatus = true, "无法展开模板（预览未接线）"
		return
	}
	text := strings.TrimSpace(u.opts.PreviewPrompts(
		ov.prompt.promptViews(), ov.prompt.promptRefList(), ov.prompt.sysPrompt.Text()))
	if text == "" {
		ov.tplErr, ov.tplStatus = true, "模板展开为空：检查人格引用是否指向已存在的模块"
		return
	}
	select {
	case u.inCh <- port.UserInput{Command: &port.Command{
		Name: "edit",
		Args: []string{string(ov.id), "--keep", text},
	}}:
	default:
		// 输入通道满：命令没投出去 ⇒ 不关遮罩（与 submit 同口径：保数据）。
		ov.tplErr, ov.tplStatus = true, "输入队列忙，请稍后重试"
		return
	}
	st.closeOverlay()
	_ = u.post(showExpandMsg{})
}

// personaMode 人格节点的应用方式恒为 Carry（D154；菜单预选不改变它）。
func personaMode(m conversation.KeepMode) conversation.KeepMode {
	if m == conversation.Fresh || m == conversation.Clone {
		return conversation.Carry
	}
	return m
}
