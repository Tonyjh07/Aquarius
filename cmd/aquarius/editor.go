package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/Tonyjh07/Aquarius/internal/i18n"
	"github.com/Tonyjh07/Aquarius/internal/logging"
)

// pickEditor 选择系统编辑器（D24）：$VISUAL → $EDITOR → 平台默认（windows 记事本 / vi）。
func pickEditor(visual, editor, goos string) []string {
	if s := strings.TrimSpace(visual); s != "" {
		return strings.Fields(s)
	}
	if s := strings.TrimSpace(editor); s != "" {
		return strings.Fields(s)
	}
	if goos == "windows" {
		return []string{"notepad"}
	}
	return []string{"vi"}
}

// memoryEditor /memory 的装配实现（D24）：文档名 → 磁盘路径（缺失先建空文件）
// → 系统编辑器阻塞打开；保存后下次读取即生效。
// 三流取自 wiring（run() 注入）——保持「标准流可注入」的 e2e 契约（不直连 os.Std*）；
// 子进程的 stderr 是它自己的原始输出，直接接宿主流，不进运行日志。
func (w *wiring) memoryEditor(fe uiFrontend) func(name string) (string, error) {
	mem, stdin, stdout, stderr := w.mem, w.stdin, w.stdout, w.stderr
	return func(name string) (string, error) {
		p, err := mem.Path(name)
		if err != nil {
			return "", err
		}
		if _, err := os.Stat(p); errors.Is(err, os.ErrNotExist) {
			if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
				return "", fmt.Errorf("%s: %w", i18n.T("cmd.asm.err.mkdir_memory"), err)
			}
			if err := os.WriteFile(p, []byte{}, 0o644); err != nil {
				return "", fmt.Errorf("%s: %w", i18n.Tf("cmd.asm.err.create_memory_file", "path", p), err)
			}
		}
		// TUI 前端先交出终端再拉起编辑器，返回后收回（审查修复）。
		sus, _ := fe.(terminalSuspend)
		if sus != nil {
			if serr := sus.Suspend(); serr != nil {
				w.note(logging.LevelWarn, logging.UI, "log.ui.terminal_suspend_failed",
					"log.act.continue", "err", serr)
			}
			defer func() {
				if rerr := sus.Resume(); rerr != nil {
					w.note(logging.LevelWarn, logging.UI, "log.ui.terminal_resume_failed",
						"log.act.continue", "err", rerr)
				}
			}()
		}
		argv := pickEditor(os.Getenv("VISUAL"), os.Getenv("EDITOR"), runtime.GOOS)
		cmd := exec.Command(argv[0], append(argv[1:], p)...)
		cmd.Stdin, cmd.Stdout, cmd.Stderr = stdin, stdout, stderr
		if err := cmd.Run(); err != nil {
			return "", fmt.Errorf("%s: %w", i18n.Tf("cmd.asm.err.run_editor", "editor", argv[0]), err)
		}
		return p, nil
	}
}
