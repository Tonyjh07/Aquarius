package main

import (
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

// profile 是门禁档位。每一档包含前一档的全部 job：档位越低跑得越勤，
// 高的一档用来补低档刻意省掉的重活。
type profile int

const (
	profFast profile = iota
	profDefault
	profFull
)

var profileNames = [...]string{"fast", "default", "full"}

func (p profile) String() string {
	if p < 0 || int(p) >= len(profileNames) {
		return "unknown"
	}
	return profileNames[p]
}

func parseProfile(s string) (profile, bool) {
	for i, n := range profileNames {
		if n == s {
			return profile(i), true
		}
	}
	return 0, false
}

// capability 是 job 需要的外部条件。探测不到时记 skip 而不是 fail：换一台机器
// 或者换一个 runner 就可能有，而「本机没装」不等于「代码坏了」。
type capability string

const (
	capCGO     capability = "cgo"     // C 工具链：-race 走 cgo，Windows 上必须显式指定 CC
	capWSL     capability = "wsl"     // WSL：非 Windows 目标要在 Linux 工具链上构建
	capNetwork capability = "network" // 出网：拉 govulncheck 与依赖
)

// verifier 决定一次 job 算不算过。
type verifier int

const (
	verifyExit  verifier = iota // 退出码为 0
	verifyQuiet                 // 退出码为 0 且没有任何输出（gofmt -l 这种「列表即不合格」）
)

// job 一条门禁。steps 顺序执行，任一步失败即整条失败。
type job struct {
	name     string
	min      profile
	group    string // 相邻且同组的 job 可并行；空 = 自成一组、串行
	requires []capability
	env      []string // 追加在继承环境之上（KEY=VALUE）
	cleanEnv bool     // 只保留白名单环境，用来模拟别人的机器
	optional bool     // 失败降级为 skip：出网这类环境问题不该判红
	timeout  time.Duration
	steps    [][]string
	verify   verifier
	note     string // 一句话说明这条挡什么
}

// defaultJobs 是门禁全表。顺序即执行顺序（同组的会被并行）。
func defaultJobs(root, tmpDir string) []job {
	bin := filepath.Join(tmpDir, "aquarius")
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}
	return []job{
		// ---- fast：pre-push 跑的就是这四条 ----
		{
			name: "fmt", min: profFast, group: "lint",
			timeout: time.Minute,
			steps:   [][]string{{"gofmt", "-l", "."}},
			verify:  verifyQuiet,
			note:    "格式：gofmt 列出的文件即不合格",
		},
		{
			name: "docsync", min: profFast, group: "lint",
			timeout: 2 * time.Minute,
			steps:   [][]string{{"go", "run", "./cmd/docsync", "-check"}},
			note:    "文档头标记与当前基线一致",
		},
		{
			name: "vet", min: profFast, group: "lint",
			timeout: 5 * time.Minute,
			steps:   [][]string{{"go", "vet", "./..."}},
			note:    "静态检查（含测试文件的类型检查）",
		},
		{
			// 放在 default 而不是 fast：vet 已经类型检查过全部非测试代码，pre-push
			// 再链一次二进制只是多等八秒的链接。发版前那次才需要见到产物。
			name: "build", min: profDefault, group: "lint",
			timeout: 5 * time.Minute,
			steps:   [][]string{{"go", "build", "-o", bin, "./cmd/aquarius"}},
			note:    "主程序能构建出单二进制",
		},

		// ---- default：合入前 ----
		{
			name: "test:zh", min: profDefault, group: "test",
			timeout: 15 * time.Minute,
			steps:   [][]string{{"go", "test", "./..."}},
			note:    "全量测试（缺省界面语言）",
		},
		{
			name: "test:en", min: profDefault, group: "test",
			env:     []string{"AQUARIUS_UI_LANG=en"},
			timeout: 15 * time.Minute,
			steps:   [][]string{{"go", "test", "./..."}},
			note:    "全量测试（英文界面语言，挡住只按中文写死的期望值）",
		},
		{
			name: "aqvalidate", min: profDefault,
			timeout: 15 * time.Minute,
			steps:   [][]string{{"go", "test", "-tags", "aqvalidate", "./..."}},
			note:    "整树自检构建：每次变更后校验会话树不变量",
		},
		{
			name: "property", min: profDefault,
			timeout: 10 * time.Minute,
			steps: [][]string{
				{"go", "test", "./internal/domain/...", "-run", "TestProperty", "-count=50"},
			},
			note: "领域性质测试加密跑（50 轮随机序列）",
		},
		{
			name: "envclean", min: profDefault, cleanEnv: true,
			timeout: 10 * time.Minute,
			steps: [][]string{
				{"go", "test", "./cmd/aquarius/...", "./internal/adapter/probe/...",
					"./internal/plugin/...", "./internal/app/..."},
			},
			note: "清空本机环境变量后重跑组装与探针：挡「测试偷偷依赖本机配好的密钥」",
		},

		// ---- full：发版前 ----
		{
			name: "race", min: profFull, requires: []capability{capCGO},
			timeout: 20 * time.Minute,
			steps:   [][]string{{"go", "test", "-race", "./..."}},
			note:    "竞态检测（需要 C 工具链）",
		},
		{
			name: "linuxgate", min: profFull, requires: []capability{capWSL},
			timeout: 20 * time.Minute,
			steps:   [][]string{{"go", "run", "./cmd/linuxgate"}},
			note:    "非 Windows 目标：Linux 工具链上的 build + vet",
		},
		{
			name: "vuln", min: profFull, requires: []capability{capNetwork}, optional: true,
			timeout: 15 * time.Minute,
			steps: [][]string{
				{"go", "run", "golang.org/x/vuln/cmd/govulncheck@latest", "./..."},
			},
			note: "依赖漏洞体检（按调用栈判可达；连不上网记跳过，不算失败）",
		},
	}
}

// goVersion 只取一次：每个 job 的输出里都可能要它，反复起进程不值。
var (
	goVersionOnce sync.Once
	goVersionVal  string
)

func goVersion() string {
	goVersionOnce.Do(func() {
		out, err := exec.Command("go", "env", "GOVERSION").Output()
		if err != nil {
			goVersionVal = "go?"
			return
		}
		goVersionVal = strings.TrimSpace(string(out))
	})
	return goVersionVal
}
