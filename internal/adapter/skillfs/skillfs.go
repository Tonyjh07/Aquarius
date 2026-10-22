// Package skillfs 实现 port.SkillCatalog（DESIGN §5.15 / D201）：从有序技能根发现
// Agent Skills（一个目录 + SKILL.md），解析 YAML frontmatter 并按开放规范校验。
//
// 三条边界：
//   - 没有工作区概念（硬性规则 7）：技能只能来自显式目录，绝不按 cwd 向上找；
//   - 坏条目**跳过并记警告**，不阻断启动——一个装坏的技能不该让助手起不来；
//   - 不做文件监听：目录内容变化由装配根/设置页显式 Rescan（硬性规则 7）。
package skillfs

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/Tonyjh07/Aquarius/internal/port"
)

const (
	// skillFileName 技能包的入口文件名（开放规范固定值，大小写敏感）。
	skillFileName = "SKILL.md"
	// scanDepth 每个技能根向下找几层（根自身 = 0）：2 层足够覆盖「一个仓库里放若干技能」。
	scanDepth = 2
	// maxSkillFileBytes SKILL.md 上限：规范建议正文 < 500 行；超过这个大小基本是误放的文件。
	maxSkillFileBytes = 1 << 20
	// maxNameLen 规范要求 name ≤ 64。
	maxNameLen = 64
	// maxDescLen 规范要求 description ≤ 1024。
	maxDescLen = 1024
)

// Options 装配参数。
type Options struct {
	// Dirs 技能根（**绝对路径**、按声明序，先命中者胜）。不存在的根只记警告。
	// 调用方负责把兜底目录（<profile>/skills）排在最后。
	Dirs []string
}

// Catalog 技能清单（内存快照 + 显式重扫）。
type Catalog struct {
	dirs []string

	mu       sync.RWMutex
	skills   []entry
	warnings []string
}

// entry 一条已通过校验的技能。
type entry struct {
	info port.SkillInfo
	path string // SKILL.md 绝对路径
}

// New 建目录并扫描一次。扫描失败不返回错误：坏目录/坏技能都进 Warnings。
func New(opts Options) *Catalog {
	c := &Catalog{dirs: normalizeDirs(opts.Dirs)}
	c.Rescan()
	return c
}

// normalizeDirs 去空、去重、规约路径（**不做**相对→绝对：无 cwd 可依，相对路径直接丢）。
func normalizeDirs(dirs []string) []string {
	out := make([]string, 0, len(dirs))
	seen := map[string]bool{}
	for _, d := range dirs {
		d = strings.TrimSpace(d)
		if d == "" || !filepath.IsAbs(d) {
			continue
		}
		d = filepath.Clean(d)
		if seen[d] {
			continue
		}
		seen[d] = true
		out = append(out, d)
	}
	return out
}

// Rescan 重新扫描全部技能根（启动、安装/卸载后、设置页「重新扫描」）。
func (c *Catalog) Rescan() {
	var skills []entry
	var warnings []string
	owner := map[string]string{} // name → 目录（同名先命中者胜）
	c.mu.RLock()
	dirs := append([]string(nil), c.dirs...)
	c.mu.RUnlock()
	for _, root := range dirs {
		fi, err := os.Stat(root)
		if err != nil || !fi.IsDir() {
			warnings = append(warnings, fmt.Sprintf("技能目录 %s 不存在或不是目录，已跳过", root))
			continue
		}
		c.scanRoot(root, &skills, &warnings, owner)
	}
	c.mu.Lock()
	c.skills, c.warnings = skills, warnings
	c.mu.Unlock()
}

// scanRoot 深度优先（目录名序）扫描一个根：命中 SKILL.md 的目录即技能，不再往下找。
func (c *Catalog) scanRoot(root string, skills *[]entry, warnings *[]string, owner map[string]string) {
	var walk func(dir string, depth int)
	walk = func(dir string, depth int) {
		path := filepath.Join(dir, skillFileName)
		if fi, err := os.Stat(path); err == nil && !fi.IsDir() {
			c.addSkill(dir, path, root, skills, warnings, owner)
			return
		}
		if depth >= scanDepth {
			return
		}
		ents, err := os.ReadDir(dir)
		if err != nil {
			*warnings = append(*warnings, fmt.Sprintf("技能目录 %s 读不出：%v", dir, err))
			return
		}
		for _, e := range ents {
			if !e.IsDir() || strings.HasPrefix(e.Name(), ".") || e.Name() == "node_modules" {
				continue
			}
			walk(filepath.Join(dir, e.Name()), depth+1)
		}
	}
	walk(root, 0)
}

// addSkill 解析 + 校验一条候选；任何不合规都只记警告。
func (c *Catalog) addSkill(dir, path, root string, skills *[]entry, warnings *[]string, owner map[string]string) {
	fm, _, _, err := readSkill(path)
	if err != nil {
		*warnings = append(*warnings, fmt.Sprintf("技能 %s 已跳过：%v", dir, err))
		return
	}
	base := filepath.Base(dir)
	if fm.Name != base {
		*warnings = append(*warnings, fmt.Sprintf("技能 %s 已跳过：frontmatter 的 name=%q 与目录名不一致",
			dir, fm.Name))
		return
	}
	if prev, ok := owner[fm.Name]; ok {
		*warnings = append(*warnings, fmt.Sprintf("技能 %q 重复：%s 与 %s 同名，前者生效（先声明者胜）",
			fm.Name, prev, dir))
		return
	}
	owner[fm.Name] = dir
	*skills = append(*skills, entry{
		info: port.SkillInfo{
			Name:        fm.Name,
			Description: fm.Description,
			Dir:         dir,
			Source:      root,
		},
		path: path,
	})
}

// List 实现 port.SkillCatalog（技能根序 + 根内目录名序，确定性）。
func (c *Catalog) List(ctx context.Context) ([]port.SkillInfo, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := make([]port.SkillInfo, 0, len(c.skills))
	for _, e := range c.skills {
		out = append(out, e.info)
	}
	return out, nil
}

// Load 实现 port.SkillCatalog：**每次现读**（用户改了 SKILL.md 不必重扫即可生效）。
func (c *Catalog) Load(ctx context.Context, name string) (port.SkillContent, error) {
	if err := ctx.Err(); err != nil {
		return port.SkillContent{}, err
	}
	name = strings.TrimSpace(name)
	c.mu.RLock()
	var found *entry
	for i := range c.skills {
		if c.skills[i].info.Name == name {
			e := c.skills[i]
			found = &e
			break
		}
	}
	c.mu.RUnlock()
	if found == nil {
		return port.SkillContent{}, fmt.Errorf("%w: %q", port.ErrSkillNotFound, name)
	}
	fm, body, bodyLine, err := readSkill(found.path)
	if err != nil {
		return port.SkillContent{}, fmt.Errorf("skill %q: %w", name, err)
	}
	if fm.Name != name {
		return port.SkillContent{}, fmt.Errorf("skill %q: %s now declares name %q", name, found.path, fm.Name)
	}
	return port.SkillContent{
		Info:     found.info,
		Body:     body,
		Path:     found.path,
		BodyLine: bodyLine,
	}, nil
}

// Warnings 实现 port.SkillCatalog：被跳过/被覆盖条目的说明（面向用户）。
func (c *Catalog) Warnings(ctx context.Context) []string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := make([]string, len(c.warnings))
	copy(out, c.warnings)
	return out
}

// Dirs 当前生效的技能根（只读快照；装配根/设置页展示用）。
func (c *Catalog) Dirs() []string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := make([]string, len(c.dirs))
	copy(out, c.dirs)
	return out
}

// SetDirs 替换技能根并重扫（设置页「技能」页增删目录）：调用方负责把兜底根排最后。
// 相对路径仍按启动口径丢弃（无 cwd 可依）。
func (c *Catalog) SetDirs(dirs []string) {
	c.mu.Lock()
	c.dirs = normalizeDirs(dirs)
	c.mu.Unlock()
	c.Rescan()
}

// Names 按名字排序的可用技能名（工具报「名字打错了」时给候选，长度由调用方截断）。
func (c *Catalog) Names() []string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := make([]string, 0, len(c.skills))
	for _, e := range c.skills {
		out = append(out, e.info.Name)
	}
	sort.Strings(out)
	return out
}

// errSkillFileTooLarge 超限文件不上模型面（避免把误放的日志当技能正文回填）。
var errSkillFileTooLarge = errors.New("SKILL.md 超过 1 MB 上限")
