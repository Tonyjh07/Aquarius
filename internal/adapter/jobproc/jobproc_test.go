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

// TestNewValidation 空日志目录拒绝。
func TestNewValidation(t *testing.T) {
	if _, err := New("  "); err == nil {
		t.Fatal("空目录应报错")
	}
}
