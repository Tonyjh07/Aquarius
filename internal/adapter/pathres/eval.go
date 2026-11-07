package pathres

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// maxLinkDepth 链接展开层数上限（防循环/爆栈；超出返回错误即 fail closed）。
const maxLinkDepth = 8

// evalPath 逐组件解析路径中的全部链接（symlink 与 Windows 目录 junction）：
// 命中链接即展开为目标组件重新入列（绝对目标重置到根；相对目标基于链接父目录），
// 真不存在的组件把剩余尾部按字面拼回（新建文件场景），无法判定时报错。
// 入参须为绝对化路径；组件可能因链接展开被重复处理，故以层数上限兜底。
func evalPath(p string) (string, error) {
	vol := filepath.VolumeName(p)
	sep := string(filepath.Separator)
	rest := strings.TrimPrefix(p[len(vol):], sep)
	var names []string
	if rest != "" {
		names = strings.Split(rest, sep)
	}
	out := vol + sep
	depth := 0
	for len(names) > 0 {
		name := names[0]
		names = names[1:]
		if name == "" || name == "." {
			continue
		}
		next := filepath.Join(out, name)
		link, err := isSymlink(next)
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				// 组件不存在：其后不可能再有可达组件，剩余按字面拼回。
				out = next
				for _, tail := range names {
					out = filepath.Join(out, tail)
				}
				return out, nil
			}
			return "", fmt.Errorf("resolve %s: %w", next, err) // 权限等：无法判定 → fail closed
		}
		if !link {
			out = next
			continue
		}
		depth++
		if depth > maxLinkDepth {
			return "", fmt.Errorf("symlink depth exceeded %d (possible loop): %s", maxLinkDepth, p)
		}
		target, err := os.Readlink(next)
		if err != nil {
			return "", fmt.Errorf("read link target %s: %w", next, err)
		}
		if !filepath.IsAbs(target) {
			target = filepath.Join(out, target) // 相对目标基于链接的父目录
		}
		target = filepath.Clean(target)
		tvol := filepath.VolumeName(target)
		troot := tvol + sep
		out = troot
		if trest := strings.TrimPrefix(target[len(troot):], sep); trest != "" {
			names = append(strings.Split(trest, sep), names...)
		}
	}
	return out, nil
}

// evalExisting 大小写/分隔符规整：对最长存在前缀求 EvalSymlinks（Windows 上会
// 规范成磁盘实际大小写），尚不存在的尾部原样拼回。只做规整——链接（含 junction
// 与悬空链接）的解析由 evalPath 负责，本函数解析失败时原样返回。
func evalExisting(p string) string {
	orig := p
	var tail []string
	for {
		resolved, err := filepath.EvalSymlinks(p)
		if err == nil {
			return filepath.Join(append([]string{resolved}, tail...)...)
		}
		parent := filepath.Dir(p)
		if parent == p {
			return orig // 到根仍解析不了：保持原样
		}
		tail = append([]string{filepath.Base(p)}, tail...)
		p = parent
	}
}
