// Package pathres 把路径换算成磁盘上的真实落点，再判断它落在哪个根之内。
//
// 只做两件事：解析（Resolve）与逐段包含判定（Within / RootSet）。两处调用方——工具权限
// 矩阵的特权目录判定（toolrun）与外部代理的文件访问闸门（acpgate）——必须用同一份判据：
// 两份实现迟早对「链接、大小写、盘符」给出不同答案，而差异落在哪一边都是绕过。
//
// 判据只有一条用途：**我们自己用来拒绝**。它挡不住「检查完再把链接换掉」（TOCTOU），
// 也不是内核围栏——进程能自己起 shell 的话，这条路根本不经过它（§5.18/A3）。
package pathres

import (
	"fmt"
	"path/filepath"
	"runtime"
	"strings"
)

// Resolve 把路径规整成**磁盘上的真实落点**：绝对化 → 大小写/分隔符规整（对最长存在
// 前缀求 EvalSymlinks）→ 逐组件解析全部链接（symlink 与 Windows junction）。
//
// 尚不存在的尾部按字面拼回（写新文件要能判到它落在哪）；任何无法判定的情形都返回错误，
// 调用方一律按「不在根内」处理（fail closed）——否则权限判据可以被一个读不出目标的链接绕开。
func Resolve(p string) (string, error) {
	if strings.TrimSpace(p) == "" {
		return "", fmt.Errorf("resolve empty path")
	}
	abs, err := filepath.Abs(p)
	if err != nil {
		return "", fmt.Errorf("resolve %s: %w", p, err)
	}
	return evalPath(evalExisting(abs))
}

// Within 目标是否落在根之内（含根自身）。两侧都必须是**已 Resolve 的绝对路径**：
// 逐段比较是为了让 /a/bc 不算在 /a/b 里，而这个边界过不了字符串前缀那一关。
func Within(root, target string) bool {
	if root == "" || target == "" {
		return false
	}
	rc, tc := filepath.Clean(root), filepath.Clean(target)
	if equalPath(rc, tc) {
		return true
	}
	if !sameVolume(rc, tc) {
		return false
	}
	rs, ts := segments(rc), segments(tc)
	if len(ts) <= len(rs) {
		return false
	}
	for i := range rs {
		if !equalSegment(rs[i], ts[i]) {
			return false
		}
	}
	return true
}

// RootSet 一组已解析的根（一次解析、反复比较）。
type RootSet struct {
	roots []string
}

// NewRootSet 解析并去重（Windows 上按大小写不敏感去重）。空项与空白项直接丢掉：
// 那不是「一个根」，只是配置或调用方留下的空行。
func NewRootSet(paths []string) (RootSet, error) {
	var set RootSet
	seen := make(map[string]bool, len(paths))
	for _, p := range paths {
		if strings.TrimSpace(p) == "" {
			continue
		}
		r, err := Resolve(p)
		if err != nil {
			return RootSet{}, err
		}
		key := r
		if runtime.GOOS == "windows" {
			key = strings.ToLower(r)
		}
		if seen[key] {
			continue
		}
		seen[key] = true
		set.roots = append(set.roots, r)
	}
	return set, nil
}

// Empty 一个根都没有：任何目标都不在里面（也就是什么都不许）。
func (s RootSet) Empty() bool { return len(s.roots) == 0 }

// Roots 已解析的根（副本；顺序与传入一致）。
func (s RootSet) Roots() []string { return append([]string(nil), s.roots...) }

// Locate 解析目标并返回命中它的那个根；未命中时 root 为空。
//
// resolved 是**我们认定它要落的那个位置**，与磁盘上的字面写法可能不同（链接展开了），
// 所以写盘前后要拿它来对账，不能拿入参路径去比。
func (s RootSet) Locate(target string) (resolved, root string, err error) {
	resolved, err = Resolve(target)
	if err != nil {
		return "", "", err
	}
	for _, r := range s.roots {
		if Within(r, resolved) {
			return resolved, r, nil
		}
	}
	return resolved, "", nil
}

// segments 拆成逐级组件（丢掉空段与 "."）。
func segments(p string) []string {
	vol := filepath.VolumeName(p)
	sep := string(filepath.Separator)
	rest := strings.TrimPrefix(p[len(vol):], sep)
	if rest == "" {
		return nil
	}
	parts := strings.Split(rest, sep)
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if part == "" || part == "." {
			continue
		}
		out = append(out, part)
	}
	return out
}

// sameVolume 同卷判定：Windows 上跨盘符/跨 UNC 共享必然不在同一棵树里；Unix 上恒为真。
func sameVolume(a, b string) bool {
	va, vb := filepath.VolumeName(a), filepath.VolumeName(b)
	if runtime.GOOS == "windows" {
		return strings.EqualFold(va, vb)
	}
	return va == vb
}

// equalPath 整条路径的相等判定（Windows 大小写不敏感：同一个目录可以有两种写法）。
func equalPath(a, b string) bool {
	if runtime.GOOS == "windows" {
		return strings.EqualFold(a, b)
	}
	return a == b
}

// equalSegment 单级组件的相等判定（同上）。
func equalSegment(a, b string) bool {
	if runtime.GOOS == "windows" {
		return strings.EqualFold(a, b)
	}
	return a == b
}
