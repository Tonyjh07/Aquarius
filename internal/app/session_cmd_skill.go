package app

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/Tonyjh07/Aquarius/internal/domain/conversation"
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
		return "", errors.New("session: 未配置技能目录，/skill 不可用")
	}
	name := strings.TrimSpace(strings.Join(args, " "))
	if name == "" || name == "list" {
		if name == "list" && len(args) > 1 {
			return "", fmt.Errorf("%s", skillAdminUsage)
		}
		return s.skillList(ctx)
	}
	if strings.ContainsAny(name, " \t") {
		return "", fmt.Errorf("%s（技能名里不含空格）", skillAdminUsage)
	}

	doc, err := s.skills.Load(ctx, name)
	if err != nil {
		if errors.Is(err, port.ErrSkillNotFound) {
			return "", fmt.Errorf("没有名为 %q 的技能（/skill 查看已安装的技能）", name)
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
	landed, _, err := s.drainInjections(ctx, true, false)
	if err != nil {
		return "", err
	}
	if len(landed) == 0 {
		return "技能已入队（未被排空）", nil
	}
	if err := s.emitClear(ctx); err != nil {
		return "", err
	}
	if _, err := s.replayHistory(ctx, "已载入技能"); err != nil {
		return "", err
	}
	return fmt.Sprintf("已载入技能 %s（正文已进上下文，未起响应；下一步直接说你要做什么）\n目录：%s",
		doc.Info.Name, doc.Info.Dir), nil
}

// skillList 清单（面向用户的中文输出）：名字、一句说明、来源；坏条目单列原因。
func (s *Session) skillList(ctx context.Context) (string, error) {
	list, err := s.skills.List(ctx)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	if len(list) == 0 {
		b.WriteString("还没有安装任何技能。\n")
		b.WriteString("把技能目录放到 <profile>/skills/<名字>/SKILL.md 就生效（或在 config 的 skills.dirs 里加目录）。")
	} else {
		fmt.Fprintf(&b, "已安装 %d 个技能：\n", len(list))
		for _, sk := range list {
			fmt.Fprintf(&b, "- %s — %s\n", sk.Name, oneLineUser(sk.Description))
			fmt.Fprintf(&b, "  来源：%s\n", sk.Source)
		}
		b.WriteString("用 /skill <名字> 把某个技能载入上下文；模型也会在需要时自己载入。")
	}
	if warns := s.skills.Warnings(ctx); len(warns) > 0 {
		fmt.Fprintf(&b, "\n未加载的条目（%d 条）：\n", len(warns))
		for _, w := range warns {
			fmt.Fprintf(&b, "- %s\n", w)
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
