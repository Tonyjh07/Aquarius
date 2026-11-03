//go:build !windows

// 技能导入的专用选择框（非 Windows 降级）：走桌面环境命令（zenity / kdialog / osascript）。
package platform

import (
	"github.com/Tonyjh07/Aquarius/internal/i18n"
	"os/exec"
	"runtime"
	"strings"
)

// RequestPathPick 请求呈现技能选择框（非 Windows）：外部对话框阻塞，交独立 goroutine。
func (p *Plat) RequestPathPick(kind PickKind) { go p.showSkillPick(kind) }

// showSkillPick 呈现选择框：选中 → Host.SkillPathPicked；无可用命令 → 一次性降级提示。
func (p *Plat) showSkillPick(kind PickKind) {
	path, ok := pickSkillPathCommand(kind)
	if !ok {
		p.noticeOnce(&p.menuNotice, unsupported(i18n.T("ui.platform.dlg.unavailable_open")))
		return
	}
	if path == "" || p.host == nil {
		return // 取消
	}
	p.host.SkillPathPicked(kind, path)
}

// pickSkillPathCommand 按平台取一条可用命令选路径（单选文件 / 选目录 / 角色卡）；
// ok=false = 无命令。
func pickSkillPathCommand(kind PickKind) (string, bool) {
	switch runtime.GOOS {
	case "darwin":
		script := `POSIX path of (choose file with prompt ` + appleScriptString(i18n.T("ui.platform.dlg.skill_file")) + `)`
		switch kind {
		case PickSkillDir:
			script = `POSIX path of (choose folder with prompt ` + appleScriptString(i18n.T("ui.platform.dlg.skill_dir")) + `)`
		case PickCharacterCard:
			script = `POSIX path of (choose file with prompt ` + appleScriptString(i18n.T("ui.platform.dlg.character_card")) + `)`
		}
		out, err := exec.Command("osascript", "-e", script).Output()
		if err != nil {
			return "", true // 命令存在但取消/报错：视同取消
		}
		return strings.TrimSpace(string(out)), true
	default:
		var cmds [][]string
		switch kind {
		case PickSkillDir:
			cmds = [][]string{
				{"zenity", "--file-selection", "--directory", "--title=" + i18n.T("ui.platform.dlg.skill_dir")},
				{"kdialog", "--getexistingdirectory", "."},
			}
		case PickCharacterCard:
			cmds = [][]string{
				{"zenity", "--file-selection",
					"--file-filter=" + i18n.T("ui.platform.filter.character") + " | *.png *.json *.charx",
					"--file-filter=" + i18n.T("ui.platform.filter.all") + " | *",
					"--title=" + i18n.T("ui.platform.dlg.character_card")},
				{"kdialog", "--getopenfilename", ".", "*.png *.json *.charx | " + i18n.T("ui.platform.filter.character")},
			}
		default:
			cmds = [][]string{
				{"zenity", "--file-selection",
					"--file-filter=" + i18n.T("ui.platform.filter.skill") + " | SKILL.md *.zip",
					"--file-filter=" + i18n.T("ui.platform.filter.all") + " | *",
					"--title=" + i18n.T("ui.platform.dlg.skill_file")},
				{"kdialog", "--getopenfilename", ".", "SKILL.md *.zip | " + i18n.T("ui.platform.filter.skill")},
			}
		}
		for _, cmd := range cmds {
			if _, err := exec.LookPath(cmd[0]); err != nil {
				continue
			}
			out, err := exec.Command(cmd[0], cmd[1:]...).Output()
			if err != nil {
				return "", true // 取消/失败：视同取消（避免回落第二条命令再弹一次）
			}
			return strings.TrimSpace(string(out)), true
		}
		return "", false
	}
}
