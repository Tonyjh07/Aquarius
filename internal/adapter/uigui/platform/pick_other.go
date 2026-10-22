//go:build !windows

// 技能导入的专用选择框（非 Windows 降级）：走桌面环境命令（zenity / kdialog / osascript）。
package platform

import (
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
		p.noticeOnce(&p.menuNotice, unsupported("文件选择框（Linux 需 zenity 或 kdialog）"))
		return
	}
	if path == "" || p.host == nil {
		return // 取消
	}
	p.host.SkillPathPicked(kind, path)
}

// pickSkillPathCommand 按平台取一条可用命令选路径（单选文件 / 选目录）；ok=false = 无命令。
func pickSkillPathCommand(kind PickKind) (string, bool) {
	switch runtime.GOOS {
	case "darwin":
		script := `POSIX path of (choose file with prompt "选择技能文件")`
		if kind == PickSkillDir {
			script = `POSIX path of (choose folder with prompt "选择技能文件夹")`
		}
		out, err := exec.Command("osascript", "-e", script).Output()
		if err != nil {
			return "", true // 命令存在但取消/报错：视同取消
		}
		return strings.TrimSpace(string(out)), true
	default:
		var cmds [][]string
		if kind == PickSkillDir {
			cmds = [][]string{
				{"zenity", "--file-selection", "--directory", "--title=选择技能文件夹"},
				{"kdialog", "--getexistingdirectory", "."},
			}
		} else {
			cmds = [][]string{
				{"zenity", "--file-selection", "--file-filter=技能文件 | SKILL.md *.zip",
					"--file-filter=所有文件 | *", "--title=选择技能文件"},
				{"kdialog", "--getopenfilename", ".", "SKILL.md *.zip | 技能文件"},
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
