// Command docsync 维护文档头部的「更新日期 + 对应提交」标记行。
//
// 为什么需要它：docs/ 各篇与 README 会随代码漂移，读者（与后续维护者）需要知道
// 「这份文档对应哪份代码」。手工改容易漏、也容易写错哈希，所以把这条信息做成
// 工具维护的单行标记，插在每个文件首个 H1 之后：
//
//	# 标题
//	<!-- docs-sync: 2026-10-21 @ f1b9ed1 -->
//
// 标记是 HTML 注释——源码里一眼可见、渲染后不打扰读者（用户手册与发布说明都是
// 面向用户的，不该出现提交号这类开发信息）。
//
// 用法：
//
//	go run ./cmd/docsync                  # 只刷新工作区里改过的文档（提交前跑）
//	go run ./cmd/docsync -commit <提交>    # 只刷新该提交改过的文档（提交后补）
//	go run ./cmd/docsync -all             # 全部文档统一刷成 -date/-rev
//	go run ./cmd/docsync -check           # 只检查每个文档都有标记（门禁）
//
// 语义：标记里的提交是**该文档内容对应的代码基线**。
//
//   - 默认模式（工作区）：只刷新「去掉标记后的内容与 HEAD 版本不同」的文档，标记写
//     HEAD——文档本身不改代码，指向提交前的代码基线才有意义。提交前跑一次，标记就
//     跟着进这笔提交；重复跑不会把已同步的文档再刷一遍。
//   - `-commit` 模式：读该提交的变更清单，只挑其中改过的文档，日期取该提交的日期、
//     哈希取它的父提交（同样是代码基线）。因为标记会改文件内容，跑完要
//     `git commit --amend --no-edit` 把标记并回那笔提交。
//
// 没改过的文档保持旧标记不动（仍然准确），避免每次提交都改一遍全仓文档。
// 缺标记的文档无论是否改过都补上，日期/哈希取它最后一次提交（同样是父提交）。
//
// 覆盖范围：`docs/` 下全部 `*.md`（含子目录，如 `docs/releases/`）与仓库根的
// `README.md`。`DESIGN.md`、`AGENTS.md` 不参与——它们与代码同批修改，标记会立刻过时。
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// markerLineRe 匹配一整行标记。
var markerLineRe = regexp.MustCompile(`^<!-- docs-sync: [^\n]*-->$`)

// marker 生成标记行（不含换行）。
func marker(date, rev string) string {
	return "<!-- docs-sync: " + date + " @ " + rev + " -->"
}

// headerMarker 返回首个 H1 之后（跳过空行）的标记行下标；没有则 -1。
// 只认「标题下那一行」——正文里的同形示例（如开发指南的用法示例）不动。
func headerMarker(lines []string) int {
	for i, l := range lines {
		if !strings.HasPrefix(l, "# ") {
			continue
		}
		for j := i + 1; j < len(lines); j++ {
			if strings.TrimSpace(lines[j]) == "" {
				continue
			}
			if markerLineRe.MatchString(lines[j]) {
				return j
			}
			return -1
		}
		return -1
	}
	return -1
}

// syncText 返回带指定标记的文本。标记固定占「H1 后的第一段」：先摘掉原有的头部
// 标记与前导空行，再写入新标记。正文里出现的同形示例不受影响。
// 没有 H1 的文件原样返回（避免误改无标题的片段文件）。
// 行尾统一为 LF（仓库属性要求），CRLF 输入会被规范化。
func syncText(src, date, rev string) string {
	lines := strings.Split(strings.ReplaceAll(src, "\r\n", "\n"), "\n")
	at := headerMarker(lines)
	h := -1
	for i, l := range lines {
		if strings.HasPrefix(l, "# ") {
			h = i
			break
		}
	}
	if h < 0 {
		return src
	}
	rest := lines[h+1:]
	if at >= 0 {
		rest = append(append([]string{}, lines[h+1:at]...), lines[at+1:]...)
	}
	for len(rest) > 0 && strings.TrimSpace(rest[0]) == "" {
		rest = rest[1:]
	}
	out := make([]string, 0, len(lines)+2)
	out = append(out, lines[:h+1]...)
	out = append(out, marker(date, rev), "")
	out = append(out, rest...)
	return strings.Join(out, "\n")
}

// hasMarker 报告文件头部是否已带标记。
func hasMarker(src string) bool {
	return headerMarker(strings.Split(strings.ReplaceAll(src, "\r\n", "\n"), "\n")) >= 0
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

// run 主流程（测试可调）。返回进程退出码：0 = 成功/已同步，1 = -check 发现缺标记或出错。
func run(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("docsync", flag.ContinueOnError)
	fs.SetOutput(stderr)
	check := fs.Bool("check", false, "只检查：有文档缺标记则退出 1（不写文件）")
	all := fs.Bool("all", false, "全部文档统一刷成 -date/-rev（默认只刷新工作区改过的文档）")
	commit := fs.String("commit", "", "只刷新该提交改过的文档（哈希取它的父提交）")
	date := fs.String("date", "", "更新日期 YYYY-MM-DD（默认今天 / -commit 取该提交日期）")
	rev := fs.String("rev", "", "对应提交（默认 HEAD；-commit 取它的父提交）")
	root := fs.String("root", "", "仓库根目录（默认从当前目录向上找 go.mod）")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	base := *root
	if base == "" {
		var err error
		if base, err = repoRoot(); err != nil {
			return fail(stderr, err)
		}
	}
	files, err := targets(base)
	if err != nil {
		return fail(stderr, err)
	}

	if *check {
		missing := 0
		for _, path := range files {
			raw, err := os.ReadFile(path)
			if err != nil {
				return fail(stderr, err)
			}
			if !hasMarker(string(raw)) {
				fmt.Fprintf(stdout, "缺标记 %s\n", relOf(base, path))
				missing++
			}
		}
		if missing > 0 {
			fmt.Fprintf(stdout, "共 %d 个文档缺标记；运行 go run ./cmd/docsync 补上\n", missing)
			return 1
		}
		fmt.Fprintf(stdout, "全部 %d 个文档都带标记\n", len(files))
		return 0
	}

	day := *date
	sha := *rev
	selected := map[string]bool{}
	switch {
	case *commit != "":
		// 读该提交的变更清单，只挑文档；日期/基线取该提交与它的父提交。
		if day == "" {
			if day, err = commitDate(base, *commit); err != nil {
				return fail(stderr, err)
			}
		}
		if sha == "" {
			if sha, err = parentRev(base, *commit); err != nil {
				return fail(stderr, err)
			}
		}
		changed, err := commitDocs(base, *commit)
		if err != nil {
			return fail(stderr, err)
		}
		for _, p := range changed {
			selected[p] = true
		}
		if len(selected) == 0 {
			fmt.Fprintf(stdout, "提交 %s 没有改动文档，无需同步\n", *commit)
			return 0
		}
	case *all:
		for _, p := range files {
			selected[relOf(base, p)] = true
		}
	default:
		// 工作区模式：逐个比对「去掉标记后的内容」与 HEAD 版本——只有内容真的改过
		// （或文件是新增的）才刷新标记。这样重复运行不会把刚补的历史标记刷成今天。
		for _, p := range files {
			rel := relOf(base, p)
			raw, err := os.ReadFile(p)
			if err != nil {
				return fail(stderr, err)
			}
			old, tracked := headContent(base, rel)
			if !tracked || stripMarker(string(raw)) != stripMarker(old) {
				selected[rel] = true
			}
		}
	}

	if day == "" {
		day = time.Now().Format("2006-01-02")
	}
	if sha == "" {
		if sha, err = gitRev(base); err != nil {
			return fail(stderr, err)
		}
	}

	updated := 0
	for _, path := range files {
		rel := relOf(base, path)
		raw, err := os.ReadFile(path)
		if err != nil {
			return fail(stderr, err)
		}
		src := string(raw)
		d, r := day, sha
		if !selected[rel] {
			// 未选中：已有标记就保持原样；缺标记则用该文件最后一次提交补（贴近事实）。
			if hasMarker(src) {
				continue
			}
			if cd, cr, err := lastCommit(base, path); err == nil {
				d, r = cd, cr
			}
		}
		next := syncText(src, d, r)
		if next == src {
			continue
		}
		if err := os.WriteFile(path, []byte(next), 0o644); err != nil {
			return fail(stderr, err)
		}
		fmt.Fprintf(stdout, "已更新 %s → %s @ %s\n", rel, d, r)
		updated++
	}
	fmt.Fprintf(stdout, "完成：更新 %d 个（%s @ %s）\n", updated, day, sha)
	return 0
}

// stripMarker 去掉头部标记行，并抹平 H1 之后的前导空行——两侧都做才能对齐，
// 用于「内容是否真的改了」的比对（纯标记变化不算改动）。
func stripMarker(src string) string {
	lines := strings.Split(strings.ReplaceAll(src, "\r\n", "\n"), "\n")
	if at := headerMarker(lines); at >= 0 {
		lines = append(append([]string{}, lines[:at]...), lines[at+1:]...)
	}
	h := -1
	for i, l := range lines {
		if strings.HasPrefix(l, "# ") {
			h = i
			break
		}
	}
	if h < 0 {
		return strings.Join(lines, "\n")
	}
	rest := lines[h+1:]
	for len(rest) > 0 && strings.TrimSpace(rest[0]) == "" {
		rest = rest[1:]
	}
	out := append(append([]string{}, lines[:h+1]...), rest...)
	return strings.Join(out, "\n")
}

// headContent 取该路径在 HEAD 里的内容；tracked = 是否存在于 HEAD。
func headContent(root, rel string) (string, bool) {
	out, err := exec.Command("git", "-C", root, "show", "HEAD:"+rel).Output()
	if err != nil {
		return "", false
	}
	return string(out), true
}

// targets 收集待维护的文件：docs/ 下全部 markdown（递归）+ 根 README.md。
func targets(root string) ([]string, error) {
	var files []string
	err := filepath.WalkDir(filepath.Join(root, "docs"), func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.EqualFold(filepath.Ext(path), ".md") {
			return nil
		}
		files = append(files, path)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("扫描 docs: %w", err)
	}
	if readme := filepath.Join(root, "README.md"); fileExists(readme) {
		files = append(files, readme)
	}
	sort.Strings(files)
	return files, nil
}

// commitDocs 该提交改动过的文档（相对仓库根、斜杠分隔）；按 targets 口径过滤。
func commitDocs(root, rev string) ([]string, error) {
	out, err := exec.Command("git", "-C", root, "show", "--name-only", "--pretty=format:", rev).Output()
	if err != nil {
		return nil, fmt.Errorf("读提交 %s 的变更清单失败: %w", rev, err)
	}
	var docs []string
	for _, line := range strings.Split(string(out), "\n") {
		rel := filepath.ToSlash(strings.TrimSpace(line))
		if rel == "" || !isDocPath(rel) {
			continue
		}
		if !fileExists(filepath.Join(root, filepath.FromSlash(rel))) {
			continue // 该提交里被删掉/改名的旧路径
		}
		docs = append(docs, rel)
	}
	sort.Strings(docs)
	return docs, nil
}

// isDocPath 与 targets 同一口径：docs/ 下的 .md，或根 README.md。
func isDocPath(rel string) bool {
	if !strings.EqualFold(filepath.Ext(rel), ".md") {
		return false
	}
	if rel == "README.md" {
		return true
	}
	return strings.HasPrefix(rel, "docs/")
}

// lastCommit 该文件最后一次提交的日期（YYYY-MM-DD）与**代码基线**（那次提交的父提交）。
// 与 -commit 模式同口径：文档内容由提交 L 产出，它对应的代码基线是 parent(L)。
func lastCommit(root, path string) (string, string, error) {
	out, err := exec.Command("git", "-C", root, "log", "-1", "--format=%ad|%H", "--date=short", "--",
		relOf(root, path)).Output()
	if err != nil {
		return "", "", err
	}
	parts := strings.SplitN(strings.TrimSpace(string(out)), "|", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", fmt.Errorf("没有提交历史: %s", path)
	}
	sha, err := parentRev(root, parts[1])
	if err != nil {
		return "", "", err
	}
	return parts[0], sha, nil
}

// commitDate 该提交的日期（YYYY-MM-DD）。
func commitDate(root, rev string) (string, error) {
	out, err := exec.Command("git", "-C", root, "log", "-1", "--format=%ad", "--date=short", rev).Output()
	if err != nil {
		return "", fmt.Errorf("读提交 %s 的日期失败: %w", rev, err)
	}
	day := strings.TrimSpace(string(out))
	if day == "" {
		return "", fmt.Errorf("读提交 %s 的日期失败：输出为空", rev)
	}
	return day, nil
}

// parentRev 该提交的父提交短哈希（根提交无父 → 用它自己）。
func parentRev(root, rev string) (string, error) {
	out, err := exec.Command("git", "-C", root, "rev-parse", "--short", rev+"^").Output()
	if err == nil {
		if sha := strings.TrimSpace(string(out)); sha != "" {
			return sha, nil
		}
	}
	// 根提交（没有父）或 rev 本身无效时回落到 rev。
	out, err = exec.Command("git", "-C", root, "rev-parse", "--short", rev).Output()
	if err != nil {
		return "", fmt.Errorf("解析提交 %s 失败: %w", rev, err)
	}
	sha := strings.TrimSpace(string(out))
	if sha == "" {
		return "", fmt.Errorf("解析提交 %s 失败：输出为空", rev)
	}
	return sha, nil
}

// repoRoot 从当前目录向上找 go.mod。
func repoRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if fileExists(filepath.Join(dir, "go.mod")) {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("找不到仓库根（向上未遇到 go.mod）")
		}
		dir = parent
	}
}

// gitRev 取短提交哈希。
func gitRev(root string) (string, error) {
	out, err := exec.Command("git", "-C", root, "rev-parse", "--short", "HEAD").Output()
	if err != nil {
		return "", fmt.Errorf("读 HEAD 失败（仓库还没提交？）: %w", err)
	}
	rev := strings.TrimSpace(string(out))
	if rev == "" {
		return "", fmt.Errorf("读 HEAD 失败：输出为空")
	}
	return rev, nil
}

func relOf(root, path string) string {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return filepath.ToSlash(path)
	}
	return filepath.ToSlash(rel)
}

func fileExists(path string) bool {
	st, err := os.Stat(path)
	return err == nil && !st.IsDir()
}

func fail(w io.Writer, err error) int {
	fmt.Fprintf(w, "docsync: %v\n", err)
	return 1
}
