//go:build !windows

package platform

// 非 Windows 打开链接：交给桌面环境的打开命令（macOS `open`、其余 `xdg-open`）。
// 只 Start 不 Wait——浏览器是独立进程，本程序不等它。

import (
	"fmt"
	"os/exec"
	"runtime"
)

// OpenURL 用桌面环境的默认浏览器打开 url。
func (p *Plat) OpenURL(url string) error {
	if url == "" {
		return fmt.Errorf("链接为空")
	}
	var name string
	var args []string
	if runtime.GOOS == "darwin" {
		name, args = "open", []string{url}
	} else {
		name, args = "xdg-open", []string{url}
	}
	path, err := exec.LookPath(name)
	if err != nil {
		return fmt.Errorf("本机没有 %s，请手动打开链接", name)
	}
	return exec.Command(path, args...).Start()
}
