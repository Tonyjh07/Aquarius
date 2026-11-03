package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/Tonyjh07/Aquarius/internal/adapter/memoryfs"
	"github.com/Tonyjh07/Aquarius/internal/i18n"
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

// openMemoryEditor /memory 的装配实现（D24）：文档名 → 磁盘路径（缺失先建空文件）
// → 系统编辑器阻塞打开；保存后下次读取即生效。
// stdio 来自 run() 注入的三流——保持"标准流可注入"的 e2e 契约（不直连 os.Std*）。
func openMemoryEditor(mem *memoryfs.Store, fe uiFrontend, stdin io.Reader, stdout, stderr io.Writer) func(name string) (string, error) {
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
				fmt.Fprintf(stderr, "%s: %v\n", i18n.T("cmd.asm.err.suspend_tui"), serr)
			}
			defer func() {
				if rerr := sus.Resume(); rerr != nil {
					fmt.Fprintf(stderr, "%s: %v\n", i18n.T("cmd.asm.err.resume_tui"), rerr)
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
