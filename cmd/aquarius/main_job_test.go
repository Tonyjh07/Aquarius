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
	"time"

	"github.com/Tonyjh07/Aquarius/internal/domain/conversation"
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

// findJobInject 找一条带 job 来源的注入节点，返回它所在的会话与正文。
func findJobInject(convs []*conversation.Conversation) (*conversation.Conversation, string) {
	for _, c := range convs {
		for _, m := range c.Snapshot().Nodes {
			if m.Role != conversation.RoleInject || !strings.Contains(m.Source, "job:") {
				continue
			}
			var b strings.Builder
			for _, p := range m.Content {
				b.WriteString(p.Text)
			}
			return c, b.String()
		}
	}
	return nil, ""
}

// TestRunJobLifecycleE2E M3 验收②：job_start 后台启动（经确认），`/jobs` 列表可见、
// `/jobs logs` 日志可查（日志落盘 ~/.aquarius/jobs/<id>.log），任务结束后完成报告
// 落进会话并起一轮（D189）。
//
// 时序不靠 sleep 猜（§10：任务不随退出终止，猜时间既会撞上「子进程还攥着日志句柄」的
// 清理失败，也会撞上「报告还没落地」的空断言）：喂前段 → 轮询会话树等注入出现，
// 再喂后段。/jobs logs 放最后一段，正好也验了「日志在任务结束后仍可查」。
func TestRunJobLifecycleE2E(t *testing.T) {
	srv, reqs := rawScriptServer(t, [][]string{
		{toolCallData("call_js", "job_start", jobEchoSpec(t))},
		{contentData("已启动。")},
		{contentData("收到完成报告。")},
	})
	dir := t.TempDir()
	writeConfig(t, dir, srv.URL, "m")

	var out bytes.Buffer
	pr, pw := io.Pipe()
	code := make(chan int, 1)
	go func() { code <- run([]string{"-data", dir}, pr, &out, io.Discard) }()

	if _, err := io.WriteString(pw, "启动后台任务\ny\n/jobs\n"); err != nil {
		t.Fatalf("写 stdin: %v", err)
	}
	// 等任务真的结束并落成注入：这时收割者已写完日志与台账，Settle 也已销账。
	var report string
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		if _, report = findJobInject(loadAllConversations(t, dir)); report != "" {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if report == "" {
		// 不读 out：run 还在写它（边写边读是数据竞争），等退出后由下面的断言报。
		t.Fatal("没等到任务完成报告（注入没落地）")
	}
	// 报告落地即起一轮（D189）：等这次请求发出，再收尾，断言才有确定的下标。
	for time.Now().Before(deadline) && reqs.len() < 3 {
		time.Sleep(20 * time.Millisecond)
	}
	if _, err := io.WriteString(pw, "/jobs logs j001\n/quit\n"); err != nil {
		t.Fatalf("写 stdin: %v", err)
	}
	_ = pw.Close()
	select {
	case c := <-code:
		if c != 0 {
			t.Fatalf("code = %d, out = %q", c, out.String())
		}
	case <-time.After(30 * time.Second):
		t.Fatal("程序未退出")
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
	// 三次请求：起任务 → 回填启动结果 → 完成报告起的那一轮。
	if reqs.len() != 3 {
		t.Fatalf("llm requests = %d, want 3（起任务 / 回填 / 完成报告）", reqs.len())
	}
	if !strings.Contains(string(reqs.at(1).body), "j001") {
		t.Fatalf("第二次请求应回填启动结果: %.400s", reqs.at(1).body)
	}
	if !strings.Contains(string(reqs.at(2).body), "job-e2e") {
		t.Fatalf("第三次请求应带上完成报告里的日志: %.400s", reqs.at(2).body)
	}
	if !strings.Contains(report, "job-e2e") {
		t.Fatalf("完成报告正文 = %q，应含日志末段", report)
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
