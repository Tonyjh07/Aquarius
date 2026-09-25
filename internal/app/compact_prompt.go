package app

import "strings"

// 压缩摘要提示词（D21，DESIGN §7.1）：借鉴 OpenCode SessionCompaction 的压缩提示词——
// 结构化英文模板措辞对任意 agent 通用接手，正文语言由模型随对话自然决定。

// compactSummaryTemplate 摘要结构化模板（<template> 标签只出现在提示里、不进摘要正文）。
const compactSummaryTemplate = `You MUST use this format for your response (you may omit sections that aren't applicable). Do not include the <template> tags in your response.
<template>
## Objective
- [one or two brief sentences describing what the user is trying to accomplish]

## Requirements
- [constraints, preferences, requirements, and scope boundaries stated by the user, or "(none)"]

## Decisions
- [decisions already made and why, or "(none)"]

## Work State
Break the objective into smaller goals and report which are completed, which are being worked on, and which are blocked.
### Completed
- [goals that have been completed; otherwise "(none)"]

### Active
- [goals currently being worked on; otherwise "(none)"]

### Blocked
- [anything blocking progress, and why; otherwise "(none)"]

## Next Move
1. [ordered list of next actions, or "(none)"]

## Relevant Files
List the files and directories that another agent would need to open to continue this work. Include at most 15, most important first. Do not list every file that was read or changed. If none, write "(none)".
- ` + "`[file or directory path]`" + `: [brief reason it matters]

## Important Context
- [facts the next agent cannot continue without and cannot easily find on its own; or "(none)"]
</template>`

// compactSummaryRules 摘要书写规则。
const compactSummaryRules = `Rules:
- Keep each section concise. Use terse, single-line bullets, not prose paragraphs or nested lists.
- Prefer short references over detailed restatement. It is fine to leave out information the next agent can recover from the code or the files listed above.
- Preserve exact file paths, symbols, commands, error strings, URLs, and identifiers.
- Carry forward only user questions or requests that remain unanswered or require further action. Do not repeat ones that newer history has answered or resolved. Preserve exact wording when carrying one forward.
- Preserve consequential workflow state, including whether changes are uncommitted, committed, pushed, under review, or merged, and whether background jobs are still running.
- Do not mention the summary process or that context was compacted.`

// compactShared 两态共用段：只总结用户与助手的言行（persona/系统设定恒回传、不入摘要）、
// 模板、规则、禁止续写与工具调用、只输出模板正文。
var compactShared = []string{
	"Summarize only what the user and the assistant said and did. Leave out instructions and setup the assistant was given rather than told by the user: the persona/system configuration, repository conventions, instruction files such as AGENTS.md, and environment details like the session ID. The next agent receives current versions of all of these separately.",
	compactSummaryTemplate,
	compactSummaryRules,
	"Do not continue the task or call tools.",
	"Return only the structured summary in the requested format. Do not include a preamble, explanation, or other commentary.",
}

// compactRetryReminder 上次输出缺模板小节时追加的重试提示（OpenCode 同款）。
const compactRetryReminder = "The previous response did not fill in the required summary template. Do not call tools. Return the summary as text using the exact section headings from the template."

// compactNudge 压缩请求尾部的输出提示（user 角色，紧随历史之后）。
const compactNudge = "Output the summary for the conversation above using the required template."

// compactPrompt 组装压缩提示词两态（OpenCode 式）：
// existing=false 首次压缩；existing=true 链式压缩——对话中已有旧摘要，
// 与其合并为一份（新历史优先、对齐 Work State 与 Next Move、只输出更新后的小节）。
func compactPrompt(existing bool) string {
	if existing {
		return strings.Join(append([]string{
			"Update the existing summary in the conversation above into one consolidated summary.",
			"Newer history always takes precedence over the existing summary. Preserve previous information unless newer history clearly contradicts, supersedes, resolves, or makes it stale. If something is no longer relevant to continuing the work, you may remove it.",
			"Incorporate newer requirements, decisions, progress, and context. Reconcile Work State and Next Move: move completed work out of Active, remove resolved blockers and answered questions, and preserve unresolved or pending work.",
			"Return only the updated Markdown sections.",
		}, compactShared...), "\n\n")
	}
	return strings.Join(append([]string{
		"You MUST summarize the conversation above into a structured summary that will be given to another agent to resume the work.",
	}, compactShared...), "\n\n")
}

// compactHeadings 模板小节标题集合（"##" 起始行、含 "###" 子节；校验摘要输出用）。
var compactHeadings = func() map[string]bool {
	m := make(map[string]bool)
	for _, line := range strings.Split(compactSummaryTemplate, "\n") {
		if line = strings.TrimSpace(line); strings.HasPrefix(line, "##") {
			m[line] = true
		}
	}
	return m
}()

// hasSummarySection 输出是否至少含一个模板小节标题（容忍行首尾空白；
// 只要一个小节即视为格式合格，与 OpenCode 的 hasSummarySection 一致）。
func hasSummarySection(text string) bool {
	for _, line := range strings.Split(text, "\n") {
		if compactHeadings[strings.TrimSpace(line)] {
			return true
		}
	}
	return false
}
