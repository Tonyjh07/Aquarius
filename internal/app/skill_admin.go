package app

// skill_admin.go /skill 的安装面（D202）：接口由消费方定义，装配根实现到 skillfs.Installer。
//
// 为什么不在 port 里：安装/卸载/更新是**用户面动作**——模型侧永远只有「列」与「载」
// （D202 口径一，让模型能拉远端指令包等于把注入面交给远端内容）。命令面与管理面共用同一
// 份实现，故接口放在消费方 app。

import (
	"context"
	"fmt"
	"strings"

	"github.com/Tonyjh07/Aquarius/internal/i18n"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

// SkillInstallReport 安装/更新结果（用户面文案的原料：名字、过程说明、跳过原因）。
type SkillInstallReport struct {
	// Skills 装上/更新的技能名（按名字排序）。
	Skills []string
	// Notes 过程说明（例如「未检测到 git，已改用压缩包下载」）。
	Notes []string
	// Skipped 来源里未安装的条目及原因（好的一样装上）。
	Skipped []string
}

// SkillAdmin 技能安装面（装配根实现；线程安全，命令面与设置窗共用）。
type SkillAdmin interface {
	// Install 安装一个来源：SKILL.md 单文件 / 文件夹 / .zip / GitHub 链接（可带 `#分支`）。
	Install(ctx context.Context, source string) (SkillInstallReport, error)
	// Remove 卸载一个安装副本（只删安装目录里的那份；外部技能根不动）。
	Remove(ctx context.Context, name string) error
	// Update 更新一个（name 空 = 更新全部可更新项）。
	Update(ctx context.Context, name string) (SkillInstallReport, error)
}

// skillAdminUsage 安装面用法（用户面；子命令错误统一提示）。
func skillAdminUsage() string { return i18n.T("usage.skill") }

// execSkillAdmin 分派 /skill 的安装面子命令（D202）。
//
// install 的来源可能是路径或链接，**整段当来源**（Windows 路径与链接里都可能有空格），
// 不再按空格切分。
func (s *Session) execSkillAdmin(ctx context.Context, args []string) (string, bool, error) {
	if len(args) == 0 {
		return "", false, nil
	}
	switch strings.ToLower(strings.TrimSpace(args[0])) {
	case "install":
		return s.skillInstall(ctx, args[1:])
	case "remove", "uninstall":
		return s.skillRemove(ctx, args[1:])
	case "update":
		return s.skillUpdate(ctx, args[1:])
	}
	return "", false, nil
}

// skillInstall /skill install <来源>。
func (s *Session) skillInstall(ctx context.Context, rest []string) (string, bool, error) {
	source := joinArgs(rest)
	if source == "" {
		return "", true, fmt.Errorf("%s", skillAdminUsage())
	}
	if s.skillAdmin == nil {
		return "", true, fmt.Errorf("%s", i18n.T("skill.admin_missing"))
	}
	rep, err := s.skillAdmin.Install(ctx, source)
	if err != nil {
		return "", true, err
	}
	return formatSkillReport(i18n.T("skill.verb.installed"), rep), true, nil
}

// skillRemove /skill remove <名字>（删除前二次确认；确认器缺席时直接执行——测试/无交互装配）。
func (s *Session) skillRemove(ctx context.Context, rest []string) (string, bool, error) {
	name := joinArgs(rest)
	if name == "" {
		return "", true, fmt.Errorf("%s", skillAdminUsage())
	}
	if s.skillAdmin == nil {
		return "", true, fmt.Errorf("%s", i18n.T("skill.admin_missing"))
	}
	if s.confirmer != nil {
		ans, err := s.confirmer.Confirm(ctx, port.ConfirmRequest{
			Origin: port.OriginCommand,
			Prompt: i18n.Tf("skill.confirm_remove", "name", name),
		})
		if err != nil {
			return "", true, fmt.Errorf("%s: %w", i18n.T("skill.confirm_remove_fail"), err)
		}
		if !ans.Allow {
			return i18n.Tf("skill.remove_canceled", "name", name), true, nil
		}
	}
	if err := s.skillAdmin.Remove(ctx, name); err != nil {
		return "", true, err
	}
	return i18n.Tf("skill.removed", "name", name), true, nil
}

// skillUpdate /skill update [名字|all]。
func (s *Session) skillUpdate(ctx context.Context, rest []string) (string, bool, error) {
	name := joinArgs(rest)
	if name == "all" {
		name = ""
	}
	if s.skillAdmin == nil {
		return "", true, fmt.Errorf("%s", i18n.T("skill.admin_missing"))
	}
	rep, err := s.skillAdmin.Update(ctx, name)
	if err != nil {
		return "", true, err
	}
	return formatSkillReport(i18n.T("skill.verb.updated"), rep), true, nil
}

// formatSkillReport 把安装/更新结果排成用户面文本（中文；不含内部编号）。
func formatSkillReport(verb string, rep SkillInstallReport) string {
	var b strings.Builder
	if len(rep.Skills) == 0 {
		b.WriteString(i18n.Tf("skill.report.zero", "verb", verb))
	} else {
		b.WriteString(i18n.Tf("skill.report.head", "verb", verb, "n", len(rep.Skills)))
		for _, name := range rep.Skills {
			b.WriteString(i18n.Tf("skill.report.item", "name", name))
		}
	}
	for _, n := range rep.Notes {
		b.WriteString(n)
		b.WriteByte('\n')
	}
	if len(rep.Skipped) > 0 {
		b.WriteString(i18n.Tf("skill.report.skipped_head", "n", len(rep.Skipped)))
		for _, sk := range rep.Skipped {
			b.WriteString(i18n.Tf("skill.report.item", "name", sk))
		}
	}
	b.WriteString(i18n.T("skill.report.footer"))
	return strings.TrimRight(b.String(), "\n")
}
