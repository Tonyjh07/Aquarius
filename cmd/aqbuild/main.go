// Command aqbuild 构建一个「带某个 Tier-1 插件」的宿主二进制（plugin-design.md §7
// 「插件怎么进到二进制里」）。
//
// 它只认路径、包名与 tag，不认识任何具体插件：插件作者在自己仓库里跑它，得到一份可执行
// 文件；宿主仓库不新增文件、不改 go.mod。注入靠三条只影响本次编译的 flag——`-overlay`
// 把宿主的 `pluginext.go` 换成插件带来的实现，`-modfile` 让依赖解析读工作区里那份
// go.mod 副本，`-tags` 打开互斥开关。
//
// 用法：
//
//	go run ./cmd/aqbuild -plugin ../aq-memory-plugin
//	go run ./cmd/aqbuild -plugin ../aq-memory-plugin -host . -o /tmp/aquarius-mem.exe -v
//
// 产物与中间文件全部落在插件仓库或缓存目录，宿主 `git status` 不变。
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// stdout 摘要输出（测试里可换掉）。
var stdout io.Writer = os.Stdout

type options struct {
	plugin  string // 插件仓库根
	hook    string // 注入实现文件（相对插件根）
	host    string // 宿主仓库根
	target  string // 被替换的中性接入口（相对宿主根）
	pkg     string // 要构建的包（相对宿主根）
	tag     string // 互斥开关
	out     string // 产物路径
	work    string // 工作区（modfile / go.sum 副本 / overlay.json）
	verbose bool   // 打每条 go 命令
}

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "aqbuild:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	opt, err := parse(args)
	if err != nil {
		return err
	}
	abs, err := opt.resolve()
	if err != nil {
		return err
	}
	out, err := abs.build()
	if err != nil {
		return err
	}
	fmt.Fprintf(stdout, "构建完成：%s\n", out)
	return nil
}

func parse(args []string) (options, error) {
	var o options
	fs := flag.NewFlagSet("aqbuild", flag.ContinueOnError)
	fs.StringVar(&o.plugin, "plugin", "", "插件仓库根（含 go.mod），必填")
	fs.StringVar(&o.hook, "hook", "pluginext.go", "注入实现文件（相对插件根）")
	fs.StringVar(&o.host, "host", ".", "宿主仓库根")
	fs.StringVar(&o.target, "target", "cmd/aquarius/pluginext.go", "被替换的中性接入口（相对宿主根）")
	fs.StringVar(&o.pkg, "pkg", "./cmd/aquarius", "要构建的包（相对宿主根）")
	fs.StringVar(&o.tag, "tag", "tier1sample", "互斥开关（与注入文件的构建约束成对）")
	fs.StringVar(&o.out, "o", "", "产物路径（缺省：插件目录下的 <宿主模块名>-with-plugin）")
	fs.StringVar(&o.work, "work", "", "工作区目录（缺省：系统缓存目录下 aquarius/aqbuild/<宿主>-<插件>-<hash>）")
	fs.BoolVar(&o.verbose, "v", false, "打印每条 go 命令")
	if err := fs.Parse(args); err != nil {
		return o, err
	}
	if fs.NArg() > 0 {
		return o, fmt.Errorf("多余的参数：%s", strings.Join(fs.Args(), " "))
	}
	if strings.TrimSpace(o.plugin) == "" {
		return o, errors.New("缺少 -plugin：请给出插件仓库根（含 go.mod）")
	}
	return o, nil
}

// resolved 全部路径都转成绝对路径：overlay 与 -modfile 都不认相对路径，
// 而构建子进程的工作目录是宿主根。
type resolved struct {
	options
	pluginDir string
	hookFile  string
	hostDir   string
	targetF   string
	outFile   string
	workDir   string
	pluginMod string
	hostMod   string
}

func (o options) resolve() (resolved, error) {
	r := resolved{options: o}
	var err error
	if r.pluginDir, err = absDir(o.plugin, "插件仓库"); err != nil {
		return r, err
	}
	hint := filepath.Join(r.pluginDir, o.hook)
	if r.hookFile, err = absFile(hint, "注入实现文件"); err != nil {
		return r, fmt.Errorf("%w（用 -hook 指定它相对插件根的位置）", err)
	}
	if r.hostDir, err = absDir(o.host, "宿主仓库"); err != nil {
		return r, err
	}
	target := filepath.Join(r.hostDir, o.target)
	if r.targetF, err = absFile(target, "接入口"); err != nil {
		return r, fmt.Errorf("%w（用 -target 指定 -host 下的相对路径）", err)
	}
	if r.pluginMod, err = modulePath(filepath.Join(r.pluginDir, "go.mod")); err != nil {
		return r, fmt.Errorf("读插件 go.mod：%w", err)
	}
	if r.hostMod, err = modulePath(filepath.Join(r.hostDir, "go.mod")); err != nil {
		return r, fmt.Errorf("读宿主 go.mod：%w", err)
	}
	r.outFile = o.out
	if r.outFile == "" {
		base := modBase(r.hostMod) + "-with-plugin" + exeSuffix()
		r.outFile = filepath.Join(r.pluginDir, base)
	}
	if r.outFile, err = filepath.Abs(r.outFile); err != nil {
		return r, err
	}
	r.workDir = o.work
	if r.workDir == "" {
		cache, cerr := os.UserCacheDir()
		if cerr != nil {
			return r, fmt.Errorf("取系统缓存目录：%w（用 -work 显式指定）", cerr)
		}
		key := modBase(r.hostMod) + "-" + modBase(r.pluginMod) + "-" + shortHash(r.hostDir+"\x00"+r.pluginDir+"\x00"+r.targetF+"\x00"+o.tag)
		r.workDir = filepath.Join(cache, "aquarius", "aqbuild", key)
	}
	if r.workDir, err = filepath.Abs(r.workDir); err != nil {
		return r, err
	}
	return r, nil
}

// build 四步：建工作区 → 改依赖视图 → 写 overlay → 构建。
func (r resolved) build() (string, error) {
	if err := os.MkdirAll(r.workDir, 0o755); err != nil {
		return "", fmt.Errorf("建工作区：%w", err)
	}
	modfile := filepath.Join(r.workDir, "build.mod")
	sumfile := filepath.Join(r.workDir, "build.sum")
	if err := copyFile(filepath.Join(r.hostDir, "go.mod"), modfile); err != nil {
		return "", fmt.Errorf("复制 go.mod：%w", err)
	}
	// go.sum 一起搬：副本的 .sum 若从空开始，-mod=mod 会把每条依赖都当成新下载。
	if err := copyFile(filepath.Join(r.hostDir, "go.sum"), sumfile); err != nil && !os.IsNotExist(err) {
		return "", fmt.Errorf("复制 go.sum：%w", err)
	}

	// 先改写宿主原有的相对 replace，再加插件的 require / replace：modfile 挪出宿主目录
	// 之后，`./pluginapi` 会按工作区解析，不改写就找不到模块。
	if err := r.absolutizeReplaces(modfile); err != nil {
		return "", err
	}
	if err := r.goModEdit(modfile,
		"-require="+r.pluginMod+"@v0.0.0",
		"-replace="+r.pluginMod+"="+slash(r.pluginDir),
	); err != nil {
		return "", err
	}

	overlay := filepath.Join(r.workDir, "overlay.json")
	if err := writeOverlay(overlay, r.targetF, r.hookFile); err != nil {
		return "", err
	}

	sum := []string{
		"build",
		"-modfile=" + modfile,
		"-mod=mod",
		"-overlay=" + overlay,
		"-tags=" + r.tag,
		"-o", r.outFile,
		r.pkg,
	}
	if err := r.runGo(r.hostDir, sum...); err != nil {
		return "", err
	}
	return r.outFile, nil
}

// modReplace `go mod edit -json` 里 replace 的形状（只取本工具要用的字段）。
type modReplace struct {
	Old struct{ Path, Version string }
	New struct{ Path, Version string }
}

func (r resolved) absolutizeReplaces(modfile string) error {
	var info struct{ Replace []modReplace }
	raw, err := r.goOutput(r.hostDir, "mod", "edit", "-json", "-modfile="+modfile)
	if err != nil {
		return fmt.Errorf("读 replace 列表：%w", err)
	}
	if err := json.Unmarshal(raw, &info); err != nil {
		return fmt.Errorf("解析 go mod edit 输出：%w", err)
	}
	for _, rp := range info.Replace {
		if rp.New.Version != "" || !isLocalPath(rp.New.Path) {
			continue // 版本形式的替换与远端路径不受工作区影响
		}
		target := rp.New.Path
		if !filepath.IsAbs(target) {
			target = filepath.Join(r.hostDir, target)
		}
		if abs, aerr := filepath.Abs(target); aerr == nil {
			target = abs
		}
		spec := rp.Old.Path
		if rp.Old.Version != "" {
			spec += "@" + rp.Old.Version
		}
		if err := r.goModEdit(modfile,
			"-dropreplace="+spec,
			"-replace="+spec+"="+slash(target),
		); err != nil {
			return err
		}
	}
	return nil
}

func (r resolved) goModEdit(modfile string, edits ...string) error {
	args := append([]string{"mod", "edit", "-modfile=" + modfile}, edits...)
	if _, err := r.goOutput(r.hostDir, args...); err != nil {
		return fmt.Errorf("改模组文件（%s）：%w", strings.Join(edits, " "), err)
	}
	return nil
}

func (r resolved) runGo(dir string, args ...string) error {
	if _, err := r.goOutput(dir, args...); err != nil {
		return fmt.Errorf("go %s 失败：%w", args[0], err)
	}
	return nil
}

// goOutput 跑一条 go 命令，返回 stdout；失败时把 stderr 一起带进错误里。
// 子进程的 GOFLAGS 清空：用户环境里的 -mod=readonly 之类会让这一步失败。
func (r resolved) goOutput(dir string, args ...string) ([]byte, error) {
	if r.verbose {
		fmt.Fprintf(stdout, "  go %s\n", strings.Join(args, " "))
	}
	cmd := exec.Command("go", args...)
	cmd.Dir = dir
	cmd.Env = cleanEnv()
	var errBuf strings.Builder
	cmd.Stderr = &errBuf
	out, err := cmd.Output()
	if err != nil {
		msg := strings.TrimSpace(errBuf.String())
		if msg == "" {
			msg = err.Error()
		}
		return nil, errors.New(msg)
	}
	return out, nil
}

func cleanEnv() []string {
	env := os.Environ()
	out := make([]string, 0, len(env)+1)
	for _, e := range env {
		if strings.HasPrefix(e, "GOFLAGS=") {
			continue
		}
		out = append(out, e)
	}
	return append(out, "GOFLAGS=")
}

func writeOverlay(path, target, hook string) error {
	// overlay 的键值都必须是绝对路径；换上去的文件不参与判定包名，只替换内容。
	body := struct {
		Replace map[string]string
	}{Replace: map[string]string{target: hook}}
	data, err := json.MarshalIndent(body, "", "  ")
	if err != nil {
		return fmt.Errorf("编码 overlay：%w", err)
	}
	data = append(data, '\n')
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("写 overlay：%w", err)
	}
	return nil
}

func modulePath(gomod string) (string, error) {
	data, err := os.ReadFile(gomod)
	if err != nil {
		return "", err
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if rest, ok := strings.CutPrefix(line, "module "); ok {
			if p := strings.TrimSpace(rest); p != "" {
				return p, nil
			}
		}
	}
	return "", fmt.Errorf("%s 里没有 module 行", gomod)
}

func absDir(p, what string) (string, error) {
	abs, err := filepath.Abs(p)
	if err != nil {
		return "", fmt.Errorf("%s 路径：%w", what, err)
	}
	info, err := os.Stat(abs)
	if err != nil {
		return "", fmt.Errorf("%s %s：%w", what, abs, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("%s %s 不是目录", what, abs)
	}
	if _, err := os.Stat(filepath.Join(abs, "go.mod")); err != nil {
		return "", fmt.Errorf("%s %s 下没有 go.mod", what, abs)
	}
	return abs, nil
}

func absFile(p, what string) (string, error) {
	abs, err := filepath.Abs(p)
	if err != nil {
		return "", fmt.Errorf("%s 路径：%w", what, err)
	}
	if _, err := os.Stat(abs); err != nil {
		return "", fmt.Errorf("%s %s：%w", what, abs, err)
	}
	return abs, nil
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// shortHash 工作区目录名的去重后缀：同一对宿主/插件换参数重建时落同一个目录。
// shortHash 工作区名字的短指纹：同一对宿主/插件重复构建命中同一个目录，换一边就换目录。
func shortHash(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:6])
}

func isLocalPath(p string) bool {
	return p == "." || p == ".." || strings.HasPrefix(p, "./") || strings.HasPrefix(p, `.\`) ||
		strings.HasPrefix(p, "../") || strings.HasPrefix(p, `..\`) || filepath.IsAbs(p)
}

// slash 写进 go.mod 的路径统一用正斜杠：Windows 的反斜杠在 modfile 里是转义字符。
func slash(p string) string { return filepath.ToSlash(p) }

func exeSuffix() string {
	if runtime.GOOS == "windows" {
		return ".exe"
	}
	return ""
}

// modBase 取模块路径最后一段（模块路径总是正斜杠形态，与平台无关）。
func modBase(mod string) string {
	if i := strings.LastIndexByte(mod, '/'); i >= 0 {
		return mod[i+1:]
	}
	return mod
}
