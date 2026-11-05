package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

type status string

const (
	stPass status = "pass"
	stFail status = "fail"
	stSkip status = "skip"
)

// jobResult 一条门禁的结果。写进证据文件，也是 JSON 输出的单元。
type jobResult struct {
	Name       string `json:"name"`
	Status     status `json:"status"`
	DurationMS int64  `json:"duration_ms"`
	Note       string `json:"note,omitempty"`
	Reason     string `json:"reason,omitempty"` // 跳过或失败的一句话原因
	Output     string `json:"output,omitempty"` // 失败时的末尾输出
}

// capState 一项外部能力可否使用。reason 只在不可用时填。
type capState struct {
	ok     bool
	reason string
}

// execute 按波次跑完全部 job。同 group 且相邻的进同一波，波内并行度受 parallel 限制。
func execute(ctx context.Context, jobs []job, root string, caps map[capability]capState, parallel int, verbose bool) []jobResult {
	rep := &reporter{tty: isTerminal(os.Stdout)}

	var results []jobResult
	for _, w := range waves(jobs) {
		if len(w) == 1 || parallel <= 1 {
			for _, j := range w {
				rep.begin(j.name)
				res := runOne(ctx, j, root, caps, verbose)
				results = append(results, res)
				rep.done(res)
			}
			continue
		}
		// 并行波不打印开始行：多行会交错，看不出谁在跑。
		results = append(results, runWave(ctx, w, root, caps, parallel, verbose, rep)...)
	}
	return results
}

func runWave(ctx context.Context, w []job, root string, caps map[capability]capState, parallel int, verbose bool, rep *reporter) []jobResult {
	out := make([]jobResult, len(w))
	sem := make(chan struct{}, parallel)
	var wg sync.WaitGroup
	for i, j := range w {
		wg.Add(1)
		go func(i int, j job) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			res := runOne(ctx, j, root, caps, verbose)
			out[i] = res
			rep.done(res)
		}(i, j)
	}
	wg.Wait()
	return out
}

// waves 把 job 切成执行波：相邻同组的合并，其余各自一波。
func waves(jobs []job) [][]job {
	var out [][]job
	for i := 0; i < len(jobs); {
		g := jobs[i].group
		if g == "" {
			out = append(out, jobs[i:i+1])
			i++
			continue
		}
		j := i
		for j < len(jobs) && jobs[j].group == g {
			j++
		}
		out = append(out, jobs[i:j])
		i = j
	}
	return out
}

// runOne 跑一条 job。能力不足直接记跳过，不起进程。
func runOne(ctx context.Context, j job, root string, caps map[capability]capState, verbose bool) jobResult {
	res := jobResult{Name: j.name, Note: j.note}

	for _, need := range j.requires {
		if st, ok := caps[need]; ok && !st.ok {
			res.Status = stSkip
			res.Reason = fmt.Sprintf("本机缺少 %s：%s", need, st.reason)
			return res
		}
	}

	start := time.Now()
	ctx, cancel := context.WithTimeout(ctx, j.timeout)
	defer cancel()

	var all bytes.Buffer
	for _, step := range j.steps {
		cmd := exec.CommandContext(ctx, step[0], step[1:]...)
		cmd.Dir = root
		cmd.Env = childEnv(j)

		var buf bytes.Buffer
		cmd.Stdout = &buf
		cmd.Stderr = &buf
		err := cmd.Run()
		all.Write(buf.Bytes())

		if err != nil {
			res.Status = stFail
			res.DurationMS = time.Since(start).Milliseconds()
			switch {
			case errors.Is(ctx.Err(), context.DeadlineExceeded):
				res.Reason = fmt.Sprintf("超时（%s）", j.timeout)
			case errors.Is(err, exec.ErrNotFound):
				res.Reason = "命令不存在：" + step[0]
			default:
				res.Reason = "退出码非零"
			}
			// optional 的 job 因环境失败不改判定：连不上网不等于代码有问题。
			if j.optional {
				res.Status = stSkip
				res.Reason = "环境未就绪，跳过：" + res.Reason
			}
			res.Output = outputTail(all.String(), verbose)
			return res
		}
		if j.verify == verifyQuiet && strings.TrimSpace(buf.String()) != "" {
			res.Status = stFail
			res.DurationMS = time.Since(start).Milliseconds()
			res.Reason = "有输出即不合格"
			res.Output = outputTail(all.String(), verbose)
			return res
		}
	}

	res.Status = stPass
	res.DurationMS = time.Since(start).Milliseconds()
	return res
}

// childEnv 构造子进程环境。cleanEnv 走白名单，等价于「换一台干净的机器跑」。
func childEnv(j job) []string {
	base := os.Environ()
	if j.cleanEnv {
		base = cleanEnv(base)
	}
	return withEnv(base, j.env)
}

// envKeep 是干净环境里保留的变量：只留让 Go 工具链、git 与临时目录工作的那些。
// 刻意不含 AQUARIUS_* 与各家模型服务的密钥——那正是这一档要暴露的东西。
var envKeep = []string{
	"PATH", "PATHEXT", "ComSpec", "SystemRoot", "SystemDrive", "windir",
	"TEMP", "TMP", "TMPDIR",
	"HOME", "USERPROFILE", "APPDATA", "LOCALAPPDATA", "USERNAME",
	"GOROOT", "GOPATH", "GOCACHE", "GOMODCACHE", "GOPROXY", "GOSUMDB",
	"GOFLAGS", "GOENV", "GOTOOLCHAIN", "CGO_ENABLED", "CC", "CXX",
	"NUMBER_OF_PROCESSORS",
}

// cleanEnv 按白名单过滤。Windows 上环境变量名不区分大小写，比较一律折叠。
func cleanEnv(environ []string) []string {
	out := make([]string, 0, len(envKeep))
	for _, kv := range environ {
		name, _, ok := strings.Cut(kv, "=")
		if !ok {
			continue
		}
		for _, keep := range envKeep {
			if strings.EqualFold(name, keep) {
				out = append(out, kv)
				break
			}
		}
	}
	return out
}

// withEnv 追加 extra，同名键以 extra 为准（避免继承环境里的旧值压住本次设定）。
func withEnv(base, extra []string) []string {
	if len(extra) == 0 {
		return base
	}
	out := make([]string, 0, len(base)+len(extra))
	for _, kv := range base {
		name, _, ok := strings.Cut(kv, "=")
		if !ok {
			continue
		}
		shadowed := false
		for _, e := range extra {
			if en, _, _ := strings.Cut(e, "="); strings.EqualFold(name, en) {
				shadowed = true
				break
			}
		}
		if !shadowed {
			out = append(out, kv)
		}
	}
	return append(out, extra...)
}

// probeCapabilities 只探测本次真要用的能力：fast 档不该为了跑 4 秒的门禁去等 6 秒网络探测。
func probeCapabilities(jobs []job) map[capability]capState {
	caps := map[capability]capState{}
	for _, j := range jobs {
		for _, need := range j.requires {
			if _, done := caps[need]; done {
				continue
			}
			switch need {
			case capCGO:
				caps[need] = probeCGO()
			case capWSL:
				caps[need] = probeWSL()
			case capNetwork:
				caps[need] = probeNetwork()
			}
		}
	}
	return caps
}

func probeCGO() capState {
	if strings.TrimSpace(goEnvValue("CGO_ENABLED")) == "0" {
		return capState{false, "CGO_ENABLED=0"}
	}
	cc := strings.TrimSpace(goEnvValue("CC"))
	if cc == "" {
		return capState{false, "go env CC 为空（-race 需要 C 编译器）"}
	}
	if filepath.IsAbs(cc) {
		if _, err := os.Stat(cc); err != nil {
			return capState{false, "CC 指向的编译器不存在：" + cc}
		}
	} else if _, err := exec.LookPath(cc); err != nil {
		return capState{false, "PATH 上找不到 " + cc}
	}
	if strings.Contains(strings.ToLower(cc), "cygwin") {
		return capState{false, "CC 指向 Cygwin 编译器，Go 拒绝用它构建原生 Windows 程序"}
	}
	return capState{ok: true}
}

func probeWSL() capState {
	if runtime.GOOS != "windows" {
		return capState{ok: true}
	}
	// 输出是 UTF-16，只看退出码：有没有已安装的发行版。
	if err := exec.Command("wsl", "-l", "-q").Run(); err != nil {
		return capState{false, "wsl 不可用或没有已安装的发行版"}
	}
	return capState{ok: true}
}

func probeNetwork() capState {
	for _, addr := range []string{"proxy.golang.org:443", "goproxy.cn:443"} {
		conn, err := net.DialTimeout("tcp", addr, 3*time.Second)
		if err == nil {
			conn.Close()
			return capState{ok: true}
		}
	}
	return capState{false, "连不上模块代理（proxy.golang.org / goproxy.cn）"}
}

var (
	goEnvOnce sync.Once
	goEnvMap  map[string]string
)

func goEnvValue(key string) string {
	goEnvOnce.Do(func() {
		out, err := exec.Command("go", "env").Output()
		if err != nil {
			goEnvMap = map[string]string{}
			return
		}
		goEnvMap = parseGoEnv(string(out))
	})
	return goEnvMap[key]
}

// parseGoEnv 解析 `go env` 的两种输出形态：Windows 上是 `set KEY=VALUE`（cmd 语法），
// 其余平台是 `KEY='VALUE'`。只认一种就会把另一边的每一项都读成空——能力探测因此
// 误报「本机没有 C 工具链」，这就是它当初被写出来的原因。
func parseGoEnv(out string) map[string]string {
	m := map[string]string{}
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "set "))
		if line == "" {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		m[strings.TrimSpace(k)] = strings.Trim(strings.TrimSpace(v), "'")
	}
	return m
}

// outputTail 截取失败输出的末尾。完整输出在并行波里会淹掉摘要，verbose 时例外。
func outputTail(s string, verbose bool) string {
	s = strings.TrimRight(s, "\r\n")
	if verbose || s == "" {
		return s
	}
	lines := strings.Split(s, "\n")
	const keep = 40
	if len(lines) <= keep {
		return s
	}
	return fmt.Sprintf("…（前 %d 行略）\n", len(lines)-keep) + strings.Join(lines[len(lines)-keep:], "\n")
}

// reporter 负责把结果打成一行。并行时多 goroutine 同时写，必须整行加锁。
type reporter struct {
	mu  sync.Mutex
	tty bool
}

// begin 串行执行时先把名字打出来，长 job 才看得出没卡死。
func (r *reporter) begin(name string) {
	if !r.tty {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	fmt.Printf("\r  ..    %-12s", name)
}

func (r *reporter) done(res jobResult) {
	r.mu.Lock()
	defer r.mu.Unlock()

	tag, extra := "PASS", ""
	switch res.Status {
	case stFail:
		tag = "FAIL"
	case stSkip:
		tag = "SKIP"
		extra = res.Reason
	}
	prefix := ""
	if r.tty {
		prefix = "\r"
	}
	fmt.Printf("%s  %-4s  %-12s %6.1fs  %s\n", prefix, tag, res.Name, float64(res.DurationMS)/1000, extra)

	if res.Output == "" {
		return
	}
	for _, line := range strings.Split(res.Output, "\n") {
		fmt.Printf("        %s\n", line)
	}
}

type tally struct {
	passed, failed, skipped int
}

func summarize(rs []jobResult) tally {
	var t tally
	for _, r := range rs {
		switch r.Status {
		case stPass:
			t.passed++
		case stFail:
			t.failed++
		case stSkip:
			t.skipped++
		}
	}
	return t
}

func printSummary(rs []jobResult, t tally, elapsed time.Duration) {
	fmt.Printf("\n  通过 %d  失败 %d  跳过 %d   用时 %.1fs\n", t.passed, t.failed, t.skipped, elapsed.Seconds())
	if t.failed == 0 {
		return
	}
	var failed []string
	for _, r := range rs {
		if r.Status == stFail {
			failed = append(failed, r.Name)
		}
	}
	fmt.Printf("  重跑失败项：go run ./cmd/ci -only=%s\n", strings.Join(failed, ","))
}

func isTerminal(f *os.File) bool {
	st, err := f.Stat()
	if err != nil {
		return false
	}
	return st.Mode()&os.ModeCharDevice != 0
}

func platform() string {
	return runtime.GOOS + "/" + runtime.GOARCH
}
