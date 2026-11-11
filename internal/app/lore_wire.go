package app

import (
	"context"
	"sync"

	"github.com/Tonyjh07/Aquarius/internal/domain/conversation"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

// 世界书条目从哪来（§5.17/D294）：会话只存卡的 slug，条目在装配期按 slug 现取。
//
// 不走「建会话时把条目快照进元数据」那条路：世界书可以有 MB 级正文，而会话树每轮都要写盘，
// 快照会把写盘成本绑到卡体积上。也不每轮读一遍 card.json：语料里有 17 MB 的卡。

// loreSource 世界书的条目来源，按 slug 在进程内缓存。
//
// 缓存不过期：本版卡不可编辑，同名重导入会产生新 slug。唯一例外是先删卡、再导入一张同名
// 卡——slug 会被复用，已开的会话沿用启动时的条目（人格同样是旧快照，口径一致）。
// 换一张卡重开会话即可。
type loreSource struct {
	catalog port.CharacterCatalog
	mu      sync.Mutex
	cards   map[string]loreCard
}

// loreCard 一张卡里检索要用的东西：`{{char}}` 的取值 + 全部条目。
type loreCard struct {
	Display string
	Entries []port.BookEntry
}

// load 取一张卡的条目。未导入或读失败返回零值：世界书缺失不该挡住对话，
// 人格快照仍在，会话照常进行（D294 的降级口径）。
func (l *loreSource) load(ctx context.Context, slug string) loreCard {
	if slug == "" {
		return loreCard{}
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if c, ok := l.cards[slug]; ok {
		return c
	}
	card, err := l.catalog.Load(ctx, slug)
	if err != nil {
		return loreCard{} // 不缓存失败：卡可能稍后才导入
	}
	c := loreCard{Display: card.Info.Display, Entries: card.Profile.BookEntries}
	if l.cards == nil {
		l.cards = make(map[string]loreCard)
	}
	l.cards[slug] = c
	return c
}

// attachLore 把本轮激活的世界书条目并进首条 system（§5.17/D294）：位置在 persona 之后、
// 记忆与常驻声明之前（那两段由调用方随后追加）。
//
// 不进树：命中集合每轮都可能变，落树等于每轮多一个节点，回溯时还会读到与当前消息矛盾的
// 旧集合。判定全在 ActivateLore，这里只负责摆放。
func (a *Agent) attachLore(ctx context.Context, c *conversation.Conversation, treePath []conversation.Message, msgs []port.PromptMessage) []port.PromptMessage {
	if len(msgs) == 0 || msgs[0].Role != port.PromptSystem {
		return msgs
	}
	act := a.loreActivation(ctx, c, treePath)
	if act.Text == "" {
		return msgs
	}
	msgs[0].Content = append(msgs[0].Content, port.PromptPart{Kind: port.PromptText, Text: "\n\n" + act.Text})
	return msgs
}

// attachSystemNotes 把记忆块与「外部内容是数据」的常驻声明追加进首条 system（§7.1/D23/D170）。
//
// 两段都在树外、每轮重算：`/system` 改人格不会把它们改掉。常驻声明单列一条 system 会让
// 「首条 system 是人格」的既有口径分叉，所以并进同一条正文；也不逐条给工具结果套围栏
// ——那会把上下文撑爆，还让正常代码读起来像可疑数据。记忆读取失败静默跳过，不阻断对话。
func (a *Agent) attachSystemNotes(ctx context.Context, c *conversation.Conversation, msgs []port.PromptMessage) []port.PromptMessage {
	if len(msgs) == 0 || msgs[0].Role != port.PromptSystem {
		return msgs
	}
	if block := a.memoryBlock(ctx, c.ID); block != "" {
		msgs[0].Content = append(msgs[0].Content, port.PromptPart{Kind: port.PromptText, Text: block})
	}
	msgs[0].Content = append(msgs[0].Content, port.PromptPart{Kind: port.PromptText, Text: "\n\n" + untrustedDataNote})
	return msgs
}

// loreActivation 本轮的世界书激活结果（§5.17/D294）。没有卡、没有条目或开关关掉时返回零值。
//
// path 用**装配用的那份**（含未提交缓冲），不是 c.Path()：本轮刚说的话要能触发条目。
func (a *Agent) loreActivation(ctx context.Context, c *conversation.Conversation, path []conversation.Message) LoreActivation {
	opt := a.cfg.Lore
	if !opt.Enabled || a.lore == nil || c == nil || c.Character == "" {
		return LoreActivation{}
	}
	card := a.lore.load(ctx, c.Character)
	if len(card.Entries) == 0 {
		return LoreActivation{}
	}
	opt = opt.withDefaults()
	scan := loreScanTexts(path, opt.ScanDepth)
	expand := func(s string) string { return a.persona.ExpandLoreText(s, card.Display) }
	return ActivateLore(card.Entries, scan, opt, a.maxCtx, expand)
}
