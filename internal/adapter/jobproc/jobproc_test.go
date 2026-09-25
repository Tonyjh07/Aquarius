package jobproc

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/Tonyjh07/Aquarius/internal/port"
)

// TestJobHelperProcess 假任务进程（helper 模式，不依赖 shell，跨平台）：
// AQUARIUS_JOB_HELPER=1 时按 -test.run 后的模式执行，否则跳过。
//
//	echo  → 打印余下参数（空格连接）后退出 0
//	fail  → 打印 failing 后以 3 退出
//	lines → 打印 N 行（line-1..line-N）后退出 0
//	fill  → 输出约 N 字节（供日志窗口/输出上限测试）后退出 0
//	sleep → 睡 30s（供 Kill/Timeout 测试）
func TestJobHelperProcess(t *testing.T) {
	if os.Getenv("AQUARIUS_JOB_HELPER") != "1" {
		t.Skip("非 helper 进程运行")
	}
	args := flag.Args()
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "helper: 缺少模式")
		os.Exit(2)
	}
	switch args[0] {
	case "echo":
		fmt.Println(strings.Join(args[1:], " "))
		os.Exit(0)
	case "fail":
		fmt.Println("failing")
		os.Exit(3)
	case "lines":
		n := 0
		fmt.Sscanf(args[1], "%d", &n)
		for i := 1; i <= n; i++ {
			fmt.Printf("line-%d\n", i)
		}
		os.Exit(0)
	case "fill":
		total := 1 << 20
		fmt.Sscanf(args[1], "%d", &total)
		block := strings.Repeat("y", 4096)
		for written := 0; written < total; {
			n := len(block)
			if remain := total - written; remain < n {
				n = remain
			}
			fmt.Print(block[:n])
			written += n
		}
		os.Exit(0)
	case "sleep":
		time.Sleep(30 * time.Second)
		os.Exit(0)
	default:
		fmt.Fprintln(os.Stderr, "helper: 未知模式", args[0])
		os.Exit(2)
	}
}

// helperSpec 构造指向本测试二进制的 JobSpec（绝对路径 + helper 环境开关）。
func helperSpec(t *testing.T, workDir string, mode string, extra ...string) port.JobSpec {
	t.Helper()
	exe, err := filepath.Abs(os.Args[0])
	if err != nil {
		t.Fatalf("abs: %v", err)
	}
	return port.JobSpec{
		Command: exe,
		Args:    append([]string{"-test.run=TestJobHelperProcess", mode}, extra...),
		Env:     map[string]string{"AQUARIUS_JOB_HELPER": "1"},
		WorkDir: workDir,
	}
}

// waitFor 轮询条件成立（避免硬 sleep）。
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("等待 %s 超时", what)
}

// TestRunEcho 同步执行：合并输出、正常退出 OK=true。
func TestRunEcho(t *testing.T) {
	m, err := New(t.TempDir())
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	res, err := m.Run(context.Background(), helperSpec(t, t.TempDir(), "echo", "hello", "world"))
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if !res.OK || !strings.Contains(res.Output, "hello world") {
		t.Fatalf("res = %+v", res)
	}
}

// TestRunFailExitCode 非零退出 → OK=false + 退出码与输出（交模型自行纠正，§10）。
func TestRunFailExitCode(t *testing.T) {
	m, err := New(t.TempDir())
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	res, err := m.Run(context.Background(), helperSpec(t, t.TempDir(), "fail"))
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if res.OK || !strings.Contains(res.Err, "退出码 3") {
		t.Fatalf("res = %+v, want 退出码 3", res)
	}
	if !strings.Contains(res.Output, "failing") {
		t.Fatalf("输出未带回: %+v", res)
	}
}

// TestRunSpawnFailure 启动失败（命令不存在）→ OK=false（模型可纠正，非装配级错误）。
func TestRunSpawnFailure(t *testing.T) {
	m, err := New(t.TempDir())
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	res, err := m.Run(context.Background(), port.JobSpec{Command: "aquarius-definitely-missing-binary-xyz"})
	if err != nil {
		t.Fatalf("启动失败应转 OK=false 而非 error: %v", err)
	}
	if res.OK || !strings.Contains(res.Err, "启动") {
		t.Fatalf("res = %+v", res)
	}
}

// TestRunEmptyCommand 空 command → OK=false。
func TestRunEmptyCommand(t *testing.T) {
	m, err := New(t.TempDir())
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	res, err := m.Run(context.Background(), port.JobSpec{})
	if err != nil || res.OK {
		t.Fatalf("res = %+v, err = %v", res, err)
	}
}

// TestRunCancelCtx ctx 取消 → 终止进程并上抛 context.Canceled（装配级）。
func TestRunCancelCtx(t *testing.T) {
	m, err := New(t.TempDir())
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(150 * time.Millisecond)
		cancel()
	}()
	_, err = m.Run(ctx, helperSpec(t, t.TempDir(), "sleep"))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}

// TestStartLifecycle 后台启动：日志落盘（头行/输出/退出标记）、状态收敛 done、List 可见。
func TestStartLifecycle(t *testing.T) {
	dir := t.TempDir()
	m, err := New(dir)
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	job, err := m.Start(context.Background(), helperSpec(t, t.TempDir(), "echo", "bg", "ok"))
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if job.ID == "" || job.PID <= 0 {
		t.Fatalf("job = %+v", job)
	}
	waitFor(t, "任务完成", func() bool {
		j, serr := m.Status(context.Background(), job.ID)
		return serr == nil && j.Status == port.JobDone
	})
	j, err := m.Status(context.Background(), job.ID)
	if err != nil || j.ExitCode != 0 || j.EndedAt.IsZero() {
		t.Fatalf("status = %+v, err = %v", j, err)
	}
	list, err := m.List(context.Background())
	if err != nil || len(list) != 1 || list[0].ID != job.ID {
		t.Fatalf("list = %+v, err = %v", list, err)
	}
	// 日志：命令头 + 输出 + 退出标记。
	logs, err := m.Logs(context.Background(), job.ID, 0)
	if err != nil {
		t.Fatalf("logs: %v", err)
	}
	for _, want := range []string{"$ ", "bg ok", "[exit code=0 status=done]"} {
		if !strings.Contains(logs, want) {
			t.Fatalf("日志缺 %q: %q", want, logs)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, string(job.ID)+".log")); err != nil {
		t.Fatalf("日志文件未落盘: %v", err)
	}
}

// TestStartKill 显式终止：状态 killed、重复 Kill/未知任务报错。
func TestStartKill(t *testing.T) {
	m, err := New(t.TempDir())
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	job, err := m.Start(context.Background(), helperSpec(t, t.TempDir(), "sleep"))
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if err := m.Kill(context.Background(), job.ID); err != nil {
		t.Fatalf("kill: %v", err)
	}
	waitFor(t, "任务被终止", func() bool {
		j, serr := m.Status(context.Background(), job.ID)
		return serr == nil && j.Status == port.JobKilled
	})
	if err := m.Kill(context.Background(), job.ID); err == nil || !strings.Contains(err.Error(), "已结束") {
		t.Fatalf("重复 kill err = %v, want 已结束", err)
	}
	if _, err := m.Status(context.Background(), "j999"); err == nil || !strings.Contains(err.Error(), "没有任务") {
		t.Fatalf("未知任务 err = %v", err)
	}
	if _, err := m.Logs(context.Background(), "j999", 0); err == nil {
		t.Fatal("未知任务取日志应报错")
	}
}

// treeSpec 构造"父包装 + 子进程写标记"的任务（§14 M3 遗留：进程树终止的自动化验证）：
// 父 = 被 Kill 的直接进程（cmd/sh），子 = 每秒追加 marker 的进程（ping / 内层 sh 循环）。
// 若 killTree 只杀父进程，孤儿子进程会继续追加 → marker 增长 → 测试判失败。
func treeSpec(mark string) port.JobSpec {
	if runtime.GOOS == "windows" {
		return port.JobSpec{
			Command: "cmd",
			Args:    []string{"/c", `ping -n 60 127.0.0.1 >> "` + mark + `"`},
		}
	}
	return port.JobSpec{
		Command: "/bin/sh",
		Args:    []string{"-c", `sh -c 'while :; do echo x >> "` + mark + `"; sleep 1; done' & wait`},
	}
}

// TestKillTerminatesProcessTree 杀整棵进程树：Kill 后孤儿子进程也必须终止
// （以 marker 是否停增判定；直接进程死亡由 TestStartKill 覆盖）。
func TestKillTerminatesProcessTree(t *testing.T) {
	m, err := New(t.TempDir())
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	mark := filepath.Join(t.TempDir(), "mark.txt")
	job, err := m.Start(context.Background(), treeSpec(mark))
	if err != nil {
		t.Fatalf("start: %v", err)
	}

	// 子进程开始写标记（首写 ≤ 数秒；轮询至多 8s）。
	waitFor(t, "子进程写入标记", func() bool {
		st, serr := os.Stat(mark)
		return serr == nil && st.Size() > 0
	})

	if err := m.Kill(context.Background(), job.ID); err != nil {
		t.Fatalf("kill: %v", err)
	}
	waitFor(t, "任务被终止", func() bool {
		j, serr := m.Status(context.Background(), job.ID)
		return serr == nil && j.Status == port.JobKilled
	})
	// 快照后观察 3s（≥2 个写入周期）：仍在增长 = 孤儿子进程存活。
	st1, err := os.Stat(mark)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	time.Sleep(3 * time.Second)
	st2, err := os.Stat(mark)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if st2.Size() != st1.Size() {
		t.Fatalf("Kill 后 marker 仍增长 %d→%d：进程树未杀净（孤儿子进程存活）",
			st1.Size(), st2.Size())
	}
}

// TestStartTimeout Timeout 到点自动终止：状态 failed。
func TestStartTimeout(t *testing.T) {
	m, err := New(t.TempDir())
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	spec := helperSpec(t, t.TempDir(), "sleep")
	spec.Timeout = 300 * time.Millisecond
	job, err := m.Start(context.Background(), spec)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	waitFor(t, "超时终止", func() bool {
		j, serr := m.Status(context.Background(), job.ID)
		return serr == nil && j.Status == port.JobFailed
	})
}

// TestStartSkipsExistingLog 序号遇旧日志跳号（跨进程重启不撞号）。
func TestStartSkipsExistingLog(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "j001.log"), []byte("old"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	m, err := New(dir)
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	job, err := m.Start(context.Background(), helperSpec(t, t.TempDir(), "echo", "hi"))
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if job.ID != "j002" {
		t.Fatalf("id = %s, want j002（跳过占用的 j001）", job.ID)
	}
	waitFor(t, "任务完成", func() bool {
		j, serr := m.Status(context.Background(), job.ID)
		return serr == nil && j.Status == port.JobDone
	})
}

// TestLogsTail Logs(id, tail>0) 只回末尾 N 行（末行是退出标记）。
func TestLogsTail(t *testing.T) {
	m, err := New(t.TempDir())
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	job, err := m.Start(context.Background(), helperSpec(t, t.TempDir(), "lines", "5"))
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	waitFor(t, "任务完成", func() bool {
		j, serr := m.Status(context.Background(), job.ID)
		return serr == nil && j.Status == port.JobDone
	})
	logs, err := m.Logs(context.Background(), job.ID, 2)
	if err != nil {
		t.Fatalf("logs: %v", err)
	}
	if !strings.Contains(logs, "line-5") || !strings.Contains(logs, "[exit code=0") {
		t.Fatalf("末尾 2 行缺内容: %q", logs)
	}
	if strings.Contains(logs, "line-3") {
		t.Fatalf("tail 裁剪失效: %q", logs)
	}
}

// TestRunCmdQuoting Windows：cmd /c 的含引号命令不被 Go 的 argv 转义破坏
// （`"`→`\"`，cmd.exe 不认反斜杠转义）——setupProc 以 SysProcAttr.CmdLine 直传。
// 非 Windows 跳过（sh -c 走 argv，无此问题）。
func TestRunCmdQuoting(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("仅 Windows 有 cmd.exe 转义问题")
	}
	m, err := New(t.TempDir())
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	res, err := m.Run(context.Background(), port.JobSpec{
		Command: "cmd",
		Args:    []string{"/c", `echo "a b"`},
	})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if !strings.Contains(res.Output, `"a b"`) {
		t.Fatalf("引号未保留: %q（期望输出含 \"a b\"）", res.Output)
	}
	if strings.Contains(res.Output, `\"`) {
		t.Fatalf("出现 argv 转义残留 \": %q", res.Output)
	}
}

// TestRunCancelUnblocksGrandchildren ctx 取消后 Run 不被孙进程持有的输出管道拖住
// （WaitDelay 兜底；取消只杀直接子进程、孙进程残留是 v1 已知行为，见包注释）。
func TestRunCancelUnblocksGrandchildren(t *testing.T) {
	m, err := New(t.TempDir())
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(300 * time.Millisecond)
		cancel()
	}()
	// 直接子进程派生孙进程（后台/等待形态），两者都继承输出管道。
	var spec port.JobSpec
	if runtime.GOOS == "windows" {
		spec = port.JobSpec{Command: "cmd", Args: []string{"/c", "ping -n 60 127.0.0.1"}}
	} else {
		spec = port.JobSpec{Command: "/bin/sh", Args: []string{"-c", "sleep 60 & wait"}}
	}
	start := time.Now()
	if _, err := m.Run(ctx, spec); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if elapsed := time.Since(start); elapsed > 8*time.Second {
		t.Fatalf("取消后阻塞 %v（WaitDelay=%s 未生效）", elapsed, waitDelay)
	}
}

// TestRunPipeHeldAfterExit 子进程正常退出但孙进程持有输出管道：
// Run 在 WaitDelay 内按退出状态结算，不等孙进程自然退出（仅 unix；windows 形态
// 由 cmd 的 start /b 行为决定，交由取消路径覆盖）。
func TestRunPipeHeldAfterExit(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix shell 后台形态")
	}
	m, err := New(t.TempDir())
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	start := time.Now()
	res, err := m.Run(context.Background(), port.JobSpec{
		Command: "/bin/sh", Args: []string{"-c", "sleep 6 &"},
	})
	if err != nil || !res.OK {
		t.Fatalf("res = %+v, err = %v", res, err)
	}
	if elapsed := time.Since(start); elapsed > 4*time.Second {
		t.Fatalf("被孙进程拖住 %v（WaitDelay 未生效）", elapsed)
	}
}

// TestRunOutputCap 输出超限时按上限采集并标注（防刷屏命令吃内存）。
func TestRunOutputCap(t *testing.T) {
	m, err := New(t.TempDir())
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	res, err := m.Run(context.Background(),
		helperSpec(t, t.TempDir(), "fill", "5242880")) // 5MB > 4MB 上限
	if err != nil || !res.OK {
		t.Fatalf("res = %+v, err = %v", res, err)
	}
	if !strings.Contains(res.Output, "已截断") {
		t.Fatalf("缺截断标注（输出 %d 字节）", len(res.Output))
	}
	if len(res.Output) > maxRunOutput+120 {
		t.Fatalf("采集长度 = %d，超上限 %d", len(res.Output), maxRunOutput)
	}
}

// TestStartSurvivesCallerCancel §10：后台任务不随调用方 ctx 取消——
// 启动后取消 ctx，任务照常跑完、状态收敛、日志可查。
func TestStartSurvivesCallerCancel(t *testing.T) {
	m, err := New(t.TempDir())
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	job, err := m.Start(ctx, helperSpec(t, t.TempDir(), "echo", "still-alive"))
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	cancel() // 启动后取消：不得影响任务
	waitFor(t, "任务完成", func() bool {
		j, serr := m.Status(context.Background(), job.ID)
		return serr == nil && j.Status == port.JobDone
	})
	logs, err := m.Logs(context.Background(), job.ID, 0)
	if err != nil || !strings.Contains(logs, "still-alive") {
		t.Fatalf("logs = %q, err = %v", logs, err)
	}
}

// TestLogsWindow 超出窗口的大日志只读末尾 1MB 并带前缀标注（含 tail 切行）。
func TestLogsWindow(t *testing.T) {
	m, err := New(t.TempDir())
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	job, err := m.Start(context.Background(),
		helperSpec(t, t.TempDir(), "fill", "2097152")) // 2MB > 1MB 窗口
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	waitFor(t, "任务完成", func() bool {
		j, serr := m.Status(context.Background(), job.ID)
		return serr == nil && j.Status == port.JobDone
	})
	logs, err := m.Logs(context.Background(), job.ID, 3)
	if err != nil {
		t.Fatalf("logs: %v", err)
	}
	if !strings.Contains(logs, "日志超出窗口") {
		t.Fatalf("缺窗口标注: %.80q", logs)
	}
	if strings.Contains(logs, "-test.run=") {
		t.Fatal("头部命令行应在窗口之外被裁掉")
	}
	// tail=0：整窗口 + 标注。
	full, err := m.Logs(context.Background(), job.ID, 0)
	if err != nil || !strings.Contains(full, "日志超出窗口") {
		t.Fatalf("full = %.80q, err = %v", full, err)
	}
	if len(full) > logWindow+120 {
		t.Fatalf("窗口读取长度 = %d，超 %d", len(full), logWindow)
	}
}

// TestNewValidation 空日志目录拒绝。
func TestNewValidation(t *testing.T) {
	if _, err := New("  "); err == nil {
		t.Fatal("空目录应报错")
	}
}
