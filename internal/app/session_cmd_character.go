package app

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/Tonyjh07/Aquarius/internal/port"
)

// execCharacter 实现 /character（§5.17）：列出 / 查看 / 导入 / 用卡开新会话 / 删除。
//
// 导入 / 删除是用户面动作，走 app.CharacterAdmin（装配根实现的一份导入器）；读侧走
// port.CharacterCatalog。回执按四类分列「读了什么、没读什么」，删卡二次确认。
func (s *Session) execCharacter(ctx context.Context, args []string) (string, error) {
	sub := "list"
	if len(args) > 0 {
		sub = strings.ToLower(strings.TrimSpace(args[0]))
		args = args[1:]
	}
	switch sub {
	case "list":
		if len(args) > 0 {
			return "", fmt.Errorf("%s", characterAdminUsage)
		}
		return s.characterList(ctx)
	case "show":
		return s.characterShow(ctx, args)
	case "import":
		return s.characterImport(ctx, args)
	case "start":
		return s.characterStart(ctx, args)
	case "remove", "rm":
		return s.characterRemove(ctx, args)
	default:
		return "", fmt.Errorf("%s", characterAdminUsage)
	}
}

// characterList 清单（面向用户的中文输出）：显示名、slug、来源与规范、世界书与备用开场白条数。
func (s *Session) characterList(ctx context.Context) (string, error) {
	if s.characters == nil {
		return "", errors.New("session: 未配置角色卡目录，/character 不可用")
	}
	list, err := s.characters.List(ctx)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	if len(list) == 0 {
		b.WriteString("还没有导入任何角色卡。\n")
		b.WriteString("用 /character import <卡文件> 导入一张（PNG / JSON / .charx）。")
		return b.String(), nil
	}
	fmt.Fprintf(&b, "已导入 %d 张角色卡：\n", len(list))
	for _, c := range list {
		fmt.Fprintf(&b, "- %s（%s） · %s/%s · 世界书 %d 条 · 备用开场白 %d 条\n",
			c.Display, c.Slug, c.Source, c.Spec, c.BookEntries, c.Greetings)
	}
	b.WriteString("用 /character show <角色> 看详情，/character start <角色> 开新会话。")
	return b.String(), nil
}

// characterShow /character show <角色>：详情 + 四类回执 + 两类提示。
func (s *Session) characterShow(ctx context.Context, args []string) (string, error) {
	slug := joinArgs(args)
	if slug == "" {
		return "", fmt.Errorf("%s", characterAdminUsage)
	}
	if s.characters == nil {
		return "", errors.New("session: 未配置角色卡目录，/character 不可用")
	}
	card, err := s.characters.Load(ctx, slug)
	if err != nil {
		if errors.Is(err, port.ErrCharacterNotFound) {
			return "", fmt.Errorf("没有名为 %q 的角色卡（/character list 查看清单）", slug)
		}
		return "", err
	}
	var b strings.Builder
	info := card.Info
	fmt.Fprintf(&b, "角色卡：%s（%s）\n", info.Display, info.Slug)
	fmt.Fprintf(&b, "规范：%s ｜ 来源：%s", info.Spec, info.Source)
	if !info.ImportedAt.IsZero() {
		fmt.Fprintf(&b, " ｜ 导入：%s", info.ImportedAt.Format("2006-01-02 15:04"))
	}
	b.WriteByte('\n')
	writeDisplayMeta(&b, info)
	s.writeCharacterReport(&b, card)
	writeWarnings(&b, info.Warnings)
	fmt.Fprintf(&b, "\n用 /character start %s 开新会话。", info.Slug)
	return b.String(), nil
}

// characterImport /character import <卡文件>：导入并回执。
func (s *Session) characterImport(ctx context.Context, args []string) (string, error) {
	source := joinArgs(args)
	if source == "" {
		return "", fmt.Errorf("%s", characterAdminUsage)
	}
	if s.charAdmin == nil {
		return "", errors.New("本构建未接入角色卡导入面")
	}
	card, err := s.charAdmin.Import(ctx, source)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	fmt.Fprintf(&b, "已导入角色卡「%s」（%s）\n", card.Info.Display, card.Info.Slug)
	s.writeCharacterReport(&b, card)
	writeWarnings(&b, card.Info.Warnings)
	fmt.Fprintf(&b, "\n用 /character start %s 开新会话。", card.Info.Slug)
	return b.String(), nil
}

// characterStart /character start <角色> [开场白序号]。
func (s *Session) characterStart(ctx context.Context, args []string) (string, error) {
	if len(args) == 0 || len(args) > 2 {
		return "", fmt.Errorf("%s", characterAdminUsage)
	}
	slug := strings.TrimSpace(args[0])
	n := 0
	if len(args) == 2 {
		v, ok := parseIndex(args[1])
		if !ok {
			return "", fmt.Errorf("%s（开场白序号是整数）", characterAdminUsage)
		}
		n = v
	}
	return s.startCharacter(ctx, slug, n)
}

// characterRemove /character remove <角色>（删除前二次确认；确认器缺席时直接执行）。
func (s *Session) characterRemove(ctx context.Context, args []string) (string, error) {
	slug := joinArgs(args)
	if slug == "" {
		return "", fmt.Errorf("%s", characterAdminUsage)
	}
	if s.charAdmin == nil {
		return "", errors.New("本构建未接入角色卡导入面")
	}
	if s.confirmer != nil {
		ans, err := s.confirmer.Confirm(ctx,
			fmt.Sprintf("确认删除角色卡 %s？已用它开过的会话不受影响（卡文本在会话里是快照）", slug))
		if err != nil {
			return "", fmt.Errorf("确认删除角色卡: %w", err)
		}
		if !ans.Allow {
			return fmt.Sprintf("已取消删除角色卡 %s", slug), nil
		}
	}
	if err := s.charAdmin.Remove(ctx, slug); err != nil {
		return "", err
	}
	return fmt.Sprintf("已删除角色卡 %s", slug), nil
}

// writeCharacterReport 写四类回执 + 两类提示（§5.17）：正文排版交给 characterReportLines，
// 这里只管把它接进命令输出。
func (s *Session) writeCharacterReport(b *strings.Builder, card port.CharacterCard) {
	for _, line := range characterReportLines(card, s.charOpts, s.agent.CompactTokens()/4) {
		b.WriteString(line)
		b.WriteByte('\n')
	}
}

// writeDisplayMeta 只展示字段里能读到的项（作者 / 版本 / 标签）；都空则不出行。
func writeDisplayMeta(b *strings.Builder, info port.CharacterInfo) {
	var parts []string
	if info.Creator != "" {
		parts = append(parts, "作者："+info.Creator)
	}
	if info.Version != "" {
		parts = append(parts, "版本："+info.Version)
	}
	if len(info.Tags) > 0 {
		parts = append(parts, "标签："+strings.Join(info.Tags, "、"))
	}
	if len(parts) > 0 {
		b.WriteString(strings.Join(parts, " ｜ "))
		b.WriteByte('\n')
	}
}

// writeWarnings 导入 / 查看回执里的警告（跳过 / 不认识的东西），逐条列出。
func writeWarnings(b *strings.Builder, warnings []string) {
	if len(warnings) == 0 {
		return
	}
	fmt.Fprintf(b, "\n注意（%d 条）：\n", len(warnings))
	for _, w := range warnings {
		fmt.Fprintf(b, "- %s\n", w)
	}
}
