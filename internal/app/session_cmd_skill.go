package app

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/Tonyjh07/Aquarius/internal/domain/conversation"
	"github.com/Tonyjh07/Aquarius/internal/i18n"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

// execSkill 实现 /skill（D201）：无参/`list` 列清单，带名字 = 把该技能正文注入上下文。
//
// 载入不触发新一轮：用户载入技能之后紧接着要说自己的要求，替他把那轮跑掉只会浪费一次
// 生成（与 /inject --no-trigger 同口径）。注入落地后走「清屏 + 回放」（移 Head 类命令
// 统一口径，D81/D92），让转写区与 Head 恒一致。
func (s *Session) execSkill(ctx context.Context, args []string) (string, error) {
	// 安装面（D202）：install / remove / update 先分派，避免被「带名字 = 载入」吃掉；
	// 也不依赖清单（装之前可能一个技能都没有）。
	if out, handled, err := s.execSkillAdmin(ctx, args); handled {
		return out, err
	}
	if s.skills == nil {
		return "", errors.New("session: " + i18n.T("skill.unavailable"))
	}
	name := joinArgs(args)
	if name == "" || name == "list" {
		if name == "list" && len(args) > 1 {
			return "", errors.New(skillAdminUsage())
		}
		return s.skillList(ctx)
	}
	if strings.ContainsAny(name, " \t") {
		return "", errors.New(skillAdminUsage() + i18n.T("skill.no_space"))
	}

	doc, err := s.skills.Load(ctx, name)
	if err != nil {
		if errors.Is(err, port.ErrSkillNotFound) {
			return "", errors.New(i18n.Tf("skill.not_found", "name", fmt.Sprintf("%q", name)))
		}
		return "", err
	}

	var b strings.Builder
	fmt.Fprintf(&b, skillInjectNoteFmt, doc.Info.Name)
	b.WriteString(doc.Body)
	fmt.Fprintf(&b, skillInjectDirFmt, doc.Info.Dir)

	noTrigger := false
	in := port.Injection{
		Source: "skill:" + doc.Info.Name,
		// 用户亲手载入 → 宿主事实（D170）：技能正文正是「要遵循的指令」，不该被当成
		// 外部数据套围栏（与 /inject 同口径）。
		Trust:   port.TrustHost,
		Parts:   []conversation.Part{{Kind: conversation.PartText, Text: b.String()}},
		Trigger: &noTrigger,
	}
	if err := s.Inject(ctx, in); err != nil {
		return "", err
	}
	// 队列里此刻只可能有本条刚入队的技能正文（Trigger 显式 false）。外部生产者攒下的注入
	// 已在 Handle 入口的排空点①落地——用户输入优先，注入不抢手输入。故这里不会有该起一轮
	// 的注入，wantTurn 恒为 false。
	landed, _, err := s.drainInjections(ctx, drainBeforeReplay)
	if err != nil {
		return "", err
	}
	if len(landed) == 0 {
		return i18n.T("skill.queued"), nil
	}
	if err := s.emitClear(ctx); err != nil {
		return "", err
	}
	if _, err := s.replayHistory(ctx, "replay.verb.skill_loaded"); err != nil {
		return "", err
	}
	return i18n.Tf("skill.loaded", "name", doc.Info.Name, "dir", doc.Info.Dir), nil
}

// skillList 清单（面向用户的中文输出）：名字、一句说明、来源；坏条目单列原因。
func (s *Session) skillList(ctx context.Context) (string, error) {
	list, err := s.skills.List(ctx)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	if len(list) == 0 {
		b.WriteString(i18n.T("skill.list.empty_head"))
		b.WriteString(i18n.T("skill.list.empty_hint"))
	} else {
		b.WriteString(i18n.Tf("skill.list.head", "n", len(list)))
		for _, sk := range list {
			b.WriteString(i18n.Tf("skill.list.item", "name", sk.Name, "desc", oneLineUser(sk.Description)))
			b.WriteString(i18n.Tf("skill.list.source", "source", sk.Source))
		}
		b.WriteString(i18n.T("skill.list.hint"))
	}
	if warns := s.skills.Warnings(ctx); len(warns) > 0 {
		b.WriteString(i18n.Tf("skill.list.warnings_head", "n", len(warns)))
		for _, w := range warns {
			b.WriteString(i18n.Tf("skill.list.warning_item", "text", w))
		}
	}
	return strings.TrimRight(b.String(), "\n"), nil
}

// oneLineUser 把技能说明压成一行（用户面展示；回车换行会把列表撑成两行）。
func oneLineUser(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	runes := []rune(s)
	if len(runes) <= 120 {
		return s
	}
	return strings.TrimSpace(string(runes[:120])) + "..."
}
