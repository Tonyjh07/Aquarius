package app

// session_cmd_tag.go 会话标签与置顶（D313）：/tag 加删标签、/pin 切换置顶。
// 两者都是会话元数据——不动会话树、不改更新时间（「最近在聊什么」与「归到哪」是两个维度）；
// 目标支持唯一前缀，改非当前会话走载入-改-存（照 /title 模板），不碰 s.cur。

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/Tonyjh07/Aquarius/internal/domain/conversation"
	"github.com/Tonyjh07/Aquarius/internal/i18n"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

// tagFlags /tag 认识的 flag：--rm <名>（可重复）删标签。任意位置识别——位置参数（会话）
// 在前，flag 写在后面（`/tag 甲 --rm 旧标签` 是正常写法）。
var tagFlags = []flagDecl{{name: "--rm", value: true}}

// addTags 打标签：去重、保持首次加入顺序（重复打同一标签无副作用）。
func addTags(c *conversation.Conversation, names []string) {
	have := make(map[string]bool, len(c.Tags))
	for _, t := range c.Tags {
		have[t] = true
	}
	for _, t := range names {
		if t == "" || have[t] {
			continue
		}
		have[t] = true
		c.Tags = append(c.Tags, t)
	}
}

// removeTags 删标签：没打的照常跳过（幂等）。
func removeTags(c *conversation.Conversation, names []string) {
	drop := make(map[string]bool, len(names))
	for _, t := range names {
		drop[t] = true
	}
	kept := c.Tags[:0]
	for _, t := range c.Tags {
		if !drop[t] {
			kept = append(kept, t)
		}
	}
	c.Tags = kept
}

// resolveTagTarget 解析 /tag 与 /pin 的目标会话：唯一前缀；非当前会话载入副本，
// 不动 s.cur（模板同 /title 的 --id 分支）。
func (s *Session) resolveTagTarget(ctx context.Context, raw string) (*conversation.Conversation, error) {
	id, err := s.resolveConversation(ctx, raw)
	if err != nil {
		return nil, err
	}
	if id == s.cur.ID {
		return s.cur, nil
	}
	c, err := s.store.Load(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("session: %s: %w", i18n.Tf("err.load_conversation", "id", id), err)
	}
	return c, nil
}

// execTag 实现 /tag：<会话> [名...] 打标签，--rm <名> 删标签，无名无 --rm 显示现有标签（D313）。
// 加时去重幂等；改名不改更新时间、不动会话树；改完即时写盘（与 /title 同规则）。
func (s *Session) execTag(ctx context.Context, cmd port.Command) (string, error) {
	flags, rest, err := scanFlags(cmd.Args, flagScan{pos: flagsAnywhere}, tagFlags)
	if err != nil || len(rest) == 0 {
		return "", errors.New(i18n.T("usage.tag"))
	}
	target, err := s.resolveTagTarget(ctx, rest[0])
	if err != nil {
		return "", err
	}
	adds, rms := rest[1:], make([]string, 0, len(flags))
	for _, f := range flags {
		rms = append(rms, f.value)
	}
	if len(adds) == 0 && len(rms) == 0 {
		if len(target.Tags) == 0 {
			return i18n.Tf("tree.tag.none", "id", target.ID), nil
		}
		return i18n.Tf("tree.tag.show", "id", target.ID, "tags", joinTags(target.Tags)), nil
	}
	addTags(target, adds)
	removeTags(target, rms)
	if err := s.store.Save(ctx, target); err != nil {
		return "", wrapSession("err.save_tag", err)
	}
	if len(target.Tags) == 0 {
		return i18n.Tf("tree.tag.cleared", "id", target.ID), nil
	}
	return i18n.Tf("tree.tag.done", "id", target.ID, "tags", joinTags(target.Tags)), nil
}

// execPin 实现 /pin：<会话> 切换置顶（D313）。回执说明新状态——切换式动作必须把结果说清楚。
func (s *Session) execPin(ctx context.Context, cmd port.Command) (string, error) {
	if len(cmd.Args) != 1 {
		return "", errors.New(i18n.T("usage.pin"))
	}
	target, err := s.resolveTagTarget(ctx, cmd.Args[0])
	if err != nil {
		return "", err
	}
	target.Pinned = !target.Pinned
	if err := s.store.Save(ctx, target); err != nil {
		return "", wrapSession("err.save_pin", err)
	}
	if target.Pinned {
		return i18n.Tf("tree.pin.on", "id", target.ID), nil
	}
	return i18n.Tf("tree.pin.off", "id", target.ID), nil
}

// joinTags 标签列表的展示拼法：单空格分隔（与 /tag 输入侧一致，能原样抄回去再打）。
func joinTags(tags []string) string {
	return strings.Join(tags, " ")
}
