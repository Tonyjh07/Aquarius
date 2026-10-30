package app

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/Tonyjh07/Aquarius/internal/domain/conversation"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

// startCharacter 用一张已导入的卡开新会话（§5.17）：投影人格/示例/尾部指令 → 选开场白 →
// 建会话（**独立路径**，见 newCharacterConversation）→ 落盘 → 清屏回放。
//
// 人格是**快照**：卡文本在投影时做完宏替换就落进 persona 首节点，之后与目录脱钩——改 config、
// 换卡版本、删卡都不改写已有会话；要按新卡重来就再开一次。n 是开场白序号（1 起，缺省/1 =
// first_mes），越界报错并列出可选条数。
func (s *Session) startCharacter(ctx context.Context, slug string, n int) (string, error) {
	if s.characters == nil {
		return "", errors.New("session: 未配置角色卡目录，/character 不可用")
	}
	card, err := s.characters.Load(ctx, slug)
	if err != nil {
		return "", fmt.Errorf("session: 载入角色卡: %w", err)
	}
	start := s.persona.ProjectCharacter(card, s.charOpts)
	greeting, err := start.Greeting(n)
	if err != nil {
		return "", err
	}
	c, err := s.newCharacterConversation(card, start, greeting)
	if err != nil {
		return "", err
	}
	if err := s.store.Save(ctx, c); err != nil {
		return "", fmt.Errorf("session: 新建角色会话: %w", err)
	}
	s.cancelWatchersFor(s.cur.ID) // D167：旧会话的后台任务报告不再属于这里
	s.cur = c
	if err := s.emitClear(ctx); err != nil { // 新会话从空白开始（D75）
		return "", err
	}
	if _, err := s.replayHistory(ctx, "已开始"); err != nil {
		return "", err
	}
	// 两种人格模式各说清一件事（§5.17）：默认态 {{original}} 按空；替换态展开为用户人格。
	mode := "用户人格在最前，卡的角色设定接在后面（{{original}} 按空处理）"
	if s.charOpts.PreferSystemPrompt {
		mode = "卡的 system_prompt 顶掉用户人格（{{original}} 展开为用户人格正文，环境信息保留）"
	}
	return fmt.Sprintf("已用角色「%s」开始新会话 %s\n人格模式：%s", card.Info.Display, c.ID, mode), nil
}

// newCharacterConversation 建角色会话：Root + persona 快照 + 示例注入节点 + 开场白，
// 并把角色 slug 与尾部指令记进会话元数据。**独立于 newConversation**——那条被 /new、
// /switch、/import 共用，语境里没有卡，硬塞一个「可选角色」参数会让三个调用点都背上它。
func (s *Session) newCharacterConversation(card port.CharacterCard, start CharacterStart, greeting string) (*conversation.Conversation, error) {
	c := conversation.New(s.ids.ConversationID(), card.Info.Display)
	c.Character = start.Slug
	c.Tail = start.Tail
	prompt := start.Persona
	if strings.TrimSpace(prompt) == "" {
		prompt = defaultSystem
	}
	persona := conversation.Message{
		ID:        s.ids.MessageID(),
		Parent:    conversation.MessageID(c.ID),
		Role:      conversation.RoleSystem,
		Content:   []conversation.Part{{Kind: conversation.PartText, Text: prompt}},
		CreatedAt: s.clock.Now(),
	}
	if err := c.AppendCommitted(persona); err != nil {
		return nil, fmt.Errorf("session: 写入角色 persona 节点: %w", err)
	}
	// 示例走注入节点（§5.17）：persona 之后、开场白之前。Trust=host 与 /skill 同口径——
	// 卡是用户亲手导入并选择开始的，写的是要遵循的指令，不是待围栏的外部数据；只追加、
	// 不动水位，因此可被后续压缩挤掉。
	if strings.TrimSpace(start.Examples) != "" {
		ex := conversation.Message{
			ID:        s.ids.MessageID(),
			Parent:    c.Head,
			Role:      conversation.RoleInject,
			Content:   []conversation.Part{{Kind: conversation.PartText, Text: start.Examples}},
			Source:    "character:" + start.Slug,
			Trust:     conversation.TrustHost,
			CreatedAt: s.clock.Now(),
		}
		if err := c.AppendCommitted(ex); err != nil {
			return nil, fmt.Errorf("session: 写入角色示例节点: %w", err)
		}
	}
	// 开场白作 assistant 节点、Head 落在它上面（§5.17）：空开场不造假消息。
	if strings.TrimSpace(greeting) != "" {
		if _, err := c.Append(conversation.RoleAssistant, []conversation.Part{{Kind: conversation.PartText, Text: greeting}}); err != nil {
			return nil, fmt.Errorf("session: 写入开场白: %w", err)
		}
	}
	return c, nil
}
