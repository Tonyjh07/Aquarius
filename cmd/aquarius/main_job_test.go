package main

// main_job_test.go 终端与后台任务：term_exec 执行与拒绝、任务生命周期、
// 杀掉长任务。

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// jobEchoSpec 平台安全的 echo 后台任务参数（job_start 的 command/args）。
func jobEchoSpec(t *testing.T) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		return `{"command":"cmd","args":["/c","echo","job-e2e"]}`
	}
	return `{"command":"/bin/sh","args":["-c","echo job-e2e"]}`
}

// TestRunTermExecE2E M3 验收①：term_exec 经确认链路执行，
// 输出回填给模型（第二次请求可见）、tool 节点 OK 落树。
// 命令带引号（Windows 回归：cmd /c 的原始命令行不得被 argv 转义破坏）。
func TestRunTermExecE2E(t *testing.T) {
	srv, reqs := rawScriptServer(t, [][]string{
		{toolCallData("call_te", "term_exec", `{"command":"echo \"quoted e2e\""}`)},
		{contentData("执行完成")},
	})
	dir := t.TempDir()
	writeConfig(t, dir, srv.URL, "m")

	var out bytes.Buffer
	if code := run([]string{"-data", dir}, strings.NewReader("跑一下 echo\ny\n/quit\n"), &out, io.Discard); code != 0 {
		t.Fatalf("code = %d, out = %q", code, out.String())
	}
	got := out.String()
	for _, want := range []string{"[y/N]", "term_exec", "[tool ok]", "执行完成"} {
		if !strings.Contains(got, want) {
			t.Fatalf("stdout 缺 %q: %q", want, got)
		}
	}
	// 引号语义（只截结果段；工具参数 JSON 预览本身含 \" 转义，不算破坏）：
	// sh -c 吃掉引号（quoted e2e）；cmd /c 保留引号且结果段不出现 \" 残留。
	idx := strings.Index(got, "[tool ok]")
	if idx < 0 {
		t.Fatalf("stdout 缺 [tool ok]: %q", got)
	}
	okSeg := got[idx:]
	if nl := strings.IndexAny(okSeg, "\r\n"); nl >= 0 {
		okSeg = okSeg[:nl]
	}
	if runtime.GOOS == "windows" {
		if !strings.Contains(okSeg, `"quoted e2e"`) || strings.Contains(okSeg, `\"`) {
			t.Fatalf("cmd /c 引号被 argv 转义破坏: %q", okSeg)
		}
	} else if !strings.Contains(okSeg, "quoted e2e") {
		t.Fatalf("结果段缺命令输出: %q", okSeg)
	}
	if reqs.len() != 2 {
		t.Fatalf("llm requests = %d, want 2", reqs.len())
	}
	if !strings.Contains(string(reqs.at(1).body), "quoted e2e") {
		t.Fatalf("第二次请求应回填命令输出: %.400s", reqs.at(1).body)
	}
	c := loadTree(t, dir)
	tp, ok := findToolPart(c)
	if !ok || tp.Result == nil || !tp.Result.OK {
		t.Fatalf("tool 分片 = %+v", tp)
	}
}

// TestRunTermExecRejectE2E strict 下拒绝确认：不执行、OK=false 回填（工具列 D22）。
func TestRunTermExecRejectE2E(t *testing.T) {
	srv, _ := rawScriptServer(t, [][]string{
		{toolCallData("call_te", "term_exec", `{"command":"echo should-not-run"}`)},
		{contentData("好的，不执行。")},
	})
	dir := t.TempDir()
	writeConfig(t, dir, srv.URL, "m")

	var out bytes.Buffer
	if code := run([]string{"-data", dir}, strings.NewReader("跑一下\nn\n/quit\n"), &out, io.Discard); code != 0 {
		t.Fatalf("code = %d, out = %q", code, out.String())
	}
	if !strings.Contains(out.String(), "user denied") {
		t.Fatalf("stdout 缺拒绝痕迹: %q", out.String())
	}
	c := loadTree(t, dir)
	tp, ok := findToolPart(c)
	if !ok || tp.Result == nil || tp.Result.OK || !strings.Contains(tp.Result.Err, "user denied") {
		t.Fatalf("tool 分片 = %+v", tp)
	}
}

// TestRunJobLifecycleE2E M3 验收②：job_start 后台启动（经确认），
// `/jobs` 列表可见、`/jobs logs` 日志可查（日志落盘 ~/.aquarius/jobs/<id>.log）。
func TestRunJobLifecycleE2E(t *testing.T) {
	srv, reqs := rawScriptServer(t, [][]string{
		{toolCallData("call_js", "job_start", jobEchoSpec(t))},
		{contentData("已启动。")},
	})
	dir := t.TempDir()
	writeConfig(t, dir, srv.URL, "m")

	var out bytes.Buffer
	stdin := "启动后台任务\ny\n/jobs\n/jobs logs j001\n/quit\n"
	if code := run([]string{"-data", dir}, strings.NewReader(stdin), &out, io.Discard); code != 0 {
		t.Fatalf("code = %d, out = %q", code, out.String())
	}
	got := out.String()
	for _, want := range []string{"[y/N]", "job_start", "已启动。", "j001"} {
		if !strings.Contains(got, want) {
			t.Fatalf("stdout 缺 %q: %q", want, got)
		}
	}
	if !strings.Contains(got, "job-e2e") {
		t.Fatalf("日志应回显任务输出: %q", got)
	}
	if reqs.len() != 2 {
		t.Fatalf("llm requests = %d, want 2", reqs.len())
	}
	// 任务日志文件落盘（jobs/<id>.log）。
	logPath := filepath.Join(dir, "profiles", "default", "jobs", "j001.log")
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("读日志文件: %v", err)
	}
	if !strings.Contains(string(data), "job-e2e") {
		t.Fatalf("日志文件 = %q", data)
	}
	// 会话树里工具分片 OK（启动成功回填，D95）。
	c := loadTree(t, dir)
	tp, ok := findToolPart(c)
	if !ok || tp.Result == nil || !tp.Result.OK || !strings.Contains(tp.Result.Output, "j001") {
		t.Fatalf("tool 分片 = %+v", tp)
	}
}

// longSleepSpec 平台可靠的长睡眠任务（约 30s；供 kill 测试启动）。
func longSleepSpec(t *testing.T) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		// ping 计时在无交互控制台下可靠等待（timeout 会因 stdin 重定向报错）。
		return `{"command":"ping","args":["-n","30","127.0.0.1"]}`
	}
	return `{"command":"sleep","args":["30"]}`
}

// TestRunJobKillE2E M3：`/jobs kill` 终止后台任务（full-access 免确认启动）。
func TestRunJobKillE2E(t *testing.T) {
	srv, _ := rawScriptServer(t, [][]string{
		{toolCallData("call_js", "job_start", longSleepSpec(t))},
		{contentData("已启动。")},
	})
	dir := t.TempDir()
	writeConfigLevel(t, dir, srv.URL, "m", "full-access")

	var out bytes.Buffer
	stdin := "启动长任务\n/jobs kill j001\n/quit\n"
	if code := run([]string{"-data", dir}, strings.NewReader(stdin), &out, io.Discard); code != 0 {
		t.Fatalf("code = %d, out = %q", code, out.String())
	}
	got := out.String()
	for _, want := range []string{"j001", "已终止 j001"} {
		if !strings.Contains(got, want) {
			t.Fatalf("stdout 缺 %q: %q", want, got)
		}
	}
}
