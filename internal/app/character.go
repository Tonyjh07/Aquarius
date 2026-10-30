package app

import (
	"fmt"

	"github.com/Tonyjh07/Aquarius/internal/port"
)

// CharacterOptions 角色卡投影的开关（§5.17/D247），由 config `characters` 段填充。
// 卡文本本身不改写：这里只决定「哪几段进上下文」，不做净化。
type CharacterOptions struct {
	// PreferSystemPrompt 替换态：卡的 system_prompt 顶掉用户人格引用（`@env` 保留）。
	// 关（默认）= 附加态，用户人格原样在最前。
	PreferSystemPrompt bool
	// InjectConstantBook 常驻世界书进 `@char.book`（只取 constant 且 enabled 的条目）。
	InjectConstantBook bool
	// InjectExamples mes_example 落注入节点（内容见 CharacterStart.Examples）。
	InjectExamples bool
}

// CharacterStart 一张卡开新会话所需的投影文本（§5.17）。
//
// 全是**展开后的终稿**：人格与示例、尾部指令在这里做完宏替换，之后落树 / 落盘即定型，
// 改 `prompts.user_name` 不再影响它（快照口径）。卡被删后这些文本照常可用。
type CharacterStart struct {
	Slug    string // 会话记住的角色 slug（Conversation.Character）
	Persona string // 展开后的人格（含 @char.* 模块），落 persona 首节点
	// Openings 开场白合并列表（§5.17）：[0] 恒为 first_mes，其后依次是 alternate_greetings。
	// 序号 n（**1 起**）取 Openings[n-1]——契约把它写成 `alternate_greetings[n-1]`，按字面
	// 读会跳过第 0 条 `first_mes`，故按合并列表定序（缺省与 `1` 都是 first_mes）。
	Openings []string
	Examples string   // mes_example 展开稿（空 = 不注入；开关关时恒空）
	Tail     string   // post_history_instructions 展开稿（空 = 无尾部指令）
	Missing  []string // 人格缺失模块名（供装配根记 warning，不静默）
}

// CharacterPromptFromCard 把一张卡归一成组合器需要的取值（§5.17/D247）。
// 原文照抄，不拼装、不改写——拼法发生在组合时（落树即终稿）。
func CharacterPromptFromCard(card port.CharacterCard, opts CharacterOptions) *CharacterPrompt {
	p := card.Profile
	return &CharacterPrompt{
		Display:      card.Info.Display,
		Description:  p.Description,
		Personality:  p.Personality,
		Scenario:     p.Scenario,
		System:       p.SystemPrompt,
		BookEntries:  p.BookEntries,
		PreferSystem: opts.PreferSystemPrompt,
		InjectBook:   opts.InjectConstantBook,
	}
}

// ProjectCharacter 把一张卡投影成建会话的文本（§5.17）：在同一份组合器上展开人格、
// 示例与尾部指令，三者共用 `{{char}}` / `{{user}}` / `{{date}}` / `{{original}}` 的同一套
// 取值（§7.7）。卡只影响这一份投影，不改动原组合器。
func (c *PersonaComposer) ProjectCharacter(card port.CharacterCard, opts CharacterOptions) CharacterStart {
	composer := c.WithCharacter(CharacterPromptFromCard(card, opts))
	persona, missing := composer.Compose()
	start := CharacterStart{Slug: card.Info.Slug, Persona: persona, Missing: missing}
	if opts.InjectExamples {
		start.Examples = composer.ExpandCardText(card.Profile.Examples)
	}
	start.Tail = composer.ExpandCardText(card.Profile.PostHistory)
	start.Openings = make([]string, 0, 1+len(card.Profile.Greetings))
	start.Openings = append(start.Openings, composer.ExpandCardText(card.Profile.FirstMes))
	for _, g := range card.Profile.Greetings {
		start.Openings = append(start.Openings, composer.ExpandCardText(g))
	}
	return start
}

// Greeting 取开场白（§5.17）：序号 **1 起**，n <= 1 视作 first_mes，n > 1 取对应备用问候；
// 越界报错并列出可选条数（不静默回落到第一条——用户点名的那条没取到却在别处开始说话，
// 比直接报错更难发现）。空开场返回空串，由调用方决定不落节点。
func (cs CharacterStart) Greeting(n int) (string, error) {
	if len(cs.Openings) == 0 {
		return "", nil
	}
	if n <= 1 {
		n = 1
	}
	if n > len(cs.Openings) {
		return "", fmt.Errorf("开场白序号 %d 越界：这张卡共有 %d 条可选", n, len(cs.Openings))
	}
	return cs.Openings[n-1], nil
}

// ExpandCardText 展开一段卡文本里的宏（§7.7）：示例、尾部指令、开场白都走这里，
// 与人格里的卡模块共用同一份取值。
//
// `{{original}}` 取替换态下的用户人格 U（默认态按空——U 已原样在场，再展开一次就是重复，
// §5.17）。非角色会话（无卡）原样返回：这些文本只在角色会话里消费。
func (c *PersonaComposer) ExpandCardText(text string) string {
	cp := c.spec.Character
	if cp == nil || text == "" {
		return text
	}
	orig := ""
	if replacementMode(cp) {
		orig = c.userPersona(c.baseModules())
	}
	return expandMacros(text, cp.Display, c.spec.UserName, c.now().Format("2006-01-02"), orig)
}
