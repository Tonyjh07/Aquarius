package app

import (
	"fmt"
	"strings"

	"github.com/Tonyjh07/Aquarius/internal/i18n"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

// contextrender.go 把 ContextReport 摊成人看的两种形态（§7.1）：概览与整份 dump。
//
// 概览（`/context`）只给分块账，不铺正文——概览的用处是回答「上下文被谁占了」。
// dump（`/context full`）给全文，写进文件；几十万字符铺进转写区会拖垮渲染与行选。

// previewRunes 概览里每条消息的正文预览长度（超出的部分不显示，全文用 `/context <n>`）。
const previewRunes = 60

// renderContextSummary 概览。
func (a *Agent) renderContextSummary(rep ContextReport) string {
	var b strings.Builder
	pct := 0.0
	if rep.Budget > 0 {
		pct = 100 * float64(rep.Total) / float64(rep.Budget)
	}
	b.WriteString(i18n.Tf("session.ctx.head",
		"model", rep.Model, "est", rep.Total, "max", rep.Budget,
		"pct", fmt.Sprintf("%.1f", pct), "at", rep.CompactAt))
	b.WriteByte('\n')
	b.WriteString(i18n.Tf("session.ctx.params", "params", rep.Params))
	b.WriteByte('\n')
	if rep.Last {
		b.WriteString(i18n.T("session.ctx.src_last"))
	} else {
		b.WriteString(i18n.T("session.ctx.src_live"))
	}

	// 按消息分组：先算出每条消息的小计与每条消息包含的段序，再逐条铺。分组是为了让
	// 「首条 system 里并进了四段」读起来是一件事，而不是四条互不相干的 system。
	type group struct {
		msg   int
		role  port.PromptRole
		total int
	}
	var groups []group
	idx := map[int]int{}
	for _, sec := range rep.Sections {
		if sec.Msg == 0 {
			continue
		}
		gi, ok := idx[sec.Msg]
		if !ok {
			gi = len(groups)
			idx[sec.Msg] = gi
			groups = append(groups, group{msg: sec.Msg, role: sec.MsgRole})
		}
		groups[gi].total += sec.Tokens
	}
	for _, g := range groups {
		var secs []ContextSection
		for _, sec := range rep.Sections {
			if sec.Msg == g.msg {
				secs = append(secs, sec)
			}
		}
		b.WriteString("\n\n")
		if len(secs) == 1 {
			// 单段消息不铺子行：标题里已经有类别与用量，再列一遍只是重复。
			b.WriteString(i18n.Tf("session.ctx.msg_one",
				"n", g.msg, "role", i18n.T(contextRoleKey(g.role)),
				"title", sectionTitle(secs[0]), "tok", g.total))
		} else {
			b.WriteString(i18n.Tf("session.ctx.msg_head",
				"n", g.msg, "role", i18n.T(contextRoleKey(g.role)), "tok", g.total))
			for _, sec := range secs {
				share := 0.0
				if g.total > 0 {
					share = 100 * float64(sec.Tokens) / float64(g.total)
				}
				b.WriteString("\n   · ")
				b.WriteString(sectionTitle(sec))
				b.WriteString(i18n.Tf("session.ctx.part_tok",
					"tok", sec.Tokens, "pct", fmt.Sprintf("%.0f", share)))
			}
		}
		// 预览只给用户与助手的正文：工具结果、注入内容的首行多半是噪声。
		if g.role == port.PromptUser || g.role == port.PromptAssistant {
			if text := contextPreview(rep.Sections, g.msg); text != "" {
				b.WriteString("\n   ")
				b.WriteString(text)
			}
		}
	}
	if rep.Tools > 0 {
		b.WriteString("\n\n")
		b.WriteString(i18n.Tf("session.ctx.tools", "n", rep.Tools, "tok", rep.ToolTok))
	}
	return b.String()
}

// contextPreview 组内第一段可读正文的首行预览。
func contextPreview(secs []ContextSection, msg int) string {
	for _, sec := range secs {
		if sec.Msg != msg || sec.Text == "" {
			continue
		}
		line := strings.TrimSpace(sec.Text)
		if i := strings.IndexByte(line, '\n'); i >= 0 {
			line = strings.TrimSpace(line[:i])
		}
		if line == "" {
			return ""
		}
		r := []rune(line)
		if len(r) > previewRunes {
			return string(r[:previewRunes]) + "…"
		}
		return line
	}
	return ""
}

// sectionTitle 一段的标题：类别名 + 类别自带的补充（世界书条数、记忆构成、注入来源）。
func sectionTitle(sec ContextSection) string {
	switch sec.Kind {
	case labelLore:
		return i18n.Tf("session.ctx.kind.lore", "n", sec.Detail)
	case labelMemory:
		return i18n.Tf("session.ctx.kind.memory", "parts", memoryParts(sec.Detail))
	case labelInjectHost, labelInjectExt:
		key := "session.ctx.kind.inject_host"
		if sec.Kind == labelInjectExt {
			key = "session.ctx.kind.inject_external"
		}
		if sec.Detail != "" {
			return i18n.Tf(key, "source", sec.Detail)
		}
		if sec.Kind == labelInjectExt {
			return i18n.T("session.ctx.kind.inject_external.plain")
		}
		return i18n.T("session.ctx.kind.inject_host.plain")
	}
	return i18n.T(contextKindKey(sec.Kind))
}

// sectionBody 单段正文；整条消息若由多段并成，按空行分开并在每段前标类别。
func sectionBody(rep ContextReport, n int) (string, bool) {
	var secs []ContextSection
	for _, sec := range rep.Sections {
		if sec.Msg == n {
			secs = append(secs, sec)
		}
	}
	if len(secs) == 0 {
		return "", false
	}
	if len(secs) == 1 {
		return secs[0].Text, true
	}
	var b strings.Builder
	for i, sec := range secs {
		if i > 0 {
			b.WriteString("\n\n")
		}
		b.WriteString("— ")
		b.WriteString(sectionTitle(sec))
		b.WriteString(" —\n")
		b.WriteString(sec.Text)
	}
	return b.String(), true
}

// renderContextDump 整份上下文（写文件用）。分节之间用分隔线，正文原样，不做转义——
// dump 的用途是逐字比对，任何改写都会让「这一处和发出去的不一样」变得无法判断。
func (a *Agent) renderContextDump(rep ContextReport) string {
	var b strings.Builder
	b.WriteString(i18n.Tf("session.ctx.dump_head", "model", rep.Model))
	b.WriteByte('\n')
	b.WriteString(i18n.Tf("session.ctx.params", "params", rep.Params))
	b.WriteByte('\n')
	if rep.Last {
		b.WriteString(i18n.T("session.ctx.src_last"))
	} else {
		b.WriteString(i18n.T("session.ctx.src_live"))
	}
	b.WriteByte('\n')
	b.WriteString(i18n.Tf("session.ctx.dump_total",
		"est", rep.Total, "max", rep.Budget, "at", rep.CompactAt))
	b.WriteByte('\n')
	b.WriteString(i18n.Tf("session.ctx.dump_tools", "n", rep.Tools, "tok", rep.ToolTok))
	for _, sec := range rep.Sections {
		b.WriteString("\n\n---\n\n")
		b.WriteString(i18n.Tf("session.ctx.dump_section",
			"n", sec.Msg, "role", i18n.T(contextRoleKey(sec.MsgRole)),
			"title", sectionTitle(sec), "tok", sec.Tokens))
		b.WriteString("\n\n")
		b.WriteString(sec.Text)
	}
	if rep.ToolText != "" {
		b.WriteString("\n\n---\n\n")
		b.WriteString(i18n.T("session.ctx.dump_tool_head"))
		b.WriteString("\n\n")
		b.WriteString(rep.ToolText)
	}
	b.WriteByte('\n')
	return b.String()
}

// contextKindKey 类别 token → i18n key。未知 token 原样显示（前向兼容：新增标签在旧
// 版本上读成拼音串，而不是空白）。
func contextKindKey(kind string) string {
	switch kind {
	case labelPersona:
		return "session.ctx.kind.persona"
	case labelSummary:
		return "session.ctx.kind.summary"
	case labelLore:
		return "session.ctx.kind.lore"
	case labelMemory:
		return "session.ctx.kind.memory"
	case labelNote:
		return "session.ctx.kind.note"
	case labelTail:
		return "session.ctx.kind.tail"
	case labelUser:
		return "session.ctx.kind.user"
	case labelAssistant:
		return "session.ctx.kind.assistant"
	case labelTool:
		return "session.ctx.kind.tool"
	case labelToolImage:
		return "session.ctx.kind.tool_image"
	case labelInjectHost:
		return "session.ctx.kind.inject_host"
	case labelInjectExt:
		return "session.ctx.kind.inject_external"
	}
	return kind
}

// contextRoleKey 角色 → i18n key。
func contextRoleKey(role port.PromptRole) string {
	switch role {
	case port.PromptSystem:
		return "session.ctx.role.system"
	case port.PromptUser:
		return "session.ctx.role.user"
	case port.PromptAssistant:
		return "session.ctx.role.assistant"
	case port.PromptTool:
		return "session.ctx.role.tool"
	}
	return string(role)
}

// memoryParts 记忆块的构成（`index` / `session` / `index+session`）翻成人话。
func memoryParts(detail string) string {
	switch detail {
	case "index":
		return i18n.T("session.ctx.mem.index")
	case "session":
		return i18n.T("session.ctx.mem.session")
	case "index+session":
		return i18n.T("session.ctx.mem.both")
	}
	return detail
}
