//go:build !windows

package platform

// 非 Windows 打开链接与目录：交给桌面环境的打开命令（macOS `open`、其余 `xdg-open`）。
// 只 Start 不 Wait——浏览器与文件管理器都是独立进程，本程序不等这两个进程。

import (
	"fmt"
	"github.com/Tonyjh07/Aquarius/internal/i18n"
	"os/exec"
	"runtime"
)

// OpenURL 用桌面环境的默认浏览器打开 url。
func (p *Plat) OpenURL(url string) error {
	if url == "" {
		return fmt.Errorf("%s", i18n.T("ui.platform.url.empty"))
	}
	return openWithDesktop(url)
}

// OpenPath 打开一个本地路径（目录 → 文件管理器，文件 → 该文件的默认程序）。
func (p *Plat) OpenPath(path string) error {
	if path == "" {
		return fmt.Errorf("%s", i18n.T("ui.platform.url.empty"))
	}
	return openWithDesktop(path)
}

// openWithDesktop 调桌面环境的打开命令。两个入口只差空值文案。
func openWithDesktop(target string) error {
	var name string
	if runtime.GOOS == "darwin" {
		name = "open"
	} else {
		name = "xdg-open"
	}
	path, err := exec.LookPath(name)
	if err != nil {
		return fmt.Errorf("%s", i18n.Tf("ui.platform.url.no_opener", "cmd", name))
	}
	return exec.Command(path, target).Start()
}
