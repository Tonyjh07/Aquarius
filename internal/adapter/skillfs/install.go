package skillfs

// install.go 技能的安装 / 卸载 / 更新（DESIGN §5.15、D202/D203）。
//
// 三条边界：
//   - **只在用户面**：本文件的能力不经 port 暴露，模型侧永远只有「列」与「载」（D202 口径一）。
//   - **落位统一为副本**：内容一律复制进 `<profile>/skills/<name>/`，git 检出留在
//     `<profile>/skill-cache/<repo-key>/`；卸载 = 删副本目录，不碰别人仓库里的兄弟技能。
//   - **校验前置 + 失败不留残渣**：先复制到同一卷的临时目录、重新解析 frontmatter 校验，
//     通过后才换入正式目录；解压另做 zip-slip 与总量门禁。

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Tonyjh07/Aquarius/internal/adapter/atomicfile"
)

const (
	// maxDownloadBytes 远程包（压缩包）下载上限：技能包远小于此，超限基本是拿错了链接。
	maxDownloadBytes = 64 << 20
	// maxZipEntries 压缩包条目上限（防「几万个小文件」把磁盘塞满）。
	maxZipEntries = 4000
	// maxZipBytes 解压后总量上限（防高压缩比炸弹）。
	maxZipBytes = 128 << 20
	// maxZipFileBytes 单个文件解压上限。
	maxZipFileBytes = 32 << 20
	// maxCopyFiles / maxCopyBytes 目录复制上限（文件夹与 zip 落位共用）。
	maxCopyFiles = 4000
	maxCopyBytes = 128 << 20
	// installScanDepth 安装源里向下找几层 SKILL.md：`repo/skills/<name>/SKILL.md` 是常见布局。
	installScanDepth = 3
	// ledgerVersion 台账格式版本（只增不改字段语义）。
	ledgerVersion = 1
)

// 安装来源种类（台账里持久化，更新动作按它分派）。导出给装配根判「能不能更新」。
const (
	KindFile = "file"
	KindDir  = "dir"
	KindZip  = "zip"
	KindGit  = "git"
)

// 内部别名：台账与分派里用短名，导出名供装配根判断。
const (
	kindFile = KindFile
	kindDir  = KindDir
	kindZip  = KindZip
	kindGit  = KindGit
)

// InstallOptions 安装面装配参数。
type InstallOptions struct {
	// Root 安装落位根（`<profile>/skills`，绝对路径）。不存在则按需创建。
	Root string
	// CacheDir git 检出与远程包暂存根（`<profile>/skill-cache`，绝对路径）。
	CacheDir string
	// LedgerPath 安装台账（`<profile>/skill-installs.json`，原子写）。
	LedgerPath string
	// Catalog 清单：装/卸/更新后重扫；同时用于「同名已生效」判定。nil = 不重扫/不查名。
	Catalog *Catalog
	// Logf 启动期日志（nil = 丢弃）。
	Logf func(format string, args ...any)
	// HTTP 远程下载客户端（nil = 默认，总超时 2 分钟）。
	HTTP *http.Client
	// GitAvailable 主机是否有 git（nil = exec.LookPath("git")）；测试可注入。
	GitAvailable func() bool
	// RunGit 执行 git（dir 空 = 当前目录）；返回合并输出。测试可注入。
	RunGit func(ctx context.Context, dir string, args ...string) (string, error)
	// Now 时钟（nil = time.Now；测试固定时间用）。
	Now func() time.Time
}

// Record 台账一条：一个已安装技能（多技能包各自一条，共享 Source）。
type Record struct {
	Name string `json:"name"`
	// Kind 来源种类：file | dir | zip | git（决定更新动作）。
	Kind string `json:"kind"`
	// Source 用户给的原始来源（路径或链接；界面展示用）。
	Source string `json:"source"`
	// Ref git 分支/标签（`url#分支` 或 `/tree/<分支>`）。
	Ref string `json:"ref,omitempty"`
	// Subdir 技能目录在来源根内的相对路径（斜杠分隔；单文件为空）。
	Subdir string `json:"subdir,omitempty"`
	// FetchURL 实际下载地址（GitHub 仓库链接在无 git 时改走压缩包，Source 仍是用户原文）。
	FetchURL string `json:"fetch_url,omitempty"`
	// Cache git 检出缓存目录名（`skill-cache/<Cache>`）。
	Cache string `json:"cache,omitempty"`
	// InstalledAt / UpdatedAt RFC3339（本地时区）。
	InstalledAt string `json:"installed_at"`
	UpdatedAt   string `json:"updated_at,omitempty"`
}

// ledger 台账文件形态。
type ledger struct {
	Version  int      `json:"version"`
	Installs []Record `json:"installs"`
}

// InstalledSkill 一次安装里落位的一个技能。
type InstalledSkill struct {
	Name string // frontmatter name（= 目录名）
	Dir  string // `<profile>/skills/<name>` 绝对路径
}

// InstallReport 安装/更新结果（用户面文案由调用方组织）。
type InstallReport struct {
	Installed []InstalledSkill
	// Skipped 未安装的条目及原因（来源里有坏技能，但好的一样装上）。
	Skipped []string
	// Notes 过程说明（例如「未检测到 git，已改用压缩包下载」）。
	Notes []string
}

// Installer 安装面（用户面动作；并发安全——设置窗异步动作与命令面共用）。
type Installer struct {
	opts InstallOptions

	mu     sync.Mutex
	ledger map[string]Record
}

// NewInstaller 建安装器并读入台账（台账坏掉只记日志并从空表起步：装坏的台账不该让助手起不来）。
func NewInstaller(opts InstallOptions) (*Installer, error) {
	if !filepath.IsAbs(opts.Root) {
		return nil, fmt.Errorf("技能安装根必须是绝对路径（得到 %q）", opts.Root)
	}
	if !filepath.IsAbs(opts.CacheDir) {
		return nil, fmt.Errorf("技能缓存根必须是绝对路径（得到 %q）", opts.CacheDir)
	}
	if opts.LedgerPath == "" {
		return nil, errors.New("技能安装台账路径为空")
	}
	if opts.HTTP == nil {
		opts.HTTP = &http.Client{Timeout: 2 * time.Minute}
	}
	if opts.GitAvailable == nil {
		opts.GitAvailable = defaultGitAvailable
	}
	if opts.RunGit == nil {
		opts.RunGit = defaultRunGit
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.Logf == nil {
		opts.Logf = func(string, ...any) {}
	}
	for _, d := range []string{opts.Root, opts.CacheDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return nil, fmt.Errorf("创建技能目录 %s: %w", d, err)
		}
	}
	i := &Installer{opts: opts, ledger: map[string]Record{}}
	i.loadLedger()
	return i, nil
}

// Root 安装落位根（只读）。
func (i *Installer) Root() string { return i.opts.Root }

// Records 台账快照（按名字排序）。
func (i *Installer) Records() []Record {
	i.mu.Lock()
	defer i.mu.Unlock()
	return i.recordsLocked()
}

func (i *Installer) recordsLocked() []Record {
	out := make([]Record, 0, len(i.ledger))
	for _, r := range i.ledger {
		out = append(out, r)
	}
	sort.Slice(out, func(a, b int) bool { return out[a].Name < out[b].Name })
	return out
}

// loadLedger 读台账；目录已消失的条目顺手清掉（D202 口径三：目录消失即清条目）。
func (i *Installer) loadLedger() {
	data, err := os.ReadFile(i.opts.LedgerPath)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			i.opts.Logf("技能台账读不出（从空表起步）：%v", err)
		}
		return
	}
	var l ledger
	if err := json.Unmarshal(data, &l); err != nil {
		i.opts.Logf("技能台账解析失败（从空表起步）：%v", err)
		return
	}
	for _, r := range l.Installs {
		if strings.TrimSpace(r.Name) == "" {
			continue
		}
		if fi, err := os.Stat(filepath.Join(i.opts.Root, r.Name)); err != nil || !fi.IsDir() {
			continue // 副本已不在：条目随之作废
		}
		i.ledger[r.Name] = r
	}
}

// saveLedger 原子写台账（排序确定，便于 diff）。
func (i *Installer) saveLedger() error {
	l := ledger{Version: ledgerVersion, Installs: i.recordsLocked()}
	data, err := json.MarshalIndent(l, "", "  ")
	if err != nil {
		return fmt.Errorf("编码技能台账: %w", err)
	}
	if err := atomicfile.WriteFile(i.opts.LedgerPath, append(data, '\n'), 0o644); err != nil {
		return fmt.Errorf("写入技能台账: %w", err)
	}
	return nil
}

// rescan 变更后重扫清单（装配根经 Catalog 持有同一实例；nil = 未接线）。
func (i *Installer) rescan() {
	if i.opts.Catalog != nil {
		i.opts.Catalog.Rescan()
	}
}

// source 解析后的安装来源。
type source struct {
	kind string
	// path 本地路径（file / dir / zip）。
	path string
	// url git 仓库地址或压缩包下载地址。
	url string
	ref string
	// sub 来源里的扫描起点（相对仓库根；`/tree/<ref>/<sub>` 形态）。
	sub string
	// cache git 检出缓存目录名。
	cache string
}

// Install 安装一个来源（返回装上的技能；多技能包逐个安装）。
func (i *Installer) Install(ctx context.Context, spec string) (InstallReport, error) {
	i.mu.Lock()
	defer i.mu.Unlock()

	src, err := i.parseSource(spec)
	if err != nil {
		return InstallReport{}, err
	}
	var rep InstallReport
	root, prefix, kind, cleanup, err := i.prepare(ctx, src, &rep)
	if err != nil {
		return rep, err
	}
	defer cleanup()

	cands, skips := discoverSkills(root, installScanDepth)
	rep.Skipped = append(rep.Skipped, skips...)
	if len(cands) == 0 {
		msg := fmt.Sprintf("来源 %s 里没有可用的技能（需要含 SKILL.md 的目录）", spec)
		if len(skips) > 0 {
			msg += "；跳过的条目：" + strings.Join(skips, "；")
		}
		return rep, errors.New(msg)
	}

	// 全部先校验与查重，一个不安（避免装到一半才发现第三个同名）。
	existing := map[string]string{} // name → 已生效来源（含 skills.dirs 与已装副本）
	if i.opts.Catalog != nil {
		if list, err := i.opts.Catalog.List(ctx); err == nil {
			for _, sk := range list {
				existing[sk.Name] = sk.Source
			}
		}
	}
	seen := map[string]bool{}
	for _, c := range cands {
		if seen[c.name] {
			return rep, fmt.Errorf("来源里有多个同名技能 %q：一次安装同名会互相覆盖", c.name)
		}
		seen[c.name] = true
		if src2, ok := existing[c.name]; ok {
			return rep, fmt.Errorf("同名技能 %q 已存在（来自 %s）：请先卸载或改名后再安装", c.name, src2)
		}
		if fi, err := os.Stat(filepath.Join(i.opts.Root, c.name)); err == nil && fi.IsDir() {
			return rep, fmt.Errorf("同名技能 %q 已存在于安装目录：请先卸载后再安装", c.name)
		}
	}

	now := i.opts.Now().Format(time.RFC3339)
	var landed []string
	for _, c := range cands {
		if err := ctx.Err(); err != nil {
			i.rollback(landed)
			return rep, err
		}
		if err := i.land(c.name, c.dir); err != nil {
			i.rollback(landed)
			return rep, fmt.Errorf("安装技能 %s: %w", c.name, err)
		}
		landed = append(landed, c.name)
		rep.Installed = append(rep.Installed, InstalledSkill{
			Name: c.name,
			Dir:  filepath.Join(i.opts.Root, c.name),
		})
		i.ledger[c.name] = Record{
			Name:        c.name,
			Kind:        kind,
			Source:      spec,
			Ref:         src.ref,
			Subdir:      path.Join(appendPath(prefix, c.rel)...),
			FetchURL:    fetchURLOf(src, kind),
			Cache:       cacheNameOf(src, kind),
			InstalledAt: now,
		}
	}
	if err := i.saveLedger(); err != nil {
		i.rollback(landed)
		for _, n := range landed {
			delete(i.ledger, n)
		}
		return rep, err
	}
	i.rescan()
	return rep, nil
}

// Update 更新一个已装技能（name 空 = 更新全部可更新项）。
func (i *Installer) Update(ctx context.Context, name string) (InstallReport, error) {
	i.mu.Lock()
	defer i.mu.Unlock()

	names := []string{}
	if strings.TrimSpace(name) == "" {
		for _, r := range i.recordsLocked() {
			if r.Kind == kindGit || r.Kind == kindZip {
				names = append(names, r.Name)
			}
		}
		if len(names) == 0 {
			return InstallReport{}, errors.New("没有可更新的技能（只有 git 与压缩包来源能更新；复制来源请重新导入）")
		}
	} else {
		names = []string{strings.TrimSpace(name)}
	}

	var rep InstallReport
	var errs []string
	for _, n := range names {
		if err := ctx.Err(); err != nil {
			return rep, err
		}
		r, ok := i.ledger[n]
		if !ok {
			errs = append(errs, fmt.Sprintf("%s：没有安装记录（可能来自 skills.dirs 或手工放置），无法更新", n))
			continue
		}
		updated, err := i.updateOne(ctx, r)
		if err != nil {
			errs = append(errs, fmt.Sprintf("%s：%v", n, err))
			continue
		}
		rep.Installed = append(rep.Installed, updated)
	}
	if len(rep.Installed) > 0 {
		if err := i.saveLedger(); err != nil {
			return rep, err
		}
		i.rescan()
	}
	if len(errs) > 0 {
		return rep, errors.New(strings.Join(errs, "；"))
	}
	return rep, nil
}

// updateOne 更新单个技能（调用方持锁）。
func (i *Installer) updateOne(ctx context.Context, r Record) (InstalledSkill, error) {
	switch r.Kind {
	case kindGit:
		if !i.opts.GitAvailable() {
			return InstalledSkill{}, errors.New("未检测到 git，无法更新该来源（可先卸载再重新导入）")
		}
		cache := filepath.Join(i.opts.CacheDir, r.Cache)
		if fi, err := os.Stat(filepath.Join(cache, ".git")); err != nil || !fi.IsDir() {
			return InstalledSkill{}, errors.New("git 检出缓存已不存在，无法更新（请先卸载再重新导入）")
		}
		if err := i.gitSync(ctx, cache, r.Ref); err != nil {
			return InstalledSkill{}, err
		}
		srcDir := filepath.Join(cache, filepath.FromSlash(r.Subdir))
		if err := i.replace(r.Name, srcDir); err != nil {
			return InstalledSkill{}, err
		}
	case kindZip:
		var data []byte
		var err error
		if r.FetchURL != "" {
			data, err = i.fetch(ctx, r.FetchURL)
		} else {
			data, err = os.ReadFile(r.Source)
		}
		if err != nil {
			return InstalledSkill{}, fmt.Errorf("读取压缩包: %w", err)
		}
		stage, err := os.MkdirTemp(i.opts.CacheDir, "update-*")
		if err != nil {
			return InstalledSkill{}, fmt.Errorf("建临时解压目录: %w", err)
		}
		defer os.RemoveAll(stage)
		if err := extractZip(data, stage); err != nil {
			return InstalledSkill{}, err
		}
		srcDir := normalizedStage(stage)
		if r.Subdir != "" {
			srcDir = filepath.Join(srcDir, filepath.FromSlash(r.Subdir))
		}
		if err := i.replace(r.Name, srcDir); err != nil {
			return InstalledSkill{}, err
		}
	default:
		return InstalledSkill{}, errors.New("复制来源没有更新动作：重新导入即可（先卸载，再安装）")
	}
	r.UpdatedAt = i.opts.Now().Format(time.RFC3339)
	i.ledger[r.Name] = r
	return InstalledSkill{Name: r.Name, Dir: filepath.Join(i.opts.Root, r.Name)}, nil
}

// Remove 卸载一个安装副本（只删 `<profile>/skills/<name>`；外部技能根不动）。
func (i *Installer) Remove(ctx context.Context, name string) error {
	i.mu.Lock()
	defer i.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return errors.New("请给出要卸载的技能名")
	}
	if strings.ContainsAny(name, `/\`) || name == "." || name == ".." {
		return errors.New("技能名里不能带路径分隔符")
	}
	dir := filepath.Join(i.opts.Root, name)
	if filepath.Dir(dir) != filepath.Clean(i.opts.Root) {
		return errors.New("技能目录不在安装根内，拒绝删除")
	}
	fi, err := os.Stat(dir)
	if err != nil || !fi.IsDir() {
		if _, ok := i.ledger[name]; ok {
			delete(i.ledger, name)
			_ = i.saveLedger()
		}
		return fmt.Errorf("没有安装副本 %q（来自 skills.dirs 或手工放置的技能请在文件系统里处理）", name)
	}
	if err := os.RemoveAll(dir); err != nil {
		return fmt.Errorf("删除技能目录: %w", err)
	}
	rec, had := i.ledger[name]
	delete(i.ledger, name)
	if err := i.saveLedger(); err != nil {
		return err
	}
	if had && rec.Cache != "" && !i.cacheInUse(rec.Cache) {
		_ = os.RemoveAll(filepath.Join(i.opts.CacheDir, rec.Cache))
	}
	i.rescan()
	return nil
}

// cacheInUse 该 git 缓存是否还被别的已装技能引用（多技能仓库共用一个检出）。
func (i *Installer) cacheInUse(cache string) bool {
	for _, r := range i.ledger {
		if r.Cache == cache {
			return true
		}
	}
	return false
}

// rollback 安装中途失败：撤掉已落位的副本（不留半套）。
func (i *Installer) rollback(names []string) {
	for _, n := range names {
		_ = os.RemoveAll(filepath.Join(i.opts.Root, n))
		delete(i.ledger, n)
	}
}

// parseSource 判定来源种类（本地路径 / GitHub / 压缩包链接）。
func (i *Installer) parseSource(spec string) (source, error) {
	s := strings.TrimSpace(spec)
	s = strings.Trim(s, `"'`) // 资源管理器「复制为路径」带引号
	if s == "" {
		return source{}, errors.New("请给出来源：SKILL.md 文件、文件夹、.zip 压缩包或 GitHub 链接")
	}
	if isRemote(s) {
		return parseRemote(s)
	}
	if !filepath.IsAbs(s) {
		return source{}, fmt.Errorf("%q 不是绝对路径：这里不猜工作目录，请给完整路径", s)
	}
	fi, err := os.Stat(s)
	if err != nil {
		return source{}, fmt.Errorf("来源不存在或读不出：%w", err)
	}
	switch {
	case fi.IsDir():
		return source{kind: kindDir, path: filepath.Clean(s)}, nil
	case strings.EqualFold(filepath.Ext(s), ".zip"):
		return source{kind: kindZip, path: filepath.Clean(s)}, nil
	default:
		return source{kind: kindFile, path: filepath.Clean(s)}, nil
	}
}

// isRemote 是否网络来源（http/https/git@/以 .git 结尾的 scp 形态）。
func isRemote(s string) bool {
	l := strings.ToLower(s)
	if strings.HasPrefix(l, "http://") || strings.HasPrefix(l, "https://") {
		return true
	}
	return strings.HasPrefix(l, "git@") || strings.HasPrefix(l, "ssh://")
}

// parseRemote 解析远端来源：GitHub 仓库（可带 `#分支` 或 `/tree/<分支>/<子目录>`）与 .zip 链接。
func parseRemote(spec string) (source, error) {
	s := spec
	ref := ""
	if k := strings.LastIndex(s, "#"); k > 0 {
		ref = strings.TrimSpace(s[k+1:])
		s = s[:k]
	}
	u, err := url.Parse(s)
	if err != nil {
		return source{}, fmt.Errorf("链接解析失败：%w", err)
	}
	host := strings.ToLower(u.Hostname())
	if host == "github.com" {
		seg := splitPath(u.Path)
		if len(seg) < 2 {
			return source{}, errors.New("GitHub 链接要指到仓库：https://github.com/<owner>/<repo>")
		}
		owner, repo := seg[0], strings.TrimSuffix(seg[1], ".git")
		sub := ""
		// /owner/repo/tree/<ref>[/<sub>]
		if len(seg) >= 4 && seg[2] == "tree" {
			ref = seg[3]
			sub = strings.Join(seg[4:], "/")
		}
		repoURL := "https://github.com/" + owner + "/" + repo
		return source{kind: kindGit, url: repoURL, ref: ref, sub: sub, cache: cacheKey(repoURL, ref)}, nil
	}
	if strings.HasSuffix(strings.ToLower(u.Path), ".zip") {
		return source{kind: kindZip, url: s}, nil
	}
	if strings.HasSuffix(strings.ToLower(u.Path), ".git") || strings.HasPrefix(strings.ToLower(s), "git@") {
		return source{kind: kindGit, url: s, ref: ref, cache: cacheKey(s, ref)}, nil
	}
	return source{}, errors.New("只支持 GitHub 仓库链接、以 .zip 结尾的下载链接，或本地路径")
}

// splitPath 切 URL 路径段（去空段）。
func splitPath(p string) []string {
	var out []string
	for _, s := range strings.Split(p, "/") {
		if s != "" {
			out = append(out, s)
		}
	}
	return out
}

// prepare 把来源摊成一个可扫描的根目录：返回根、相对前缀（台账 Subdir 的基准）、
// 台账种类、清理函数。
//
// 前缀的含义：Subdir = 前缀 + 候选相对根路径。zip 的统一包装目录（GitHub 归档的
// `<repo>-<ref>/`）会被剥掉，故前缀为 ""；git 带子目录时前缀就是 `/tree/<ref>/<sub>` 那段。
func (i *Installer) prepare(ctx context.Context, src source, rep *InstallReport) (root, prefix, kind string, cleanup func(), err error) {
	noop := func() {}
	switch src.kind {
	case kindFile:
		stage, err := os.MkdirTemp(i.opts.CacheDir, "file-*")
		if err != nil {
			return "", "", "", noop, fmt.Errorf("建临时目录: %w", err)
		}
		cleanup = func() { os.RemoveAll(stage) }
		data, err := os.ReadFile(src.path)
		if err != nil {
			return "", "", "", cleanup, fmt.Errorf("读 SKILL.md: %w", err)
		}
		if err := os.WriteFile(filepath.Join(stage, skillFileName), data, 0o644); err != nil {
			return "", "", "", cleanup, fmt.Errorf("暂存 SKILL.md: %w", err)
		}
		return stage, "", kindFile, cleanup, nil

	case kindDir:
		return src.path, "", kindDir, noop, nil

	case kindZip:
		var data []byte
		if src.url != "" {
			var err error
			data, err = i.fetch(ctx, src.url)
			if err != nil {
				return "", "", "", noop, fmt.Errorf("下载压缩包: %w", err)
			}
		} else {
			var err error
			data, err = os.ReadFile(src.path)
			if err != nil {
				return "", "", "", noop, fmt.Errorf("读压缩包: %w", err)
			}
		}
		stage, err := os.MkdirTemp(i.opts.CacheDir, "zip-*")
		if err != nil {
			return "", "", "", noop, fmt.Errorf("建临时解压目录: %w", err)
		}
		cleanup = func() { os.RemoveAll(stage) }
		if err := extractZip(data, stage); err != nil {
			return "", "", "", cleanup, err
		}
		return normalizedStage(stage), "", kindZip, cleanup, nil

	case kindGit:
		cache := filepath.Join(i.opts.CacheDir, src.cache)
		if i.opts.GitAvailable() {
			if fi, err := os.Stat(filepath.Join(cache, ".git")); err == nil && fi.IsDir() {
				if err := i.gitSync(ctx, cache, src.ref); err != nil {
					return "", "", "", noop, err
				}
			} else {
				_ = os.RemoveAll(cache)
				args := []string{"clone", "--depth", "1"}
				if src.ref != "" {
					args = append(args, "--branch", src.ref)
				}
				args = append(args, src.url, cache)
				if _, err := i.opts.RunGit(ctx, "", args...); err != nil {
					return "", "", "", noop, fmt.Errorf("克隆 %s: %w", src.url, err)
				}
			}
			root := cache
			if src.sub != "" {
				root = filepath.Join(cache, filepath.FromSlash(src.sub))
			}
			return root, src.sub, kindGit, noop, nil
		}
		// 无 git：GitHub 仓库退回压缩包下载（更新时重新下载）。
		archive, ok := githubArchive(src.url, src.ref)
		if !ok {
			return "", "", "", noop, errors.New("未检测到 git，且来源不是 GitHub 仓库：请安装 git 或改用 .zip 压缩包")
		}
		rep.Notes = append(rep.Notes, "未检测到 git，已改用压缩包下载（更新时重新下载）")
		data, err := i.fetch(ctx, archive)
		if err != nil {
			return "", "", "", noop, fmt.Errorf("下载仓库压缩包: %w", err)
		}
		stage, err := os.MkdirTemp(i.opts.CacheDir, "zip-*")
		if err != nil {
			return "", "", "", noop, fmt.Errorf("建临时解压目录: %w", err)
		}
		cleanup = func() { os.RemoveAll(stage) }
		if err := extractZip(data, stage); err != nil {
			return "", "", "", cleanup, err
		}
		return normalizedStage(stage), "", kindZip, cleanup, nil
	}
	return "", "", "", noop, fmt.Errorf("未知来源种类 %q", src.kind)
}

// normalizedStage 剥掉压缩包的单一顶层目录（GitHub 归档与「先打包再分享」的常见形态）。
// 顶层已有 SKILL.md 或不止一个条目时原样返回——不猜用户的目录结构。
func normalizedStage(stage string) string {
	if fi, err := os.Stat(filepath.Join(stage, skillFileName)); err == nil && !fi.IsDir() {
		return stage
	}
	ents, err := os.ReadDir(stage)
	if err != nil {
		return stage
	}
	var only os.DirEntry
	n := 0
	for _, e := range ents {
		if strings.HasPrefix(e.Name(), "__MACOSX") {
			continue
		}
		n++
		only = e
	}
	if n == 1 && only.IsDir() {
		return filepath.Join(stage, only.Name())
	}
	return stage
}

// fetchURLOf 台账里记录的下载地址（git 来源只有仓库地址，下载地址是更新时才现算）。
func fetchURLOf(src source, kind string) string {
	if kind == kindZip && src.kind == kindGit {
		u, _ := githubArchive(src.url, src.ref)
		return u
	}
	if kind == kindZip {
		return src.url
	}
	return ""
}

// cacheNameOf 台账里的 git 缓存目录名（非 git 来源为空）。
func cacheNameOf(src source, kind string) string {
	if kind == kindGit {
		return src.cache
	}
	return ""
}

// cacheKey git 检出缓存目录名：主机/仓库可读前缀 + 短哈希（同名仓库不同分支不互相踩）。
func cacheKey(repoURL, ref string) string {
	sum := sha1.Sum([]byte(repoURL + "#" + ref))
	host := repoURL
	if u, err := url.Parse(repoURL); err == nil && u.Hostname() != "" {
		host = u.Hostname() + "-" + strings.Trim(u.Path, "/")
	} else {
		host = strings.TrimPrefix(repoURL, "git@")
	}
	var b strings.Builder
	for _, r := range strings.ToLower(host) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '/' || r == '.' || r == ':' || r == '_' || r == '-':
			b.WriteByte('-')
		}
	}
	prefix := strings.Trim(b.String(), "-")
	for strings.Contains(prefix, "--") {
		prefix = strings.ReplaceAll(prefix, "--", "-")
	}
	if len(prefix) > 60 {
		prefix = prefix[:60]
	}
	if prefix == "" {
		prefix = "repo"
	}
	return prefix + "-" + hex.EncodeToString(sum[:4])
}

// githubArchive GitHub 仓库压缩包下载地址（HEAD = 默认分支；分支名/SHA 均可）。
func githubArchive(repoURL, ref string) (string, bool) {
	u, err := url.Parse(repoURL)
	if err != nil || !strings.EqualFold(u.Hostname(), "github.com") {
		return "", false
	}
	seg := splitPath(u.Path)
	if len(seg) < 2 {
		return "", false
	}
	owner, repo := seg[0], strings.TrimSuffix(seg[1], ".git")
	if ref == "" {
		ref = "HEAD"
	}
	return fmt.Sprintf("https://github.com/%s/%s/archive/%s.zip", owner, repo, url.PathEscape(ref)), true
}

// fetch 下载远程文件（限 maxDownloadBytes）。
func (i *Installer) fetch(ctx context.Context, rawURL string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Aquarius-skill-installer")
	resp, err := i.opts.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("下载 %s：服务端返回 %s", rawURL, resp.Status)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxDownloadBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxDownloadBytes {
		return nil, fmt.Errorf("下载内容超过 %d MB 上限", maxDownloadBytes>>20)
	}
	return data, nil
}

// gitSync 同步已存在的检出：无分支走 `pull --ff-only`（失败退回 fetch+reset），
// 指定分支则 `fetch --depth 1 origin <ref>` + `reset --hard FETCH_HEAD`（D202 口径四）。
func (i *Installer) gitSync(ctx context.Context, dir, ref string) error {
	if ref == "" {
		if _, err := i.opts.RunGit(ctx, dir, "pull", "--ff-only"); err == nil {
			return nil
		}
		if _, err := i.opts.RunGit(ctx, dir, "fetch", "--depth", "1", "origin", "HEAD"); err != nil {
			return fmt.Errorf("拉取更新: %w", err)
		}
		if _, err := i.opts.RunGit(ctx, dir, "reset", "--hard", "FETCH_HEAD"); err != nil {
			return fmt.Errorf("应用更新: %w", err)
		}
		return nil
	}
	if _, err := i.opts.RunGit(ctx, dir, "fetch", "--depth", "1", "origin", ref); err != nil {
		return fmt.Errorf("拉取分支 %s: %w", ref, err)
	}
	if _, err := i.opts.RunGit(ctx, dir, "reset", "--hard", "FETCH_HEAD"); err != nil {
		return fmt.Errorf("应用分支 %s: %w", ref, err)
	}
	return nil
}

// candidate 来源里的一个技能候选。
type candidate struct {
	dir  string // 候选目录绝对路径
	rel  string // 相对来源根的路径（斜杠分隔）
	name string // frontmatter name
}

// discoverSkills 在来源根里向下找 SKILL.md（命中即停，不再下钻；与清单扫描同口径）。
// 坏的候选进 skips（用户面原因），不影响好的一起装。
func discoverSkills(root string, maxDepth int) ([]candidate, []string) {
	var (
		cands []candidate
		skips []string
	)
	var walk func(dir, rel string, depth int)
	walk = func(dir, rel string, depth int) {
		docPath := filepath.Join(dir, skillFileName)
		if fi, err := os.Stat(docPath); err == nil && !fi.IsDir() {
			fm, _, _, err := readSkill(docPath)
			if err != nil {
				skips = append(skips, fmt.Sprintf("%s 已跳过：%v", relOrDot(rel), err))
				return
			}
			cands = append(cands, candidate{dir: dir, rel: rel, name: fm.Name})
			return
		}
		if depth >= maxDepth {
			return
		}
		ents, err := os.ReadDir(dir)
		if err != nil {
			skips = append(skips, fmt.Sprintf("%s 读不出：%v", relOrDot(rel), err))
			return
		}
		for _, e := range ents {
			if !e.IsDir() || strings.HasPrefix(e.Name(), ".") || e.Name() == "node_modules" {
				continue
			}
			walk(filepath.Join(dir, e.Name()), path.Join(rel, e.Name()), depth+1)
		}
	}
	walk(root, "", 0)
	return cands, skips
}

// relOrDot 空相对路径显示为「根目录」。
func relOrDot(rel string) string {
	if rel == "" {
		return "根目录"
	}
	return rel
}

// land 把一个候选目录复制进安装根（先落临时目录并复核 frontmatter，再换入正式名）。
func (i *Installer) land(name, srcDir string) error {
	tmp, err := os.MkdirTemp(i.opts.Root, ".install-*")
	if err != nil {
		return fmt.Errorf("建临时安装目录: %w", err)
	}
	deferred := true
	defer func() {
		if deferred {
			_ = os.RemoveAll(tmp)
		}
	}()
	if err := copyTree(srcDir, tmp); err != nil {
		return err
	}
	// 校验前置（D202 口径六）：落位前最后再读一次，确认拷进来的 SKILL.md 仍是这个技能。
	fm, _, _, err := readSkill(filepath.Join(tmp, skillFileName))
	if err != nil {
		return fmt.Errorf("复制后校验失败：%w", err)
	}
	if fm.Name != name {
		return fmt.Errorf("复制后 name=%q 与预期 %q 不一致", fm.Name, name)
	}
	target := filepath.Join(i.opts.Root, name)
	if err := os.Rename(tmp, target); err != nil {
		return fmt.Errorf("落位到 %s: %w", target, err)
	}
	deferred = false
	return nil
}

// replace 用来源目录整体替换已装副本（更新语义；先备份旧副本，失败回滚）。
func (i *Installer) replace(name, srcDir string) error {
	if fi, err := os.Stat(srcDir); err != nil || !fi.IsDir() {
		return fmt.Errorf("更新来源里找不到技能目录 %s（上游可能改名或删掉了）", srcDir)
	}
	fm, _, _, err := readSkill(filepath.Join(srcDir, skillFileName))
	if err != nil {
		return fmt.Errorf("更新来源校验失败：%w", err)
	}
	if fm.Name != name {
		return fmt.Errorf("更新来源的 name=%q 与已装技能 %q 不一致（上游改过 name，请先卸载再安装）",
			fm.Name, name)
	}
	tmp, err := os.MkdirTemp(i.opts.Root, ".install-*")
	if err != nil {
		return fmt.Errorf("建临时安装目录: %w", err)
	}
	defer os.RemoveAll(tmp)
	if err := copyTree(srcDir, tmp); err != nil {
		return err
	}
	target := filepath.Join(i.opts.Root, name)
	backupRoot, err := os.MkdirTemp(i.opts.CacheDir, "replace-*")
	if err != nil {
		return fmt.Errorf("建回滚目录: %w", err)
	}
	defer os.RemoveAll(backupRoot)
	backup := filepath.Join(backupRoot, name)
	if err := os.Rename(target, backup); err != nil {
		return fmt.Errorf("暂存旧副本: %w", err)
	}
	if err := os.Rename(tmp, target); err != nil {
		_ = os.Rename(backup, target) // 尽量回滚
		return fmt.Errorf("换入新副本: %w", err)
	}
	return nil
}

// copyTree 递归复制（跳过 `.git` 与符号链接；条目/总量设上限）。
func copyTree(src, dst string) error {
	var (
		files int
		bytes int64
	)
	var walk func(from, to string) error
	walk = func(from, to string) error {
		ents, err := os.ReadDir(from)
		if err != nil {
			return fmt.Errorf("读目录 %s: %w", from, err)
		}
		for _, e := range ents {
			name := e.Name()
			if name == ".git" {
				continue
			}
			srcPath := filepath.Join(from, name)
			dstPath := filepath.Join(to, name)
			info, err := e.Info()
			if err != nil {
				return fmt.Errorf("读条目 %s: %w", srcPath, err)
			}
			if info.Mode()&os.ModeSymlink != 0 {
				continue // 不跟随符号链接（可能指向来源之外）
			}
			if info.IsDir() {
				if err := os.MkdirAll(dstPath, 0o755); err != nil {
					return fmt.Errorf("建目录 %s: %w", dstPath, err)
				}
				if err := walk(srcPath, dstPath); err != nil {
					return err
				}
				continue
			}
			files++
			if files > maxCopyFiles {
				return fmt.Errorf("技能文件数超过 %d 个上限", maxCopyFiles)
			}
			bytes += info.Size()
			if bytes > maxCopyBytes {
				return fmt.Errorf("技能总大小超过 %d MB 上限", maxCopyBytes>>20)
			}
			if err := copyFile(srcPath, dstPath, info.Mode().Perm()); err != nil {
				return err
			}
		}
		return nil
	}
	return walk(src, dst)
}

// copyFile 单文件复制（保留可执行位：`scripts/` 里的脚本要能直接跑）。
func copyFile(src, dst string, perm os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("读 %s: %w", src, err)
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, perm)
	if err != nil {
		return fmt.Errorf("写 %s: %w", dst, err)
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return fmt.Errorf("复制 %s: %w", src, err)
	}
	return out.Close()
}

// extractZip 解压到 dest（zip-slip 防护 + 条目/展开总量上限；符号链接条目直接拒绝）。
func extractZip(data []byte, dest string) error {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return fmt.Errorf("压缩包读不出：%w", err)
	}
	if len(zr.File) > maxZipEntries {
		return fmt.Errorf("压缩包条目数 %d 超过 %d 上限", len(zr.File), maxZipEntries)
	}
	var total int64
	for _, f := range zr.File {
		rel, err := safeZipPath(f.Name)
		if err != nil {
			return err
		}
		if rel == "" {
			continue
		}
		if f.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("压缩包里有符号链接 %q：拒绝解压", f.Name)
		}
		target := filepath.Join(dest, filepath.FromSlash(rel))
		if f.FileInfo().IsDir() {
			if err := os.MkdirAll(target, 0o755); err != nil {
				return fmt.Errorf("建目录 %s: %w", target, err)
			}
			continue
		}
		if f.UncompressedSize64 > maxZipFileBytes {
			return fmt.Errorf("压缩包内文件 %q 超过 %d MB 上限", f.Name, maxZipFileBytes>>20)
		}
		total += int64(f.UncompressedSize64)
		if total > maxZipBytes {
			return fmt.Errorf("压缩包解压总量超过 %d MB 上限", maxZipBytes>>20)
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return fmt.Errorf("建目录 %s: %w", target, err)
		}
		if err := extractZipFile(f, target); err != nil {
			return err
		}
	}
	return nil
}

// extractZipFile 解一个条目（按上限截断检测，避免谎报大小撑爆磁盘）。
func extractZipFile(f *zip.File, target string) error {
	rc, err := f.Open()
	if err != nil {
		return fmt.Errorf("打开压缩包条目 %s: %w", f.Name, err)
	}
	defer rc.Close()
	out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return fmt.Errorf("写 %s: %w", target, err)
	}
	if _, err := io.Copy(out, io.LimitReader(rc, maxZipFileBytes+1)); err != nil {
		out.Close()
		return fmt.Errorf("解压 %s: %w", f.Name, err)
	}
	return out.Close()
}

// safeZipPath 校验并归一压缩包条目路径（拒绝绝对路径、盘符、`..` 与空名）。
func safeZipPath(name string) (string, error) {
	n := strings.ReplaceAll(name, `\`, "/")
	if strings.TrimSpace(n) == "" {
		return "", nil
	}
	if strings.HasPrefix(n, "/") {
		return "", fmt.Errorf("压缩包条目 %q 是绝对路径：拒绝解压", name)
	}
	if len(n) >= 2 && n[1] == ':' {
		return "", fmt.Errorf("压缩包条目 %q 带盘符：拒绝解压", name)
	}
	clean := path.Clean(n)
	if clean == "." {
		return "", nil
	}
	if clean == ".." || strings.HasPrefix(clean, "../") {
		return "", fmt.Errorf("压缩包条目 %q 越出目标目录：拒绝解压", name)
	}
	return clean, nil
}

// appendPath 拼接非空路径段（空段不进结果）。
func appendPath(base, extra string) []string {
	var out []string
	if base != "" {
		out = append(out, filepath.ToSlash(base))
	}
	if extra != "" {
		out = append(out, filepath.ToSlash(extra))
	}
	if len(out) == 0 {
		return []string{""}
	}
	return out
}

// defaultGitAvailable 主机是否有 git。
func defaultGitAvailable() bool {
	_, err := exec.LookPath("git")
	return err == nil
}

// defaultRunGit 执行 git（合并输出；失败带尾部输出，便于用户判断是认证还是网络问题）。
func defaultRunGit(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	if dir != "" {
		cmd.Dir = dir
	}
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	setupGitProc(cmd)
	out, err := cmd.CombinedOutput()
	text := string(out)
	if err != nil {
		return text, fmt.Errorf("git %s: %v%s", strings.Join(args, " "), err, tailOutput(text))
	}
	return text, nil
}

// tailOutput 取输出尾部（报错时带上下文，不把整份克隆进度塞进错误框）。
func tailOutput(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	lines := strings.Split(s, "\n")
	if len(lines) > 6 {
		lines = lines[len(lines)-6:]
	}
	return "：" + strings.Join(lines, " / ")
}
