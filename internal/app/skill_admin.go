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
const skillAdminUsage = "用法: /skill list | /skill <名字> | /skill install <路径或链接> | /skill remove <名字> | /skill update [名字|all]"

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
		return "", true, fmt.Errorf("%s", skillAdminUsage)
	}
	if s.skillAdmin == nil {
		return "", true, fmt.Errorf("本构建未接入技能安装面")
	}
	rep, err := s.skillAdmin.Install(ctx, source)
	if err != nil {
		return "", true, err
	}
	return formatSkillReport("已安装", rep), true, nil
}

// skillRemove /skill remove <名字>（删除前二次确认；确认器缺席时直接执行——测试/无交互装配）。
func (s *Session) skillRemove(ctx context.Context, rest []string) (string, bool, error) {
	name := joinArgs(rest)
	if name == "" {
		return "", true, fmt.Errorf("%s", skillAdminUsage)
	}
	if s.skillAdmin == nil {
		return "", true, fmt.Errorf("本构建未接入技能安装面")
	}
	if s.confirmer != nil {
		ans, err := s.confirmer.Confirm(ctx,
			fmt.Sprintf("确认卸载技能 %s？只删除安装目录里的这份副本，外部技能目录不受影响", name))
		if err != nil {
			return "", true, fmt.Errorf("确认卸载技能: %w", err)
		}
		if !ans.Allow {
			return fmt.Sprintf("已取消卸载技能 %s", name), true, nil
		}
	}
	if err := s.skillAdmin.Remove(ctx, name); err != nil {
		return "", true, err
	}
	return fmt.Sprintf("已卸载技能 %s（安装目录里的副本已删除）", name), true, nil
}

// skillUpdate /skill update [名字|all]。
func (s *Session) skillUpdate(ctx context.Context, rest []string) (string, bool, error) {
	name := joinArgs(rest)
	if name == "all" {
		name = ""
	}
	if s.skillAdmin == nil {
		return "", true, fmt.Errorf("本构建未接入技能安装面")
	}
	rep, err := s.skillAdmin.Update(ctx, name)
	if err != nil {
		return "", true, err
	}
	return formatSkillReport("已更新", rep), true, nil
}

// formatSkillReport 把安装/更新结果排成用户面文本（中文；不含内部编号）。
func formatSkillReport(verb string, rep SkillInstallReport) string {
	var b strings.Builder
	if len(rep.Skills) == 0 {
		fmt.Fprintf(&b, "%s 0 个技能", verb)
	} else {
		fmt.Fprintf(&b, "%s %d 个技能：\n", verb, len(rep.Skills))
		for _, name := range rep.Skills {
			fmt.Fprintf(&b, "- %s\n", name)
		}
	}
	for _, n := range rep.Notes {
		fmt.Fprintf(&b, "%s\n", n)
	}
	if len(rep.Skipped) > 0 {
		fmt.Fprintf(&b, "\n未安装的条目（%d 条）：\n", len(rep.Skipped))
		for _, sk := range rep.Skipped {
			fmt.Fprintf(&b, "- %s\n", sk)
		}
	}
	b.WriteString("\n用 /skill list 查看清单；技能里的 scripts/ 仍要经工具确认才会执行。")
	return strings.TrimRight(b.String(), "\n")
}
