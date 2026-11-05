// Command ci 在本机跑门禁，也就是「CI」——只是触发者和执行者都是开发者自己。
//
// 为什么是 Go 程序而不是 makefile 或一堆 .sh：本仓不含 shell 脚本（行尾、编码、
// Windows 无 bash 三件事都指向同一个结论），把自动化收进 .go 是既有做法
// （cmd/docsync 刷文档标记、cmd/linuxgate 跑非 Windows 门禁、cmd/iconify 生成图标）。
//
// 档位按「多久跑一次」切：fast 给 pre-push（十几秒），default 是合入前（分钟级），
// full 是发版前（含 -race、非 Windows 门禁、依赖漏洞体检）。后一档包含前一档。
//
// 迁移到托管 runner 时改动面应当只有 workflow 文件：那边调
// `go run ./cmd/ci --profile=full --json=ci-report.json`，其余不动。为此 job 定义里
// 不出现任何托管平台的概念（matrix、artifact、runner label 都不进门），机器差异
// 只经 requires 声明，由执行器探测后决定跑还是跳过。
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	os.Exit(run(ctx, os.Args[1:]))
}

// options 一次运行的参数。
type options struct {
	profile  profile
	only     string
	list     bool
	jobs     int
	jsonPath string
	verbose  bool
}

func usage(fs *flag.FlagSet) {
	fmt.Fprintf(fs.Output(), `用法：go run ./cmd/ci [选项]

档位（后一档包含前一档）：
  fast     十几秒：gofmt / 文档标记 / vet。pre-push hook 调这一档。
  default  合入前：另加构建、两种界面语言的全量测试、整树自检、性质测试加密跑、
           干净环境复查。
  full     发版前：另加 -race、非 Windows 门禁、依赖漏洞体检。

选项：
`)
	fs.PrintDefaults()
}

func run(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("ci", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.Usage = func() { usage(fs) }

	profileName := fs.String("profile", "default", "档位：fast | default | full")
	only := fs.String("only", "", "只跑这些 job（逗号分隔），忽略档位")
	list := fs.Bool("list", false, "列出当前档位的 job 后退出")
	jobs := fs.Int("jobs", 0, "并行度（0 = 自动：fast 档各 job 并行，其余串行）")
	jsonPath := fs.String("json", "", "额外把本次结果写成 JSON 到该路径")
	verbose := fs.Bool("v", false, "失败时打印完整输出（缺省只打印末尾若干行）")
	installHooks := fs.Bool("install-hooks", false, "安装 pre-push hook（推送前跑 fast 档）")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	root, err := repoRoot()
	if err != nil {
		fmt.Fprintf(os.Stderr, "找不到仓库根：%v\n", err)
		return 2
	}

	if *installHooks {
		if err := installPrePush(root); err != nil {
			fmt.Fprintf(os.Stderr, "安装 hook 失败：%v\n", err)
			return 1
		}
		return 0
	}

	prof, ok := parseProfile(*profileName)
	if !ok {
		fmt.Fprintf(os.Stderr, "未知档位 %q；可选 fast / default / full\n", *profileName)
		return 2
	}

	return runProfile(ctx, root, options{
		profile:  prof,
		only:     *only,
		list:     *list,
		jobs:     *jobs,
		jsonPath: *jsonPath,
		verbose:  *verbose,
	})
}

func runProfile(ctx context.Context, root string, opt options) int {
	// 工作区状态必须在跑之前取：测试本身可能写出文件，跑完再看就把自己的产物算进去了。
	sha, dirty, err := gitHead(root)
	if err != nil {
		fmt.Fprintf(os.Stderr, "读取 git 状态失败：%v（门禁仍会跑，但不记录证据）\n", err)
	}

	tmp, err := os.MkdirTemp("", "aquarius-ci-")
	if err != nil {
		fmt.Fprintf(os.Stderr, "创建临时目录失败：%v\n", err)
		return 2
	}
	defer os.RemoveAll(tmp)

	all := defaultJobs(root, tmp)
	if opt.list {
		for _, j := range selectJobs(all, opt.profile, opt.only) {
			fmt.Printf("%-10s %-8s %s\n", j.name, j.min, j.note)
		}
		return 0
	}

	selected := selectJobs(all, opt.profile, opt.only)
	if len(selected) == 0 {
		fmt.Fprintf(os.Stderr, "没有可跑的 job（档位 %s，-only=%q）\n", opt.profile, opt.only)
		return 2
	}

	fmt.Printf("Aquarius 门禁  %s  %d 项  %s  %s\n\n", opt.profile, len(selected), platform(), goVersion())
	printLastRuns(root, opt.profile, sha)

	start := time.Now()
	results := execute(ctx, selected, root, probeCapabilities(selected),
		parallelism(opt.profile, opt.jobs), opt.verbose)
	elapsed := time.Since(start)

	sum := summarize(results)
	printSummary(results, sum, elapsed)

	if sha == "" {
		fmt.Println("未取到提交号，本次结果不写入证据文件")
		return exitCode(sum)
	}
	ev := evidence{
		SHA: sha, Dirty: dirty, Profile: opt.profile.String(),
		StartedAt: start, DurationMS: elapsed.Milliseconds(),
		GoVersion: goVersion(), Platform: platform(),
		Passed: sum.passed, Failed: sum.failed, Skipped: sum.skipped,
		Jobs: results,
	}
	switch {
	case opt.only != "":
		// 只跑几项不构成「这一档通过」的证据；写进去会把上一轮完整的结果覆盖掉，
		// 之后 pre-push 就会误报「上次 default 通过」。
		fmt.Println("按 -only 运行，不入档位证据")
	default:
		if err := writeRecord(root, ev); err != nil {
			fmt.Fprintf(os.Stderr, "写证据失败：%v\n", err)
		} else {
			fmt.Printf("证据已写入 %s\n", relTo(root, recordPath(root)))
		}
	}
	if opt.jsonPath != "" {
		if err := writeJSON(opt.jsonPath, ev); err != nil {
			fmt.Fprintf(os.Stderr, "写 JSON 失败：%v\n", err)
		}
	}
	return exitCode(sum)
}

func exitCode(t tally) int {
	if t.failed > 0 {
		return 1
	}
	return 0
}

// selectJobs 按档位与 -only 过滤，保持声明顺序。
func selectJobs(all []job, prof profile, only string) []job {
	want := map[string]bool{}
	for _, n := range strings.Split(only, ",") {
		if n = strings.TrimSpace(n); n != "" {
			want[n] = true
		}
	}
	var out []job
	for _, j := range all {
		if len(want) > 0 {
			if want[j.name] {
				out = append(out, j)
			}
			continue
		}
		if j.min <= prof {
			out = append(out, j)
		}
	}
	return out
}

// parallelism 给缺省并行度：fast 档全是秒级 job，并行能把它压到十几秒；
// 其余档的 job 动辄跑满全部核心，再并行只会互相抢。
func parallelism(prof profile, flagValue int) int {
	if flagValue > 0 {
		return flagValue
	}
	if prof == profFast {
		return 4
	}
	return 1
}

// printLastRuns 提示上一轮其他档位的结果，避免「我记得跑过」与「跑的是旧提交」混为一谈。
// fast 档（pre-push）尤其需要：它挡不住 default 那一层，用户得知道有多久没跑过。
func printLastRuns(root string, prof profile, head string) {
	if prof != profFast {
		return
	}
	rec, err := readRecord(root)
	if err != nil || len(rec) == 0 {
		return
	}
	var names []string
	for name := range rec {
		if name != prof.String() {
			names = append(names, name)
		}
	}
	if len(names) == 0 {
		return
	}
	sort.Strings(names)
	for _, name := range names {
		ev := rec[name]
		stale := ""
		if head != "" && ev.SHA != head {
			stale = "（不是当前提交，之后的改动未覆盖）"
		}
		verdict := "通过"
		if ev.Failed > 0 {
			verdict = "失败"
		}
		fmt.Printf("  上次 %s：%s %s @%s，%s%s\n",
			name, ev.StartedAt.Format("2006-01-02 15:04"), verdict,
			shortSHA(ev.SHA), humanSince(time.Since(ev.StartedAt)), stale)
	}
	fmt.Println()
}

func shortSHA(s string) string {
	if len(s) > 7 {
		return s[:7]
	}
	return s
}

func humanSince(d time.Duration) string {
	switch {
	case d < time.Minute:
		return "刚刚"
	case d < time.Hour:
		return fmt.Sprintf("%d 分钟前", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%d 小时前", int(d.Hours()))
	default:
		return fmt.Sprintf("%d 天前", int(d.Hours()/24))
	}
}

func relTo(root, p string) string {
	if r, err := filepath.Rel(root, p); err == nil {
		return r
	}
	return p
}
