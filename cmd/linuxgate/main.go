// Command linuxgate 跑非 Windows 目标的构建门禁（S4b/D111 出口项）。
//
// 为什么需要它：`uigui` 依赖 Gio，而 Gio 在 Linux 上**必须 cgo**（X11/Wayland/EGL/
// Vulkan 全走 cgo；`CGO_ENABLED=0` 时 `gioui.org/internal/vk` 与 `internal/gl` 直接
// 「build constraints exclude all Go files」）。因此在 Windows 上无法用单个
// `GOOS=linux go build ./...` 验证——Go 自带的交叉编译不带 Linux C 工具链与开发头。
//
// 本工具在 Windows 宿主上经 WSL 调用 Linux 侧原生工具链（工具链齐全时 cgo 天然可用），
// 在 Linux/macOS 宿主上直接本地跑。两侧口径一致：`go build ./...` + `go vet ./...`
// （vet 会一并类型检查 _test.go，能挡住「测试文件依赖 Windows 专属符号」这类缺口）。
//
// 前置（WSL 内一次性）：
//
//	sudo apt-get install -y golang-go xorg-dev libx11-xcb-dev libxkbcommon-dev \
//	    libxkbcommon-x11-dev libwayland-dev libegl1-mesa-dev libvulkan-dev
//
// 用法：go run ./cmd/linuxgate            # 缺省 linux/amd64，WSL 发行版 Ubuntu
package main

import (
	"bytes"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"unicode"
)

// aptHint 缺头文件时的补救命令（Linux 侧一次性安装）。
const aptHint = "sudo apt-get install -y golang-go xorg-dev libx11-xcb-dev libxkbcommon-dev " +
	"libxkbcommon-x11-dev libwayland-dev libegl1-mesa-dev libvulkan-dev"

// headerHints 头文件缺失 → 说明（Gio 的 Linux 后端依赖这些开发包）。
var headerHints = map[string]string{
	"vulkan/vulkan.h":  "libvulkan-dev（Gio Vulkan 后端）",
	"X11/Xlib.h":       "xorg-dev / libx11-dev（X11 后端）",
	"EGL/egl.h":        "libegl1-mesa-dev（GL 上下文）",
	"wayland-client.h": "libwayland-dev（Wayland 后端）",
}

func main() {
	distro := flag.String("distro", "Ubuntu", "WSL 发行版名（仅 Windows 宿主使用）")
	target := flag.String("target", "linux", "目标 GOOS")
	proxy := flag.String("proxy", "https://goproxy.cn,direct", "模块代理（GOPROXY）")
	modcache := flag.String("modcache", "", "共享模块缓存目录（Windows 路径；空 = 自动探测，用于免下载）")
	runVet := flag.Bool("vet", true, "同时跑 go vet ./...")
	flag.Parse()

	root, err := repoRoot()
	if err != nil {
		fail(err)
	}
	if code := gate(*distro, *target, *proxy, *modcache, root, *runVet); code != 0 {
		os.Exit(code)
	}
}

// gate 依次跑各步骤；返回进程退出码（0 = 全绿）。
func gate(distro, target, proxy, modcache, root string, runVet bool) int {
	steps := []string{"build ./..."}
	if runVet {
		steps = append(steps, "vet ./...")
	}
	if runtime.GOOS != "windows" { // 原生 Linux/macOS：本机工具链直接跑
		if target != runtime.GOOS {
			fmt.Fprintf(os.Stderr, "注意：宿主 %s 上交叉编译 %s 需目标平台 C 工具链（cgo）\n", runtime.GOOS, target)
		}
		for _, s := range steps {
			if !runLocal(target, proxy, s) {
				return 1
			}
		}
		return 0
	}

	// Windows 宿主：经 WSL 调 Linux 侧工具链（gio Linux 后端必须 cgo）。
	repoWSL, err := wslPath(root)
	if err != nil {
		fail(fmt.Errorf("仓库路径无法映射进 WSL：%w（请在 WSL 可见的盘符下检出）", err))
	}
	if modcache == "" {
		modcache = defaultModCache() // 免下载：复用 Windows 侧已下好的模块缓存
	}
	mcWSL := ""
	if modcache != "" {
		if p, err := wslPath(modcache); err == nil {
			mcWSL = p
		} else {
			fmt.Fprintf(os.Stderr, "忽略模块缓存 %s：%v\n", modcache, err)
		}
	}
	fmt.Printf("== linuxgate：宿主 %s/%s → 目标 %s（WSL 发行版 %s）==\n",
		runtime.GOOS, runtime.GOARCH, target, distro)
	script := "cd " + shellQuote(repoWSL) + " && go env GOOS GOARCH CGO_ENABLED GOVERSION"
	if out, err := wsl(distro, script); err != nil {
		fail(fmt.Errorf("WSL 调用失败：%v\n%s\n提示：wsl -l -v 看发行版名，或 -distro 指定", err, out))
	}
	env := []string{"GOPROXY=" + proxy, "GOTOOLCHAIN=local", "GOCACHE=$HOME/.cache/go-build"}
	if mcWSL != "" {
		env = append(env, "GOMODCACHE="+mcWSL)
	}
	for _, s := range steps {
		fmt.Printf("\n== go %s ==\n", s)
		cmd := "cd " + shellQuote(repoWSL) + " && export " + strings.Join(env, " ") + " && go " + s
		out, err := wsl(distro, cmd)
		if err != nil {
			fmt.Fprintf(os.Stderr, "\n[FAIL] go %s\n%s", s, out)
			hint(out)
			return 1
		}
	}
	fmt.Println("\n[OK] Linux 目标：build + vet 全绿")
	return 0
}

// defaultModCache Windows 侧模块缓存（go env GOMODCACHE 优先，回落 %USERPROFILE%\go\pkg\mod）。
// 复用它可以免去在 WSL 内重新下载全部依赖（国内镜像拉 2GB 级缓存很慢）。
func defaultModCache() string {
	if v := os.Getenv("GOMODCACHE"); v != "" {
		if st, err := os.Stat(v); err == nil && st.IsDir() {
			return v
		}
	}
	if home := os.Getenv("USERPROFILE"); home != "" {
		p := filepath.Join(home, "go", "pkg", "mod")
		if st, err := os.Stat(p); err == nil && st.IsDir() {
			return p
		}
	}
	return ""
}

// runLocal 非 Windows 宿主的本地执行（GOOS=target 与宿主一致时即原生构建）。
func runLocal(target, proxy, step string) bool {
	fmt.Printf("\n== go %s (GOOS=%s) ==\n", step, target)
	cmd := exec.Command("go", strings.Fields(step)...)
	cmd.Env = append(os.Environ(), "GOOS="+target, "GOPROXY="+proxy)
	var buf bytes.Buffer
	cmd.Stdout = io.MultiWriter(os.Stdout, &buf)
	cmd.Stderr = io.MultiWriter(os.Stderr, &buf)
	if err := cmd.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "\n[FAIL] go %s\n", step)
		hint(buf.String())
		return false
	}
	return true
}

// wsl 调 WSL 内 bash 执行脚本；输出边流边收集（供失败诊断），并滤掉 wsl.exe 自身的
// 环境告警（见 quietWriter）。
func wsl(distro, script string) (string, error) {
	cmd := exec.Command("wsl", "-d", distro, "--", "bash", "-lc", script)
	out := &quietWriter{w: os.Stdout}
	errw := &quietWriter{w: os.Stderr}
	cmd.Stdout, cmd.Stderr = out, errw
	err := cmd.Run()
	out.flush()
	errw.flush()
	if n := out.dropped + errw.dropped; n > 0 {
		fmt.Printf("[已滤除 wsl.exe 环境告警 %d 行]\n", n)
	}
	return out.text.String() + errw.text.String(), err
}

// quietWriter 行过滤输出：丢弃 wsl.exe 打印的「localhost 代理未镜像到 WSL」告警。
// 该告警来自 WSL 自身的 autoProxy 探测（与本次构建无关），且在非 UTF-8 控制台上以
// UTF-16 字节形式出现（字符间夹空格）——按「去掉空白后含 localhost 与 WSL」判定。
type quietWriter struct {
	w       io.Writer
	text    bytes.Buffer // 全量留档（失败提示用）
	part    []byte       // 未成行的残段
	dropped int          // 滤除数（环境告警；收尾一次性说明）
}

func (q *quietWriter) Write(p []byte) (int, error) {
	q.part = append(q.part, p...)
	for {
		i := bytes.IndexByte(q.part, '\n')
		if i < 0 {
			break
		}
		q.line(q.part[:i+1])
		q.part = q.part[i+1:]
	}
	return len(p), nil
}

// flush 输出末尾残行（无换行结尾）。
func (q *quietWriter) flush() {
	if len(q.part) > 0 {
		q.line(q.part)
		q.part = nil
	}
}

func (q *quietWriter) line(b []byte) {
	if isWSLNoise(string(b)) {
		q.dropped++
		return // wsl.exe 环境告警：不入流、不留档
	}
	q.text.Write(b)
	_, _ = q.w.Write(b)
}

// isWSLNoise 判定 wsl.exe 的「localhost 代理未镜像到 WSL」告警。它在管道里是 UTF-16
// 字节（字符间夹 NUL），且中文控制台下整行编码混杂——故先剥 NUL 与空白再匹配。
func isWSLNoise(line string) bool {
	c := strings.Join(strings.Fields(strings.ReplaceAll(line, "\x00", "")), "")
	if c == "" {
		return false
	}
	low := strings.ToLower(c)
	return strings.HasPrefix(low, "wsl:") && strings.Contains(low, "localhost")
}

// hint 按失败输出给补救提示（缺头文件是最常见的一类）。
func hint(out string) {
	for h, why := range headerHints {
		if strings.Contains(out, h) {
			fmt.Fprintf(os.Stderr, "缺 %s（%s）→ 在 WSL 内执行：\n  %s\n", h, why, aptHint)
			return
		}
	}
	if strings.Contains(out, "cannot find package") || strings.Contains(out, "module lookup disabled") {
		fmt.Fprintf(os.Stderr, "依赖取不到 → 确认 -proxy（缺省 %s）或在 WSL 内先 go mod download\n",
			"https://goproxy.cn,direct")
	}
}

// repoRoot 自 cwd 上溯找 go.mod（最多 4 层）。
func repoRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for i := 0; i < 5; i++ {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return "", fmt.Errorf("未找到 go.mod：请在仓库内运行（当前 %s）", dir)
}

// wslPath Windows 路径 → WSL 挂载路径（E:\Aquarius → /mnt/e/Aquarius）。
func wslPath(p string) (string, error) {
	v := filepath.Clean(p)
	if strings.HasPrefix(v, `\\`) {
		return "", fmt.Errorf("UNC 路径 %q 不在 WSL 挂载内", p)
	}
	if len(v) < 2 || v[1] != ':' {
		return "", fmt.Errorf("非 Windows 盘符路径 %q", p)
	}
	drive := unicode.ToLower(rune(v[0]))
	rest := strings.ReplaceAll(v[2:], `\`, "/")
	if rest == "/" { // 盘符根：E:\ → /mnt/e
		rest = ""
	}
	return "/mnt/" + string(drive) + rest, nil
}

// shellQuote 单引号包裹（路径含空格/中文时的最小转义）。
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// fail 打印错误并以用法错误码退出。
func fail(err error) {
	fmt.Fprintln(os.Stderr, "linuxgate:", err)
	os.Exit(2)
}
