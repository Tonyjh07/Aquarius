package app

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/Tonyjh07/Aquarius/internal/i18n"
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
			return "", errors.New(characterAdminUsage())
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
		return "", errors.New(characterAdminUsage())
	}
}

// characterList 清单（面向用户的中文输出）：显示名、slug、来源与规范、世界书与备用开场白条数。
func (s *Session) characterList(ctx context.Context) (string, error) {
	if s.characters == nil {
		return "", errors.New("session: " + i18n.T("char.unavailable"))
	}
	list, err := s.characters.List(ctx)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	if len(list) == 0 {
		b.WriteString(i18n.T("char.list.empty_head"))
		b.WriteString(i18n.T("char.list.empty_hint"))
		return b.String(), nil
	}
	b.WriteString(i18n.Tf("char.list.head", "n", len(list)))
	for _, c := range list {
		b.WriteString(i18n.Tf("char.list.item",
			"display", c.Display, "slug", c.Slug, "source", c.Source, "spec", c.Spec,
			"book", c.BookEntries, "greetings", c.Greetings))
	}
	b.WriteString(i18n.T("char.list.hint"))
	return b.String(), nil
}

// characterShow /character show <角色>：详情 + 四类回执 + 两类提示。
func (s *Session) characterShow(ctx context.Context, args []string) (string, error) {
	slug := joinArgs(args)
	if slug == "" {
		return "", errors.New(characterAdminUsage())
	}
	if s.characters == nil {
		return "", errors.New("session: " + i18n.T("char.unavailable"))
	}
	card, err := s.characters.Load(ctx, slug)
	if err != nil {
		if errors.Is(err, port.ErrCharacterNotFound) {
			return "", errors.New(i18n.Tf("char.not_found", "slug", fmt.Sprintf("%q", slug)))
		}
		return "", err
	}
	var b strings.Builder
	info := card.Info
	b.WriteString(i18n.Tf("char.show.head", "display", info.Display, "slug", info.Slug))
	b.WriteString(i18n.Tf("char.show.spec_source", "spec", info.Spec, "source", info.Source))
	if !info.ImportedAt.IsZero() {
		b.WriteString(i18n.Tf("char.show.imported", "at", info.ImportedAt.Format("2006-01-02 15:04")))
	}
	b.WriteByte('\n')
	writeDisplayMeta(&b, info)
	s.writeCharacterReport(&b, card)
	writeWarnings(&b, info.Warnings)
	b.WriteString(i18n.Tf("char.start_hint", "slug", info.Slug))
	return b.String(), nil
}

// characterImport /character import <卡文件>：导入并回执。
func (s *Session) characterImport(ctx context.Context, args []string) (string, error) {
	source := joinArgs(args)
	if source == "" {
		return "", errors.New(characterAdminUsage())
	}
	if s.charAdmin == nil {
		return "", errors.New(i18n.T("char.admin_missing"))
	}
	card, err := s.charAdmin.Import(ctx, source)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	b.WriteString(i18n.Tf("char.imported", "display", card.Info.Display, "slug", card.Info.Slug))
	s.writeCharacterReport(&b, card)
	writeWarnings(&b, card.Info.Warnings)
	b.WriteString(i18n.Tf("char.start_hint", "slug", card.Info.Slug))
	return b.String(), nil
}

// characterStart /character start <角色> [开场白序号]。
func (s *Session) characterStart(ctx context.Context, args []string) (string, error) {
	if len(args) == 0 || len(args) > 2 {
		return "", errors.New(characterAdminUsage())
	}
	slug := strings.TrimSpace(args[0])
	n := 0
	if len(args) == 2 {
		v, ok := parseIndex(args[1])
		if !ok {
			return "", errors.New(characterAdminUsage() + i18n.T("char.bad_opening_index"))
		}
		n = v
	}
	return s.startCharacter(ctx, slug, n)
}

// characterRemove /character remove <角色>（删除前二次确认；确认器缺席时直接执行）。
func (s *Session) characterRemove(ctx context.Context, args []string) (string, error) {
	slug := joinArgs(args)
	if slug == "" {
		return "", errors.New(characterAdminUsage())
	}
	if s.charAdmin == nil {
		return "", errors.New(i18n.T("char.admin_missing"))
	}
	if s.confirmer != nil {
		ans, err := s.confirmer.Confirm(ctx,
			i18n.Tf("char.confirm_remove", "slug", slug))
		if err != nil {
			return "", fmt.Errorf("%s: %w", i18n.T("char.confirm_remove_fail"), err)
		}
		if !ans.Allow {
			return i18n.Tf("char.remove_canceled", "slug", slug), nil
		}
	}
	if err := s.charAdmin.Remove(ctx, slug); err != nil {
		return "", err
	}
	return i18n.Tf("char.removed", "slug", slug), nil
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
		parts = append(parts, i18n.Tf("char.meta.author", "name", info.Creator))
	}
	if info.Version != "" {
		parts = append(parts, i18n.Tf("char.meta.version", "version", info.Version))
	}
	if len(info.Tags) > 0 {
		parts = append(parts, i18n.Tf("char.meta.tags", "tags", strings.Join(info.Tags, i18n.T("char.list_join"))))
	}
	if len(parts) > 0 {
		b.WriteString(strings.Join(parts, i18n.T("char.meta.join")))
		b.WriteByte('\n')
	}
}

// writeWarnings 导入 / 查看回执里的警告（跳过 / 不认识的东西），逐条列出。
func writeWarnings(b *strings.Builder, warnings []string) {
	if len(warnings) == 0 {
		return
	}
	b.WriteString(i18n.Tf("char.warnings.head", "n", len(warnings)))
	for _, w := range warnings {
		b.WriteString(i18n.Tf("char.warnings.item", "text", w))
	}
}
